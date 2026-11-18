package agent

import "Q-Mi/agent/tools/sticker"

var QQFaceNames = sticker.QQFaceNames

func stickerDependencies(ctx *ToolContext) *sticker.Dependencies {
	deps := &sticker.Dependencies{
		Store: ctx.Store, Turn: ctx.Turn, Call: callNapCat, Send: sendSegments,
	}
	if ctx.Images != nil {
		deps.Images = ctx.Images.inner
	}
	return deps
}

func stickerTools(ctx *ToolContext) []Tool {
	return sticker.Tools(stickerDependencies(ctx))
}

func sendQQFace(chatType string, chatID int64, emojiID string, replyTo int64) (string, error) {
	return (&sticker.Dependencies{Call: callNapCat}).SendQQFace(chatType, chatID, emojiID, replyTo)
}
