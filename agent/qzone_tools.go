package agent

import qzonetools "Q-Mi/agent/tools/qzone"

func qzoneTools(ctx *ToolContext) []Tool {
	deps := &qzonetools.Dependencies{
		Turn: ctx.Turn, Admin: ctx.Admin, Call: callNapCat, Authorize: authorizeNapcatAction,
	}
	if ctx.Images != nil {
		deps.Images = ctx.Images.inner
	}
	return qzonetools.Tools(deps)
}
