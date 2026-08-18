package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leoobai/modeltestbed/internal/api"
	"github.com/leoobai/modeltestbed/internal/model"
)

func TestProviderModelHandlers_CRUDHappyPath(t *testing.T) {
	cfg, _ := newTestConfig(t, "http://example.invalid")
	r := api.NewRouter(cfg)

	pBody, _ := json.Marshal(model.Provider{Name: "MoonshotAI", Contact: "ops@example.com"})
	wp := httptest.NewRecorder()
	r.ServeHTTP(wp, httptest.NewRequest(http.MethodPost, "/api/providers", bytes.NewReader(pBody)))
	if wp.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating provider, got %d: %s", wp.Code, wp.Body.String())
	}
	var provider model.Provider
	if err := json.Unmarshal(wp.Body.Bytes(), &provider); err != nil {
		t.Fatalf("unmarshal provider: %v", err)
	}
	if provider.ID == "" {
		t.Fatal("expected provider ID to be assigned")
	}

	wlp := httptest.NewRecorder()
	r.ServeHTTP(wlp, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if wlp.Code != http.StatusOK {
		t.Fatalf("expected 200 listing providers, got %d", wlp.Code)
	}
	var providers []model.Provider
	if err := json.Unmarshal(wlp.Body.Bytes(), &providers); err != nil || len(providers) != 1 {
		t.Fatalf("expected exactly 1 provider listed, got %v (err=%v)", providers, err)
	}

	mBody, _ := json.Marshal(model.Model{
		ProviderID:            provider.ID,
		ModelKey:              "kimi-k3",
		EndpointViaTokenpanel: "https://jiwu.wtgo.com.cn/",
		Capability: model.CapabilityProfile{
			ImageBase64:             true,
			VideoBase64:             true,
			ThinkingToggleMethods:   []string{"enable_thinking"},
			DefaultThinkingBehavior: "thinks_by_default",
		},
	})
	wm := httptest.NewRecorder()
	r.ServeHTTP(wm, httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewReader(mBody)))
	if wm.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating model, got %d: %s", wm.Code, wm.Body.String())
	}
	var created model.Model
	if err := json.Unmarshal(wm.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal model: %v", err)
	}

	wgm := httptest.NewRecorder()
	r.ServeHTTP(wgm, httptest.NewRequest(http.MethodGet, "/api/models/"+created.ID, nil))
	if wgm.Code != http.StatusOK {
		t.Fatalf("expected 200 getting model, got %d", wgm.Code)
	}
	var got model.Model
	if err := json.Unmarshal(wgm.Body.Bytes(), &got); err != nil || got.ModelKey != "kimi-k3" {
		t.Fatalf("GetModel round-trip mismatch: %+v (err=%v)", got, err)
	}
}

func TestCreateModel_RejectsInvalidCapability(t *testing.T) {
	cfg, s := newTestConfig(t, "http://example.invalid")
	p, err := s.CreateProvider(model.Provider{Name: "X"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	r := api.NewRouter(cfg)

	mBody, _ := json.Marshal(model.Model{ProviderID: p.ID, ModelKey: "k", EndpointViaTokenpanel: "e", Capability: model.CapabilityProfile{}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewReader(mBody)))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid capability, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetModel_NotFoundReturns404(t *testing.T) {
	cfg, _ := newTestConfig(t, "http://example.invalid")
	r := api.NewRouter(cfg)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/models/nope", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}
