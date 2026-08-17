package assertion

import "fmt"

// ValidateJSONSchema 是一个最小 JSON Schema 校验器，只支持套件当前用到的子集：
// type=object/string/number/integer/boolean/array，properties、required、
// additionalProperties:false。不追求通用 JSON Schema 实现，按 04 节
// json_schema_valid 断言「输出用给定 JSON Schema 做结构校验，零违规」的实际需要收敛范围。
func ValidateJSONSchema(schema map[string]any, data any) []string {
	var violations []string
	validateNode(schema, data, "$", &violations)
	return violations
}

func validateNode(schema map[string]any, data any, path string, violations *[]string) {
	wantType, _ := schema["type"].(string)
	if wantType != "" && !typeMatches(wantType, data) {
		*violations = append(*violations, fmt.Sprintf("%s 类型应为 %s，实际为 %s", path, wantType, goTypeName(data)))
		return
	}

	if wantType == "object" {
		obj, ok := data.(map[string]any)
		if !ok {
			return
		}
		propsRaw, _ := schema["properties"].(map[string]any)
		for key, propSchemaRaw := range propsRaw {
			propSchema, _ := propSchemaRaw.(map[string]any)
			if val, present := obj[key]; present {
				validateNode(propSchema, val, path+"."+key, violations)
			}
		}
		if required, ok := schema["required"].([]any); ok {
			for _, r := range required {
				name, _ := r.(string)
				if _, present := obj[name]; !present {
					*violations = append(*violations, fmt.Sprintf("%s 缺少必填字段 %s", path, name))
				}
			}
		}
		if additional, ok := schema["additionalProperties"].(bool); ok && !additional {
			for key := range obj {
				if _, declared := propsRaw[key]; !declared {
					*violations = append(*violations, fmt.Sprintf("%s 出现未声明字段 %s（additionalProperties=false）", path, key))
				}
			}
		}
	}

	if wantType == "array" {
		arr, ok := data.([]any)
		if !ok {
			return
		}
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for i, item := range arr {
				validateNode(itemSchema, item, fmt.Sprintf("%s[%d]", path, i), violations)
			}
		}
	}
}

func typeMatches(wantType string, data any) bool {
	switch wantType {
	case "object":
		_, ok := data.(map[string]any)
		return ok
	case "array":
		_, ok := data.([]any)
		return ok
	case "string":
		_, ok := data.(string)
		return ok
	case "number":
		_, ok := data.(float64)
		return ok
	case "integer":
		f, ok := data.(float64)
		return ok && f == float64(int64(f))
	case "boolean":
		_, ok := data.(bool)
		return ok
	case "null":
		return data == nil
	default:
		return true
	}
}

func goTypeName(data any) string {
	switch data.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", data)
	}
}
