// testbed-cli 是 P1「无 UI 调用器」：按套件定义逐条执行用例，产出 CaseResult/CaseAttempt
// 留痕。用于对接真实 tokenpanel（P2）或对接 mock 服务器验证引擎自身正确性（P1 验收）。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/leoobai/modeltestbed/internal/client"
	"github.com/leoobai/modeltestbed/internal/engine"
	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/render"
	"github.com/leoobai/modeltestbed/internal/suitedef"
)

func main() {
	suitePath := flag.String("suite", "suites/kimi-k3/suite.v1.json", "套件定义文件路径")
	baseURL := flag.String("base-url", "", "被测网关 base URL（如 http://127.0.0.1:8081）")
	apiKey := flag.String("api-key", "", "测试用 API Key")
	modelKey := flag.String("model-key", "", "被测模型 model_key")
	capabilityPath := flag.String("capability", "", "CAPABILITY_PROFILE JSON 文件路径")
	materialsBaseURL := flag.String("materials-base-url", "http://127.0.0.1:8080", "素材托管服务 base URL")
	repoRoot := flag.String("repo-root", ".", "仓库根目录（用于解析 materials_manifest 相对路径）")
	outPath := flag.String("out", "", "结果 JSON 输出路径（留空则只打印摘要）")
	flag.Parse()

	if *baseURL == "" || *modelKey == "" {
		log.Fatal("必须指定 -base-url 与 -model-key")
	}

	suite, err := suitedef.LoadSuite(*suitePath)
	if err != nil {
		log.Fatalf("加载套件定义失败: %v", err)
	}

	materials, err := suitedef.LoadMaterialsManifestForSuite(*repoRoot, suite)
	if err != nil {
		log.Fatalf("加载素材清单失败: %v", err)
	}

	capability := model.CapabilityProfile{}
	if *capabilityPath != "" {
		raw, err := os.ReadFile(*capabilityPath)
		if err != nil {
			log.Fatalf("读取能力声明文件失败: %v", err)
		}
		if err := json.Unmarshal(raw, &capability); err != nil {
			log.Fatalf("解析能力声明文件失败: %v", err)
		}
		if err := model.ValidateCapabilityProfile(capability); err != nil {
			log.Fatalf("能力声明不合法: %v", err)
		}
	}

	e := &engine.Engine{
		Client:     client.New(*baseURL, *apiKey),
		Suite:      suite,
		Materials:  materials,
		Capability: capability,
		RenderCtx: render.Context{
			ModelKey:        *modelKey,
			APIKey:          *apiKey,
			Fixtures:        suite.Fixtures,
			Materials:       materials,
			MaterialBaseURL: *materialsBaseURL,
		},
	}

	ctx := context.Background()
	results := make([]model.CaseResult, 0, len(suite.Cases))
	for _, c := range suite.Cases {
		r := e.RunCase(ctx, c)
		results = append(results, r)
		fmt.Printf("%-45s %-13s %s\n", c.ID, r.Status, summarize(r))
	}

	printSummary(results)

	if *outPath != "" {
		out, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			log.Fatalf("序列化结果失败: %v", err)
		}
		if err := os.WriteFile(*outPath, out, 0o644); err != nil {
			log.Fatalf("写入结果文件失败: %v", err)
		}
	}
}

func summarize(r model.CaseResult) string {
	if r.Status == model.StatusNotDeclared {
		return r.FailReason
	}
	if r.Attempts > 0 {
		return fmt.Sprintf("%d/%d passed", r.PassedAttempts, r.Attempts)
	}
	if r.FailReason != "" {
		return r.FailReason
	}
	return ""
}

func printSummary(results []model.CaseResult) {
	base22Total, base22Pass, manualReview := 0, 0, 0
	for _, r := range results {
		// 05 节口径：22 项分母只统计 counts_in_base22 的用例，reasoning_effort
		// 等附加用例单独展示，不掺进这个分母；NOT_DECLARED 也不计入分母。
		if !r.CountsInBase22 || r.Status == model.StatusNotDeclared {
			continue
		}
		base22Total++
		switch r.Status {
		case model.StatusPass:
			base22Pass++
		case model.StatusManualReview:
			manualReview++
		}
	}
	fmt.Printf("\n=== 汇总: 22 项基础用例 %d/%d 通过", base22Pass, base22Total)
	if manualReview > 0 {
		fmt.Printf("，另有 %d 项待人工复核（MANUAL_REVIEW，未计入通过数）", manualReview)
	}
	fmt.Println(" ===")
}
