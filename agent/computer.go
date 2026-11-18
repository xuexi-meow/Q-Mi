package agent

import (
	"Q-Mi/agent/tools/computer"
)

func computerTools(ctx *ToolContext) []Tool {
	deps := &computer.Dependencies{
		Store: ctx.Store, Workspace: ctx.Workspace, Approved: ctx.Approved,
		Call: callNapCat,
	}
	if ctx.Images != nil {
		deps.Images = ctx.Images.inner
	}
	return computer.Tools(deps)
}
