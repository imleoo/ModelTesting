package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"sync"
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
	LogRef         string    // LogWriter 对应的可读引用（如文件路径），写入 BenchmarkRun.RawStdoutRef
}

type RunResult struct {
	Run      model.BenchmarkRun
	Metrics  []model.BenchmarkMetric
	Outcomes []RequestOutcome
}

// runMu 保证同一进程内同一时刻只有一次 Run 在执行（设计方案 6.3 节：
// "首版压测任务用互斥锁保证同一时刻只有一个压测在执行"），避免两次压测
// 并发抢占导致 TTFT 等时序指标失真。这只覆盖单进程内的并发调用；跨进程/
// 跨机器的互斥不在本版范围内（首版单进程部署，见设计方案 10.1 节）。
var runMu sync.Mutex

// apiKeyRedactRe 匹配常见的 -api-key <value> / -api-key=<value> 命令行参数写法，
// 用于从 raw_command 留痕里去掉真实密钥——BenchmarkRun.RawParamsJSON/RawCommand
// 会被写进结果文件和报告，绝不能把 API Key 明文留在这些产物里。
var apiKeyRedactRe = regexp.MustCompile(`(-{1,2}api-key[= ])(\S+)`)

func RedactCommand(cmd string) string {
	return apiKeyRedactRe.ReplaceAllString(cmd, "${1}***REDACTED***")
}

// Run 执行一次完整压测：生成会话 → 按爬坡到达率并发执行 → 汇总指标 → 按 6.2
// 节规则判定 → 产出 BenchmarkRun + []BenchmarkMetric。
func Run(ctx context.Context, cfg RunConfig) (RunResult, error) {
	runMu.Lock()
	defer runMu.Unlock()

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
		cachedStr := "n/a"
		if o.CachedTokens != nil {
			cachedStr = fmt.Sprintf("%d", *o.CachedTokens)
		}
		if o.Success {
			logLine("[request] session=%d round=%d ok ttft=%.3fs latency=%.3fs output_tokens=%d cached_tokens=%s/%d",
				o.SessionID, o.RoundIndex, o.TTFTSeconds, o.LatencySeconds, o.OutputTokens, cachedStr, o.PromptTokens)
		} else {
			logLine("[request] session=%d round=%d FAILED status=%d err=%q", o.SessionID, o.RoundIndex, o.HTTPStatus, o.Err)
		}
	})

	duration := endedAt.Sub(startedAt).Seconds()
	logLine("benchmark run end: duration=%.1fs total_requests=%d", duration, len(outcomes))

	run := model.BenchmarkRun{
		ID:             fmt.Sprintf("bench-%d", startedAt.Unix()),
		RawCommand:     RedactCommand(cfg.RawCommand),
		RawParamsJSON:  string(paramsJSON),
		ToolVersion:    cfg.ToolVersion,
		DatasetVersion: cfg.DatasetVersion,
		RawStdoutRef:   cfg.LogRef,
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
		BaselineVerdict: judgeLowerIsBetterStrict(tpotP.P50, baselineTPOTP50Millis),
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

// cacheHitMetrics 计算整体（稳态均值）与分轮次缓存命中率。只使用真正观测到
// usage.prompt_tokens_details.cached_tokens 字段的样本（CachedTokens != nil）；
// 该字段完全未出现时不能当作"命中率为 0%"，必须走 6.2 节的 NOT_OBSERVABLE
// 兜底规则，否则会把"没测到"误判成"确实没命中"进而误判 FAIL。
func cacheHitMetrics(successful []RequestOutcome) []model.BenchmarkMetric {
	var metrics []model.BenchmarkMetric

	byRound := map[int][]RequestOutcome{}
	maxRound := -1
	observedAny := false
	for _, o := range successful {
		if o.CachedTokens == nil || o.PromptTokens <= 0 {
			continue // 未观测到该字段，或 prompt_tokens 缺失导致无法算比率
		}
		observedAny = true
		byRound[o.RoundNumber] = append(byRound[o.RoundNumber], o)
		if o.RoundNumber > maxRound {
			maxRound = o.RoundNumber
		}
	}

	if !observedAny {
		metrics = append(metrics, model.BenchmarkMetric{
			Name: "cache_hit_rate", Scope: "overall", Unit: "ratio",
			BaselineVerdict: model.BaselineNotObservable,
			Note:            "未在任何响应的 usage 中观测到 prompt_tokens_details.cached_tokens 字段，按 6.2 节兜底规则移出验收门禁",
		})
		return metrics
	}

	// 单轮内取 token 加权比率（同一轮次多个请求汇总看命中情况是合理的），
	// 但"稳态整体"按设计方案原文"取稳态轮次均值"，是对多个轮次各自的比率
	// 取算术平均，不是把所有轮次的 token 数混在一起再算一个比率——后者会让
	// token 量特别大的轮次主导结果，掩盖其他轮次命中率异常的信号。
	rateOfRound := func(rows []RequestOutcome) float64 {
		sumPrompt, sumCached := 0, 0
		for _, o := range rows {
			sumPrompt += o.PromptTokens
			sumCached += *o.CachedTokens
		}
		if sumPrompt == 0 {
			return 0
		}
		return float64(sumCached) / float64(sumPrompt)
	}

	perRoundRate := make(map[int]float64, len(byRound))
	for round, rows := range byRound {
		perRoundRate[round] = rateOfRound(rows)
	}

	var steadyRates []float64
	for round, rate := range perRoundRate {
		if round > 0 {
			steadyRates = append(steadyRates, rate)
		}
	}
	steadyAvg := 0.0
	verdict := model.BaselineNotObservable
	note := "首轮（Round 0）无历史，已剔除；稳态轮次为空，无法判定"
	if len(steadyRates) > 0 {
		sum := 0.0
		for _, r := range steadyRates {
			sum += r
		}
		steadyAvg = sum / float64(len(steadyRates))
		verdict = judgeHigherIsBetter(steadyAvg, baselineCacheHitRate)
		note = "剔除首轮（Round 0）后，对各稳态轮次的命中率取算术平均"
	}
	metrics = append(metrics, model.BenchmarkMetric{
		Name: "cache_hit_rate", Scope: "overall", Avg: steadyAvg, Unit: "ratio",
		BaselineVerdict: verdict, Note: note,
	})

	for round := 0; round <= maxRound; round++ {
		rate, ok := perRoundRate[round]
		if !ok {
			continue
		}
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
