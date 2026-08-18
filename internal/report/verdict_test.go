package report

import (
	"testing"

	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/suitedef"
)

// casesFor 构造测试用的套件定义：base22 里的 ID 计入 22 项分母，
// additional 里的 ID 不计入（如 reasoning_effort.scaling）。
func casesFor(base22 []string, additional []string) []suitedef.Case {
	var cs []suitedef.Case
	for _, id := range base22 {
		cs = append(cs, suitedef.Case{ID: id, CountsInBase22: true})
	}
	for _, id := range additional {
		cs = append(cs, suitedef.Case{ID: id, CountsInBase22: false})
	}
	return cs
}

func TestCompute_AllPassIsVerdictPass(t *testing.T) {
	cases := casesFor([]string{"stream_integrity"}, []string{"reasoning_effort"})
	results := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
		{CaseID: "reasoning_effort", Status: model.StatusPass, CountsInBase22: false},
	}
	metrics := []model.BenchmarkMetric{
		{Name: "throughput_req_s", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "ttft", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "tpot", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "cache_hit_rate", Scope: "overall", BaselineVerdict: model.BaselineOK},
	}
	s := Compute(cases, results, metrics)
	if s.Verdict != VerdictPass {
		t.Errorf("expected PASS, got %s (rule1=%s reasons=%v rule2=%s reasons=%v rule3=%s reasons=%v)",
			s.Verdict, s.Rule1.State, s.Rule1.Reasons, s.Rule2.State, s.Rule2.Reasons, s.Rule3.State, s.Rule3.Reasons)
	}
}

func TestCompute_Base22FailIsVerdictFail(t *testing.T) {
	cases := casesFor([]string{"stream_integrity"}, nil)
	results := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusFail, CountsInBase22: true, FailReason: "boom"},
	}
	s := Compute(cases, results, nil)
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
	cases := casesFor([]string{"stream_integrity"}, []string{"reasoning_effort"})
	results := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
		{CaseID: "reasoning_effort", Status: model.StatusFail, CountsInBase22: false, FailReason: "low/high 无区分度"},
	}
	s := Compute(cases, results, nil)
	if s.Verdict != VerdictFail {
		t.Errorf("expected FAIL when declared reasoning_effort fails even though it's outside base22, got %s", s.Verdict)
	}
	if s.Rule2.State != RuleFail {
		t.Errorf("expected Rule2 FAIL, got %s", s.Rule2.State)
	}
}

func TestCompute_ManualReviewWithoutFailIsPending(t *testing.T) {
	cases := casesFor([]string{"video_url"}, nil)
	results := []model.CaseResult{
		{CaseID: "video_url", Status: model.StatusManualReview, CountsInBase22: true},
	}
	s := Compute(cases, results, nil)
	if s.Verdict != VerdictPendingManualReview {
		t.Errorf("expected PENDING_MANUAL_REVIEW, got %s", s.Verdict)
	}
}

// TestCompute_FailOutranksManualReview 验证"确定失败"比"待复核"更严重：
// 两者同时出现时，总体结论必须是 FAIL，不能被弱化成"待人工确认"。
func TestCompute_FailOutranksManualReview(t *testing.T) {
	cases := casesFor([]string{"stream_integrity", "video_url"}, nil)
	results := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusFail, CountsInBase22: true, FailReason: "boom"},
		{CaseID: "video_url", Status: model.StatusManualReview, CountsInBase22: true},
	}
	s := Compute(cases, results, nil)
	if s.Verdict != VerdictFail {
		t.Errorf("expected FAIL to outrank PENDING, got %s", s.Verdict)
	}
}

func TestCompute_NotObservableCacheHitDoesNotBlock(t *testing.T) {
	cases := casesFor([]string{"stream_integrity"}, nil)
	results := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
	}
	metrics := []model.BenchmarkMetric{
		{Name: "throughput_req_s", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "ttft", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "tpot", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "cache_hit_rate", Scope: "overall", BaselineVerdict: model.BaselineNotObservable},
	}
	s := Compute(cases, results, metrics)
	if s.Verdict != VerdictPass {
		t.Errorf("expected PASS (NOT_OBSERVABLE cache_hit_rate moved out of the gate per 6.2 节), got %s: rule3 reasons=%v", s.Verdict, s.Rule3.Reasons)
	}
}

func TestCompute_MissingBenchmarkMetricIsPending(t *testing.T) {
	cases := casesFor([]string{"stream_integrity"}, nil)
	results := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
	}
	s := Compute(cases, results, nil) // 没有任何压测指标
	if s.Verdict != VerdictPendingManualReview {
		t.Errorf("expected PENDING_MANUAL_REVIEW when benchmark data is entirely missing, got %s", s.Verdict)
	}
}

func TestCompute_BenchmarkMetricFailBlocksOverall(t *testing.T) {
	cases := casesFor([]string{"stream_integrity"}, nil)
	results := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
	}
	metrics := []model.BenchmarkMetric{
		{Name: "throughput_req_s", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "ttft", Scope: "overall", BaselineVerdict: model.BaselineFail},
		{Name: "tpot", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "cache_hit_rate", Scope: "overall", BaselineVerdict: model.BaselineOK},
	}
	s := Compute(cases, results, metrics)
	if s.Verdict != VerdictFail {
		t.Errorf("expected FAIL when a gated benchmark metric is FAIL, got %s", s.Verdict)
	}
}

func TestCompute_EmptyCaseResultsIsPendingNotPass(t *testing.T) {
	s := Compute(nil, nil, nil)
	if s.Verdict == VerdictPass {
		t.Error("expected empty case results to never default to PASS")
	}
}

func TestCompute_NotDeclaredCasesExcludedFromBothRules(t *testing.T) {
	cases := casesFor([]string{"stream_integrity", "video_url"}, []string{"reasoning_effort"})
	results := []model.CaseResult{
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
	s := Compute(cases, results, metrics)
	if s.Verdict != VerdictPass {
		t.Errorf("expected PASS: NOT_DECLARED cases must not block the overall verdict, got %s (rule1=%s rule2=%s)", s.Verdict, s.Rule1.State, s.Rule2.State)
	}
}

// --- P4 review round 1 发现的三处阻塞性问题的回归测试 ---

// TestCompute_MissingBase22CaseIsPendingNotOK 防止 P4 review round-1 发现的
// 回归：只有 1 条基础用例结果、其余 21 条缺失时，Rule1 不能因为"已出现的都
// PASS"就判 OK——必须能看出还有用例根本没跑。
func TestCompute_MissingBase22CaseIsPendingNotOK(t *testing.T) {
	expected := casesFor([]string{"stream_integrity", "usage_nonstream", "usage_stream"}, nil)
	// 只提供了 1 条结果，其余 2 条完全缺失。
	results := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
	}
	s := Compute(expected, results, nil)
	if s.Rule1.State != RulePending {
		t.Errorf("expected Rule1 PENDING when base22 cases are missing, got %s (reasons=%v)", s.Rule1.State, s.Rule1.Reasons)
	}
	if s.Verdict == VerdictPass {
		t.Error("expected missing base22 cases to block a PASS verdict")
	}
}

// TestCompute_DuplicateCaseResultIsFail 防止同一用例产出两条互相矛盾的结果时
// 被静默取其一放过——数据不一致应该判 FAIL 而不是被忽略。
func TestCompute_DuplicateCaseResultIsFail(t *testing.T) {
	expected := casesFor([]string{"stream_integrity"}, nil)
	results := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
		{CaseID: "stream_integrity", Status: model.StatusFail, CountsInBase22: true, FailReason: "boom"},
	}
	s := Compute(expected, results, nil)
	if s.Rule1.State != RuleFail {
		t.Errorf("expected Rule1 FAIL on duplicate case results, got %s", s.Rule1.State)
	}
}

// TestCompute_UnknownCaseStatusIsPendingNotOK 防止非法/未知的 CaseStatus 落进
// 无动作的 default 分支被当成通过。
func TestCompute_UnknownCaseStatusIsPendingNotOK(t *testing.T) {
	expected := casesFor([]string{"stream_integrity"}, nil)
	results := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.CaseStatus("SOMETHING_WEIRD"), CountsInBase22: true},
	}
	s := Compute(expected, results, nil)
	if s.Rule1.State != RulePending {
		t.Errorf("expected Rule1 PENDING on unknown status value, got %s", s.Rule1.State)
	}
}

// TestCompute_UnknownBaselineVerdictIsPendingNotOK 防止非法/未知的
// BaselineVerdict（如空字符串）落进无动作的 default 分支被当成性能达标。
func TestCompute_UnknownBaselineVerdictIsPendingNotOK(t *testing.T) {
	cases := casesFor([]string{"stream_integrity"}, nil)
	results := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
	}
	metrics := []model.BenchmarkMetric{
		{Name: "throughput_req_s", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "ttft", Scope: "overall", BaselineVerdict: model.BaselineVerdict("")},
		{Name: "tpot", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "cache_hit_rate", Scope: "overall", BaselineVerdict: model.BaselineOK},
	}
	s := Compute(cases, results, metrics)
	if s.Rule3.State != RulePending {
		t.Errorf("expected Rule3 PENDING on empty/unknown BaselineVerdict, got %s (reasons=%v)", s.Rule3.State, s.Rule3.Reasons)
	}
	if s.Verdict == VerdictPass {
		t.Error("expected unknown BaselineVerdict to block a PASS verdict")
	}
}

// TestCompute_DuplicateOverallMetricIsFail 防止同名 overall 指标出现两条记录
// 时被后一条静默覆盖前一条。
func TestCompute_DuplicateOverallMetricIsFail(t *testing.T) {
	cases := casesFor([]string{"stream_integrity"}, nil)
	results := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
	}
	metrics := []model.BenchmarkMetric{
		{Name: "throughput_req_s", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "throughput_req_s", Scope: "overall", BaselineVerdict: model.BaselineFail},
		{Name: "ttft", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "tpot", Scope: "overall", BaselineVerdict: model.BaselineOK},
		{Name: "cache_hit_rate", Scope: "overall", BaselineVerdict: model.BaselineOK},
	}
	s := Compute(cases, results, metrics)
	if s.Rule3.State != RuleFail {
		t.Errorf("expected Rule3 FAIL on duplicate overall metric records, got %s", s.Rule3.State)
	}
}

// TestCompute_UnexpectedExtraCaseIsPending 防止套件定义变更后，caseResults
// 里混入了不在当前套件里的用例却被悄悄忽略。
func TestCompute_UnexpectedExtraCaseIsPending(t *testing.T) {
	expected := casesFor([]string{"stream_integrity"}, nil)
	results := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
		{CaseID: "some_new_case_not_in_suite", Status: model.StatusPass, CountsInBase22: true},
	}
	s := Compute(expected, results, nil)
	if s.Rule1.State != RulePending {
		t.Errorf("expected Rule1 PENDING when an unexpected extra case appears, got %s", s.Rule1.State)
	}
}

// --- P4 review round 2 发现的两处阻塞性问题的回归测试 ---

// TestCompute_EmptySuiteAlwaysPendingRegardlessOfResults 防止 P4 review
// round-2 发现的回归：cases 为空时（如套件加载出问题）不能静默退化成"只看
// 已出现结果是否全 PASS"——哪怕 caseResults 里全是 PASS，没有套件定义就
// 无法确认 22 项是否齐全，必须判 PENDING。
func TestCompute_EmptySuiteAlwaysPendingRegardlessOfResults(t *testing.T) {
	results := []model.CaseResult{
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
	}
	s := Compute(nil, results, nil)
	if s.Rule1.State != RulePending || s.Rule2.State != RulePending {
		t.Errorf("expected Rule1/Rule2 PENDING when no suite is provided, got rule1=%s rule2=%s", s.Rule1.State, s.Rule2.State)
	}
	if s.Verdict == VerdictPass {
		t.Error("expected empty suite to never default to PASS even if all present results are PASS")
	}
}

// TestCompute_MismatchedCountsInBase22IsFail 防止 P4 review round-2 发现的
// 回归：套件定义说某用例是 22 项基础用例（CountsInBase22=true），但结果自带
// 的同名字段却是 false（数据不一致）——不能被两处分组逻辑各自为政地悄悄
// 放过，必须明确判 FAIL 并且仍按套件权威分组纳入 Rule1 校验。
func TestCompute_MismatchedCountsInBase22IsFail(t *testing.T) {
	cases := casesFor([]string{"stream_integrity"}, nil)
	results := []model.CaseResult{
		// 套件说这是 base22，结果自己却标了 false——数据不一致。
		{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: false},
	}
	s := Compute(cases, results, nil)
	if s.Rule1.State != RuleFail {
		t.Errorf("expected Rule1 FAIL on CountsInBase22 mismatch, got %s (reasons=%v)", s.Rule1.State, s.Rule1.Reasons)
	}
}
