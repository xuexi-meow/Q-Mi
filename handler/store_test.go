package handler

import (
	"database/sql"
	"path/filepath"
	"testing"

	"Q-Mi/app"
	"Q-Mi/event"
	"Q-Mi/storage"
)

func handlerTestStore(t *testing.T) (*storage.Store, string) {
	t.Helper()
	t.Chdir(t.TempDir())
	path := filepath.Join(t.TempDir(), "events.db")
	st, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a := app.GetApp()
	saved := a.Store
	a.Store = st
	t.Cleanup(func() {
		event.Wait()
		a.Store = saved
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	return st, path
}

func TestStoreMessageEvents(t *testing.T) {
	st, _ := handlerTestStore(t)
	cases := []struct {
		raw, direction, chatType string
		id, chatID, userID       int64
	}{
		{`{"post_type":"message","message_type":"private","self_id":999,"user_id":10,"message_id":11,"time":1,"raw_message":"hello","message":[{"type":"text","data":{"text":"hello"}}]}`, "recv", "private", 11, 10, 10},
		{`{"post_type":"message","message_type":"group","self_id":999,"user_id":10,"group_id":100,"message_id":12,"time":1,"raw_message":"hello","message":[{"type":"text","data":{"text":"hello"}}]}`, "recv", "group", 12, 100, 10},
		{`{"post_type":"message_sent","message_type":"private","self_id":999,"user_id":999,"target_id":10,"message_id":13,"time":1,"raw_message":"hello","message":[{"type":"text","data":{"text":"hello"}}]}`, "sent", "private", 13, 10, 999},
		{`{"post_type":"message_sent","message_type":"group","self_id":999,"user_id":999,"target_id":100,"group_id":100,"message_id":14,"time":1,"raw_message":"hello","message":[{"type":"text","data":{"text":"hello"}}]}`, "sent", "group", 14, 100, 999},
	}
	for _, tc := range cases {
		event.Dispatch([]byte(tc.raw))
		event.Wait()
		got, err := st.GetMessage(tc.id)
		if err != nil || got == nil {
			t.Fatalf("message %d missing: %v", tc.id, err)
		}
		if got.Direction != tc.direction || got.ChatType != tc.chatType || got.ChatID != tc.chatID || got.UserID != tc.userID || got.RawMessage != "hello" || string(got.Segments) != `[{"type":"text","data":{"text":"hello"}}]` {
			t.Fatalf("message %d mapped incorrectly: %+v", tc.id, got)
		}
	}
}

func TestRecallBeforeMessageSurvivesDuplicateEvent(t *testing.T) {
	st, _ := handlerTestStore(t)
	event.Dispatch([]byte(`{"post_type":"notice","notice_type":"friend_recall","message_id":21,"user_id":10}`))
	event.Wait()
	for range 2 {
		event.Dispatch([]byte(`{"post_type":"message","message_type":"private","message_id":21,"user_id":10,"raw_message":"hello","message":[]}`))
		event.Wait()
	}
	got, err := st.GetMessage(21)
	if err != nil || got == nil || !got.Recalled {
		t.Fatalf("撤回状态在消息落库或重复上报后丢失: %+v, %v", got, err)
	}
}

func TestPokeWithoutRawInfoIsArchived(t *testing.T) {
	_, path := handlerTestStore(t)
	event.Dispatch([]byte(`{"post_type":"notice","notice_type":"notify","sub_type":"poke","group_id":100,"user_id":10,"target_id":999}`))
	event.Wait()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM notices WHERE notice_type = 'notify' AND sub_type = 'poke' AND chat_id = 100").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("缺少 raw_info 的通知未归档: count=%d", count)
	}
}
