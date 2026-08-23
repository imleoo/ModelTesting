// Package assertion 实现设计方案 04 节各断言类型的单次判定逻辑。多请求用例
// （usage_fields_stream 的两段式重试、thinking_toggle_pair 的开/关两次、
// reasoning_effort_scaling 的三档×N次）的编排留给 internal/engine，本包只
// 负责单次请求/单个响应的 PASS/FAIL 判定。
package assertion

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/leoobai/modeltestbed/internal/openaiapi"
)

type Verdict struct {
	Passed bool
	Reason string
}

func pass() Verdict              { return Verdict{Passed: true} }
func fail(reason string) Verdict { return Verdict{Passed: false, Reason: reason} }

func StreamIntegrity(chunkCount int, sawDone bool) Verdict {
	if chunkCount < 2 {
		return fail(fmt.Sprintf("SSE 分片数为 %d，少于要求的 ≥2", chunkCount))
	}
	if !sawDone {
		return fail("未收到 [DONE] 结束标记，疑似中途断流或网关截断")
	}
	return pass()
}

func usageSelfConsistent(u *openaiapi.Usage) Verdict {
	if u == nil {
		return fail("缺少 usage 字段")
	}
	if u.PromptTokens < 0 || u.CompletionTokens < 0 || u.TotalTokens < 0 {
		return fail("usage 字段存在负数")
	}
	if u.PromptTokens+u.CompletionTokens != u.TotalTokens {
		return fail(fmt.Sprintf("usage 三字段不自洽：prompt_tokens(%d)+completion_tokens(%d)≠total_tokens(%d)",
			u.PromptTokens, u.CompletionTokens, u.TotalTokens))
	}
	return pass()
}

func UsageFieldsNonstream(resp openaiapi.Response) Verdict {
	return usageSelfConsistent(resp.Usage)
}

// UsageFieldsStreamAttempt 检查单次流式请求末包是否带合法 usage；
// 引擎负责按 04 节两段式重试逻辑编排两次尝试。
func UsageFieldsStreamAttempt(lastChunkUsage *openaiapi.Usage) Verdict {
	return usageSelfConsistent(lastChunkUsage)
}

func ContentNonempty(resp openaiapi.Response) Verdict {
	if len(resp.Choices) == 0 {
		return fail("choices 为空")
	}
	msg := resp.Choices[0].Message
	if msg == nil || msg.Content == nil || strings.TrimSpace(*msg.Content) == "" {
		return fail("响应内容为空")
	}
	return pass()
}

func validToolCallArgs(calls []openaiapi.ToolCall) Verdict {
	for _, tc := range calls {
		var js any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &js); err != nil {
			return fail(fmt.Sprintf("tool_call %s 的 arguments 不是合法 JSON: %v", tc.Function.Name, err))
		}
	}
	return pass()
}

// TextOrToolCall: 非空文本 或 合法 tool_calls 二选一；content 为 null 但存在
// 合法 tool_calls 视为通过。
func TextOrToolCall(resp openaiapi.Response) Verdict {
	if len(resp.Choices) == 0 {
		return fail("choices 为空")
	}
	msg := resp.Choices[0].Message
	if msg == nil {
		return fail("缺少 message")
	}
	if msg.Content != nil && strings.TrimSpace(*msg.Content) != "" {
		return pass()
	}
	if len(msg.ToolCalls) > 0 {
		return validToolCallArgs(msg.ToolCalls)
	}
	return fail("既无非空文本也无合法 tool_calls")
}

func ToolCallRequired(resp openaiapi.Response) Verdict {
	if len(resp.Choices) == 0 {
		return fail("choices 为空")
	}
	msg := resp.Choices[0].Message
	if msg == nil || len(msg.ToolCalls) == 0 {
		return fail("未发起任何工具调用")
	}
	return validToolCallArgs(msg.ToolCalls)
}

// ToolCallForbidden: 响应不含 tool_calls，仅文本（04节：既不发起工具调用，
// 也要求确实以文本形式给出了回答，不是既无调用也无内容的空响应）。
func ToolCallForbidden(resp openaiapi.Response) Verdict {
	if len(resp.Choices) == 0 {
		return fail("choices 为空")
	}
	msg := resp.Choices[0].Message
	if msg == nil {
		return fail("缺少 message")
	}
	if len(msg.ToolCalls) > 0 {
		return fail("tool_choice=none 时不应发起工具调用")
	}
	if msg.Content == nil || strings.TrimSpace(*msg.Content) == "" {
		return fail("tool_choice=none 时应仅以文本作答，但响应内容为空")
	}
	return pass()
}

// ToolCallNamed: 调用的函数名与指定一致，且该次调用的参数为合法 JSON 并满足
// 声明的 parameters schema（04 节原文三项要求都要满足，不能只查函数名和 JSON 语法）。
// paramsSchema 为 nil 时跳过 schema 校验（用例定义未提供 parameters 时的降级行为）。
func ToolCallNamed(resp openaiapi.Response, expectedFnName string, paramsSchema map[string]any) Verdict {
	if len(resp.Choices) == 0 {
		return fail("choices 为空")
	}
	msg := resp.Choices[0].Message
	if msg == nil || len(msg.ToolCalls) == 0 {
		return fail("未发起工具调用")
	}
	for _, tc := range msg.ToolCalls {
		if tc.Function.Name != expectedFnName {
			return fail(fmt.Sprintf("调用了非指定函数 %s，期望 %s", tc.Function.Name, expectedFnName))
		}
	}
	if v := validToolCallArgs(msg.ToolCalls); !v.Passed {
		return v
	}
	if paramsSchema == nil {
		return pass()
	}
	for _, tc := range msg.ToolCalls {
		var args any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return fail(fmt.Sprintf("tool_call %s 的 arguments 不是合法 JSON: %v", tc.Function.Name, err))
		}
		if violations := ValidateJSONSchema(paramsSchema, args); len(violations) > 0 {
			return fail(fmt.Sprintf("tool_call %s 的参数不满足声明的 parameters schema: %s", tc.Function.Name, strings.Join(violations, "; ")))
		}
	}
	return pass()
}

// ToolCallSubset: 调用次数≥1且全部函数名⊆allowed（04节从严解释：零次调用不视为通过）。
func ToolCallSubset(resp openaiapi.Response, allowed []string) Verdict {
	if len(resp.Choices) == 0 {
		return fail("choices 为空")
	}
	msg := resp.Choices[0].Message
	if msg == nil || len(msg.ToolCalls) == 0 {
		return fail("零次调用不视为通过（从严解释，见 04 节）")
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allowedSet[a] = true
	}
	for _, tc := range msg.ToolCalls {
		if !allowedSet[tc.Function.Name] {
			return fail(fmt.Sprintf("调用了不在 allowed_tools 子集内的函数 %s", tc.Function.Name))
		}
	}
	return validToolCallArgs(msg.ToolCalls)
}

func ThinkingPresent(reasoningContent *string) bool {
	return reasoningContent != nil && strings.TrimSpace(*reasoningContent) != ""
}

func ThinkingTogglePair(onPresent, offPresent bool) Verdict {
	if !onPresent {
		return fail("开启思考开关时未返回 reasoning_content（或等价字段）")
	}
	if offPresent {
		return fail("关闭思考开关时仍返回了 reasoning_content（或等价字段）")
	}
	return pass()
}

func DefaultThinkingMatchesDeclaration(present bool, declared string) Verdict {
	switch declared {
	case "thinks_by_default":
		if !present {
			return fail("声明默认思考，但不传开关时实际未返回思考内容")
		}
	case "no_thinking_by_default":
		if present {
			return fail("声明默认不思考，但不传开关时实际返回了思考内容")
		}
	default:
		return fail(fmt.Sprintf("CAPABILITY_PROFILE.default_thinking_behavior 取值非法: %q", declared))
	}
	return pass()
}

func JSONParseable(resp openaiapi.Response) Verdict {
	if len(resp.Choices) == 0 {
		return fail("choices 为空")
	}
	msg := resp.Choices[0].Message
	if msg == nil || msg.Content == nil {
		return fail("缺少 content")
	}
	var js any
	if err := json.Unmarshal([]byte(*msg.Content), &js); err != nil {
		return fail(fmt.Sprintf("content 不是合法 JSON: %v", err))
	}
	return pass()
}

func JSONSchemaValid(resp openaiapi.Response, schema map[string]any) Verdict {
	if len(resp.Choices) == 0 {
		return fail("choices 为空")
	}
	msg := resp.Choices[0].Message
	if msg == nil || msg.Content == nil {
		return fail("缺少 content")
	}
	var data any
	if err := json.Unmarshal([]byte(*msg.Content), &data); err != nil {
		return fail(fmt.Sprintf("content 不是合法 JSON: %v", err))
	}
	violations := ValidateJSONSchema(schema, data)
	if len(violations) > 0 {
		return fail(strings.Join(violations, "; "))
	}
	return pass()
}

var digitsRe = regexp.MustCompile(`\d+`)

// MultimodalVerdict 三态：PASS / FAIL / MANUAL_REVIEW，对应 manifest.json
// answer_match_definitions.digits_exact.decision_rule。
type MultimodalVerdict struct {
	Status string
	Reason string
}

func DeterministicMultimodalQA(responseText, expectedAnswer string) MultimodalVerdict {
	matches := digitsRe.FindAllString(strings.TrimSpace(responseText), -1)
	distinct := make(map[string]bool, len(matches))
	for _, m := range matches {
		distinct[m] = true
	}
	switch len(distinct) {
	case 0:
		return MultimodalVerdict{Status: "MANUAL_REVIEW", Reason: "响应未包含可比对的数字（开放式描述）"}
	case 1:
		var only string
		for k := range distinct {
			only = k
		}
		if only == expectedAnswer {
			return MultimodalVerdict{Status: "PASS"}
		}
		return MultimodalVerdict{Status: "FAIL", Reason: fmt.Sprintf("唯一可比对答案为 %q，与预期 %q 不符", only, expectedAnswer)}
	default:
		return MultimodalVerdict{Status: "MANUAL_REVIEW", Reason: "响应包含多个不同数字，无法确定唯一作答"}
	}
}

// ── 以下断言为 z-ai 套件引入（截图实测底稿的 14 项里，kimi-k3 引擎未覆盖的
// 那几项）。与本包既有函数一样，只做单次请求/单个响应的判定，跨请求的编排
// （重复采样、预热+复用两次调用）留在 internal/engine。

// finishReasonOf 取 choices[0].finish_reason；ok=false 表示字段缺失或为 null。
// 截断/停止这两类断言的核心证据就是这个字段，缺失时必须显式失败，不能当成
// 空字符串继续比对——那样会把"网关没回 finish_reason"误报成"值不对"。
func finishReasonOf(resp openaiapi.Response) (string, bool) {
	if len(resp.Choices) == 0 || resp.Choices[0].FinishReason == nil {
		return "", false
	}
	return *resp.Choices[0].FinishReason, true
}

func contentOf(resp openaiapi.Response) (string, bool) {
	if len(resp.Choices) == 0 || resp.Choices[0].Message == nil || resp.Choices[0].Message.Content == nil {
		return "", false
	}
	return *resp.Choices[0].Message.Content, true
}

// MaxTokensTruncation 校验 max_tokens 的强制截断语义：请求给了一个远小于模型
// 自然输出长度的 max_tokens，响应必须
//  1. finish_reason == "length"（而不是 "stop"——那意味着截断没生效）；
//  2. usage.completion_tokens 存在且 ≤ max_tokens（含等于，多一个 token 都算
//     越界）；
//  3. 输出内容非空（截断不等于什么都不生成）。
//
// 第 2 条要求 usage 必须存在：截图实测里 z.ai 的 usage 字段"不稳定"，而这条
// 用例恰恰只能靠 usage 证明截断发生在正确的位置，所以 usage 缺失直接判 FAIL，
// 不做宽松放行。
func MaxTokensTruncation(resp openaiapi.Response, maxTokens int) Verdict {
	reason, ok := finishReasonOf(resp)
	if !ok {
		return fail("响应缺少 choices[0].finish_reason，无法确认是否发生截断")
	}
	if reason != "length" {
		return fail(fmt.Sprintf("finish_reason 为 %q，期望 \"length\"（max_tokens=%d 未触发强制截断）", reason, maxTokens))
	}
	if resp.Usage == nil {
		return fail("响应缺少 usage，无法核对 completion_tokens 是否落在 max_tokens 之内")
	}
	if resp.Usage.CompletionTokens <= 0 {
		return fail(fmt.Sprintf("usage.completion_tokens=%d，截断不应导致零输出", resp.Usage.CompletionTokens))
	}
	if resp.Usage.CompletionTokens > maxTokens {
		return fail(fmt.Sprintf("usage.completion_tokens=%d 超过请求的 max_tokens=%d，截断未在正确位置生效",
			resp.Usage.CompletionTokens, maxTokens))
	}
	if text, ok := contentOf(resp); !ok || strings.TrimSpace(text) == "" {
		return fail("finish_reason=length 但 content 为空，截断把输出整个吃掉了")
	}
	return pass()
}

// StopSequenceRespected 校验 stop 语义：请求指定了 stop 序列，且提示词保证模型
// 一定会先输出 mustContain、再输出 stop 序列。响应必须
//  1. 含 mustContain —— 证明模型确实按提示词开始作答，不是因为别的原因提前结束；
//  2. 不含任何 stop 序列本身 —— stop 序列必须被吞掉，不能出现在输出里；
//  3. finish_reason == "stop"。
//
// 第 1 条是这条断言能成立的关键：只查"不含 stop 串"的话，一个什么都没生成的
// 空响应也能通过。
func StopSequenceRespected(resp openaiapi.Response, stops []string, mustContain string) Verdict {
	if len(stops) == 0 {
		return fail("用例定义未给出 stop 序列，无法校验 stop 语义")
	}
	text, ok := contentOf(resp)
	if !ok {
		return fail("响应缺少 content，无法校验 stop 语义")
	}
	if mustContain != "" && !strings.Contains(text, mustContain) {
		return fail(fmt.Sprintf("输出中未出现提示词要求的前置标记 %q，无法判断是 stop 生效还是模型没按要求作答；实际输出: %s",
			mustContain, truncateForReason(text)))
	}
	for _, s := range stops {
		if s != "" && strings.Contains(text, s) {
			return fail(fmt.Sprintf("输出中仍包含 stop 序列 %q，stop 未生效或未被截掉；实际输出: %s", s, truncateForReason(text)))
		}
	}
	reason, hasReason := finishReasonOf(resp)
	if !hasReason {
		return fail("响应缺少 choices[0].finish_reason，无法确认是否因 stop 结束")
	}
	if reason != "stop" {
		return fail(fmt.Sprintf("finish_reason 为 %q，期望 \"stop\"", reason))
	}
	return pass()
}

// DeterministicRepeat 校验同一请求在 temperature=0 下重复多次的输出完全一致。
// 判定按逐字节相等，不做 trim/归一化：一旦允许"忽略首尾空白也算相同"，就等于
// 默认了供应商可以在确定性档位下产出不同的空白，那这条用例就失去了意义。
func DeterministicRepeat(texts []string) Verdict {
	if len(texts) < 2 {
		return fail(fmt.Sprintf("确定性用例至少需要 2 次成功采样才能比对，实际只有 %d 次", len(texts)))
	}
	for i := 1; i < len(texts); i++ {
		if texts[i] != texts[0] {
			return fail(fmt.Sprintf("temperature=0 下第 1 次与第 %d 次输出不一致：\n  第 1 次: %s\n  第 %d 次: %s",
				i+1, truncateForReason(texts[0]), i+1, truncateForReason(texts[i])))
		}
	}
	return pass()
}

// LongContextRecall 校验长上下文：把一个暗号埋在超长输入的正中间，要求模型把它
// 找回来。两条独立证据缺一不可：
//  1. usage.prompt_tokens ≥ minPromptTokens —— 证明超长输入真的被完整送进了
//     模型，而不是被网关/模型静默截断了；只看答案对不对无法区分"读完全文找到了"
//     和"截断后恰好保留了暗号那一段"。
//  2. 答案里唯一的数字串等于 needle —— 沿用 deterministic_multimodal_qa 的
//     digits 提取口径（0 个或多个数字串都不算答对，见 DeterministicMultimodalQA）。
//
// 与多模态用例不同，这里不产出 MANUAL_REVIEW：暗号是纯数字、提示词明确要求
// "只回答数字"，模型给不出唯一数字就是没通过，没有需要人工看图判断的空间。
func LongContextRecall(resp openaiapi.Response, needle string, minPromptTokens int) Verdict {
	if resp.Usage == nil {
		return fail("响应缺少 usage，无法确认超长输入是否被完整处理")
	}
	if resp.Usage.PromptTokens < minPromptTokens {
		return fail(fmt.Sprintf("usage.prompt_tokens=%d 低于要求的 %d，输入很可能被网关或模型截断了，本次结果不能算长上下文通过",
			resp.Usage.PromptTokens, minPromptTokens))
	}
	text, ok := contentOf(resp)
	if !ok {
		return fail("响应缺少 content")
	}
	matches := digitsRe.FindAllString(strings.TrimSpace(text), -1)
	distinct := make(map[string]bool, len(matches))
	for _, m := range matches {
		distinct[m] = true
	}
	switch len(distinct) {
	case 0:
		return fail(fmt.Sprintf("响应未包含任何数字，未找回暗号 %q；实际输出: %s", needle, truncateForReason(text)))
	case 1:
		var only string
		for k := range distinct {
			only = k
		}
		if only == needle {
			return pass()
		}
		return fail(fmt.Sprintf("找回的数字为 %q，与埋入的暗号 %q 不符", only, needle))
	default:
		return fail(fmt.Sprintf("响应包含多个不同数字，无法确认唯一作答；实际输出: %s", truncateForReason(text)))
	}
}

// PromptCacheHitRate 校验上下文缓存命中率，返回判定结果与实测命中率
// （cached_tokens / prompt_tokens，无 usage 时为 0）。
//
// 调用约定：只对"预热之后的那一次请求"调用本函数。首次请求必然 0 命中，拿它
// 判定等于给一条恒假的断言。
func PromptCacheHitRate(resp openaiapi.Response, minRate float64) (Verdict, float64) {
	if resp.Usage == nil {
		return fail("响应缺少 usage，无法计算缓存命中率"), 0
	}
	cached, present := resp.Usage.CachedTokens()
	if !present {
		return fail("响应 usage 中既无 prompt_tokens_details.cached_tokens 也无 cached_tokens，网关未回传缓存命中数"), 0
	}
	if resp.Usage.PromptTokens <= 0 {
		return fail(fmt.Sprintf("usage.prompt_tokens=%d，无法作为命中率分母", resp.Usage.PromptTokens)), 0
	}
	if cached < 0 || cached > resp.Usage.PromptTokens {
		return fail(fmt.Sprintf("cached_tokens=%d 不在 [0, prompt_tokens=%d] 区间内，usage 自相矛盾",
			cached, resp.Usage.PromptTokens)), 0
	}
	rate := float64(cached) / float64(resp.Usage.PromptTokens)
	if rate < minRate {
		return fail(fmt.Sprintf("缓存命中率 %.2f%%（cached_tokens=%d / prompt_tokens=%d）低于门槛 %.2f%%",
			rate*100, cached, resp.Usage.PromptTokens, minRate*100)), rate
	}
	return pass(), rate
}

// truncateForReason 把失败原因里引用的模型输出截短，避免长上下文用例把几十万
// 字的正文塞进 fail_reason 里（那会让报告和 JSON 结果文件都没法看）。
func truncateForReason(s string) string {
	const max = 200
	r := []rune(s)
	if len(r) <= max {
		return strconv.Quote(s)
	}
	return strconv.Quote(string(r[:max])) + fmt.Sprintf("...（共 %d 字，已截断）", len(r))
}
