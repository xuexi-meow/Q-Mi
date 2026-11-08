// notices.go P3-4 通知唤醒:把 QQ notice/request 事件格式化成一行
// [系统通知] 文本投递给 agent 思考循环,要不要反应由模型自决
// (通知频率远低于聊天消息,全部唤醒的成本可以忽略)。
// 本文件只放纯格式化函数 formatXxx 与入队钩子;事件注册接线在 wiring.go init。
package agent

import (
	"fmt"

	"Q-Mi/event"
)

// NoticeWake 通知事件的格式化结果,直接映射成 IncomingMessage。
// Text 为空表示该通知不唤醒 agent(如 bot_offline,仅由 handler/store.go 归档)。
type NoticeWake struct {
	ChatType   string
	ChatID     int64
	SenderID   int64
	SenderName string
	Text       string
	Urgent     bool
}

// enqueueNotice 通知入队钩子:生产实现复用 wiring 的 getManager() 惰性单例
// (agent 未启用时 getManager 返回 nil,通知静默丢弃);测试可整体替换以捕获唤醒。
var enqueueNotice = func(m IncomingMessage) {
	if mgr := getManager(); mgr != nil {
		mgr.Enqueue(m)
	}
}

// wakeNotice 格式化结果 → IncomingMessage → 入队钩子。
// SelfID/Time 取自事件公共字段;Text 为空的通知(bot_offline)跳过不入队。
func wakeNotice(main event.MainEvent, w NoticeWake) {
	if w.Text == "" {
		return
	}
	enqueueNotice(IncomingMessage{
		Wake:       WakeBackground,
		ChatType:   w.ChatType,
		ChatID:     w.ChatID,
		SelfID:     int64(main.SelfID),
		SenderID:   w.SenderID,
		SenderName: w.SenderName,
		Text:       w.Text,
		Time:       unixTime(main.Time),
		Urgent:     w.Urgent,
	})
}

// actorOr 操作者字段为 0(未知)时回退到当事人 user_id。
func actorOr(operator, user int) int64 {
	if operator != 0 {
		return int64(operator)
	}
	return int64(user)
}

// humanizeBanDuration 禁言秒数转人话(向下取整到最近单位)。
func humanizeBanDuration(sec int) string {
	switch {
	case sec < 60:
		return fmt.Sprintf("%d 秒", sec)
	case sec < 3600:
		return fmt.Sprintf("%d 分钟", sec/60)
	case sec < 86400:
		return fmt.Sprintf("%d 小时", sec/3600)
	default:
		return fmt.Sprintf("%d 天", sec/86400)
	}
}

// ChatKey 规则:群类通知 → group:<group_id>;
// 好友类(friend_add/friend_recall/私聊 poke/profile_like/input_status/request.friend)
// → private:<user_id 或 sender_id>。

func formatGroupUpload(e *event.GroupUploadEvent) NoticeWake {
	return NoticeWake{
		ChatType: "group", ChatID: int64(e.GroupID), SenderID: int64(e.UserID),
		Text: fmt.Sprintf("[系统通知][群文件] %d 上传了 %s", e.UserID, e.File.Name),
	}
}

func formatGroupAdmin(e *event.GroupAdminEvent) NoticeWake {
	var act string
	switch e.SubType {
	case "set":
		act = "被设为管理员"
	case "unset":
		act = "被取消了管理员"
	default:
		return NoticeWake{}
	}
	return NoticeWake{
		ChatType: "group", ChatID: int64(e.GroupID), SenderID: int64(e.UserID),
		Text: fmt.Sprintf("[系统通知][群管理] %d %s", e.UserID, act),
	}
}

func formatGroupDecrease(e *event.GroupDecreaseEvent) NoticeWake {
	w := NoticeWake{ChatType: "group", ChatID: int64(e.GroupID), SenderID: actorOr(e.OperatorID, e.UserID)}
	switch e.SubType {
	case "kick":
		if e.OperatorID != 0 {
			w.Text = fmt.Sprintf("[系统通知][群成员] %d 将 %d 踢出了群", e.OperatorID, e.UserID)
		} else {
			w.Text = fmt.Sprintf("[系统通知][群成员] %d 被踢出了群", e.UserID)
		}
	case "kick_me":
		if e.OperatorID != 0 {
			w.Text = fmt.Sprintf("[系统通知][群成员] %d 把你踢出了群", e.OperatorID)
		} else {
			w.Text = "[系统通知][群成员] 你被踢出了群"
		}
	case "leave":
		w.Text = fmt.Sprintf("[系统通知][群成员] %d 退出了群", e.UserID)
	case "disband":
		if e.OperatorID != 0 {
			w.Text = fmt.Sprintf("[系统通知][群成员] %d 解散了群", e.OperatorID)
		} else {
			w.Text = "[系统通知][群成员] 群已解散"
		}
	default:
		return NoticeWake{}
	}
	return w
}

func formatGroupIncrease(e *event.GroupIncreaseEvent) NoticeWake {
	w := NoticeWake{ChatType: "group", ChatID: int64(e.GroupID), SenderID: int64(e.UserID)}
	switch e.SubType {
	case "approve":
		w.Text = fmt.Sprintf("[系统通知][群成员] %d 加入了群", e.UserID)
	case "invite":
		w.Text = fmt.Sprintf("[系统通知][群成员] %d 受邀加入了群", e.UserID)
	default:
		return NoticeWake{}
	}
	return w
}

func formatGroupBan(e *event.GroupBanEvent) NoticeWake {
	w := NoticeWake{ChatType: "group", ChatID: int64(e.GroupID), SenderID: actorOr(e.OperatorID, e.UserID)}
	switch e.SubType {
	case "ban":
		if e.UserID == e.SelfID {
			w.Text = fmt.Sprintf("[系统通知][禁言] 你被禁言 %s", humanizeBanDuration(e.Duration))
		} else {
			w.Text = fmt.Sprintf("[系统通知][禁言] %d 被禁言 %s", e.UserID, humanizeBanDuration(e.Duration))
		}
	case "lift_ban":
		if e.UserID == e.SelfID {
			w.Text = "[系统通知][禁言] 你被解除了禁言"
		} else {
			w.Text = fmt.Sprintf("[系统通知][禁言] %d 被解除了禁言", e.UserID)
		}
	default:
		return NoticeWake{}
	}
	return w
}

func formatFriendAdd(e *event.FriendAddEvent) NoticeWake {
	return NoticeWake{
		ChatType: "private", ChatID: int64(e.UserID), SenderID: int64(e.UserID),
		Text: fmt.Sprintf("[系统通知][好友] %d 添加了你为好友", e.UserID),
	}
}

func formatGroupRecall(e *event.GroupRecallEvent) NoticeWake {
	return NoticeWake{
		ChatType: "group", ChatID: int64(e.GroupID), SenderID: int64(e.UserID),
		Text: fmt.Sprintf("[系统通知][撤回] %d 撤回了一条消息", e.UserID),
	}
}

func formatFriendRecall(e *event.FriendRecallEvent) NoticeWake {
	return NoticeWake{
		ChatType: "private", ChatID: int64(e.UserID), SenderID: int64(e.UserID),
		Text: fmt.Sprintf("[系统通知][撤回] %d 撤回了一条消息", e.UserID),
	}
}

func formatGroupCard(e *event.GroupCardEvent) NoticeWake {
	return NoticeWake{
		ChatType: "group", ChatID: int64(e.GroupID), SenderID: int64(e.UserID),
		Text: fmt.Sprintf("[系统通知][群名片] %d 名片变更为 %s", e.UserID, e.CardNew),
	}
}

func formatGroupEssence(e *event.GroupEssenceEvent) NoticeWake {
	op := actorOr(e.OperatorID, e.UserID)
	w := NoticeWake{ChatType: "group", ChatID: int64(e.GroupID), SenderID: op}
	switch e.SubType {
	case "add":
		w.Text = fmt.Sprintf("[系统通知][精华] %d 把一条消息设为了精华", op)
	case "delete":
		w.Text = fmt.Sprintf("[系统通知][精华] %d 把一条消息移出了精华", op)
	default:
		return NoticeWake{}
	}
	return w
}

func formatGroupEmojiLike(e *event.GroupMsgEmojiLikeEvent) NoticeWake {
	return NoticeWake{
		ChatType: "group", ChatID: int64(e.GroupID), SenderID: int64(e.UserID),
		Text: fmt.Sprintf("[系统通知][表情回应] %d 给消息 %d 添加了表情回应", e.UserID, e.MessageID),
	}
}

func formatGroupName(e *event.GroupNameEvent) NoticeWake {
	return NoticeWake{
		ChatType: "group", ChatID: int64(e.GroupID), SenderID: int64(e.UserID),
		Text: fmt.Sprintf("[系统通知][群名] %d 将群名改为 %s", e.UserID, e.NameNew),
	}
}

func formatGroupTitle(e *event.GroupTitleEvent) NoticeWake {
	return NoticeWake{
		ChatType: "group", ChatID: int64(e.GroupID), SenderID: int64(e.UserID),
		Text: fmt.Sprintf("[系统通知][头衔] %d 获得头衔 %s", e.UserID, e.Title),
	}
}

// 戳一戳:同一 key "notice.notify.poke" 下注册群/好友两个变体
// (wiring 里各按标记字段 group_id / sender_id 是否为 0 决定是否处理),
// raw_info 不展开,文本给 user/target 即可;戳一戳可回戳,判定 urgent。
func formatGroupPoke(e *event.GroupPokeEvent) NoticeWake {
	return NoticeWake{
		ChatType: "group", ChatID: int64(e.GroupID), SenderID: int64(e.UserID),
		Text:   fmt.Sprintf("[系统通知][戳一戳] %d 戳了戳 %d", e.UserID, e.TargetID),
		Urgent: true,
	}
}

func formatFriendPoke(e *event.FriendPokeEvent) NoticeWake {
	chat := int64(e.SenderID)
	if chat == 0 {
		chat = int64(e.UserID)
	}
	return NoticeWake{
		ChatType: "private", ChatID: chat, SenderID: int64(e.UserID),
		Text:   fmt.Sprintf("[系统通知][戳一戳] %d 戳了戳 %d", e.UserID, e.TargetID),
		Urgent: true,
	}
}

func formatProfileLike(e *event.ProfileLikeEvent) NoticeWake {
	who := fmt.Sprintf("%d", e.OperatorID)
	name := ""
	if e.OperatorNick != "" {
		name = e.OperatorNick
		who = fmt.Sprintf("%s(%d)", e.OperatorNick, e.OperatorID)
	}
	return NoticeWake{
		ChatType: "private", ChatID: int64(e.OperatorID), SenderID: int64(e.OperatorID), SenderName: name,
		Text: fmt.Sprintf("[系统通知][点赞] %s 给你点了 %d 个赞", who, e.Times),
	}
}

func formatInputStatus(e *event.InputStatusEvent) NoticeWake {
	st := e.StatusText
	if st == "" {
		st = "正在输入"
	}
	return NoticeWake{
		ChatType: "private", ChatID: int64(e.UserID), SenderID: int64(e.UserID),
		Text: fmt.Sprintf("[系统通知][输入状态] %d %s", e.UserID, st),
	}
}

// request.friend/request.group:flag 必须完整带上,模型要原样传给
// handle_friend_request / handle_group_request;需要尽快处理,判定 urgent。
func formatFriendRequest(e *event.OB11FriendRequestEvent) NoticeWake {
	return NoticeWake{
		ChatType: "private", ChatID: int64(e.UserID), SenderID: int64(e.UserID),
		Text:   fmt.Sprintf("[系统通知][好友申请] QQ:%d 验证消息:%s (flag=%s)", e.UserID, e.Comment, e.Flag),
		Urgent: true,
	}
}

func formatGroupRequest(e *event.OB11GroupRequestEvent) NoticeWake {
	return NoticeWake{
		ChatType: "group", ChatID: int64(e.GroupID), SenderID: int64(e.UserID),
		Text: fmt.Sprintf(
			"[系统通知][加群申请] QQ:%d 验证消息:%s (flag=%s, sub_type=%s, group_id=%d)",
			e.UserID, e.Comment, e.Flag, e.SubType, e.GroupID,
		),
		Urgent: true,
	}
}

// formatBotOffline 机器人离线通知不唤醒 agent,返回零值由 wakeNotice 跳过
// (原始 JSON 仍由 handler/store.go 归档)。
func formatBotOffline(*event.BotOfflineEvent) NoticeWake {
	return NoticeWake{}
}
