package agent

import (
	"log/slog"

	"Q-Mi/app"
	"Q-Mi/bot"
	evmsg "Q-Mi/event/message"
	"Q-Mi/storage"
)

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
