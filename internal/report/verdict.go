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
// cases 是当前套件的完整用例定义（通常直接传 suite.Cases），是"某个用例是否
// 计入 22 项基础分母"的唯一权威来源——caseResults 里同名的 CountsInBase22
// 字段只用作一致性校验，不参与分组，防止两处数据来源不一致时出现"规则 1
// 报告缺失、规则 2 报告多余、报告页面又展示在另一个分桶里"的分裂结果。
//
// cases 为空（未提供套件定义）时，Rule1/Rule2 直接判 PENDING，不静默退化成
// 只看已出现结果的宽松检查——没有套件定义就无法确认 22 项是否齐全，绝不能
// 默认判 OK（这是此前版本的一个真实漏洞：生产入口一旦套件加载出问题、传入
// 空 cases，完整性校验会被整体绕过）。
//
// caseResults/metrics 缺失、重复、或出现非法枚举值时一律不默认判 PASS
// （「不得因缺失数据直接判 FAIL，也不得默认判 PASS」，见设计方案 6.2 节
// 兜底规则原文，这里把同一原则应用到全部四条规则）。
func Compute(cases []suitedef.Case, caseResults []model.CaseResult, metrics []model.BenchmarkMetric) Summary {
	rule1, rule2 := evalRules1And2(cases, caseResults)
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

// caseGroupOf 从套件定义构造 CaseID -> CountsInBase22 的权威映射。
func caseGroupOf(cases []suitedef.Case) map[string]bool {
	m := make(map[string]bool, len(cases))
	for _, c := range cases {
		m[c.ID] = c.CountsInBase22
	}
	return m
}

// validateSuiteCases 校验套件定义本身的完整性：Case ID 不能为空、不能重复。
// caseGroupOf 用 map 构造分组依据，空/重复 ID 会被后一条定义静默覆盖前一条，
// 使期望的用例集合在悄无声息间被压缩——哪怕调用方按规范传入了"非空"的
// cases，22 项/附加能力用例的完整性校验也会因此形同虚设。必须在分组之前
// 单独堵住，而不是指望"非空校验"顺带覆盖。
func validateSuiteCases(cases []suitedef.Case) []string {
	var problems []string
	seen := make(map[string]int, len(cases))
	for i, c := range cases {
		if c.ID == "" {
			problems = append(problems, fmt.Sprintf("第 %d 个用例定义的 id 为空", i))
			continue
		}
		seen[c.ID]++
	}
	for id, n := range seen {
		if n > 1 {
			problems = append(problems, fmt.Sprintf("用例 id %q 在套件定义中重复出现 %d 次", id, n))
		}
	}
	return problems
}

func evalRules1And2(cases []suitedef.Case, caseResults []model.CaseResult) (RuleResult, RuleResult) {
	if len(cases) == 0 {
		noSuite := RuleResult{State: RulePending, Reasons: []string{
			"未提供套件定义（cases 为空），无法校验 22 项基础用例是否完整，不能默认判 OK",
		}}
		return noSuite, noSuite
	}

	if problems := validateSuiteCases(cases); len(problems) > 0 {
		invalid := RuleResult{
			State:   RuleFail,
			Reasons: append([]string{"套件定义本身不合法（存在空/重复的用例 id），无法据此校验用例完整性："}, problems...),
		}
		return invalid, invalid
	}

	groupOf := caseGroupOf(cases)
	var expectedBase22, expectedNonBase22 []string
	for id, base22 := range groupOf {
		if base22 {
			expectedBase22 = append(expectedBase22, id)
		} else {
			expectedNonBase22 = append(expectedNonBase22, id)
		}
	}

	// byID 按套件权威分组归拢结果；CaseResult 自带的 CountsInBase22 只用来做
	// 一致性校验（下面），不用来决定这条结果归到哪个 byID 桶——避免两处数据
	// 来源在分组上产生分歧。
	byID := map[string][]model.CaseResult{}
	var unknownReasons, base22MismatchReasons, nonBase22MismatchReasons []string
	for _, r := range caseResults {
		want, known := groupOf[r.CaseID]
		if !known {
			// 套件里完全找不到这个 CaseID，按定义它不属于任何一个分组，
			// 归到规则 1 报告（22 项完整性是设计方案里最主要的度量维度）。
			unknownReasons = append(unknownReasons, fmt.Sprintf("%s: 不在当前套件的用例列表中，套件定义可能已变更", r.CaseID))
			continue
		}
		if r.CountsInBase22 != want {
			// 归到套件定义所声明的那个分组，而不是无条件挂在规则 1 下——否则
			// 一个附加能力用例（套件定义 CountsInBase22=false）的不一致会被
			// 误报成"22 项基础用例"的问题，误导报告读者去错误的地方定位。
			reason := fmt.Sprintf("%s: 结果自带的分组标记（counts_in_base22=%v）与套件定义（%v）不一致，数据不一致", r.CaseID, r.CountsInBase22, want)
			if want {
				base22MismatchReasons = append(base22MismatchReasons, reason)
			} else {
				nonBase22MismatchReasons = append(nonBase22MismatchReasons, reason)
			}
		}
		byID[r.CaseID] = append(byID[r.CaseID], r)
	}

	rule1 := evalCaseGroup(expectedBase22, byID)
	rule2 := evalCaseGroup(expectedNonBase22, byID)

	if len(unknownReasons) > 0 {
		rule1 = RuleResult{State: combine(rule1.State, RulePending), Reasons: append(rule1.Reasons, unknownReasons...)}
	}
	if len(base22MismatchReasons) > 0 {
		rule1 = RuleResult{State: combine(rule1.State, RuleFail), Reasons: append(rule1.Reasons, base22MismatchReasons...)}
	}
	if len(nonBase22MismatchReasons) > 0 {
		rule2 = RuleResult{State: combine(rule2.State, RuleFail), Reasons: append(rule2.Reasons, nonBase22MismatchReasons...)}
	}

	return rule1, rule2
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

// evalCaseGroup 校验 expectedIDs（某个 CountsInBase22 分组期望包含的全部
// CaseID，来自套件定义）在 byID（已按套件权威分组归拢好的结果，见
// evalRules1And2）里是否完整、状态是否全部通过：
//  1. 完整性——expectedIDs 里的每个用例都必须在 byID 里出现且只出现一次；
//     完全缺失（用例从未产出结果，不是 NOT_DECLARED）判 PENDING；同一用例
//     出现多条结果判 FAIL（数据不一致，比缺失更严重，可能意味着测试流水线
//     本身出了 bug，不能悄悄取其中一条了事）。
//  2. 状态——白名单判定，只有 PASS 算通过；FAIL 判 FAIL；MANUAL_REVIEW/任何
//     无法识别的状态值一律判 PENDING（不能让非法枚举值落进默认分支被当成
//     通过，这是此前版本的一个真实漏洞）；NOT_DECLARED 视为已妥善处理，不
//     参与判定。
//
// 套件外用例、分组标记不一致的检测在 evalRules1And2 里统一做过一次，这里
// 不用重复扫描（byID 本身就只包含套件已知且属于本分组的用例）。
func evalCaseGroup(expectedIDs []string, byID map[string][]model.CaseResult) RuleResult {
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

	for _, id := range expectedIDs {
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
