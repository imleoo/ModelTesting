// Package engine 编排单条用例的执行：按能力声明裁剪、渲染请求、调用被测网关、
// 跑全局 openai_schema_valid 基线 + 04 节对应断言类型，产出 CaseResult/CaseAttempt。
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/leoobai/modeltestbed/internal/anthropicapi"
	"github.com/leoobai/modeltestbed/internal/assertion"
	"github.com/leoobai/modeltestbed/internal/client"
	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/openaiapi"
	"github.com/leoobai/modeltestbed/internal/render"
	"github.com/leoobai/modeltestbed/internal/suitedef"
)

type Engine struct {
	Client     *client.Client
	Suite      *suitedef.Suite
	Materials  *suitedef.MaterialsManifest
	Capability model.CapabilityProfile
	RenderCtx  render.Context
	// Style 决定全局基线校验/响应解析/流式结束判定走哪种协议形状，取值见
	// suitedef.StyleOpenAIChatCompletions/StyleAnthropicMessages；空值按
	// StyleOpenAIChatCompletions 处理，保持 kimi-k3 现状行为不变。调用方
	// （如 cmd/testbed-cli）应把它设成与 Suite.Protocol.Style 一致的值。
	Style string
}

// 以下几个 helper 是本文件唯一按协议分流的地方：其余全部编排逻辑（含
// internal/assertion 的断言函数）保持协议无关，靠 anthropicapi.ParseResponse
// 把 Anthropic 响应归一化成 openaiapi.Response 复用。Style=="" 或
// StyleOpenAIChatCompletions 时，下面每个 helper 的行为必须与改动前逐字节
// 一致——这是保护 kimi-k3 既有回归测试的硬约束。

// schemaBaselineLabel 用于失败原因文案，避免 Anthropic 套件的报告里出现
// "openai_schema_valid 未通过" 这种协议名对不上的误导性文字。
func (e *Engine) schemaBaselineLabel() string {
	if e.Style == suitedef.StyleAnthropicMessages {
		return "anthropic_schema_valid"
	}
	return "openai_schema_valid"
}

func (e *Engine) parseResponse(raw []byte) (openaiapi.Response, error) {
	if e.Style == suitedef.StyleAnthropicMessages {
		return anthropicapi.ParseResponse(raw)
	}
	return openaiapi.ParseResponse(raw)
}

func (e *Engine) validateNonStreamSchema(raw []byte) (bool, []string) {
	if e.Style == suitedef.StyleAnthropicMessages {
		return anthropicapi.ValidateSchema(raw, false)
	}
	return openaiapi.ValidateSchema(raw, false)
}

// isStreamComplete 替代直接读取 cr.SSEResult.SawDone：OpenAI 协议的
// "[DONE]" 哨兵由 internal/sse.Parse 在扫描阶段就识别并写入 SawDone；
// Anthropic 协议没有这个哨兵，改为扫描分片里是否出现 message_stop 事件
// （见 internal/anthropicapi/stream.go），两种协议的判定入口在这里统一。
func (e *Engine) isStreamComplete(cr client.CallResult) bool {
	if e.Style == suitedef.StyleAnthropicMessages {
		return anthropicapi.IsStreamComplete(cr.SSEResult.Chunks)
	}
	return cr.SSEResult.SawDone
}

// finalStreamUsage 返回一次流式请求"最终应该拿到 usage 的那个分片"携带的
// usage，供 usage_fields_stream 断言使用。两种协议对"哪个分片是最终分片"
// 的定义不同（OpenAI 是 [DONE] 前最后一包，Anthropic 是最后一个
// message_delta 分片），分流逻辑收在这里，调用方不用关心协议差异。
func (e *Engine) finalStreamUsage(cr client.CallResult) (*openaiapi.Usage, bool) {
	if e.Style == suitedef.StyleAnthropicMessages {
		return anthropicapi.FinalUsage(cr.SSEResult.Chunks)
	}
	return lastChunkUsage(cr)
}

// capabilityGate 判断用例是否应该跳过（标记 NOT_DECLARED）。
// 返回 (跳过, 原因)；不跳过时应正常执行用例。
func (e *Engine) capabilityGate(c suitedef.Case) (bool, string) {
	switch c.RequiredRule {
	case suitedef.FixedRequired:
		return false, ""
	case suitedef.DeclaredRequired, suitedef.ExemptAllowed, suitedef.Additional:
		declared := e.isCapabilityDeclared(c.CapabilityTag)
		if !declared {
			return true, fmt.Sprintf("能力 %q 未在 CAPABILITY_PROFILE 中声明", c.CapabilityTag)
		}
		return false, ""
	default:
		// 非法的 required_rule（套件定义损坏/拼写错误）不能悄悄按“必须执行”处理，
		// 必须在 RunCase 里被当作用例定义错误直接判 FAIL，而不是走到这里被放行。
		panic(fmt.Sprintf("capabilityGate: 非法的 required_rule %q，调用方应在此之前拦截", c.RequiredRule))
	}
}

// validRequiredRule 校验 required_rule 是否是四个已知取值之一。
func validRequiredRule(r suitedef.RequiredRule) bool {
	switch r {
	case suitedef.FixedRequired, suitedef.DeclaredRequired, suitedef.ExemptAllowed, suitedef.Additional:
		return true
	default:
		return false
	}
}

func (e *Engine) isCapabilityDeclared(tag string) bool {
	const thinkingPrefix = "thinking_toggle:"
	if len(tag) > len(thinkingPrefix) && tag[:len(thinkingPrefix)] == thinkingPrefix {
		return e.Capability.HasThinkingMethod(tag[len(thinkingPrefix):])
	}
	switch tag {
	case "image_url":
		return e.Capability.ImageURL
	case "video_url":
		return e.Capability.VideoURL
	case "tool_choice_function":
		return e.Capability.ToolChoiceFunction
	case "reasoning_effort":
		return e.Capability.ReasoningEffort
	case "prompt_cache":
		return e.Capability.PromptCache
	case "long_context":
		// 长上下文能力用「声明了上下文窗口上限」表达，而不是再加一个布尔量：
		// 用例本身需要这个数值来算 min_prompt_tokens，一个只说"支持"却不说
		// "支持到多少"的布尔量对这条用例没有意义。
		return e.Capability.ContextWindowTokens > 0
	default:
		return false
	}
}

// RunCase 执行单条用例，返回 CaseResult（含全部 CaseAttempt 留痕）。
func (e *Engine) RunCase(ctx context.Context, c suitedef.Case) model.CaseResult {
	result := model.CaseResult{CaseID: c.ID, ID: c.ID, CountsInBase22: c.CountsInBase22}

	if !validRequiredRule(c.RequiredRule) {
		result.Status = model.StatusFail
		result.FailReason = fmt.Sprintf("套件定义非法：required_rule %q 不是已知取值", c.RequiredRule)
		return result
	}

	if skip, reason := e.capabilityGate(c); skip {
		result.Status = model.StatusNotDeclared
		result.FailReason = reason
		return result
	}

	switch c.AssertionType {
	case "stream_integrity":
		e.runSingleStream(ctx, c, &result, e.scoreStreamIntegrity)
	case "usage_fields_nonstream":
		e.runSingleNonStream(ctx, c, &result, e.scoreUsageNonstream)
	case "usage_fields_stream":
		e.runUsageFieldsStream(ctx, c, &result)
	case "content_nonempty":
		e.runSingleNonStream(ctx, c, &result, e.scoreContentNonempty)
	case "text_or_tool_call":
		e.runSingleNonStream(ctx, c, &result, e.scoreTextOrToolCall)
	case "tool_call_required":
		e.runSingleNonStream(ctx, c, &result, e.scoreToolCallRequired)
	case "tool_call_forbidden":
		e.runSingleNonStream(ctx, c, &result, e.scoreToolCallForbidden)
	case "tool_call_named":
		e.runSingleNonStream(ctx, c, &result, e.scoreToolCallNamed)
	case "tool_call_subset":
		e.runSingleNonStream(ctx, c, &result, e.scoreToolCallSubset)
	case "thinking_toggle_pair":
		e.runThinkingTogglePair(ctx, c, &result)
	case "default_thinking_matches_declaration":
		e.runSingleNonStream(ctx, c, &result, e.scoreDefaultThinking)
	case "json_schema_valid":
		e.runSingleNonStream(ctx, c, &result, e.scoreJSONSchemaValid)
	case "json_parseable":
		e.runSingleNonStream(ctx, c, &result, e.scoreJSONParseable)
	case "deterministic_multimodal_qa":
		e.runDeterministicMultimodalQA(ctx, c, &result)
	case "reasoning_effort_scaling":
		e.runReasoningEffortScaling(ctx, c, &result)
	case "rejects_invalid_request":
		e.runRejectsInvalidRequest(ctx, c, &result)
	case "max_tokens_truncation":
		e.runSingleNonStream(ctx, c, &result, e.scoreMaxTokensTruncation)
	case "stop_sequence_respected":
		e.runSingleNonStream(ctx, c, &result, e.scoreStopSequenceRespected)
	case "long_context_recall":
		e.runSingleNonStream(ctx, c, &result, e.scoreLongContextRecall)
	case "deterministic_repeat":
		e.runDeterministicRepeat(ctx, c, &result)
	case "prompt_cache_hit_rate":
		e.runPromptCacheHitRate(ctx, c, &result)
	default:
		result.Status = model.StatusFail
		result.FailReason = fmt.Sprintf("未知断言类型 %q", c.AssertionType)
	}

	finalizeAggregate(&result)
	return result
}

func finalizeAggregate(r *model.CaseResult) {
	if r.Status != "" {
		return // 已由三态断言（deterministic_multimodal_qa）显式设置
	}
	r.Attempts = len(r.CaseAttempts)
	passed := 0
	for _, a := range r.CaseAttempts {
		if a.Passed {
			passed++
		}
	}
	r.PassedAttempts = passed
	if r.Attempts > 0 {
		r.PassRate = float64(passed) / float64(r.Attempts)
	}
	if passed == r.Attempts && r.Attempts > 0 {
		r.Status = model.StatusPass
	} else {
		r.Status = model.StatusFail
		if r.FailReason == "" {
			for _, a := range r.CaseAttempts {
				if !a.Passed {
					r.FailReason = a.FailReason
					break
				}
			}
		}
	}
}

// doCall 渲染请求并发起一次调用，返回原始留痕字段（不含业务断言结果）。
func (e *Engine) doCall(ctx context.Context, c suitedef.Case, overrides map[string]any) (client.CallResult, error) {
	body, err := render.Body(c.RequestTemplate.Body, overrides, e.RenderCtx)
	if err != nil {
		return client.CallResult{}, fmt.Errorf("渲染请求体失败: %w", err)
	}
	timeout := time.Duration(e.Suite.TimeoutFor(c.Category)) * time.Second
	res := e.Client.Call(ctx, e.Suite.Protocol.BasePath, body, timeout)
	return res, nil
}

func newAttempt(idx int, variantLabel string, cr client.CallResult) model.CaseAttempt {
	return model.CaseAttempt{
		AttemptIndex: idx,
		VariantLabel: variantLabel,
		HTTPStatus:   cr.HTTPStatus,
		LatencyMS:    cr.LatencyMS,
		RequestBody:  cr.RequestBody,
		ResponseBody: cr.ResponseBody,
	}
}

// newAttemptTraced 在 newAttempt 之上按用例声明的 trace_body_max_chars 截断
// 请求体/响应体留痕。默认（未声明或 <=0）不截断，保持设计方案 04 节"每用例
// 强制留痕完整请求体&响应体"的现状。只有长上下文这类单个请求体就上百万字符的
// 用例才该显式打开——否则一条用例就能把结果 JSON 撑到几百 MB，报告根本打不开。
func (e *Engine) newAttemptTraced(c suitedef.Case, idx int, variantLabel string, cr client.CallResult) model.CaseAttempt {
	a := newAttempt(idx, variantLabel, cr)
	if maxChars := c.ParamInt("trace_body_max_chars", 0); maxChars > 0 {
		a.RequestBody = truncateTrace(a.RequestBody, maxChars)
		a.ResponseBody = truncateTrace(a.ResponseBody, maxChars)
	}
	return a
}

func truncateTrace(s string, maxChars int) string {
	r := []rune(s)
	if len(r) <= maxChars {
		return s
	}
	return string(r[:maxChars]) + fmt.Sprintf("\n...[留痕已按 trace_body_max_chars=%d 截断，原文共 %d 字符]", maxChars, len(r))
}

// scoreFunc 对一次已完成的调用做业务断言，填充 attempt.Passed/FailReason。
type scoreFunc func(c suitedef.Case, cr client.CallResult, attempt *model.CaseAttempt)

// repeatCount 返回单变体应重复请求的次数。RepeatAttempts 未填（0）或非法（负数）
// 时按 1 次处理——kimi-k3 套件里除 reasoning_effort 外全部是 1，行为不变。
// z-ai 套件用 >1 的取值来复现截图里"usage 对象字段不稳定"这类偶发问题：单次
// 请求碰巧正常并不能证明字段稳定。
func repeatCount(c suitedef.Case) int {
	if c.RepeatAttempts < 1 {
		return 1
	}
	return c.RepeatAttempts
}

func (e *Engine) runSingleNonStream(ctx context.Context, c suitedef.Case, result *model.CaseResult, score scoreFunc) {
	for i := 0; i < repeatCount(c); i++ {
		cr, err := e.doCall(ctx, c, nil)
		attempt := e.newAttemptTraced(c, i+1, "", cr)
		if err != nil {
			attempt.Passed = false
			attempt.FailReason = err.Error()
		} else if cr.TransportErr != nil {
			attempt.Passed = false
			attempt.FailReason = cr.TransportErr.Error()
		} else if cr.HTTPStatus != 200 {
			attempt.Passed = false
			attempt.FailReason = fmt.Sprintf("HTTP 状态码 %d，期望 200", cr.HTTPStatus)
		} else if ok, violations := e.validateNonStreamSchema([]byte(cr.ResponseBody)); !ok {
			attempt.Passed = false
			attempt.FailReason = e.schemaBaselineLabel() + " 未通过: " + joinViolations(violations)
		} else {
			score(c, cr, &attempt)
		}
		result.CaseAttempts = append(result.CaseAttempts, attempt)
	}
}

func (e *Engine) runSingleStream(ctx context.Context, c suitedef.Case, result *model.CaseResult, score scoreFunc) {
	for i := 0; i < repeatCount(c); i++ {
		cr, err := e.doCall(ctx, c, nil)
		attempt := e.newAttemptTraced(c, i+1, "", cr)
		if err != nil {
			attempt.Passed = false
			attempt.FailReason = err.Error()
		} else if cr.TransportErr != nil {
			attempt.Passed = false
			attempt.FailReason = cr.TransportErr.Error()
		} else if cr.HTTPStatus != 200 {
			attempt.Passed = false
			attempt.FailReason = fmt.Sprintf("HTTP 状态码 %d，期望 200", cr.HTTPStatus)
		} else if ok, reason := validateStreamSchema(cr); !ok {
			attempt.Passed = false
			attempt.FailReason = e.schemaBaselineLabel() + " 未通过: " + reason
		} else {
			score(c, cr, &attempt)
		}
		result.CaseAttempts = append(result.CaseAttempts, attempt)
	}
}

// runRejectsInvalidRequest 承载"网关应对非法输入做校验，返回 4xx 而不是
// 把非法输入透传给后端触发 500"这类用例（非 PDF 原文，07 节 SOP 允许的
// 供应商专属补充用例，见 suites/kimi-k3/suite.v1.json 里对应用例的 notes）。
// 这类用例的成功响应本身就不是一个合法的 chat completion 对象（网关应该
// 直接拒绝、根本不会产出可 openai_schema_valid 校验的内容），所以不能像
// runSingleNonStream 那样先跑全局 schema 基线——这里的判定标准是 HTTP
// 状态码本身，不是响应体结构。
func (e *Engine) runRejectsInvalidRequest(ctx context.Context, c suitedef.Case, result *model.CaseResult) {
	cr, err := e.doCall(ctx, c, nil)
	attempt := newAttempt(1, "", cr)
	switch {
	case err != nil:
		attempt.Passed, attempt.FailReason = false, err.Error()
	case cr.TransportErr != nil:
		attempt.Passed, attempt.FailReason = false, cr.TransportErr.Error()
	case cr.HTTPStatus >= 500:
		attempt.Passed = false
		attempt.FailReason = fmt.Sprintf(
			"HTTP 状态码 %d：网关把非法输入透传给了后端并触发服务端错误，应在网关层完成输入校验并返回 4xx", cr.HTTPStatus)
	case cr.HTTPStatus >= 400 && cr.HTTPStatus < 500:
		attempt.Passed = true
	default:
		attempt.Passed = false
		attempt.FailReason = fmt.Sprintf(
			"HTTP 状态码 %d：网关未对非法输入做校验，直接当作合法请求处理了", cr.HTTPStatus)
	}
	result.CaseAttempts = append(result.CaseAttempts, attempt)
}

func validateStreamSchema(cr client.CallResult) (bool, string) {
	for i, chunk := range cr.SSEResult.Chunks {
		ok, violations := openaiapi.ValidateSchema([]byte(chunk), true)
		if !ok {
			return false, fmt.Sprintf("chunk[%d]: %s", i, joinViolations(violations))
		}
	}
	return true, ""
}

func joinViolations(v []string) string {
	return strings.Join(v, "; ")
}

func (e *Engine) scoreStreamIntegrity(_ suitedef.Case, cr client.CallResult, attempt *model.CaseAttempt) {
	v := assertion.StreamIntegrity(len(cr.SSEResult.Chunks), cr.SSEResult.SawDone)
	attempt.Passed, attempt.FailReason = v.Passed, v.Reason
}

func (e *Engine) scoreUsageNonstream(_ suitedef.Case, cr client.CallResult, attempt *model.CaseAttempt) {
	resp, err := openaiapi.ParseResponse([]byte(cr.ResponseBody))
	if err != nil {
		attempt.Passed, attempt.FailReason = false, "响应体解析失败: "+err.Error()
		return
	}
	v := assertion.UsageFieldsNonstream(resp)
	attempt.Passed, attempt.FailReason = v.Passed, v.Reason
}

func (e *Engine) scoreContentNonempty(_ suitedef.Case, cr client.CallResult, attempt *model.CaseAttempt) {
	resp, err := openaiapi.ParseResponse([]byte(cr.ResponseBody))
	if err != nil {
		attempt.Passed, attempt.FailReason = false, "响应体解析失败: "+err.Error()
		return
	}
	v := assertion.ContentNonempty(resp)
	attempt.Passed, attempt.FailReason = v.Passed, v.Reason
}

func (e *Engine) scoreTextOrToolCall(_ suitedef.Case, cr client.CallResult, attempt *model.CaseAttempt) {
	resp, err := openaiapi.ParseResponse([]byte(cr.ResponseBody))
	if err != nil {
		attempt.Passed, attempt.FailReason = false, "响应体解析失败: "+err.Error()
		return
	}
	v := assertion.TextOrToolCall(resp)
	attempt.Passed, attempt.FailReason = v.Passed, v.Reason
}

func (e *Engine) scoreToolCallRequired(_ suitedef.Case, cr client.CallResult, attempt *model.CaseAttempt) {
	resp, err := openaiapi.ParseResponse([]byte(cr.ResponseBody))
	if err != nil {
		attempt.Passed, attempt.FailReason = false, "响应体解析失败: "+err.Error()
		return
	}
	v := assertion.ToolCallRequired(resp)
	attempt.Passed, attempt.FailReason = v.Passed, v.Reason
}

func (e *Engine) scoreToolCallForbidden(_ suitedef.Case, cr client.CallResult, attempt *model.CaseAttempt) {
	resp, err := openaiapi.ParseResponse([]byte(cr.ResponseBody))
	if err != nil {
		attempt.Passed, attempt.FailReason = false, "响应体解析失败: "+err.Error()
		return
	}
	v := assertion.ToolCallForbidden(resp)
	attempt.Passed, attempt.FailReason = v.Passed, v.Reason
}

func (e *Engine) scoreToolCallNamed(c suitedef.Case, cr client.CallResult, attempt *model.CaseAttempt) {
	resp, err := openaiapi.ParseResponse([]byte(cr.ResponseBody))
	if err != nil {
		attempt.Passed, attempt.FailReason = false, "响应体解析失败: "+err.Error()
		return
	}
	name, ok := extractToolChoiceFunctionName(c)
	if !ok {
		attempt.Passed, attempt.FailReason = false, "用例定义缺少 tool_choice.function.name，无法比对"
		return
	}
	schema, toolFound := extractToolParametersSchema(e.Suite.Fixtures, c, name)
	if !toolFound {
		attempt.Passed, attempt.FailReason = false,
			fmt.Sprintf("套件定义不一致：tool_choice 指定的函数 %q 未在 tools/fixtures 中声明，无法校验参数", name)
		return
	}
	v := assertion.ToolCallNamed(resp, name, schema)
	attempt.Passed, attempt.FailReason = v.Passed, v.Reason
}

func (e *Engine) scoreToolCallSubset(c suitedef.Case, cr client.CallResult, attempt *model.CaseAttempt) {
	resp, err := openaiapi.ParseResponse([]byte(cr.ResponseBody))
	if err != nil {
		attempt.Passed, attempt.FailReason = false, "响应体解析失败: "+err.Error()
		return
	}
	allowed := extractAllowedToolNames(c)
	v := assertion.ToolCallSubset(resp, allowed)
	attempt.Passed, attempt.FailReason = v.Passed, v.Reason
}

func (e *Engine) scoreDefaultThinking(_ suitedef.Case, cr client.CallResult, attempt *model.CaseAttempt) {
	resp, err := openaiapi.ParseResponse([]byte(cr.ResponseBody))
	if err != nil {
		attempt.Passed, attempt.FailReason = false, "响应体解析失败: "+err.Error()
		return
	}
	present := len(resp.Choices) > 0 && resp.Choices[0].Message != nil && assertion.ThinkingPresent(resp.Choices[0].Message.ReasoningContent)
	v := assertion.DefaultThinkingMatchesDeclaration(present, e.Capability.DefaultThinkingBehavior)
	attempt.Passed, attempt.FailReason = v.Passed, v.Reason
}

func (e *Engine) scoreJSONParseable(_ suitedef.Case, cr client.CallResult, attempt *model.CaseAttempt) {
	resp, err := openaiapi.ParseResponse([]byte(cr.ResponseBody))
	if err != nil {
		attempt.Passed, attempt.FailReason = false, "响应体解析失败: "+err.Error()
		return
	}
	v := assertion.JSONParseable(resp)
	attempt.Passed, attempt.FailReason = v.Passed, v.Reason
}

func (e *Engine) scoreJSONSchemaValid(c suitedef.Case, cr client.CallResult, attempt *model.CaseAttempt) {
	resp, err := openaiapi.ParseResponse([]byte(cr.ResponseBody))
	if err != nil {
		attempt.Passed, attempt.FailReason = false, "响应体解析失败: "+err.Error()
		return
	}
	schema, ok := extractJSONSchema(c)
	if !ok {
		attempt.Passed, attempt.FailReason = false, "用例定义缺少 response_format.json_schema.schema，无法校验"
		return
	}
	v := assertion.JSONSchemaValid(resp, schema)
	attempt.Passed, attempt.FailReason = v.Passed, v.Reason
}

func (e *Engine) runUsageFieldsStream(ctx context.Context, c suitedef.Case, result *model.CaseResult) {
	for i, variant := range c.Variants {
		cr, err := e.doCall(ctx, c, variant.RequestOverrides)
		attempt := newAttempt(i+1, variant.VariantLabel, cr)
		switch {
		case err != nil:
			attempt.Passed, attempt.FailReason = false, err.Error()
		case cr.TransportErr != nil:
			attempt.Passed, attempt.FailReason = false, cr.TransportErr.Error()
		case cr.HTTPStatus != 200:
			attempt.Passed, attempt.FailReason = false, fmt.Sprintf("HTTP 状态码 %d，期望 200", cr.HTTPStatus)
		default:
			if ok, reason := validateStreamSchema(cr); !ok {
				attempt.Passed, attempt.FailReason = false, "openai_schema_valid 未通过: "+reason
			} else if lastUsage, ok := lastChunkUsage(cr); !ok {
				attempt.Passed, attempt.FailReason = false, "未收到 [DONE] 或末包缺失/无法解析，无法确认末包是否携带 usage"
			} else {
				v := assertion.UsageFieldsStreamAttempt(lastUsage)
				attempt.Passed, attempt.FailReason = v.Passed, v.Reason
			}
		}
		result.CaseAttempts = append(result.CaseAttempts, attempt)
		if attempt.Passed {
			result.Status = model.StatusPass
			result.Attempts = len(result.CaseAttempts)
			result.PassedAttempts = 1
			result.PassRate = 1
			return
		}
	}
	// 两次尝试均失败才 FAIL（04 节两段式逻辑）。
}

// lastChunkUsage 返回 [DONE] 前最后一包携带的 usage（04 节要求的检查对象）。
// ok=false 表示流未见 [DONE]、没有分片、或末包解析失败——这些情况下不存在
// 一个可信的“最终分片”，调用方应视为该次尝试失败，而不是继续往前找任意一个
// 带 usage 的历史分片（那样会把中间分片误判为“末包”）。
func lastChunkUsage(cr client.CallResult) (*openaiapi.Usage, bool) {
	if !cr.SSEResult.SawDone || len(cr.SSEResult.Chunks) == 0 {
		return nil, false
	}
	last := cr.SSEResult.Chunks[len(cr.SSEResult.Chunks)-1]
	chunk, err := openaiapi.ParseChunk(last)
	if err != nil {
		return nil, false
	}
	return chunk.Usage, true
}

func (e *Engine) runThinkingTogglePair(ctx context.Context, c suitedef.Case, result *model.CaseResult) {
	type outcome struct {
		idx     int
		present bool
		ok      bool // 本次请求本身是否顺利完成到可判定 present 的地步（未被传输层/HTTP/schema 拦下）
	}
	outcomes := make(map[string]outcome)
	for i, variant := range c.Variants {
		cr, err := e.doCall(ctx, c, variant.RequestOverrides)
		attempt := newAttempt(i+1, variant.VariantLabel, cr)
		var present, ok bool
		switch {
		case err != nil:
			attempt.FailReason = err.Error()
		case cr.TransportErr != nil:
			attempt.FailReason = cr.TransportErr.Error()
		case cr.HTTPStatus != 200:
			attempt.FailReason = fmt.Sprintf("HTTP 状态码 %d，期望 200", cr.HTTPStatus)
		default:
			if svOK, violations := openaiapi.ValidateSchema([]byte(cr.ResponseBody), false); !svOK {
				attempt.FailReason = "openai_schema_valid 未通过: " + joinViolations(violations)
			} else {
				resp, perr := openaiapi.ParseResponse([]byte(cr.ResponseBody))
				if perr != nil {
					attempt.FailReason = "响应体解析失败: " + perr.Error()
				} else {
					present = len(resp.Choices) > 0 && resp.Choices[0].Message != nil && assertion.ThinkingPresent(resp.Choices[0].Message.ReasoningContent)
					ok = true
				}
			}
		}
		result.CaseAttempts = append(result.CaseAttempts, attempt)
		outcomes[variant.VariantLabel] = outcome{idx: len(result.CaseAttempts) - 1, present: present, ok: ok}
	}

	onO, hasOn := outcomes["on"]
	offO, hasOff := outcomes["off"]
	if !hasOn || !hasOff {
		result.Status = model.StatusFail
		result.FailReason = "用例定义缺少 on/off 变体"
		return
	}
	if !onO.ok || !offO.ok {
		// 任一次请求本身失败（传输/HTTP/schema）：各自留痕保持 Passed=false + 各自的
		// 失败原因，绝不能因为另一侧的组合判定结果而被覆盖成 PASS。整体交给
		// finalizeAggregate 按“全部 attempt 都通过才 PASS”的规则汇总。
		return
	}
	v := assertion.ThinkingTogglePair(onO.present, offO.present)
	result.CaseAttempts[onO.idx].Passed = v.Passed
	result.CaseAttempts[offO.idx].Passed = v.Passed
	if !v.Passed {
		result.CaseAttempts[onO.idx].FailReason = v.Reason
		result.CaseAttempts[offO.idx].FailReason = v.Reason
	}
}

func (e *Engine) runDeterministicMultimodalQA(ctx context.Context, c suitedef.Case, result *model.CaseResult) {
	if c.MaterialRef == nil || e.Materials == nil {
		result.Status = model.StatusFail
		result.FailReason = "用例缺少 material_ref 或未加载素材清单"
		return
	}
	mat, ok := e.Materials.MaterialByID(c.MaterialRef.MaterialID)
	if !ok {
		result.Status = model.StatusFail
		result.FailReason = fmt.Sprintf("素材清单中找不到 material_id %q", c.MaterialRef.MaterialID)
		return
	}
	cr, err := e.doCall(ctx, c, nil)
	attempt := newAttempt(1, "", cr)
	switch {
	case err != nil:
		attempt.FailReason = err.Error()
		result.Status = model.StatusFail
	case cr.TransportErr != nil:
		attempt.FailReason = cr.TransportErr.Error()
		result.Status = model.StatusFail
	case cr.HTTPStatus != 200:
		attempt.FailReason = fmt.Sprintf("HTTP 状态码 %d，期望 200", cr.HTTPStatus)
		result.Status = model.StatusFail
	default:
		if ok, violations := openaiapi.ValidateSchema([]byte(cr.ResponseBody), false); !ok {
			attempt.FailReason = "openai_schema_valid 未通过: " + joinViolations(violations)
			result.Status = model.StatusFail
		} else {
			resp, perr := openaiapi.ParseResponse([]byte(cr.ResponseBody))
			if perr != nil {
				attempt.FailReason = "响应体解析失败: " + perr.Error()
				result.Status = model.StatusFail
			} else {
				text := ""
				if len(resp.Choices) > 0 && resp.Choices[0].Message != nil && resp.Choices[0].Message.Content != nil {
					text = *resp.Choices[0].Message.Content
				}
				mv := assertion.DeterministicMultimodalQA(text, mat.ExpectedAnswer)
				attempt.Passed = mv.Status == "PASS"
				attempt.FailReason = mv.Reason
				result.Status = model.CaseStatus(mv.Status)
			}
		}
	}
	result.CaseAttempts = append(result.CaseAttempts, attempt)
	result.Attempts = 1
	if attempt.Passed {
		result.PassedAttempts = 1
		result.PassRate = 1
	}
}

func (e *Engine) runReasoningEffortScaling(ctx context.Context, c suitedef.Case, result *model.CaseResult) {
	tierTokens := make(map[string][]int)
	idx := 0
	allAttemptsOK := true
	firstFailure := ""
	for _, variant := range c.Variants {
		for rep := 0; rep < c.RepeatAttempts; rep++ {
			idx++
			cr, err := e.doCall(ctx, c, variant.RequestOverrides)
			attempt := newAttempt(idx, variant.VariantLabel, cr)
			switch {
			case err != nil:
				attempt.FailReason = err.Error()
			case cr.TransportErr != nil:
				attempt.FailReason = cr.TransportErr.Error()
			case cr.HTTPStatus != 200:
				attempt.FailReason = fmt.Sprintf("HTTP 状态码 %d，期望 200", cr.HTTPStatus)
			default:
				if ok, violations := openaiapi.ValidateSchema([]byte(cr.ResponseBody), false); !ok {
					attempt.FailReason = "openai_schema_valid 未通过: " + joinViolations(violations)
				} else {
					resp, perr := openaiapi.ParseResponse([]byte(cr.ResponseBody))
					if perr != nil {
						attempt.FailReason = "响应体解析失败: " + perr.Error()
					} else if resp.Usage != nil && resp.Usage.CompletionTokensDetails != nil {
						tokens := resp.Usage.CompletionTokensDetails.ReasoningTokens
						attempt.ReasoningTokens = tokens
						attempt.Passed = true
						tierTokens[variant.VariantLabel] = append(tierTokens[variant.VariantLabel], tokens)
					} else {
						attempt.FailReason = "usage.completion_tokens_details.reasoning_tokens 缺失"
					}
				}
			}
			if !attempt.Passed {
				allAttemptsOK = false
				if firstFailure == "" {
					firstFailure = fmt.Sprintf("%s 第 %d 次采样: %s", variant.VariantLabel, rep+1, attempt.FailReason)
				}
			}
			result.CaseAttempts = append(result.CaseAttempts, attempt)
		}
	}

	// 先按实际留痕填好 attempts/passed_attempts/pass_rate（无论后面判 PASS 还是
	// FAIL，都不能出现「9 次采样全跑完但 attempts=0」这种与实际留痕脱节的汇总）。
	result.Attempts = len(result.CaseAttempts)
	for _, a := range result.CaseAttempts {
		if a.Passed {
			result.PassedAttempts++
		}
	}
	if result.Attempts > 0 {
		result.PassRate = float64(result.PassedAttempts) / float64(result.Attempts)
	}

	if !allAttemptsOK {
		// openai_schema_valid 全局基线叠加在每一条用例之上、不可跳过：任何一次
		// 采样（含 max 档）传输/HTTP/schema 失败，都不能被其余采样的“凑巧算出多数”
		// 掩盖过去，必须整体判 FAIL。
		result.Status = model.StatusFail
		result.FailReason = "存在采样请求未成功（传输/HTTP/schema 失败），无法可靠聚合 reasoning_tokens: " + firstFailure
		return
	}

	// 要求"有可观测差异"的档位对：默认 low vs high（kimi-k3 的 04 节口径，
	// 套件不填 assertion_params 时行为不变）。z.ai 这类把 low 与 high 映射到
	// 同一档思考强度的供应商，必须在套件里显式声明真正可区分的档位对
	// （如 [["low","max"]]），否则这条用例会因为供应商侧的档位映射而恒失败，
	// 那是套件口径没对齐，不是模型不合格。
	pairs := c.ParamStringPairs("required_distinct_pairs")
	if len(pairs) == 0 {
		pairs = [][2]string{{"low", "high"}}
	}
	for _, pair := range pairs {
		aAgg, aOK := majority(tierTokens[pair[0]])
		if !aOK {
			result.Status = model.StatusFail
			result.FailReason = fmt.Sprintf("%s 档 reasoning_tokens 未形成多数结果（各次采样取值分散，无单一取值占多数；该档共 %d 次采样）",
				pair[0], len(tierTokens[pair[0]]))
			return
		}
		bAgg, bOK := majority(tierTokens[pair[1]])
		if !bOK {
			result.Status = model.StatusFail
			result.FailReason = fmt.Sprintf("%s 档 reasoning_tokens 未形成多数结果（各次采样取值分散，无单一取值占多数；该档共 %d 次采样）",
				pair[1], len(tierTokens[pair[1]]))
			return
		}
		if aAgg == bAgg {
			result.Status = model.StatusFail
			result.FailReason = fmt.Sprintf("%s 档与 %s 档的 reasoning_tokens 多数结果均为 %d，无可观测差异",
				pair[0], pair[1], aAgg)
			return
		}
	}
	result.Status = model.StatusPass
}

// majority 取严格多数（出现次数 > N/2 的取值）。不足半数的最高频值不算“多数结果”，
// 返回 ok=false，交给调用方判定为聚合失败，而不是在并列/分散时静默挑一个值充数。
func majority(values []int) (int, bool) {
	if len(values) == 0 {
		return 0, false
	}
	counts := make(map[int]int)
	for _, v := range values {
		counts[v]++
	}
	threshold := len(values)/2 + 1
	keys := make([]int, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		if counts[k] >= threshold {
			return k, true
		}
	}
	return 0, false
}

func extractToolChoiceFunctionName(c suitedef.Case) (string, bool) {
	tc, ok := c.RequestTemplate.Body["tool_choice"].(map[string]any)
	if !ok {
		return "", false
	}
	fn, ok := tc["function"].(map[string]any)
	if !ok {
		return "", false
	}
	name, ok := fn["name"].(string)
	return name, ok
}

func extractAllowedToolNames(c suitedef.Case) []string {
	tc, ok := c.RequestTemplate.Body["tool_choice"].(map[string]any)
	if !ok {
		return nil
	}
	at, ok := tc["allowed_tools"].(map[string]any)
	if !ok {
		return nil
	}
	tools, ok := at["tools"].([]any)
	if !ok {
		return nil
	}
	var names []string
	for _, t := range tools {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := tm["function"].(map[string]any)
		if !ok {
			continue
		}
		if name, ok := fn["name"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

// extractToolParametersSchema 找到函数名匹配的工具定义，返回其声明的
// function.parameters（用作 tool_call_named 断言的参数 schema）。优先查字面
// tools 数组（用例未借助 fixtures 占位符定义工具的情况），因为 tools 通常是
// 通过 {{fixtures.tools.xxx}} 占位符引用的（渲染前仍是字符串），再回退到
// fixtures.tools 按函数名匹配查找。
//
// 返回值区分两种不同情况，调用方不能混为一谈：
//   - toolFound=false：tools/fixtures 里根本找不到这个函数名，属于套件定义本身
//     的不一致（tool_choice 指向了未声明的工具），应判 FAIL，不能放行。
//   - toolFound=true 但 schema==nil：工具存在，只是没有声明 parameters（例如
//     无参函数），此时"参数满足声明的 schema"没有约束可查，跳过 schema 校验是
//     合理的，不是套件定义错误。
func extractToolParametersSchema(fixtures suitedef.Fixtures, c suitedef.Case, fnName string) (schema map[string]any, toolFound bool) {
	if tools, ok := c.RequestTemplate.Body["tools"].([]any); ok {
		for _, t := range tools {
			tm, ok := t.(map[string]any)
			if !ok {
				continue
			}
			fn, ok := tm["function"].(map[string]any)
			if !ok {
				continue
			}
			if name, _ := fn["name"].(string); name == fnName {
				schema, _ := fn["parameters"].(map[string]any)
				return schema, true
			}
		}
	}
	for _, tool := range fixtures.Tools {
		fn, ok := tool["function"].(map[string]any)
		if !ok {
			continue
		}
		if name, _ := fn["name"].(string); name != fnName {
			continue
		}
		schema, _ := fn["parameters"].(map[string]any)
		return schema, true
	}
	return nil, false
}

func extractJSONSchema(c suitedef.Case) (map[string]any, bool) {
	rf, ok := c.RequestTemplate.Body["response_format"].(map[string]any)
	if !ok {
		return nil, false
	}
	js, ok := rf["json_schema"].(map[string]any)
	if !ok {
		return nil, false
	}
	schema, ok := js["schema"].(map[string]any)
	return schema, ok
}

// ── z-ai 套件引入的断言编排（见 suites/z-ai/SCHEMA.md）。判定逻辑本身都在
// internal/assertion，这里只负责取参数、组织请求次数、填留痕。

// requestBodyField 从本次调用的**实际请求体**里取一个顶层字段。判定参数必须
// 取自真正发出去的那个 body，而不是用例模板：模板里的值可能被 variant 的
// request_overrides 覆盖，用模板值去校验会出现"断言按 16 判、实际发了 32"的
// 错位。ok=false 表示请求体不可解析或没有该字段。
func requestBodyField(cr client.CallResult, key string) (any, bool) {
	var body map[string]any
	if err := json.Unmarshal([]byte(cr.RequestBody), &body); err != nil {
		return nil, false
	}
	v, ok := body[key]
	return v, ok
}

func (e *Engine) scoreMaxTokensTruncation(c suitedef.Case, cr client.CallResult, attempt *model.CaseAttempt) {
	resp, err := e.parseResponse([]byte(cr.ResponseBody))
	if err != nil {
		attempt.Passed, attempt.FailReason = false, "响应体解析失败: "+err.Error()
		return
	}
	raw, ok := requestBodyField(cr, "max_tokens")
	if !ok {
		attempt.Passed, attempt.FailReason = false, "用例定义缺少 max_tokens，无法校验强制截断"
		return
	}
	maxTokens, ok := raw.(float64)
	if !ok || maxTokens <= 0 {
		attempt.Passed, attempt.FailReason = false, fmt.Sprintf("请求体 max_tokens 取值非法: %v", raw)
		return
	}
	v := assertion.MaxTokensTruncation(resp, int(maxTokens))
	attempt.Passed, attempt.FailReason = v.Passed, v.Reason
}

func (e *Engine) scoreStopSequenceRespected(c suitedef.Case, cr client.CallResult, attempt *model.CaseAttempt) {
	resp, err := e.parseResponse([]byte(cr.ResponseBody))
	if err != nil {
		attempt.Passed, attempt.FailReason = false, "响应体解析失败: "+err.Error()
		return
	}
	raw, ok := requestBodyField(cr, "stop")
	if !ok {
		attempt.Passed, attempt.FailReason = false, "用例定义缺少 stop 字段，无法校验 stop 语义"
		return
	}
	var stops []string
	switch v := raw.(type) {
	case string:
		stops = []string{v}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				stops = append(stops, s)
			}
		}
	}
	if len(stops) == 0 {
		attempt.Passed, attempt.FailReason = false, fmt.Sprintf("请求体 stop 取值非法: %v", raw)
		return
	}
	verdict := assertion.StopSequenceRespected(resp, stops, c.ParamString("must_contain", ""))
	attempt.Passed, attempt.FailReason = verdict.Passed, verdict.Reason
}

func (e *Engine) scoreLongContextRecall(c suitedef.Case, cr client.CallResult, attempt *model.CaseAttempt) {
	resp, err := e.parseResponse([]byte(cr.ResponseBody))
	if err != nil {
		attempt.Passed, attempt.FailReason = false, "响应体解析失败: "+err.Error()
		return
	}
	needle := c.ParamString("needle", "")
	if needle == "" {
		attempt.Passed, attempt.FailReason = false, "用例定义缺少 assertion_params.needle，无法校验长上下文找回"
		return
	}
	minPromptTokens := c.ParamInt("min_prompt_tokens", 0)
	if minPromptTokens <= 0 {
		attempt.Passed, attempt.FailReason = false, "用例定义缺少 assertion_params.min_prompt_tokens（必须 >0），无法确认输入是否被截断"
		return
	}
	if resp.Usage != nil {
		attempt.Metrics = map[string]float64{"prompt_tokens": float64(resp.Usage.PromptTokens)}
	}
	v := assertion.LongContextRecall(resp, needle, minPromptTokens)
	attempt.Passed, attempt.FailReason = v.Passed, v.Reason
}

// runDeterministicRepeat 编排 temperature=0 确定性用例：同一请求连发
// repeat_attempts 次，全部成功后比对输出是否逐字节一致。比对结论同时写回每一条
// attempt——报告里点开任意一次采样都应能看到"是这组采样整体不一致"，而不是只有
// 最后一次带失败原因。
func (e *Engine) runDeterministicRepeat(ctx context.Context, c suitedef.Case, result *model.CaseResult) {
	n := repeatCount(c)
	if n < 2 {
		result.Status = model.StatusFail
		result.FailReason = "deterministic_repeat 用例的 repeat_attempts 必须 ≥2，否则没有可比对的第二次采样"
		return
	}
	texts := make([]string, 0, n)
	allOK := true
	for i := 0; i < n; i++ {
		cr, err := e.doCall(ctx, c, nil)
		attempt := e.newAttemptTraced(c, i+1, "", cr)
		switch {
		case err != nil:
			attempt.FailReason = err.Error()
		case cr.TransportErr != nil:
			attempt.FailReason = cr.TransportErr.Error()
		case cr.HTTPStatus != 200:
			attempt.FailReason = fmt.Sprintf("HTTP 状态码 %d，期望 200", cr.HTTPStatus)
		default:
			if ok, violations := e.validateNonStreamSchema([]byte(cr.ResponseBody)); !ok {
				attempt.FailReason = e.schemaBaselineLabel() + " 未通过: " + joinViolations(violations)
			} else if resp, perr := e.parseResponse([]byte(cr.ResponseBody)); perr != nil {
				attempt.FailReason = "响应体解析失败: " + perr.Error()
			} else if len(resp.Choices) == 0 || resp.Choices[0].Message == nil || resp.Choices[0].Message.Content == nil {
				attempt.FailReason = "响应缺少 content，无法比对确定性"
			} else {
				texts = append(texts, *resp.Choices[0].Message.Content)
				attempt.Passed = true
			}
		}
		if !attempt.Passed {
			allOK = false
		}
		result.CaseAttempts = append(result.CaseAttempts, attempt)
	}
	if !allOK {
		// 有采样根本没跑成功时不做一致性判定：拿剩下的几次"凑巧相同"来判 PASS
		// 会掩盖掉真实故障。交给 finalizeAggregate 按全量 attempt 汇总成 FAIL。
		return
	}
	v := assertion.DeterministicRepeat(texts)
	for i := range result.CaseAttempts {
		result.CaseAttempts[i].Passed = v.Passed
		if !v.Passed {
			result.CaseAttempts[i].FailReason = v.Reason
		}
	}
}

// runPromptCacheHitRate 编排上下文缓存命中率用例：先发 warmup_requests 次预热
// 请求把前缀写进缓存，再发一次同样的请求并对它判定命中率。预热请求只校验到
// "请求本身成功"为止，不参与命中率判定——首次请求必然 0 命中，拿它判定等于写了
// 一条恒假断言。
func (e *Engine) runPromptCacheHitRate(ctx context.Context, c suitedef.Case, result *model.CaseResult) {
	warmups := c.ParamInt("warmup_requests", 1)
	if warmups < 1 {
		warmups = 1
	}
	minRate := c.ParamFloat("min_hit_rate", 0.5)

	for i := 0; i < warmups; i++ {
		cr, err := e.doCall(ctx, c, nil)
		attempt := e.newAttemptTraced(c, i+1, "warmup", cr)
		switch {
		case err != nil:
			attempt.FailReason = err.Error()
		case cr.TransportErr != nil:
			attempt.FailReason = cr.TransportErr.Error()
		case cr.HTTPStatus != 200:
			attempt.FailReason = fmt.Sprintf("HTTP 状态码 %d，期望 200", cr.HTTPStatus)
		default:
			if ok, violations := e.validateNonStreamSchema([]byte(cr.ResponseBody)); !ok {
				attempt.FailReason = e.schemaBaselineLabel() + " 未通过: " + joinViolations(violations)
			} else {
				attempt.Passed = true
			}
		}
		result.CaseAttempts = append(result.CaseAttempts, attempt)
		if !attempt.Passed {
			return // 预热都没成功，后面那次的命中率没有意义
		}
	}

	cr, err := e.doCall(ctx, c, nil)
	attempt := e.newAttemptTraced(c, warmups+1, "measured", cr)
	switch {
	case err != nil:
		attempt.FailReason = err.Error()
	case cr.TransportErr != nil:
		attempt.FailReason = cr.TransportErr.Error()
	case cr.HTTPStatus != 200:
		attempt.FailReason = fmt.Sprintf("HTTP 状态码 %d，期望 200", cr.HTTPStatus)
	default:
		if ok, violations := e.validateNonStreamSchema([]byte(cr.ResponseBody)); !ok {
			attempt.FailReason = e.schemaBaselineLabel() + " 未通过: " + joinViolations(violations)
		} else if resp, perr := e.parseResponse([]byte(cr.ResponseBody)); perr != nil {
			attempt.FailReason = "响应体解析失败: " + perr.Error()
		} else {
			v, rate := assertion.PromptCacheHitRate(resp, minRate)
			attempt.Metrics = map[string]float64{"prompt_cache_hit_rate": rate}
			if resp.Usage != nil {
				attempt.Metrics["prompt_tokens"] = float64(resp.Usage.PromptTokens)
				if cached, present := resp.Usage.CachedTokens(); present {
					attempt.Metrics["cached_tokens"] = float64(cached)
				}
			}
			attempt.Passed, attempt.FailReason = v.Passed, v.Reason
		}
	}
	result.CaseAttempts = append(result.CaseAttempts, attempt)
}
