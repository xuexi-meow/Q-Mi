package agent

import (
	memorytools "Q-Mi/agent/tools/memory"
	"Q-Mi/storage"
)

func memoryTools(ctx *ToolContext) []Tool {
	deps := &memorytools.Dependencies{
		Store: ctx.Store, Turn: ctx.Turn, Attention: recentAttention, Call: callNapCat,
	}
	if ctx.Images != nil {
		deps.Images = ctx.Images.inner
	}
	return memorytools.Tools(deps)
}

func queryMessageRows(store *storage.Store, images *imageStore, turn *Turn, filter storage.MessageFilter, keyword ...string) (string, error) {
	var resolver memorytools.ImageResolver
	if images != nil {
		resolver = images.inner
	}
	return memorytools.QueryRows(store, resolver, turn, filter, keyword...)
}
