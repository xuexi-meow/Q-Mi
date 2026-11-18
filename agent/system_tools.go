package agent

import systemtools "Q-Mi/agent/tools/system"

func systemTools(ctx *ToolContext) []Tool {
	deps := &systemtools.Dependencies{Turn: ctx.Turn}
	// 避免将 nil 具体指针装进非 nil 接口。
	if ctx.Timer != nil {
		deps.Timer = ctx.Timer
	}
	if ctx.Tasks != nil {
		deps.Tasks = ctx.Tasks
	}
	if ctx.Events != nil {
		deps.Events = ctx.Events
	}
	return systemtools.Tools(deps)
}
