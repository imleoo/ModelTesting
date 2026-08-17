package benchmark

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ChunkEvent 记录一个 SSE 分片到达的时刻，用于计算 TTFT/TPOT/ITL——这是
// P1 阶段 internal/sse 包（只关心是否合规、不关心时序）之外单独实现的原因：
// 压测需要逐分片的到达时间戳，P1 的用例引擎不需要。
type ChunkEvent struct {
	At  time.Time
	Raw string
}

// StreamCallResult 是一次流式请求的完整时序留痕。
type StreamCallResult struct {
	HTTPStatus      int
	SentAt          time.Time
	FirstByteAt     time.Time // 首个 SSE 分片到达时刻（TTFT 锚点）
	DoneAt          time.Time
	Chunks          []ChunkEvent
	PromptTokens    int
	OutputTokens    int
	CachedTokens    int
	Err             error
	RawRequestBody  string
	RawResponseHead string // 首个/末个分片，用于失败排查留痕（不留全量，避免日志过大）
}

// StreamCall 发起一次流式 /v1/chat/completions 请求并记录逐分片到达时间。
func StreamCall(ctx context.Context, httpClient *http.Client, baseURL, apiKey string, body map[string]any, timeout time.Duration) StreamCallResult {
	body["stream"] = true
	if _, ok := body["stream_options"]; !ok {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	reqJSON, err := json.Marshal(body)
	if err != nil {
		return StreamCallResult{Err: fmt.Errorf("marshal request: %w", err)}
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/chat/completions", bytes.NewReader(reqJSON))
	if err != nil {
		return StreamCallResult{Err: fmt.Errorf("build request: %w", err), RawRequestBody: string(reqJSON)}
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	result := StreamCallResult{SentAt: time.Now(), RawRequestBody: string(reqJSON)}

	resp, err := httpClient.Do(req)
	if err != nil {
		result.Err = fmt.Errorf("http call: %w", err)
		return result
	}
	defer resp.Body.Close()
	result.HTTPStatus = resp.StatusCode

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		result.RawResponseHead = string(raw)
		result.DoneAt = time.Now()
		return result
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		now := time.Now()
		if first {
			result.FirstByteAt = now
			result.RawResponseHead = data
			first = false
		}
		if data == "[DONE]" {
			break
		}
		result.Chunks = append(result.Chunks, ChunkEvent{At: now, Raw: data})
		extractUsageFromChunk(data, &result)
	}
	if err := scanner.Err(); err != nil && result.Err == nil {
		result.Err = fmt.Errorf("read stream: %w", err)
	}
	result.DoneAt = time.Now()
	return result
}

type chunkUsagePeek struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

func extractUsageFromChunk(raw string, result *StreamCallResult) {
	var c chunkUsagePeek
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return
	}
	if c.Usage != nil {
		result.PromptTokens = c.Usage.PromptTokens
		result.OutputTokens = c.Usage.CompletionTokens
		if c.Usage.PromptTokensDetails != nil {
			result.CachedTokens = c.Usage.PromptTokensDetails.CachedTokens
		}
	}
}

// ContentChunkTimestamps 返回携带非空 delta.content 的分片到达时间，用于计算
// TPOT/ITL（跳过只带 role/空 delta/仅 usage 的控制分片，避免把它们当成"输出了
// 一个 token"）。
func (r StreamCallResult) ContentChunkTimestamps() []time.Time {
	var ts []time.Time
	for _, ch := range r.Chunks {
		var c chunkUsagePeek
		if err := json.Unmarshal([]byte(ch.Raw), &c); err != nil {
			continue
		}
		if len(c.Choices) > 0 && c.Choices[0].Delta.Content != "" {
			ts = append(ts, ch.At)
		}
	}
	return ts
}
