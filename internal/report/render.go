package report

import (
	"bytes"
	"fmt"
	"html/template"

	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/suitedef"
)

// Environment 是报告「环境信息」章节的数据（08 节：模型 ID、API 端点、测试日期）。
type Environment struct {
	ModelID      string
	Endpoint     string
	TestDate     string // 测试执行日期，如 "2026-08-18"
	GeneratedAt  string // 报告生成时间，由调用方传入（不在包内调用 time.Now，保持纯函数可测试）
	SuiteID      string
	SuiteVersion string
}

// Input 是生成一份报告所需的全部数据：P1/P2 功能测试结果 + CAPABILITY_PROFILE +
// P3 压测结果（BenchmarkRun 为 nil 表示压测尚未执行，报告仍可生成，只是性能章节
// 与验收结论规则 3 会显式标注"压测未执行"，不会伪造判定）。
type Input struct {
	Environment      Environment
	Capability       model.CapabilityProfile
	Cases            []suitedef.Case // 用于展示用例名称/类别，可为空（退化为只显示 case_id）
	CaseResults      []model.CaseResult
	BenchmarkRun     *model.BenchmarkRun
	BenchmarkMetrics []model.BenchmarkMetric
}

type caseView struct {
	model.CaseResult
	Name     string
	Category string
}

type renderData struct {
	Env         Environment
	Capability  model.CapabilityProfile
	Base22      []caseView
	NotDeclared []caseView
	// Additional 是「已声明但不计入 22 分母」的能力用例（当前套件里是
	// reasoning_effort.scaling）。按 CountsInBase22==false 分组，不按具体
	// case_id 字符串匹配——套件定义里这类用例的 ID 可能不是 "reasoning_effort"
	// 这个猜测出来的名字（真实套件里是 "reasoning_effort.scaling"），按 ID
	// 硬编码曾经导致这类用例被静默漏掉，不出现在报告任何一个可见分桶里。
	Additional       []caseView
	AllCaseResults   []caseView
	Base22Total      int
	Base22Pass       int
	BenchmarkRun     *model.BenchmarkRun
	OverallMetrics   []model.BenchmarkMetric
	PerRoundMetrics  []model.BenchmarkMetric
	HasBenchmarkData bool
	Summary          Summary
	VerdictLabel     string
}

var verdictLabels = map[Verdict]string{
	VerdictPass:                "通过（PASS）",
	VerdictFail:                "未通过（FAIL）",
	VerdictPendingManualReview: "待人工确认（PENDING_MANUAL_REVIEW）",
}

var statusLabels = map[model.CaseStatus]string{
	model.StatusPass:         "PASS",
	model.StatusFail:         "FAIL",
	model.StatusNotDeclared:  "NOT_DECLARED",
	model.StatusManualReview: "MANUAL_REVIEW",
}

// Render 生成完整的单页 HTML 报告（内联 CSS，无外部依赖，可直接用浏览器打开
// 或打印为 PDF，对应设计方案 10.1 节「服务端渲染 HTML，PDF 通过浏览器打印
// 生成」的首版方案）。
func Render(in Input) (string, error) {
	nameByID := map[string]suitedef.Case{}
	for _, c := range in.Cases {
		nameByID[c.ID] = c
	}

	toView := func(r model.CaseResult) caseView {
		v := caseView{CaseResult: r}
		if c, ok := nameByID[r.CaseID]; ok {
			v.Name, v.Category = c.Name, c.Category
		}
		return v
	}

	var base22, notDeclared, additional, all []caseView
	base22Total, base22Pass := 0, 0
	for _, r := range in.CaseResults {
		v := toView(r)
		all = append(all, v)
		switch {
		case r.Status == model.StatusNotDeclared:
			notDeclared = append(notDeclared, v)
		case !r.CountsInBase22:
			additional = append(additional, v)
		default:
			base22 = append(base22, v)
			base22Total++
			if r.Status == model.StatusPass {
				base22Pass++
			}
		}
	}

	var overall, perRound []model.BenchmarkMetric
	for _, m := range in.BenchmarkMetrics {
		if m.Scope == "overall" {
			overall = append(overall, m)
		} else {
			perRound = append(perRound, m)
		}
	}

	summary := Compute(in.Cases, in.CaseResults, in.BenchmarkMetrics)

	data := renderData{
		Env:              in.Environment,
		Capability:       in.Capability,
		Base22:           base22,
		NotDeclared:      notDeclared,
		Additional:       additional,
		AllCaseResults:   all,
		Base22Total:      base22Total,
		Base22Pass:       base22Pass,
		BenchmarkRun:     in.BenchmarkRun,
		OverallMetrics:   overall,
		PerRoundMetrics:  perRound,
		HasBenchmarkData: in.BenchmarkRun != nil,
		Summary:          summary,
		VerdictLabel:     verdictLabels[summary.Verdict],
	}

	tmpl, err := template.New("report").Funcs(template.FuncMap{
		"statusLabel": func(s model.CaseStatus) string { return statusLabels[s] },
		"pct":         func(f float64) string { return fmt.Sprintf("%.1f%%", f*100) },
	}).Parse(reportTemplate)
	if err != nil {
		return "", fmt.Errorf("parse report template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute report template: %w", err)
	}
	return buf.String(), nil
}
