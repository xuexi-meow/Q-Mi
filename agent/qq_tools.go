package agent

import "Q-Mi/agent/tools/qq"

type segmentParam = qq.SegmentParam

func qqDependencies(ctx *ToolContext) *qq.Dependencies {
	return &qq.Dependencies{
		Store: ctx.Store, Workspace: ctx.Workspace, Admin: ctx.Admin,
		Call: callNapCat, Send: sendSegments, SendFace: sendQQFace,
	}
}

func qqTools(ctx *ToolContext) []Tool {
	return qq.Tools(qqDependencies(ctx))
}

func authorizeNapcatAction(action string, admin bool) error {
	return qq.AuthorizeAction(action, admin)
}
