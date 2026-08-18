package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leoobai/modeltestbed/internal/api"
)

// TestAuthMiddleware_EmptyTokenAllowsAllRequests 确认默认（未配置
// -auth-token）行为是放行——本地开发/测试场景不应该被迫每次都带 token，
// 这是有意为之的默认值，不是遗漏。
func TestAuthMiddleware_EmptyTokenAllowsAllRequests(t *testing.T) {
	cfg, _ := newTestConfig(t, "http://example.invalid")
	r := api.NewRouter(cfg)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with no auth configured, got %d", w.Code)
	}
}

// TestAuthMiddleware_RejectsMissingOrWrongToken 防止 P4 API 服务 review
// round-1 提到的观察项：设计方案 10.1 节要求首版至少有固定 Token 鉴权，
// 一旦配置了 AuthToken 就必须真的生效——缺失或错误的 Authorization 头都
// 应该被拒绝。
func TestAuthMiddleware_RejectsMissingOrWrongToken(t *testing.T) {
	cfg, _ := newTestConfig(t, "http://example.invalid")
	cfg.AuthToken = "secret-token"
	r := api.NewRouter(cfg)

	noHeader := httptest.NewRecorder()
	r.ServeHTTP(noHeader, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if noHeader.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without Authorization header, got %d", noHeader.Code)
	}

	wrongToken := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	r.ServeHTTP(wrongToken, req)
	if wrongToken.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with a wrong token, got %d", wrongToken.Code)
	}

	correctToken := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	req2.Header.Set("Authorization", "Bearer secret-token")
	r.ServeHTTP(correctToken, req2)
	if correctToken.Code != http.StatusOK {
		t.Errorf("expected 200 with the correct token, got %d", correctToken.Code)
	}
}
