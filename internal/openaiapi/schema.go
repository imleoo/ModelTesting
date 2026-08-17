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
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return false, []string{fmt.Sprintf("响应不是合法 JSON 对象: %v", err)}
	}

	var violations []string
	requireString(top, "id", &violations)
	requireString(top, "object", &violations)
	requireNumber(top, "created", &violations)
	requireString(top, "model", &violations)

	choicesRaw, ok := top["choices"]
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
		choice, ok := c.(map[string]any)
		if !ok {
			violations = append(violations, fmt.Sprintf("choices[%d] 应为对象", i))
			continue
		}
		requireNumber(choice, "index", &violations)
		if _, hasFinish := choice["finish_reason"]; !hasFinish {
			violations = append(violations, fmt.Sprintf("choices[%d] 缺少 finish_reason 字段", i))
		} else if choice["finish_reason"] != nil {
			if _, ok := choice["finish_reason"].(string); !ok {
				violations = append(violations, fmt.Sprintf("choices[%d].finish_reason 类型应为 string 或 null", i))
			}
		}

		key := "message"
		if streamed {
			key = "delta"
		}
		body, hasBody := choice[key]
		if !hasBody {
			violations = append(violations, fmt.Sprintf("choices[%d] 缺少 %s 字段", i, key))
			continue
		}
		bodyMap, ok := body.(map[string]any)
		if !ok {
			violations = append(violations, fmt.Sprintf("choices[%d].%s 应为对象", i, key))
			continue
		}
		if role, hasRole := bodyMap["role"]; hasRole {
			if _, ok := role.(string); !ok {
				violations = append(violations, fmt.Sprintf("choices[%d].%s.role 类型应为 string", i, key))
			}
		}
		if toolCallsRaw, hasToolCalls := bodyMap["tool_calls"]; hasToolCalls && toolCallsRaw != nil {
			toolCalls, ok := toolCallsRaw.([]any)
			if !ok {
				violations = append(violations, fmt.Sprintf("choices[%d].%s.tool_calls 应为数组", i, key))
			} else {
				for j, tc := range toolCalls {
					validateToolCall(tc, i, j, key, &violations)
				}
			}
		}
	}

	if usageRaw, hasUsage := top["usage"]; hasUsage && usageRaw != nil {
		usage, ok := usageRaw.(map[string]any)
		if !ok {
			violations = append(violations, "usage 字段应为对象")
		} else {
			requireNumber(usage, "prompt_tokens", &violations)
			requireNumber(usage, "completion_tokens", &violations)
			requireNumber(usage, "total_tokens", &violations)
		}
	}

	return len(violations) == 0, violations
}

func validateToolCall(tc any, choiceIdx, toolIdx int, key string, violations *[]string) {
	m, ok := tc.(map[string]any)
	if !ok {
		*violations = append(*violations, fmt.Sprintf("choices[%d].%s.tool_calls[%d] 应为对象", choiceIdx, key, toolIdx))
		return
	}
	requireString(m, "id", violations)
	requireString(m, "type", violations)
	fnRaw, ok := m["function"]
	if !ok {
		*violations = append(*violations, fmt.Sprintf("choices[%d].%s.tool_calls[%d] 缺少 function 字段", choiceIdx, key, toolIdx))
		return
	}
	fn, ok := fnRaw.(map[string]any)
	if !ok {
		*violations = append(*violations, fmt.Sprintf("choices[%d].%s.tool_calls[%d].function 应为对象", choiceIdx, key, toolIdx))
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
