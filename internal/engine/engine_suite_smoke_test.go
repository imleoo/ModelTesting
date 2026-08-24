package engine_test

// 全套件冒烟测试：加载真实的 suites/kimi-k3/suite.v1.json + materials manifest，
// 声明全部能力，起一个通用 mock 网关（尽力模拟“答对了”的模型）和真实的
// materialssrv 素材托管处理器，完整跑一遍渲染 → 调用 → 断言流水线，
// 验证端到端不出错（占位符替换、素材下载、多请求用例编排等）。
// 这不是逐用例的精确 oracle（mock 网关是启发式模拟），但能捕获集成层面的
// 崩溃/渲染错误/nil 解引用等问题，是对 engine_acceptance_test.go 里针对
// 单一断言类型的正反例测试的补充。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leoobai/modeltestbed/internal/client"
	"github.com/leoobai/modeltestbed/internal/engine"
	"github.com/leoobai/modeltestbed/internal/materialssrv"
	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/render"
	"github.com/leoobai/modeltestbed/internal/suitedef"
)

func repoRootForTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试文件路径")
	}
	// internal/engine/engine_suite_smoke_test.go -> 仓库根目录
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

func fullCapabilityProfile() model.CapabilityProfile {
	return model.CapabilityProfile{
		ImageURL:           true,
		ImageBase64:        true,
		VideoURL:           true,
		VideoBase64:        true,
		ToolCall:           true,
		ToolChoiceFunction: true,
		ThinkingToggleMethods: []string{
			"enable_thinking",
			"thinking_type",
			"chat_template_kwargs_enable_thinking",
			"chat_template_kwargs_thinking",
			"reasoning_effort_off",
		},
		DefaultThinkingBehavior: "no_thinking_by_default",
		ReasoningEffort:         true,
		ContextWindowTokens:     1048576,
		MaxOutputTokens:         131072,
		PromptCache:             true,
	}
}

// mockGatewayHandler 尽力模拟一个「全都答对」的 OpenAI 兼容网关。
func mockGatewayHandler() http.HandlerFunc {
	var cacheWarm atomic.Bool
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request json", http.StatusBadRequest)
			return
		}
		// 这个 mock 网关被 TestSmoke_FullSuiteAgainstMockGateway 当作"表现
		// 良好的被测网关"用来驱动整套 suite.v1.json（含 input_validation.*
		// 系列 rejects_invalid_request 用例），所以除了正常应答之外，还需要
		// 正确拒绝那几类故意构造的非法输入——一个真正做了输入校验的网关
		// 应该表现成这样，而不是像真实 tokenpanel 那样 500。
		if reason := invalidRequestReason(body); reason != "" {
			http.Error(w, reason, http.StatusBadRequest)
			return
		}
		streamed, _ := body["stream"].(bool)
		if streamed {
			serveMockStream(w, body)
			return
		}
		serveMockNonStream(w, body, &cacheWarm)
	}
}

// invalidRequestReason 识别 suites/kimi-k3/suite.v1.json 里 input_validation.*
// 系列用例构造的 5 类非法输入，非空字符串表示应该拒绝（400）。
func invalidRequestReason(body map[string]any) string {
	if mt, ok := body["max_tokens"].(float64); ok {
		if mt < 0 {
			return "max_tokens must be positive"
		}
		if mt > 1_000_000 {
			return "max_tokens exceeds model limit"
		}
	}
	messages, _ := body["messages"].([]any)
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		switch role {
		case "system", "user", "assistant", "tool":
		default:
			return fmt.Sprintf("invalid role %q", role)
		}
		if role == "tool" {
			if _, isString := msg["content"].(string); !isString {
				if _, isMap := msg["content"].(map[string]any); isMap {
					return "tool message content must be a string"
				}
			}
		}
		if parts, ok := msg["content"].([]any); ok {
			seen := map[string]bool{}
			for _, p := range parts {
				part, ok := p.(map[string]any)
				if !ok {
					continue
				}
				if part["type"] == "text" {
					text, _ := part["text"].(string)
					if seen[text] {
						return "duplicate content part"
					}
					seen[text] = true
				}
				if part["type"] == "image_url" {
					imgURL, _ := part["image_url"].(map[string]any)
					url, _ := imgURL["url"].(string)
					if strings.Contains(url, "not-valid-base64-data") {
						return "malformed base64 image data"
					}
				}
			}
		}
	}
	return ""
}

func serveMockNonStream(w http.ResponseWriter, body map[string]any, cacheWarm *atomic.Bool) {
	msg := buildMockMessage(body)
	usage := map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	finish := "stop"
	if reff, ok := body["reasoning_effort"].(string); ok {
		tokens := map[string]int{"low": 50, "high": 200, "max": 220}[reff]
		usage["completion_tokens_details"] = map[string]any{"reasoning_tokens": tokens}
	}

	// v1.2.0 新增的 4 条用例（stop 语义、长上下文召回、缓存命中率、
	// max_tokens 强制截断）靠固定提示词里的特征字符串识别，与 z-ai
	// 冒烟测试（engine_zai_suite_smoke_test.go）用同一套判别逻辑，
	// 复用其 firstUserText 辅助函数。
	prompt := firstUserText(body)
	switch {
	case strings.Contains(prompt, "【暗号】"):
		msg["content"] = "7391"
		usage["prompt_tokens"] = 950000
		usage["total_tokens"] = 950005
	case strings.Contains(prompt, "只回答两个字"):
		msg["content"] = "收到"
		usage["prompt_tokens"] = 5000
		cached := 0
		if cacheWarm.Swap(true) {
			cached = 4800
		}
		usage["prompt_tokens_details"] = map[string]any{"cached_tokens": cached}
	case strings.Contains(prompt, "ALPHA"):
		msg["content"] = "ALPHA\n"
	case strings.Contains(prompt, "《四季》"):
		mt, _ := body["max_tokens"].(float64)
		msg["content"] = "春天到了，风里带着新翻的泥土气息"
		finish = "length"
		usage["completion_tokens"] = int(mt)
		usage["total_tokens"] = 10 + int(mt)
	}

	resp := map[string]any{
		"id":      "chatcmpl-mock",
		"object":  "chat.completion",
		"created": 1700000000,
		"model":   "mock-model",
		"choices": []any{
			map[string]any{"index": 0, "message": msg, "finish_reason": finish},
		},
		"usage": usage,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func buildMockMessage(body map[string]any) map[string]any {
	msg := map[string]any{"role": "assistant", "content": nil}

	if thinkingOn(body) {
		msg["reasoning_content"] = "让我想想……"
	}

	if answer := multimodalAnswer(body); answer != "" {
		msg["content"] = answer
		return msg
	}

	if rf, ok := body["response_format"].(map[string]any); ok {
		switch rf["type"] {
		case "json_schema":
			msg["content"] = `{"city":"北京","temperature_c":23}`
		case "json_object":
			msg["content"] = `{"city":"北京","weather":"晴"}`
		}
		return msg
	}

	if toolCalls := mockToolCalls(body); toolCalls != nil {
		msg["tool_calls"] = toolCalls
		return msg
	}

	msg["content"] = "这是一句正常的中文回答。"
	return msg
}

func thinkingOn(body map[string]any) bool {
	if v, ok := body["enable_thinking"].(bool); ok {
		return v
	}
	if v, ok := body["thinking"].(map[string]any); ok {
		return v["type"] == "enabled"
	}
	if v, ok := body["chat_template_kwargs"].(map[string]any); ok {
		if b, ok := v["enable_thinking"].(bool); ok {
			return b
		}
		if b, ok := v["thinking"].(bool); ok {
			return b
		}
	}
	if v, ok := body["reasoning_effort"].(string); ok {
		return v != "off"
	}
	return false // default_thinking_behavior = no_thinking_by_default
}

func multimodalAnswer(body map[string]any) string {
	messages, ok := body["messages"].([]any)
	if !ok {
		return ""
	}
	for _, m := range messages {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		content, ok := mm["content"].([]any)
		if !ok {
			continue
		}
		for _, part := range content {
			pm, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if iu, ok := pm["image_url"].(map[string]any); ok {
				url, _ := iu["url"].(string)
				if strings.Contains(url, "image_qa_v1") || strings.HasPrefix(url, "data:image/") {
					return "7429"
				}
			}
			if vu, ok := pm["video_url"].(map[string]any); ok {
				url, _ := vu["url"].(string)
				if strings.Contains(url, "video_qa_v1") || strings.HasPrefix(url, "data:video/") {
					return "8153"
				}
			}
		}
	}
	return ""
}

func mockToolCalls(body map[string]any) []any {
	tools, hasTools := body["tools"].([]any)
	if !hasTools || len(tools) == 0 {
		return nil
	}
	firstToolName := func() string {
		tm, _ := tools[0].(map[string]any)
		fn, _ := tm["function"].(map[string]any)
		name, _ := fn["name"].(string)
		return name
	}

	call := func(name string) []any {
		return []any{map[string]any{
			"id":   "call_1",
			"type": "function",
			"function": map[string]any{
				"name":      name,
				"arguments": `{"city":"北京"}`,
			},
		}}
	}

	switch tc := body["tool_choice"].(type) {
	case string:
		switch tc {
		case "required":
			return call(firstToolName())
		case "none":
			return nil
		case "auto":
			return nil // 走 text_or_tool_call 的文本分支
		}
	case map[string]any:
		if tc["type"] == "function" {
			fn, _ := tc["function"].(map[string]any)
			name, _ := fn["name"].(string)
			return call(name)
		}
		if tc["type"] == "allowed_tools" {
			at, _ := tc["allowed_tools"].(map[string]any)
			tools, _ := at["tools"].([]any)
			if len(tools) > 0 {
				tm, _ := tools[0].(map[string]any)
				fn, _ := tm["function"].(map[string]any)
				name, _ := fn["name"].(string)
				return call(name)
			}
		}
	default:
		// Default（不传 tool_choice）：走文本分支，测 text_or_tool_call。
		return nil
	}
	return nil
}

func serveMockStream(w http.ResponseWriter, body map[string]any) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")

	writeChunk := func(delta map[string]any, finish any, usage map[string]any) {
		chunk := map[string]any{
			"id": "chatcmpl-mock", "object": "chat.completion.chunk",
			"created": 1700000000, "model": "mock-model",
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		}
		if usage != nil {
			chunk["usage"] = usage
		}
		raw, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", raw)
		flusher.Flush()
	}

	writeChunk(map[string]any{"role": "assistant", "content": "你"}, nil, nil)
	writeChunk(map[string]any{"content": "好"}, nil, nil)

	var usage map[string]any
	opts, _ := body["stream_options"].(map[string]any)
	includeUsage, _ := opts["include_usage"].(bool)
	if includeUsage {
		usage = map[string]any{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}
	}
	writeChunk(map[string]any{}, "stop", usage)

	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func TestSmoke_FullSuiteAgainstMockGateway(t *testing.T) {
	repoRoot := repoRootForTest(t)

	suite, err := suitedef.LoadSuite(filepath.Join(repoRoot, "suites/kimi-k3/suite.v1.json"))
	if err != nil {
		t.Fatalf("加载套件失败: %v", err)
	}
	materials, err := suitedef.LoadMaterialsManifestForSuite(repoRoot, suite)
	if err != nil {
		t.Fatalf("加载素材清单失败: %v", err)
	}

	materialsHandler, err := materialssrv.NewHandler(filepath.Join(repoRoot, "suites"))
	if err != nil {
		t.Fatalf("构建素材托管 handler 失败: %v", err)
	}
	materialsSrv := httptest.NewServer(materialsHandler)
	defer materialsSrv.Close()

	gatewaySrv := httptest.NewServer(mockGatewayHandler())
	defer gatewaySrv.Close()

	e := &engine.Engine{
		Client:     client.New(gatewaySrv.URL, "mock-key"),
		Suite:      suite,
		Materials:  materials,
		Capability: fullCapabilityProfile(),
		RenderCtx: render.Context{
			ModelKey:        "mock-model",
			APIKey:          "mock-key",
			Fixtures:        suite.Fixtures,
			Materials:       materials,
			MaterialBaseURL: materialsSrv.URL,
		},
	}

	notDeclaredCount := 0
	for _, c := range suite.Cases {
		result := e.RunCase(context.Background(), c)
		t.Logf("%-45s %-13s attempts=%d passed=%d reason=%s",
			c.ID, result.Status, result.Attempts, result.PassedAttempts, result.FailReason)

		if result.Status == model.StatusNotDeclared {
			notDeclaredCount++
			t.Errorf("用例 %s 在全量能力声明下不应为 NOT_DECLARED: %s", c.ID, result.FailReason)
			continue
		}
		if result.Status != model.StatusPass {
			t.Errorf("用例 %s 期望 PASS（mock 网关按预期作答），实际 %s: %s", c.ID, result.Status, result.FailReason)
		}
	}
}
