package memory_test

import (
	"encoding/json"
	"strings"
	"testing"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/tool"
	memorytools "Q-Mi/agent/tools/memory"
	"Q-Mi/storage"
)

func execute(t *testing.T, tools []tool.Tool, name, args string) (string, error) {
	t.Helper()
	for _, item := range tools {
		if item.Name == name {
			return item.Run(json.RawMessage(args))
		}
	}
	t.Fatalf("missing %s", name)
	return "", nil
}

func TestRecentMessagesDefaultToBoundChatAndLookupRejectsOtherChat(t *testing.T) {
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, record := range []*storage.MessageRecord{
		{MessageID: 1, ChatType: "group", ChatID: 8, Direction: "recv", RawMessage: "group eight", Time: 1},
		{MessageID: 2, ChatType: "group", ChatID: 9, Direction: "recv", RawMessage: "other group", Time: 2},
	} {
		if err := store.PutMessage(record); err != nil {
			t.Fatal(err)
		}
	}
	tools := memorytools.Tools(&memorytools.Dependencies{
		Store: store, Turn: &agentcontext.Turn{ChatType: "group", ChatID: 8},
	})
	out, err := execute(t, tools, "get_recent_messages", `{}`)
	if err != nil || !strings.Contains(out, "group eight") || strings.Contains(out, "other group") || !json.Valid([]byte(out)) {
		t.Fatalf("recent messages = %s, %v", out, err)
	}
	if _, err := execute(t, tools, "get_message", `{"chat_type":"group","chat_id":8,"message_id":2}`); err == nil {
		t.Fatal("lookup read a message belonging to another chat")
	}
}

func TestMemoryDNDChangesAreImmediatelyVisibleButArchiveRemainsSearchable(t *testing.T) {
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.PutMessage(&storage.MessageRecord{
		MessageID: 1, ChatType: "group", ChatID: 8, Direction: "recv", UserID: 7, RawMessage: "archived", Time: 1,
	}); err != nil {
		t.Fatal(err)
	}
	tools := memorytools.Tools(&memorytools.Dependencies{
		Store: store, Turn: &agentcontext.Turn{ChatType: "group", ChatID: 8},
		Attention: func(*agentcontext.Turn) *storage.AttentionFilter { return &storage.AttentionFilter{SelfID: 99} },
	})
	if _, err := execute(t, tools, "chat_dnd", `{"action":"add","chat_type":"group","chat_id":8}`); err != nil {
		t.Fatal(err)
	}
	if out, err := execute(t, tools, "get_recent_messages", `{}`); err != nil || strings.Contains(out, "archived") {
		t.Fatalf("muted recent messages = %s, %v", out, err)
	}
	if out, err := execute(t, tools, "search_messages", `{"chat_type":"group","chat_id":8}`); err != nil || !strings.Contains(out, "archived") {
		t.Fatalf("archive = %s, %v", out, err)
	}
}

func TestMemoryToolsConstructWithoutServicesAndRejectAtRun(t *testing.T) {
	tools := memorytools.Tools(nil)
	if len(tools) != 5 {
		t.Fatalf("memory tools = %d, want 5", len(tools))
	}
	if _, err := execute(t, tools, "view_message_images", `{"message_id":1}`); err == nil {
		t.Fatal("missing storage was silently accepted")
	}
}
