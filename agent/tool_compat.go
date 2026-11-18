package agent

import "Q-Mi/agent/tool"

// Tool 保留旧包入口，工具契约由 tool 包维护。
type Tool = tool.Tool

func toolParameters(t Tool) any {
	return tool.ParameterSchema(t)
}

func objectParams(properties map[string]any, required []string) map[string]any {
	return tool.ObjectParams(properties, required)
}

func strProp(desc string) map[string]any {
	return tool.StringProp(desc)
}

func intProp(desc string) map[string]any {
	return tool.IntProp(desc)
}

func boolProp(desc string) map[string]any {
	return tool.BoolProp(desc)
}
