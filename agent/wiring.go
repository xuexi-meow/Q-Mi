// wiring.go 事件接线（spec 第 2/5/10 节）：agent 包 init 里注册
// message.private / message.group handler 与 P3-4 的全部通知唤醒 handler，
// 事件到来时才查 app.GetApp()（与 handler/store.go 同模式），
// 配置未启用即跳过，零影响。
package agent

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	apimsg "Q-Mi/api/message"
	"Q-Mi/app"
	"Q-Mi/bot"
	"Q-Mi/event"
	evmsg "Q-Mi/event/message"
	"Q-Mi/storage"
	"Q-Mi/utils/config"
)

var (
	managerMu     sync.Mutex
	manager       *Manager
	managerInited bool

	// newManagerFunc 构造 Manager 的接缝,测试可替换为直接构造,
	// 从而绕过全局单例/惰性初始化。
	newManagerFunc = defaultNewManager
)

// defaultNewManager 用真实配置组装默认实现:DefaultBuilder + OpenAI 客户端(120s 超时)。
// P3.6：把 bot.qq_number 注入 Manager（定时轮询以自己为会话），并按
// agent.self_wake 启动定时轮询兜底唤醒（空串=默认 10 分钟，"off"/"0" 跳过）。
// 工具文档（PromptDocs）在此注入 builder 的 system 静态区。
func defaultNewManager(cfg config.AgentConfig, store *storage.Store) *Manager {
	cfgQQ := int64(0)
	if a := app.GetApp(); a != nil {
		if cur := a.Current(); cur != nil && cur.Bot != nil {
			cfgQQ = int64(cur.Bot.QQ_Number)
		}
	}
	b := NewDefaultBuilder(store, cfg)
	m := NewManager(b, NewClient(cfg.BaseURL, cfg.APIKey, 120*time.Second), cfg, store, cfgQQ)
	b.SetToolDocs(m.toolRegistry().PromptDocs())
	globalBrowser = m.browser
	m.StartSelfWake(selfWakeInterval(cfg))
	// 订阅配置热重载：模型/提示词/阈值热生效，需重启字段只告警。
	if a := app.GetApp(); a != nil && a.ConfigStore != nil {
		m.AttachConfigStore(a.ConfigStore)
	}
	return m
}

// urgentFromEvent 标记私聊/@，仅作消息属性，不取消正在进行的请求：
// 私聊一律紧急；群聊仅当消息 @ 了机器人自身（按事件自带 self_id）才紧急。
func urgentFromEvent(chatType string, msgs []*evmsg.Message, selfID int64) bool {
	if chatType == "private" {
		return true
	}
	return bot.AtUserID(msgs, selfID)
}

// eventImageRefs 从事件消息段提取图片段（P2.7 多模态）：按 image 段顺序取
// (Idx, URL)，Idx 对全部 image 段计数（含缺 url 的段），与 storage 映射锚
// 一致；段未解析时懒解析，解析失败跳过。
func eventImageRefs(msgs []*evmsg.Message) []imageRef {
	var refs []imageRef
	idx := 0
	for _, m := range msgs {
		if m == nil || m.Type != string(apimsg.TypeImage) {
			continue
		}
		if m.Data == nil {
			if _, err := m.TypeHandler(); err != nil {
				continue
			}
		}
		if img, ok := m.Data.(evmsg.Image); ok && img.Url != nil && *img.Url != "" {
			refs = append(refs, imageRef{Idx: idx, URL: *img.Url})
		}
		idx++
	}
	return refs
}

func eventSegments(msgs []*evmsg.Message) json.RawMessage {
	segments := make([]contextSegment, 0, len(msgs))
	for _, msg := range msgs {
		if msg == nil {
			return nil
		}
		data := msg.Parsed
		if len(data) == 0 && msg.Data != nil {
			data, _ = json.Marshal(msg.Data)
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil || fields == nil {
			return nil
		}
		segments = append(segments, contextSegment{Type: msg.Type, Data: fields})
	}
	raw, _ := json.Marshal(segments)
	return raw
}

func init() {
	event.RegisterTyped("message.private", func(e *event.PrivateMessageEvent) {
		m := getManager()
		if m == nil || e.Sender == nil {
			return
		}
		// 管理员的「同意/拒绝 <id>」审批回复：拦截裁决，不进思考循环
		if bot.IsAdmin(int64(e.UserID)) && m.TryResolveApproval(bot.Text(e.Message)) {
			return
		}
		// 私聊进入收件箱；是否回复由模型决定。
		m.Enqueue(IncomingMessage{
			MessageID:  int64(e.MessageID),
			ChatType:   "private",
			ChatID:     int64(e.UserID),
			SelfID:     int64(e.SelfID),
			SenderID:   int64(e.UserID),
			SenderName: e.Sender.Nickname,
			Text:       bot.Text(e.Message),
			Segments:   eventSegments(e.Message),
			Images:     eventImageRefs(e.Message),
			Time:       unixTime(e.Time),
			Urgent:     urgentFromEvent("private", e.Message, int64(e.SelfID)),
			Mentioned:  mentionedFromEvent(e.Message, int64(e.SelfID)),
		})
	})
	event.RegisterTyped("message.group", func(e *event.GroupMessageEvent) {
		m := getManager()
		if m == nil || e.Sender == nil {
			return
		}
		// P2 起群聊不再设 @ 门槛（spec §3）：未 @ 也入队进思考循环，
		// 回不回由模型自决；self_id 取事件自带字段,不依赖配置的 qq_number
		m.Enqueue(IncomingMessage{
			MessageID:  int64(e.MessageID),
			ChatType:   "group",
			ChatID:     int64(e.GroupID),
			SelfID:     int64(e.SelfID),
			SenderID:   int64(e.UserID),
			SenderName: e.Sender.Nickname,
			Text:       bot.Text(e.Message),
			Segments:   eventSegments(e.Message),
			Images:     eventImageRefs(e.Message),
			Time:       unixTime(e.Time),
			Urgent:     urgentFromEvent("group", e.Message, int64(e.SelfID)),
			Mentioned:  mentionedFromEvent(e.Message, int64(e.SelfID)),
		})
	})
	registerNoticeWake()
}

// registerNoticeWake P3-4 通知唤醒:全部 QQ 通知/请求事件唤醒 agent,
// 每类通知格式化成一行 [系统通知] 文本入队,是否反应由模型自决。
// ChatKey:群类通知 → group:<group_id>;好友类 → private:<user_id/sender_id>;
// bot_offline 不唤醒(formatBotOffline 返回零值,wakeNotice 跳过,仅归档)。
func registerNoticeWake() {
	event.RegisterTyped("notice.group_upload", func(e *event.GroupUploadEvent) {
		wakeNotice(e.MainEvent, formatGroupUpload(e))
	})
	event.RegisterTyped("notice.group_admin", func(e *event.GroupAdminEvent) {
		wakeNotice(e.MainEvent, formatGroupAdmin(e))
	})
	event.RegisterTyped("notice.group_decrease", func(e *event.GroupDecreaseEvent) {
		wakeNotice(e.MainEvent, formatGroupDecrease(e))
	})
	event.RegisterTyped("notice.group_increase", func(e *event.GroupIncreaseEvent) {
		wakeNotice(e.MainEvent, formatGroupIncrease(e))
	})
	event.RegisterTyped("notice.group_ban", func(e *event.GroupBanEvent) {
		wakeNotice(e.MainEvent, formatGroupBan(e))
	})
	event.RegisterTyped("notice.friend_add", func(e *event.FriendAddEvent) {
		wakeNotice(e.MainEvent, formatFriendAdd(e))
	})
	event.RegisterTyped("notice.group_recall", func(e *event.GroupRecallEvent) {
		wakeNotice(e.MainEvent, formatGroupRecall(e))
	})
	event.RegisterTyped("notice.friend_recall", func(e *event.FriendRecallEvent) {
		wakeNotice(e.MainEvent, formatFriendRecall(e))
	})
	event.RegisterTyped("notice.group_card", func(e *event.GroupCardEvent) {
		wakeNotice(e.MainEvent, formatGroupCard(e))
	})
	event.RegisterTyped("notice.group_essence", func(e *event.GroupEssenceEvent) {
		wakeNotice(e.MainEvent, formatGroupEssence(e))
	})
	event.RegisterTyped("notice.group_msg_emoji_like", func(e *event.GroupMsgEmojiLikeEvent) {
		wakeNotice(e.MainEvent, formatGroupEmojiLike(e))
	})
	event.RegisterTyped("notice.notify.group_name", func(e *event.GroupNameEvent) {
		wakeNotice(e.MainEvent, formatGroupName(e))
	})
	event.RegisterTyped("notice.notify.group_title", func(e *event.GroupTitleEvent) {
		wakeNotice(e.MainEvent, formatGroupTitle(e))
	})
	// 戳一戳:群戳带 group_id、好友戳带 sender_id(见 event.PokeEvent 注释),
	// 同一 key 注册两个变体,各自按标记字段是否为 0 决定是否处理。
	event.RegisterTyped("notice.notify.poke", func(e *event.GroupPokeEvent) {
		if e.GroupID == 0 {
			return
		}
		wakeNotice(e.MainEvent, formatGroupPoke(e))
	})
	event.RegisterTyped("notice.notify.poke", func(e *event.FriendPokeEvent) {
		if e.SenderID == 0 {
			return
		}
		wakeNotice(e.MainEvent, formatFriendPoke(e))
	})
	event.RegisterTyped("notice.notify.profile_like", func(e *event.ProfileLikeEvent) {
		wakeNotice(e.MainEvent, formatProfileLike(e))
	})
	event.RegisterTyped("notice.notify.input_status", func(e *event.InputStatusEvent) {
		wakeNotice(e.MainEvent, formatInputStatus(e))
	})
	event.RegisterTyped("request.friend", func(e *event.OB11FriendRequestEvent) {
		wakeNotice(e.MainEvent, formatFriendRequest(e))
	})
	event.RegisterTyped("request.group", func(e *event.OB11GroupRequestEvent) {
		wakeNotice(e.MainEvent, formatGroupRequest(e))
	})
	// bot_offline 走零值格式化占位,保证"不唤醒"语义集中在一处(wakeNotice 跳过)。
	event.RegisterTyped("notice.bot_offline", func(e *event.BotOfflineEvent) {
		wakeNotice(e.MainEvent, formatBotOffline(e))
	})
}

// UsageByModelTotals 返回运行中 manager 的按候选用量（未初始化时为空表）。
// 只读快照，供 WebUI 概览展示「哪个模型花了多少」。
func UsageByModelTotals() map[string]UsageSnapshot {
	managerMu.Lock()
	defer managerMu.Unlock()
	if manager == nil {
		return map[string]UsageSnapshot{}
	}
	return manager.UsageByModel()
}

// getManager 惰性初始化全局 Manager,只初始化一次。
// enable=false 或配置缺失返回 nil,handler 直接 return,零影响。
func getManager() *Manager {
	managerMu.Lock()
	defer managerMu.Unlock()
	if !managerInited {
		managerInited = true
		a := app.GetApp()
		cur := a.Current()
		if cur == nil || cur.Agent == nil || !cur.Agent.Enable {
			return nil
		}
		if a.Store == nil {
			slog.Warn("记忆库未启用,agent 将没有历史上下文,仅凭当前消息对话")
		}
		manager = newManagerFunc(*cur.Agent, a.Store)
	}
	return manager
}

// unixTime NapCat 上报的 unix 秒(浮点)转 time.Time。
func unixTime(sec float64) time.Time {
	return time.Unix(int64(sec), 0)
}
