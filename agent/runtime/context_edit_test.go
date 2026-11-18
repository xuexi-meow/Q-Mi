package runtime_test

import (
	"reflect"
	"testing"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
	agentruntime "Q-Mi/agent/runtime"
)

func editableConversation() agentruntime.Conversation {
	return agentruntime.Conversation{UnreadStart: 4, Messages: []agentcontext.Message{
		agentcontext.FromWire(model.ChatMessage{Role: "system", Content: "keep prompt"}, agentcontext.KindWork),
		agentcontext.FromWire(model.ChatMessage{Role: "user", Content: "old text"}, agentcontext.KindWork),
		agentcontext.FromWire(model.ChatMessage{Role: "assistant", ReasoningContent: "private reasoning", ToolCalls: []model.ToolCall{
			{ID: "one", Type: "function", Function: model.ToolFunction{Name: "read_file", Arguments: "{}"}},
			{ID: "two", Type: "function", Function: model.ToolFunction{Name: "send_message", Arguments: "{}"}},
		}}, agentcontext.KindWork),
		agentcontext.FromWire(model.ChatMessage{Role: "tool", ToolCallID: "one", Content: "first result"}, agentcontext.KindWork),
		agentcontext.FromWire(model.ChatMessage{Role: "tool", ToolCallID: "two", Content: "second result"}, agentcontext.KindWork),
		agentcontext.FromWire(model.ChatMessage{Role: "user", Content: "active"}, agentcontext.KindActiveInput),
	}}
}

func TestContextDeletePlansEntireToolBatchWithoutMutatingOriginal(t *testing.T) {
	c := editableConversation()
	before := append([]agentcontext.Message(nil), c.Messages...)
	next, affected, err := c.Change(agentruntime.ContextChange{Action: "delete", Index: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(affected, []int{2, 3, 4}) || len(next.Messages) != 3 || next.UnreadStart != 2 {
		t.Fatalf("batch deletion or unread remap wrong: affected=%v next=%+v", affected, next)
	}
	if next.Messages[2].Wire.Content != "active" || !reflect.DeepEqual(before, c.Messages) {
		t.Fatal("preview changed original or dropped active input")
	}
}

func TestContextEditPreservesImagesAndToolProtocol(t *testing.T) {
	c := editableConversation()
	c.Messages[1].Wire.Content = []model.ContentPart{
		{Type: "text", Text: "old text"},
		{Type: "image_url", ImageURL: &struct {
			URL string `json:"url"`
		}{URL: "data:image/png;base64,AAAA"}},
	}
	text := "edited"
	next, _, err := c.Change(agentruntime.ContextChange{Action: "edit", Index: 1, Text: &text})
	if err != nil {
		t.Fatal(err)
	}
	parts := next.Messages[1].Wire.Content.([]model.ContentPart)
	if parts[0].Text != "edited" || parts[1].ImageURL.URL != "data:image/png;base64,AAAA" ||
		c.Messages[1].Wire.Content.([]model.ContentPart)[0].Text != "old text" {
		t.Fatal("text edit destroyed images or mutated original")
	}
	next, _, err = c.Change(agentruntime.ContextChange{Action: "edit", Index: 2, ClearReasoning: true})
	if err != nil || next.Messages[2].Wire.ReasoningContent != "" ||
		!reflect.DeepEqual(next.Messages[2].Wire.ToolCalls, c.Messages[2].Wire.ToolCalls) {
		t.Fatalf("reasoning edit changed protocol: %v %+v", err, next)
	}
	for _, index := range []int{-1, 0, 6} {
		if _, _, err := c.Change(agentruntime.ContextChange{Action: "edit", Index: index, Text: &text}); err == nil {
			t.Fatalf("edited protected/missing entry %d", index)
		}
	}
}

func TestContextCleanupAndResetPreservePromptsAndLatestState(t *testing.T) {
	c := editableConversation()
	c.State = "latest state"
	c.Messages = append(c.Messages,
		agentcontext.FromWire(model.ChatMessage{Role: "user", Content: "obsolete state"}, agentcontext.KindState),
		agentcontext.FromWire(model.ChatMessage{Role: "user", Content: "latest state"}, agentcontext.KindState))
	c.UnreadStart = 5
	next, affected, err := c.Change(agentruntime.ContextChange{Action: "clean"})
	if err != nil || !reflect.DeepEqual(affected, []int{1, 2, 3, 4, 6}) || next.UnreadStart != 1 ||
		len(next.Messages) != 3 || next.Messages[1].Wire.Content != "active" {
		t.Fatalf("cleanup lost unread or retained stale records: %v %v %+v", err, affected, next)
	}
	next, _, err = c.Change(agentruntime.ContextChange{Action: "reset"})
	if err != nil || len(next.Messages) != 2 || next.Messages[0].Wire.Content != "keep prompt" ||
		next.Messages[1].Wire.Content != "latest state" || next.State != "latest state" || next.UnreadStart != 2 {
		t.Fatalf("reset removed core state: %v %+v", err, next)
	}
}
