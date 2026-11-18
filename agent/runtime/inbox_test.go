package runtime_test

import (
	"testing"
	"time"

	agentruntime "Q-Mi/agent/runtime"
)

func TestInboxMixedChatsRetainSourceAndConsumeOnce(t *testing.T) {
	inbox := agentruntime.NewInbox()
	defer inbox.Close()
	segments := []byte(`[{"type":"at","data":{"qq":"7"}}]`)
	inbox.Enqueue(agentruntime.IncomingMessage{
		MessageID: 1, SelfID: 7, ChatType: "group", ChatID: 8, SenderID: 10,
		Segments: segments,
	}, time.Hour)
	segments[0] = '!'
	inbox.Enqueue(agentruntime.IncomingMessage{
		MessageID: 2, SelfID: 7, ChatType: "private", ChatID: 9, SenderID: 9,
		Wake: "background",
	}, time.Hour)
	turn := inbox.Take(nil)
	if turn == nil || turn.SelfID != 7 || turn.ChatType != "" || turn.ChatID != 0 ||
		turn.Wake != "chat" || len(turn.NewMessages) != 2 {
		t.Fatalf("mixed batch = %#v", turn)
	}
	if turn.NewMessages[0].ChatID != 8 || turn.NewMessages[1].ChatID != 9 ||
		string(turn.NewMessages[0].Segments) != `[{"type":"at","data":{"qq":"7"}}]` {
		t.Fatalf("source metadata lost: %#v", turn.NewMessages)
	}
	if inbox.Take(nil) != nil || inbox.Len() != 0 {
		t.Fatal("batch consumed twice")
	}
}

func TestInboxRechecksAttentionWhenTakingPending(t *testing.T) {
	inbox := agentruntime.NewInbox()
	defer inbox.Close()
	inbox.Enqueue(agentruntime.IncomingMessage{ChatType: "group", ChatID: 8, SelfID: 7}, time.Hour)
	inbox.Enqueue(agentruntime.IncomingMessage{ChatType: "group", ChatID: 9, SelfID: 7}, time.Hour)
	turn := inbox.Take(func(msg agentruntime.IncomingMessage) bool { return msg.ChatID == 9 })
	if turn == nil || turn.ChatID != 9 || len(turn.NewMessages) != 1 || inbox.Len() != 0 {
		t.Fatalf("filtered batch = %#v", turn)
	}
}
