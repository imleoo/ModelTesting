// Package engine 编排单条用例的执行：按能力声明裁剪、渲染请求、调用被测网关、
// 跑全局 openai_schema_valid 基线 + 04 节对应断言类型，产出 CaseResult/CaseAttempt。
package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

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
		return false, ""
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
	default:
		return false
	}
}

// RunCase 执行单条用例，返回 CaseResult（含全部 CaseAttempt 留痕）。
func (e *Engine) RunCase(ctx context.Context, c suitedef.Case) model.CaseResult {
	result := model.CaseResult{CaseID: c.ID, ID: c.ID}

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

// scoreFunc 对一次已完成的调用做业务断言，填充 attempt.Passed/FailReason。
type scoreFunc func(c suitedef.Case, cr client.CallResult, attempt *model.CaseAttempt)

func (e *Engine) runSingleNonStream(ctx context.Context, c suitedef.Case, result *model.CaseResult, score scoreFunc) {
	cr, err := e.doCall(ctx, c, nil)
	attempt := newAttempt(1, "", cr)
	if err != nil {
		attempt.Passed = false
		attempt.FailReason = err.Error()
	} else if cr.TransportErr != nil {
		attempt.Passed = false
		attempt.FailReason = cr.TransportErr.Error()
	} else if cr.HTTPStatus != 200 {
		attempt.Passed = false
		attempt.FailReason = fmt.Sprintf("HTTP 状态码 %d，期望 200", cr.HTTPStatus)
	} else if ok, violations := openaiapi.ValidateSchema([]byte(cr.ResponseBody), false); !ok {
		attempt.Passed = false
		attempt.FailReason = "openai_schema_valid 未通过: " + joinViolations(violations)
	} else {
		score(c, cr, &attempt)
	}
	result.CaseAttempts = append(result.CaseAttempts, attempt)
}

func (e *Engine) runSingleStream(ctx context.Context, c suitedef.Case, result *model.CaseResult, score scoreFunc) {
	cr, err := e.doCall(ctx, c, nil)
	attempt := newAttempt(1, "", cr)
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
		attempt.FailReason = "openai_schema_valid 未通过: " + reason
	} else {
		score(c, cr, &attempt)
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
	v := assertion.ToolCallNamed(resp, name)
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
			} else {
				lastUsage := lastChunkUsage(cr)
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

func lastChunkUsage(cr client.CallResult) *openaiapi.Usage {
	for i := len(cr.SSEResult.Chunks) - 1; i >= 0; i-- {
		chunk, err := openaiapi.ParseChunk(cr.SSEResult.Chunks[i])
		if err != nil {
			continue
		}
		if chunk.Usage != nil {
			return chunk.Usage
		}
	}
	return nil
}

func (e *Engine) runThinkingTogglePair(ctx context.Context, c suitedef.Case, result *model.CaseResult) {
	presentByLabel := make(map[string]bool)
	for i, variant := range c.Variants {
		cr, err := e.doCall(ctx, c, variant.RequestOverrides)
		attempt := newAttempt(i+1, variant.VariantLabel, cr)
		var present bool
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
				} else {
					present = len(resp.Choices) > 0 && resp.Choices[0].Message != nil && assertion.ThinkingPresent(resp.Choices[0].Message.ReasoningContent)
				}
			}
		}
		presentByLabel[variant.VariantLabel] = present
		result.CaseAttempts = append(result.CaseAttempts, attempt)
	}
	v := assertion.ThinkingTogglePair(presentByLabel["on"], presentByLabel["off"])
	for i := range result.CaseAttempts {
		result.CaseAttempts[i].Passed = v.Passed
		if !v.Passed && result.CaseAttempts[i].FailReason == "" {
			result.CaseAttempts[i].FailReason = v.Reason
		}
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
			result.CaseAttempts = append(result.CaseAttempts, attempt)
		}
	}

	lowAgg, lowOK := majority(tierTokens["low"])
	highAgg, highOK := majority(tierTokens["high"])
	if !lowOK || !highOK {
		result.Status = model.StatusFail
		result.FailReason = "low/high 档位未能取得足够的有效采样，无法聚合比较"
		return
	}
	if lowAgg == highAgg {
		result.Status = model.StatusFail
		result.FailReason = fmt.Sprintf("low 档与 high 档的 reasoning_tokens 聚合值均为 %d，无可观测差异", lowAgg)
		return
	}
	result.Status = model.StatusPass
}

// majority 取众数（出现次数最多的值）；并列时取数值较小者，保证确定性。
func majority(values []int) (int, bool) {
	if len(values) == 0 {
		return 0, false
	}
	counts := make(map[int]int)
	for _, v := range values {
		counts[v]++
	}
	best, bestCount := 0, -1
	keys := make([]int, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		if counts[k] > bestCount {
			best, bestCount = k, counts[k]
		}
	}
	return best, true
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
