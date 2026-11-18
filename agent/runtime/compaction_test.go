package runtime_test

import (
	"reflect"
	"strings"
	"testing"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
	agentruntime "Q-Mi/agent/runtime"
	"Q-Mi/utils/config"
)

func TestConversationPrepareContextSummarizesOnlyOldWork(t *testing.T) {
	conversation := &agentruntime.Conversation{
		State: "private state", UnreadStart: 3,
		Messages: []agentcontext.Message{
			agentcontext.FromWire(model.ChatMessage{Role: "system", Content: "system"}, agentcontext.KindWork),
			agentcontext.FromWire(model.ChatMessage{Role: "user", Content: strings.Repeat("old ", 3000)}, agentcontext.KindWork),
			agentcontext.FromWire(model.ChatMessage{Role: "user", Content: "private state"}, agentcontext.KindState),
			agentcontext.FromWire(model.ChatMessage{Role: "user", Content: "active input"}, agentcontext.KindActiveInput),
		},
	}
	policy := config.AgentContext{MaxInputTokens: 5000, SummaryTokens: 256, KeepRecentMessages: 1}
	calls := 0
	result, err := conversation.PrepareContext(policy, nil, func(messages []model.ChatMessage) string {
		calls++
		if len(messages) != 1 || messages[0].Role != "user" ||
			!strings.HasPrefix(messages[0].Content.(string), "old old ") {
			t.Fatalf("protected data entered summary input: %+v", messages)
		}
		return "old work summary"
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !result.Compacted || result.Removed != 1 || result.Before <= result.After ||
		conversation.Compactions != 1 || conversation.UnreadStart != 3 {
		t.Fatalf("wrong compaction result: %+v, conversation=%+v calls=%d", result, conversation, calls)
	}
	if len(conversation.Messages) != 4 || conversation.Messages[0].Wire.Content != "system" ||
		!strings.Contains(conversation.Messages[1].Wire.Content.(string), "old work summary") ||
		conversation.Messages[2].Wire.Content != "private state" || conversation.Messages[3].Wire.Content != "active input" {
		t.Fatalf("compaction lost protected content: %+v", conversation.Messages)
	}
}

func TestConversationPrepareContextRejectsOversizedUnreadWithoutSummarizing(t *testing.T) {
	conversation := &agentruntime.Conversation{
		UnreadStart: 1,
		Messages: []agentcontext.Message{
			agentcontext.FromWire(model.ChatMessage{Role: "system", Content: "system"}, agentcontext.KindWork),
			agentcontext.FromWire(model.ChatMessage{Role: "user", Content: strings.Repeat("unread ", 3000)}, agentcontext.KindWork),
		},
	}
	before := append([]agentcontext.Message(nil), conversation.Messages...)
	result, err := conversation.PrepareContext(
		config.AgentContext{MaxInputTokens: 5000, SummaryTokens: 256, KeepRecentMessages: 1}, nil,
		func([]model.ChatMessage) string {
			t.Fatal("summary request cannot make unread content fit")
			return ""
		},
	)
	if err == nil || result.Compacted || !reflect.DeepEqual(before, conversation.Messages) {
		t.Fatalf("oversized unread data was changed or accepted: result=%+v err=%v", result, err)
	}
}

func TestOverBudgetShrinksRecentWindowButPreservesActiveInput(t *testing.T) {
	c := &agentruntime.Conversation{UnreadStart: 2, Messages: []agentcontext.Message{
		agentcontext.FromWire(model.ChatMessage{Role: "system", Content: "prompt"}, agentcontext.KindWork),
		agentcontext.FromWire(model.ChatMessage{Role: "user", Content: strings.Repeat("old work ", 2400)}, agentcontext.KindWork),
		agentcontext.FromWire(model.ChatMessage{Role: "user", Content: "current input"}, agentcontext.KindActiveInput),
	}}
	result, err := c.PrepareContext(config.AgentContext{MaxInputTokens: 5000, SummaryTokens: 256, KeepRecentMessages: 12}, nil,
		func(messages []model.ChatMessage) string {
			if len(messages) != 1 || strings.Contains(messages[0].Content.(string), "current input") {
				t.Fatal("emergency summary consumed current input")
			}
			return "old work summarized"
		})
	if err != nil || !result.Compacted || result.After >= 5000 ||
		c.Messages[len(c.Messages)-1].Wire.Content != "current input" {
		t.Fatalf("over-budget context did not recover: %+v %v", result, err)
	}
}

func TestOverBudgetReclaimsSupersededStateWithoutCallingModel(t *testing.T) {
	c := &agentruntime.Conversation{State: "current state", UnreadStart: 1, Messages: []agentcontext.Message{
		agentcontext.FromWire(model.ChatMessage{Role: "system", Content: "prompt"}, agentcontext.KindWork),
		agentcontext.FromWire(model.ChatMessage{Role: "user", Content: strings.Repeat("obsolete ", 2400)}, agentcontext.KindState),
		agentcontext.FromWire(model.ChatMessage{Role: "user", Content: "current state"}, agentcontext.KindState),
		agentcontext.FromWire(model.ChatMessage{Role: "user", Content: "current input"}, agentcontext.KindActiveInput),
	}}
	result, err := c.PrepareContext(config.AgentContext{MaxInputTokens: 5000, SummaryTokens: 256}, nil,
		func([]model.ChatMessage) string { t.Fatal("stale state should be reclaimed locally"); return "" })
	if err != nil || !result.Compacted || len(c.Messages) != 3 || c.UnreadStart != 1 ||
		c.Messages[1].Wire.Content != "current state" {
		t.Fatalf("stale snapshots retained: %+v %v %+v", result, err, c)
	}
}
