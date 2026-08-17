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
		fmt.Fprintf(&sb, "%v", val)
		last = m[1]
	}
	sb.WriteString(s[last:])
	return sb.String(), nil
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
