package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"Q-Mi/app"
	"Q-Mi/bot"
	evmsg "Q-Mi/event/message"
	"Q-Mi/storage"
)

func chatDNDTool(ctx *ToolContext) Tool {
	return Tool{
		Name:        "chat_dnd",
		Description: "自主添加、取消或查看会话勿扰。普通消息不唤醒、不插入思考、不进入最近消息；管理员、@自己、@全体照常进入。记录仍完整存档，可主动搜索。",
		Parameters: objectParams(map[string]any{
			"action":    map[string]any{"type": "string", "enum": []string{"add", "remove", "list"}, "description": "add 开启勿扰，remove 取消，list 查看列表"},
			"chat_type": strProp("add/remove 必填：private / group"),
			"chat_id":   intProp("add/remove 必填：群号或私聊对方 QQ，正数"),
		}, []string{"action"}),
		Usage: "设置持久化、立即生效，可以自己决定管理，无须管理员授权。例：`{\"action\":\"add\",\"chat_type\":\"group\",\"chat_id\":888}`；取消传 remove；查看传 `{\"action\":\"list\"}`。不删除已进入工作上下文的内容，不影响自己的发言记录或后台任务/定时结果。",
		Run: func(args json.RawMessage) (string, error) {
			if ctx.Store == nil {
				return "", fmt.Errorf("记忆库未启用，无法保存或查询勿扰设置")
			}
			var p struct {
				Action   string `json:"action"`
				ChatType string `json:"chat_type"`
				ChatID   int64  `json:"chat_id"`
			}
			if err := decodeArgs(args, &p); err != nil {
				return "", err
			}
			switch p.Action {
			case "list":
				chats, err := ctx.Store.ListChatDND()
				if err != nil {
					return "", err
				}
				return marshalCompact(chats)
			case "add", "remove":
				enabled := p.Action == "add"
				if err := ctx.Store.SetChatDND(p.ChatType, p.ChatID, enabled); err != nil {
					return "", err
				}
				return marshalCompact(map[string]any{
					"chat_type": p.ChatType, "chat_id": p.ChatID, "enabled": enabled,
				})
			default:
				return "", fmt.Errorf("action 必须为 add/remove/list")
			}
		},
	}
}

func recentAttention(turn *Turn) *storage.AttentionFilter {
	filter := &storage.AttentionFilter{}
	if turn != nil {
		filter.SelfID = turn.SelfID
	}
	if cfg := app.GetApp().Current(); cfg != nil && cfg.Bot != nil {
		if filter.SelfID <= 0 {
			filter.SelfID = int64(cfg.Bot.QQ_Number)
		}
		for _, id := range cfg.Bot.Admin_Number {
			filter.AdminIDs = append(filter.AdminIDs, int64(id))
		}
	}
	return filter
}

func mentionedFromEvent(msgs []*evmsg.Message, selfID int64) bool {
	if bot.AtUserID(msgs, selfID) {
		return true
	}
	for _, msg := range msgs {
		if msg == nil || msg.Type != "at" {
			continue
		}
		if msg.Data == nil {
			if _, err := msg.TypeHandler(); err != nil {
				continue
			}
		}
		if at, ok := msg.Data.(evmsg.At); ok && at.QQ == "all" {
			return true
		}
	}
	return false
}

func (m *Manager) acceptsChatMessage(chatType string, chatID, senderID int64, wake string, mentioned bool) bool {
	if m.store == nil || (wake != "" && wake != WakeChat) {
		return true
	}
	muted, err := m.store.IsChatDND(chatType, chatID)
	if err != nil {
		slog.Warn("读取勿扰设置失败，跳过本条聊天消息", "chat_type", chatType, "chat_id", chatID, "err", err)
		return false
	}
	return !muted || mentioned || bot.IsAdmin(senderID)
}
