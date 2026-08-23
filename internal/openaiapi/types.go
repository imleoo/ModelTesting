// Package openaiapi 定义 OpenAI Chat Completions 响应结构（非流式响应体 / 流式分片），
// 并实现设计方案 04 节 4.1 openai_schema_valid 全局基线断言。
package openaiapi

import "encoding/json"

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Message struct {
	Role             string     `json:"role"`
	Content          *string    `json:"content"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ReasoningContent *string    `json:"reasoning_content,omitempty"`
}

type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// PromptTokensDetails 承载输入侧 token 的细分，目前只用到上下文缓存命中数。
// 字段名对齐 OpenAI 的 usage.prompt_tokens_details.cached_tokens；部分网关把
// 命中数平铺在 usage 顶层（usage.cached_tokens），两种位置都读，见
// Usage.CachedTokens。
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type Usage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	// CachedTokensFlat 是部分网关（含 tokenpanel 转发的 z.ai）把缓存命中数直接
	// 平铺在 usage 顶层的形态。不做协议层面的对错判断，读到哪个用哪个。
	CachedTokensFlat *int `json:"cached_tokens,omitempty"`
}

// CachedTokens 返回本次请求命中上下文缓存的输入 token 数，以及该字段是否真的
// 出现在响应里。区分「命中 0 个」与「网关根本没回这个字段」很重要：前者是
// 一个可判定的业务结果（缓存没命中），后者是能力/协议缺失，两者不能都当成 0。
func (u *Usage) CachedTokens() (int, bool) {
	if u == nil {
		return 0, false
	}
	if u.PromptTokensDetails != nil {
		return u.PromptTokensDetails.CachedTokens, true
	}
	if u.CachedTokensFlat != nil {
		return *u.CachedTokensFlat, true
	}
	return 0, false
}

type Choice struct {
	Index        int      `json:"index"`
	Message      *Message `json:"message,omitempty"`
	Delta        *Message `json:"delta,omitempty"`
	FinishReason *string  `json:"finish_reason"`
}

// Response 是非流式 /v1/chat/completions 响应体。
type Response struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Chunk 是单个流式 SSE 分片（data: 后的 JSON）。
type Chunk struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

func ParseResponse(raw []byte) (Response, error) {
	var r Response
	err := json.Unmarshal(raw, &r)
	return r, err
}

func ParseChunk(raw string) (Chunk, error) {
	var c Chunk
	err := json.Unmarshal([]byte(raw), &c)
	return c, err
}
