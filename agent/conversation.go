package agent

import (
	"fmt"
	"log/slog"
	"strings"

	agentcontext "Q-Mi/agent/context"
	agentruntime "Q-Mi/agent/runtime"
)

func turnSummary(ms []TurnMessage) string {
	if len(ms) == 0 {
		return ""
	}
	summary := fmt.Sprintf("%s: %s", ms[0].SenderName, ms[0].Text)
	if len(ms) > 1 {
		summary = fmt.Sprintf("%s 等%d条消息", summary, len(ms))
	}
	if len([]rune(summary)) > 200 {
		summary = string([]rune(summary)[:200])
	}
	return strings.TrimSpace(summary)
}

type workingConversation = agentruntime.Conversation

func (m *Manager) loadTurnState(turn *Turn) {
	notes, err := LoadNotes(m.notesPath)
	if err != nil {
		slog.Warn("读取思考笔记失败，本轮不带笔记", "path", m.notesPath, "err", err)
	} else {
		turn.Notes = notes
	}
	turn.Mood = m.loadMood()
}

func (m *Manager) beginTurn(turn *Turn) []agentcontext.Message {
	m.events.Record(turn.ChatType, turn.ChatID, KindTurnStart, turnSummary(turn.NewMessages))
	var fresh []agentcontext.Message
	if builder, ok := m.builder.(interface{ BuildContinuation(*Turn) []ChatMessage }); ok && len(m.conversation.Messages) > 0 {
		fresh = agentcontext.FromWireMessages(builder.BuildContinuation(turn))
	} else if builder, ok := m.builder.(interface {
		BuildWorkingMessages(*Turn) []agentcontext.Message
	}); ok && len(m.conversation.Messages) == 0 {
		fresh = builder.BuildWorkingMessages(turn)
	} else if builder, ok := m.builder.(interface{ BuildWorking(*Turn) []ChatMessage }); ok && len(m.conversation.Messages) == 0 {
		fresh = agentcontext.FromWireMessages(builder.BuildWorking(turn))
	} else {
		fresh = agentcontext.FromWireMessages(m.builder.Build(turn))
	}
	incomingStart := len(fresh) - len(turn.NewMessages)
	if incomingStart < 0 {
		incomingStart = 0
	}
	state := ""
	if len(m.conversation.Messages) == 0 {
		if builder, ok := m.builder.(interface{ StateText(*Turn) string }); ok {
			state = builder.StateText(turn)
		}
	}
	m.conversation.Begin(fresh, incomingStart, state, agentruntime.MeaningfulTurn(turn))
	return m.conversation.Messages
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
		system := agentcontext.FromWire(builder.SystemMessage(live), agentcontext.KindWork)
		m.conversation.Refresh(system, builder.StateText(live))
	}
	return live
}
