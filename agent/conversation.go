package agent

import (
	"log/slog"
	"reflect"
)

type contextKind uint8

const (
	contextWork contextKind = iota
	contextState
	contextSummary
	contextActiveInput
)

type workingConversation struct {
	messages    []ChatMessage
	state       string
	wakes       int
	unreadStart int
	compactions uint64
}

func (m *Manager) loadTurnState(turn *Turn) {
	notes, err := LoadNotes(m.notesPath)
	if err != nil {
		slog.Warn("读取思考笔记失败，本轮不带笔记", "path", m.notesPath, "err", err)
	} else {
		turn.Notes = notes
	}
	turn.Mood = m.loadMood()
}

func (m *Manager) beginTurn(turn *Turn) []ChatMessage {
	m.events.Record(turn.ChatType, turn.ChatID, KindTurnStart, turnSummary(turn.NewMessages))
	var fresh []ChatMessage
	if builder, ok := m.builder.(interface{ BuildContinuation(*Turn) []ChatMessage }); ok && len(m.conversation.messages) > 0 {
		fresh = builder.BuildContinuation(turn)
	} else if builder, ok := m.builder.(interface{ BuildWorking(*Turn) []ChatMessage }); ok && len(m.conversation.messages) == 0 {
		fresh = builder.BuildWorking(turn)
	} else {
		fresh = m.builder.Build(turn)
	}
	incomingStart := len(fresh) - len(turn.NewMessages)
	if incomingStart < 0 {
		incomingStart = 0
	}
	for i := incomingStart; i < len(fresh); i++ {
		if fresh[i].Role == "user" && fresh[i].kind != contextState {
			fresh[i].kind = contextActiveInput
		}
	}
	if len(m.conversation.messages) == 0 {
		m.conversation.messages = fresh
		m.conversation.unreadStart = incomingStart
		if builder, ok := m.builder.(interface{ StateText(*Turn) string }); ok {
			m.conversation.state = builder.StateText(turn)
		}
	} else {
		for _, message := range fresh {
			if message.Role != "system" {
				m.conversation.messages = append(m.conversation.messages, message)
			}
		}
	}
	if meaningfulTurn(turn) {
		m.conversation.wakes++
	}
	return m.conversation.messages
}

func meaningfulTurn(turn *Turn) bool {
	for _, msg := range turn.NewMessages {
		if msg.Wake != WakeBackground || msg.SenderName != "轮询" {
			return true
		}
	}
	return false
}

func (m *Manager) refreshLiveContext(turn *Turn) *Turn {
	// Builders and background tool closures may still hold the old batch.
	live := &Turn{
		ChatType: turn.ChatType, ChatID: turn.ChatID, Wake: turn.Wake,
		SelfID: turn.SelfID, Now: turn.Now, NewMessages: turn.NewMessages,
		Mood: turn.Mood, Notes: turn.Notes,
	}
	m.loadTurnState(live)
	if builder, ok := m.builder.(interface {
		SystemMessage(*Turn) ChatMessage
		StateText(*Turn) string
	}); ok {
		c := &m.conversation
		system := builder.SystemMessage(live)
		if len(c.messages) > 0 && c.messages[0].Role == "system" && !reflect.DeepEqual(c.messages[0], system) {
			c.messages = append([]ChatMessage(nil), c.messages...)
			c.messages[0] = system
		}
		state := builder.StateText(live)
		if state != c.state {
			c.messages = append(c.messages, ChatMessage{Role: "user", Content: state, kind: contextState})
			c.state = state
		}
	}
	return live
}
