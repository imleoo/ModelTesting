package engine_test

// 覆盖 z-ai 套件引入的 5 个断言类型（max_tokens_truncation /
// stop_sequence_respected / deterministic_repeat / long_context_recall /
// prompt_cache_hit_rate）与三项引擎改造（repeat_attempts 通用化、
// reasoning_effort 比较对参数化、trace_body_max_chars 留痕截断）。
// 用例来源见 suites/z-ai/suite.v1.json 与 suites/z-ai/SCHEMA.md。

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/suitedef"
)

// completion 拼一个通过 openai_schema_valid 基线的非流式响应体。
func completion(content, finishReason, usageJSON string) string {
	if usageJSON == "" {
		usageJSON = `"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}`
	}
	return fmt.Sprintf(`{
		"id":"chatcmpl-1","object":"chat.completion","created":1700000000,"model":"test-model",
		"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":%q}],
		%s
	}`, content, finishReason, usageJSON)
}

func caseWithParams(assertionType string, body map[string]any, params map[string]any, repeat int) suitedef.Case {
	c := simpleCase(assertionType, body)
	c.AssertionParams = params
	c.RepeatAttempts = repeat
	return c
}

func serve(t *testing.T, bodies func(n int32) (int, string)) (*testEnv, func()) {
	t.Helper()
	var calls atomic.Int32
	e, closeFn := newTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		status, body := bodies(n)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	})
	return &testEnv{engine: e, calls: &calls}, closeFn
}

type testEnv struct {
	engine interface {
		RunCase(context.Context, suitedef.Case) model.CaseResult
	}
	calls *atomic.Int32
}

// ── max_tokens_truncation ────────────────────────────────────────────────

func TestMaxTokensTruncation_LengthAndWithinBudget(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("这是被截断的开", "length",
			`"usage":{"prompt_tokens":12,"completion_tokens":16,"total_tokens":28}`)
	})
	defer closeFn()

	c := caseWithParams("max_tokens_truncation", map[string]any{
		"model":      "{{model_key}}",
		"messages":   []any{map[string]any{"role": "user", "content": "写一篇长文"}},
		"max_tokens": 16,
		"stream":     false,
	}, nil, 1)

	if got := env.engine.RunCase(context.Background(), c); got.Status != model.StatusPass {
		t.Fatalf("期望 PASS，实际 %s: %s", got.Status, got.FailReason)
	}
}

func TestMaxTokensTruncation_FinishReasonStopIsFail(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("完整回答", "stop",
			`"usage":{"prompt_tokens":12,"completion_tokens":4,"total_tokens":16}`)
	})
	defer closeFn()

	c := caseWithParams("max_tokens_truncation", map[string]any{
		"model":      "{{model_key}}",
		"messages":   []any{map[string]any{"role": "user", "content": "写一篇长文"}},
		"max_tokens": 16,
	}, nil, 1)

	got := env.engine.RunCase(context.Background(), c)
	if got.Status != model.StatusFail {
		t.Fatalf("finish_reason=stop 时期望 FAIL，实际 %s", got.Status)
	}
	if !strings.Contains(got.FailReason, "length") {
		t.Errorf("失败原因应点明期望 finish_reason=length，实际: %s", got.FailReason)
	}
}

func TestMaxTokensTruncation_CompletionTokensOverBudgetIsFail(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("超出预算的输出", "length",
			`"usage":{"prompt_tokens":12,"completion_tokens":99,"total_tokens":111}`)
	})
	defer closeFn()

	c := caseWithParams("max_tokens_truncation", map[string]any{
		"model":      "{{model_key}}",
		"messages":   []any{map[string]any{"role": "user", "content": "写一篇长文"}},
		"max_tokens": 16,
	}, nil, 1)

	got := env.engine.RunCase(context.Background(), c)
	if got.Status != model.StatusFail {
		t.Fatalf("completion_tokens 超过 max_tokens 时期望 FAIL，实际 %s", got.Status)
	}
}

// usage 缺失必须判 FAIL：截图里 z.ai 的 usage 字段"不稳定"，而这条用例只能靠
// usage 证明截断落在正确位置，不能因为 finish_reason 对了就宽松放行。
func TestMaxTokensTruncation_MissingUsageIsFail(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, `{"id":"c1","object":"chat.completion","created":1700000000,"model":"test-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"被截断"},"finish_reason":"length"}]}`
	})
	defer closeFn()

	c := caseWithParams("max_tokens_truncation", map[string]any{
		"model":      "{{model_key}}",
		"messages":   []any{map[string]any{"role": "user", "content": "写一篇长文"}},
		"max_tokens": 16,
	}, nil, 1)

	if got := env.engine.RunCase(context.Background(), c); got.Status != model.StatusFail {
		t.Fatalf("usage 缺失时期望 FAIL，实际 %s", got.Status)
	}
}

// ── stop_sequence_respected ──────────────────────────────────────────────

func stopCase(params map[string]any) suitedef.Case {
	return caseWithParams("stop_sequence_respected", map[string]any{
		"model":    "{{model_key}}",
		"messages": []any{map[string]any{"role": "user", "content": "依次输出 ALPHA、BRAVO、CHARLIE"}},
		"stop":     []any{"BRAVO"},
	}, params, 1)
}

func TestStopSequence_Respected(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("ALPHA\n", "stop", "")
	})
	defer closeFn()

	if got := env.engine.RunCase(context.Background(), stopCase(map[string]any{"must_contain": "ALPHA"})); got.Status != model.StatusPass {
		t.Fatalf("期望 PASS，实际 %s: %s", got.Status, got.FailReason)
	}
}

func TestStopSequence_StopWordLeakedIsFail(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("ALPHA\nBRAVO\nCHARLIE", "stop", "")
	})
	defer closeFn()

	got := env.engine.RunCase(context.Background(), stopCase(map[string]any{"must_contain": "ALPHA"}))
	if got.Status != model.StatusFail {
		t.Fatalf("输出里仍含 stop 序列时期望 FAIL，实际 %s", got.Status)
	}
	if !strings.Contains(got.FailReason, "BRAVO") {
		t.Errorf("失败原因应点名泄漏的 stop 序列，实际: %s", got.FailReason)
	}
}

// 空输出 + 不含 stop 串本身也满足"没泄漏"，必须靠 must_contain 挡住，
// 否则这条断言会把"模型什么都没生成"判成通过。
func TestStopSequence_EmptyOutputIsFail(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("", "stop", "")
	})
	defer closeFn()

	if got := env.engine.RunCase(context.Background(), stopCase(map[string]any{"must_contain": "ALPHA"})); got.Status != model.StatusFail {
		t.Fatalf("空输出时期望 FAIL，实际 %s", got.Status)
	}
}

// ── deterministic_repeat ─────────────────────────────────────────────────

func determinismCase(repeat int) suitedef.Case {
	return caseWithParams("deterministic_repeat", map[string]any{
		"model":       "{{model_key}}",
		"messages":    []any{map[string]any{"role": "user", "content": "1+1=?"}},
		"temperature": 0,
	}, nil, repeat)
}

func TestDeterministicRepeat_IdenticalOutputsPass(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("2", "stop", "")
	})
	defer closeFn()

	got := env.engine.RunCase(context.Background(), determinismCase(3))
	if got.Status != model.StatusPass {
		t.Fatalf("期望 PASS，实际 %s: %s", got.Status, got.FailReason)
	}
	if len(got.CaseAttempts) != 3 {
		t.Fatalf("期望 3 条采样留痕，实际 %d", len(got.CaseAttempts))
	}
	if env.calls.Load() != 3 {
		t.Fatalf("期望实际发出 3 次请求，实际 %d", env.calls.Load())
	}
}

func TestDeterministicRepeat_DivergentOutputsFailAllAttempts(t *testing.T) {
	env, closeFn := serve(t, func(n int32) (int, string) {
		if n == 2 {
			return 200, completion("二", "stop", "")
		}
		return 200, completion("2", "stop", "")
	})
	defer closeFn()

	got := env.engine.RunCase(context.Background(), determinismCase(3))
	if got.Status != model.StatusFail {
		t.Fatalf("输出不一致时期望 FAIL，实际 %s", got.Status)
	}
	// 一致性是这组采样的整体属性：不能只让"不一样的那一次"带失败原因，
	// 否则报告里点开第 1 次采样会看到一条通过的记录，与结论矛盾。
	for i, a := range got.CaseAttempts {
		if a.Passed || a.FailReason == "" {
			t.Fatalf("第 %d 次采样应带失败原因且不通过，实际 passed=%v reason=%q", i+1, a.Passed, a.FailReason)
		}
	}
}

func TestDeterministicRepeat_SingleAttemptIsRejected(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("2", "stop", "")
	})
	defer closeFn()

	got := env.engine.RunCase(context.Background(), determinismCase(1))
	if got.Status != model.StatusFail {
		t.Fatalf("repeat_attempts=1 时期望 FAIL（无可比对样本），实际 %s", got.Status)
	}
	if env.calls.Load() != 0 {
		t.Fatalf("套件定义不合法时不应发出请求，实际发出 %d 次", env.calls.Load())
	}
}

// ── long_context_recall ──────────────────────────────────────────────────

func longContextCase(params map[string]any) suitedef.Case {
	return caseWithParams("long_context_recall", map[string]any{
		"model": "{{model_key}}",
		"messages": []any{map[string]any{
			"role":    "user",
			"content": "{{filler:200}}\n【暗号】7391\n{{filler:200}}\n上文暗号是多少？只回答数字。",
		}},
	}, params, 1)
}

func TestLongContextRecall_Pass(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("7391", "stop",
			`"usage":{"prompt_tokens":900000,"completion_tokens":3,"total_tokens":900003}`)
	})
	defer closeFn()

	got := env.engine.RunCase(context.Background(), longContextCase(map[string]any{
		"needle": "7391", "min_prompt_tokens": 800000,
	}))
	if got.Status != model.StatusPass {
		t.Fatalf("期望 PASS，实际 %s: %s", got.Status, got.FailReason)
	}
	if got.CaseAttempts[0].Metrics["prompt_tokens"] != 900000 {
		t.Errorf("应把实测 prompt_tokens 写进 metrics 留痕，实际 %+v", got.CaseAttempts[0].Metrics)
	}
}

// 答案对但 prompt_tokens 远低于目标：说明输入被网关或模型截断了，模型可能只是
// 恰好读到了保留下来的那一段。这种情况不能算长上下文通过。
func TestLongContextRecall_TruncatedInputIsFailEvenWithRightAnswer(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("7391", "stop",
			`"usage":{"prompt_tokens":4096,"completion_tokens":3,"total_tokens":4099}`)
	})
	defer closeFn()

	got := env.engine.RunCase(context.Background(), longContextCase(map[string]any{
		"needle": "7391", "min_prompt_tokens": 800000,
	}))
	if got.Status != model.StatusFail {
		t.Fatalf("prompt_tokens 不足时期望 FAIL，实际 %s", got.Status)
	}
	if !strings.Contains(got.FailReason, "截断") {
		t.Errorf("失败原因应指出输入被截断，实际: %s", got.FailReason)
	}
}

func TestLongContextRecall_WrongNeedleIsFail(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("1234", "stop",
			`"usage":{"prompt_tokens":900000,"completion_tokens":3,"total_tokens":900003}`)
	})
	defer closeFn()

	if got := env.engine.RunCase(context.Background(), longContextCase(map[string]any{
		"needle": "7391", "min_prompt_tokens": 800000,
	})); got.Status != model.StatusFail {
		t.Fatalf("答案不符时期望 FAIL，实际 %s", got.Status)
	}
}

func TestLongContextRecall_MissingParamsIsFail(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("7391", "stop",
			`"usage":{"prompt_tokens":900000,"completion_tokens":3,"total_tokens":900003}`)
	})
	defer closeFn()

	if got := env.engine.RunCase(context.Background(), longContextCase(map[string]any{"needle": "7391"})); got.Status != model.StatusFail {
		t.Fatalf("缺 min_prompt_tokens 时期望 FAIL，实际 %s", got.Status)
	}
}

// filler 占位符必须渲染成真实文本并进入请求体，否则长上下文用例发出去的还是
// 一个短请求，测了个寂寞。
func TestFillerPlaceholderRendersIntoRequestBody(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("7391", "stop",
			`"usage":{"prompt_tokens":900000,"completion_tokens":3,"total_tokens":900003}`)
	})
	defer closeFn()

	got := env.engine.RunCase(context.Background(), longContextCase(map[string]any{
		"needle": "7391", "min_prompt_tokens": 800000,
	}))
	body := got.CaseAttempts[0].RequestBody
	if strings.Contains(body, "{{filler:") {
		t.Fatalf("请求体里仍有未渲染的 filler 占位符: %s", body)
	}
	if !strings.Contains(body, "填充语料") {
		t.Fatalf("请求体里未出现填充语料: %s", body)
	}
}

// trace_body_max_chars 只在用例显式声明时生效，声明后请求体留痕必须被截短。
func TestTraceBodyTruncation(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("7391", "stop",
			`"usage":{"prompt_tokens":900000,"completion_tokens":3,"total_tokens":900003}`)
	})
	defer closeFn()

	c := longContextCase(map[string]any{
		"needle": "7391", "min_prompt_tokens": 800000, "trace_body_max_chars": 120,
	})
	got := env.engine.RunCase(context.Background(), c)
	body := []rune(got.CaseAttempts[0].RequestBody)
	if len(body) < 120 {
		t.Fatalf("截断后应保留前 120 字符，实际长度 %d", len(body))
	}
	if !strings.Contains(string(body), "已按 trace_body_max_chars=120 截断") {
		t.Fatalf("截断后应留下说明，实际: %s", string(body))
	}
}

// ── prompt_cache_hit_rate ────────────────────────────────────────────────

func cacheCase(params map[string]any) suitedef.Case {
	return caseWithParams("prompt_cache_hit_rate", map[string]any{
		"model":    "{{model_key}}",
		"messages": []any{map[string]any{"role": "user", "content": "{{filler:2000}}\n请回答：收到。"}},
	}, params, 1)
}

func TestPromptCacheHitRate_SecondRequestHits(t *testing.T) {
	env, closeFn := serve(t, func(n int32) (int, string) {
		if n == 1 {
			return 200, completion("收到", "stop",
				`"usage":{"prompt_tokens":2000,"completion_tokens":2,"total_tokens":2002,"prompt_tokens_details":{"cached_tokens":0}}`)
		}
		return 200, completion("收到", "stop",
			`"usage":{"prompt_tokens":2000,"completion_tokens":2,"total_tokens":2002,"prompt_tokens_details":{"cached_tokens":2000}}`)
	})
	defer closeFn()

	got := env.engine.RunCase(context.Background(), cacheCase(map[string]any{"min_hit_rate": 0.8}))
	if got.Status != model.StatusPass {
		t.Fatalf("期望 PASS，实际 %s: %s", got.Status, got.FailReason)
	}
	if len(got.CaseAttempts) != 2 {
		t.Fatalf("期望 1 次预热 + 1 次判定共 2 条留痕，实际 %d", len(got.CaseAttempts))
	}
	if got.CaseAttempts[0].VariantLabel != "warmup" || got.CaseAttempts[1].VariantLabel != "measured" {
		t.Fatalf("留痕应区分预热与判定请求，实际 %q/%q", got.CaseAttempts[0].VariantLabel, got.CaseAttempts[1].VariantLabel)
	}
	if rate := got.CaseAttempts[1].Metrics["prompt_cache_hit_rate"]; rate != 1 {
		t.Errorf("命中率应为 1.0（2000/2000），实际 %v", rate)
	}
}

// 首次请求必然 0 命中：如果引擎拿预热那次去判定，这条用例会恒失败。
func TestPromptCacheHitRate_WarmupZeroHitDoesNotFailCase(t *testing.T) {
	env, closeFn := serve(t, func(n int32) (int, string) {
		cached := 0
		if n > 1 {
			cached = 1800
		}
		return 200, completion("收到", "stop", fmt.Sprintf(
			`"usage":{"prompt_tokens":2000,"completion_tokens":2,"total_tokens":2002,"prompt_tokens_details":{"cached_tokens":%d}}`, cached))
	})
	defer closeFn()

	if got := env.engine.RunCase(context.Background(), cacheCase(map[string]any{"min_hit_rate": 0.8})); got.Status != model.StatusPass {
		t.Fatalf("期望 PASS，实际 %s: %s", got.Status, got.FailReason)
	}
}

func TestPromptCacheHitRate_BelowThresholdIsFail(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("收到", "stop",
			`"usage":{"prompt_tokens":2000,"completion_tokens":2,"total_tokens":2002,"prompt_tokens_details":{"cached_tokens":200}}`)
	})
	defer closeFn()

	got := env.engine.RunCase(context.Background(), cacheCase(map[string]any{"min_hit_rate": 0.8}))
	if got.Status != model.StatusFail {
		t.Fatalf("命中率低于门槛时期望 FAIL，实际 %s", got.Status)
	}
	if !strings.Contains(got.FailReason, "10.00%") {
		t.Errorf("失败原因应给出实测命中率，实际: %s", got.FailReason)
	}
}

// 网关根本不回传命中数 ≠ 命中 0 个：前者是能力缺失，必须显式失败并说清楚。
func TestPromptCacheHitRate_MissingFieldIsFail(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("收到", "stop",
			`"usage":{"prompt_tokens":2000,"completion_tokens":2,"total_tokens":2002}`)
	})
	defer closeFn()

	got := env.engine.RunCase(context.Background(), cacheCase(map[string]any{"min_hit_rate": 0.8}))
	if got.Status != model.StatusFail {
		t.Fatalf("usage 无 cached_tokens 时期望 FAIL，实际 %s", got.Status)
	}
	if !strings.Contains(got.FailReason, "cached_tokens") {
		t.Errorf("失败原因应点明缺失字段，实际: %s", got.FailReason)
	}
}

// 部分网关把命中数平铺在 usage 顶层，也要能读到。
func TestPromptCacheHitRate_FlatCachedTokensField(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, completion("收到", "stop",
			`"usage":{"prompt_tokens":2000,"completion_tokens":2,"total_tokens":2002,"cached_tokens":2000}`)
	})
	defer closeFn()

	if got := env.engine.RunCase(context.Background(), cacheCase(map[string]any{"min_hit_rate": 0.8})); got.Status != model.StatusPass {
		t.Fatalf("期望 PASS（usage.cached_tokens 平铺形态），实际 %s: %s", got.Status, got.FailReason)
	}
}

// ── repeat_attempts 通用化 ───────────────────────────────────────────────

// 截图备注"响应体 usage 对象字段不稳定"：单次请求碰巧正常不能算通过，
// repeat_attempts 必须对普通单请求断言也生效，且任一次不合格即整体 FAIL。
func TestRepeatAttempts_AppliesToSingleRequestAssertions(t *testing.T) {
	env, closeFn := serve(t, func(n int32) (int, string) {
		if n == 3 {
			return 200, completion("春天来了", "stop",
				`"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":99}`)
		}
		return 200, completion("春天来了", "stop", "")
	})
	defer closeFn()

	c := caseWithParams("usage_fields_nonstream", map[string]any{
		"model":    "{{model_key}}",
		"messages": []any{map[string]any{"role": "user", "content": "写一句春天"}},
	}, nil, 5)

	got := env.engine.RunCase(context.Background(), c)
	if got.Status != model.StatusFail {
		t.Fatalf("第 3 次 usage 不自洽时期望整体 FAIL，实际 %s", got.Status)
	}
	if len(got.CaseAttempts) != 5 {
		t.Fatalf("期望 5 条采样留痕，实际 %d", len(got.CaseAttempts))
	}
	if got.PassedAttempts != 4 {
		t.Errorf("期望 4 次通过 / 5 次采样，实际 %d", got.PassedAttempts)
	}
}

// ── reasoning_effort 比较对参数化 ────────────────────────────────────────

func effortCase(pairs any, variants []string) suitedef.Case {
	c := caseWithParams("reasoning_effort_scaling", map[string]any{
		"model":    "{{model_key}}",
		"messages": []any{map[string]any{"role": "user", "content": "算一道题"}},
	}, map[string]any{"required_distinct_pairs": pairs}, 1)
	c.CapabilityTag = "reasoning_effort"
	c.CountsInBase22 = false
	for _, v := range variants {
		c.Variants = append(c.Variants, suitedef.Variant{
			VariantLabel:     v,
			RequestOverrides: map[string]any{"reasoning_effort": v},
		})
	}
	return c
}

func effortResponse(reasoningTokens int) string {
	return completion("答案", "stop", fmt.Sprintf(
		`"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"completion_tokens_details":{"reasoning_tokens":%d}}`,
		reasoningTokens))
}

// z.ai 把 low 与 high 映射到同一档思考强度：默认的 low/high 比较会恒失败，
// 套件必须能显式声明真正可区分的档位对（low vs max）。
func TestReasoningEffort_CustomDistinctPairs(t *testing.T) {
	tokensByCall := map[int32]int{1: 100, 2: 100, 3: 900}
	env, closeFn := serve(t, func(n int32) (int, string) {
		return 200, effortResponse(tokensByCall[n])
	})
	defer closeFn()
	c := effortCase([]any{[]any{"low", "max"}}, []string{"low", "high", "max"})
	if got := env.engine.RunCase(context.Background(), c); got.Status != model.StatusPass {
		t.Fatalf("声明 low/max 可区分时期望 PASS，实际 %s: %s", got.Status, got.FailReason)
	}
}

func TestReasoningEffort_CustomPairWithoutDifferenceIsFail(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, effortResponse(100)
	})
	defer closeFn()

	c := effortCase([]any{[]any{"low", "max"}}, []string{"low", "high", "max"})
	got := env.engine.RunCase(context.Background(), c)
	if got.Status != model.StatusFail {
		t.Fatalf("low/max 无差异时期望 FAIL，实际 %s", got.Status)
	}
	if !strings.Contains(got.FailReason, "low") || !strings.Contains(got.FailReason, "max") {
		t.Errorf("失败原因应点名是哪两档无区分度，实际: %s", got.FailReason)
	}
}

// 不填参数时必须保持 kimi-k3 的原口径（low vs high）。
func TestReasoningEffort_DefaultsToLowVsHigh(t *testing.T) {
	env, closeFn := serve(t, func(int32) (int, string) {
		return 200, effortResponse(100)
	})
	defer closeFn()

	c := effortCase(nil, []string{"low", "high", "max"})
	delete(c.AssertionParams, "required_distinct_pairs")
	got := env.engine.RunCase(context.Background(), c)
	if got.Status != model.StatusFail {
		t.Fatalf("未声明比较对且 low/high 无差异时期望 FAIL，实际 %s", got.Status)
	}
	if !strings.Contains(got.FailReason, "low 档与 high 档") {
		t.Errorf("默认口径应比较 low 与 high，实际: %s", got.FailReason)
	}
}
