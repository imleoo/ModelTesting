package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leoobai/modeltestbed/internal/api"
)

// TestCORS_PreflightAndHeaders 确认 horizon-next 前端（跑在与 api-server
// 不同源的 Next.js dev/生产端口）能通过浏览器 CORS 校验：OPTIONS 预检
// 直接放行且不需要鉴权，实际请求带上了允许跨域的响应头。
func TestCORS_PreflightAndHeaders(t *testing.T) {
	cfg, _ := newTestConfig(t, "http://example.invalid")
	r := api.NewRouter(cfg)

	preflight := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/providers", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "GET")
	r.ServeHTTP(preflight, req)
	if preflight.Code != http.StatusNoContent {
		t.Errorf("expected 204 for CORS preflight, got %d", preflight.Code)
	}
	if preflight.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("expected Access-Control-Allow-Origin header on preflight response")
	}

	actual := httptest.NewRecorder()
	r.ServeHTTP(actual, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if actual.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", actual.Code)
	}
	if actual.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("expected Access-Control-Allow-Origin header on actual response")
	}
}
