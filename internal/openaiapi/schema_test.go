package openaiapi

import "testing"

// 回归测试：真实 OpenAI Chat Completions 流式协议在
// stream_options.include_usage=true 时，末尾会额外发一个 choices:[]、只携带
// usage 的收尾分片（2026-09-17 用 gpt-6-astra 真实抓包确认），这是合规行为。
// ValidateSchema 曾对流式/非流式一视同仁地要求 choices 非空，导致这个合规
// 收尾分片被误判违规——接入 gpt-oai 供应商时首次跑真实 OpenAI 协议网关才
// 暴露，之前只测过 kimi-k3/z-ai 从未触发过这个分片形状。

func TestValidateSchema_StreamedTerminalUsageChunk_EmptyChoicesAllowed(t *testing.T) {
	raw := []byte(`{
		"id": "resp_1",
		"object": "chat.completion.chunk",
		"created": 1700000000,
		"model": "gpt-6-astra",
		"choices": [],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`)
	ok, violations := ValidateSchema(raw, true)
	if !ok {
		t.Fatalf("期望通过（合规的 usage 收尾分片），实际违规: %v", violations)
	}
}

func TestValidateSchema_StreamedEmptyChoicesWithoutUsage_StillRejected(t *testing.T) {
	raw := []byte(`{
		"id": "resp_2",
		"object": "chat.completion.chunk",
		"created": 1700000000,
		"model": "gpt-6-astra",
		"choices": []
	}`)
	ok, violations := ValidateSchema(raw, true)
	if ok {
		t.Fatal("期望 FAIL（既没有 choices 也没有 usage 的空分片不是合规的收尾分片），实际 PASS")
	}
	found := false
	for _, v := range violations {
		if v == "choices 数组不应为空" {
			found = true
		}
	}
	if !found {
		t.Fatalf("期望违规列表包含 choices 数组不应为空，实际: %v", violations)
	}
}

func TestValidateSchema_NonStreamedEmptyChoices_StillRejected(t *testing.T) {
	raw := []byte(`{
		"id": "resp_3",
		"object": "chat.completion",
		"created": 1700000000,
		"model": "gpt-6-astra",
		"choices": [],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`)
	ok, violations := ValidateSchema(raw, false)
	if ok {
		t.Fatal("期望 FAIL（非流式响应即便带 usage，choices 为空也不合规），实际 PASS")
	}
	found := false
	for _, v := range violations {
		if v == "choices 数组不应为空" {
			found = true
		}
	}
	if !found {
		t.Fatalf("期望违规列表包含 choices 数组不应为空，实际: %v", violations)
	}
}
