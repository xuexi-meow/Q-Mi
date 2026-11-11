package agent

import (
	"encoding/json"
	"fmt"

	"Q-Mi/storage"
)

func getMessageTool(ctx *ToolContext) Tool {
	return Tool{
		Name:        "get_message",
		Description: "按消息 ID 精确查询同一会话的原消息，核对引用、发送者和 @ 对象。必须指定原会话；查不到不代表引用的是自己。",
		Parameters: objectParams(map[string]any{
			"chat_type":  strProp("原消息会话类型：group / private"),
			"chat_id":    intProp("原消息所在群号；私聊为对方 QQ"),
			"message_id": intProp("原消息 ID，例如 reply.message_id"),
		}, []string{"chat_type", "chat_id", "message_id"}),
		Usage: "例：`{\"chat_type\":\"group\",\"chat_id\":888,\"message_id\":42}`。只读本地存档；缺失时可用 get_msg_history 补查同一会话，不借用其他群的背景。",
		Run: func(args json.RawMessage) (string, error) {
			var p struct {
				ChatType  string `json:"chat_type"`
				ChatID    int64  `json:"chat_id"`
				MessageID int64  `json:"message_id"`
			}
			if err := decodeArgs(args, &p); err != nil {
				return "", err
			}
			if (p.ChatType != "group" && p.ChatType != "private") || p.ChatID <= 0 || p.MessageID == 0 {
				return "", fmt.Errorf("需要有效的 chat_type、chat_id 和 message_id")
			}
			if ctx.Store == nil {
				return "", fmt.Errorf("记忆库未启用；可用 get_msg_history 补查原会话")
			}
			record, err := ctx.Store.GetMessage(p.MessageID)
			if err != nil {
				return "", fmt.Errorf("查询消息失败: %w", err)
			}
			if record == nil || record.ChatType != p.ChatType || record.ChatID != p.ChatID {
				return "", fmt.Errorf("在 %s:%d 的本地存档中未找到消息 %d；可用 get_msg_history 补查该会话，不要猜测引用对象", p.ChatType, p.ChatID, p.MessageID)
			}
			if record.Recalled {
				return "", fmt.Errorf("消息 %d 已撤回，不能作为当前可见原文", p.MessageID)
			}
			var selfID int64
			if ctx.Turn != nil {
				selfID = ctx.Turn.SelfID
			}
			return marshalCompact(messageView(ctx.Store, selfID, recordTurnMessage(record), "archive"))
		},
	}
}

func validateReplyScope(store *storage.Store, chatType string, chatID int64, segments []segmentParam) error {
	if store == nil {
		return nil
	}
	for index, segment := range segments {
		if segment.Type != "reply" {
			continue
		}
		id, err := segID(segment.Data, "id", index)
		if err != nil {
			return err
		}
		record, err := store.GetMessage(id)
		if err != nil {
			return fmt.Errorf("无法核对引用消息 %d: %w", id, err)
		}
		if record != nil && (record.ChatType != chatType || record.ChatID != chatID) {
			return fmt.Errorf("引用消息 %d 不属于发送目标 %s:%d，请核对会话，不能跨会话引用", id, chatType, chatID)
		}
	}
	return nil
}
