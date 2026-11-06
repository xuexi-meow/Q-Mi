package event

import (
	"encoding/json"
	"log/slog"
	"sync"

	"Q-Mi/event/message"
)

// Handler 事件处理函数,接收事件的原始 JSON。
// 推荐用 RegisterTyped 注册,框架自动解析成具体事件结构体。
type Handler func(raw json.RawMessage)

// registry 事件注册表:key -> 处理函数列表。
// 同一 key 可注册多个 handler,同一事件内按注册顺序执行;
// 不同事件之间并发执行,互不阻塞。
var registry = make(map[string][]Handler)
var pending sync.WaitGroup

// Register 注册一个事件处理函数。
//
// key 由 post_type 和二级字段拼成,格式如下:
//
//	message.private / message.group            私聊、群聊消息
//	message_sent.private / message_sent.group  机器人自己发出的消息
//	notice.group_upload / group_admin / group_decrease / group_increase /
//	notice.group_ban / group_recall / group_card / group_essence /
//	notice.group_msg_emoji_like / friend_add / friend_recall / bot_offline
//	notice.notify.group_name / group_title / poke / profile_like / input_status
//	request.friend / request.group             好友/加群请求
//	meta.heartbeat / meta.lifecycle            元事件
//
// 注册函数返回 true 表示 key 有实际 handler,便于在 init 里确认。
func Register(key string, h Handler) {
	registry[key] = append(registry[key], h)
}

// RegisterTyped 类型安全的注册方式。
// 框架负责把原始 JSON 解析成 *T 再调用 h,每个事件都是独立实例,并发安全。
func RegisterTyped[T any](key string, h func(e *T)) {
	Register(key, func(raw json.RawMessage) {
		var e T
		if err := json.Unmarshal(raw, &e); err != nil {
			slog.Error("事件解析失败", "key", key, "err", err)
			return
		}
		if err := handleMessageTypeData(&e); err != nil {
			slog.Error("message 反序列化后处理失败", "key", key, "err", err)
		}
		h(&e)
	})
}

// Registered 返回该 key 是否已注册处理函数,便于排查"事件没反应"的问题。
func Registered(key string) bool {
	_, ok := registry[key]
	return ok
}

func handleMessageTypeData(event any) error {
	me, ok := event.(interface{ Messages() []*message.Message })
	if !ok {
		return nil
	}
	for _, msg := range me.Messages() {
		if msg == nil {
			continue
		}
		if _, err := msg.TypeHandler(); err != nil {
			slog.Error("message 类型解析失败", "err", err)
		}
	}
	return nil
}

// Dispatch 事件唯一入口:根据 post_type 拼出 key,开一个 goroutine 执行全部匹配的 handler。
// 每个事件一个 goroutine,即"简单并发":耗时处理不会阻塞 HTTP 上报,不同事件互不影响。
func Dispatch(raw []byte) {
	key := resolveKey(raw)
	if key == "" {
		return
	}
	hs, ok := registry[key]
	if !ok || len(hs) == 0 {
		return
	}

	pending.Add(1)
	go func() {
		defer pending.Done()
		for _, h := range hs {
			invokeHandler(key, h, raw)
		}
	}()
}

// Wait 必须在停止接收新事件后调用。
func Wait() {
	pending.Wait()
}

func invokeHandler(key string, h Handler, raw json.RawMessage) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("事件处理 panic", "key", key, "panic", r)
		}
	}()
	h(raw)
}

// resolveKey 从事件 JSON 中取出 post_type 与二级字段,拼出注册表 key。
// 一级 post_type 决定用哪个二级字段:
//
//	message / message_sent -> message_type
//	notice                  -> notice_type;其中 notify 还要再拼 sub_type
//	request                 -> request_type
//	meta_event              -> meta_event_type
func resolveKey(raw []byte) string {
	var head struct {
		PostType string `json:"post_type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil || head.PostType == "" {
		return ""
	}

	switch head.PostType {
	case "message", "message_sent":
		var m struct {
			MessageType string `json:"message_type"`
		}
		json.Unmarshal(raw, &m)
		return head.PostType + "." + m.MessageType

	case "notice":
		var n struct {
			NoticeType string `json:"notice_type"`
			SubType    string `json:"sub_type"`
		}
		json.Unmarshal(raw, &n)
		// notify 是一类"复合通知",真正的事件靠 sub_type 区分(戳一戳/群名/头衔等)
		if n.NoticeType == "notify" && n.SubType != "" {
			return "notice.notify." + n.SubType
		}
		return "notice." + n.NoticeType

	case "request":
		var r struct {
			RequestType string `json:"request_type"`
		}
		json.Unmarshal(raw, &r)
		return "request." + r.RequestType

	case "meta_event":
		var m struct {
			MetaEventType string `json:"meta_event_type"`
		}
		json.Unmarshal(raw, &m)
		return "meta." + m.MetaEventType
	}

	return head.PostType
}
