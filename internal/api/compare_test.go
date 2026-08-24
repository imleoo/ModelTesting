package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/leoobai/modeltestbed/internal/api"
	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/store"
)

// writeCaseResultsFixture 直接落盘一份 []model.CaseResult JSON 文件，绕开真实
// 编排流程——CompareTestRuns 只读这个文件，不关心它是怎么产出的，用最小 fixture
// 换取测试速度。
func writeCaseResultsFixture(t *testing.T, dir string, results []model.CaseResult) string {
	t.Helper()
	raw, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("marshal case results: %v", err)
	}
	path := filepath.Join(dir, "case-results.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write case results: %v", err)
	}
	return path
}

// createCompletedTestRun 注册一个 Provider+Model（可指定 modelKey），并造出一条
// 已经"跑完"的 TestRun（CaseResultsPath 指向手写的 fixture 文件），不走真实的
// orchestrate 编排。
func createCompletedTestRun(t *testing.T, s *store.Store, providerName, modelKey, suiteID string, results []model.CaseResult) model.TestRun {
	t.Helper()
	p, err := s.CreateProvider(model.Provider{Name: providerName})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	m, err := s.CreateModel(model.Model{
		ProviderID:            p.ID,
		ModelKey:              modelKey,
		EndpointViaTokenpanel: "http://example.invalid/" + providerName,
		Capability: model.CapabilityProfile{
			ImageBase64:             true,
			VideoBase64:             true,
			ThinkingToggleMethods:   []string{"enable_thinking"},
			DefaultThinkingBehavior: "thinks_by_default",
		},
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	run, err := s.CreateTestRun(model.TestRun{
		ModelID: m.ID, SuiteID: suiteID, Status: model.RunCompleted, StartedAt: "2026-08-24T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("CreateTestRun: %v", err)
	}
	run.CaseResultsPath = writeCaseResultsFixture(t, t.TempDir(), results)
	run.FinishedAt = "2026-08-24T00:01:00Z"
	if err := s.UpdateTestRun(run); err != nil {
		t.Fatalf("UpdateTestRun: %v", err)
	}
	return run
}

func passingCaseResult() []model.CaseResult {
	return []model.CaseResult{
		{
			CaseID: "connectivity.basic", Status: model.StatusPass, Attempts: 1, PassedAttempts: 1, PassRate: 1,
			CaseAttempts:   []model.CaseAttempt{{LatencyMS: 100, Passed: true}},
			CountsInBase22: true,
		},
	}
}

func failingCaseResult() []model.CaseResult {
	return []model.CaseResult{
		{
			CaseID: "connectivity.basic", Status: model.StatusFail, Attempts: 1, PassedAttempts: 0,
			FailReason:     "empty content",
			CaseAttempts:   []model.CaseAttempt{{LatencyMS: 300, Passed: false}},
			CountsInBase22: true,
		},
	}
}

func TestCompareTestRuns_TwoRunsSameModelKeyAlignByCaseID(t *testing.T) {
	cfg, s := newTestConfig(t, "http://example.invalid")
	writeFixtureSuite(t, cfg.SuitesRoot, "mini", miniSuiteJSON)

	runA := createCompletedTestRun(t, s, "ProviderA", "mini-model", "mini/suite.v1.json", passingCaseResult())
	runB := createCompletedTestRun(t, s, "ProviderB", "mini-model", "mini/suite.v1.json", failingCaseResult())

	r := api.NewRouter(cfg)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/test-runs/compare?run_ids="+runA.ID+","+runB.ID, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ModelKey   string `json:"model_key"`
		SuiteID    string `json:"suite_id"`
		Runs       []struct {
			RunID string `json:"run_id"`
		} `json:"runs"`
		Categories []struct {
			Category string `json:"category"`
			Cases    []struct {
				CaseID string `json:"case_id"`
				Cells  map[string]struct {
					Status string `json:"status"`
				} `json:"cells"`
			} `json:"cases"`
		} `json:"categories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ModelKey != "mini-model" {
		t.Errorf("expected model_key=mini-model, got %q", resp.ModelKey)
	}
	if len(resp.Runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(resp.Runs))
	}
	if len(resp.Categories) != 1 || len(resp.Categories[0].Cases) != 1 {
		t.Fatalf("expected 1 category with 1 case, got %+v", resp.Categories)
	}
	cells := resp.Categories[0].Cases[0].Cells
	if cells[runA.ID].Status != "PASS" {
		t.Errorf("expected runA cell status PASS, got %q", cells[runA.ID].Status)
	}
	if cells[runB.ID].Status != "FAIL" {
		t.Errorf("expected runB cell status FAIL, got %q", cells[runB.ID].Status)
	}
}

func TestCompareTestRuns_TooFewRunIDsReturns400(t *testing.T) {
	cfg, s := newTestConfig(t, "http://example.invalid")
	writeFixtureSuite(t, cfg.SuitesRoot, "mini", miniSuiteJSON)
	runA := createCompletedTestRun(t, s, "ProviderA", "mini-model", "mini/suite.v1.json", passingCaseResult())

	r := api.NewRouter(cfg)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/test-runs/compare?run_ids="+runA.ID, nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCompareTestRuns_UnknownRunIDReturns404(t *testing.T) {
	cfg, s := newTestConfig(t, "http://example.invalid")
	writeFixtureSuite(t, cfg.SuitesRoot, "mini", miniSuiteJSON)
	runA := createCompletedTestRun(t, s, "ProviderA", "mini-model", "mini/suite.v1.json", passingCaseResult())

	r := api.NewRouter(cfg)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/test-runs/compare?run_ids="+runA.ID+",does-not-exist", nil))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCompareTestRuns_MismatchedModelKeyReturns400(t *testing.T) {
	cfg, s := newTestConfig(t, "http://example.invalid")
	writeFixtureSuite(t, cfg.SuitesRoot, "mini", miniSuiteJSON)
	runA := createCompletedTestRun(t, s, "ProviderA", "mini-model", "mini/suite.v1.json", passingCaseResult())
	runB := createCompletedTestRun(t, s, "ProviderB", "other-model", "mini/suite.v1.json", passingCaseResult())

	r := api.NewRouter(cfg)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/test-runs/compare?run_ids="+runA.ID+","+runB.ID, nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCompareTestRuns_MismatchedSuiteIDReturns400(t *testing.T) {
	cfg, s := newTestConfig(t, "http://example.invalid")
	writeFixtureSuite(t, cfg.SuitesRoot, "mini", miniSuiteJSON)
	writeFixtureSuite(t, cfg.SuitesRoot, "mini-fail", miniFailingSuiteJSON)
	runA := createCompletedTestRun(t, s, "ProviderA", "mini-model", "mini/suite.v1.json", passingCaseResult())
	runB := createCompletedTestRun(t, s, "ProviderB", "mini-model", "mini-fail/suite.v1.json", passingCaseResult())

	r := api.NewRouter(cfg)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/test-runs/compare?run_ids="+runA.ID+","+runB.ID, nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCompareTestRuns_RunWithoutCaseResultsReturns409(t *testing.T) {
	cfg, s := newTestConfig(t, "http://example.invalid")
	writeFixtureSuite(t, cfg.SuitesRoot, "mini", miniSuiteJSON)
	runA := createCompletedTestRun(t, s, "ProviderA", "mini-model", "mini/suite.v1.json", passingCaseResult())

	m := createProviderAndModel(t, s, "http://example.invalid/pending")
	pendingRun, err := s.CreateTestRun(model.TestRun{
		ModelID: m.ID, SuiteID: "mini/suite.v1.json", Status: model.RunRunningFunctional, StartedAt: "2026-08-24T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("CreateTestRun: %v", err)
	}

	r := api.NewRouter(cfg)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/test-runs/compare?run_ids="+runA.ID+","+pendingRun.ID, nil))

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}
