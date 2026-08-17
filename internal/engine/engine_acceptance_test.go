package engine_test

// P1 验收测试：用固定 mock 响应跑正反例，验证用例引擎在各类场景下都能按预期
// PASS/FAIL，对应设计方案 14 节 P1 验收标准：
// 「用固定 mock 响应跑正反例：结构错误、断流、非法工具参数、content=null+合法
// tool_calls 等场景都能按预期 PASS/FAIL」

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leoobai/modeltestbed/internal/client"
	"github.com/leoobai/modeltestbed/internal/engine"
	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/render"
	"github.com/leoobai/modeltestbed/internal/suitedef"
)

func newTestEngine(t *testing.T, handler http.HandlerFunc) (*engine.Engine, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	suite := &suitedef.Suite{
		SuiteID:  "test",
		Protocol: suitedef.Protocol{BasePath: "/v1/chat/completions"},
		Defaults: suitedef.Defaults{TimeoutSeconds: 5},
	}
	e := &engine.Engine{
		Client: client.New(srv.URL, "test-key"),
		Suite:  suite,
		RenderCtx: render.Context{
			ModelKey: "test-model",
			APIKey:   "test-key",
		},
	}
	return e, srv.Close
}

func simpleCase(assertionType string, body map[string]any) suitedef.Case {
	return suitedef.Case{
		ID:             "test." + assertionType,
		Category:       "test",
		RequiredRule:   suitedef.FixedRequired,
		CountsInBase22: true,
		AssertionType:  assertionType,
		RequestTemplate: suitedef.RequestTemplate{
			Method: "POST",
			Body:   body,
		},
	}
}

// 场景 1：结构错误——响应缺少 openai_schema_valid 要求的 object 字段，
// 即便业务内容（content 非空）看起来正常，也必须判 FAIL。
func TestAcceptance_StructuralError_FailsSchema(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "chatcmpl-1",
			"created": 1700000000,
			"model": "test-model",
			"choices": [{"index":0,"message":{"role":"assistant","content":"这句话看起来完全正常"},"finish_reason":"stop"}]
		}`)
	})
	defer closeFn()

	c := simpleCase("content_nonempty", map[string]any{
		"model":    "{{model_key}}",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"stream":   false,
	})
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusFail {
		t.Fatalf("期望 FAIL（缺少 object 字段应被 openai_schema_valid 拦截），实际 status=%s", result.Status)
	}
	if len(result.CaseAttempts) != 1 {
		t.Fatalf("期望 1 条 CaseAttempt，实际 %d", len(result.CaseAttempts))
	}
	got := result.CaseAttempts[0].FailReason
	if got == "" {
		t.Fatal("期望 fail_reason 非空，说明是被 schema 校验拦截而非业务断言拦截")
	}
	t.Logf("fail_reason: %s", got)
}

// 场景 2：断流——SSE 只发了 1 个分片就断开连接，没有 [DONE]。
func TestAcceptance_StreamDisconnect_FailsStreamIntegrity(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter 不支持 Flush")
		}
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1700000000,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"h\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		// 中途断开，不再发送任何分片，也不发 [DONE]。
	})
	defer closeFn()

	c := simpleCase("stream_integrity", map[string]any{
		"model":    "{{model_key}}",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"stream":   true,
	})
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusFail {
		t.Fatalf("期望 FAIL（断流未见 [DONE]），实际 status=%s", result.Status)
	}
	t.Logf("fail_reason: %s", result.CaseAttempts[0].FailReason)
}

// 场景 2b：断流的另一种表现——分片数不足 2 片（PDF 要求 ≥2 片）。
func TestAcceptance_TooFewChunks_FailsStreamIntegrity(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1700000000,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"h\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	defer closeFn()

	c := simpleCase("stream_integrity", map[string]any{
		"model":    "{{model_key}}",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"stream":   true,
	})
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusFail {
		t.Fatalf("期望 FAIL（分片数=1，少于要求的≥2），实际 status=%s", result.Status)
	}
}

// 场景 3：非法工具参数——tool_calls 的 arguments 不是合法 JSON。
func TestAcceptance_InvalidToolCallArgs_FailsToolCallRequired(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "chatcmpl-1",
			"object": "chat.completion",
			"created": 1700000000,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": null,
					"tool_calls": [{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{city: beijing"}}]
				},
				"finish_reason": "tool_calls"
			}]
		}`)
	})
	defer closeFn()

	c := simpleCase("tool_call_required", map[string]any{
		"model":       "{{model_key}}",
		"messages":    []any{map[string]any{"role": "user", "content": "北京天气"}},
		"tools":       []any{map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}},
		"tool_choice": "required",
		"stream":      false,
	})
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusFail {
		t.Fatalf("期望 FAIL（tool_calls.arguments 不是合法 JSON），实际 status=%s", result.Status)
	}
	t.Logf("fail_reason: %s", result.CaseAttempts[0].FailReason)
}

// 场景 4：content 为 null 但存在合法 tool_calls，应视为通过（04 节 text_or_tool_call 断言）。
func TestAcceptance_NullContentWithValidToolCalls_Passes(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "chatcmpl-1",
			"object": "chat.completion",
			"created": 1700000000,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": null,
					"tool_calls": [{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"beijing\"}"}}]
				},
				"finish_reason": "tool_calls"
			}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
		}`)
	})
	defer closeFn()

	c := simpleCase("text_or_tool_call", map[string]any{
		"model":    "{{model_key}}",
		"messages": []any{map[string]any{"role": "user", "content": "北京天气"}},
		"tools":    []any{map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}},
		"stream":   false,
	})
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusPass {
		t.Fatalf("期望 PASS（content=null 但有合法 tool_calls），实际 status=%s reason=%s",
			result.Status, result.CaseAttempts[0].FailReason)
	}
}

// 场景 5（正例基线）：完全合法的非流式响应，content 非空，应当 PASS。
func TestAcceptance_ValidResponse_Passes(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "chatcmpl-1",
			"object": "chat.completion",
			"created": 1700000000,
			"model": "test-model",
			"choices": [{"index":0,"message":{"role":"assistant","content":"你好"},"finish_reason":"stop"}],
			"usage": {"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5}
		}`)
	})
	defer closeFn()

	c := simpleCase("content_nonempty", map[string]any{
		"model":    "{{model_key}}",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"stream":   false,
	})
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusPass {
		t.Fatalf("期望 PASS，实际 status=%s reason=%s", result.Status, result.CaseAttempts[0].FailReason)
	}
}

// 场景 6：tool_choice=none 时模型仍发起了工具调用，应判 FAIL。
func TestAcceptance_ToolCallWhenForbidden_Fails(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "chatcmpl-1",
			"object": "chat.completion",
			"created": 1700000000,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},
				"finish_reason": "tool_calls"
			}]
		}`)
	})
	defer closeFn()

	c := simpleCase("tool_call_forbidden", map[string]any{
		"model":       "{{model_key}}",
		"messages":    []any{map[string]any{"role": "user", "content": "北京天气"}},
		"tool_choice": "none",
		"stream":      false,
	})
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusFail {
		t.Fatalf("期望 FAIL（tool_choice=none 但仍发起了调用），实际 status=%s", result.Status)
	}
}
