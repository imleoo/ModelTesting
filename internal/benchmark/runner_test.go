package benchmark_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/leoobai/modeltestbed/internal/benchmark"
	"github.com/leoobai/modeltestbed/internal/model"
)

func mockStreamingGateway() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flush support", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")

		write := func(delta map[string]any, finish any, usage map[string]any) {
			chunk := map[string]any{
				"id": "c1", "object": "chat.completion.chunk", "created": 1700000000, "model": "mock",
				"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
			}
			if usage != nil {
				chunk["usage"] = usage
			}
			raw, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", raw)
			flusher.Flush()
			time.Sleep(2 * time.Millisecond)
		}

		write(map[string]any{"role": "assistant", "content": "a"}, nil, nil)
		write(map[string]any{"content": "b"}, nil, nil)
		write(map[string]any{"content": "c"}, nil, nil)
		write(map[string]any{}, "stop", map[string]any{
			"prompt_tokens": 500, "completion_tokens": 3,
			"prompt_tokens_details": map[string]any{"cached_tokens": 200},
		})
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

func TestRun_EndToEndAgainstMockGateway(t *testing.T) {
	srv := httptest.NewServer(mockStreamingGateway())
	defer srv.Close()

	params, err := benchmark.DefaultParams(6)
	if err != nil {
		t.Fatalf("DefaultParams: %v", err)
	}
	// 测试用短爬坡窗口 + 压缩会话内轮次间隔/轮数，避免单元测试实际等待接近
	// 真实 6.1 节参数下一个会话可能耗时数百秒（turn-interval p95=86s × 数十轮）。
	// 压测判定逻辑本身（吞吐/TTFT/TPOT/缓存命中率的计算与 6.2 规则）不依赖
	// 这两个分布的具体取值，用小数值验证逻辑正确性是合理的简化。
	params.RampDurationSeconds = 1
	params.ArrivalRateStart = 20
	params.ArrivalRateEnd = 20
	fastRounds, err := benchmark.NewPercentileSampler("num-rounds-test", 1,
		[]benchmark.PercentilePoint{{P: 0.5, Value: 2}, {P: 0.95, Value: 3}}, 4)
	if err != nil {
		t.Fatalf("build fastRounds sampler: %v", err)
	}
	fastTurnInterval, err := benchmark.NewPercentileSampler("turn-interval-test", 0.001,
		[]benchmark.PercentilePoint{{P: 0.5, Value: 0.005}, {P: 0.95, Value: 0.01}}, 0.02)
	if err != nil {
		t.Fatalf("build fastTurnInterval sampler: %v", err)
	}
	params.NumRounds = fastRounds
	params.TurnIntervalSeconds = fastTurnInterval

	var logBuf fakeWriter
	result, err := benchmark.Run(context.Background(), benchmark.RunConfig{
		Params:         params,
		BaseURL:        srv.URL,
		APIKey:         "test-key",
		ModelKey:       "mock-model",
		RequestTimeout: 5 * time.Second,
		RawCommand:     "go test",
		ToolVersion:    "self-built-v0",
		DatasetVersion: "synthetic-v1",
		Seed:           42,
		LogWriter:      &logBuf,
		LogRef:         "test.log",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Run.TotalRequests == 0 {
		t.Fatal("expected at least some requests to be recorded")
	}
	if result.Run.DurationS <= 0 {
		t.Fatal("expected positive duration")
	}
	if result.Run.RawParamsJSON == "" || result.Run.RawParamsJSON == "{}" {
		t.Fatalf("expected non-trivial raw_params_json, got %q", result.Run.RawParamsJSON)
	}
	if logBuf.String() == "" {
		t.Fatal("expected non-empty raw log output")
	}

	byName := map[string]model.BenchmarkMetric{}
	for _, m := range result.Metrics {
		if m.Scope == "overall" {
			byName[m.Name] = m
		}
	}

	for _, name := range []string{"total_requests", "duration", "throughput_req_s", "ttft", "tpot", "latency", "itl", "cache_hit_rate"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("missing expected overall metric %q", name)
		}
	}

	throughput := byName["throughput_req_s"]
	if throughput.BaselineVerdict != model.BaselineOK {
		t.Errorf("expected throughput to be OK against 0.6 req/s baseline with 20 req/s arrival rate, got verdict=%s avg=%v",
			throughput.BaselineVerdict, throughput.Avg)
	}

	cacheHit := byName["cache_hit_rate"]
	// mock 网关固定返回 cached_tokens=200/prompt_tokens=500=0.4，低于 0.6 基线但在
	// MaxDegradeRatio(2x) 内，应判 SAME_ORDER（除非全部落在 Round 0 被剔除，
	// 那种情况下才会是 NOT_OBSERVABLE，两种都不代表 bug，这里只检查没有非预期值）。
	if cacheHit.BaselineVerdict != model.BaselineSameOrder && cacheHit.BaselineVerdict != model.BaselineNotObservable {
		t.Errorf("unexpected cache_hit_rate verdict=%s avg=%v", cacheHit.BaselineVerdict, cacheHit.Avg)
	}

	if byName["latency"].BaselineVerdict != model.BaselineManualReview {
		t.Errorf("expected latency verdict MANUAL_REVIEW (no PDF baseline), got %s", byName["latency"].BaselineVerdict)
	}
	if byName["itl"].BaselineVerdict != model.BaselineManualReview {
		t.Errorf("expected itl verdict MANUAL_REVIEW (no PDF baseline), got %s", byName["itl"].BaselineVerdict)
	}

	ttft := byName["ttft"]
	if ttft.P50 <= 0 {
		t.Errorf("expected positive ttft p50, got %v", ttft.P50)
	}
	if ttft.BaselineVerdict != model.BaselineOK {
		t.Errorf("expected ttft OK against 15s baseline with a fast mock server, got verdict=%s p50=%v", ttft.BaselineVerdict, ttft.P50)
	}

	if result.Run.RawStdoutRef != "test.log" {
		t.Errorf("expected RawStdoutRef to carry RunConfig.LogRef, got %q", result.Run.RawStdoutRef)
	}
}

// TestRun_RequiresLogRef 验证 6.3 节"stdout/stderr 全量日志引用"要求在入口
// 就被强制：留空 LogRef 时 Run 必须直接报错，而不是悄悄产出一个
// RawStdoutRef=="" 的、不满足留痕要求的 BenchmarkRun。
func TestRun_RequiresLogRef(t *testing.T) {
	params, err := benchmark.DefaultParams(1)
	if err != nil {
		t.Fatalf("DefaultParams: %v", err)
	}
	_, err = benchmark.Run(context.Background(), benchmark.RunConfig{
		Params:   params,
		BaseURL:  "http://example.invalid",
		ModelKey: "mock-model",
		Seed:     1,
		LogRef:   "",
	})
	if err == nil {
		t.Fatal("expected error when RunConfig.LogRef is empty")
	}
}

func TestSampler_MonotonicAndBounded(t *testing.T) {
	s, err := benchmark.NewPercentileSampler("t", 1,
		[]benchmark.PercentilePoint{{P: 0.5, Value: 100}, {P: 0.95, Value: 1000}}, 3000)
	if err != nil {
		t.Fatalf("NewPercentileSampler: %v", err)
	}
	rng := rand.New(rand.NewSource(1))
	min, max := 1e18, 0.0
	for range 2000 {
		v := s.Sample(rng)
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	if min < 1 {
		t.Errorf("sampled value below floor: %v", min)
	}
	if max > 3000 {
		t.Errorf("sampled value above tail extrapolation point: %v", max)
	}
}

func TestNewPercentileSampler_RejectsNonMonotonic(t *testing.T) {
	_, err := benchmark.NewPercentileSampler("bad", 10,
		[]benchmark.PercentilePoint{{P: 0.5, Value: 100}, {P: 0.95, Value: 50}}, 200)
	if err == nil {
		t.Fatal("expected error for non-monotonic percentile points")
	}
}

type fakeWriter struct {
	buf []byte
}

func (w *fakeWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	return len(p), nil
}

func (w *fakeWriter) String() string {
	return string(w.buf)
}
