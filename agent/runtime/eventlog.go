// eventlog.go implements the WebUI event ring and live model-run snapshots.
package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Event is one activity-log entry or the latest snapshot of a model attempt.
type Event struct {
	ID            uint64        `json:"id"`
	Cursor        uint64        `json:"cursor"`
	RunID         uint64        `json:"run_id,omitempty"`
	Time          time.Time     `json:"time"`
	UpdatedAt     time.Time     `json:"updated_at,omitempty"`
	ChatType      string        `json:"chat_type,omitempty"`
	ChatID        int64         `json:"chat_id,omitempty"`
	Kind          string        `json:"kind"`
	State         string        `json:"state,omitempty"`
	Round         int           `json:"round,omitempty"`
	Candidate     string        `json:"candidate,omitempty"`
	Model         string        `json:"model,omitempty"`
	Text          string        `json:"text"`
	Reasoning     string        `json:"reasoning,omitempty"`
	Content       string        `json:"content,omitempty"`
	ToolCalls     []RunToolCall `json:"tool_calls,omitempty"`
	ReceivedBytes int64         `json:"received_bytes,omitempty"`
}

type RunToolCall struct {
	Index     int    `json:"index"`
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type RunUpdate struct {
	ReasoningDelta string
	ContentDelta   string
	ToolCalls      []RunToolCall
	ReceivedBytes  int64
}

// Kind identifies the event category.
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
	KindUsage       = "usage"
	KindConfig      = "config"
	KindModelStream = "model_stream"
)

const (
	eventLogCapacity = 500
	MaxEventText     = 2000
)

// EventLog is a fixed-size event ring. Event IDs identify appended entries;
// cursors also advance when a live model-run entry is updated.
type EventLog struct {
	mu              sync.Mutex
	buf             []Event
	start           int
	count           int
	nextID          uint64
	nextRunID       uint64
	cursor          uint64
	discardedCursor uint64
	runs            map[uint64]int
	notify          chan struct{}
}

func NewEventLog() *EventLog {
	return &EventLog{
		buf:    make([]Event, eventLogCapacity),
		runs:   make(map[uint64]int),
		notify: make(chan struct{}),
	}
}

func (l *EventLog) initLocked() {
	if l.buf == nil {
		l.buf = make([]Event, eventLogCapacity)
	}
	if l.runs == nil {
		l.runs = make(map[uint64]int)
	}
	if l.notify == nil {
		l.notify = make(chan struct{})
	}
}

// Record appends a normal event; nil receivers are no-ops.
func (l *EventLog) Record(chatType string, chatID int64, kind, text string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()
	l.appendLocked(Event{
		Time: time.Now(), UpdatedAt: time.Now(), ChatType: chatType,
		ChatID: chatID, Kind: kind, Text: truncateEventText(text),
	})
}

// BeginRun appends a stable live event and returns its run identifier.
func (l *EventLog) BeginRun(chatType string, chatID int64, round int, candidate, model string) uint64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()
	l.nextRunID++
	runID := l.nextRunID
	now := time.Now()
	event := Event{
		RunID: runID, Time: now, UpdatedAt: now, ChatType: chatType, ChatID: chatID,
		Kind: KindModelStream, State: "generating", Round: round, Candidate: candidate, Model: model,
	}
	slot := l.appendLocked(event)
	l.runs[runID] = slot
	return runID
}

// UpdateRun appends delta fields to the stable run snapshot.
func (l *EventLog) UpdateRun(runID uint64, update RunUpdate) bool {
	if l == nil || runID == 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	slot, ok := l.runs[runID]
	if !ok {
		return false
	}
	event := &l.buf[slot]
	if event.RunID != runID {
		delete(l.runs, runID)
		return false
	}
	event.Reasoning = truncateEventText(event.Reasoning + update.ReasoningDelta)
	event.Content = truncateEventText(event.Content + update.ContentDelta)
	for _, delta := range update.ToolCalls {
		index := -1
		for i := range event.ToolCalls {
			if event.ToolCalls[i].Index == delta.Index {
				index = i
				break
			}
		}
		if index < 0 {
			event.ToolCalls = append(event.ToolCalls, delta)
			continue
		}
		call := &event.ToolCalls[index]
		if delta.ID != "" {
			call.ID = delta.ID
		}
		if delta.Type != "" {
			call.Type = delta.Type
		}
		call.Name = truncateEventText(call.Name + delta.Name)
		call.Arguments = truncateEventText(call.Arguments + delta.Arguments)
	}
	event.ReceivedBytes += update.ReceivedBytes
	event.UpdatedAt = time.Now()
	l.advanceCursorLocked(event)
	return true
}

// EndRun moves a live event to a terminal state while retaining its content.
func (l *EventLog) EndRun(runID uint64, state, detail string) bool {
	if l == nil || runID == 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	slot, ok := l.runs[runID]
	if !ok || l.buf[slot].RunID != runID {
		delete(l.runs, runID)
		return false
	}
	event := &l.buf[slot]
	event.State = state
	if detail != "" {
		event.Text = truncateEventText(detail)
	}
	event.UpdatedAt = time.Now()
	l.advanceCursorLocked(event)
	return true
}

func (l *EventLog) appendLocked(event Event) int {
	l.initLocked()
	var slot int
	if l.count == eventLogCapacity {
		slot = l.start
		discarded := l.buf[slot]
		if discarded.Cursor > l.discardedCursor {
			l.discardedCursor = discarded.Cursor
		}
		if discarded.RunID != 0 {
			delete(l.runs, discarded.RunID)
		}
		l.start = (l.start + 1) % eventLogCapacity
	} else {
		slot = (l.start + l.count) % eventLogCapacity
		l.count++
	}
	l.nextID++
	l.cursor++
	event.ID = l.nextID
	event.Cursor = l.cursor
	if event.UpdatedAt.IsZero() {
		event.UpdatedAt = event.Time
	}
	l.buf[slot] = event
	l.signalLocked()
	return slot
}

func (l *EventLog) advanceCursorLocked(event *Event) {
	l.cursor++
	event.Cursor = l.cursor
	l.signalLocked()
}

func (l *EventLog) signalLocked() {
	close(l.notify)
	l.notify = make(chan struct{})
}

func truncateEventText(text string) string {
	runes := []rune(text)
	if len(runes) > MaxEventText {
		return string(runes[:MaxEventText])
	}
	return text
}

// After returns appended events with ID > after in oldest-to-newest order.
func (l *EventLog) After(after uint64) []Event {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, 0, l.count)
	for i := 0; i < l.count; i++ {
		event := l.buf[(l.start+i)%eventLogCapacity]
		if event.ID > after {
			out = append(out, event)
		}
	}
	return out
}

// Cursor returns the current event mutation cursor.
func (l *EventLog) Cursor() uint64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cursor
}

// WaitAfter returns changed events immediately, or waits for a mutation,
// context cancellation, or timeout. A reset snapshot is returned if the
// requested cursor predates data discarded from the ring.
func (l *EventLog) WaitAfter(ctx context.Context, after uint64, timeout time.Duration) ([]Event, uint64, bool) {
	if l == nil {
		return []Event{}, 0, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout < 0 {
		timeout = 0
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		l.mu.Lock()
		l.initLocked()
		events, cursor, reset := l.afterCursorLocked(after)
		notify := l.notify
		l.mu.Unlock()
		if reset || len(events) > 0 || timeout == 0 {
			return events, cursor, reset
		}
		select {
		case <-ctx.Done():
			l.mu.Lock()
			events, cursor, reset = l.afterCursorLocked(after)
			l.mu.Unlock()
			return events, cursor, reset
		case <-timer.C:
			l.mu.Lock()
			events, cursor, reset = l.afterCursorLocked(after)
			l.mu.Unlock()
			return events, cursor, reset
		case <-notify:
		}
	}
}

func (l *EventLog) afterCursorLocked(after uint64) ([]Event, uint64, bool) {
	reset := after < l.discardedCursor
	out := make([]Event, 0, l.count)
	for i := 0; i < l.count; i++ {
		event := l.buf[(l.start+i)%eventLogCapacity]
		if reset || event.Cursor > after {
			out = append(out, event)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cursor < out[j].Cursor })
	return out, l.cursor, reset
}

func mergeRunToolCalls(calls []RunToolCall) string {
	var parts []string
	for _, call := range calls {
		parts = append(parts, fmt.Sprintf("%s %s", call.Name, call.Arguments))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
