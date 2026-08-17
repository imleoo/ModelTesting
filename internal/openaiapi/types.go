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

type Usage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
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
