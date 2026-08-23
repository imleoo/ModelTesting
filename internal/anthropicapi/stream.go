package anthropicapi

import (
	"encoding/json"

	"github.com/leoobai/modeltestbed/internal/openaiapi"
)

// chunkEnvelope 只解析流式分片里判断"流是否正常结束"与"最终 usage"所需的
// 最小字段集合，不用为此走完整的 ContentBlock 归一化路径。message_delta
// 分片的 usage 是与 delta 同级的顶层字段（真实抓包验证过，2026-08-19：
// {"type":"message_delta","delta":{...},"usage":{...}}），不是嵌套在
// delta 对象内部。
type chunkEnvelope struct {
	Type  string `json:"type"`
	Usage *Usage `json:"usage"`
}

// IsStreamComplete 替代 OpenAI 协议里 "data: [DONE]" 哨兵的判定：Anthropic
// Messages 流式响应以一个 type=="message_stop" 的分片结束（真实抓包验证过，
// 2026-08-19），没有单独的 [DONE] 行；internal/sse.Parse 已经只收集
// "data:" 行本身、天然跳过 "event:" 具名事件行，不需要改动 sse 包。
func IsStreamComplete(chunks []string) bool {
	for _, c := range chunks {
		var env chunkEnvelope
		if err := json.Unmarshal([]byte(c), &env); err != nil {
			continue
		}
		if env.Type == "message_stop" {
			return true
		}
	}
	return false
}

// FinalUsage 返回流里最后一个 message_delta 分片携带的 usage（真实抓包
// 验证过：该分片的 usage 已经同时带 input_tokens 与该次响应的最终
// output_tokens，是流式场景下唯一一个"总量已定"的 usage 快照——
// message_start 只带占位的 output_tokens=1，message_stop 本身不带 usage）。
// ok=false 表示没有任何 message_delta 分片携带合法 usage。
func FinalUsage(chunks []string) (*openaiapi.Usage, bool) {
	var found *openaiapi.Usage
	for _, c := range chunks {
		var env chunkEnvelope
		if err := json.Unmarshal([]byte(c), &env); err != nil {
			continue
		}
		if env.Type != "message_delta" || env.Usage == nil {
			continue
		}
		u := env.Usage
		found = &openaiapi.Usage{
			PromptTokens:     u.InputTokens,
			CompletionTokens: u.OutputTokens,
			TotalTokens:      u.InputTokens + u.OutputTokens,
		}
	}
	if found == nil {
		return nil, false
	}
	return found, true
}
