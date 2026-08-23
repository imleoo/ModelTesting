package engine_test

// z-ai 套件的全套件冒烟测试：加载真实的 suites/z-ai/suite.v1.json，按
// suites/z-ai/capability_z-ai.real.json 声明能力，对一个"表现良好的 z.ai 网关"
// mock 完整跑一遍渲染 → 调用 → 断言流水线。
//
// 与 kimi-k3 冒烟测试的定位相同：不是逐用例的精确 oracle，而是保证套件数据与
// 引擎实现始终对得上——占位符能渲染、assertion_type 都被引擎认识、能力门禁
// 不会误杀、多请求用例的编排不会 panic。套件 JSON 改坏了，这里会立刻红。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leoobai/modeltestbed/internal/client"
	"github.com/leoobai/modeltestbed/internal/engine"
	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/render"
	"github.com/leoobai/modeltestbed/internal/suitedef"
)

// zaiMockGateway 模拟一个"每一项都做对了"的 z.ai 网关，但保留一个真实的
// 供应商特性：reasoning_effort 的 low 与 high 映射到同一档思考强度（低档），
// 只有 max 才明显更高。套件正是因为这个特性才把可区分档位对声明成 low/max，
// 这里如实复现，确保那条声明真的有效。
func zaiMockGateway() http.HandlerFunc {
	var cacheWarm atomic.Bool
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request json", http.StatusBadRequest)
			return
		}
		// 输入校验：max_tokens 超过模型上限必须在网关层拒绝。
		if mt, ok := body["max_tokens"].(float64); ok && mt > 131072 {
			http.Error(w, "max_tokens exceeds model limit 131072", http.StatusBadRequest)
			return
		}
		if streamed, _ := body["stream"].(bool); streamed {
			serveMockStream(w, body)
			return
		}
		serveZaiNonStream(w, body, &cacheWarm)
	}
}

func serveZaiNonStream(w http.ResponseWriter, body map[string]any, cacheWarm *atomic.Bool) {
	usage := map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	msg := map[string]any{"role": "assistant", "content": nil}
	finish := "stop"

	if zaiThinkingOn(body) {
		msg["reasoning_content"] = "让我想想……"
	}
	if reff, ok := body["reasoning_effort"].(string); ok {
		// low 与 high 同档，是 z.ai 在 GLM-5.2 上的真实映射。
		tokens := map[string]int{"off": 0, "low": 120, "high": 120, "max": 900}[reff]
		usage["completion_tokens_details"] = map[string]any{"reasoning_tokens": tokens}
	}

	prompt := firstUserText(body)
	switch {
	case strings.Contains(prompt, "【暗号】"):
		msg["content"] = "7391"
		usage["prompt_tokens"] = 950000
		usage["total_tokens"] = 950004
	case strings.Contains(prompt, "只回答两个字"):
		msg["content"] = "收到"
		usage["prompt_tokens"] = 5000
		cached := 0
		if cacheWarm.Swap(true) {
			cached = 4800
		}
		usage["prompt_tokens_details"] = map[string]any{"cached_tokens": cached}
	case strings.Contains(prompt, "ALPHA"):
		// stop=["BRAVO"] 生效：只吐出 stop 序列之前的内容。
		msg["content"] = "ALPHA\n"
	case strings.Contains(prompt, "哈希函数"):
		// temperature=0 下逐字返回同一段文本。
		msg["content"] = "哈希函数把任意长度的输入映射成固定长度的摘要，相同输入总得到相同输出。"
	case strings.Contains(prompt, "《四季》"):
		mt, _ := body["max_tokens"].(float64)
		msg["content"] = "春天到了，风里带着新翻的泥土气息"
		finish = "length"
		usage["completion_tokens"] = int(mt)
		usage["total_tokens"] = 10 + int(mt)
	default:
		msg["content"], finish = zaiDefaultContent(body)
		if tc := mockToolCalls(body); tc != nil {
			msg["tool_calls"] = tc
			msg["content"] = nil
		}
	}

	resp := map[string]any{
		"id": "chatcmpl-mock", "object": "chat.completion", "created": 1700000000, "model": "mock-model",
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
		"usage":   usage,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func zaiDefaultContent(body map[string]any) (any, string) {
	if rf, ok := body["response_format"].(map[string]any); ok {
		switch rf["type"] {
		case "json_schema":
			return `{"city":"测试城市","temperature_c":21.5}`, "stop"
		case "json_object":
			return `{"city":"测试城市","temperature_c":21.5}`, "stop"
		}
	}
	return "这是一句正常的中文回答。", "stop"
}

// zaiThinkingOn 复现 z.ai 的两条关闭思考路径：thinking.type 与 reasoning_effort。
// 不传任何开关时按 CAPABILITY_PROFILE 声明的 thinks_by_default 返回思考内容。
func zaiThinkingOn(body map[string]any) bool {
	if v, ok := body["thinking"].(map[string]any); ok {
		return v["type"] == "enabled"
	}
	if v, ok := body["reasoning_effort"].(string); ok {
		return v != "off"
	}
	return true
}

func firstUserText(body map[string]any) string {
	messages, _ := body["messages"].([]any)
	var sb strings.Builder
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if s, ok := msg["content"].(string); ok {
			sb.WriteString(s)
		}
	}
	return sb.String()
}

func TestSmoke_ZaiSuiteAgainstMockGateway(t *testing.T) {
	root := repoRootForTest(t)
	suite, err := suitedef.LoadSuite(filepath.Join(root, "suites/z-ai/suite.v1.json"))
	if err != nil {
		t.Fatalf("加载 z-ai 套件失败: %v", err)
	}
	materials, err := suitedef.LoadMaterialsManifestForSuite(root, suite)
	if err != nil {
		t.Fatalf("纯文本套件不声明 materials_manifest 时不应报错: %v", err)
	}
	if materials != nil {
		t.Fatalf("z-ai 套件不含多模态用例，不应加载出素材清单")
	}

	raw, err := os.ReadFile(filepath.Join(root, "suites/z-ai/capability_z-ai.real.json"))
	if err != nil {
		t.Fatalf("读取能力声明失败: %v", err)
	}
	var capability model.CapabilityProfile
	if err := json.Unmarshal(raw, &capability); err != nil {
		t.Fatalf("解析能力声明失败: %v", err)
	}
	if err := model.ValidateCapabilityProfile(capability); err != nil {
		t.Fatalf("能力声明未通过校验: %v", err)
	}

	srv := httptest.NewServer(zaiMockGateway())
	defer srv.Close()

	e := &engine.Engine{
		Client:     client.New(srv.URL, "test-key"),
		Suite:      suite,
		Capability: capability,
		Style:      suite.Protocol.Style,
		RenderCtx:  render.Context{ModelKey: "glm-5.2", APIKey: "test-key", Fixtures: suite.Fixtures},
	}

	for _, c := range suite.Cases {
		t.Run(c.ID, func(t *testing.T) {
			result := e.RunCase(context.Background(), c)
			if result.Status != model.StatusPass {
				t.Fatalf("对一个表现良好的 mock 网关，用例 %s 应判 PASS，实际 %s: %s",
					c.ID, result.Status, failDetail(result))
			}
		})
	}
}

func failDetail(r model.CaseResult) string {
	if r.FailReason != "" {
		return r.FailReason
	}
	for _, a := range r.CaseAttempts {
		if !a.Passed {
			return fmt.Sprintf("[%s] %s", a.VariantLabel, a.FailReason)
		}
	}
	return "(无失败原因留痕)"
}
