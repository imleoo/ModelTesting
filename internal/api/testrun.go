package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"github.com/leoobai/modeltestbed/internal/benchmark"
	"github.com/leoobai/modeltestbed/internal/client"
	"github.com/leoobai/modeltestbed/internal/engine"
	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/render"
	"github.com/leoobai/modeltestbed/internal/report"
	"github.com/leoobai/modeltestbed/internal/store"
	"github.com/leoobai/modeltestbed/internal/suitedef"
)

type launchTestRunRequest struct {
	ModelID       string `json:"model_id" binding:"required"`
	SuiteID       string `json:"suite_id" binding:"required"`
	APIKey        string `json:"api_key"`
	TotalSessions int    `json:"total_sessions"`
}

// LaunchTestRun 对应 09 节执行时序的入口："选择模型 + 套件，发起测试"。
// 立即返回 202 + 刚创建的 TestRun（status=PENDING），实际执行在后台
// goroutine 里跑（功能测试 → 视结果决定是否解锁压测 → 生成报告），前端轮询
// GET /api/test-runs/:id 查看进度。
func (cfg *Config) LaunchTestRun(c *gin.Context) {
	var req launchTestRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	m, err := cfg.Store.GetModel(req.ModelID)
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "model not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if !cfg.orchestrateMu.TryLock() {
		c.JSON(http.StatusConflict, gin.H{"error": "另一个测试任务正在执行，本系统首版单一时刻只支持一个任务（设计方案 10.1 节：不引入任务队列）"})
		return
	}

	run, err := cfg.Store.CreateTestRun(model.TestRun{
		ModelID: m.ID, SuiteID: req.SuiteID, Status: model.RunPending, StartedAt: nowRFC3339(),
	})
	if err != nil {
		cfg.orchestrateMu.Unlock()
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	totalSessions := req.TotalSessions
	if totalSessions <= 0 {
		totalSessions = cfg.DefaultTotalSessions
	}

	go func() {
		defer cfg.orchestrateMu.Unlock()
		cfg.orchestrate(context.Background(), run, m, req.APIKey, totalSessions)
	}()

	c.JSON(http.StatusAccepted, run)
}

// orchestrate 是 09 节执行时序的完整落地：功能测试全部用例跑完 → 用
// report.Compute 的规则 1/2（22 项基础用例 + 已声明附加能力用例，不含性能
// 门禁）判断是否解锁压测 → 解锁则跑压测 → 无论是否解锁都生成报告（未解锁
// 时报告的性能章节显式标注"压测未执行"，不伪造判定，见 P4 报告生成阶段的
// 既有行为）。任何一步出现执行错误（不是业务判定失败，是网络异常/引擎
// panic 之类）都会把 TestRun 标记为 FAILED 并提前退出。
func (cfg *Config) orchestrate(ctx context.Context, run model.TestRun, m model.Model, apiKey string, totalSessions int) {
	fail := func(err error) {
		run.Status = model.RunFailed
		run.ErrorMessage = err.Error()
		run.FinishedAt = nowRFC3339()
		if uerr := cfg.Store.UpdateTestRun(run); uerr != nil {
			log.Printf("api: test_run %s: 标记 FAILED 时更新数据库也失败: %v（原始错误: %v）", run.ID, uerr, err)
		}
	}

	// orchestrate 跑在独立的后台 goroutine 里（LaunchTestRun 用 go func()
	// 启动），Gin 的 Recovery 中间件只保护 HTTP 请求处理那条 goroutine，管不
	// 到这里——不加这个 recover，engine.RunCase 内部任何一次 panic（比如某个
	// 断言函数对畸形响应做了不安全的类型断言）都会直接崩溃整个 goroutine，
	// 严重时能拖垮整个 api-server 进程，任务永远卡在 RUNNING_FUNCTIONAL，
	// 而不是像文档承诺的那样被标记 FAILED。
	defer func() {
		if r := recover(); r != nil {
			fail(fmt.Errorf("panic: %v", r))
		}
	}()

	run.Status = model.RunRunningFunctional
	if err := cfg.Store.UpdateTestRun(run); err != nil {
		log.Printf("api: test_run %s: 更新为 RUNNING_FUNCTIONAL 失败: %v", run.ID, err)
		return
	}

	suitePath, err := safeJoinResolved(cfg.SuitesRoot, run.SuiteID)
	if err != nil {
		fail(fmt.Errorf("非法的 suite_id: %w", err))
		return
	}
	suite, err := suitedef.LoadSuite(suitePath)
	if err != nil {
		fail(fmt.Errorf("加载套件定义失败: %w", err))
		return
	}

	materials, err := suitedef.LoadMaterialsManifestForSuite(cfg.RepoRoot, suite)
	if err != nil {
		fail(fmt.Errorf("加载素材清单失败: %w", err))
		return
	}

	e := &engine.Engine{
		Client:     client.New(m.EndpointViaTokenpanel, apiKey),
		Suite:      suite,
		Materials:  materials,
		Capability: m.Capability,
		RenderCtx: render.Context{
			ModelKey:        m.ModelKey,
			APIKey:          apiKey,
			Fixtures:        suite.Fixtures,
			Materials:       materials,
			MaterialBaseURL: cfg.MaterialsBaseURL,
		},
	}

	caseResults := make([]model.CaseResult, 0, len(suite.Cases))
	for _, c := range suite.Cases {
		caseResults = append(caseResults, e.RunCase(ctx, c))
	}

	// m.ModelKey 已经在 store.CreateModel 里被 modelKeyPattern 白名单校验过
	// （不含 "/" 或 ".."），字符串层面天然无法逃出 ReportsRoot；但如果
	// ReportsRoot 内部本身存在一个名字恰好匹配某个合法 model_key 的符号
	// 链接（不是通过这个 API 能直接制造的场景，但属于运维/部署层面可能
	// 引入的风险），modelDir 实际指向的真实路径仍可能在 ReportsRoot 之外，
	// 所以创建后仍要做一次 resolveWithinRoot 校验，不能只靠白名单单点防御。
	modelDir := filepath.Join(cfg.ReportsRoot, m.ModelKey)
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		fail(fmt.Errorf("创建结果目录失败: %w", err))
		return
	}
	if err := resolveWithinRoot(cfg.ReportsRoot, modelDir); err != nil {
		fail(fmt.Errorf("结果目录校验失败: %w", err))
		return
	}

	caseResultsPath := filepath.Join(modelDir, run.ID+"-case-results.json")
	if err := writeJSONFile(caseResultsPath, caseResults); err != nil {
		fail(err)
		return
	}
	run.CaseResultsPath = caseResultsPath

	// 09 节 alt 分支："22 项基础用例全部通过且无未清空的 MANUAL_REVIEW"才
	// 解锁压测——这正是 report.Compute 规则 1（22 项基础用例）和规则 2
	// （已声明的附加能力用例，如 reasoning_effort）合起来的判定，复用已经
	// 过 4 轮 Codex review 的判定逻辑，不重新发明一套计数规则。这里先不传
	// metrics（压测还没跑），规则 3 自然是 PENDING，不影响这次只看规则 1/2
	// 的解锁判断。
	gate := report.Compute(suite.Cases, caseResults, nil)
	unlocked := gate.Rule1.State == report.RuleOK && gate.Rule2.State == report.RuleOK

	var benchmarkRun *model.BenchmarkRun
	var benchmarkMetrics []model.BenchmarkMetric

	if !unlocked {
		run.Status = model.RunFunctionalBlocked
	} else {
		run.Status = model.RunRunningBenchmark
		if err := cfg.Store.UpdateTestRun(run); err != nil {
			log.Printf("api: test_run %s: 更新为 RUNNING_BENCHMARK 失败: %v", run.ID, err)
			return
		}

		result, err := cfg.runBenchmark(ctx, run, m, apiKey, totalSessions, modelDir)
		if err != nil {
			fail(fmt.Errorf("压测执行失败: %w", err))
			return
		}

		benchmarkResultPath := filepath.Join(modelDir, run.ID+"-benchmark-result.json")
		if err := writeJSONFile(benchmarkResultPath, result); err != nil {
			fail(err)
			return
		}
		run.BenchmarkResultPath = benchmarkResultPath
		benchmarkRun = &result.Run
		benchmarkMetrics = result.Metrics
		run.Status = model.RunCompleted
	}

	final := report.Compute(suite.Cases, caseResults, benchmarkMetrics)
	html, err := report.Render(report.Input{
		Environment: report.Environment{
			ModelID:      m.ModelKey,
			Endpoint:     m.EndpointViaTokenpanel,
			TestDate:     run.StartedAt,
			GeneratedAt:  nowRFC3339(),
			SuiteID:      suite.SuiteID,
			SuiteVersion: suite.SuiteVersion,
		},
		Capability:       m.Capability,
		Cases:            suite.Cases,
		CaseResults:      caseResults,
		BenchmarkRun:     benchmarkRun,
		BenchmarkMetrics: benchmarkMetrics,
	})
	if err != nil {
		fail(fmt.Errorf("生成报告失败: %w", err))
		return
	}

	reportPath := filepath.Join(modelDir, run.ID+"-report.html")
	if err := os.WriteFile(reportPath, []byte(html), 0o644); err != nil {
		fail(fmt.Errorf("写入报告文件失败: %w", err))
		return
	}

	run.FinishedAt = nowRFC3339()
	if err := cfg.Store.UpdateTestRun(run); err != nil {
		log.Printf("api: test_run %s: 写入最终状态失败: %v", run.ID, err)
		return
	}

	if _, err := cfg.Store.CreateReport(model.Report{
		TestRunID:   run.ID,
		GeneratedAt: run.FinishedAt,
		Verdict:     string(final.Verdict),
		HTMLRef:     reportPath,
	}); err != nil {
		log.Printf("api: test_run %s: 写入 Report 记录失败: %v", run.ID, err)
	}
}

func (cfg *Config) runBenchmark(ctx context.Context, run model.TestRun, m model.Model, apiKey string, totalSessions int, modelDir string) (benchmark.RunResult, error) {
	params, err := cfg.benchmarkParams(totalSessions)
	if err != nil {
		return benchmark.RunResult{}, fmt.Errorf("构造压测参数失败: %w", err)
	}

	logPath := filepath.Join(modelDir, run.ID+"-benchmark.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return benchmark.RunResult{}, fmt.Errorf("创建压测日志文件失败: %w", err)
	}
	defer logFile.Close()

	return benchmark.Run(ctx, benchmark.RunConfig{
		Params:         params,
		BaseURL:        m.EndpointViaTokenpanel,
		APIKey:         apiKey,
		ModelKey:       m.ModelKey,
		RequestTimeout: cfg.RequestTimeout,
		RawCommand:     fmt.Sprintf("api-server test-run %s", run.ID),
		ToolVersion:    "modeltestbed-benchmark-self-built-v1",
		DatasetVersion: "synthetic-percentile-reconstruction-v1",
		Seed:           1,
		LogWriter:      logFile,
		LogRef:         logPath,
	})
}

func (cfg *Config) GetTestRun(c *gin.Context) {
	run, err := cfg.Store.GetTestRun(c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "test run not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, run)
}

func (cfg *Config) ListTestRuns(c *gin.Context) {
	modelID := c.Query("model_id")
	if modelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model_id query 参数必填"})
		return
	}
	runs, err := cfg.Store.ListTestRunsForModel(modelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, runs)
}

// GetTestRunCaseResults 供「结果详情」页面展示功能测试逐条明细（08 节
// 报告结构的数据来源之一，这里直接把 testbed-cli/orchestrate 落盘的
// []model.CaseResult JSON 原样吐给前端，不需要再包一层）。
func (cfg *Config) GetTestRunCaseResults(c *gin.Context) {
	run, err := cfg.Store.GetTestRun(c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "test run not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if run.CaseResultsPath == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "功能测试结果尚未产出", "status": run.Status})
		return
	}
	c.File(run.CaseResultsPath)
}

// GetTestRunReport 供「报告下载」页面直接内嵌/下载报告 HTML（08 节报告
// 生成阶段产出的自包含单页 HTML，浏览器可以直接打印成 PDF）。
func (cfg *Config) GetTestRunReport(c *gin.Context) {
	rpt, err := cfg.Store.GetReportForTestRun(c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "报告尚未生成或测试任务不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.File(rpt.HTMLRef)
}
