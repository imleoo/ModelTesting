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
