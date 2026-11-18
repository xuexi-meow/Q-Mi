package runtime

import (
	"encoding/json"
	"sync"
	"time"

	agentcontext "Q-Mi/agent/context"
)

type IncomingMessage struct {
	MessageID  int64
	Wake       string
	ChatType   string
	ChatID     int64
	SelfID     int64
	SenderID   int64
	SenderName string
	Text       string
	Segments   json.RawMessage
	Images     []agentcontext.ImageRef
	Time       time.Time
	Urgent     bool
	Mentioned  bool
}

// Inbox 保留账号级批处理语义，每条消息始终携带自己的来源。
type Inbox struct {
	mu      sync.Mutex
	pending []IncomingMessage
	selfID  int64
	timer   *time.Timer
	ready   chan struct{}
}

func NewInbox() *Inbox { return &Inbox{ready: make(chan struct{}, 1)} }

func (s *Inbox) Ready() <-chan struct{} { return s.ready }

func (s *Inbox) Enqueue(msg IncomingMessage, delay time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if msg.SelfID > 0 {
		s.selfID = msg.SelfID
	}
	if msg.Wake == "" {
		msg.Wake = agentcontext.WakeChat
	}
	if msg.Time.IsZero() {
		msg.Time = time.Now()
	}
	msg.Segments = append(json.RawMessage(nil), msg.Segments...)
	msg.Images = append([]agentcontext.ImageRef(nil), msg.Images...)
	s.pending = append(s.pending, msg)
	if s.timer == nil {
		s.timer = time.AfterFunc(delay, func() {
			select {
			case s.ready <- struct{}{}:
			default:
			}
		})
	}
}

func (s *Inbox) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

func (s *Inbox) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

func (s *Inbox) Take(accept func(IncomingMessage) bool) *agentcontext.Turn {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	pending := s.pending
	s.pending = nil
	messages := make([]agentcontext.TurnMessage, 0, len(pending))
	for _, msg := range pending {
		if accept != nil && !accept(msg) {
			continue
		}
		messages = append(messages, agentcontext.TurnMessage{
			MessageID: msg.MessageID, Wake: msg.Wake, ChatType: msg.ChatType, ChatID: msg.ChatID,
			SenderID: msg.SenderID, SenderName: msg.SenderName, Text: msg.Text,
			Segments: msg.Segments, Images: msg.Images, Time: msg.Time, Mentioned: msg.Mentioned,
		})
	}
	if len(messages) == 0 {
		return nil
	}
	turn := &agentcontext.Turn{
		SelfID: s.selfID, Wake: WakeFor(messages), Now: time.Now(), NewMessages: messages,
		ChatType: messages[0].ChatType, ChatID: messages[0].ChatID,
	}
	for _, msg := range messages[1:] {
		if msg.ChatType != turn.ChatType || msg.ChatID != turn.ChatID {
			turn.ChatType, turn.ChatID = "", 0
			break
		}
	}
	return turn
}

func WakeFor(messages []agentcontext.TurnMessage) string {
	for _, msg := range messages {
		if msg.Wake == agentcontext.WakeChat || msg.Wake == "" {
			return agentcontext.WakeChat
		}
	}
	return agentcontext.WakeBackground
}
