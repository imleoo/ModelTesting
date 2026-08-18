// report-cli 是 P4「报告生成」的命令行入口：把 P1/P2 产出的功能测试结果
// （testbed-cli -out）、CAPABILITY_PROFILE、P3 产出的压测结果（benchmark-cli
// -out，可选）汇总成一份符合设计方案 08 节结构的 HTML 报告。
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/report"
	"github.com/leoobai/modeltestbed/internal/suitedef"
)

// benchmarkResultFile 只承接 benchmark-cli -out 结果文件里报告需要的两个
// 字段（Run/Metrics），不引入整个 internal/benchmark 包（那里还有 Outcomes
// 等报告用不到的逐请求明细，没必要让 report-cli 依赖压测引擎的内部类型）。
type benchmarkResultFile struct {
	Run     model.BenchmarkRun
	Metrics []model.BenchmarkMetric
}

func main() {
	suitePath := flag.String("suite", "suites/kimi-k3/suite.v1.json", "套件定义文件路径（用于展示用例名称/类别）")
	caseResultsPath := flag.String("case-results", "", "功能测试结果 JSON 路径（testbed-cli -out 产物，必填）")
	capabilityPath := flag.String("capability", "", "CAPABILITY_PROFILE JSON 路径（必填）")
	benchmarkPath := flag.String("benchmark", "", "压测结果 JSON 路径（benchmark-cli -out 产物；留空则报告只含功能测试部分，验收结论规则 3 标注为待人工确认）")
	modelID := flag.String("model-id", "", "模型 ID（必填）")
	endpoint := flag.String("endpoint", "", "被测网关 API 端点（必填）")
	testDate := flag.String("test-date", "", "测试执行日期，如 2026-08-18（必填）")
	outPath := flag.String("out", "", "报告 HTML 输出路径（必填）")
	flag.Parse()

	if *caseResultsPath == "" || *capabilityPath == "" || *modelID == "" || *endpoint == "" || *testDate == "" || *outPath == "" {
		log.Fatal("必须指定 -case-results -capability -model-id -endpoint -test-date -out")
	}

	suite, err := suitedef.LoadSuite(*suitePath)
	if err != nil {
		log.Fatalf("加载套件定义失败: %v", err)
	}
	if len(suite.Cases) == 0 {
		// suitedef.LoadSuite 只做 JSON 反序列化，不校验 cases 是否为空——
		// 一份 {"cases":[]} 也能加载成功。空套件传给 report.Compute 会让
		// 22 项完整性校验整体判 PENDING（不会误判 OK，见 verdict.go），
		// 但报告生成本身没有意义，在此直接拒绝，而不是产出一份"待人工确认"
		// 的空壳报告。
		log.Fatalf("套件定义 %s 不含任何用例（cases 为空），无法生成有意义的报告", *suitePath)
	}

	caseResults, err := loadCaseResults(*caseResultsPath)
	if err != nil {
		log.Fatalf("加载功能测试结果失败: %v", err)
	}

	capability, err := loadCapability(*capabilityPath)
	if err != nil {
		log.Fatalf("加载能力声明失败: %v", err)
	}
	if err := model.ValidateCapabilityProfile(capability); err != nil {
		log.Fatalf("能力声明不合法: %v", err)
	}

	var benchmarkRun *model.BenchmarkRun
	var benchmarkMetrics []model.BenchmarkMetric
	if *benchmarkPath != "" {
		bench, err := loadBenchmark(*benchmarkPath)
		if err != nil {
			log.Fatalf("加载压测结果失败: %v", err)
		}
		benchmarkRun = &bench.Run
		benchmarkMetrics = bench.Metrics
	}

	html, err := report.Render(report.Input{
		Environment: report.Environment{
			ModelID:      *modelID,
			Endpoint:     *endpoint,
			TestDate:     *testDate,
			GeneratedAt:  time.Now().Format(time.RFC3339),
			SuiteID:      suite.SuiteID,
			SuiteVersion: suite.SuiteVersion,
		},
		Capability:       capability,
		Cases:            suite.Cases,
		CaseResults:      caseResults,
		BenchmarkRun:     benchmarkRun,
		BenchmarkMetrics: benchmarkMetrics,
	})
	if err != nil {
		log.Fatalf("生成报告失败: %v", err)
	}

	if err := os.WriteFile(*outPath, []byte(html), 0o644); err != nil {
		log.Fatalf("写入报告文件失败: %v", err)
	}
	log.Printf("报告已写入 %s", *outPath)
}

func loadCaseResults(path string) ([]model.CaseResult, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var results []model.CaseResult
	if err := json.Unmarshal(raw, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func loadCapability(path string) (model.CapabilityProfile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return model.CapabilityProfile{}, err
	}
	var c model.CapabilityProfile
	if err := json.Unmarshal(raw, &c); err != nil {
		return model.CapabilityProfile{}, err
	}
	return c, nil
}

func loadBenchmark(path string) (benchmarkResultFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return benchmarkResultFile{}, err
	}
	var b benchmarkResultFile
	if err := json.Unmarshal(raw, &b); err != nil {
		return benchmarkResultFile{}, err
	}
	return b, nil
}
