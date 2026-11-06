package handler

import (
	"log/slog"
	"strconv"

	"Q-Mi/event"
)

// 消息事件处理:机器人收到的私聊 / 群聊消息

func init() {
	event.RegisterTyped("message.private", func(e *event.PrivateMessageEvent) {
		if e.Sender == nil {
			return
		}

		message := ""
		for _, msg := range e.Message {
			if msg == nil {
				return
			}
			if s, ok := msg.Data.(interface{ Stringf() string }); ok {
				//message.WriteString(s.Stringf())
				message += s.Stringf()
			}
		}
		slog.Info("[私聊][" + e.Sender.Nickname + "]:" + message)
	})

	event.RegisterTyped("message.group", func(e *event.GroupMessageEvent) {
		if e.Sender == nil {
			return
		}
		slog.Info("[" + strconv.Itoa(e.GroupID) + "][" + e.Sender.Nickname + "]:" + e.RawMessage)
	})

	// message_sent(机器人发出的消息)暂时不处理,
	// 需要时按 "message_sent.private" / "message_sent.group" 注册
}
