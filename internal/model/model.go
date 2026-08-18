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

// Provider 对应 03 节 PROVIDER 实体：供应商登记信息（P4 Web 控制台「模型登记」
// 页面的顶层记录）。
type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Contact string `json:"contact,omitempty"`
}

// Model 对应 03 节 MODEL 实体：某供应商在 tokenpanel 上开通的一个被测模型端点。
// EndpointViaTokenpanel 是 tokenpanel 暴露的 OpenAI 兼容 base URL（不是供应商
// 自己的上游地址——本系统只消费 tokenpanel 已开通的端点，见 01 节范围边界）。
type Model struct {
	ID                    string            `json:"id"`
	ProviderID            string            `json:"provider_id"`
	ModelKey              string            `json:"model_key"`
	EndpointViaTokenpanel string            `json:"endpoint_via_tokenpanel"`
	Capability            CapabilityProfile `json:"capability"`
}

// TestRunStatus 是 TEST_RUN.status 的枚举取值，对应 09 节执行时序：功能测试
// 先跑，全部必过用例通过且无未清空的 MANUAL_REVIEW 才解锁压测；任一环节失败
// 都会提前终止，不会静默卡在中间状态。
type TestRunStatus string

const (
	RunPending           TestRunStatus = "PENDING"
	RunRunningFunctional TestRunStatus = "RUNNING_FUNCTIONAL"
	RunFunctionalBlocked TestRunStatus = "FUNCTIONAL_BLOCKED" // 必过用例未 100% 通过或存在未清空 MANUAL_REVIEW，压测被阻断（09 节 alt 分支）
	RunRunningBenchmark  TestRunStatus = "RUNNING_BENCHMARK"
	RunCompleted         TestRunStatus = "COMPLETED"
	RunFailed            TestRunStatus = "FAILED" // 执行过程本身出错（网络/引擎异常），不是业务判定失败
)

// ValidateTestRunStatus 校验状态值是否落在上面枚举的合法取值内。SQLite 的
// status 列是普通 TEXT，DDL 层面不会拦住任意字符串，写入前必须在应用层过一
// 遍白名单，否则一条非法状态会让状态机后续的分支判断（如"是否已完成"）
// 全部落空。
func ValidateTestRunStatus(s TestRunStatus) error {
	switch s {
	case RunPending, RunRunningFunctional, RunFunctionalBlocked, RunRunningBenchmark, RunCompleted, RunFailed:
		return nil
	default:
		return fmt.Errorf("非法的 TestRunStatus 取值: %q", s)
	}
}

// TestRun 对应 03 节 TEST_RUN 实体：一次完整测试执行的生命周期记录。
// CaseResultsPath/BenchmarkResultPath 指向本地文件系统上的详细产物（10.1 节：
// SQLite 只存协调元数据，请求/响应体与压测原始日志走本地文件系统），不在
// SQLite 里重复存一份，避免同一份数据两个真相来源。
type TestRun struct {
	ID                  string        `json:"id"`
	ModelID             string        `json:"model_id"`
	SuiteID             string        `json:"suite_id"`
	Status              TestRunStatus `json:"status"`
	StartedAt           string        `json:"started_at"`
	FinishedAt          string        `json:"finished_at,omitempty"`
	CaseResultsPath     string        `json:"case_results_path,omitempty"`
	BenchmarkResultPath string        `json:"benchmark_result_path,omitempty"`
	ErrorMessage        string        `json:"error_message,omitempty"`
}

// Report 对应 03 节 REPORT 实体：某次 TEST_RUN 生成的报告产物指针。
type Report struct {
	ID          string `json:"id"`
	TestRunID   string `json:"test_run_id"`
	GeneratedAt string `json:"generated_at"`
	Verdict     string `json:"verdict"`
	HTMLRef     string `json:"html_ref"`
}
