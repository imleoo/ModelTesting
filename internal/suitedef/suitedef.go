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
}

type Fixtures struct {
	Tools   map[string]map[string]any `json:"tools"`
	Prompts map[string]string         `json:"prompts"`
}

type Defaults struct {
	TimeoutSeconds           int            `json:"timeout_seconds"`
	CategoryTimeoutOverrides map[string]int `json:"category_timeout_overrides"`
}

type Protocol struct {
	BasePath    string `json:"base_path"`
	AuthHeader  string `json:"auth_header"`
	ContentType string `json:"content_type"`
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
func LoadMaterialsManifestForSuite(repoRoot string, s *Suite) (*MaterialsManifest, error) {
	return LoadMaterialsManifest(filepath.Join(repoRoot, s.MaterialsManifest))
}
