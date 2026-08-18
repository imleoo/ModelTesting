package report

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/suitedef"
)

// TestRender_AgainstRealP2P3Artifacts 用仓库里真实的 P2 功能测试结果、P3
// 压测结果（对真实 tokenpanel/kimi-k3 网关执行产出，见 reports/kimi-k3/）
// 渲染一次报告，验证 report 包和真实产物的 JSON 形状是匹配的，而不是只对
// 手工构造的最小 fixture 有效。
func TestRender_AgainstRealP2P3Artifacts(t *testing.T) {
	suite, err := suitedef.LoadSuite("../../suites/kimi-k3/suite.v1.json")
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}

	capRaw, err := os.ReadFile("../../suites/kimi-k3/capability_kimi-k3.real.json")
	if err != nil {
		t.Fatalf("read capability: %v", err)
	}
	var capability model.CapabilityProfile
	if err := json.Unmarshal(capRaw, &capability); err != nil {
		t.Fatalf("unmarshal capability: %v", err)
	}

	caseRaw, err := os.ReadFile("../../reports/kimi-k3/run-2026-08-17.json")
	if err != nil {
		t.Fatalf("read case results: %v", err)
	}
	var caseResults []model.CaseResult
	if err := json.Unmarshal(caseRaw, &caseResults); err != nil {
		t.Fatalf("unmarshal case results: %v", err)
	}
	if len(caseResults) == 0 {
		t.Fatal("expected non-empty real case results fixture")
	}

	benchRaw, err := os.ReadFile("../../reports/kimi-k3/benchmark-2026-08-18.json")
	if err != nil {
		t.Fatalf("read benchmark result: %v", err)
	}
	var bench struct {
		Run     model.BenchmarkRun
		Metrics []model.BenchmarkMetric
	}
	if err := json.Unmarshal(benchRaw, &bench); err != nil {
		t.Fatalf("unmarshal benchmark result: %v", err)
	}
	if bench.Run.ID == "" {
		t.Fatal("expected non-empty real BenchmarkRun.ID fixture")
	}

	html, err := Render(Input{
		Environment: Environment{
			ModelID:      "kimi-k3",
			Endpoint:     "https://jiwu.wtgo.com.cn/",
			TestDate:     "2026-08-18",
			GeneratedAt:  "2026-08-18T00:00:00Z",
			SuiteID:      suite.SuiteID,
			SuiteVersion: suite.SuiteVersion,
		},
		Capability:       capability,
		Cases:            suite.Cases,
		CaseResults:      caseResults,
		BenchmarkRun:     &bench.Run,
		BenchmarkMetrics: bench.Metrics,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	for _, want := range []string{"kimi-k3", "验收结论", "功能测试结果表", "性能测试结果", bench.Run.RawStdoutRef} {
		if !strings.Contains(html, want) {
			t.Errorf("expected rendered HTML to contain %q", want)
		}
	}
	// raw_command 里的 API Key 已在 P3 阶段脱敏（RedactCommand），报告只是
	// 原样引用该字段，这里再断言一次真实密钥不会因为报告渲染而意外出现。
	if strings.Contains(html, "sk-0da28b5ba62018fbce86919f840df2e2a141a16d62109a9816c25a2a1ffd97ac") {
		t.Fatal("rendered report must never contain the real API key")
	}
	// 防止 P4 review round-1 发现的回归：真实套件里唯一的附加能力用例 ID
	// 是 "reasoning_effort.scaling"（不是猜测出来的 "reasoning_effort"），
	// 必须出现在"附加能力用例结果"章节里，不能被静默漏掉。
	if !strings.Contains(html, "附加能力用例结果") || !strings.Contains(html, "reasoning_effort.scaling") {
		t.Error("expected reasoning_effort.scaling to be visible in the additional-capability section")
	}
}

func TestRender_NoBenchmarkDataStillRenders(t *testing.T) {
	html, err := Render(Input{
		Environment: Environment{ModelID: "m", Endpoint: "e", TestDate: "d", GeneratedAt: "g"},
		CaseResults: []model.CaseResult{
			{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: true},
		},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(html, "压测尚未执行") {
		t.Error("expected explicit note when benchmark data is absent, not a silently empty section")
	}
	if !strings.Contains(html, "PENDING_MANUAL_REVIEW") {
		t.Error("expected overall verdict to be pending, not a fabricated PASS, when perf data is missing")
	}
}

// TestRender_BucketingFollowsSuiteDefinitionNotResultField 防止 P4 review
// round-2 发现的回归：套件定义说某用例是 22 项基础用例，但 CaseResult 自带
// 的 CountsInBase22 字段却是 false（数据不一致）——报告页面的分桶必须以
// 套件定义为准（展示在"功能测试结果表"的 22 项表格里），不能被结果自带的
// 错误字段带偏到"附加能力用例结果"表格。
func TestRender_BucketingFollowsSuiteDefinitionNotResultField(t *testing.T) {
	html, err := Render(Input{
		Environment: Environment{ModelID: "m", Endpoint: "e", TestDate: "d", GeneratedAt: "g"},
		Cases:       []suitedef.Case{{ID: "stream_integrity", Name: "Stream 完整性", CountsInBase22: true}},
		CaseResults: []model.CaseResult{
			// 套件说这是 base22，结果自己却标了 false。
			{CaseID: "stream_integrity", Status: model.StatusPass, CountsInBase22: false},
		},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(html, "Stream 完整性") {
		t.Fatal("expected the case to render somewhere in the report")
	}
	if strings.Contains(html, "附加能力用例结果") {
		t.Error("expected a suite-declared base22 case to never land in the additional-capability section, even if CaseResult.CountsInBase22 disagrees")
	}
}
