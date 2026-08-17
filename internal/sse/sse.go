// Package sse 手工解析 OpenAI 兼容 /v1/chat/completions 的流式 SSE 响应
// （data: 分片，以 [DONE] 结束），对应设计方案 10.1 节选型：
// net/http + bufio.Scanner，不依赖第三方 EventSource 客户端。
package sse

import (
	"bufio"
	"io"
	"strings"
)

type Result struct {
	Chunks  []string // 每个分片 "data: " 之后的原始 JSON 文本（已排除 [DONE] 本身）
	SawDone bool     // 是否收到 [DONE] 结束标记
}

// Parse 按行读取 SSE 流，收集所有 "data: " 分片，直到遇到 "data: [DONE]" 或流结束。
// 网关中途断流（未见到 [DONE] 就 EOF）时 SawDone 保持 false，调用方据此判定
// stream_integrity 断言是否通过。
func Parse(r io.Reader) (Result, error) {
	var res Result
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			res.SawDone = true
			break
		}
		res.Chunks = append(res.Chunks, data)
	}
	if err := scanner.Err(); err != nil {
		return res, err
	}
	return res, nil
}
