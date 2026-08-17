// Package client 实现对 OpenAI 兼容 /v1/chat/completions 的 HTTP 调用（含流式），
// 按设计方案 04 节要求为每次调用留痕状态码、耗时、完整请求体与响应体（或流式分片）。
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/leoobai/modeltestbed/internal/sse"
)

type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		HTTPClient: &http.Client{},
	}
}

// CallResult 是一次 HTTP 请求的完整留痕，直接对应设计方案 03 节 CASE_ATTEMPT
// 的 http_status/latency_ms/request_body/response_body 字段。
type CallResult struct {
	HTTPStatus   int
	LatencyMS    int64
	RequestBody  string
	ResponseBody string
	Streamed     bool
	SSEResult    sse.Result
	TransportErr error // 网络层错误（连接失败/超时等），HTTPStatus 此时为 0
}

// Call 发起一次 /v1/chat/completions 请求。body["stream"]==true 时按 SSE 读取，
// 否则读取整体 JSON 响应体。
func (c *Client) Call(ctx context.Context, path string, body map[string]any, timeout time.Duration) CallResult {
	reqJSON, err := json.Marshal(body)
	if err != nil {
		return CallResult{TransportErr: fmt.Errorf("marshal request body: %w", err)}
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, c.BaseURL+path, bytes.NewReader(reqJSON))
	if err != nil {
		return CallResult{RequestBody: string(reqJSON), TransportErr: fmt.Errorf("build request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	streamed, _ := body["stream"].(bool)

	start := time.Now()
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return CallResult{
			RequestBody:  string(reqJSON),
			Streamed:     streamed,
			LatencyMS:    time.Since(start).Milliseconds(),
			TransportErr: fmt.Errorf("http call: %w", err),
		}
	}
	defer resp.Body.Close()

	result := CallResult{
		RequestBody: string(reqJSON),
		HTTPStatus:  resp.StatusCode,
		Streamed:    streamed,
	}

	if streamed {
		sseResult, err := sse.Parse(resp.Body)
		result.LatencyMS = time.Since(start).Milliseconds()
		result.SSEResult = sseResult
		result.ResponseBody = reconstructSSEBody(sseResult)
		if err != nil {
			result.TransportErr = fmt.Errorf("parse sse stream: %w", err)
		}
		return result
	}

	raw, err := io.ReadAll(resp.Body)
	result.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		result.TransportErr = fmt.Errorf("read response body: %w", err)
		return result
	}
	result.ResponseBody = string(raw)
	return result
}

func reconstructSSEBody(r sse.Result) string {
	var sb strings.Builder
	for _, c := range r.Chunks {
		sb.WriteString("data: ")
		sb.WriteString(c)
		sb.WriteString("\n")
	}
	if r.SawDone {
		sb.WriteString("data: [DONE]\n")
	}
	return sb.String()
}
