package tool

// ParameterSchema 返回模型请求和调用校验共用的参数定义。
// 异步工具仅复制 schema 和 properties 两层，避免添加 _async 时污染共享定义。
func ParameterSchema(t Tool) any {
	if !t.Asyncable {
		if t.Parameters == nil {
			return ObjectParams(nil, nil)
		}
		return t.Parameters
	}
	base, _ := t.Parameters.(map[string]any)
	schema := make(map[string]any, len(base)+1)
	for key, value := range base {
		schema[key] = value
	}
	props := map[string]any{}
	if existing, ok := base["properties"].(map[string]any); ok {
		for key, value := range existing {
			props[key] = value
		}
	}
	props["_async"] = BoolProp("可选，true 表示后台执行，完成后自动回填结果")
	schema["type"], schema["properties"] = "object", props
	return schema
}

// ObjectParams 构造对象参数定义。空 properties 编码为 {}，空 required 省略，
// 避免向不接受 null 的模型服务发送无效 schema。
func ObjectParams(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// StringProp 构造带说明的字符串属性。
func StringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// IntProp 构造带说明的整数属性。
func IntProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

// BoolProp 构造带说明的布尔属性。
func BoolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
