package runtime_test

import (
	"reflect"
	"strings"
	"testing"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
	agentruntime "Q-Mi/agent/runtime"
)

func work(role, text string, kind agentcontext.MessageKind) agentcontext.Message {
	return agentcontext.FromWire(model.ChatMessage{Role: role, Content: text}, kind)
}

func TestConversationBeginPinsInputsAndDoesNotReplaySystem(t *testing.T) {
	var conversation agentruntime.Conversation
	conversation.Begin([]agentcontext.Message{
		work("system", "persona", agentcontext.KindWork),
		work("user", "state", agentcontext.KindState),
		work("user", "first", agentcontext.KindWork),
	}, 2, "state", true)
	if conversation.Messages[2].Kind != agentcontext.KindActiveInput || conversation.UnreadStart != 2 || conversation.Wakes != 1 {
		t.Fatalf("initial activity = %+v", conversation)
	}
	conversation.Begin([]agentcontext.Message{
		work("system", "persona", agentcontext.KindWork),
		work("user", "inserted", agentcontext.KindWork),
	}, 1, "", false)
	if len(conversation.Messages) != 4 || conversation.Messages[3].Kind != agentcontext.KindActiveInput || conversation.UnreadStart != 2 {
		t.Fatalf("inserted input lost its boundary: %+v", conversation)
	}
	old := append([]agentcontext.Message(nil), conversation.Messages...)
	conversation.BeginActivity()
	if conversation.Messages[2].Kind != agentcontext.KindWork || old[2].Kind != agentcontext.KindActiveInput {
		t.Fatal("activity boundary did not demote prior inputs without mutating published data")
	}
}

func TestConversationSummaryPreservesActiveStateAndUnreadResults(t *testing.T) {
	conversation := agentruntime.Conversation{
		Messages: []agentcontext.Message{
			work("system", "persona", agentcontext.KindWork),
			work("user", "old work", agentcontext.KindWork),
			work("user", "old state", agentcontext.KindState),
			work("user", "live", agentcontext.KindActiveInput),
			work("tool", "unread result", agentcontext.KindWork),
		},
		State: "latest state", UnreadStart: 4,
	}
	messages, unread := conversation.WithSummary(1, 4, "summary")
	if unread != 4 || len(messages) != 5 || messages[1].Kind != agentcontext.KindSummary ||
		messages[2].Wire.Content != "latest state" || messages[3].Wire.Content != "live" || messages[4].Wire.Content != "unread result" {
		t.Fatalf("compacted messages=%+v unread=%d", messages, unread)
	}
	if strings.Contains(messages[1].Wire.Content.(string), "latest state") {
		t.Fatal("current state was delegated to a lossy summary")
	}
}

func TestConversationRefreshIsImmutableAndDoesNotRepeatUnchangedState(t *testing.T) {
	conversation := agentruntime.Conversation{
		Messages: []agentcontext.Message{work("system", "old", agentcontext.KindWork)},
	}
	old := append([]agentcontext.Message(nil), conversation.Messages...)
	system := work("system", "edited", agentcontext.KindWork)
	conversation.Refresh(system, "current")
	first := append([]agentcontext.Message(nil), conversation.Messages...)
	conversation.Refresh(system, "current")
	if !reflect.DeepEqual(first, conversation.Messages) || len(first) != 2 || old[0].Wire.Content != "old" ||
		first[0].Wire.Content != "edited" || first[1].Kind != agentcontext.KindState {
		t.Fatalf("refresh changed old data or repeated state: %+v", conversation)
	}
}
