// hooks.go W1 WebUI 的 agent 包导出钩子（spec §3.2）：薄封装包级单例，
// agent 未启用/未初始化时 nil 安全（返回空值或错误），供 webui 包调用。
package agent

import (
	"errors"
	"time"
)

// 钩子的错误语义（agent 未启用 / 机器人 QQ 号未配置）。
var (
	errAgentNotEnabled = errors.New("agent 未启用")
	errSelfIDMissing   = errors.New("机器人 QQ 号未配置（bot.qq_number 为 0）")
)

// EventsAfter 返回思考链事件环形缓冲中 ID > after 的事件（按序）。
// agent 未启用时返回 nil。
func EventsAfter(after uint64) []Event {
	m := getManager()
	if m == nil {
		return nil
	}
	return m.events.After(after)
}

// Wake 把一条 [WebUI] 前缀消息注入 agent 的 self 队列（私聊自己、紧急）。
// agent 未启用或 selfID 未配置（qq_number=0）时返回错误。
func Wake(text string) error {
	m := getManager()
	if m == nil {
		return errAgentNotEnabled
	}
	if m.selfID <= 0 {
		return errSelfIDMissing
	}
	m.Enqueue(IncomingMessage{
		ChatType:   "private",
		ChatID:     m.selfID,
		SelfID:     m.selfID,
		SenderID:   m.selfID,
		SenderName: "WebUI",
		Text:       "[WebUI] " + text,
		Time:       time.Now(),
		Urgent:     false,
	})
	return nil
}

// MoodGet 读取情绪状态（agent/state.json）；文件缺失/损坏返回空串。
func MoodGet() string {
	state, err := readStateFrom(defaultStatePath)
	if err != nil {
		return ""
	}
	return state.Mood
}

// NotesList 读取思考笔记（agent/notes.json）；文件缺失/损坏返回 nil。
func NotesList() []Note {
	notes, err := LoadNotes(defaultNotesPath)
	if err != nil {
		return nil
	}
	return notes
}

// TimerCount 在跑计时器数量。W1 占位返回 -1（TimerStore 缺列表方法，W3 补）。
func TimerCount() int {
	return -1
}
