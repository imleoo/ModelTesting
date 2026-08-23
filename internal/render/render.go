// Package render 把 suite.vN.json 里的 request_template.body 渲染成真实请求体：
// 替换 {{model_key}}、{{prompts.xxx}}、{{material:...}} 等占位符，并对
// {{fixtures.tools.xxx}} 这类整节点占位符做对象级替换而非字符串插值
// （规则见 suites/kimi-k3/SCHEMA.md「请求模板占位符」一节）。
package render

import (
	"encoding/base64"
	"fmt"
	"maps"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/leoobai/modeltestbed/internal/suitedef"
)

type Context struct {
	ModelKey        string
	APIKey          string
	Fixtures        suitedef.Fixtures
	Materials       *suitedef.MaterialsManifest
	MaterialBaseURL string // 如 http://127.0.0.1:8080，用于拼接 material url 形态
}

var placeholderRe = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

// Body 渲染 request_template.body，并把 variant 的 request_overrides 做浅合并
// （顶层键覆盖）后再整体渲染。
func Body(tmpl map[string]any, overrides map[string]any, ctx Context) (map[string]any, error) {
	merged := mergeShallow(tmpl, overrides)
	rendered, err := renderNode(merged, ctx)
	if err != nil {
		return nil, err
	}
	m, ok := rendered.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("rendered body is not an object")
	}
	return m, nil
}

func mergeShallow(base map[string]any, overrides map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(overrides))
	maps.Copy(out, base)
	maps.Copy(out, overrides)
	return out
}

func renderNode(node any, ctx Context) (any, error) {
	switch v := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			rv, err := renderNode(val, ctx)
			if err != nil {
				return nil, err
			}
			out[k] = rv
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			rv, err := renderNode(val, ctx)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	case string:
		return renderString(v, ctx)
	default:
		return v, nil
	}
}

// renderString 处理单个字符串节点：如果整个字符串就是一个占位符，
// 按该占位符解析出的值类型返回（可能不是字符串，如 fixtures.tools.xxx 是对象）；
// 否则做普通的子串插值，插值结果统一转成字符串拼接。
func renderString(s string, ctx Context) (any, error) {
	matches := placeholderRe.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 1 && matches[0][0] == 0 && matches[0][1] == len(s) {
		token := s[matches[0][2]:matches[0][3]]
		return resolveToken(token, ctx)
	}

	var sb strings.Builder
	last := 0
	for _, m := range matches {
		sb.WriteString(s[last:m[0]])
		token := s[m[2]:m[3]]
		val, err := resolveToken(token, ctx)
		if err != nil {
			return nil, err
		}
		scalar, ok := asScalarString(val)
		if !ok {
			return nil, fmt.Errorf("占位符 %q 解析出的值不是标量（%T），不能与其他文本拼接在同一字符串里；"+
				"该占位符只能作为整节点单独出现（见 SCHEMA.md 请求模板占位符一节）", token, val)
		}
		sb.WriteString(scalar)
		last = m[1]
	}
	sb.WriteString(s[last:])
	return sb.String(), nil
}

// asScalarString 把标量值（string/数字/布尔/nil）转成字符串用于拼接；
// map/slice 等复合值返回 ok=false，调用方应当报错而不是用 %v 生成 "map[...]" 这种
// 对下游请求体毫无意义的字符串。
func asScalarString(v any) (string, bool) {
	switch v.(type) {
	case map[string]any, []any:
		return "", false
	default:
		return fmt.Sprintf("%v", v), true
	}
}

func resolveToken(token string, ctx Context) (any, error) {
	switch {
	case token == "model_key":
		return ctx.ModelKey, nil
	case token == "api_key":
		return ctx.APIKey, nil
	case strings.HasPrefix(token, "prompts."):
		key := strings.TrimPrefix(token, "prompts.")
		val, ok := ctx.Fixtures.Prompts[key]
		if !ok {
			return nil, fmt.Errorf("unknown prompt fixture %q", key)
		}
		return val, nil
	case strings.HasPrefix(token, "fixtures.tools."):
		key := strings.TrimPrefix(token, "fixtures.tools.")
		val, ok := ctx.Fixtures.Tools[key]
		if !ok {
			return nil, fmt.Errorf("unknown tool fixture %q", key)
		}
		return val, nil
	case strings.HasPrefix(token, "material:"):
		return resolveMaterialToken(token, ctx)
	case strings.HasPrefix(token, "filler:"):
		return resolveFillerToken(token)
	default:
		return nil, fmt.Errorf("unknown placeholder %q", token)
	}
}

// resolveMaterialToken 解析 "material:<id>:url" / "material:<id>:base64" /
// "material:<id>:question_prompt"。
func resolveMaterialToken(token string, ctx Context) (any, error) {
	parts := strings.SplitN(token, ":", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed material placeholder %q", token)
	}
	materialID, form := parts[1], parts[2]
	if ctx.Materials == nil {
		return nil, fmt.Errorf("no materials manifest loaded for placeholder %q", token)
	}
	mat, ok := ctx.Materials.MaterialByID(materialID)
	if !ok {
		return nil, fmt.Errorf("unknown material id %q", materialID)
	}
	switch form {
	case "url":
		if ctx.MaterialBaseURL == "" {
			return nil, fmt.Errorf("material base url not configured, cannot resolve %q", token)
		}
		return strings.TrimRight(ctx.MaterialBaseURL, "/") + mat.Hosting.FrozenURLPath, nil
	case "base64":
		raw, err := os.ReadFile(ctx.Materials.FilePath(mat))
		if err != nil {
			return nil, fmt.Errorf("read material file for %q: %w", materialID, err)
		}
		return base64.StdEncoding.EncodeToString(raw), nil
	case "question_prompt":
		return mat.QuestionPrompt, nil
	default:
		return nil, fmt.Errorf("unknown material form %q in placeholder %q", form, token)
	}
}

// fillerSentence 是 {{filler:N}} 的重复单元。三条硬性约束，改动前先读完：
//  1. 不含任何阿拉伯数字——长上下文用例靠"回答里出现的唯一数字串"判定是否
//     找回了埋在正中间的暗号，填充语料里只要出现数字就会污染判定。
//  2. 内容固定、不随机——同一个 {{filler:N}} 必须每次渲染出逐字节相同的文本，
//     否则上下文缓存命中率用例的两次请求前缀不一致，缓存必然不命中。
//  3. 语义上明确声明"本段不含答案"——避免模型把填充语料当成需要总结的正文。
const fillerSentence = "这是长上下文填充语料，本段不包含任何答案或暗号，仅用于把上下文撑到目标长度，请忽略本段内容。"

// resolveFillerToken 解析 "filler:<字符数>"，返回由 fillerSentence 重复拼接、
// 精确截断到该字符数（按 rune 计，不是字节）的确定性文本。
//
// 为什么参数是字符数而不是 token 数：token 数取决于供应商的分词器，测试台
// 无法在本地精确计算；用例真正要断言的"输入确实达到了目标规模"由响应体的
// usage.prompt_tokens 来验证（见 long_context_recall 的 min_prompt_tokens
// 参数），这里只负责生成一份可复现的、足够大的输入。
func resolveFillerToken(token string) (any, error) {
	parts := strings.SplitN(token, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed filler placeholder %q", token)
	}
	n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return nil, fmt.Errorf("filler placeholder %q 的字符数参数不是整数: %w", token, err)
	}
	if n < 0 {
		return nil, fmt.Errorf("filler placeholder %q 的字符数不能为负", token)
	}
	unit := []rune(fillerSentence)
	out := make([]rune, 0, n)
	for len(out) < n {
		remain := n - len(out)
		if remain >= len(unit) {
			out = append(out, unit...)
			continue
		}
		out = append(out, unit[:remain]...)
	}
	return string(out), nil
}
