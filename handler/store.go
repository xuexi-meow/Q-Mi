// store.go 事件落库:收发消息、撤回标记、其余 notice 原始 JSON 存档。
// Store 在 init 时可能尚未打开(main 在 handler init 之后才 Open),
// 所以每个 handler 都在事件到来时才从 app 取 Store,nil 即跳过。
package handler

import (
	"encoding/json"
	"log/slog"

	"Q-Mi/app"
	"Q-Mi/event"
	"Q-Mi/storage"
)

func init() {
	event.RegisterTyped("message.private", func(e *event.PrivateMessageEvent) {
		putMessage(privateRecord(e))
	})
	event.RegisterTyped("message.group", func(e *event.GroupMessageEvent) {
		putMessage(groupRecord(e))
	})
	event.RegisterTyped("message_sent.private", func(e *event.MessageSentEvent) {
		putMessage(sentRecord(e, "private"))
	})
	event.RegisterTyped("message_sent.group", func(e *event.MessageSentEvent) {
		putMessage(sentRecord(e, "group"))
	})

	event.RegisterTyped("notice.group_recall", func(e *event.GroupRecallEvent) {
		st := app.GetApp().Store
		if st == nil {
			return
		}
		if err := st.MarkRecalled(int64(e.MessageID)); err != nil {
			slog.Warn("撤回标记失败", "message_id", e.MessageID, "err", err)
		}
	})
	event.RegisterTyped("notice.friend_recall", func(e *event.FriendRecallEvent) {
		st := app.GetApp().Store
		if st == nil {
			return
		}
		if err := st.MarkRecalled(int64(e.MessageID)); err != nil {
			slog.Warn("撤回标记失败", "message_id", e.MessageID, "err", err)
		}
	})

	for _, key := range noticeStoreKeys {
		k := key
		event.Register(k, func(raw json.RawMessage) {
			storeNotice(k, raw)
		})
	}
}

// noticeStoreKeys 全部落库的 notice key(与 dispatcher 的 key 规则一致;
// 撤回两类不在此列,它们走 MarkRecalled)
var noticeStoreKeys = []string{
	"notice.group_upload", "notice.group_admin", "notice.group_decrease",
	"notice.group_increase", "notice.group_ban", "notice.friend_add",
	"notice.group_card", "notice.group_essence", "notice.group_msg_emoji_like",
	"notice.notify.group_name", "notice.notify.group_title",
	"notice.notify.poke", "notice.notify.profile_like", "notice.notify.input_status",
	"notice.bot_offline",
}

func putMessage(r *storage.MessageRecord) {
	st := app.GetApp().Store
	if st == nil {
		return
	}
	if err := st.PutMessage(r); err != nil {
		slog.Warn("消息落库失败", "message_id", r.MessageID, "err", err)
	}
}

func privateRecord(e *event.PrivateMessageEvent) *storage.MessageRecord {
	segs, _ := json.Marshal(e.Message) // Message.Parsed 是 json.RawMessage,重编即原始段 JSON
	return &storage.MessageRecord{
		MessageID: int64(e.MessageID), Direction: "recv", ChatType: "private",
		ChatID: int64(e.UserID), UserID: int64(e.UserID),
		RawMessage: e.RawMessage, Segments: segs, Time: e.Time,
	}
}

func groupRecord(e *event.GroupMessageEvent) *storage.MessageRecord {
	segs, _ := json.Marshal(e.Message)
	return &storage.MessageRecord{
		MessageID: int64(e.MessageID), Direction: "recv", ChatType: "group",
		ChatID: int64(e.GroupID), UserID: int64(e.UserID),
		RawMessage: e.RawMessage, Segments: segs, Time: e.Time,
	}
}

func sentRecord(e *event.MessageSentEvent, chatType string) *storage.MessageRecord {
	// TODO(真机核对): 连上 NapCat 发一条群聊/私聊消息,核对后删除此日志
	slog.Info("message_sent 原始数据", "user_id", e.UserID, "target_id", e.TargetID, "group_id", e.GroupID)
	segs, _ := json.Marshal(e.Message)
	r := &storage.MessageRecord{
		MessageID: int64(e.MessageID), Direction: "sent", ChatType: chatType,
		UserID: int64(e.UserID), // 发出消息 user_id = 机器人自身
		RawMessage: e.RawMessage, Segments: segs, Time: e.Time,
	}
	if chatType == "group" {
		r.ChatID = int64(e.GroupID)
	} else {
		r.ChatID = int64(e.TargetID) // 私聊 chat_id = 对方 QQ
	}
	return r
}

func storeNotice(key string, raw json.RawMessage) {
	st := app.GetApp().Store
	if st == nil {
		return
	}
	var head struct {
		NoticeType string  `json:"notice_type"`
		SubType    string  `json:"sub_type"`
		GroupID    int     `json:"group_id"`
		UserID     int     `json:"user_id"`
		Time       float64 `json:"time"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		slog.Warn("notice 解析失败,跳过落库", "key", key, "err", err)
		return
	}
	n := &storage.NoticeRecord{
		NoticeType: head.NoticeType, SubType: head.SubType,
		ChatID: int64(head.GroupID), UserID: int64(head.UserID),
		Raw: raw, Time: head.Time,
	}
	if err := st.PutNotice(n); err != nil {
		slog.Warn("notice 落库失败", "key", key, "err", err)
	}
}
