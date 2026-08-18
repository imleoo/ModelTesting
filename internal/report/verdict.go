// Package report 实现设计方案 08 节「报告生成」：把 P1/P2 产出的
// []model.CaseResult、CAPABILITY_PROFILE、P3 产出的 BenchmarkRun/[]BenchmarkMetric
// 汇总成一份符合 PDF「四、结果回传要求」的 HTML 报告，并按 08 节「验收结论四条
// 规则」自动判定。
package report

import (
	"fmt"

	"github.com/leoobai/modeltestbed/internal/model"
)

// Verdict 是报告「验收结论」章节的三态结果（08 节表格：全自动判定，但
// MANUAL_REVIEW 未清空前锁定为「待人工确认」，不是简单的 PASS/FAIL 二元）。
type Verdict string

const (
	VerdictPass                Verdict = "PASS"
	VerdictFail                Verdict = "FAIL"
	VerdictPendingManualReview Verdict = "PENDING_MANUAL_REVIEW"
)

// RuleState 是单条规则的判定状态。FAIL 比 PENDING 更严重——同时存在确定失败项
// 和待复核项时，总体判定为 FAIL 而不是「待人工确认」（确定失败是比"尚待确认"
// 更强的结论，不能被弱化掩盖）。
type RuleState string

const (
	RuleOK      RuleState = "OK"
	RuleFail    RuleState = "FAIL"
	RulePending RuleState = "PENDING"
)

// RuleResult 是单条规则的判定结果 + 人类可读的具体阻塞原因，供报告「验收结论」
// 章节展示（不能只给一个 PASS/FAIL，必须说清楚是哪个用例/指标导致的）。
type RuleResult struct {
	State   RuleState
	Reasons []string
}

// Summary 是「验收结论」章节的完整判定结果。
type Summary struct {
	// Rule1：22 项基础用例（CountsInBase22==true 且已声明/固定必过）100% 通过，
	// MANUAL_REVIEW 视为未通过（08 节规则 1）。
	Rule1 RuleResult
	// Rule2：已声明但不计入 22 分母的能力用例（当前套件里只有 reasoning_effort）
	// 100% 通过（08 节规则 2；v0.2 曾遗漏这条，声明能力失败时不拖累总体结论）。
	Rule2 RuleResult
	// Rule3：有 PDF 基线且可观测的性能指标（throughput_req_s / ttft P50 /
	// tpot P50 / cache_hit_rate）满足 6.2 节单向判定规则（08 节规则 3）；
	// NOT_OBSERVABLE 按 6.2 节兜底规则移出本条门禁，不算 FAIL 也不算 PENDING。
	Rule3   RuleResult
	Verdict Verdict
}

// gatedMetricNames 是 08 节规则 3「有 PDF 基线且可观测」的性能指标名单，均取
// Scope=="overall" 的记录。
var gatedMetricNames = []string{"throughput_req_s", "ttft", "tpot", "cache_hit_rate"}

// Compute 按 08 节验收结论四条规则计算最终判定。caseResults 为空、或
// metrics 缺失某个门禁指标时，一律不默认判 PASS，落到 PENDING（「不得因缺失
// 数据直接判 FAIL，也不得默认判 PASS」，见设计方案 6.2 节兜底规则原文）。
func Compute(caseResults []model.CaseResult, metrics []model.BenchmarkMetric) Summary {
	rule1 := evalRule1(caseResults)
	rule2 := evalRule2(caseResults)
	rule3 := evalRule3(metrics)

	overall := combine(rule1.State, rule2.State, rule3.State)
	var verdict Verdict
	switch overall {
	case RuleFail:
		verdict = VerdictFail
	case RulePending:
		verdict = VerdictPendingManualReview
	default:
		verdict = VerdictPass
	}

	return Summary{Rule1: rule1, Rule2: rule2, Rule3: rule3, Verdict: verdict}
}

// combine 取多条规则里最严重的状态：FAIL > PENDING > OK。
func combine(states ...RuleState) RuleState {
	worst := RuleOK
	for _, s := range states {
		if s == RuleFail {
			return RuleFail
		}
		if s == RulePending {
			worst = RulePending
		}
	}
	return worst
}

func evalRule1(caseResults []model.CaseResult) RuleResult {
	var counted []model.CaseResult
	for _, r := range caseResults {
		if r.CountsInBase22 && r.Status != model.StatusNotDeclared {
			counted = append(counted, r)
		}
	}
	if len(counted) == 0 {
		return RuleResult{State: RulePending, Reasons: []string{"未提供 22 项基础用例的结果数据，无法判定"}}
	}
	return evalCaseGroup(counted)
}

// evalRule2 覆盖「已声明但不计入 22 分母」的能力用例（当前套件里只有
// reasoning_effort）。CountsInBase22==true 的能力用例（image_url/video_url/
// function/思考开关各方式）已经在 Rule1 里按 base22 分母检查过，这里刻意
// 只看 CountsInBase22==false 的部分，避免和 Rule1 重复判定同一批用例。
func evalRule2(caseResults []model.CaseResult) RuleResult {
	var counted []model.CaseResult
	for _, r := range caseResults {
		if !r.CountsInBase22 && r.Status != model.StatusNotDeclared {
			counted = append(counted, r)
		}
	}
	if len(counted) == 0 {
		// 没有任何"声明但不计入 22 分母"的能力用例被执行（比如 reasoning_effort
		// 未声明），这是合法状态，不是数据缺失——直接判 OK。
		return RuleResult{State: RuleOK}
	}
	return evalCaseGroup(counted)
}

func evalCaseGroup(results []model.CaseResult) RuleResult {
	var reasons []string
	hasFail, hasPending := false, false
	for _, r := range results {
		switch r.Status {
		case model.StatusFail:
			hasFail = true
			reasons = append(reasons, fmt.Sprintf("%s: FAIL（%s）", r.CaseID, r.FailReason))
		case model.StatusManualReview:
			hasPending = true
			reasons = append(reasons, fmt.Sprintf("%s: MANUAL_REVIEW，待人工确认", r.CaseID))
		}
	}
	switch {
	case hasFail:
		return RuleResult{State: RuleFail, Reasons: reasons}
	case hasPending:
		return RuleResult{State: RulePending, Reasons: reasons}
	default:
		return RuleResult{State: RuleOK}
	}
}

func evalRule3(metrics []model.BenchmarkMetric) RuleResult {
	byName := map[string]model.BenchmarkMetric{}
	for _, m := range metrics {
		if m.Scope == "overall" {
			byName[m.Name] = m
		}
	}

	var reasons []string
	hasFail, hasPending := false, false
	for _, name := range gatedMetricNames {
		m, ok := byName[name]
		if !ok {
			hasPending = true
			reasons = append(reasons, fmt.Sprintf("%s: 缺失该性能指标数据，无法判定", name))
			continue
		}
		switch m.BaselineVerdict {
		case model.BaselineFail:
			hasFail = true
			reasons = append(reasons, fmt.Sprintf("%s: FAIL（实测偏离基线超过 %.0fx）", name, 2.0))
		case model.BaselineNotObservable:
			// 6.2 节兜底规则：确认不可观测时移出验收门禁，既不算 FAIL 也不算
			// PENDING（不是"缺数据判不了"，是"已经确认这条指标测不到"）。
		}
	}
	switch {
	case hasFail:
		return RuleResult{State: RuleFail, Reasons: reasons}
	case hasPending:
		return RuleResult{State: RulePending, Reasons: reasons}
	default:
		return RuleResult{State: RuleOK}
	}
}
