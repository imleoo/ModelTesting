package engine_test

// 针对 Codex P1 第一轮审核发现的 7 个阻塞性问题的专项回归测试，
// 每条对应一个具体缺陷场景，防止后续改动重新引入。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/suitedef"
)

func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// 缺陷 1：thinking_toggle_pair 会把失败请求覆盖成 PASS。
// 场景：on 请求正常返回 reasoning_content，off 请求 HTTP 500。
// 期望：整体 FAIL，且两条 attempt 都不能是 Passed=true。
func TestRegression_ThinkingTogglePair_OneSideErrors_DoesNotForcePass(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = decodeJSON(r, &body)
		if v, _ := body["enable_thinking"].(bool); v {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{
				"id":"c1","object":"chat.completion","created":1700000000,"model":"test-model",
				"choices":[{"index":0,"message":{"role":"assistant","content":"答案","reasoning_content":"想想"},"finish_reason":"stop"}]
			}`)
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	defer closeFn()

	c := suitedef.Case{
		ID: "thinking.test", AssertionType: "thinking_toggle_pair", RequiredRule: suitedef.FixedRequired,
		RequestTemplate: suitedef.RequestTemplate{Body: map[string]any{
			"model": "{{model_key}}", "messages": []any{map[string]any{"role": "user", "content": "1+1"}}, "stream": false,
		}},
		Variants: []suitedef.Variant{
			{VariantLabel: "on", RequestOverrides: map[string]any{"enable_thinking": true}},
			{VariantLabel: "off", RequestOverrides: map[string]any{"enable_thinking": false}},
		},
	}
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusFail {
		t.Fatalf("期望整体 FAIL（off 请求 500），实际 status=%s", result.Status)
	}
	for _, a := range result.CaseAttempts {
		if a.Passed {
			t.Fatalf("variant=%s 不应被判 Passed=true，off 端请求失败应导致整体判定不通过而非被覆盖", a.VariantLabel)
		}
	}
}

// 缺陷 2：tool_call_named 未校验 parameters schema，参数字段不满足声明也会 PASS。
func TestRegression_ToolCallNamed_ValidatesParametersSchema(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id":"c1","object":"chat.completion","created":1700000000,"model":"test-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":null,
				"tool_calls":[{"id":"c1","type":"function","function":{"name":"get_weather","arguments":"{\"foo\":1}"}}]},
				"finish_reason":"tool_calls"}]
		}`)
	})
	defer closeFn()

	c := suitedef.Case{
		ID: "tool.named.test", AssertionType: "tool_call_named", RequiredRule: suitedef.FixedRequired,
		RequestTemplate: suitedef.RequestTemplate{Body: map[string]any{
			"model":    "{{model_key}}",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
			"tools": []any{map[string]any{"type": "function", "function": map[string]any{
				"name": "get_weather",
				"parameters": map[string]any{
					"type":                 "object",
					"properties":           map[string]any{"city": map[string]any{"type": "string"}},
					"required":             []any{"city"},
					"additionalProperties": false,
				},
			}}},
			"tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}},
			"stream":      false,
		}},
	}
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusFail {
		t.Fatalf("期望 FAIL（arguments 不含必填 city，含未声明字段 foo），实际 status=%s", result.Status)
	}
}

// 缺陷 3：tool_call_forbidden 没有要求响应必须是非空文本。
func TestRegression_ToolCallForbidden_RequiresNonEmptyText(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id":"c1","object":"chat.completion","created":1700000000,"model":"test-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":null},"finish_reason":"stop"}]
		}`)
	})
	defer closeFn()

	c := simpleCase("tool_call_forbidden", map[string]any{
		"model": "{{model_key}}", "messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tool_choice": "none", "stream": false,
	})
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusFail {
		t.Fatalf("期望 FAIL（无 tool_calls 但 content 也为空，不满足“仅文本”），实际 status=%s", result.Status)
	}
}

// 缺陷 4：reasoning_effort_scaling 会忽略采样失败、留痕汇总为 0/0。
func TestRegression_ReasoningEffortScaling_SamplingFailurePreventsPass(t *testing.T) {
	callCount := 0
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = decodeJSON(r, &body)
		callCount++
		effort, _ := body["reasoning_effort"].(string)
		if effort == "low" && callCount%3 == 1 {
			http.Error(w, "transient", http.StatusInternalServerError)
			return
		}
		tokens := map[string]int{"low": 50, "high": 200, "max": 220}[effort]
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"id":"c1","object":"chat.completion","created":1700000000,"model":"test-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"42"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"completion_tokens_details":{"reasoning_tokens":%d}}
		}`, tokens)
	})
	defer closeFn()

	c := suitedef.Case{
		ID: "reasoning.test", AssertionType: "reasoning_effort_scaling", RequiredRule: suitedef.FixedRequired,
		RepeatAttempts: 3,
		RequestTemplate: suitedef.RequestTemplate{Body: map[string]any{
			"model": "{{model_key}}", "messages": []any{map[string]any{"role": "user", "content": "q"}}, "stream": false,
		}},
		Variants: []suitedef.Variant{
			{VariantLabel: "low", RequestOverrides: map[string]any{"reasoning_effort": "low"}},
			{VariantLabel: "high", RequestOverrides: map[string]any{"reasoning_effort": "high"}},
			{VariantLabel: "max", RequestOverrides: map[string]any{"reasoning_effort": "max"}},
		},
	}
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusFail {
		t.Fatalf("期望 FAIL（low 档存在一次采样失败），实际 status=%s", result.Status)
	}
	if result.Attempts != 9 {
		t.Fatalf("期望留痕 9 次采样（3 档 × 3 次），实际 attempts=%d（不应该出现 0/0 这种与实际执行脱节的汇总）", result.Attempts)
	}
	if result.PassedAttempts != 8 {
		t.Fatalf("期望 8 次成功 1 次失败，实际 passed_attempts=%d", result.PassedAttempts)
	}
}

// 缺陷 5：usage_fields_stream 应该只认 [DONE] 前最后一包的 usage，不能找到任意历史分片的 usage 就算数。
func TestRegression_UsageFieldsStream_OnlyLastChunkBeforeDoneCounts(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		// 第一包违规地带了 usage，但真正的末包（[DONE] 前最后一包）不带 usage。
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1700000000,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"h\"},\"finish_reason\":null}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1700000000,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	})
	defer closeFn()

	c := suitedef.Case{
		ID: "usage.stream.test", AssertionType: "usage_fields_stream", RequiredRule: suitedef.FixedRequired,
		RequestTemplate: suitedef.RequestTemplate{Body: map[string]any{
			"model": "{{model_key}}", "messages": []any{map[string]any{"role": "user", "content": "hi"}}, "stream": true,
		}},
		Variants: []suitedef.Variant{
			{VariantLabel: "no_include_usage", RequestOverrides: map[string]any{}},
			{VariantLabel: "with_include_usage", RequestOverrides: map[string]any{"stream_options": map[string]any{"include_usage": true}}},
		},
	}
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusFail {
		t.Fatalf("期望 FAIL（末包不带 usage，不应因中间分片带 usage 而误判 PASS），实际 status=%s", result.Status)
	}
}

// 缺陷 6：openai_schema_valid 应拦截 role 缺失、tool_calls[].type 非 function、usage 为小数。
func TestRegression_SchemaValid_CatchesMoreStructuralIssues(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing_role", `{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"content":"hi"},"finish_reason":"stop"}]}`},
		{"tool_call_type_not_function", `{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"1","type":"weird","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`},
		{"fractional_usage_tokens", `{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1.5,"completion_tokens":1,"total_tokens":2.5}}`},
		{"wrong_object_literal", `{"id":"c1","object":"something.else","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			})
			defer closeFn()
			c := simpleCase("content_nonempty", map[string]any{
				"model": "{{model_key}}", "messages": []any{map[string]any{"role": "user", "content": "hi"}}, "stream": false,
			})
			result := e.RunCase(context.Background(), c)
			if result.Status != model.StatusFail {
				t.Fatalf("场景 %s 期望被 openai_schema_valid 拦截为 FAIL，实际 status=%s", tc.name, result.Status)
			}
		})
	}
}

// P2 用真实 kimi-k3 网关实测发现：openai_schema_valid 曾要求非流式 message.content
// 键必须存在（可为 null），但真实网关返回 tool_calls 时会直接省略 content 字段，
// 而不是显式写 "content":null——这是合法的 OpenAI 兼容行为，不应判 FAIL。
func TestRegression_SchemaValid_OmittedContentWithToolCallsIsValid(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 注意：message 对象里没有 "content" 键，只有 role/reasoning_content/tool_calls。
		fmt.Fprint(w, `{
			"id":"c1","object":"chat.completion","created":1700000000,"model":"test-model",
			"choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"xiangxiang",
				"tool_calls":[{"id":"c1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"beijing\"}"}}]},
				"finish_reason":"tool_calls"}]
		}`)
	})
	defer closeFn()

	toolsBody := map[string]any{
		"model":       "{{model_key}}",
		"messages":    []any{map[string]any{"role": "user", "content": "hi"}},
		"tools":       []any{map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}},
		"tool_choice": "required",
		"stream":      false,
	}
	c := simpleCase("tool_call_required", toolsBody)
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusPass {
		t.Fatalf("expected PASS (content field omitted but tool_calls valid, should not be blocked by schema check), got status=%s reason=%s",
			result.Status, result.CaseAttempts[0].FailReason)
	}
}

// 缺陷 7：非法 required_rule 不能被默认当作”必须执行”悄悄放行。
func TestRegression_InvalidRequiredRule_FailsInsteadOfSilentlyRunning(t *testing.T) {
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("非法 required_rule 的用例不应该发起任何网络请求")
	})
	defer closeFn()

	c := suitedef.Case{
		ID:            "bad.rule",
		RequiredRule:  suitedef.RequiredRule("not_a_real_rule"),
		AssertionType: "content_nonempty",
		RequestTemplate: suitedef.RequestTemplate{Body: map[string]any{
			"model": "{{model_key}}", "messages": []any{map[string]any{"role": "user", "content": "hi"}}, "stream": false,
		}},
	}
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusFail {
		t.Fatalf("期望 FAIL（套件定义非法），实际 status=%s", result.Status)
	}
}
