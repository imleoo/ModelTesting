package store_test

import (
	"errors"
	"testing"

	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	// file::memory: 每个连接独立数据库；:memory: 配合 SetMaxOpenConns(1)（见
	// store.go）保证测试全程只用同一个连接，数据不会因为连接池切换而丢失。
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func validCapability() model.CapabilityProfile {
	return model.CapabilityProfile{
		ImageBase64:             true,
		VideoBase64:             true,
		ThinkingToggleMethods:   []string{"enable_thinking"},
		DefaultThinkingBehavior: "thinks_by_default",
	}
}

func TestStore_ProviderModelTestRunReport_HappyPath(t *testing.T) {
	s := openTestStore(t)

	p, err := s.CreateProvider(model.Provider{Name: "MoonshotAI", Contact: "ops@example.com"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	if p.ID == "" {
		t.Fatal("expected CreateProvider to assign an ID")
	}

	m, err := s.CreateModel(model.Model{
		ProviderID:            p.ID,
		ModelKey:              "kimi-k3",
		EndpointViaTokenpanel: "https://jiwu.wtgo.com.cn/",
		Capability:            validCapability(),
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if m.ID == "" {
		t.Fatal("expected CreateModel to assign an ID")
	}
	if m.Capability.ModelID != m.ID {
		t.Errorf("expected stored capability.model_id to be backfilled to %q, got %q", m.ID, m.Capability.ModelID)
	}

	got, err := s.GetModel(m.ID)
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if got.ModelKey != "kimi-k3" || !got.Capability.ImageBase64 {
		t.Errorf("GetModel round-trip mismatch: %+v", got)
	}

	run, err := s.CreateTestRun(model.TestRun{ModelID: m.ID, SuiteID: "kimi-k3-v1", StartedAt: "2026-08-18T00:00:00Z"})
	if err != nil {
		t.Fatalf("CreateTestRun: %v", err)
	}
	if run.Status != model.RunPending {
		t.Errorf("expected default status PENDING, got %s", run.Status)
	}

	run.Status = model.RunCompleted
	run.FinishedAt = "2026-08-18T00:10:00Z"
	run.CaseResultsPath = "reports/kimi-k3/run.json"
	run.BenchmarkResultPath = "reports/kimi-k3/benchmark.json"
	if err := s.UpdateTestRun(run); err != nil {
		t.Fatalf("UpdateTestRun: %v", err)
	}

	got2, err := s.GetTestRun(run.ID)
	if err != nil {
		t.Fatalf("GetTestRun: %v", err)
	}
	if got2.Status != model.RunCompleted || got2.CaseResultsPath == "" {
		t.Errorf("GetTestRun after update mismatch: %+v", got2)
	}

	runs, err := s.ListTestRunsForModel(m.ID)
	if err != nil {
		t.Fatalf("ListTestRunsForModel: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 test run, got %d", len(runs))
	}

	report, err := s.CreateReport(model.Report{TestRunID: run.ID, GeneratedAt: "2026-08-18T00:10:01Z", Verdict: "FAIL", HTMLRef: "reports/kimi-k3/report.html"})
	if err != nil {
		t.Fatalf("CreateReport: %v", err)
	}
	gotReport, err := s.GetReportForTestRun(run.ID)
	if err != nil {
		t.Fatalf("GetReportForTestRun: %v", err)
	}
	if gotReport.ID != report.ID || gotReport.Verdict != "FAIL" {
		t.Errorf("GetReportForTestRun mismatch: %+v", gotReport)
	}
}

// TestStore_CreateModel_RejectsPathTraversalModelKey 防止 P4 API 服务
// review round-1 发现的回归：internal/api 会直接拿 ModelKey 当文件系统
// 目录名用（ReportsRoot/<model_key>/...），CreateModel 必须在数据写入前
// 就拒绝形如 "../../etc" 的 model_key，不能让污染数据进库后才在别的层去
// 补救。
func TestStore_CreateModel_RejectsPathTraversalModelKey(t *testing.T) {
	s := openTestStore(t)
	p, err := s.CreateProvider(model.Provider{Name: "X"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	for _, bad := range []string{"../evil", "../../etc/passwd", "/absolute", "a/b", ".hidden", "a:b"} {
		_, err := s.CreateModel(model.Model{ProviderID: p.ID, ModelKey: bad, EndpointViaTokenpanel: "e", Capability: validCapability()})
		if err == nil {
			t.Errorf("expected CreateModel to reject model_key %q", bad)
		}
	}
}

func TestStore_CreateModel_RejectsUnknownProvider(t *testing.T) {
	s := openTestStore(t)
	_, err := s.CreateModel(model.Model{ProviderID: "does-not-exist", ModelKey: "k", EndpointViaTokenpanel: "e", Capability: validCapability()})
	if err == nil {
		t.Fatal("expected error for unknown provider_id")
	}
}

func TestStore_CreateModel_RejectsInvalidCapability(t *testing.T) {
	s := openTestStore(t)
	p, err := s.CreateProvider(model.Provider{Name: "X"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	_, err = s.CreateModel(model.Model{ProviderID: p.ID, ModelKey: "k", EndpointViaTokenpanel: "e", Capability: model.CapabilityProfile{}})
	if err == nil {
		t.Fatal("expected error for capability missing thinking_toggle_methods (model.ValidateCapabilityProfile)")
	}
}

func TestStore_CreateTestRun_RejectsUnknownModel(t *testing.T) {
	s := openTestStore(t)
	_, err := s.CreateTestRun(model.TestRun{ModelID: "does-not-exist", SuiteID: "s", StartedAt: "t"})
	if err == nil {
		t.Fatal("expected error for unknown model_id")
	}
}

func TestStore_GetModel_NotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.GetModel("nope")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_UpdateTestRun_NotFound(t *testing.T) {
	s := openTestStore(t)
	err := s.UpdateTestRun(model.TestRun{ID: "nope", Status: model.RunCompleted})
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_GetReportForTestRun_NotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.GetReportForTestRun("nope")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// --- P4 store review round 1 发现的阻塞性问题的回归测试 ---

// TestStore_CreateReport_RejectsUnknownTestRun 防止 review round-1 发现的
// 回归：SQLite 默认不强制 REFERENCES 外键，CreateReport 必须在应用层自己
// 校验 test_run_id 存在，否则会产出悬空外键的 Report 记录。
func TestStore_CreateReport_RejectsUnknownTestRun(t *testing.T) {
	s := openTestStore(t)
	_, err := s.CreateReport(model.Report{TestRunID: "does-not-exist", GeneratedAt: "t", Verdict: "PASS", HTMLRef: "r.html"})
	if err == nil {
		t.Fatal("expected error for unknown test_run_id")
	}
}

func setupModel(t *testing.T, s *store.Store) model.Model {
	t.Helper()
	p, err := s.CreateProvider(model.Provider{Name: "X"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	m, err := s.CreateModel(model.Model{ProviderID: p.ID, ModelKey: "k", EndpointViaTokenpanel: "e", Capability: validCapability()})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	return m
}

// TestStore_CreateTestRun_RejectsInvalidStatus 防止非法状态字符串（不在
// model.TestRunStatus 枚举内）被静默写入，破坏后续状态机分支判断。
func TestStore_CreateTestRun_RejectsInvalidStatus(t *testing.T) {
	s := openTestStore(t)
	m := setupModel(t, s)
	_, err := s.CreateTestRun(model.TestRun{ModelID: m.ID, SuiteID: "s", StartedAt: "t", Status: model.TestRunStatus("SOMETHING_WEIRD")})
	if err == nil {
		t.Fatal("expected error for invalid TestRunStatus")
	}
}

// TestStore_UpdateTestRun_RejectsInvalidStatus 同上，覆盖更新路径。
func TestStore_UpdateTestRun_RejectsInvalidStatus(t *testing.T) {
	s := openTestStore(t)
	m := setupModel(t, s)
	run, err := s.CreateTestRun(model.TestRun{ModelID: m.ID, SuiteID: "s", StartedAt: "t"})
	if err != nil {
		t.Fatalf("CreateTestRun: %v", err)
	}
	run.Status = model.TestRunStatus("SOMETHING_WEIRD")
	if err := s.UpdateTestRun(run); err == nil {
		t.Fatal("expected error for invalid TestRunStatus")
	}
}

// TestStore_ListProviders_EmptyIsEmptyNotError 确认空库场景返回空切片而不是
// 报错，调用方（未来的 API handler）不需要为"从未注册过供应商"单独处理错误。
func TestStore_ListProviders_EmptyIsEmptyNotError(t *testing.T) {
	s := openTestStore(t)
	got, err := s.ListProviders()
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestStore_ListModels_EmptyIsEmptyNotError(t *testing.T) {
	s := openTestStore(t)
	got, err := s.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

// TestStore_GetReportForTestRun_ReturnsLatest 确认同一 TestRun 下多条 Report
// （如重新生成过报告）时取的是最新一条，不是随便一条或最早一条。
func TestStore_GetReportForTestRun_ReturnsLatest(t *testing.T) {
	s := openTestStore(t)
	m := setupModel(t, s)
	run, err := s.CreateTestRun(model.TestRun{ModelID: m.ID, SuiteID: "s", StartedAt: "t"})
	if err != nil {
		t.Fatalf("CreateTestRun: %v", err)
	}
	if _, err := s.CreateReport(model.Report{TestRunID: run.ID, GeneratedAt: "2026-08-18T00:00:00Z", Verdict: "FAIL", HTMLRef: "old.html"}); err != nil {
		t.Fatalf("CreateReport (old): %v", err)
	}
	latest, err := s.CreateReport(model.Report{TestRunID: run.ID, GeneratedAt: "2026-08-18T01:00:00Z", Verdict: "PASS", HTMLRef: "new.html"})
	if err != nil {
		t.Fatalf("CreateReport (latest): %v", err)
	}
	got, err := s.GetReportForTestRun(run.ID)
	if err != nil {
		t.Fatalf("GetReportForTestRun: %v", err)
	}
	if got.ID != latest.ID || got.HTMLRef != "new.html" {
		t.Errorf("expected the most recently generated report, got %+v", got)
	}
}
