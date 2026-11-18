package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// ValidateArguments 使用与 API 定义相同的 schema 校验，不解析远程引用。
func ValidateArguments(tool Tool, raw string) (json.RawMessage, error) {
	if raw == "" {
		raw = "{}"
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("%s 参数不是合法 JSON: %w", tool.Name, err)
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, fmt.Errorf("%s arguments 必须是 JSON 对象，不能是 null、数组或字符串", tool.Name)
	}
	parameters := ParameterSchema(tool)
	if parameters == nil {
		parameters = ObjectParams(nil, nil)
	}
	data, err := json.Marshal(parameters)
	if err != nil {
		return nil, fmt.Errorf("%s 参数 schema 不可用: %w", tool.Name, err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.LoadURL = func(string) (io.ReadCloser, error) {
		return nil, fmt.Errorf("remote tool schemas are disabled")
	}
	const url = "https://q-mi.invalid/tool-arguments"
	if err := compiler.AddResource(url, bytes.NewReader(data)); err != nil {
		return nil, err
	}
	schema, err := compiler.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("%s 参数 schema 不可用: %w", tool.Name, err)
	}
	if err := schema.Validate(value); err != nil {
		return nil, fmt.Errorf("%s 参数校验失败: %w", tool.Name, err)
	}
	return json.RawMessage(raw), nil
}
