// Package anthropicapi 定义 Anthropic Messages API 响应结构（非流式响应体 /
// 流式 SSE 分片），并把它们归一化成 internal/openaiapi 既有的 Response 结构体，
// 这样 internal/assertion 的全部断言函数、internal/engine 的绝大多数编排逻辑
// 都能对 Anthropic 协议零改动复用——只有"怎么解析原始响应"和"什么算流式
// 结束"这两处需要按协议分流（见 internal/engine/engine.go 的 style 分流
// helper）。
//
// 字段形状均已用真实请求验证（2026-08-19，对 kiro.leoobai.cn/cc 与
// api.we2ai.com 两个目标），不是照 Anthropic 文档假设的产物：
//   - content[].type=="text"：正文文本分片
//   - content[].type=="thinking"：扩展思考内容（对应 OpenAI 家族的
//     reasoning_content，本套件目标链路实测该内容块从未出现，见
//     suites/kiro-claude/SCHEMA.md 已知协议缺口 1）
//   - content[].type=="tool_use"：工具调用，input 是已解析好的 JSON 对象
//     （不是 OpenAI 那种 arguments 字符串，归一化时用 json.Marshal 转回字符串）
//   - usage.input_tokens / usage.output_tokens：无独立 total 字段可交叉校验
//     （归一化时 TotalTokens 由本包相加算出，usage 自洽性检查因此在这个协议
//     下退化为重言式，如实记录在 SCHEMA.md，不假装是等强度校验）
package anthropicapi

import (
	"encoding/json"
	"fmt"

	"github.com/leoobai/modeltestbed/internal/openaiapi"
)

type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
}

type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// Response 是非流式 POST /v1/messages 响应体。
type Response struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"` // 恒为 "message"
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      *Usage         `json:"usage"`
}

func parseResponse(raw []byte) (Response, error) {
	var r Response
	err := json.Unmarshal(raw, &r)
	return r, err
}

// ParseResponse 把一次非流式 /v1/messages 响应归一化成
// openaiapi.Response——这是本包对外的主要入口，engine 的既有断言编排代码
// 不用感知 Anthropic 的原始形状。
func ParseResponse(raw []byte) (openaiapi.Response, error) {
	r, err := parseResponse(raw)
	if err != nil {
		return openaiapi.Response{}, err
	}
	return normalize(r), nil
}

func normalize(r Response) openaiapi.Response {
	msg := &openaiapi.Message{Role: r.Role}

	var text string
	for _, block := range r.Content {
		switch block.Type {
		case "text":
			text += block.Text
		case "thinking":
			thinking := block.Thinking
			msg.ReasoningContent = &thinking
		case "tool_use":
			args := "{}"
			if len(block.Input) > 0 {
				args = string(block.Input)
			}
			msg.ToolCalls = append(msg.ToolCalls, openaiapi.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: openaiapi.FunctionCall{
					Name:      block.Name,
					Arguments: args,
				},
			})
		}
	}
	if text != "" {
		msg.Content = &text
	}

	finishReason := finishReasonFromStopReason(r.StopReason)
	choice := openaiapi.Choice{Index: 0, Message: msg, FinishReason: &finishReason}

	resp := openaiapi.Response{
		ID:      r.ID,
		Object:  "chat.completion",
		Model:   r.Model,
		Choices: []openaiapi.Choice{choice},
	}
	if r.Usage != nil {
		resp.Usage = &openaiapi.Usage{
			PromptTokens:     r.Usage.InputTokens,
			CompletionTokens: r.Usage.OutputTokens,
			TotalTokens:      r.Usage.InputTokens + r.Usage.OutputTokens,
		}
	}
	return resp
}

// finishReasonFromStopReason 把 Anthropic 的 stop_reason 映射成 OpenAI 家族
// 惯用的 finish_reason 取值，供归一化后的 openaiapi.Response 结构携带
// （目前没有断言函数读取这个字段，只是让归一化结果语义完整，便于以后如需
// 按 finish_reason 断言时可直接复用）。
func finishReasonFromStopReason(stopReason string) string {
	switch stopReason {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "":
		return "stop"
	default:
		return fmt.Sprintf("anthropic:%s", stopReason)
	}
}
