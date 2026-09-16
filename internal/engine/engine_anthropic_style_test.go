package engine_test

// 协议分流回归测试：验证 Style=StyleAnthropicMessages 时，engine.go 里的
// score* 断言函数与多请求编排函数（runThinkingTogglePair 等）都通过
// e.parseResponse()/e.validateNonStreamSchema() 做协议分流，而不是像历史遗留
// 代码那样硬编码调用 openaiapi.ParseResponse/openaiapi.ValidateSchema——后者
// 对 Anthropic 原生响应体（顶层 content 数组、没有 object/choices 字段）要么
// 解析出空 Choices，要么直接被 openai_schema_valid 拦下，导致所有断言都
// 以「看似合理但实际原因错误」的理由误判 FAIL。
//
// 这批断言函数在本测试新增之前没有任何 Anthropic 协议下的自动化验证，问题
// 目前是潜伏状态（仓库里还没有声明 anthropic_messages 风格的套件），修复
// 详见设计方案审计记录；此测试防止未来再有断言类型被加回硬编码调用。

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

func newAnthropicTestEngine(t *testing.T, handler http.HandlerFunc) (*engine.Engine, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	suite := &suitedef.Suite{
		SuiteID: "test-anthropic",
		Protocol: suitedef.Protocol{
			BasePath: "/v1/messages",
			Style:    suitedef.StyleAnthropicMessages,
		},
		Defaults: suitedef.Defaults{TimeoutSeconds: 5},
	}
	e := &engine.Engine{
		Client: client.New(srv.URL, "test-key"),
		Suite:  suite,
		Style:  suitedef.StyleAnthropicMessages,
		RenderCtx: render.Context{
			ModelKey: "test-model",
			APIKey:   "test-key",
		},
	}
	return e, srv.Close
}

func anthropicCase(assertionType string, body map[string]any) suitedef.Case {
	return suitedef.Case{
		ID:             "test-anthropic." + assertionType,
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

// content_nonempty 经由 runSingleNonStream -> scoreContentNonempty 分发，
// 修复前 scoreContentNonempty 硬编码调用 openaiapi.ParseResponse，对
// Anthropic 响应会拿到空 Choices，误判 FAIL（原因是"choices 为空"而不是
// 内容真的为空）。
func TestAnthropicStyle_ContentNonempty_Passes(t *testing.T) {
	e, closeFn := newAnthropicTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "msg_1",
			"type": "message",
			"role": "assistant",
			"model": "test-model",
			"content": [{"type": "text", "text": "你好，这是一段正常回复"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 10, "output_tokens": 8}
		}`)
	})
	defer closeFn()

	c := anthropicCase("content_nonempty", map[string]any{
		"model":      "{{model_key}}",
		"max_tokens": 256,
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
	})
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusPass {
		reason := ""
		if len(result.CaseAttempts) > 0 {
			reason = result.CaseAttempts[0].FailReason
		}
		t.Fatalf("期望 PASS（Anthropic 响应含非空 text block），实际 status=%s fail_reason=%q", result.Status, reason)
	}
}

// tool_call_required 验证 content[].type=="tool_use" 能被正确归一化成
// openaiapi.Response.Choices[0].Message.ToolCalls，修复前同样会因为硬编码
// 调用 openaiapi.ParseResponse 而拿到空 ToolCalls，误判 FAIL。
func TestAnthropicStyle_ToolCallRequired_Passes(t *testing.T) {
	e, closeFn := newAnthropicTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "msg_2",
			"type": "message",
			"role": "assistant",
			"model": "test-model",
			"content": [{"type": "tool_use", "id": "toolu_1", "name": "get_weather", "input": {"city": "北京"}}],
			"stop_reason": "tool_use",
			"usage": {"input_tokens": 12, "output_tokens": 6}
		}`)
	})
	defer closeFn()

	c := anthropicCase("tool_call_required", map[string]any{
		"model":      "{{model_key}}",
		"max_tokens": 256,
		"messages":   []any{map[string]any{"role": "user", "content": "北京天气怎么样"}},
		"tools":      []any{map[string]any{"name": "get_weather"}},
	})
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusPass {
		reason := ""
		if len(result.CaseAttempts) > 0 {
			reason = result.CaseAttempts[0].FailReason
		}
		t.Fatalf("期望 PASS（Anthropic 响应含 tool_use block），实际 status=%s fail_reason=%q", result.Status, reason)
	}
}

// thinking_toggle_pair 是多请求编排函数（runThinkingTogglePair），修复前
// 不仅硬编码调用 openaiapi.ParseResponse，还硬编码调用
// openaiapi.ValidateSchema——Anthropic 响应缺少 OpenAI schema 要求的 object
// 字段，会在协议基线这一步就被拦下，两个变体（开/关）都会 FAIL 而不是按
// thinking content block 是否存在来判定。
func TestAnthropicStyle_ThinkingTogglePair_Passes(t *testing.T) {
	e, closeFn := newAnthropicTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Thinking *struct {
				Type string `json:"type"`
			} `json:"thinking"`
		}
		_ = decodeJSON(r, &body)

		if body.Thinking != nil && body.Thinking.Type == "enabled" {
			fmt.Fprint(w, `{
				"id": "msg_on",
				"type": "message",
				"role": "assistant",
				"model": "test-model",
				"content": [
					{"type": "thinking", "thinking": "先分析一下这个问题"},
					{"type": "text", "text": "答案是 42"}
				],
				"stop_reason": "end_turn",
				"usage": {"input_tokens": 10, "output_tokens": 20}
			}`)
			return
		}
		fmt.Fprint(w, `{
			"id": "msg_off",
			"type": "message",
			"role": "assistant",
			"model": "test-model",
			"content": [{"type": "text", "text": "答案是 42"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 10, "output_tokens": 8}
		}`)
	})
	defer closeFn()

	c := suitedef.Case{
		ID:             "test-anthropic.thinking_toggle_pair",
		Category:       "test",
		RequiredRule:   suitedef.FixedRequired,
		CountsInBase22: true,
		AssertionType:  "thinking_toggle_pair",
		RequestTemplate: suitedef.RequestTemplate{
			Method: "POST",
			Body: map[string]any{
				"model":      "{{model_key}}",
				"max_tokens": 256,
				"messages":   []any{map[string]any{"role": "user", "content": "算一道题"}},
			},
		},
		Variants: []suitedef.Variant{
			{VariantLabel: "on", RequestOverrides: map[string]any{"thinking": map[string]any{"type": "enabled"}}},
			{VariantLabel: "off", RequestOverrides: map[string]any{"thinking": map[string]any{"type": "disabled"}}},
		},
	}
	result := e.RunCase(context.Background(), c)

	if result.Status != model.StatusPass {
		var reasons []string
		for _, a := range result.CaseAttempts {
			reasons = append(reasons, a.FailReason)
		}
		t.Fatalf("期望 PASS（开启变体含 thinking block、关闭变体不含），实际 status=%s fail_reason=%v", result.Status, reasons)
	}
}
