package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

func responseProtocolError(resp *ChatResponse) string {
	if reason := resp.FinishReason(); reason == "length" || reason == "content_filter" {
		return "response incomplete: finish_reason=" + reason
	}
	calls := resp.ToolCalls()
	if len(calls) == 0 {
		return "missing tool_calls"
	}
	ids := make(map[string]bool, len(calls))
	for _, call := range calls {
		if call.ID == "" || ids[call.ID] || call.Type != "function" || call.Function.Name == "" {
			return "tool_calls contains missing/duplicate id, invalid type or empty function name"
		}
		ids[call.ID] = true
	}
	return ""
}

func validateToolArguments(tool Tool, raw string) (json.RawMessage, error) {
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
	parameters := toolParameters(tool)
	if parameters == nil {
		parameters = objectParams(nil, nil)
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

// Keep complete, bounded JSON instead of cutting a JSON document mid-string.
func truncateToolResult(result string) string {
	if len([]rune(result)) <= maxContentRunes {
		return result
	}
	encoded, _ := marshalCompact(map[string]any{
		"truncated": true, "original_runes": len([]rune(result)),
		"preview": truncateRunes(result, maxContentRunes/8),
		"note":    "仅为结果预览，不是完整数据；按相关会话、消息 ID 或更小范围重新查询。",
	})
	return encoded
}
