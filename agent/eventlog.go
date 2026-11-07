// eventlog.go W1 WebUI 事件环形缓冲（spec §3.1）：handleTurn 各关键点位
// 把思考链事件写入容量 500 的环形缓冲，供 WebUI 按 ID 增量拉取。
// 只做旁路记录，与现有 slog 并存；Record 在 nil 接收器上为 no-op。
package agent

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Event 单条思考链事件。
type Event struct {
	ID       uint64    `json:"id"`
	Time     time.Time `json:"time"`
	ChatType string    `json:"chat_type,omitempty"`
	ChatID   int64     `json:"chat_id,omitempty"`
	Kind     string    `json:"kind"`
	Text     string    `json:"text"`
}

// Kind 事件类别（spec §3.1）。
const (
	KindTurnStart   = "turn_start"
	KindReasoning   = "reasoning"
	KindOutput      = "output"
	KindToolCall    = "tool_call"
	KindToolResult  = "tool_result"
	KindReply       = "reply"
	KindError       = "error"
	KindFinish      = "finish"
	KindInterrupted = "interrupted"
)

// eventLogCapacity 环形缓冲容量：超出后丢最旧事件。
const eventLogCapacity = 500

// maxEventText 单条事件文本截断长度（reasoning/输出可能极长，UI 只需概览）。
const maxEventText = 2000

// EventLog 固定容量环形缓冲：buf 为定长槽位，start 指向最旧条目，
// count 为当前条数；nextID 单调递增保证 After 增量语义。
type EventLog struct {
	mu     sync.Mutex
	buf    []Event
	start  int
	count  int
	nextID uint64
}

// NewEventLog 构造容量 eventLogCapacity 的空缓冲。
func NewEventLog() *EventLog {
	return &EventLog{buf: make([]Event, eventLogCapacity)}
}

// Record 追加一条事件；满容量时覆盖最旧条目。nil 接收器 no-op。
func (l *EventLog) Record(chatType string, chatID int64, kind, text string) {
	if l == nil {
		return
	}
	if len(text) > maxEventText {
		text = text[:maxEventText]
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.nextID++
	l.buf[(l.start+l.count)%eventLogCapacity] = Event{
		ID:       l.nextID,
		Time:     time.Now(),
		ChatType: chatType,
		ChatID:   chatID,
		Kind:     kind,
		Text:     text,
	}
	if l.count < eventLogCapacity {
		l.count++
	} else {
		l.start = (l.start + 1) % eventLogCapacity
	}
}

// After 返回 ID > after 的事件按序快照（ oldest → newest）。
func (l *EventLog) After(after uint64) []Event {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, 0, l.count)
	for i := 0; i < l.count; i++ {
		e := l.buf[(l.start+i)%eventLogCapacity]
		if e.ID > after {
			out = append(out, e)
		}
	}
	return out
}

// turnSummary turn_start 事件的新消息摘要：首条「发送者: 文本」，
// 多条时追加「等N条」，超长截断。
func turnSummary(ms []TurnMessage) string {
	if len(ms) == 0 {
		return ""
	}
	summary := fmt.Sprintf("%s: %s", ms[0].SenderName, ms[0].Text)
	if len(ms) > 1 {
		summary = fmt.Sprintf("%s 等%d条消息", summary, len(ms))
	}
	if len(summary) > 200 {
		summary = summary[:200]
	}
	return strings.TrimSpace(summary)
}
