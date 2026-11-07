package agent

import (
	"strings"
	"sync"
	"testing"

	"Q-Mi/event"
	efile "Q-Mi/event/file"
)

// ---------- P3-4 通知唤醒:格式化函数单测(纯函数,直接断言 NoticeWake) ----------

// wantWake 断言 NoticeWake 的 ChatKey(ChatType:ChatID)、文本与紧急度。
func wantWake(t *testing.T, w NoticeWake, chatType string, chatID int64, text string, urgent bool) {
	t.Helper()
	if w.ChatType != chatType || w.ChatID != chatID {
		t.Errorf("ChatKey = %s:%d, want %s:%d", w.ChatType, w.ChatID, chatType, chatID)
	}
	if w.Text != text {
		t.Errorf("Text = %q, want %q", w.Text, text)
	}
	if w.Urgent != urgent {
		t.Errorf("Urgent = %v, want %v", w.Urgent, urgent)
	}
}

func TestFormatGroupUpload(t *testing.T) {
	w := formatGroupUpload(&event.GroupUploadEvent{
		GroupID: 100, UserID: 5,
		File: efile.File{Name: "报告.pdf"},
	})
	wantWake(t, w, "group", 100, "[系统通知][群文件] 5 上传了 报告.pdf", false)
}

func TestFormatGroupAdmin(t *testing.T) {
	set := formatGroupAdmin(&event.GroupAdminEvent{SubType: "set", GroupID: 100, UserID: 123})
	wantWake(t, set, "group", 100, "[系统通知][群管理] 123 被设为管理员", false)

	unset := formatGroupAdmin(&event.GroupAdminEvent{SubType: "unset", GroupID: 100, UserID: 123})
	wantWake(t, unset, "group", 100, "[系统通知][群管理] 123 被取消了管理员", false)
}

// kick 带 operator(operator 未知时降级为"被踢出");kick_me 用"你";disband 同理。
func TestFormatGroupDecrease(t *testing.T) {
	kick := formatGroupDecrease(&event.GroupDecreaseEvent{SubType: "kick", GroupID: 100, OperatorID: 666, UserID: 123})
	wantWake(t, kick, "group", 100, "[系统通知][群成员] 666 将 123 踢出了群", false)

	kickNoOp := formatGroupDecrease(&event.GroupDecreaseEvent{SubType: "kick", GroupID: 100, UserID: 123})
	wantWake(t, kickNoOp, "group", 100, "[系统通知][群成员] 123 被踢出了群", false)

	kickMe := formatGroupDecrease(&event.GroupDecreaseEvent{SubType: "kick_me", GroupID: 100, OperatorID: 666, UserID: 999})
	wantWake(t, kickMe, "group", 100, "[系统通知][群成员] 666 把你踢出了群", false)

	kickMeNoOp := formatGroupDecrease(&event.GroupDecreaseEvent{SubType: "kick_me", GroupID: 100, UserID: 999})
	wantWake(t, kickMeNoOp, "group", 100, "[系统通知][群成员] 你被踢出了群", false)

	leave := formatGroupDecrease(&event.GroupDecreaseEvent{SubType: "leave", GroupID: 100, UserID: 123})
	wantWake(t, leave, "group", 100, "[系统通知][群成员] 123 退出了群", false)

	disband := formatGroupDecrease(&event.GroupDecreaseEvent{SubType: "disband", GroupID: 100, OperatorID: 666, UserID: 666})
	wantWake(t, disband, "group", 100, "[系统通知][群成员] 666 解散了群", false)

	disbandNoOp := formatGroupDecrease(&event.GroupDecreaseEvent{SubType: "disband", GroupID: 100})
	wantWake(t, disbandNoOp, "group", 100, "[系统通知][群成员] 群已解散", false)
}

func TestFormatGroupIncrease(t *testing.T) {
	approve := formatGroupIncrease(&event.GroupIncreaseEvent{SubType: "approve", GroupID: 100, UserID: 123})
	wantWake(t, approve, "group", 100, "[系统通知][群成员] 123 加入了群", false)

	invite := formatGroupIncrease(&event.GroupIncreaseEvent{SubType: "invite", GroupID: 100, UserID: 123})
	wantWake(t, invite, "group", 100, "[系统通知][群成员] 123 受邀加入了群", false)
}

// ban 带人话时长;user_id==self 时注明"你"。
func TestFormatGroupBan(t *testing.T) {
	banSelf := &event.GroupBanEvent{SubType: "ban", GroupID: 100, UserID: 999, Duration: 600}
	banSelf.SelfID = 999
	wantWake(t, formatGroupBan(banSelf), "group", 100, "[系统通知][禁言] 你被禁言 10 分钟", false)

	banOther := formatGroupBan(&event.GroupBanEvent{SubType: "ban", GroupID: 100, UserID: 123, Duration: 86400})
	wantWake(t, banOther, "group", 100, "[系统通知][禁言] 123 被禁言 1 天", false)

	liftSelf := &event.GroupBanEvent{SubType: "lift_ban", GroupID: 100, UserID: 999}
	liftSelf.SelfID = 999
	wantWake(t, formatGroupBan(liftSelf), "group", 100, "[系统通知][禁言] 你被解除了禁言", false)

	liftOther := formatGroupBan(&event.GroupBanEvent{SubType: "lift_ban", GroupID: 100, UserID: 123})
	wantWake(t, liftOther, "group", 100, "[系统通知][禁言] 123 被解除了禁言", false)
}

func TestHumanizeBanDuration(t *testing.T) {
	cases := map[int]string{
		0: "0 秒", 59: "59 秒", 60: "1 分钟", 600: "10 分钟", 3599: "59 分钟",
		3600: "1 小时", 86399: "23 小时", 86400: "1 天", 2592000: "30 天",
	}
	for sec, want := range cases {
		if got := humanizeBanDuration(sec); got != want {
			t.Errorf("humanizeBanDuration(%d) = %q, want %q", sec, got, want)
		}
	}
}

func TestFormatFriendAdd(t *testing.T) {
	wantWake(t, formatFriendAdd(&event.FriendAddEvent{UserID: 123}),
		"private", 123, "[系统通知][好友] 123 添加了你为好友", false)
}

// 撤回:群撤回走群 ChatKey,好友撤回走私聊 ChatKey,文本一致。
func TestFormatRecall(t *testing.T) {
	g := formatGroupRecall(&event.GroupRecallEvent{GroupID: 100, UserID: 123, OperatorID: 666, MessageID: 7})
	wantWake(t, g, "group", 100, "[系统通知][撤回] 123 撤回了一条消息", false)

	f := formatFriendRecall(&event.FriendRecallEvent{UserID: 123, MessageID: 8})
	wantWake(t, f, "private", 123, "[系统通知][撤回] 123 撤回了一条消息", false)
}

func TestFormatGroupCard(t *testing.T) {
	wantWake(t, formatGroupCard(&event.GroupCardEvent{GroupID: 100, UserID: 123, CardNew: "新名片", CardOld: "旧名片"}),
		"group", 100, "[系统通知][群名片] 123 名片变更为 新名片", false)
}

func TestFormatGroupEssence(t *testing.T) {
	add := formatGroupEssence(&event.GroupEssenceEvent{SubType: "add", GroupID: 100, OperatorID: 666, UserID: 666})
	wantWake(t, add, "group", 100, "[系统通知][精华] 666 把一条消息设为了精华", false)

	del := formatGroupEssence(&event.GroupEssenceEvent{SubType: "delete", GroupID: 100, OperatorID: 666, UserID: 666})
	wantWake(t, del, "group", 100, "[系统通知][精华] 666 把一条消息移出了精华", false)
}

func TestFormatGroupEmojiLike(t *testing.T) {
	wantWake(t, formatGroupEmojiLike(&event.GroupMsgEmojiLikeEvent{GroupID: 100, UserID: 123, MessageID: 42}),
		"group", 100, "[系统通知][表情回应] 123 给消息 42 添加了表情回应", false)
}

func TestFormatGroupNameAndTitle(t *testing.T) {
	name := formatGroupName(&event.GroupNameEvent{GroupID: 100, UserID: 123, NameNew: "新群名"})
	wantWake(t, name, "group", 100, "[系统通知][群名] 123 将群名改为 新群名", false)

	title := formatGroupTitle(&event.GroupTitleEvent{GroupID: 100, UserID: 123, Title: "大佬"})
	wantWake(t, title, "group", 100, "[系统通知][头衔] 123 获得头衔 大佬", false)
}

// 戳一戳:群戳走群 ChatKey、好友戳走私聊 ChatKey,均判定 urgent(可回戳)。
func TestFormatPoke(t *testing.T) {
	group := formatGroupPoke(&event.GroupPokeEvent{
		PokeEvent: event.PokeEvent{UserID: 10, TargetID: 999}, GroupID: 100,
	})
	wantWake(t, group, "group", 100, "[系统通知][戳一戳] 10 戳了戳 999", true)

	friend := formatFriendPoke(&event.FriendPokeEvent{
		PokeEvent: event.PokeEvent{UserID: 10, TargetID: 999}, SenderID: 10,
	})
	wantWake(t, friend, "private", 10, "[系统通知][戳一戳] 10 戳了戳 999", true)
}

// 点赞:有昵称时 Text/SenderName 用昵称,否则用 QQ 号;非 urgent。
func TestFormatProfileLike(t *testing.T) {
	nick := formatProfileLike(&event.ProfileLikeEvent{OperatorID: 123, OperatorNick: "小明", Times: 10})
	wantWake(t, nick, "private", 123, "[系统通知][点赞] 小明(123) 给你点了 10 个赞", false)
	if nick.SenderName != "小明" {
		t.Errorf("SenderName = %q, want 小明", nick.SenderName)
	}

	noNick := formatProfileLike(&event.ProfileLikeEvent{OperatorID: 123, Times: 3})
	wantWake(t, noNick, "private", 123, "[系统通知][点赞] 123 给你点了 3 个赞", false)
	if noNick.SenderName != "" {
		t.Errorf("SenderName = %q, want 空", noNick.SenderName)
	}
}

func TestFormatInputStatus(t *testing.T) {
	st := formatInputStatus(&event.InputStatusEvent{UserID: 123, StatusText: "对方正在输入..."})
	wantWake(t, st, "private", 123, "[系统通知][输入状态] 123 对方正在输入...", false)

	fallback := formatInputStatus(&event.InputStatusEvent{UserID: 123})
	wantWake(t, fallback, "private", 123, "[系统通知][输入状态] 123 正在输入", false)
}

// flag 必须完整出现在文本里(模型要原样传给 handle_friend_request / handle_group_request)。
func TestFormatFriendRequestKeepsFullFlag(t *testing.T) {
	w := formatFriendRequest(&event.OB11FriendRequestEvent{
		RequsetEvent: event.RequsetEvent{UserID: 123, Flag: "friend_flag_aBc.123-x"},
		Comment:      "我是小明",
	})
	wantWake(t, w, "private", 123,
		"[系统通知][好友申请] QQ:123 验证消息:我是小明 (flag=friend_flag_aBc.123-x)", true)
}

func TestFormatGroupRequestKeepsFullFlag(t *testing.T) {
	w := formatGroupRequest(&event.OB11GroupRequestEvent{
		RequsetEvent: event.RequsetEvent{UserID: 123, Flag: "grp_flag_XyZ.456"},
		GroupID:      100, Comment: "想进群", SubType: "invite",
	})
	wantWake(t, w, "group", 100,
		"[系统通知][加群申请] QQ:123 验证消息:想进群 (flag=grp_flag_XyZ.456, sub_type=invite, group_id=100)", true)
}

// bot_offline 不唤醒 agent:返回零值,由 wakeNotice 跳过(仍由 handler/store.go 归档)。
func TestFormatBotOfflineReturnsZero(t *testing.T) {
	w := formatBotOffline(&event.BotOfflineEvent{UserID: 999, Tag: "test", Message: "offline"})
	if w != (NoticeWake{}) {
		t.Errorf("bot_offline 应返回零值, got %+v", w)
	}
}

// ---------- 集成:真实 dispatcher 路由 → enqueueNotice 钩子入队 ----------

// TestDispatchGroupPokeWakesAgent Dispatch 群戳 JSON → 真实路由 notice.notify.poke →
// enqueueNotice 钩子收到 ChatType=group 且 Text 含 [戳一戳] 的唤醒(urgent)。
func TestDispatchGroupPokeWakesAgent(t *testing.T) {
	var (
		mu  sync.Mutex
		got []IncomingMessage
	)
	saved := enqueueNotice
	enqueueNotice = func(m IncomingMessage) {
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
	}
	t.Cleanup(func() { enqueueNotice = saved })

	event.Dispatch([]byte(`{
		"post_type":"notice","notice_type":"notify","sub_type":"poke",
		"self_id":999,"time":1794000000,
		"group_id":100,"user_id":10,"target_id":999
	}`))
	event.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("群戳应入队恰好 1 条通知(好友戳变体应因 sender_id 缺失跳过), got %d: %+v", len(got), got)
	}
	m := got[0]
	if m.ChatType != "group" || m.ChatID != 100 {
		t.Errorf("ChatKey = %s:%d, want group:100", m.ChatType, m.ChatID)
	}
	if !strings.Contains(m.Text, "[戳一戳]") {
		t.Errorf("Text = %q, 应含 [戳一戳]", m.Text)
	}
	if !m.Urgent {
		t.Error("戳一戳应判定为 urgent")
	}
	if m.SenderID != 10 {
		t.Errorf("SenderID = %d, want 10", m.SenderID)
	}
	if m.SelfID != 999 {
		t.Errorf("SelfID = %d, want 999", m.SelfID)
	}
}

// TestDispatchBotOfflineDoesNotEnqueue bot_offline 只归档不唤醒 agent。
func TestDispatchBotOfflineDoesNotEnqueue(t *testing.T) {
	var (
		mu    sync.Mutex
		count int
	)
	saved := enqueueNotice
	enqueueNotice = func(IncomingMessage) {
		mu.Lock()
		count++
		mu.Unlock()
	}
	t.Cleanup(func() { enqueueNotice = saved })

	event.Dispatch([]byte(`{
		"post_type":"notice","notice_type":"bot_offline",
		"self_id":999,"time":1,"user_id":999,"tag":"test","message":"offline"
	}`))
	event.Wait()

	mu.Lock()
	defer mu.Unlock()
	if count != 0 {
		t.Errorf("bot_offline 不应唤醒 agent, 入队 %d 条", count)
	}
}
