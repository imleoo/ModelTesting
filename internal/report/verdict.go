// Package report 实现设计方案 08 节「报告生成」：把 P1/P2 产出的
// []model.CaseResult、CAPABILITY_PROFILE、P3 产出的 BenchmarkRun/[]BenchmarkMetric
// 汇总成一份符合 PDF「四、结果回传要求」的 HTML 报告，并按 08 节「验收结论四条
// 规则」自动判定。
package report

import (
	"fmt"

	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/suitedef"
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
	// Rule1：22 项基础用例（CountsInBase22==true 且已声明/固定必过）100% 通过、
	// 且这 22 项本身必须完整出现（不能因为缺失结果而被默默放过），MANUAL_REVIEW
	// 视为未通过（08 节规则 1）。
	Rule1 RuleResult
	// Rule2：已声明但不计入 22 分母的能力用例（当前套件里只有 reasoning_effort.
	// scaling）100% 通过（08 节规则 2；v0.2 曾遗漏这条，声明能力失败时不拖累
	// 总体结论）。
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

// Compute 按 08 节验收结论四条规则计算最终判定。
//
// cases 是当前套件的完整用例定义（通常直接传 suite.Cases），用来校验 22 项
// 基础用例、附加能力用例是否完整出现在 caseResults 里——不传（nil/空）时退化
// 为只检查"已出现的结果"，不做完整性校验（供不关心这一层的单元测试使用；
// cmd/report-cli 等生产入口必须传入真实套件定义，否则漏跑的用例会被静默
// 放过判 OK，这正是本函数要防止的问题）。
//
// caseResults/metrics 缺失、重复、或出现非法枚举值时一律不默认判 PASS
// （「不得因缺失数据直接判 FAIL，也不得默认判 PASS」，见设计方案 6.2 节
// 兜底规则原文，这里把同一原则应用到全部四条规则）。
func Compute(cases []suitedef.Case, caseResults []model.CaseResult, metrics []model.BenchmarkMetric) Summary {
	var expectedBase22, expectedNonBase22 []string
	for _, c := range cases {
		if c.CountsInBase22 {
			expectedBase22 = append(expectedBase22, c.ID)
		} else {
			expectedNonBase22 = append(expectedNonBase22, c.ID)
		}
	}

	rule1 := evalCaseGroup(expectedBase22, caseResults, true)
	rule2 := evalCaseGroup(expectedNonBase22, caseResults, false)
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

// evalCaseGroup 校验某个 CountsInBase22 分组（true=22 项基础用例，
// false=附加能力用例）：
//  1. 完整性——expectedIDs 里的每个用例都必须在 caseResults 里出现且只出现
//     一次；完全缺失（用例从未产出结果，不是 NOT_DECLARED）判 PENDING；同一
//     用例出现多条结果判 FAIL（数据不一致，比缺失更严重，可能意味着测试
//     流水线本身出了 bug，不能悄悄取其中一条了事）。
//  2. 状态——白名单判定，只有 PASS 算通过；FAIL 判 FAIL；MANUAL_REVIEW/任何
//     无法识别的状态值一律判 PENDING（不能让非法枚举值落进默认分支被当成
//     通过，这是此前版本的一个真实漏洞）；NOT_DECLARED 视为已妥善处理，不
//     参与判定。
//  3. caseResults 里如果出现了不在 expectedIDs 里、但 CountsInBase22 与本组
//     一致的用例，说明套件定义和期望列表已经不一致，同样判 PENDING（不能
//     假装没看见）。
//
// expectedIDs 为空时（调用方未传入套件定义）跳过步骤 1/3，只做步骤 2——
// 见 Compute 文档字符串。
func evalCaseGroup(expectedIDs []string, caseResults []model.CaseResult, countsInBase22 bool) RuleResult {
	byID := map[string][]model.CaseResult{}
	for _, r := range caseResults {
		if r.CountsInBase22 == countsInBase22 {
			byID[r.CaseID] = append(byID[r.CaseID], r)
		}
	}

	var reasons []string
	hasFail, hasPending := false, false
	fail := func(format string, a ...any) { hasFail = true; reasons = append(reasons, fmt.Sprintf(format, a...)) }
	pending := func(format string, a ...any) { hasPending = true; reasons = append(reasons, fmt.Sprintf(format, a...)) }

	checkStatus := func(id string, r model.CaseResult) {
		switch r.Status {
		case model.StatusPass, model.StatusNotDeclared:
			// PASS：通过，无需处理；NOT_DECLARED：不计入本组判定。
		case model.StatusFail:
			fail("%s: FAIL（%s）", id, r.FailReason)
		case model.StatusManualReview:
			pending("%s: MANUAL_REVIEW，待人工确认", id)
		default:
			pending("%s: 状态值 %q 无法识别，无法判定", id, r.Status)
		}
	}

	if len(expectedIDs) == 0 {
		// 未传套件定义：退化为只看已出现的结果，不做完整性校验。
		for id, rs := range byID {
			for _, r := range rs {
				checkStatus(id, r)
			}
		}
	} else {
		expectedSet := make(map[string]bool, len(expectedIDs))
		for _, id := range expectedIDs {
			expectedSet[id] = true
			rs, ok := byID[id]
			switch {
			case !ok:
				pending("%s: 缺失结果，用例从未执行或未产出记录", id)
			case len(rs) > 1:
				fail("%s: 出现 %d 条重复结果，数据不一致", id, len(rs))
			default:
				checkStatus(id, rs[0])
			}
		}
		for id := range byID {
			if !expectedSet[id] {
				pending("%s: 不在当前套件的用例列表中，套件定义可能已变更", id)
			}
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
	byName := map[string][]model.BenchmarkMetric{}
	for _, m := range metrics {
		if m.Scope == "overall" {
			byName[m.Name] = append(byName[m.Name], m)
		}
	}

	var reasons []string
	hasFail, hasPending := false, false
	for _, name := range gatedMetricNames {
		ms, ok := byName[name]
		switch {
		case !ok:
			hasPending = true
			reasons = append(reasons, fmt.Sprintf("%s: 缺失该性能指标数据，无法判定", name))
			continue
		case len(ms) > 1:
			hasFail = true
			reasons = append(reasons, fmt.Sprintf("%s: 出现 %d 条重复的 overall 指标记录，数据不一致", name, len(ms)))
			continue
		}
		m := ms[0]
		// 白名单判定：只有明确落在这几个已知取值里的才不阻塞；任何非法/未知
		// 取值（含空字符串）一律判 PENDING，不能被无动作的 default 分支
		// 悄悄当成达标。
		switch m.BaselineVerdict {
		case model.BaselineOK, model.BaselineSameOrder:
			// 达标或同一数量级，符合 6.2 节判定。
		case model.BaselineNotObservable:
			// 6.2 节兜底规则：确认不可观测时移出验收门禁。
		case model.BaselineFail:
			hasFail = true
			reasons = append(reasons, fmt.Sprintf("%s: FAIL（实测偏离基线超过 MAX_DEGRADE_RATIO 倍数）", name))
		default:
			hasPending = true
			reasons = append(reasons, fmt.Sprintf("%s: BaselineVerdict 取值 %q 无法识别，无法判定", name, m.BaselineVerdict))
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
