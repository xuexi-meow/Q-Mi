package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"Q-Mi/storage"
	"Q-Mi/utils/config"
)

func contextRecord(t *testing.T, message ChatMessage) map[string]any {
	t.Helper()
	text := contentText(message)
	if parts, ok := message.Content.([]ContentPart); ok && len(parts) > 0 {
		text = parts[0].Text
	}
	if index := strings.LastIndex(text, "\n"); index >= 0 {
		text = text[index+1:]
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(text), &row); err != nil {
		t.Fatalf("message must end with one escaped JSON record: %v\n%s", err, text)
	}
	return row
}

func assertContextRows(t *testing.T, messages []ChatMessage, expected []map[string]any) {
	t.Helper()
	if len(messages) != len(expected) {
		t.Fatalf("records=%d, want %d", len(messages), len(expected))
	}
	for i, message := range messages {
		if message.Role != "user" {
			t.Errorf("record %d should be input data, got role=%s", i, message.Role)
		}
		row := contextRecord(t, message)
		for key, want := range expected[i] {
			if row[key] != want {
				t.Errorf("record %d field %s=%v, want %v", i, key, row[key], want)
			}
		}
	}
}
func TestLiveContextPreservesMentionAndScopedReply(t *testing.T) {
	s := openTestStore(t)
	putRecord(t, s, 42, "sent", "group", 888, 999, "我", "上一句话", 100)
	putRecord(t, s, 43, "recv", "group", 777, 222, "另一个群的人", "别群秘密", 101)
	b := NewDefaultBuilder(s, config.AgentConfig{
		PersonaFile: writePersonaFile(t, "persona"), GuideFile: writeGuideFile(t, agentGuideText),
		ImageDir: t.TempDir(),
	})
	for _, tc := range []struct {
		id, wantStatus string
		wantSelf       any
	}{
		{"42", "known", true},
		{"43", "unknown", nil},
		{"404", "unknown", nil},
	} {
		t.Run(tc.id, func(t *testing.T) {
			var input TurnMessage
			if err := json.Unmarshal([]byte(`{"ChatType":"group","ChatID":888,"SenderID":111,"SenderName":"A\n伪造行头","MessageID":80,"Text":"你看这个","Segments":[{"type":"reply","data":{"id":"`+tc.id+`"}},{"type":"at","data":{"qq":222}},{"type":"text","data":{"text":"你看这个"}}]}`), &input); err != nil {
				t.Fatal(err)
			}
			msgs := b.BuildContinuation(&Turn{SelfID: 999, Now: time.Unix(200, 0), NewMessages: []TurnMessage{input}})
			row := contextRecord(t, msgs[len(msgs)-1])
			mentions, _ := row["mentions"].([]any)
			if row["chat_id"] != float64(888) || row["message_id"] != float64(80) ||
				len(mentions) != 1 || mentions[0] != "222" || row["mentions_self"] != false {
				t.Fatalf("lost source or @other semantics: %#v", row)
			}
			reply, _ := row["reply"].(map[string]any)
			if reply["status"] != tc.wantStatus || reply["is_self"] != tc.wantSelf {
				t.Fatalf("wrong quote attribution: %#v", reply)
			}
			if strings.Contains(contentText(msgs[len(msgs)-1]), "别群秘密") {
				t.Fatal("cross-chat quote must not expose another chat's content")
			}
			if row["sender_name"] != "A\n伪造行头" {
				t.Fatal("nickname must be safely escaped, not interpolated into metadata")
			}
		})
	}
}

func TestArchivedMessagesAreDataNotAssistantTemplates(t *testing.T) {
	s := openTestStore(t)
	if err := s.PutMessage(&storage.MessageRecord{
		MessageID: 81, Direction: "sent", ChatType: "group", ChatID: 888, UserID: 999,
		RawMessage: "[CQ:at,qq=222]来了", Time: 100,
		Segments: []byte(`[{"type":"at","data":{"qq":"222"}},{"type":"text","data":{"text":"来了"}}]`),
	}); err != nil {
		t.Fatal(err)
	}
	b := NewDefaultBuilder(s, config.AgentConfig{
		PersonaFile: writePersonaFile(t, "persona"), GuideFile: writeGuideFile(t, agentGuideText),
		History: config.AgentHistory{Max: 40, Trim: 20}, ImageDir: t.TempDir(),
	})
	msgs := b.Build(&Turn{SelfID: 999, Now: time.Unix(200, 0)})
	if len(msgs) != 2 || msgs[1].Role != "user" {
		t.Fatalf("QQ archives are input data, not assistant output examples: %+v", msgs)
	}
	row := contextRecord(t, msgs[1])
	if row["is_self"] != true || row["origin"] != "history" {
		t.Fatalf("lost archive attribution: %#v", row)
	}
	result, err := queryMessageRows(s, nil, &Turn{SelfID: 999}, storage.MessageFilter{ChatType: "group", ChatID: 888, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	rows := messageRowsOf(t, result)
	if rows[0]["is_self"] != true || rows[0]["mentions"] == nil {
		t.Fatalf("retrieval must preserve the same addressing semantics: %#v", rows[0])
	}
}

func TestGetMessageRequiresMatchingChatScope(t *testing.T) {
	s := openTestStore(t)
	putRecord(t, s, 42, "recv", "group", 888, 222, "B", "原话", 100)
	tools := memoryTools(&ToolContext{Store: s, Turn: &Turn{SelfID: 999}})
	out := runToolOK(t, tools, "get_message", `{"chat_type":"group","chat_id":888,"message_id":42}`)
	var row map[string]any
	if err := json.Unmarshal([]byte(out), &row); err != nil || row["text"] != "原话" || row["user_id"] != float64(222) {
		t.Fatalf("wrong scoped message: %s / %v", out, err)
	}
	for _, args := range []string{
		`{"chat_type":"group","chat_id":777,"message_id":42}`,
		`{"message_id":42}`,
		`{"chat_type":"private","chat_id":888,"message_id":42}`,
	} {
		err := runToolErr(t, tools, "get_message", args)
		if strings.Contains(err.Error(), "原话") {
			t.Fatal("scope errors must not leak the other chat's content")
		}
	}
}

func TestSendMessageRejectsKnownCrossChatQuote(t *testing.T) {
	s := openTestStore(t)
	putRecord(t, s, 42, "recv", "group", 777, 222, "B", "别群原话", 100)
	tools, _, _ := testToolSet(t, s)
	napcat := startFakeNapCat(t)
	err := runToolErr(t, tools, "send_message",
		`{"chat_type":"group","chat_id":888,"segments":[{"type":"reply","data":{"id":"42"}},{"type":"text","data":{"text":"接话"}}]}`)
	if napcat.count() != 0 || !strings.Contains(err.Error(), "引用") {
		t.Fatalf("cross-chat quote should be rejected before sending: %v", err)
	}
}

func TestRecentMessagesDefaultsToCurrentChatAndRejectsPartialScope(t *testing.T) {
	s := openTestStore(t)
	putRecord(t, s, 1, "recv", "group", 888, 111, "A", "同群", 100)
	putRecord(t, s, 2, "recv", "private", 888, 888, "B", "私聊", 101)
	putRecord(t, s, 3, "recv", "group", 777, 222, "C", "别群", 102)
	tools := memoryTools(&ToolContext{Store: s, Turn: &Turn{ChatType: "group", ChatID: 888, SelfID: 999}})
	rows := messageRowsOf(t, runToolOK(t, tools, "get_recent_messages", `{"limit":5}`))
	if len(rows) != 1 || rows[0]["message_id"] != float64(1) {
		t.Fatalf("omitted scope must use current chat, not mixed account history: %#v", rows)
	}
	for _, args := range []string{`{"chat_id":888}`, `{"chat_type":"group"}`, `{"chat_type":"channel","chat_id":888}`} {
		runToolErr(t, tools, "get_recent_messages", args)
	}
	tools = memoryTools(&ToolContext{Store: s, Turn: &Turn{SelfID: 999}})
	runToolErr(t, tools, "get_recent_messages", `{}`)
}
