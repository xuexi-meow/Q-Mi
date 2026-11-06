package handler

import (
	"context"

	"Q-Mi/bot"
	"Q-Mi/chain"
	"Q-Mi/event"
)

// 命令入口:bot.RegisterCommand 定义命令,这里统一匹配并分发;
// 新命令在 init 里注册,在 runCommand 里加 case,业务大了再拆文件。

func init() {
	bot.RegisterCommand(bot.Command{Name: "ping", Prefixed: true})

	event.RegisterTyped("message.private", func(e *event.PrivateMessageEvent) {
		runCommand(bot.Text(e.Message), "private", int64(e.UserID), int64(e.UserID))
	})
	event.RegisterTyped("message.group", func(e *event.GroupMessageEvent) {
		runCommand(bot.Text(e.Message), "group", int64(e.GroupID), int64(e.UserID))
	})
}

func runCommand(text, chatType string, chatID, senderID int64) {
	cmd, args := bot.MatchCommand(text)
	if cmd == nil {
		return
	}
	if cmd.Admin && !bot.IsAdmin(senderID) {
		return
	}
	switch cmd.Name {
	case "ping":
		_ = bot.Reply(context.Background(), chatType, chatID, chain.New().Text("pong"))
	}
	_ = args // 供后续需要参数的命令使用
}
