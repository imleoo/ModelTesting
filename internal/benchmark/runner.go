package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"

	"github.com/leoobai/modeltestbed/internal/model"
)

type RunConfig struct {
	Params         *Params
	BaseURL        string
	APIKey         string
	ModelKey       string
	RequestTimeout time.Duration
	RawCommand     string
	ToolVersion    string
	DatasetVersion string
	Seed           int64
	LogWriter      io.Writer // 逐行写入原始事件留痕（对应 6.3 节 stdout/stderr 全量日志引用）
}

type RunResult struct {
	Run      model.BenchmarkRun
	Metrics  []model.BenchmarkMetric
	Outcomes []RequestOutcome
}

// Run 执行一次完整压测：生成会话 → 按爬坡到达率并发执行 → 汇总指标 → 按 6.2
// 节规则判定 → 产出 BenchmarkRun + []BenchmarkMetric。
func Run(ctx context.Context, cfg RunConfig) (RunResult, error) {
	rng := rand.New(rand.NewSource(cfg.Seed))
	sessions := GenerateSessions(cfg.Params, rng)

	paramsJSON, err := json.Marshal(cfg.Params)
	if err != nil {
		return RunResult{}, fmt.Errorf("marshal params: %w", err)
	}

	var outcomes []RequestOutcome
	logLine := func(format string, args ...any) {
		if cfg.LogWriter != nil {
			fmt.Fprintf(cfg.LogWriter, format+"\n", args...)
		}
	}
	logLine("benchmark run start: total_sessions=%d model=%s base_url=%s", cfg.Params.TotalSessions, cfg.ModelKey, cfg.BaseURL)

	loadCfg := LoadGenConfig{
		Params:         cfg.Params,
		BaseURL:        cfg.BaseURL,
		APIKey:         cfg.APIKey,
		ModelKey:       cfg.ModelKey,
		HTTPClient:     &http.Client{},
		RequestTimeout: cfg.RequestTimeout,
	}

	startedAt, endedAt := RunLoadTest(ctx, loadCfg, sessions, rng, func(o RequestOutcome) {
		outcomes = append(outcomes, o)
		if o.Success {
			logLine("[request] session=%d round=%d ok ttft=%.3fs latency=%.3fs output_tokens=%d cached_tokens=%d/%d",
				o.SessionID, o.RoundIndex, o.TTFTSeconds, o.LatencySeconds, o.OutputTokens, o.CachedTokens, o.PromptTokens)
		} else {
			logLine("[request] session=%d round=%d FAILED status=%d err=%q", o.SessionID, o.RoundIndex, o.HTTPStatus, o.Err)
		}
	})

	duration := endedAt.Sub(startedAt).Seconds()
	logLine("benchmark run end: duration=%.1fs total_requests=%d", duration, len(outcomes))

	run := model.BenchmarkRun{
		ID:             fmt.Sprintf("bench-%d", startedAt.Unix()),
		RawCommand:     cfg.RawCommand,
		RawParamsJSON:  string(paramsJSON),
		ToolVersion:    cfg.ToolVersion,
		DatasetVersion: cfg.DatasetVersion,
		TotalRequests:  len(outcomes),
		DurationS:      duration,
	}

	metrics := computeMetrics(outcomes, duration)
	return RunResult{Run: run, Metrics: metrics, Outcomes: outcomes}, nil
}

func computeMetrics(outcomes []RequestOutcome, durationS float64) []model.BenchmarkMetric {
	var metrics []model.BenchmarkMetric

	successful := make([]RequestOutcome, 0, len(outcomes))
	for _, o := range outcomes {
		if o.Success {
			successful = append(successful, o)
		}
	}

	// Total requests / Duration：仅记录，无判定（6.2 节表格）。
	metrics = append(metrics, model.BenchmarkMetric{
		Name: "total_requests", Scope: "overall", Avg: float64(len(outcomes)), Unit: "count",
		BaselineVerdict: model.BaselineNotApplicable,
	})
	metrics = append(metrics, model.BenchmarkMetric{
		Name: "duration", Scope: "overall", Avg: durationS, Unit: "s",
		BaselineVerdict: model.BaselineNotApplicable,
	})

	// Throughput：req/s + 输入/输出 tok/s。
	throughputReqS := 0.0
	inputTokS := 0.0
	outputTokS := 0.0
	if durationS > 0 {
		throughputReqS = float64(len(successful)) / durationS
		sumPrompt, sumOutput := 0, 0
		for _, o := range successful {
			sumPrompt += o.PromptTokens
			sumOutput += o.OutputTokens
		}
		inputTokS = float64(sumPrompt) / durationS
		outputTokS = float64(sumOutput) / durationS
	}
	metrics = append(metrics, model.BenchmarkMetric{
		Name: "throughput_req_s", Scope: "overall", Avg: throughputReqS, Unit: "req/s",
		BaselineVerdict: judgeHigherIsBetter(throughputReqS, baselineThroughputReqS),
	})
	metrics = append(metrics, model.BenchmarkMetric{
		Name: "throughput_input_tok_s", Scope: "overall", Avg: inputTokS, Unit: "tok/s",
		BaselineVerdict: model.BaselineNotApplicable, Note: "PDF 未给该分项独立基线，随 throughput_req_s 一并参考",
	})
	metrics = append(metrics, model.BenchmarkMetric{
		Name: "throughput_output_tok_s", Scope: "overall", Avg: outputTokS, Unit: "tok/s",
		BaselineVerdict: model.BaselineNotApplicable, Note: "PDF 未给该分项独立基线，随 throughput_req_s 一并参考",
	})

	// TTFT
	ttftSamples := make([]float64, 0, len(successful))
	for _, o := range successful {
		ttftSamples = append(ttftSamples, o.TTFTSeconds)
	}
	ttftP := ComputePercentiles(ttftSamples)
	metrics = append(metrics, model.BenchmarkMetric{
		Name: "ttft", Scope: "overall", Unit: "s",
		Avg: ttftP.Avg, P50: ttftP.P50, P75: ttftP.P75, P90: ttftP.P90, P95: ttftP.P95, P99: ttftP.P99,
		BaselineVerdict: judgeLowerIsBetter(ttftP.P50, baselineTTFTP50Seconds),
		Note:            "判定仅看 P50，其余分位仅记录（06 节 6.2 表）",
	})

	// TPOT
	tpotSamples := make([]float64, 0, len(successful))
	for _, o := range successful {
		if o.TPOTMillis > 0 {
			tpotSamples = append(tpotSamples, o.TPOTMillis)
		}
	}
	tpotP := ComputePercentiles(tpotSamples)
	metrics = append(metrics, model.BenchmarkMetric{
		Name: "tpot", Scope: "overall", Unit: "ms",
		Avg: tpotP.Avg, P50: tpotP.P50, P75: tpotP.P75, P90: tpotP.P90, P95: tpotP.P95, P99: tpotP.P99,
		BaselineVerdict: judgeLowerIsBetter(tpotP.P50, baselineTPOTP50Millis),
		Note:            "判定仅看 P50，其余分位仅记录（06 节 6.2 表）",
	})

	// Latency：无 PDF 基线，首版仅记录 + MANUAL_REVIEW（06 节）。
	latSamples := make([]float64, 0, len(successful))
	for _, o := range successful {
		latSamples = append(latSamples, o.LatencySeconds)
	}
	latP := ComputePercentiles(latSamples)
	metrics = append(metrics, model.BenchmarkMetric{
		Name: "latency", Scope: "overall", Unit: "s",
		Avg: latP.Avg, P50: latP.P50, P75: latP.P75, P90: latP.P90, P95: latP.P95, P99: latP.P99,
		BaselineVerdict: model.BaselineManualReview,
		Note:            "无 PDF 基线，首版仅记录，待历史对比能力上线后启用趋势比较（06 节）",
	})

	// ITL：同 Latency，无基线，仅记录。
	var itlAll []float64
	for _, o := range successful {
		itlAll = append(itlAll, o.ITLMillis...)
	}
	itlP := ComputePercentiles(itlAll)
	metrics = append(metrics, model.BenchmarkMetric{
		Name: "itl", Scope: "overall", Unit: "ms",
		Avg: itlP.Avg, P50: itlP.P50, P75: itlP.P75, P90: itlP.P90, P95: itlP.P95, P99: itlP.P99,
		BaselineVerdict: model.BaselineManualReview,
		Note:            "无 PDF 基线，首版仅记录，待历史对比能力上线后启用趋势比较（06 节）",
	})

	// 缓存命中率：整体 + 分轮次，剔除首轮（Round 0）后取稳态轮次均值判定。
	metrics = append(metrics, cacheHitMetrics(successful)...)

	return metrics
}

func cacheHitMetrics(successful []RequestOutcome) []model.BenchmarkMetric {
	var metrics []model.BenchmarkMetric

	byRound := map[int][]RequestOutcome{}
	maxRound := -1
	for _, o := range successful {
		if o.PromptTokens <= 0 {
			continue // 无法计算命中率的样本（如 usage 未回传）不计入
		}
		byRound[o.RoundNumber] = append(byRound[o.RoundNumber], o)
		if o.RoundNumber > maxRound {
			maxRound = o.RoundNumber
		}
	}

	if maxRound < 0 {
		metrics = append(metrics, model.BenchmarkMetric{
			Name: "cache_hit_rate", Scope: "overall", Unit: "ratio",
			BaselineVerdict: model.BaselineNotObservable,
			Note:            "未在响应 usage 中观测到 prompt_tokens_details.cached_tokens 字段，按 6.2 节兜底规则移出验收门禁",
		})
		return metrics
	}

	rateOf := func(rows []RequestOutcome) float64 {
		sumPrompt, sumCached := 0, 0
		for _, o := range rows {
			sumPrompt += o.PromptTokens
			sumCached += o.CachedTokens
		}
		if sumPrompt == 0 {
			return 0
		}
		return float64(sumCached) / float64(sumPrompt)
	}

	// 稳态：剔除 Round 0。
	var steady []RequestOutcome
	for round, rows := range byRound {
		if round > 0 {
			steady = append(steady, rows...)
		}
	}
	steadyRate := rateOf(steady)
	verdict := model.BaselineNotObservable
	note := "首轮（Round 0）无历史，已剔除；稳态轮次为空，无法判定"
	if len(steady) > 0 {
		verdict = judgeHigherIsBetter(steadyRate, baselineCacheHitRate)
		note = "剔除首轮（Round 0）后取稳态轮次均值"
	}
	metrics = append(metrics, model.BenchmarkMetric{
		Name: "cache_hit_rate", Scope: "overall", Avg: steadyRate, Unit: "ratio",
		BaselineVerdict: verdict, Note: note,
	})

	for round := 0; round <= maxRound; round++ {
		rows, ok := byRound[round]
		if !ok {
			continue
		}
		rate := rateOf(rows)
		v := model.BaselineNotApplicable
		n := "仅记录，不参与验收判定（06 节判定基于稳态整体均值，不逐轮次判定）"
		if round == 0 {
			n = "首轮无历史，命中率接近 0 属正常（06 节稳态提示）"
		}
		metrics = append(metrics, model.BenchmarkMetric{
			Name: "cache_hit_rate", Scope: "per_round", RoundIndex: round, Avg: rate, Unit: "ratio",
			BaselineVerdict: v, Note: n,
		})
	}
	return metrics
}
