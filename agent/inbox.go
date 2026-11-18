package agent

import (
	agentruntime "Q-Mi/agent/runtime"
	"Q-Mi/bot"
)

type accountInbox = agentruntime.Inbox

// Enqueue 在门面检查权限与勿扰，队列自身只管理批次和来源。
func (m *Manager) Enqueue(msg IncomingMessage) {
	if m.sleepGate(msg) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.acceptsChatMessage(msg.ChatType, msg.ChatID, msg.SenderID, msg.Wake, msg.Mentioned) {
		return
	}
	if m.inbox == nil {
		m.inbox = agentruntime.NewInbox()
		go m.workerLoop(m.inbox.Ready())
	}
	delay := m.config().Debounce
	if delay <= 0 {
		delay = defaultDebounce
	}
	m.inbox.Enqueue(msg, delay)
}

func (m *Manager) takePending() *Turn {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inbox == nil {
		return nil
	}
	return m.inbox.Take(func(msg IncomingMessage) bool {
		return !m.sleepGate(msg) && m.acceptsChatMessage(msg.ChatType, msg.ChatID, msg.SenderID, msg.Wake, msg.Mentioned)
	})
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
