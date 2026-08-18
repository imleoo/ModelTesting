package engine_test

// 覆盖 rejects_invalid_request 断言类型（07 节 SOP 允许的供应商专属补充
// 用例：网关应对非法输入返回 4xx，而不是透传给后端触发 500，见
// suites/kimi-k3/suite.v1.json 里 input_validation.* 系列用例）。

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestRejectsInvalidRequest_4xxIsPass(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"error":{"message":"max_tokens must be positive"}}`)
	})
	defer closeFn()

	c := simpleCase("rejects_invalid_request", map[string]any{
		"model":      "test-model",
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
		"max_tokens": -1,
	})
	result := e.RunCase(context.Background(), c)
	if result.Status != "PASS" {
		t.Fatalf("expected PASS when gateway returns 422 for invalid input, got %s (attempts=%+v)", result.Status, result.CaseAttempts)
	}
}

func TestRejectsInvalidRequest_500IsFail(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `internal server error`)
	})
	defer closeFn()

	c := simpleCase("rejects_invalid_request", map[string]any{
		"model":    "test-model",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	result := e.RunCase(context.Background(), c)
	if result.Status != "FAIL" {
		t.Fatalf("expected FAIL when gateway leaks a 500 for invalid input, got %s", result.Status)
	}
	if len(result.CaseAttempts) != 1 || result.CaseAttempts[0].FailReason == "" {
		t.Fatalf("expected a non-empty fail reason explaining the 500, got %+v", result.CaseAttempts)
	}
}

func TestRejectsInvalidRequest_200IsFail(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "c1", "object": "chat.completion", "created": 1700000000, "model": "test-model",
			"choices": [{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]
		}`)
	})
	defer closeFn()

	c := simpleCase("rejects_invalid_request", map[string]any{
		"model":    "test-model",
		"messages": []any{map[string]any{"role": "not_a_real_role", "content": "hi"}},
	})
	result := e.RunCase(context.Background(), c)
	if result.Status != "FAIL" {
		t.Fatalf("expected FAIL when the gateway silently accepts invalid input (200), got %s", result.Status)
	}
}

// TestRejectsInvalidRequest_SkipsGlobalSchemaBaseline 确认这类用例不会先跑
// 04 节 4.1 全局 openai_schema_valid 基线——网关对非法输入的 4xx 错误响应体
// 本来就不是一个合法的 chat completion 对象，不应该被当成 schema 违规判 FAIL。
func TestRejectsInvalidRequest_SkipsGlobalSchemaBaseline(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		// 一个完全不符合 chat.completion schema 的错误响应体（没有 id/object/
		// choices 等字段）——如果误跑了全局 schema 基线，这里会被判 FAIL。
		fmt.Fprint(w, `{"error":"bad request"}`)
	})
	defer closeFn()

	c := simpleCase("rejects_invalid_request", map[string]any{
		"model":    "test-model",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	result := e.RunCase(context.Background(), c)
	if result.Status != "PASS" {
		t.Fatalf("expected PASS: a 400 error body that isn't schema-valid must still count as a correct rejection, got %s", result.Status)
	}
}
