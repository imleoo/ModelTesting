package report

import (
	"testing"

	"github.com/leoobai/modeltestbed/internal/model"
)

func TestCompute_AllPassIsVerdictPass(t *testing.T) {
	cases := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
		{CaseID: "reasoning_effort", Status: model.StatusPass, CountsInBase22: false},
	}
	metrics := []model.BenchmarkMetric{
		{Name: "throughput_req_s", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "ttft", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "tpot", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "cache_hit_rate", Scope: "overall", BaselineVerdict: model.BaselineOK},
	}
	s := Compute(cases, metrics)
	if s.Verdict != VerdictPass {
		t.Errorf("expected PASS, got %s (rule1=%s rule2=%s rule3=%s)", s.Verdict, s.Rule1.State, s.Rule2.State, s.Rule3.State)
	}
}

func TestCompute_Base22FailIsVerdictFail(t *testing.T) {
	cases := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusFail, CountsInBase22: true, FailReason: "boom"},
	}
	s := Compute(cases, nil)
	if s.Verdict != VerdictFail {
		t.Errorf("expected FAIL when a base22 case fails, got %s", s.Verdict)
	}
	if s.Rule1.State != RuleFail {
		t.Errorf("expected Rule1 FAIL, got %s", s.Rule1.State)
	}
}

// TestCompute_DeclaredReasoningEffortFailStillBlocksOverall 防止 v0.2 曾经的
// 回归：reasoning_effort 不计入 22 分母，但已声明时失败仍必须拖累总体结论
// （08 节规则 2 存在的意义）。
func TestCompute_DeclaredReasoningEffortFailStillBlocksOverall(t *testing.T) {
	cases := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
		{CaseID: "reasoning_effort", Status: model.StatusFail, CountsInBase22: false, FailReason: "low/high 无区分度"},
	}
	s := Compute(cases, nil)
	if s.Verdict != VerdictFail {
		t.Errorf("expected FAIL when declared reasoning_effort fails even though it's outside base22, got %s", s.Verdict)
	}
	if s.Rule2.State != RuleFail {
		t.Errorf("expected Rule2 FAIL, got %s", s.Rule2.State)
	}
}

func TestCompute_ManualReviewWithoutFailIsPending(t *testing.T) {
	cases := []model.CaseResult{
		{CaseID: "video_url", Status: model.StatusManualReview, CountsInBase22: true},
	}
	s := Compute(cases, nil)
	if s.Verdict != VerdictPendingManualReview {
		t.Errorf("expected PENDING_MANUAL_REVIEW, got %s", s.Verdict)
	}
}

// TestCompute_FailOutranksManualReview 验证"确定失败"比"待复核"更严重：
// 两者同时出现时，总体结论必须是 FAIL，不能被弱化成"待人工确认"。
func TestCompute_FailOutranksManualReview(t *testing.T) {
	cases := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusFail, CountsInBase22: true, FailReason: "boom"},
		{CaseID: "video_url", Status: model.StatusManualReview, CountsInBase22: true},
	}
	s := Compute(cases, nil)
	if s.Verdict != VerdictFail {
		t.Errorf("expected FAIL to outrank PENDING, got %s", s.Verdict)
	}
}

func TestCompute_NotObservableCacheHitDoesNotBlock(t *testing.T) {
	cases := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
	}
	metrics := []model.BenchmarkMetric{
		{Name: "throughput_req_s", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "ttft", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "tpot", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "cache_hit_rate", Scope: "overall", BaselineVerdict: model.BaselineNotObservable},
	}
	s := Compute(cases, metrics)
	if s.Verdict != VerdictPass {
		t.Errorf("expected PASS (NOT_OBSERVABLE cache_hit_rate moved out of the gate per 6.2 节), got %s: rule3 reasons=%v", s.Verdict, s.Rule3.Reasons)
	}
}

func TestCompute_MissingBenchmarkMetricIsPending(t *testing.T) {
	cases := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
	}
	s := Compute(cases, nil) // 没有任何压测指标
	if s.Verdict != VerdictPendingManualReview {
		t.Errorf("expected PENDING_MANUAL_REVIEW when benchmark data is entirely missing, got %s", s.Verdict)
	}
}

func TestCompute_BenchmarkMetricFailBlocksOverall(t *testing.T) {
	cases := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
	}
	metrics := []model.BenchmarkMetric{
		{Name: "throughput_req_s", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "ttft", Scope: "overall", BaselineVerdict: model.BaselineFail},
		{Name: "tpot", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "cache_hit_rate", Scope: "overall", BaselineVerdict: model.BaselineOK},
	}
	s := Compute(cases, metrics)
	if s.Verdict != VerdictFail {
		t.Errorf("expected FAIL when a gated benchmark metric is FAIL, got %s", s.Verdict)
	}
}

func TestCompute_EmptyCaseResultsIsPendingNotPass(t *testing.T) {
	s := Compute(nil, nil)
	if s.Verdict == VerdictPass {
		t.Error("expected empty case results to never default to PASS")
	}
}

func TestCompute_NotDeclaredCasesExcludedFromBothRules(t *testing.T) {
	cases := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
		{CaseID: "video_url", Status: model.StatusNotDeclared, CountsInBase22: true},
		{CaseID: "reasoning_effort", Status: model.StatusNotDeclared, CountsInBase22: false},
	}
	metrics := []model.BenchmarkMetric{
		{Name: "throughput_req_s", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "ttft", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "tpot", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "cache_hit_rate", Scope: "overall", BaselineVerdict: model.BaselineOK},
	}
	s := Compute(cases, metrics)
	if s.Verdict != VerdictPass {
		t.Errorf("expected PASS: NOT_DECLARED cases must not block the overall verdict, got %s (rule1=%s rule2=%s)", s.Verdict, s.Rule1.State, s.Rule2.State)
	}
}
