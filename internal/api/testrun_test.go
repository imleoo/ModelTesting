package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leoobai/modeltestbed/internal/api"
	"github.com/leoobai/modeltestbed/internal/benchmark"
	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/store"
)

// miniSuiteJSON 是一个只有 1 条 fixed_required 基础用例（connectivity.basic，
// content_nonempty 断言）的最小套件定义——不是仓库里真实的 23 条 Kimi-K3
// 套件。API 层的编排测试只需要验证状态机/文件落盘/并发互斥这些编排本身的
// 正确性，用例断言逻辑本身已经在 internal/engine 有专门的测试覆盖，这里
// 用最小套件换取测试速度和自包含性。
const miniSuiteJSON = `{
  "suite_id": "mini",
  "suite_version": "1.0.0",
  "materials_manifest": "mini/manifest.json",
  "protocol": {"base_path": "/v1/chat/completions", "auth_header": "Authorization: Bearer {{api_key}}", "content_type": "application/json"},
  "defaults": {"timeout_seconds": 10, "category_timeout_overrides": {}},
  "fixtures": {"tools": {}, "prompts": {"generic_short": "Say hello."}},
  "cases": [
    {
      "id": "connectivity.basic",
      "category": "连通性",
      "name": "基础单轮请求",
      "required_rule": "fixed_required",
      "counts_in_base22": true,
      "assertion_type": "content_nonempty",
      "repeat_attempts": 1,
      "request_template": {
        "method": "POST",
        "body": {"model": "{{model_key}}", "messages": [{"role": "user", "content": "{{prompts.generic_short}}"}], "stream": false}
      }
    }
  ]
}`

// miniFailingSuiteJSON 的唯一用例断言类型是 tool_call_required，但 mock
// 网关只会返回纯文本——这条用例必然 FAIL，用于驱动编排走
// FUNCTIONAL_BLOCKED 分支（压测不解锁）。
const miniFailingSuiteJSON = `{
  "suite_id": "mini-fail",
  "suite_version": "1.0.0",
  "materials_manifest": "mini-fail/manifest.json",
  "protocol": {"base_path": "/v1/chat/completions", "auth_header": "Authorization: Bearer {{api_key}}", "content_type": "application/json"},
  "defaults": {"timeout_seconds": 10, "category_timeout_overrides": {}},
  "fixtures": {"tools": {}, "prompts": {"generic_short": "Say hello."}},
  "cases": [
    {
      "id": "tool.always_fails",
      "category": "工具调用支持",
      "name": "必然失败的工具调用用例",
      "required_rule": "fixed_required",
      "counts_in_base22": true,
      "assertion_type": "tool_call_required",
      "repeat_attempts": 1,
      "request_template": {
        "method": "POST",
        "body": {"model": "{{model_key}}", "messages": [{"role": "user", "content": "{{prompts.generic_short}}"}], "stream": false}
      }
    }
  ]
}`

const minimalManifestJSON = `{"manifest_version": "1", "suite_id": "mini", "materials": []}`

// mockGateway 处理两类请求：功能测试用例的非流式调用（body.stream=false）
// 返回一次性 JSON；压测引擎的调用固定 stream=true（见
// internal/benchmark/executor.go），返回 SSE 分片。两者共用同一个 mock，
// 因为同一个 base URL 在 orchestrate 里既服务功能测试也服务压测。
func mockGateway() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if stream, _ := body["stream"].(bool); !stream {
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]any{
				"id": "chatcmpl-1", "object": "chat.completion", "created": 1700000000, "model": "mini",
				"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "hello there"}, "finish_reason": "stop"}},
				"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(delta map[string]any, finish any, usage map[string]any) {
			chunk := map[string]any{
				"id": "c1", "object": "chat.completion.chunk", "created": 1700000000, "model": "mini",
				"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
			}
			if usage != nil {
				chunk["usage"] = usage
			}
			raw, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", raw)
			flusher.Flush()
		}
		write(map[string]any{"role": "assistant", "content": "hi"}, nil, nil)
		write(map[string]any{}, "stop", map[string]any{
			"prompt_tokens": 5, "completion_tokens": 1,
			"prompt_tokens_details": map[string]any{"cached_tokens": 2},
		})
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

func fastBenchmarkParams(totalSessions int) (*benchmark.Params, error) {
	params, err := benchmark.DefaultParams(totalSessions)
	if err != nil {
		return nil, err
	}
	// 覆盖成极短的合成分布，避免集成测试卡在真实 6.1 节参数（轮次间隔均值
	// 18.6s）上——同 internal/benchmark 自己的端到端测试（
	// TestRun_EndToEndAgainstMockGateway）采用的手法。
	params.RampDurationSeconds = 1
	params.ArrivalRateStart = 20
	params.ArrivalRateEnd = 20
	fastRounds, err := benchmark.NewPercentileSampler("num-rounds-test", 1,
		[]benchmark.PercentilePoint{{P: 0.5, Value: 1}, {P: 0.95, Value: 2}}, 3)
	if err != nil {
		return nil, err
	}
	fastTurnInterval, err := benchmark.NewPercentileSampler("turn-interval-test", 0.001,
		[]benchmark.PercentilePoint{{P: 0.5, Value: 0.005}, {P: 0.95, Value: 0.01}}, 0.02)
	if err != nil {
		return nil, err
	}
	params.NumRounds = fastRounds
	params.TurnIntervalSeconds = fastTurnInterval
	return params, nil
}

// writeFixtureSuite 在 dir 下写入一个套件文件 + 素材清单，返回相对 dir 的
// 套件文件路径（供 TestRun.SuiteID 使用）。
func writeFixtureSuite(t *testing.T, root, name, suiteJSON string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir suite dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "suite.v1.json"), []byte(suiteJSON), 0o644); err != nil {
		t.Fatalf("write suite json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(minimalManifestJSON), 0o644); err != nil {
		t.Fatalf("write manifest json: %v", err)
	}
	return filepath.Join(name, "suite.v1.json")
}

func newTestConfig(t *testing.T, gatewayURL string) (*api.Config, *store.Store) {
	t.Helper()
	root := t.TempDir()
	suitesRoot := filepath.Join(root, "suites")
	if err := os.MkdirAll(suitesRoot, 0o755); err != nil {
		t.Fatalf("mkdir suites root: %v", err)
	}
	reportsRoot := filepath.Join(root, "reports")

	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	cfg := &api.Config{
		Store:                s,
		SuitesRoot:           suitesRoot,
		ReportsRoot:          reportsRoot,
		RepoRoot:             suitesRoot,
		MaterialsBaseURL:     gatewayURL,
		RequestTimeout:       10 * time.Second,
		DefaultTotalSessions: 1,
		NewBenchmarkParams:   fastBenchmarkParams,
	}
	return cfg, s
}

func createProviderAndModel(t *testing.T, s *store.Store, endpoint string) model.Model {
	t.Helper()
	p, err := s.CreateProvider(model.Provider{Name: "MockProvider"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	m, err := s.CreateModel(model.Model{
		ProviderID:            p.ID,
		ModelKey:              "mini-model",
		EndpointViaTokenpanel: endpoint,
		Capability: model.CapabilityProfile{
			ImageBase64:             true,
			VideoBase64:             true,
			ThinkingToggleMethods:   []string{"enable_thinking"},
			DefaultThinkingBehavior: "thinks_by_default",
		},
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	return m
}

func pollUntilTerminal(t *testing.T, s *store.Store, runID string, timeout time.Duration) model.TestRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		run, err := s.GetTestRun(runID)
		if err != nil {
			t.Fatalf("GetTestRun: %v", err)
		}
		switch run.Status {
		case model.RunCompleted, model.RunFunctionalBlocked, model.RunFailed:
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for test run to reach a terminal status, last status=%s", run.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestOrchestrate_AllPassUnlocksBenchmarkAndCompletes(t *testing.T) {
	gw := httptest.NewServer(mockGateway())
	defer gw.Close()

	cfg, s := newTestConfig(t, gw.URL)
	suiteID := writeFixtureSuite(t, cfg.SuitesRoot, "mini", miniSuiteJSON)
	m := createProviderAndModel(t, s, gw.URL)

	r := api.NewRouter(cfg)

	body, _ := json.Marshal(map[string]any{"model_id": m.ID, "suite_id": suiteID, "api_key": "test-key", "total_sessions": 1})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/test-runs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var run model.TestRun
	if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil {
		t.Fatalf("unmarshal launch response: %v", err)
	}

	final := pollUntilTerminal(t, s, run.ID, 20*time.Second)
	if final.Status != model.RunCompleted {
		t.Fatalf("expected COMPLETED, got %s (error=%q)", final.Status, final.ErrorMessage)
	}
	if final.CaseResultsPath == "" || final.BenchmarkResultPath == "" {
		t.Errorf("expected both case results and benchmark result paths to be populated, got %+v", final)
	}

	rpt, err := s.GetReportForTestRun(run.ID)
	if err != nil {
		t.Fatalf("GetReportForTestRun: %v", err)
	}
	if _, err := os.Stat(rpt.HTMLRef); err != nil {
		t.Errorf("expected report HTML file to exist at %s: %v", rpt.HTMLRef, err)
	}

	// GET /api/test-runs/:id/report 应该能拿到这份 HTML。
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/api/test-runs/"+run.ID+"/report", nil))
	if w2.Code != http.StatusOK {
		t.Errorf("expected 200 for report download, got %d", w2.Code)
	}
	if len(w2.Body.Bytes()) == 0 {
		t.Error("expected non-empty report body")
	}
}

func TestOrchestrate_RequiredCaseFailBlocksBenchmark(t *testing.T) {
	gw := httptest.NewServer(mockGateway())
	defer gw.Close()

	cfg, s := newTestConfig(t, gw.URL)
	suiteID := writeFixtureSuite(t, cfg.SuitesRoot, "mini-fail", miniFailingSuiteJSON)
	m := createProviderAndModel(t, s, gw.URL)

	r := api.NewRouter(cfg)
	body, _ := json.Marshal(map[string]any{"model_id": m.ID, "suite_id": suiteID, "api_key": "test-key"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/test-runs", bytes.NewReader(body)))
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var run model.TestRun
	if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	final := pollUntilTerminal(t, s, run.ID, 10*time.Second)
	if final.Status != model.RunFunctionalBlocked {
		t.Fatalf("expected FUNCTIONAL_BLOCKED, got %s (error=%q)", final.Status, final.ErrorMessage)
	}
	if final.BenchmarkResultPath != "" {
		t.Errorf("expected no benchmark to run when required cases fail, got benchmark path %q", final.BenchmarkResultPath)
	}
	// 即便压测没跑，报告仍然应该生成（功能测试部分 + 显式标注压测未执行）。
	rpt, err := s.GetReportForTestRun(run.ID)
	if err != nil {
		t.Fatalf("expected a report to still be generated on the blocked path: %v", err)
	}
	if rpt.Verdict != "FAIL" {
		t.Errorf("expected report verdict FAIL for a blocked run with a failing required case, got %s", rpt.Verdict)
	}
}

func TestLaunchTestRun_RejectsConcurrentLaunch(t *testing.T) {
	gw := httptest.NewServer(mockGateway())
	defer gw.Close()

	cfg, s := newTestConfig(t, gw.URL)
	suiteID := writeFixtureSuite(t, cfg.SuitesRoot, "mini", miniSuiteJSON)
	m := createProviderAndModel(t, s, gw.URL)
	r := api.NewRouter(cfg)

	launch := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"model_id": m.ID, "suite_id": suiteID, "api_key": "test-key", "total_sessions": 1})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/test-runs", bytes.NewReader(body)))
		return w
	}

	first := launch()
	if first.Code != http.StatusAccepted {
		t.Fatalf("expected first launch to be accepted, got %d: %s", first.Code, first.Body.String())
	}
	second := launch()
	if second.Code != http.StatusConflict {
		t.Errorf("expected second concurrent launch to be rejected with 409, got %d: %s", second.Code, second.Body.String())
	}

	var run model.TestRun
	_ = json.Unmarshal(first.Body.Bytes(), &run)
	pollUntilTerminal(t, s, run.ID, 20*time.Second) // 等第一个任务跑完，释放锁，避免污染后续测试
}

func TestLaunchTestRun_UnknownModelReturns404(t *testing.T) {
	cfg, _ := newTestConfig(t, "http://example.invalid")
	r := api.NewRouter(cfg)
	body, _ := json.Marshal(map[string]any{"model_id": "does-not-exist", "suite_id": "mini/suite.v1.json"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/test-runs", bytes.NewReader(body)))
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown model_id, got %d: %s", w.Code, w.Body.String())
	}
}
