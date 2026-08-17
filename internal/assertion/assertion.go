// Package assertion 实现设计方案 04 节各断言类型的单次判定逻辑。多请求用例
// （usage_fields_stream 的两段式重试、thinking_toggle_pair 的开/关两次、
// reasoning_effort_scaling 的三档×N次）的编排留给 internal/engine，本包只
// 负责单次请求/单个响应的 PASS/FAIL 判定。
package assertion

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/leoobai/modeltestbed/internal/openaiapi"
)

type Verdict struct {
	Passed bool
	Reason string
}

func pass() Verdict              { return Verdict{Passed: true} }
func fail(reason string) Verdict { return Verdict{Passed: false, Reason: reason} }

func StreamIntegrity(chunkCount int, sawDone bool) Verdict {
	if chunkCount < 2 {
		return fail(fmt.Sprintf("SSE 分片数为 %d，少于要求的 ≥2", chunkCount))
	}
	if !sawDone {
		return fail("未收到 [DONE] 结束标记，疑似中途断流或网关截断")
	}
	return pass()
}

func usageSelfConsistent(u *openaiapi.Usage) Verdict {
	if u == nil {
		return fail("缺少 usage 字段")
	}
	if u.PromptTokens < 0 || u.CompletionTokens < 0 || u.TotalTokens < 0 {
		return fail("usage 字段存在负数")
	}
	if u.PromptTokens+u.CompletionTokens != u.TotalTokens {
		return fail(fmt.Sprintf("usage 三字段不自洽：prompt_tokens(%d)+completion_tokens(%d)≠total_tokens(%d)",
			u.PromptTokens, u.CompletionTokens, u.TotalTokens))
	}
	return pass()
}

func UsageFieldsNonstream(resp openaiapi.Response) Verdict {
	return usageSelfConsistent(resp.Usage)
}

// UsageFieldsStreamAttempt 检查单次流式请求末包是否带合法 usage；
// 引擎负责按 04 节两段式重试逻辑编排两次尝试。
func UsageFieldsStreamAttempt(lastChunkUsage *openaiapi.Usage) Verdict {
	return usageSelfConsistent(lastChunkUsage)
}

func ContentNonempty(resp openaiapi.Response) Verdict {
	if len(resp.Choices) == 0 {
		return fail("choices 为空")
	}
	msg := resp.Choices[0].Message
	if msg == nil || msg.Content == nil || strings.TrimSpace(*msg.Content) == "" {
		return fail("响应内容为空")
	}
	return pass()
}

func validToolCallArgs(calls []openaiapi.ToolCall) Verdict {
	for _, tc := range calls {
		var js any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &js); err != nil {
			return fail(fmt.Sprintf("tool_call %s 的 arguments 不是合法 JSON: %v", tc.Function.Name, err))
		}
	}
	return pass()
}

// TextOrToolCall: 非空文本 或 合法 tool_calls 二选一；content 为 null 但存在
// 合法 tool_calls 视为通过。
func TextOrToolCall(resp openaiapi.Response) Verdict {
	if len(resp.Choices) == 0 {
		return fail("choices 为空")
	}
	msg := resp.Choices[0].Message
	if msg == nil {
		return fail("缺少 message")
	}
	if msg.Content != nil && strings.TrimSpace(*msg.Content) != "" {
		return pass()
	}
	if len(msg.ToolCalls) > 0 {
		return validToolCallArgs(msg.ToolCalls)
	}
	return fail("既无非空文本也无合法 tool_calls")
}

func ToolCallRequired(resp openaiapi.Response) Verdict {
	if len(resp.Choices) == 0 {
		return fail("choices 为空")
	}
	msg := resp.Choices[0].Message
	if msg == nil || len(msg.ToolCalls) == 0 {
		return fail("未发起任何工具调用")
	}
	return validToolCallArgs(msg.ToolCalls)
}

func ToolCallForbidden(resp openaiapi.Response) Verdict {
	if len(resp.Choices) == 0 {
		return fail("choices 为空")
	}
	msg := resp.Choices[0].Message
	if msg != nil && len(msg.ToolCalls) > 0 {
		return fail("tool_choice=none 时不应发起工具调用")
	}
	return pass()
}

func ToolCallNamed(resp openaiapi.Response, expectedFnName string) Verdict {
	if len(resp.Choices) == 0 {
		return fail("choices 为空")
	}
	msg := resp.Choices[0].Message
	if msg == nil || len(msg.ToolCalls) == 0 {
		return fail("未发起工具调用")
	}
	for _, tc := range msg.ToolCalls {
		if tc.Function.Name != expectedFnName {
			return fail(fmt.Sprintf("调用了非指定函数 %s，期望 %s", tc.Function.Name, expectedFnName))
		}
	}
	return validToolCallArgs(msg.ToolCalls)
}

// ToolCallSubset: 调用次数≥1且全部函数名⊆allowed（04节从严解释：零次调用不视为通过）。
func ToolCallSubset(resp openaiapi.Response, allowed []string) Verdict {
	if len(resp.Choices) == 0 {
		return fail("choices 为空")
	}
	msg := resp.Choices[0].Message
	if msg == nil || len(msg.ToolCalls) == 0 {
		return fail("零次调用不视为通过（从严解释，见 04 节）")
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allowedSet[a] = true
	}
	for _, tc := range msg.ToolCalls {
		if !allowedSet[tc.Function.Name] {
			return fail(fmt.Sprintf("调用了不在 allowed_tools 子集内的函数 %s", tc.Function.Name))
		}
	}
	return validToolCallArgs(msg.ToolCalls)
}

func ThinkingPresent(reasoningContent *string) bool {
	return reasoningContent != nil && strings.TrimSpace(*reasoningContent) != ""
}

func ThinkingTogglePair(onPresent, offPresent bool) Verdict {
	if !onPresent {
		return fail("开启思考开关时未返回 reasoning_content（或等价字段）")
	}
	if offPresent {
		return fail("关闭思考开关时仍返回了 reasoning_content（或等价字段）")
	}
	return pass()
}

func DefaultThinkingMatchesDeclaration(present bool, declared string) Verdict {
	switch declared {
	case "thinks_by_default":
		if !present {
			return fail("声明默认思考，但不传开关时实际未返回思考内容")
		}
	case "no_thinking_by_default":
		if present {
			return fail("声明默认不思考，但不传开关时实际返回了思考内容")
		}
	default:
		return fail(fmt.Sprintf("CAPABILITY_PROFILE.default_thinking_behavior 取值非法: %q", declared))
	}
	return pass()
}

func JSONParseable(resp openaiapi.Response) Verdict {
	if len(resp.Choices) == 0 {
		return fail("choices 为空")
	}
	msg := resp.Choices[0].Message
	if msg == nil || msg.Content == nil {
		return fail("缺少 content")
	}
	var js any
	if err := json.Unmarshal([]byte(*msg.Content), &js); err != nil {
		return fail(fmt.Sprintf("content 不是合法 JSON: %v", err))
	}
	return pass()
}

func JSONSchemaValid(resp openaiapi.Response, schema map[string]any) Verdict {
	if len(resp.Choices) == 0 {
		return fail("choices 为空")
	}
	msg := resp.Choices[0].Message
	if msg == nil || msg.Content == nil {
		return fail("缺少 content")
	}
	var data any
	if err := json.Unmarshal([]byte(*msg.Content), &data); err != nil {
		return fail(fmt.Sprintf("content 不是合法 JSON: %v", err))
	}
	violations := ValidateJSONSchema(schema, data)
	if len(violations) > 0 {
		return fail(strings.Join(violations, "; "))
	}
	return pass()
}

var digitsRe = regexp.MustCompile(`\d+`)

// MultimodalVerdict 三态：PASS / FAIL / MANUAL_REVIEW，对应 manifest.json
// answer_match_definitions.digits_exact.decision_rule。
type MultimodalVerdict struct {
	Status string
	Reason string
}

func DeterministicMultimodalQA(responseText, expectedAnswer string) MultimodalVerdict {
	matches := digitsRe.FindAllString(strings.TrimSpace(responseText), -1)
	distinct := make(map[string]bool, len(matches))
	for _, m := range matches {
		distinct[m] = true
	}
	switch len(distinct) {
	case 0:
		return MultimodalVerdict{Status: "MANUAL_REVIEW", Reason: "响应未包含可比对的数字（开放式描述）"}
	case 1:
		var only string
		for k := range distinct {
			only = k
		}
		if only == expectedAnswer {
			return MultimodalVerdict{Status: "PASS"}
		}
		return MultimodalVerdict{Status: "FAIL", Reason: fmt.Sprintf("唯一可比对答案为 %q，与预期 %q 不符", only, expectedAnswer)}
	default:
		return MultimodalVerdict{Status: "MANUAL_REVIEW", Reason: "响应包含多个不同数字，无法确定唯一作答"}
	}
}
