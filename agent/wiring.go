// wiring.go 事件接线（spec 第 2/5/10 节）：agent 包 init 里注册
// message.private / message.group handler，事件到来时才查 app.GetApp()
// （与 handler/store.go 同模式），配置未启用即跳过，零影响。
package agent

import (
	"log/slog"
	"sync"
	"time"

	"Q-Mi/app"
	"Q-Mi/bot"
	apimsg "Q-Mi/api/message"
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
func defaultNewManager(cfg config.AgentConfig, store *storage.Store) *Manager {
	return NewManager(NewDefaultBuilder(store, cfg), NewClient(cfg.BaseURL, cfg.APIKey, 120*time.Second), cfg, store)
}

// urgentFromEvent 判定消息紧急度（spec §5 插话打断）：
// 私聊一律紧急；群聊仅当消息 @ 了机器人自身（按事件自带 self_id）才紧急。
func urgentFromEvent(chatType string, msgs []*evmsg.Message, selfID int64) bool {
	if chatType == "private" {
		return true
	}
	return bot.AtUserID(msgs, selfID)
}

// eventImageURLs 从事件消息段提取图片 URL（P2.7 多模态）：
// 只取 type=="image" 且 url 非空的段；段未解析时懒解析，解析失败跳过。
func eventImageURLs(msgs []*evmsg.Message) []string {
	var urls []string
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
			urls = append(urls, *img.Url)
		}
	}
	return urls
}

func init() {
	event.RegisterTyped("message.private", func(e *event.PrivateMessageEvent) {
		m := getManager()
		if m == nil || e.Sender == nil {
			return
		}
		// 私聊一律响应
		m.Enqueue(IncomingMessage{
			ChatType:   "private",
			ChatID:     int64(e.UserID),
			SelfID:     int64(e.SelfID),
			SenderID:   int64(e.UserID),
			SenderName: e.Sender.Nickname,
			Text:       bot.Text(e.Message),
			Images:     eventImageURLs(e.Message),
			Time:       unixTime(e.Time),
			Urgent:     urgentFromEvent("private", e.Message, int64(e.SelfID)),
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
			ChatType:   "group",
			ChatID:     int64(e.GroupID),
			SelfID:     int64(e.SelfID),
			SenderID:   int64(e.UserID),
			SenderName: e.Sender.Nickname,
			Text:       bot.Text(e.Message),
			Images:     eventImageURLs(e.Message),
			Time:       unixTime(e.Time),
			Urgent:     urgentFromEvent("group", e.Message, int64(e.SelfID)),
		})
	})
}

// getManager 惰性初始化全局 Manager,只初始化一次。
// enable=false 或配置缺失返回 nil,handler 直接 return,零影响。
func getManager() *Manager {
	managerMu.Lock()
	defer managerMu.Unlock()
	if !managerInited {
		managerInited = true
		a := app.GetApp()
		if a.Config == nil || a.Config.Agent == nil || !a.Config.Agent.Enable {
			return nil
		}
		if a.Store == nil {
			slog.Warn("记忆库未启用,agent 将没有历史上下文,仅凭当前消息对话")
		}
		manager = newManagerFunc(*a.Config.Agent, a.Store)
	}
	return manager
}

// unixTime NapCat 上报的 unix 秒(浮点)转 time.Time。
func unixTime(sec float64) time.Time {
	return time.Unix(int64(sec), 0)
}
