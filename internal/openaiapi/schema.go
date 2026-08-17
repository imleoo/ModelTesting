package openaiapi

import (
	"encoding/json"
	"fmt"
)

// ValidateSchema 实现设计方案 04 节 4.1 openai_schema_valid 全局基线断言：
// 响应字段名、层级、类型必须符合 OpenAI Chat Completions 规范。streamed=true 时
// 按流式分片（choices[].delta）校验，否则按非流式响应（choices[].message）校验。
// 返回 (是否合规, 违规原因列表)；违规列表为空即视为通过。
func ValidateSchema(raw []byte, streamed bool) (bool, []string) {
	var top any
	if err := json.Unmarshal(raw, &top); err != nil {
		return false, []string{fmt.Sprintf("响应不是合法 JSON: %v", err)}
	}
	topMap, ok := top.(map[string]any)
	if !ok {
		return false, []string{fmt.Sprintf("响应顶层不是 JSON 对象，实际为 %s", goTypeName(top))}
	}

	var violations []string
	requireString(topMap, "id", &violations)
	wantObject := "chat.completion"
	if streamed {
		wantObject = "chat.completion.chunk"
	}
	requireExactString(topMap, "object", wantObject, &violations)
	requireNumber(topMap, "created", &violations)
	requireString(topMap, "model", &violations)

	choicesRaw, ok := topMap["choices"]
	if !ok {
		violations = append(violations, "缺少 choices 字段")
		return len(violations) == 0, violations
	}
	choices, ok := choicesRaw.([]any)
	if !ok {
		violations = append(violations, "choices 字段类型应为数组")
		return len(violations) == 0, violations
	}
	if len(choices) == 0 {
		violations = append(violations, "choices 数组不应为空")
	}
	for i, c := range choices {
		validateChoice(c, i, streamed, &violations)
	}

	if usageRaw, hasUsage := topMap["usage"]; hasUsage && usageRaw != nil {
		usage, ok := usageRaw.(map[string]any)
		if !ok {
			violations = append(violations, "usage 字段应为对象")
		} else {
			requireInteger(usage, "prompt_tokens", &violations)
			requireInteger(usage, "completion_tokens", &violations)
			requireInteger(usage, "total_tokens", &violations)
		}
	}

	return len(violations) == 0, violations
}

func validateChoice(c any, i int, streamed bool, violations *[]string) {
	choice, ok := c.(map[string]any)
	if !ok {
		*violations = append(*violations, fmt.Sprintf("choices[%d] 应为对象", i))
		return
	}
	requireNumber(choice, "index", violations)
	if _, hasFinish := choice["finish_reason"]; !hasFinish {
		*violations = append(*violations, fmt.Sprintf("choices[%d] 缺少 finish_reason 字段", i))
	} else if choice["finish_reason"] != nil {
		if _, ok := choice["finish_reason"].(string); !ok {
			*violations = append(*violations, fmt.Sprintf("choices[%d].finish_reason 类型应为 string 或 null", i))
		}
	}

	key := "message"
	if streamed {
		key = "delta"
	}
	body, hasBody := choice[key]
	if !hasBody {
		*violations = append(*violations, fmt.Sprintf("choices[%d] 缺少 %s 字段", i, key))
		return
	}
	bodyMap, ok := body.(map[string]any)
	if !ok {
		*violations = append(*violations, fmt.Sprintf("choices[%d].%s 应为对象", i, key))
		return
	}
	validateMessageBody(bodyMap, i, key, streamed, violations)
}

func validateMessageBody(bodyMap map[string]any, choiceIdx int, key string, streamed bool, violations *[]string) {
	prefix := fmt.Sprintf("choices[%d].%s", choiceIdx, key)

	contentVal, hasContentKey := bodyMap["content"]
	if hasContentKey && contentVal != nil {
		if _, ok := contentVal.(string); !ok {
			*violations = append(*violations, prefix+".content 类型应为 string 或 null")
		}
	}

	if !streamed {
		// 非流式 message：role 必须存在（delta 允许省略，只有首个分片才带 role）。
		requireString(bodyMap, "role", violations)
	} else if role, hasRole := bodyMap["role"]; hasRole && role != nil {
		if _, ok := role.(string); !ok {
			*violations = append(*violations, prefix+".role 类型应为 string")
		}
	}

	if rc, has := bodyMap["reasoning_content"]; has && rc != nil {
		if _, ok := rc.(string); !ok {
			*violations = append(*violations, prefix+".reasoning_content 类型应为 string 或 null")
		}
	}

	hasValidToolCalls := false
	if toolCallsRaw, hasToolCalls := bodyMap["tool_calls"]; hasToolCalls && toolCallsRaw != nil {
		toolCalls, ok := toolCallsRaw.([]any)
		if !ok {
			*violations = append(*violations, prefix+".tool_calls 应为数组")
		} else if len(toolCalls) > 0 {
			before := len(*violations)
			for j, tc := range toolCalls {
				validateToolCall(tc, choiceIdx, j, key, violations)
			}
			hasValidToolCalls = len(*violations) == before
		}
	}

	// content 键可以省略，但只有在真的存在合法 tool_calls 时才算"这条消息有实质
	// 内容"——真实网关返回 tool_calls 时常见做法是直接省略 content 字段而不是
	// 显式写 "content":null（P2 用真实 kimi-k3 网关验证时发现的情况，二者语义
	// 等价）。但不能反过来放宽到"content 和 tool_calls 都没有也算合规"，那种
	// 响应本质上是空消息，必须判违规，不能被这条兼容规则掩盖过去。
	// 只对非流式 message 做这条约束：流式 delta 天然会拆成多个分片，单个分片
	// （例如只带 finish_reason 的收尾分片）既没有 content 也没有 tool_calls 是
	// 正常现象，不能套用同一条"消息不能为空"的规则。
	if !streamed && !hasContentKey && !hasValidToolCalls {
		*violations = append(*violations, prefix+" 既没有 content 字段也没有合法 tool_calls，消息内容为空")
	}
}

func validateToolCall(tc any, choiceIdx, toolIdx int, key string, violations *[]string) {
	prefix := fmt.Sprintf("choices[%d].%s.tool_calls[%d]", choiceIdx, key, toolIdx)
	m, ok := tc.(map[string]any)
	if !ok {
		*violations = append(*violations, prefix+" 应为对象")
		return
	}
	requireString(m, "id", violations)
	requireExactString(m, "type", "function", violations)
	fnRaw, ok := m["function"]
	if !ok {
		*violations = append(*violations, prefix+" 缺少 function 字段")
		return
	}
	fn, ok := fnRaw.(map[string]any)
	if !ok {
		*violations = append(*violations, prefix+".function 应为对象")
		return
	}
	requireString(fn, "name", violations)
	requireString(fn, "arguments", violations)
}

func requireString(m map[string]any, field string, violations *[]string) {
	v, ok := m[field]
	if !ok {
		*violations = append(*violations, fmt.Sprintf("缺少 %s 字段", field))
		return
	}
	if _, ok := v.(string); !ok {
		*violations = append(*violations, fmt.Sprintf("%s 字段类型应为 string", field))
	}
}

// requireExactString 校验字段存在、类型为 string，且取值精确等于 want
// （用于 object、tool_calls[].type 这类只应取固定字面量的字段，不是任意字符串都算合规）。
func requireExactString(m map[string]any, field, want string, violations *[]string) {
	v, ok := m[field]
	if !ok {
		*violations = append(*violations, fmt.Sprintf("缺少 %s 字段", field))
		return
	}
	s, ok := v.(string)
	if !ok {
		*violations = append(*violations, fmt.Sprintf("%s 字段类型应为 string", field))
		return
	}
	if s != want {
		*violations = append(*violations, fmt.Sprintf("%s 字段取值应为 %q，实际为 %q", field, want, s))
	}
}

func requireNumber(m map[string]any, field string, violations *[]string) {
	v, ok := m[field]
	if !ok {
		*violations = append(*violations, fmt.Sprintf("缺少 %s 字段", field))
		return
	}
	if _, ok := v.(float64); !ok {
		*violations = append(*violations, fmt.Sprintf("%s 字段类型应为 number", field))
	}
}

// requireInteger 校验字段是不带小数部分的数字（token 计数不应是小数）。
func requireInteger(m map[string]any, field string, violations *[]string) {
	v, ok := m[field]
	if !ok {
		*violations = append(*violations, fmt.Sprintf("缺少 %s 字段", field))
		return
	}
	f, ok := v.(float64)
	if !ok {
		*violations = append(*violations, fmt.Sprintf("%s 字段类型应为 number", field))
		return
	}
	if f != float64(int64(f)) {
		*violations = append(*violations, fmt.Sprintf("%s 应为整数（token 计数不应含小数部分），实际为 %v", field, f))
	}
}

func goTypeName(data any) string {
	switch data.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", data)
	}
}
