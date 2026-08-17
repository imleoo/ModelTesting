package benchmark

import (
	"testing"

	"github.com/leoobai/modeltestbed/internal/model"
)

func overallMetric(metrics []model.BenchmarkMetric, name string) model.BenchmarkMetric {
	for _, m := range metrics {
		if m.Scope == "overall" && m.Name == name {
			return m
		}
	}
	return model.BenchmarkMetric{}
}

// TestComputeMetrics_TTFTAndTPOTNotObservableWhenNoContentEverArrives 防止
// P3 review round-2 发现的回归：全部成功请求都没有观测到首个非空
// delta.content 分片时，TTFT/TPOT 必须落到 NOT_OBSERVABLE，不能因为
// TTFTSeconds/TPOTMillis 恒为 0 而被 judgeLowerIsBetter(Strict) 误判成 OK。
func TestComputeMetrics_TTFTAndTPOTNotObservableWhenNoContentEverArrives(t *testing.T) {
	outcomes := []RequestOutcome{
		{
			Success:        true,
			HTTPStatus:     200,
			LatencySeconds: 1.0,
			OutputTokens:   0,
			PromptTokens:   10,
			// TTFTObserved / TPOTObserved 保持零值 false：模拟"200 但从未
			// 出现非空 delta.content 分片"的响应。
		},
	}

	metrics := computeMetrics(outcomes, 1.0)

	if v := overallMetric(metrics, "ttft").BaselineVerdict; v != model.BaselineNotObservable {
		t.Errorf("expected ttft NOT_OBSERVABLE when no content ever arrived, got %s", v)
	}
	if v := overallMetric(metrics, "tpot").BaselineVerdict; v != model.BaselineNotObservable {
		t.Errorf("expected tpot NOT_OBSERVABLE when no content ever arrived, got %s", v)
	}
}

// TestComputeMetrics_TTFTObservedSamplesStillJudged 确认修复没有误伤正常
// 路径：有真实观测样本时 TTFT/TPOT 仍按原基线规则判定。
func TestComputeMetrics_TTFTObservedSamplesStillJudged(t *testing.T) {
	outcomes := []RequestOutcome{
		{
			Success: true, HTTPStatus: 200,
			TTFTSeconds: 1.0, TTFTObserved: true,
			TPOTMillis: 10.0, TPOTObserved: true,
			LatencySeconds: 2.0, OutputTokens: 5, PromptTokens: 10,
		},
	}

	metrics := computeMetrics(outcomes, 1.0)

	if v := overallMetric(metrics, "ttft").BaselineVerdict; v != model.BaselineOK {
		t.Errorf("expected ttft OK for a fast observed sample, got %s", v)
	}
	if v := overallMetric(metrics, "tpot").BaselineVerdict; v != model.BaselineOK {
		t.Errorf("expected tpot OK for a fast observed sample, got %s", v)
	}
}

// TestExtractUsageFromChunk_EmptyPromptTokensDetailsIsNotObserved 防止
// P3 review round-2 发现的回归：prompt_tokens_details 键存在但是个空对象
// （没有 cached_tokens 字段）时，必须视为"未观测到"，不能被 JSON 反序列化
// 的零值悄悄当成"观测到且为 0"。
func TestExtractUsageFromChunk_EmptyPromptTokensDetailsIsNotObserved(t *testing.T) {
	result := &StreamCallResult{}
	extractUsageFromChunk(`{"usage":{"prompt_tokens":10,"completion_tokens":2,"prompt_tokens_details":{}}}`, result)
	if result.CachedTokens != nil {
		t.Errorf("expected CachedTokens nil when cached_tokens key absent, got %v", *result.CachedTokens)
	}
}

// TestExtractUsageFromChunk_ExplicitZeroCachedTokensIsObserved 确认修复没有
// 误伤正常路径：cached_tokens 显式回传 0 时必须被当作"观测到，且为 0"。
func TestExtractUsageFromChunk_ExplicitZeroCachedTokensIsObserved(t *testing.T) {
	result := &StreamCallResult{}
	extractUsageFromChunk(`{"usage":{"prompt_tokens":10,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":0}}}`, result)
	if result.CachedTokens == nil {
		t.Fatal("expected CachedTokens observed (non-nil) when cached_tokens explicitly present")
	}
	if *result.CachedTokens != 0 {
		t.Errorf("expected CachedTokens=0, got %d", *result.CachedTokens)
	}
}

// TestExtractUsageFromChunk_MissingPromptTokensDetailsIsNotObserved 确认
// prompt_tokens_details 整个键都不存在时（最常见的情况）依然是"未观测到"。
func TestExtractUsageFromChunk_MissingPromptTokensDetailsIsNotObserved(t *testing.T) {
	result := &StreamCallResult{}
	extractUsageFromChunk(`{"usage":{"prompt_tokens":10,"completion_tokens":2}}`, result)
	if result.CachedTokens != nil {
		t.Errorf("expected CachedTokens nil when prompt_tokens_details absent, got %v", *result.CachedTokens)
	}
}
