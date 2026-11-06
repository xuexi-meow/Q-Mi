package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"Q-Mi/event"
)

// 通知事件处理:群管理、好友、撤回、戳一戳等

func init() {
	event.RegisterTyped("notice.group_upload", func(e *event.GroupUploadEvent) {
		slog.Info("[群文件][" + strconv.Itoa(e.GroupID) + "][" + strconv.Itoa(e.UserID) + "]:" + e.File.Name)
	})

	event.RegisterTyped("notice.group_admin", func(e *event.GroupAdminEvent) {
		switch e.SubType {
		case "set":
			slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "][" + strconv.Itoa(e.UserID) + "]被设置为管理员")
		case "unset":
			slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "][" + strconv.Itoa(e.UserID) + "]被取消管理员")
		}
	})

	event.RegisterTyped("notice.group_decrease", func(e *event.GroupDecreaseEvent) {
		switch e.SubType {
		case "kick":
			slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "][" + strconv.Itoa(e.UserID) + "]被[" + strconv.Itoa(e.OperatorID) + "]踢出群聊")
		case "leave":
			slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "][" + strconv.Itoa(e.UserID) + "]退出了群聊")
		case "kick_me":
			slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "]我被[" + strconv.Itoa(e.OperatorID) + "]踢出群聊")
		case "disband":
			slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "]群聊已解散")
		}
	})

	event.RegisterTyped("notice.group_increase", func(e *event.GroupIncreaseEvent) {
		switch e.SubType {
		case "approve":
			slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "][" + strconv.Itoa(e.UserID) + "]被[" + strconv.Itoa(e.OperatorID) + "]批准入群")
		case "invite":
			slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "][" + strconv.Itoa(e.UserID) + "]被[" + strconv.Itoa(e.OperatorID) + "]邀请入群")
		}
	})

	event.RegisterTyped("notice.group_ban", func(e *event.GroupBanEvent) {
		switch e.SubType {
		case "ban":
			slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "][" + strconv.Itoa(e.UserID) + "]被[" + strconv.Itoa(e.OperatorID) + "]禁言" + strconv.Itoa(e.Duration) + "秒")
		case "lift_ban":
			slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "][" + strconv.Itoa(e.UserID) + "]被[" + strconv.Itoa(e.OperatorID) + "]解除禁言")
		}
	})

	event.RegisterTyped("notice.friend_add", func(e *event.FriendAddEvent) {
		slog.Info("[好友][" + strconv.Itoa(e.UserID) + "]添加了我为好友")
	})

	event.RegisterTyped("notice.group_recall", func(e *event.GroupRecallEvent) {
		slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "] [" + strconv.Itoa(e.UserID) + "]撤回了一条消息")
	})

	event.RegisterTyped("notice.friend_recall", func(e *event.FriendRecallEvent) {
		slog.Info("[好友][" + strconv.Itoa(e.UserID) + "]撤回了一条消息")
	})

	event.RegisterTyped("notice.group_card", func(e *event.GroupCardEvent) {
		slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "]名片变更为[" + e.CardNew + "]")
	})

	event.RegisterTyped("notice.notify.group_name", func(e *event.GroupNameEvent) {
		slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "]群名变更为[" + e.NameNew + "]")
	})

	event.RegisterTyped("notice.notify.group_title", func(e *event.GroupTitleEvent) {
		slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "] [" + strconv.Itoa(e.UserID) + "]头衔变更为[" + e.Title + "]")
	})

	event.RegisterTyped("notice.group_essence", func(e *event.GroupEssenceEvent) {
		switch e.SubType {
		case "add":
			slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "] [" + strconv.Itoa(e.SenderID) + "]的消息被[" + strconv.Itoa(e.OperatorID) + "]设置为精华消息")
		case "delete":
			slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "] [" + strconv.Itoa(e.SenderID) + "]的消息被[" + strconv.Itoa(e.OperatorID) + "]取消精华消息")
		}
	})

	event.RegisterTyped("notice.group_msg_emoji_like", func(e *event.GroupMsgEmojiLikeEvent) {
		// TODO: 表情回应业务
	})

	// 戳一戳:群戳和好友戳用同一 key,靠字段区分
	event.Register("notice.notify.poke", func(raw json.RawMessage) {
		var rawMap map[string]any
		if err := json.Unmarshal(raw, &rawMap); err != nil {
			return
		}
		if _, ok := rawMap["group_id"]; ok {
			var e event.GroupPokeEvent
			json.Unmarshal(raw, &e)
			handleGroupPoke(&e)
			return
		}
		if _, ok := rawMap["sender_id"]; ok {
			var e event.FriendPokeEvent
			json.Unmarshal(raw, &e)
			handleFriendPoke(&e)
		}
	})

	event.RegisterTyped("notice.notify.profile_like", func(e *event.ProfileLikeEvent) {
		// TODO: 资料点赞业务
	})

	event.RegisterTyped("notice.notify.input_status", func(e *event.InputStatusEvent) {
		// TODO: 输入状态业务
	})

	event.RegisterTyped("notice.bot_offline", func(e *event.BotOfflineEvent) {
		// TODO: 掉线告警
	})

	// 群灰条暂不注册,需要时按 "notice.notify.gray_tip" 注册
}

func handleFriendPoke(e *event.FriendPokeEvent) {
	if msg, ok := e.RawInfo.(string); ok {
		slog.Info("[好友][" + strconv.Itoa(e.SenderID) + "]" + msg)
		return
	}
	if rawItems, ok := e.RawInfo.([]any); ok {
		txt1, txt2 := pokeTexts(rawItems)
		slog.Info("[好友][" + strconv.Itoa(e.SenderID) + "]" + txt1 + "[" + strconv.Itoa(e.TargetID) + "]" + txt2)
		return
	}
	slog.Info(fmt.Sprintf("%T", e.RawInfo))
	slog.Info(fmt.Sprintf("%v", e.RawInfo))
}

func handleGroupPoke(e *event.GroupPokeEvent) {
	if msg, ok := e.RawInfo.(string); ok {
		slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "][" + strconv.Itoa(e.UserID) + "]" + msg)
		return
	}
	if rawItems, ok := e.RawInfo.([]any); ok {
		txt1, txt2 := pokeTexts(rawItems)
		slog.Info("[群聊][" + strconv.Itoa(e.GroupID) + "][" + strconv.Itoa(e.UserID) + "]" + txt1 + "[" + strconv.Itoa(e.TargetID) + "]" + txt2)
		return
	}
	slog.Info(fmt.Sprintf("%T", e.RawInfo))
}

/*
raw_info 的数据结构大概是这样的
[
  {
    "type": "qq",
    "col": 1,
    "nm": "",
    "uid": "u_DFRxr4xviXg-yKRBXiRyBg"
  },
  {
    "type": "img",
    "jp": "https://zb.vip.qq.com/v2/pages/nudgeMall?_wv=2&actionId=0",
    "src": "http://tianquan.gtimg.cn/nudgeaction/item/0/expression.jpg"
  },
  {
    "type": "nor",
    "txt": "戳了戳"
  },
  {
    "type": "qq",
    "col": 1,
    "nm": "",
    "tp": 0,
    "uid": "u_Oma5ZPG44vuFZbf6Wl7O4Q"
  },
  {
    "type": "nor",
    "txt": ""
  }
]
*/

// pokeTexts 从 raw_info 数组里取出两个 "nor" 文本(第 3 个和第 5 个元素)
func pokeTexts(rawItems []any) (txt1, txt2 string) {
	for n, item := range rawItems {
		obj, ok := item.(map[string]any)
		if !ok || obj["type"] != "nor" {
			continue
		}
		if txt, ok := obj["txt"].(string); ok {
			if n == 2 {
				txt1 = txt
			}
			if n == 4 {
				txt2 = txt
			}
		}
	}
	return
}
