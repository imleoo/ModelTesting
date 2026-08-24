package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/store"
	"github.com/leoobai/modeltestbed/internal/suitedef"
)

const (
	minCompareRuns = 2
	maxCompareRuns = 8
)

// compareResponse 是「结果对比」页面消费的并排对比数据：把多个已完成 TestRun
// 的 CaseResult（各自落盘在 CaseResultsPath 指向的 JSON 文件里）按 case_id
// 对齐到同一张表。这是纯读接口，不落盘任何新数据，也不经过
// Config.orchestrateMu——和「同一时刻只能跑一个测试任务」的编排互斥语义无关。
type compareResponse struct {
	ModelKey   string            `json:"model_key"`
	SuiteID    string            `json:"suite_id"`
	Runs       []compareRunMeta  `json:"runs"`
	Categories []compareCategory `json:"categories"`
}

type compareRunMeta struct {
	RunID        string `json:"run_id"`
	ModelID      string `json:"model_id"`
	ProviderID   string `json:"provider_id"`
	ProviderName string `json:"provider_name"`
	Endpoint     string `json:"endpoint_via_tokenpanel"`
	Status       string `json:"status"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at,omitempty"`
}

type compareCategory struct {
	Category string           `json:"category"`
	Cases    []compareCaseRow `json:"cases"`
}

type compareCaseRow struct {
	CaseID         string                     `json:"case_id"`
	Name           string                     `json:"name"`
	CountsInBase22 bool                       `json:"counts_in_base22"`
	Cells          map[string]compareCaseCell `json:"cells"` // key = run_id
}

// compareCaseCell 的 Status 沿用 model.CaseStatus 的取值，外加一个本包私有的
// "MISSING" 兜底值——只在 suite_id 相同但套件文件在两次跑测之间被人工改过、
// 导致某个 case_id 在其中一个 run 的结果里找不到时出现，防止这种边界情况
// 被静默吞掉或让 handler panic。
type compareCaseCell struct {
	Status         string  `json:"status"`
	Attempts       int     `json:"attempts"`
	PassedAttempts int     `json:"passed_attempts"`
	AvgLatencyMS   float64 `json:"avg_latency_ms"`
	FailReason     string  `json:"fail_reason,omitempty"`
}

// CompareTestRuns 是「结果对比」页面的数据来源：GET
// /api/test-runs/compare?run_ids=id1,id2,... 把多个已完成 TestRun 的用例结果
// 按 case_id 对齐并排返回。限定同一 model_key（不支持跨模型对比，用例集不同
// 没法对齐）、同一 suite_id（两者独立校验，不能假设 model_key 相同就意味着
// suite_id 也相同——一个来自 Model 登记，一个来自发起 TestRun 时传的参数）。
func (cfg *Config) CompareTestRuns(c *gin.Context) {
	runIDs := parseRunIDs(c.Query("run_ids"))
	if len(runIDs) < minCompareRuns {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("run_ids 至少需要 %d 个才能对比", minCompareRuns)})
		return
	}
	if len(runIDs) > maxCompareRuns {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("run_ids 最多支持 %d 个同时对比", maxCompareRuns)})
		return
	}

	type runBundle struct {
		run      model.TestRun
		m        model.Model
		provider model.Provider
	}
	bundles := make([]runBundle, 0, len(runIDs))
	for _, id := range runIDs {
		run, err := cfg.Store.GetTestRun(id)
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "test run not found: " + id})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if run.CaseResultsPath == "" {
			c.JSON(http.StatusConflict, gin.H{"error": "TestRun " + id + " 尚未产出功能测试结果", "status": run.Status})
			return
		}

		m, err := cfg.Store.GetModel(run.ModelID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("加载 TestRun %s 关联的模型失败: %v", id, err)})
			return
		}
		provider, err := cfg.Store.GetProvider(m.ProviderID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("加载 TestRun %s 关联的供应商失败: %v", id, err)})
			return
		}

		bundles = append(bundles, runBundle{run: run, m: m, provider: provider})
	}

	modelKey := bundles[0].m.ModelKey
	suiteID := bundles[0].run.SuiteID
	for _, b := range bundles[1:] {
		if b.m.ModelKey != modelKey {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("所选 TestRun 的 model_key 不一致，无法对比: %q vs %q（不支持跨 model_key 对比，用例集不同无法按 case_id 对齐）", modelKey, b.m.ModelKey),
			})
			return
		}
		if b.run.SuiteID != suiteID {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("所选 TestRun 的 suite_id 不一致，无法对比: %q vs %q", suiteID, b.run.SuiteID),
			})
			return
		}
	}

	suitePath, err := safeJoinResolved(cfg.SuitesRoot, suiteID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "非法的 suite_id: " + err.Error()})
		return
	}
	suite, err := suitedef.LoadSuite(suitePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载套件定义失败: " + err.Error()})
		return
	}

	runs := make([]compareRunMeta, 0, len(bundles))
	resultsByRun := make(map[string]map[string]model.CaseResult, len(bundles))
	for _, b := range bundles {
		runs = append(runs, compareRunMeta{
			RunID:        b.run.ID,
			ModelID:      b.m.ID,
			ProviderID:   b.provider.ID,
			ProviderName: b.provider.Name,
			Endpoint:     b.m.EndpointViaTokenpanel,
			Status:       string(b.run.Status),
			StartedAt:    b.run.StartedAt,
			FinishedAt:   b.run.FinishedAt,
		})

		raw, err := os.ReadFile(b.run.CaseResultsPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("读取 TestRun %s 的用例结果失败: %v", b.run.ID, err)})
			return
		}
		var caseResults []model.CaseResult
		if err := json.Unmarshal(raw, &caseResults); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("解析 TestRun %s 的用例结果失败: %v", b.run.ID, err)})
			return
		}
		byCaseID := make(map[string]model.CaseResult, len(caseResults))
		for _, cr := range caseResults {
			byCaseID[cr.CaseID] = cr
		}
		resultsByRun[b.run.ID] = byCaseID
	}

	categories := make([]compareCategory, 0)
	categoryIndex := make(map[string]int)
	for _, sc := range suite.Cases {
		idx, ok := categoryIndex[sc.Category]
		if !ok {
			idx = len(categories)
			categoryIndex[sc.Category] = idx
			categories = append(categories, compareCategory{Category: sc.Category})
		}

		row := compareCaseRow{
			CaseID:         sc.ID,
			Name:           sc.Name,
			CountsInBase22: sc.CountsInBase22,
			Cells:          make(map[string]compareCaseCell, len(bundles)),
		}
		for _, b := range bundles {
			cr, ok := resultsByRun[b.run.ID][sc.ID]
			if !ok {
				row.Cells[b.run.ID] = compareCaseCell{Status: "MISSING"}
				continue
			}
			row.Cells[b.run.ID] = compareCaseCell{
				Status:         string(cr.Status),
				Attempts:       cr.Attempts,
				PassedAttempts: cr.PassedAttempts,
				AvgLatencyMS:   avgLatencyMS(cr.CaseAttempts),
				FailReason:     cr.FailReason,
			}
		}
		categories[idx].Cases = append(categories[idx].Cases, row)
	}

	c.JSON(http.StatusOK, compareResponse{
		ModelKey:   modelKey,
		SuiteID:    suiteID,
		Runs:       runs,
		Categories: categories,
	})
}

// parseRunIDs 把逗号分隔的 run_ids 参数拆成去重、去空白、保序的列表。
func parseRunIDs(raw string) []string {
	seen := make(map[string]bool)
	ids := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		id := strings.TrimSpace(part)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// avgLatencyMS 是 CaseAttempts[].LatencyMS 的算术平均，无尝试记录时置 0
// （而不是让调用方各自处理除零)。
func avgLatencyMS(attempts []model.CaseAttempt) float64 {
	if len(attempts) == 0 {
		return 0
	}
	var sum int64
	for _, a := range attempts {
		sum += a.LatencyMS
	}
	return float64(sum) / float64(len(attempts))
}
