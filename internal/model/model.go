// Package model 定义设计方案 03 节数据模型对应的 Go 结构体，供 P1 用例引擎使用。
package model

import (
	"fmt"
	"slices"
)

// CaseStatus 对应设计方案 03 节 CASE_RESULT.status 枚举。
type CaseStatus string

const (
	StatusPass         CaseStatus = "PASS"
	StatusFail         CaseStatus = "FAIL"
	StatusNotDeclared  CaseStatus = "NOT_DECLARED"
	StatusManualReview CaseStatus = "MANUAL_REVIEW"
)

// CapabilityProfile 对应 03 节 CAPABILITY_PROFILE 实体。
type CapabilityProfile struct {
	ModelID                 string   `json:"model_id"`
	ImageURL                bool     `json:"image_url"`
	ImageBase64             bool     `json:"image_base64"`
	VideoURL                bool     `json:"video_url"`
	VideoBase64             bool     `json:"video_base64"`
	ToolCall                bool     `json:"tool_call"`
	ToolChoiceFunction      bool     `json:"tool_choice_function"`
	ThinkingToggleMethods   []string `json:"thinking_toggle_methods"`
	DefaultThinkingBehavior string   `json:"default_thinking_behavior"` // "thinks_by_default" | "no_thinking_by_default"
	ReasoningEffort         bool     `json:"reasoning_effort"`
}

// HasThinkingMethod 判断某个思考开关方式（method_key，如 "enable_thinking"）
// 是否在 CAPABILITY_PROFILE.thinking_toggle_methods 声明列表中。
func (c CapabilityProfile) HasThinkingMethod(methodKey string) bool {
	return slices.Contains(c.ThinkingToggleMethods, methodKey)
}

// ValidateCapabilityProfile 校验能力声明本身是否合法（不是测试用例执行时才发现
// 声明有问题）。设计方案 04 节明确约束：thinking_toggle_methods 声明列表至少
// 包含一种方式，否则视为能力声明本身不合法，应在加载时拒绝。
func ValidateCapabilityProfile(c CapabilityProfile) error {
	if len(c.ThinkingToggleMethods) == 0 {
		return fmt.Errorf("thinking_toggle_methods 至少需要声明一种支持的思考开关方式（设计方案 04 节约束）")
	}
	if c.DefaultThinkingBehavior != "thinks_by_default" && c.DefaultThinkingBehavior != "no_thinking_by_default" {
		return fmt.Errorf("default_thinking_behavior 取值非法: %q，必须是 thinks_by_default 或 no_thinking_by_default", c.DefaultThinkingBehavior)
	}
	return nil
}

// CaseAttempt 对应 03 节 CASE_ATTEMPT 实体：请求级明细。
type CaseAttempt struct {
	ID              string `json:"id"`
	CaseResultID    string `json:"case_result_id"`
	AttemptIndex    int    `json:"attempt_index"`
	VariantLabel    string `json:"variant_label"`
	HTTPStatus      int    `json:"http_status"`
	LatencyMS       int64  `json:"latency_ms"`
	RequestBody     string `json:"request_body"`
	ResponseBody    string `json:"response_body"`
	ReasoningTokens int    `json:"reasoning_tokens,omitempty"`
	Passed          bool   `json:"passed"`
	FailReason      string `json:"fail_reason,omitempty"`
}

// CaseResult 对应 03 节 CASE_RESULT 实体：用例级汇总。
type CaseResult struct {
	ID             string        `json:"id"`
	CaseID         string        `json:"case_id"`
	Status         CaseStatus    `json:"status"`
	Attempts       int           `json:"attempts"`
	PassedAttempts int           `json:"passed_attempts"`
	PassRate       float64       `json:"pass_rate"`
	FailReason     string        `json:"fail_reason,omitempty"`
	CaseAttempts   []CaseAttempt `json:"case_attempts"`
	// CountsInBase22 冗余保存用例定义里的 counts_in_base22（05 节口径：附加用例
	// 如 reasoning_effort 不计入 22 项分母），供报告/CLI 汇总时无需回查套件定义。
	CountsInBase22 bool `json:"counts_in_base22"`
}

// BenchmarkRun 对应 03 节 BENCHMARK_RUN 实体：一次压测的整体留痕。
type BenchmarkRun struct {
	ID             string  `json:"id"`
	RawCommand     string  `json:"raw_command"`
	RawParamsJSON  string  `json:"raw_params_json"`
	ToolVersion    string  `json:"tool_version"`
	DatasetVersion string  `json:"dataset_version"`
	RawStdoutRef   string  `json:"raw_stdout_ref"`
	TotalRequests  int     `json:"total_requests"`
	DurationS      float64 `json:"duration_s"`
}

// BaselineVerdict 对应 06 节 6.2 判定规则的三态结果。
type BaselineVerdict string

const (
	BaselineOK            BaselineVerdict = "OK"             // 达到或优于基线
	BaselineSameOrder     BaselineVerdict = "SAME_ORDER"     // 劣化但仍在 MAX_DEGRADE_RATIO 倍数内，"同一数量级"
	BaselineFail          BaselineVerdict = "FAIL"           // 劣化超出 MAX_DEGRADE_RATIO
	BaselineManualReview  BaselineVerdict = "MANUAL_REVIEW"  // 无 PDF 基线，仅记录（Latency/ITL）
	BaselineNotObservable BaselineVerdict = "NOT_OBSERVABLE" // 指标来源不可得（缓存命中率兜底规则）
	BaselineNotApplicable BaselineVerdict = "NOT_APPLICABLE" // 该指标本身无判定规则（仅记录，如 Total requests/Duration）
)

// BenchmarkMetric 对应 03 节 BENCHMARK_METRIC 实体：单个指标的分位数与判定结果。
type BenchmarkMetric struct {
	Name            string          `json:"name"`  // "throughput_req_s" | "ttft_s" | "tpot_ms" | "itl_ms" | "latency_s" | "cache_hit_rate" ...
	Scope           string          `json:"scope"` // "overall" | "per_round"
	RoundIndex      int             `json:"round_index,omitempty"`
	Avg             float64         `json:"avg"`
	P50             float64         `json:"p50"`
	P75             float64         `json:"p75"`
	P90             float64         `json:"p90"`
	P95             float64         `json:"p95"`
	P99             float64         `json:"p99"`
	Unit            string          `json:"unit"`
	BaselineVerdict BaselineVerdict `json:"baseline_verdict"`
	Note            string          `json:"note,omitempty"`
}
