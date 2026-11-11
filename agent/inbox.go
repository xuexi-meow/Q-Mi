package agent

import (
	"time"

	"Q-Mi/bot"
)

type accountInbox struct {
	pending []TurnMessage
	selfID  int64
	timer   *time.Timer
	ready   chan struct{}
}

// Enqueue preserves source metadata and never cancels a model request.
func (m *Manager) Enqueue(msg IncomingMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.acceptsChatMessage(msg.ChatType, msg.ChatID, msg.SenderID, msg.Wake, msg.Mentioned) {
		return
	}
	if m.inbox == nil {
		m.inbox = &accountInbox{ready: make(chan struct{}, 1)}
		go m.workerLoop(m.inbox.ready)
	}
	s := m.inbox
	if msg.SelfID > 0 {
		s.selfID = msg.SelfID
	}
	if msg.Wake == "" {
		msg.Wake = WakeChat
	}
	if msg.Time.IsZero() {
		msg.Time = time.Now()
	}
	s.pending = append(s.pending, TurnMessage{
		MessageID: msg.MessageID, Wake: msg.Wake, ChatType: msg.ChatType, ChatID: msg.ChatID,
		SenderID: msg.SenderID, SenderName: msg.SenderName, Text: msg.Text,
		Segments: append([]byte(nil), msg.Segments...),
		Images:   msg.Images, Time: msg.Time, Mentioned: msg.Mentioned,
	})
	// Start once per batch so a busy chat cannot postpone a wake indefinitely.
	if s.timer == nil {
		delay := m.config().Debounce
		if delay <= 0 {
			delay = defaultDebounce
		}
		s.timer = time.AfterFunc(delay, func() {
			select {
			case s.ready <- struct{}{}:
			default:
			}
		})
	}
}

func (m *Manager) takePending() *Turn {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.inbox
	if s == nil || len(s.pending) == 0 {
		return nil
	}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	pending := s.pending
	s.pending = nil
	visible := make([]TurnMessage, 0, len(pending))
	for _, msg := range pending {
		if m.acceptsChatMessage(msg.ChatType, msg.ChatID, msg.SenderID, msg.Wake, msg.Mentioned) {
			visible = append(visible, msg)
		}
	}
	if len(visible) == 0 {
		return nil
	}
	turn := &Turn{
		SelfID: s.selfID, Wake: wakeFor(visible), Now: time.Now(), NewMessages: visible,
		ChatType: visible[0].ChatType, ChatID: visible[0].ChatID,
	}
	for _, msg := range visible[1:] {
		if msg.ChatType != turn.ChatType || msg.ChatID != turn.ChatID {
			turn.ChatType, turn.ChatID = "", 0
			break
		}
	}
	return turn
}

func (m *Manager) workerLoop(ready <-chan struct{}) {
	for range ready {
		for {
			turn := m.takePending()
			if turn == nil {
				break
			}
			m.handleTurn(turn)
		}
	}
}

func batchIsAdmin(turn *Turn) bool {
	if len(turn.NewMessages) == 0 {
		return false
	}
	for _, msg := range turn.NewMessages {
		if (msg.Wake != "" && msg.Wake != WakeChat) || !bot.IsAdmin(msg.SenderID) {
			return false
		}
	}
	return true
}
