package api_test

import (
	"bytes"
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

// newSuitesTestConfig 搭一份独立的 Config：真实 SQLite（内存库，suites 表
// 是套件定义的权威来源）+ 一个临时 MaterialsRoot（素材文件的持久化目录）。
func newSuitesTestConfig(t *testing.T) (*api.Config, *store.Store, string) {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	materialsRoot := filepath.Join(t.TempDir(), "materials")
	cfg := &api.Config{
		Store:         s,
		MaterialsRoot: materialsRoot,
	}
	return cfg, s, materialsRoot
}

const testSuiteDefinitionJSON = `{
  "suite_id": "kimi-k3",
  "suite_version": "1.0.0",
  "description": "a test suite kept for cloning",
  "materials_manifest": "suites/kimi-k3/materials/manifest.json",
  "protocol": {"base_path": "/v1/chat/completions", "auth_header": "Authorization: Bearer {{api_key}}", "content_type": "application/json"},
  "defaults": {"timeout_seconds": 10, "category_timeout_overrides": {}},
  "fixtures": {"tools": {}, "prompts": {"generic_short": "Say hello."}},
  "cases": [
    {
      "id": "connectivity.basic",
      "category": "连通性",
      "name": "基础单轮请求",
      "required_rule": "fixed_required",
      "counts_in_base22": true,
      "assertion_type": "content_nonempty",
      "repeat_attempts": 1,
      "request_template": {"method": "POST", "body": {"model": "{{model_key}}", "messages": [], "stream": false}}
    }
  ]
}`

// seedSuiteWithMaterials 直接往 store 里插一条套件记录，并在
// materialsRoot/<name>/materials 下放一份素材清单 + 素材文件——不经过
// SeedSuitesFromDisk（那是磁盘同步路径，这里只是给 List/Clone 测试准备
// 一条已存在的 DB 记录）。
func seedSuiteWithMaterials(t *testing.T, s *store.Store, materialsRoot, name string) {
	t.Helper()
	matDir := filepath.Join(materialsRoot, name, "materials")
	if err := os.MkdirAll(matDir, 0o755); err != nil {
		t.Fatalf("mkdir materials dir: %v", err)
	}
	manifestJSON := `{
  "manifest_version": "1",
  "suite_id": "` + name + `",
  "materials": [
    {
      "id": "image_qa_v1",
      "kind": "image",
      "file": "image_qa_v1.png",
      "hosting": {
        "mode": "self_hosted_static",
        "frozen_url_path": "/materials/` + name + `/v1/image_qa_v1.png"
      }
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(matDir, "manifest.json"), []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write manifest json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(matDir, "image_qa_v1.png"), []byte("fake-png-bytes"), 0o644); err != nil {
		t.Fatalf("write material file: %v", err)
	}

	definitionJSON := testSuiteDefinitionJSON
	if name != "kimi-k3" {
		rewritten, err := json.Marshal(map[string]any{"suite_id": name, "suite_version": "1.0.0", "cases": []any{
			map[string]any{"id": "connectivity.basic", "category": "连通性", "required_rule": "fixed_required", "counts_in_base22": true, "assertion_type": "content_nonempty", "repeat_attempts": 1},
		}})
		if err != nil {
			t.Fatalf("marshal definition: %v", err)
		}
		definitionJSON = string(rewritten)
	}
	if _, err := s.CreateSuite(model.Suite{
		Name:           name,
		DefinitionJSON: definitionJSON,
		HasMaterials:   true,
		CreatedAt:      "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed suite %s: %v", name, err)
	}
}

func TestListSuites_ReturnsDBRecords(t *testing.T) {
	cfg, s, materialsRoot := newSuitesTestConfig(t)
	seedSuiteWithMaterials(t, s, materialsRoot, "kimi-k3")
	seedSuiteWithMaterials(t, s, materialsRoot, "z-ai")
	r := api.NewRouter(cfg)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/suites", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got []api.SuiteSummary
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 suites, got %d: %+v", len(got), got)
	}
	if got[0].SuiteID != "kimi-k3" || got[0].Name != "kimi-k3" || got[0].CaseCount != 1 {
		t.Fatalf("unexpected first summary: %+v", got[0])
	}
	if got[1].SuiteID != "z-ai" {
		t.Fatalf("unexpected second summary: %+v", got[1])
	}
}

func TestListSuites_EmptyDBReturnsEmptyList(t *testing.T) {
	cfg, _, _ := newSuitesTestConfig(t)
	r := api.NewRouter(cfg)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/suites", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "[]" {
		t.Fatalf("expected empty JSON array, got %s", w.Body.String())
	}
}

func TestCreateSuite_HappyPath(t *testing.T) {
	cfg, s, _ := newSuitesTestConfig(t)
	r := api.NewRouter(cfg)

	body, _ := json.Marshal(map[string]string{"name": "new-provider", "description": "占位"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/suites", bytes.NewReader(body)))
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var summary api.SuiteSummary
	if err := json.Unmarshal(w.Body.Bytes(), &summary); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if summary.SuiteID != "new-provider" || summary.CaseCount != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if _, err := s.GetSuiteByName("new-provider"); err != nil {
		t.Fatalf("expected suite to exist in store: %v", err)
	}
}

func TestCreateSuite_RejectsInvalidName(t *testing.T) {
	cfg, _, _ := newSuitesTestConfig(t)
	r := api.NewRouter(cfg)

	body, _ := json.Marshal(map[string]string{"name": "../escape"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/suites", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path-unsafe name, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateSuite_RejectsDuplicateName(t *testing.T) {
	cfg, s, materialsRoot := newSuitesTestConfig(t)
	seedSuiteWithMaterials(t, s, materialsRoot, "kimi-k3")
	r := api.NewRouter(cfg)

	body, _ := json.Marshal(map[string]string{"name": "kimi-k3"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/suites", bytes.NewReader(body)))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate name, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCloneSuite_CopiesAndRewritesIdentity(t *testing.T) {
	cfg, s, materialsRoot := newSuitesTestConfig(t)
	seedSuiteWithMaterials(t, s, materialsRoot, "kimi-k3")
	r := api.NewRouter(cfg)

	body, _ := json.Marshal(map[string]string{"source_suite_id": "kimi-k3", "new_name": "acme"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/suites/clone", bytes.NewReader(body)))
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var summary api.SuiteSummary
	if err := json.Unmarshal(w.Body.Bytes(), &summary); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if summary.SuiteID != "acme" || summary.Name != "acme" || summary.CaseCount != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	// 克隆出的素材文件本身也要在——不能只克隆套件定义。
	if _, err := os.Stat(filepath.Join(materialsRoot, "acme", "materials", "image_qa_v1.png")); err != nil {
		t.Fatalf("expected cloned material file to exist: %v", err)
	}

	cloned, err := s.GetSuiteByName("acme")
	if err != nil {
		t.Fatalf("GetSuiteByName(acme): %v", err)
	}
	var clonedDoc map[string]any
	if err := json.Unmarshal([]byte(cloned.DefinitionJSON), &clonedDoc); err != nil {
		t.Fatalf("unmarshal cloned definition: %v", err)
	}
	if clonedDoc["suite_id"] != "acme" {
		t.Fatalf("expected suite_id=acme, got %v", clonedDoc["suite_id"])
	}
	if clonedDoc["description"] != "a test suite kept for cloning" {
		t.Fatalf("expected description field to survive the clone (not dropped by re-marshaling), got %v", clonedDoc["description"])
	}
	if !cloned.HasMaterials {
		t.Fatal("expected cloned suite to carry HasMaterials=true")
	}

	manifestRaw, err := os.ReadFile(filepath.Join(materialsRoot, "acme", "materials", "manifest.json"))
	if err != nil {
		t.Fatalf("read cloned manifest: %v", err)
	}
	var manifestDoc map[string]any
	if err := json.Unmarshal(manifestRaw, &manifestDoc); err != nil {
		t.Fatalf("unmarshal cloned manifest: %v", err)
	}
	if manifestDoc["suite_id"] != "acme" {
		t.Fatalf("expected manifest suite_id=acme, got %v", manifestDoc["suite_id"])
	}
	materials := manifestDoc["materials"].([]any)
	mat := materials[0].(map[string]any)
	hosting := mat["hosting"].(map[string]any)
	if hosting["frozen_url_path"] != "/materials/acme/v1/image_qa_v1.png" {
		t.Fatalf("expected rewritten frozen_url_path, got %v", hosting["frozen_url_path"])
	}

	// 源套件必须保持原样，不能被克隆动作动到。
	orig, err := s.GetSuiteByName("kimi-k3")
	if err != nil {
		t.Fatalf("GetSuiteByName(kimi-k3): %v", err)
	}
	var origDoc map[string]any
	if err := json.Unmarshal([]byte(orig.DefinitionJSON), &origDoc); err != nil {
		t.Fatalf("unmarshal original definition: %v", err)
	}
	if origDoc["suite_id"] != "kimi-k3" {
		t.Fatalf("expected original suite untouched, got suite_id=%v", origDoc["suite_id"])
	}
	if _, err := os.Stat(filepath.Join(materialsRoot, "kimi-k3", "materials", "image_qa_v1.png")); err != nil {
		t.Fatalf("expected original material file untouched: %v", err)
	}
}

func TestCloneSuite_WithoutMaterialsSkipsFileCopy(t *testing.T) {
	cfg, s, _ := newSuitesTestConfig(t)
	if _, err := s.CreateSuite(model.Suite{
		Name:           "no-materials",
		DefinitionJSON: `{"suite_id": "no-materials", "suite_version": "1.0.0", "cases": []}`,
		HasMaterials:   false,
		CreatedAt:      "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed suite: %v", err)
	}
	r := api.NewRouter(cfg)

	body, _ := json.Marshal(map[string]string{"source_suite_id": "no-materials", "new_name": "no-materials-clone"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/suites/clone", bytes.NewReader(body)))
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	cloned, err := s.GetSuiteByName("no-materials-clone")
	if err != nil {
		t.Fatalf("GetSuiteByName: %v", err)
	}
	if cloned.HasMaterials {
		t.Fatal("expected cloned suite to carry HasMaterials=false")
	}
}

func TestCloneSuite_RejectsUnknownSource(t *testing.T) {
	cfg, _, _ := newSuitesTestConfig(t)
	r := api.NewRouter(cfg)

	body, _ := json.Marshal(map[string]string{"source_suite_id": "does-not-exist", "new_name": "acme"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/suites/clone", bytes.NewReader(body)))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown source_suite_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCloneSuite_RejectsDuplicateNewName(t *testing.T) {
	cfg, s, materialsRoot := newSuitesTestConfig(t)
	seedSuiteWithMaterials(t, s, materialsRoot, "kimi-k3")
	seedSuiteWithMaterials(t, s, materialsRoot, "z-ai")
	r := api.NewRouter(cfg)

	body, _ := json.Marshal(map[string]string{"source_suite_id": "kimi-k3", "new_name": "z-ai"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/suites/clone", bytes.NewReader(body)))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for existing new_name, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCloneSuite_RejectsInvalidNewName(t *testing.T) {
	cfg, s, materialsRoot := newSuitesTestConfig(t)
	seedSuiteWithMaterials(t, s, materialsRoot, "kimi-k3")
	r := api.NewRouter(cfg)

	body, _ := json.Marshal(map[string]string{"source_suite_id": "kimi-k3", "new_name": "../escape"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/suites/clone", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path-unsafe new_name, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSeedSuitesFromDisk_ImportsAndSyncsMaterials(t *testing.T) {
	root := t.TempDir()
	suitesRoot := filepath.Join(root, "suites")
	materialsRoot := filepath.Join(root, "materials")

	dir := filepath.Join(suitesRoot, "kimi-k3")
	if err := os.MkdirAll(filepath.Join(dir, "materials"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "suite.v1.json"), []byte(testSuiteDefinitionJSON), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "materials", "manifest.json"), []byte(`{"manifest_version":"1","suite_id":"kimi-k3","materials":[]}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "materials", "asset.png"), []byte("binary"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if err := api.SeedSuitesFromDisk(s, suitesRoot, materialsRoot); err != nil {
		t.Fatalf("SeedSuitesFromDisk: %v", err)
	}

	suite, err := s.GetSuiteByName("kimi-k3")
	if err != nil {
		t.Fatalf("expected kimi-k3 imported into DB: %v", err)
	}
	if !suite.HasMaterials {
		t.Fatal("expected HasMaterials=true")
	}
	if _, err := os.Stat(filepath.Join(materialsRoot, "kimi-k3", "materials", "asset.png")); err != nil {
		t.Fatalf("expected materials synced to MaterialsRoot: %v", err)
	}

	// 第二次运行必须覆盖（以磁盘文件为准），而不是保留旧的 DB 记录 id。
	firstID := suite.ID
	if err := api.SeedSuitesFromDisk(s, suitesRoot, materialsRoot); err != nil {
		t.Fatalf("SeedSuitesFromDisk (second run): %v", err)
	}
	suite2, err := s.GetSuiteByName("kimi-k3")
	if err != nil {
		t.Fatalf("GetSuiteByName after second sync: %v", err)
	}
	if suite2.ID != firstID {
		t.Fatalf("expected upsert to preserve id across re-sync, got %q vs %q", suite2.ID, firstID)
	}
}
