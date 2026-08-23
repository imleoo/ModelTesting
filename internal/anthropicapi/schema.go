package anthropicapi

import (
	"encoding/json"
	"fmt"
)

// ValidateSchema 是 Anthropic Messages 协议版的全局基线断言（对应
// internal/openaiapi/schema.go 的 openai_schema_valid，风格保持一致：未知
// 字段宽松放行，声明必需的字段严格校验存在性与类型）。streamed=true 时按
// 单个 SSE data 分片（message_start/content_block_delta/... 等具名事件的
// 载荷）校验，否则按整体非流式响应体校验。字段形状均已用真实请求验证
// （2026-08-19），见 types.go 包注释。
func ValidateSchema(raw []byte, streamed bool) (bool, []string) {
	if streamed {
		return validateChunk(raw)
	}
	return validateNonStream(raw)
}

func validateNonStream(raw []byte) (bool, []string) {
	topMap, violations, ok := decodeObject(raw)
	if !ok {
		return false, violations
	}

	requireString(topMap, "id", &violations)
	requireExactString(topMap, "type", "message", &violations)
	requireString(topMap, "role", &violations)
	requireString(topMap, "model", &violations)
	requireStringOrNull(topMap, "stop_reason", &violations)

	contentRaw, hasContent := topMap["content"]
	if !hasContent {
		violations = append(violations, "缺少 content 字段")
	} else if content, ok := contentRaw.([]any); !ok {
		violations = append(violations, "content 字段类型应为数组")
	} else {
		for i, block := range content {
			validateContentBlock(block, i, &violations)
		}
	}

	if usageRaw, hasUsage := topMap["usage"]; !hasUsage || usageRaw == nil {
		violations = append(violations, "缺少 usage 字段")
	} else if usage, ok := usageRaw.(map[string]any); !ok {
		violations = append(violations, "usage 字段应为对象")
	} else {
		requireInteger(usage, "input_tokens", &violations)
		requireInteger(usage, "output_tokens", &violations)
	}

	return len(violations) == 0, violations
}

func validateContentBlock(b any, i int, violations *[]string) {
	prefix := fmt.Sprintf("content[%d]", i)
	block, ok := b.(map[string]any)
	if !ok {
		*violations = append(*violations, prefix+" 应为对象")
		return
	}
	typ, ok := block["type"].(string)
	if !ok {
		*violations = append(*violations, prefix+" 缺少 type 字段或类型应为 string")
		return
	}
	switch typ {
	case "text":
		requireString(block, "text", violations)
	case "thinking":
		requireString(block, "thinking", violations)
	case "tool_use":
		requireString(block, "id", violations)
		requireString(block, "name", violations)
		if _, has := block["input"]; !has {
			*violations = append(*violations, prefix+" 缺少 input 字段")
		}
	default:
		*violations = append(*violations, fmt.Sprintf("%s.type 取值 %q 不是已知的 text/thinking/tool_use 之一", prefix, typ))
	}
}

// knownChunkTypes 是流式响应里具名事件载荷（data: 之后的 JSON）里
// "type" 字段的已知取值全集，真实抓包验证过（2026-08-19）。
var knownChunkTypes = map[string]bool{
	"ping":                true,
	"message_start":       true,
	"content_block_start": true,
	"content_block_delta": true,
	"content_block_stop":  true,
	"message_delta":       true,
	"message_stop":        true,
}

func validateChunk(raw []byte) (bool, []string) {
	topMap, violations, ok := decodeObject(raw)
	if !ok {
		return false, violations
	}

	typ, ok := topMap["type"].(string)
	if !ok {
		return false, append(violations, "缺少 type 字段或类型应为 string")
	}
	if !knownChunkTypes[typ] {
		violations = append(violations, fmt.Sprintf("type 取值 %q 不是已知的流式事件类型之一", typ))
	}

	switch typ {
	case "message_start":
		msgRaw, has := topMap["message"]
		if !has {
			violations = append(violations, "message_start 缺少 message 字段")
			break
		}
		msg, ok := msgRaw.(map[string]any)
		if !ok {
			violations = append(violations, "message_start.message 应为对象")
			break
		}
		if usageRaw, has := msg["usage"]; !has || usageRaw == nil {
			violations = append(violations, "message_start.message 缺少 usage 字段")
		} else if usage, ok := usageRaw.(map[string]any); !ok {
			violations = append(violations, "message_start.message.usage 应为对象")
		} else {
			requireInteger(usage, "input_tokens", &violations)
		}
	case "message_delta":
		if _, has := topMap["delta"]; !has {
			violations = append(violations, "message_delta 缺少 delta 字段")
		}
	}

	return len(violations) == 0, violations
}

func decodeObject(raw []byte) (map[string]any, []string, bool) {
	var top any
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, []string{fmt.Sprintf("响应不是合法 JSON: %v", err)}, false
	}
	topMap, ok := top.(map[string]any)
	if !ok {
		return nil, []string{fmt.Sprintf("响应顶层不是 JSON 对象，实际为 %s", goTypeName(top))}, false
	}
	return topMap, nil, true
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

func requireStringOrNull(m map[string]any, field string, violations *[]string) {
	v, ok := m[field]
	if !ok {
		*violations = append(*violations, fmt.Sprintf("缺少 %s 字段", field))
		return
	}
	if v == nil {
		return
	}
	if _, ok := v.(string); !ok {
		*violations = append(*violations, fmt.Sprintf("%s 字段类型应为 string 或 null", field))
	}
}

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
