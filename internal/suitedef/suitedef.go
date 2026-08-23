// Package suitedef 加载 suites/<suite_id>/suite.vN.json 与 materials/manifest.json，
// 提供 P1 用例引擎读取用例目录与素材清单的能力。字段含义见
// suites/kimi-k3/SCHEMA.md。
package suitedef

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type RequiredRule string

const (
	FixedRequired    RequiredRule = "fixed_required"
	DeclaredRequired RequiredRule = "declared_required"
	ExemptAllowed    RequiredRule = "exempt_allowed"
	Additional       RequiredRule = "additional"
)

type Variant struct {
	VariantLabel     string         `json:"variant_label"`
	RequestOverrides map[string]any `json:"request_overrides"`
}

type MaterialRef struct {
	MaterialID string `json:"material_id"`
	Form       string `json:"form"` // "url" | "base64"
}

type RequestTemplate struct {
	Method string         `json:"method"`
	Body   map[string]any `json:"body"`
}

type Case struct {
	ID              string          `json:"id"`
	Category        string          `json:"category"`
	Name            string          `json:"name"`
	RequiredRule    RequiredRule    `json:"required_rule"`
	CountsInBase22  bool            `json:"counts_in_base22"`
	CapabilityTag   string          `json:"capability_tag"`
	AssertionType   string          `json:"assertion_type"`
	RepeatAttempts  int             `json:"repeat_attempts"`
	PDFRef          string          `json:"pdf_ref"`
	Notes           string          `json:"notes"`
	MaterialRef     *MaterialRef    `json:"material_ref"`
	RequestTemplate RequestTemplate `json:"request_template"`
	Variants        []Variant       `json:"variants"`

	// AssertionParams 是断言类型自带的判定参数（如 max_tokens_truncation 的
	// expected_finish_reason、prompt_cache_hit_rate 的 min_hit_rate）。放在用例
	// 数据里而不是写死在引擎里，是为了让同一个断言类型能被不同供应商用不同阈值
	// 复用（见 suites/z-ai/SCHEMA.md「assertion_params」一节）。为空表示该断言
	// 全部走内置默认值——kimi-k3 套件不填这个字段，行为与引入本字段前一致。
	AssertionParams map[string]any `json:"assertion_params"`
}

// ParamInt 读取 assertion_params 里的整数参数。JSON 解出来的数字是 float64，
// 这里统一收敛掉；缺失或类型不对时返回 def，由调用方决定是当默认值用还是报错。
func (c Case) ParamInt(key string, def int) int {
	switch v := c.AssertionParams[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return def
	}
}

// ParamFloat 读取 assertion_params 里的浮点参数，语义同 ParamInt。
func (c Case) ParamFloat(key string, def float64) float64 {
	switch v := c.AssertionParams[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return def
	}
}

// ParamString 读取 assertion_params 里的字符串参数，语义同 ParamInt。
func (c Case) ParamString(key, def string) string {
	if v, ok := c.AssertionParams[key].(string); ok {
		return v
	}
	return def
}

// ParamStringPairs 读取形如 [["low","max"],["off","max"]] 的字符串对列表，
// 供 reasoning_effort_scaling 声明「哪些档位之间必须有可观测差异」。任何一对
// 不是恰好两个字符串的元素都会被跳过，避免一处笔误让整条用例静默失去约束。
func (c Case) ParamStringPairs(key string) [][2]string {
	raw, ok := c.AssertionParams[key].([]any)
	if !ok {
		return nil
	}
	var out [][2]string
	for _, item := range raw {
		pair, ok := item.([]any)
		if !ok || len(pair) != 2 {
			continue
		}
		a, aok := pair[0].(string)
		b, bok := pair[1].(string)
		if aok && bok {
			out = append(out, [2]string{a, b})
		}
	}
	return out
}

type Fixtures struct {
	Tools   map[string]map[string]any `json:"tools"`
	Prompts map[string]string         `json:"prompts"`
}

type Defaults struct {
	TimeoutSeconds           int            `json:"timeout_seconds"`
	CategoryTimeoutOverrides map[string]int `json:"category_timeout_overrides"`
}

// 协议风格取值：Style 为空等价于 StyleOpenAIChatCompletions（现状默认，
// kimi-k3 的 suite.v1.json 不需要显式填写这个字段）。
const (
	StyleOpenAIChatCompletions = "openai_chat_completions"
	StyleAnthropicMessages     = "anthropic_messages"
)

type Protocol struct {
	BasePath    string `json:"base_path"`
	AuthHeader  string `json:"auth_header"`
	ContentType string `json:"content_type"`
	// Style 声明被测网关走哪种线上协议形状；见上面两个常量。空值按
	// StyleOpenAIChatCompletions 处理，保持 kimi-k3 现状行为不变。
	Style string `json:"style"`
}

type Suite struct {
	SuiteID           string   `json:"suite_id"`
	SuiteVersion      string   `json:"suite_version"`
	Protocol          Protocol `json:"protocol"`
	Defaults          Defaults `json:"defaults"`
	Fixtures          Fixtures `json:"fixtures"`
	MaterialsManifest string   `json:"materials_manifest"`
	Cases             []Case   `json:"cases"`

	// dir 是 suite.vN.json 所在目录，供加载 materials manifest 时解析相对路径。
	dir string `json:"-"`
}

// TimeoutFor 返回某类别用例的单请求超时时间。
func (s *Suite) TimeoutFor(category string) int {
	if t, ok := s.Defaults.CategoryTimeoutOverrides[category]; ok {
		return t
	}
	return s.Defaults.TimeoutSeconds
}

// CaseByID 按 id 查找用例定义。
func (s *Suite) CaseByID(id string) (Case, bool) {
	for _, c := range s.Cases {
		if c.ID == id {
			return c, true
		}
	}
	return Case{}, false
}

func LoadSuite(path string) (*Suite, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read suite file: %w", err)
	}
	var s Suite
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("parse suite json: %w", err)
	}
	s.dir = filepath.Dir(path)
	return &s, nil
}

type AnswerMatchRule struct {
	ExtractedDistinctDigitRuns any    `json:"extracted_distinct_digit_runs"` // int 或 "≥2"
	MatchesExpectedAnswer      *bool  `json:"matches_expected_answer"`
	Verdict                    string `json:"verdict"`
	Reason                     string `json:"reason"`
}

type AnswerMatchDefinition struct {
	Description  string            `json:"description"`
	DecisionRule []AnswerMatchRule `json:"decision_rule"`
}

type MaterialHosting struct {
	Mode              string            `json:"mode"`
	ServerBinary      string            `json:"server_binary"`
	FrozenURLPath     string            `json:"frozen_url_path"`
	BaseURLResolution map[string]string `json:"base_url_resolution"`
	Status            string            `json:"status"`
	Note              string            `json:"note"`
}

type Material struct {
	ID              string          `json:"id"`
	Kind            string          `json:"kind"`
	File            string          `json:"file"`
	MimeType        string          `json:"mime_type"`
	SHA256          string          `json:"sha256"`
	SizeBytes       int64           `json:"size_bytes"`
	Base64SizeBytes int64           `json:"base64_size_bytes"`
	ExpectedAnswer  string          `json:"expected_answer"`
	QuestionPrompt  string          `json:"question_prompt"`
	AnswerMatch     string          `json:"answer_match"`
	UsedByCases     []string        `json:"used_by_cases"`
	Hosting         MaterialHosting `json:"hosting"`
}

type MaterialsManifest struct {
	ManifestVersion        string                           `json:"manifest_version"`
	SuiteID                string                           `json:"suite_id"`
	AnswerMatchDefinitions map[string]AnswerMatchDefinition `json:"answer_match_definitions"`
	Materials              []Material                       `json:"materials"`

	dir string `json:"-"`
}

// MaterialByID 按 id 查找素材定义。
func (m *MaterialsManifest) MaterialByID(id string) (Material, bool) {
	for _, mat := range m.Materials {
		if mat.ID == id {
			return mat, true
		}
	}
	return Material{}, false
}

// FilePath 返回素材文件在磁盘上的绝对路径（manifest.json 所在目录 + material.File）。
func (m *MaterialsManifest) FilePath(mat Material) string {
	return filepath.Join(m.dir, mat.File)
}

func LoadMaterialsManifest(path string) (*MaterialsManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read materials manifest: %w", err)
	}
	var m MaterialsManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse materials manifest json: %w", err)
	}
	m.dir = filepath.Dir(path)
	return &m, nil
}

// LoadMaterialsManifestForSuite 加载某个 Suite 声明的 materials_manifest（相对仓库根的路径）。
//
// 套件未声明 materials_manifest 时返回 (nil, nil)，不是错误：纯文本套件
// （如 suites/z-ai）没有多模态用例，也就没有素材清单。此前这里会把空路径拼成
// 仓库根目录再去 ReadFile，得到一个"is a directory"的错误，让调用方误以为素材
// 清单坏了。引擎侧对 Materials==nil 已有处理（deterministic_multimodal_qa 会
// 判 FAIL 并说明未加载素材清单），不会静默放过真正需要素材的用例。
func LoadMaterialsManifestForSuite(repoRoot string, s *Suite) (*MaterialsManifest, error) {
	if s.MaterialsManifest == "" {
		return nil, nil
	}
	return LoadMaterialsManifest(filepath.Join(repoRoot, s.MaterialsManifest))
}
