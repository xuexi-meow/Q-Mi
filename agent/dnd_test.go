package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"Q-Mi/app"
	"Q-Mi/event"
	"Q-Mi/storage"
	"Q-Mi/utils/config"
)

func setDNDTestConfig(t *testing.T) {
	t.Helper()
	a := app.GetApp()
	old := a.Config
	a.Config = &config.Config{Bot: &config.Bot{QQ_Number: 999, Admin_Number: []int{555}}}
	t.Cleanup(func() { a.Config = old })
}

func TestChatDNDToolPersistsAndSeparatesConversationTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	tools := buildTools(&ToolContext{Store: store})
	for _, kind := range []string{"group", "private", "group"} {
		runToolOK(t, tools, "chat_dnd", `{"action":"add","chat_type":"`+kind+`","chat_id":888}`)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	tools = buildTools(&ToolContext{Store: store})
	rows := rowsOf(t, runToolOK(t, tools, "chat_dnd", `{"action":"list"}`))
	if len(rows) != 2 || rows[0]["chat_type"] != "group" || rows[1]["chat_type"] != "private" {
		t.Fatalf("settings did not persist independently/idempotently: %#v", rows)
	}
	runToolOK(t, tools, "chat_dnd", `{"action":"remove","chat_type":"group","chat_id":888}`)
	rows = rowsOf(t, runToolOK(t, tools, "chat_dnd", `{"action":"list"}`))
	if len(rows) != 1 || rows[0]["chat_type"] != "private" {
		t.Fatalf("removing group also removed private: %#v", rows)
	}
	for _, args := range []string{
		`{}`, `{"action":"unknown"}`, `{"action":"add","chat_type":"other","chat_id":1}`,
		`{"action":"add","chat_type":"group"}`, `{"action":"remove","chat_type":"private","chat_id":-1}`,
	} {
		runToolErr(t, tools, "chat_dnd", args)
	}
	runToolErr(t, buildTools(&ToolContext{}), "chat_dnd", `{"action":"list"}`)
}

func TestChatDNDRecentContextFiltersBeforeLimitButSearchKeepsArchive(t *testing.T) {
	setDNDTestConfig(t)
	store := openTestStore(t)
	tools := buildTools(&ToolContext{Store: store, Turn: &Turn{SelfID: 999}})
	runToolOK(t, tools, "chat_dnd", `{"action":"add","chat_type":"group","chat_id":888}`)
	runToolOK(t, tools, "chat_dnd", `{"action":"add","chat_type":"private","chat_id":222}`)
	records := []*storage.MessageRecord{
		{MessageID: 1, ChatType: "group", ChatID: 888, UserID: 123, RawMessage: "hidden-normal", Segments: []byte(`[]`)},
		{MessageID: 2, ChatType: "group", ChatID: 888, UserID: 555, RawMessage: "visible-admin"},
		{MessageID: 3, ChatType: "group", ChatID: 888, UserID: 123, RawMessage: "visible-self-at", Segments: []byte(`[{"type":"at","data":{"qq":"999"}}]`)},
		{MessageID: 4, ChatType: "group", ChatID: 888, UserID: 123, RawMessage: "visible-all-at", Segments: []byte(`[{"type":"at","data":{"qq":"all"}}]`)},
		{MessageID: 5, ChatType: "group", ChatID: 888, UserID: 123, RawMessage: "hidden-other-at", Segments: []byte(`[{"type":"at","data":{"qq":"777"}}]`)},
		{MessageID: 6, ChatType: "group", ChatID: 888, UserID: 999, RawMessage: "visible-own", Direction: "sent"},
		{MessageID: 7, ChatType: "group", ChatID: 889, UserID: 123, RawMessage: "visible-unmuted"},
		{MessageID: 8, ChatType: "private", ChatID: 888, UserID: 888, RawMessage: "visible-other-type"},
		{MessageID: 9, ChatType: "private", ChatID: 222, UserID: 222, RawMessage: "hidden-private"},
		{MessageID: 10, ChatType: "private", ChatID: 555, UserID: 555, RawMessage: "visible-admin-private"},
		{MessageID: 11, ChatType: "group", ChatID: 888, UserID: 123, RawMessage: "hidden-fake-at [CQ:at,qq=999]", Segments: []byte(`[{"type":"text","data":{"text":"@999"}}]`)},
	}
	runToolOK(t, tools, "chat_dnd", `{"action":"add","chat_type":"private","chat_id":555}`)
	for i := int64(12); i < 132; i++ {
		records = append(records, &storage.MessageRecord{MessageID: i, ChatType: "group", ChatID: 888,
			UserID: 123, RawMessage: fmt.Sprintf("hidden-flood-%d", i)})
	}
	for _, r := range records {
		r.Time = float64(r.MessageID)
		if r.Direction == "" {
			r.Direction = "recv"
		}
		if err := store.PutMessage(r); err != nil {
			t.Fatal(err)
		}
	}
	// Bootstrap history still spans the account; explicit retrieval is scoped.
	out, err := queryMessageRows(store, nil, &Turn{SelfID: 999},
		storage.MessageFilter{Limit: 7, Attention: recentAttention(&Turn{SelfID: 999})})
	if err != nil {
		t.Fatal(err)
	}
	rows := messageRowsOf(t, out)
	var ids []int64
	for _, row := range rows {
		ids = append(ids, int64(row["message_id"].(float64)))
	}
	if !reflect.DeepEqual(ids, []int64{2, 3, 4, 6, 7, 8, 10}) {
		t.Fatalf("recent filter or pre-limit selection failed: %v", ids)
	}
	cfg := config.AgentConfig{History: config.AgentHistory{Max: 10, Trim: 5}, WorkspaceDir: t.TempDir()}
	b := NewDefaultBuilder(store, cfg)
	msgs := b.BuildWorking(&Turn{SelfID: 999, Now: time.Now()})
	var text strings.Builder
	for _, m := range msgs {
		text.WriteString(contentText(m))
	}
	if strings.Contains(text.String(), "hidden-") || !strings.Contains(text.String(), "visible-all-at") ||
		!strings.Contains(text.String(), "visible-admin") {
		t.Fatalf("automatic history bypassed DND: %s", text.String())
	}
	search := runToolOK(t, tools, "search_messages", `{"query":"hidden","limit":50}`)
	if len(messageRowsOf(t, search)) == 0 {
		t.Fatal("DND must not delete or hide searchable archive")
	}
	// Rules are consulted on every query, not captured when tools were built.
	runToolOK(t, tools, "chat_dnd", `{"action":"remove","chat_type":"group","chat_id":888}`)
	out = runToolOK(t, tools, "get_recent_messages", `{"chat_type":"group","chat_id":888,"limit":1}`)
	if !strings.Contains(out, "hidden-flood-131") {
		t.Fatalf("DND removal did not apply immediately: %s", out)
	}
}

func TestChatDNDInboxDropsOrdinaryPrivateDespiteUrgent(t *testing.T) {
	setDNDTestConfig(t)
	store := openTestStore(t)
	tools := buildTools(&ToolContext{Store: store})
	runToolOK(t, tools, "chat_dnd", `{"action":"add","chat_type":"private","chat_id":222}`)
	m := &Manager{store: store}
	m.Enqueue(IncomingMessage{ChatType: "private", ChatID: 222, SenderID: 222, SelfID: 999,
		Urgent: true, Text: "must-not-wake"})
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inbox != nil {
		t.Fatal("ordinary muted private message created a worker/timer")
	}
}

func TestChatDNDPendingMessagesRecheckedAtInsertion(t *testing.T) {
	setDNDTestConfig(t)
	store := openTestStore(t)
	tools := buildTools(&ToolContext{Store: store})
	m := &Manager{store: store, inbox: &accountInbox{selfID: 999, pending: []TurnMessage{
		{ChatType: "group", ChatID: 888, SenderID: 123, Text: "queued-before-muted"},
		{ChatType: "group", ChatID: 888, SenderID: 555, Text: "admin"},
		{ChatType: "group", ChatID: 888, Wake: WakeBackground, Text: "task-result"},
	}}}
	runToolOK(t, tools, "chat_dnd", `{"action":"add","chat_type":"group","chat_id":888}`)
	turn := m.takePending()
	if turn == nil || len(turn.NewMessages) != 2 || turn.NewMessages[0].Text != "admin" ||
		turn.NewMessages[1].Text != "task-result" {
		t.Fatalf("pending messages bypassed current rules: %+v", turn)
	}
	m.inbox.pending = []TurnMessage{
		{ChatType: "group", ChatID: 888, SenderID: 123, Text: "all-filtered"},
	}
	if turn := m.takePending(); turn != nil || len(m.inbox.pending) != 0 {
		t.Fatalf("filtered batch was not drained: %+v", turn)
	}
}

func TestChatDNDRecentContextIgnoresMalformedSegments(t *testing.T) {
	setDNDTestConfig(t)
	store := openTestStore(t)
	if err := store.SetChatDND("group", 888, true); err != nil {
		t.Fatal(err)
	}
	for i, segments := range []string{
		``, `{invalid`, `null`, `{}`, `{"type":"at","data":{"qq":"999"}}`,
		`["not-json",null,42,true]`,
		`[{"type":"at","data":null},{"type":"text","data":{"qq":"all"}}]`,
		`["not-json",{"type":"at","data":{"qq":999}}]`,
	} {
		if err := store.PutMessage(&storage.MessageRecord{
			MessageID: int64(i + 1), Direction: "recv", ChatType: "group", ChatID: 888,
			UserID: 123, RawMessage: "old record", Segments: []byte(segments), Time: float64(i + 1),
		}); err != nil {
			t.Fatal(err)
		}
	}
	tools := buildTools(&ToolContext{Store: store, Turn: &Turn{SelfID: 999}})
	rows := messageRowsOf(t, runToolOK(t, tools, "get_recent_messages", `{"chat_type":"group","chat_id":888}`))
	if len(rows) != 1 || rows[0]["message_id"] != float64(8) {
		t.Fatalf("malformed or non-at segments passed as mentions: %#v", rows)
	}
}

func TestChatDNDRecentContextUsesCurrentAdminsAndEventSelfID(t *testing.T) {
	setDNDTestConfig(t)
	store := openTestStore(t)
	if err := store.SetChatDND("group", 888, true); err != nil {
		t.Fatal(err)
	}
	for _, record := range []*storage.MessageRecord{
		{MessageID: 1, UserID: 555},
		{MessageID: 2, UserID: 556},
		{MessageID: 3, UserID: 123, Segments: []byte(`[{"type":"at","data":{"qq":"998"}}]`)},
		{MessageID: 4, UserID: 123, Segments: []byte(`[{"type":"at","data":{"qq":"999"}}]`)},
	} {
		record.Direction, record.ChatType, record.ChatID = "recv", "group", 888
		record.Time = float64(record.MessageID)
		if err := store.PutMessage(record); err != nil {
			t.Fatal(err)
		}
	}
	tools := buildTools(&ToolContext{Store: store, Turn: &Turn{SelfID: 998}})
	for i, want := range [][]int64{{1, 3}, {2, 3}} {
		if i > 0 {
			app.GetApp().Config.Bot.Admin_Number = []int{556}
		}
		rows := messageRowsOf(t, runToolOK(t, tools, "get_recent_messages", `{"chat_type":"group","chat_id":888}`))
		var got []int64
		for _, row := range rows {
			got = append(got, int64(row["message_id"].(float64)))
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("current admins/event self_id not respected: got %v, want %v", got, want)
		}
	}
}

func TestChatDNDEventWiringPassesOnlyAdminSelfAndAllMentions(t *testing.T) {
	setDNDTestConfig(t)
	store := openTestStore(t)
	cfg := testCfg(time.Hour)
	cfg.Enable = true
	m := &Manager{store: store, cfg: cfg}
	// A precreated inbox captures delivery without running a model/worker.
	m.inbox = &accountInbox{ready: make(chan struct{}, 1)}
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.inbox.timer != nil {
			m.inbox.timer.Stop()
		}
	}()
	managerMu.Lock()
	old, initialized := manager, managerInited
	manager, managerInited = m, true
	managerMu.Unlock()
	t.Cleanup(func() {
		managerMu.Lock()
		manager, managerInited = old, initialized
		managerMu.Unlock()
	})
	a := app.GetApp()
	a.Config.Agent = &cfg
	tools := buildTools(&ToolContext{Store: store})
	runToolOK(t, tools, "chat_dnd", `{"action":"add","chat_type":"group","chat_id":888}`)
	runToolOK(t, tools, "chat_dnd", `{"action":"add","chat_type":"private","chat_id":222}`)
	for _, tc := range []struct {
		kind   string
		chat   int64
		sender int64
		at     string
		want   int
	}{
		{"group", 888, 123, "", 0},
		{"group", 888, 123, "777", 0},
		{"private", 222, 222, "", 0},
		{"group", 888, 555, "", 1},
		{"group", 888, 123, "999", 1},
		{"group", 888, 123, "all", 1},
		{"group", 889, 123, "", 1},
		{"private", 223, 223, "", 1},
	} {
		t.Run(fmt.Sprintf("%s/%d/%d/%s", tc.kind, tc.chat, tc.sender, tc.at), func(t *testing.T) {
			segments := []any{map[string]any{"type": "text", "data": map[string]any{"text": "message"}}}
			if tc.at != "" {
				segments = append(segments, map[string]any{"type": "at", "data": map[string]any{"qq": tc.at}})
			}
			raw, _ := json.Marshal(map[string]any{
				"post_type": "message", "message_type": tc.kind, "sub_type": "normal",
				"group_id": tc.chat, "user_id": tc.sender, "self_id": 999, "message_id": 100,
				"time": 1794000000, "message": segments, "raw_message": "message",
				"sender": map[string]any{"user_id": tc.sender, "nickname": "member", "role": "member"},
			})
			event.Dispatch(raw)
			event.Wait()
			turn := m.takePending()
			count := 0
			if turn != nil {
				count = len(turn.NewMessages)
			}
			if count != tc.want {
				t.Fatalf("delivered %d messages, want %d: %+v", count, tc.want, turn)
			}
		})
	}
}
