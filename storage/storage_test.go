package storage

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenCreatesSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	want := []string{"messages", "notices", "messages_fts", "message_recalls"}
	for _, table := range want {
		var name string
		err := s.db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type IN ('table','view') AND name = ?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("表 %s 不存在: %v", table, err)
		}
	}
}

func testRecord(id int64, chatID int64, t float64) *MessageRecord {
	return &MessageRecord{
		MessageID: id, Direction: "recv", ChatType: "group",
		ChatID: chatID, UserID: 10001,
		RawMessage: "签到成功", Segments: []byte(`[{"type":"text","data":{"text":"签到成功"}}]`),
		Time: t,
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) 失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPutGetMessage(t *testing.T) {
	s := openTestStore(t) // helper: Open(":memory:")
	want := testRecord(10001, 9527, 1729200000)
	if err := s.PutMessage(want); err != nil {
		t.Fatalf("PutMessage: %v", err)
	}

	got, err := s.GetMessage(10001)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got == nil {
		t.Fatal("GetMessage 返回 nil")
	}
	if !reflect.DeepEqual(got, want) { // 结构体含 []byte,不可用 == 直接比较
		t.Errorf("roundtrip 不一致:\n want %+v\n got  %+v", want, got)
	}

	missing, err := s.GetMessage(999999)
	if err != nil || missing != nil {
		t.Errorf("不存在的消息应返回 nil,nil, 得到 %v, %v", missing, err)
	}
}

func TestPutMessageUpsert(t *testing.T) {
	s := openTestStore(t)
	if err := s.PutMessage(testRecord(10001, 9527, 1729200000)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutMessage(testRecord(10001, 9527, 1729200001)); err != nil { // 同 id 重写
		t.Fatal(err)
	}
	got, _ := s.GetMessage(10001)
	if got.Time != 1729200001 {
		t.Errorf("重复 Put 应覆盖, time = %v", got.Time)
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&n)
	if n != 1 {
		t.Errorf("重复 Put 不应多行, count = %d", n)
	}
}

func TestPutMessageFTSFailureRollsBack(t *testing.T) {
	for _, tc := range []struct {
		name      string
		operation string
		existing  bool
	}{
		{"insert", "INSERT", false},
		{"update_delete", "DELETE", true},
		{"update_insert", "INSERT", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			original := testRecord(1, 100, 1)
			original.RawMessage = "original"
			if tc.existing {
				if err := s.PutMessage(original); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec(`CREATE TRIGGER fail_fts BEFORE ` + tc.operation + ` ON messages_fts_docsize
				BEGIN SELECT RAISE(ABORT, 'forced FTS failure'); END`); err != nil {
				t.Fatal(err)
			}
			updated := testRecord(1, 200, 2)
			updated.RawMessage = "updated"
			if err := s.PutMessage(updated); err == nil || !strings.Contains(err.Error(), "FTS") {
				t.Fatalf("expected forced FTS failure, got %v", err)
			}
			got, err := s.GetMessage(1)
			if err != nil {
				t.Fatal(err)
			}
			var want *MessageRecord
			if tc.existing {
				want = original
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("message not rolled back: got %+v, want %+v", got, want)
			}
			var count int
			if err := s.db.QueryRow("SELECT COUNT(*) FROM messages_fts").Scan(&count); err != nil {
				t.Fatal(err)
			}
			wantCount := 0
			if tc.existing {
				wantCount = 1
				var text string
				if err := s.db.QueryRow("SELECT raw_message FROM messages_fts WHERE rowid = 1").Scan(&text); err != nil || text != "original" {
					t.Errorf("FTS not rolled back: text=%q, err=%v", text, err)
				}
			}
			if count != wantCount {
				t.Errorf("FTS rows = %d, want %d", count, wantCount)
			}
			if _, err := s.db.Exec("DROP TRIGGER fail_fts"); err != nil {
				t.Fatal(err)
			}
			if err := s.PutMessage(updated); err != nil {
				t.Fatalf("write after rollback: %v", err)
			}
		})
	}
}

func TestPutMessageConcurrentIndexConsistency(t *testing.T) {
	s := openTestStore(t)
	const writers = 32
	for round := 0; round < 5; round++ {
		start := make(chan struct{})
		errs := make(chan error, writers)
		var wg sync.WaitGroup
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				r := testRecord(1, 100, float64(i))
				r.RawMessage = fmt.Sprintf("shared token%d", round*writers+i)
				errs <- s.PutMessage(r)
			}(i)
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("concurrent PutMessage: %v", err)
			}
		}
		got, err := s.GetMessage(1)
		if err != nil || got == nil {
			t.Fatalf("GetMessage: got=%+v, err=%v", got, err)
		}
		var indexed string
		if err := s.db.QueryRow("SELECT raw_message FROM messages_fts WHERE rowid = 1").Scan(&indexed); err != nil {
			t.Fatal(err)
		}
		if indexed != got.RawMessage {
			t.Errorf("index=%q, message=%q", indexed, got.RawMessage)
		}
		for i := 0; i < writers; i++ {
			text := fmt.Sprintf("shared token%d", round*writers+i)
			var count int
			if err := s.db.QueryRow("SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH ?", `"`+text+`"`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if text == got.RawMessage {
				want = 1
			}
			if count != want {
				t.Errorf("search %q: count=%d, want %d", text, count, want)
			}
		}
	}
}

func TestOpenIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s := openFileStore(t, path)
	r := testRecord(1, 100, 1)
	r.RawMessage = "吃火锅 hello"
	if err := s.PutMessage(r); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := s.db.QueryRow("PRAGMA schema_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s = openFileStore(t, path)
		assertNoFTSRebuild(t, s, version)
		got, err := s.GetMessage(1)
		if err != nil || !reflect.DeepEqual(got, r) {
			t.Errorf("message changed after reopening: got=%+v, err=%v", got, err)
		}
		found, err := s.SearchMessages("火锅", MessageFilter{})
		if err != nil || !reflect.DeepEqual(ids(found), []int64{1}) {
			t.Errorf("index lost after reopening: ids=%v, err=%v", ids(found), err)
		}
	}
}

func assertNoFTSRebuild(t *testing.T, s *Store, version int) {
	t.Helper()
	var changes, gotVersion int
	if err := s.db.QueryRow("SELECT total_changes()").Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("PRAGMA schema_version").Scan(&gotVersion); err != nil {
		t.Fatal(err)
	}
	if changes != 0 || gotVersion != version {
		t.Errorf("unchanged schema rebuilt on Open: changes=%d, version=%d, want version=%d", changes, gotVersion, version)
	}
}

func TestOpenMigratesExternalContentFTS(t *testing.T) {
	for _, option := range []string{"content='messages'", "CONTENT = 'messages'"} {
		t.Run(option, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			_, err = db.Exec(`
CREATE TABLE messages (
    message_id INTEGER PRIMARY KEY,
    direction TEXT NOT NULL,
    chat_type TEXT NOT NULL,
    chat_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    raw_message TEXT NOT NULL,
    segments TEXT NOT NULL,
    time REAL NOT NULL,
    recalled INTEGER NOT NULL DEFAULT 0
);
CREATE VIRTUAL TABLE messages_fts USING fts5(raw_message, ` + option + `, content_rowid='message_id');
CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, raw_message) VALUES (new.message_id, new.raw_message);
END;
CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, raw_message) VALUES ('delete', old.message_id, old.raw_message);
END;
CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, raw_message) VALUES ('delete', old.message_id, old.raw_message);
    INSERT INTO messages_fts(rowid, raw_message) VALUES (new.message_id, new.raw_message);
END;`)
			if err != nil {
				t.Fatal(err)
			}
			recs := []*MessageRecord{testRecord(1, 100, 1), testRecord(2, 200, 2), testRecord(3, 100, 3), testRecord(4, 100, 4)}
			recs[0].RawMessage = "吃火锅 hello"
			recs[1].RawMessage = "锅里有火"
			recs[2].RawMessage = "吃火锅 world"
			recs[2].Recalled = true
			recs[3].RawMessage = ""
			for _, r := range recs {
				if _, err := db.Exec(`INSERT INTO messages
					(message_id, direction, chat_type, chat_id, user_id, raw_message, segments, time, recalled)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.MessageID, r.Direction, r.ChatType, r.ChatID,
					r.UserID, r.RawMessage, string(r.Segments), r.Time, r.Recalled); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			s := openFileStore(t, path)
			for _, want := range recs {
				got, err := s.GetMessage(want.MessageID)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("migration changed message: got=%+v, want=%+v, err=%v", got, want, err)
				}
			}
			for _, tc := range []struct {
				f    MessageFilter
				want []int64
			}{
				{MessageFilter{}, []int64{1}},
				{MessageFilter{IncludeRecalled: true}, []int64{3, 1}},
				{MessageFilter{ChatID: 200}, []int64{}},
			} {
				got, err := s.SearchMessages("火锅", tc.f)
				if err != nil || !reflect.DeepEqual(ids(got), tc.want) {
					t.Errorf("migrated search: ids=%v, want %v, err=%v", ids(got), tc.want, err)
				}
			}
			var count int
			if err := s.db.QueryRow("SELECT COUNT(*) FROM messages_fts").Scan(&count); err != nil || count != 4 {
				t.Errorf("migrated FTS row count=%d, err=%v", count, err)
			}
			if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name IN ('messages_ai', 'messages_ad', 'messages_au')").Scan(&count); err != nil || count != 0 {
				t.Errorf("legacy triggers remain: count=%d, err=%v", count, err)
			}
			var version int
			if err := s.db.QueryRow("PRAGMA schema_version").Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openFileStore(t, path)
			assertNoFTSRebuild(t, s, version)
			updated := *recs[0]
			updated.RawMessage = "炒饭 hello"
			if err := s.PutMessage(&updated); err != nil {
				t.Fatalf("upsert after migration: %v", err)
			}
			if got, err := s.SearchMessages("火锅", MessageFilter{IncludeRecalled: true}); err != nil || !reflect.DeepEqual(ids(got), []int64{3}) {
				t.Errorf("stale FTS after upsert: ids=%v, err=%v", ids(got), err)
			}
			if got, err := s.SearchMessages("炒饭", MessageFilter{}); err != nil || !reflect.DeepEqual(ids(got), []int64{1}) {
				t.Errorf("new FTS after upsert: ids=%v, err=%v", ids(got), err)
			}
		})
	}
}

func seedMessages(t *testing.T, s *Store) {
	t.Helper()
	// 两个群、一个私聊,时间乱序插入,其中一条已撤回
	recs := []*MessageRecord{
		{MessageID: 1, Direction: "recv", ChatType: "group", ChatID: 100, UserID: 11, RawMessage: "早上好", Time: 1729200001},
		{MessageID: 2, Direction: "recv", ChatType: "group", ChatID: 100, UserID: 22, RawMessage: "中午吃啥", Time: 1729200003, Recalled: true},
		{MessageID: 3, Direction: "sent", ChatType: "group", ChatID: 100, UserID: 999, RawMessage: "吃火锅", Time: 1729200002},
		{MessageID: 4, Direction: "recv", ChatType: "group", ChatID: 200, UserID: 33, RawMessage: "别群的消息", Time: 1729200004},
		{MessageID: 5, Direction: "recv", ChatType: "private", ChatID: 77, UserID: 77, RawMessage: "私聊在吗", Time: 1729200005},
		{MessageID: 6, Direction: "recv", ChatType: "group", ChatID: 100, UserID: 11, RawMessage: "晚上开黑", Time: 1729200006},
	}
	for _, r := range recs {
		if err := s.PutMessage(r); err != nil {
			t.Fatalf("seed %d: %v", r.MessageID, err)
		}
	}
}

func TestRecentMessages(t *testing.T) {
	s := openTestStore(t)
	seedMessages(t, s)

	got, err := s.RecentMessages("group", 100, 2)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("条数 = %d, 期望 2", len(got))
	}
	if got[0].MessageID != 6 || got[1].MessageID != 3 {
		t.Errorf("顺序错误: %d, %d (已撤回的 2 应被排除)", got[0].MessageID, got[1].MessageID)
	}
}

func TestQueryMessagesFilter(t *testing.T) {
	s := openTestStore(t)
	seedMessages(t, s)

	// 按用户过滤
	got, _ := s.QueryMessages(MessageFilter{UserID: 11})
	if len(got) != 2 || got[0].MessageID != 6 {
		t.Errorf("按用户过滤错误: %+v", ids(got))
	}

	// 按时间窗过滤(注:计划原 Until=1729200005 且 Since=1729200002 只含 msg 3,
	// 此处修正为 Since=1729200001/Until=1729200006,窗口含 msg 1/3;
	// msg 1 时间恰为 Since 验证左闭,msg 6 恰为 Until 验证右开)
	got, _ = s.QueryMessages(MessageFilter{ChatType: "group", ChatID: 100,
		Since: time.Unix(1729200001, 0), Until: time.Unix(1729200006, 0)})
	if len(got) != 2 || got[0].MessageID != 3 || got[1].MessageID != 1 {
		t.Errorf("时间窗过滤错误: %+v", ids(got))
	}

	// IncludeRecalled
	got, _ = s.QueryMessages(MessageFilter{ChatID: 100, IncludeRecalled: true})
	if len(got) != 4 {
		t.Errorf("IncludeRecalled 应含撤回条目, 得到 %+v", ids(got))
	}

	// Limit 缺省 50
	got, _ = s.QueryMessages(MessageFilter{ChatID: 100, IncludeRecalled: true, Limit: 0})
	if len(got) != 4 { // 数据不足 50,验证不炸即可
		t.Errorf("Limit 缺省异常: %+v", ids(got))
	}
}

func TestMarkRecalled(t *testing.T) {
	s := openTestStore(t)
	seedMessages(t, s)

	if err := s.MarkRecalled(1); err != nil {
		t.Fatalf("MarkRecalled: %v", err)
	}
	got, _ := s.GetMessage(1)
	if !got.Recalled {
		t.Error("标记后 Recalled 应为 true")
	}
	recent, _ := s.RecentMessages("group", 100, 10)
	for _, r := range recent {
		if r.MessageID == 1 {
			t.Error("默认查询不应包含已撤回消息")
		}
	}
	if err := s.MarkRecalled(1); err != nil { // 重复标记幂等
		t.Errorf("重复标记报错: %v", err)
	}
}

func openFileStore(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRecalledOutOfOrderAndRestart(t *testing.T) {
	for _, recallFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("recall_first_%t", recallFirst), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recalls.db")
			s := openFileStore(t, path)
			if !recallFirst {
				if err := s.PutMessage(testRecord(1, 100, 1)); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				if err := s.MarkRecalled(1); err != nil {
					t.Fatal(err)
				}
			}
			if recallFirst {
				if got, err := s.GetMessage(1); err != nil || got != nil {
					t.Fatalf("recall must not create a message: got=%+v, err=%v", got, err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openFileStore(t, path)
			for i := 0; i < 2; i++ {
				if err := s.PutMessage(testRecord(1, 100, float64(i+2))); err != nil {
					t.Fatal(err)
				}
				got, err := s.GetMessage(1)
				if err != nil || got == nil || !got.Recalled {
					t.Errorf("recall lost after PutMessage: got=%+v, err=%v", got, err)
				}
			}
			if err := s.MarkRecalled(1); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := s.db.QueryRow("SELECT COUNT(*) FROM message_recalls WHERE message_id = 1").Scan(&count); err != nil || count != 1 {
				t.Errorf("recall not persisted idempotently: count=%d, err=%v", count, err)
			}
			if err := s.PutMessage(testRecord(2, 100, 4)); err != nil {
				t.Fatal(err)
			}
			if got, err := s.RecentMessages("group", 100, 0); err != nil || !reflect.DeepEqual(ids(got), []int64{2}) {
				t.Errorf("default recall exclusion: ids=%v, err=%v", ids(got), err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openFileStore(t, path)
			if got, err := s.GetMessage(1); err != nil || got == nil || !got.Recalled {
				t.Errorf("recall lost after restart: got=%+v, err=%v", got, err)
			}
		})
	}
}

func TestPutMessageRecalledMonotonic(t *testing.T) {
	for _, initiallyRecalled := range []bool{true, false} {
		t.Run(fmt.Sprintf("initially_recalled_%t", initiallyRecalled), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recalled.db")
			s := openFileStore(t, path)
			r := testRecord(1, 100, 1)
			r.Recalled = initiallyRecalled
			if err := s.PutMessage(r); err != nil {
				t.Fatal(err)
			}
			r.Recalled = true
			if err := s.PutMessage(r); err != nil {
				t.Fatal(err)
			}
			if got, err := s.GetMessage(1); err != nil || got == nil || !got.Recalled {
				t.Fatalf("incoming recalled flag ignored: got=%+v, err=%v", got, err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openFileStore(t, path)
			r.Recalled = false
			if err := s.PutMessage(r); err != nil {
				t.Fatal(err)
			}
			if got, err := s.GetMessage(1); err != nil || got == nil || !got.Recalled {
				t.Errorf("existing recalled flag reset: got=%+v, err=%v", got, err)
			}
		})
	}
}

func TestMarkRecalledRollsBack(t *testing.T) {
	for _, target := range []string{"INSERT ON message_recalls", "UPDATE ON messages"} {
		t.Run(target, func(t *testing.T) {
			s := openTestStore(t)
			if err := s.PutMessage(testRecord(1, 100, 1)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`CREATE TRIGGER fail_recall BEFORE ` + target + `
				BEGIN SELECT RAISE(ABORT, 'forced recall failure'); END`); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkRecalled(1); err == nil {
				t.Fatal("expected recall failure")
			}
			if got, err := s.GetMessage(1); err != nil || got == nil || got.Recalled {
				t.Errorf("message changed on failure: got=%+v, err=%v", got, err)
			}
			var count int
			if err := s.db.QueryRow("SELECT COUNT(*) FROM message_recalls").Scan(&count); err != nil || count != 0 {
				t.Errorf("recall record not rolled back: count=%d, err=%v", count, err)
			}
			if _, err := s.db.Exec("DROP TRIGGER fail_recall"); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkRecalled(1); err != nil {
				t.Fatalf("recall after rollback: %v", err)
			}
			if got, err := s.GetMessage(1); err != nil || got == nil || !got.Recalled {
				t.Errorf("recall after rollback not saved: got=%+v, err=%v", got, err)
			}
		})
	}
}

func TestSearchMessages(t *testing.T) {
	s := openTestStore(t)
	seedMessages(t, s)

	got, err := s.SearchMessages("火锅", MessageFilter{})
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(got) != 1 || got[0].MessageID != 3 {
		t.Errorf("搜索结果错误: %+v", ids(got))
	}

	got, err = s.SearchMessages("不存在的词xyz", MessageFilter{})
	if err != nil || len(got) != 0 {
		t.Errorf("未命中应返回空: %v, %+v", err, ids(got))
	}
}

func TestSearchMessagesFilter(t *testing.T) {
	s := openTestStore(t)
	recs := []*MessageRecord{
		{MessageID: 1, ChatType: "group", ChatID: 100, UserID: 11, Time: 100.125},
		{MessageID: 2, ChatType: "group", ChatID: 100, UserID: 11, Time: 100.250},
		{MessageID: 3, ChatType: "group", ChatID: 100, UserID: 22, Time: 100.375},
		{MessageID: 4, ChatType: "private", ChatID: 100, UserID: 11, Time: 100.500},
		{MessageID: 5, ChatType: "group", ChatID: 200, UserID: 11, Time: 100.625},
		{MessageID: 6, ChatType: "group", ChatID: 100, UserID: 11, Time: 100.750, Recalled: true},
		{MessageID: 7, ChatType: "group", ChatID: 100, UserID: 11, Time: 101},
		{MessageID: 8, ChatType: "group", ChatID: 100, UserID: 11, Time: 101.250},
	}
	for _, r := range recs {
		r.RawMessage = "火锅"
		if r.MessageID == 8 {
			r.RawMessage = "炒饭"
		}
		if err := s.PutMessage(r); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		f    MessageFilter
		want []int64
	}{
		{"default", MessageFilter{}, []int64{7, 5, 4, 3, 2, 1}},
		{"chat_type", MessageFilter{ChatType: "group"}, []int64{7, 5, 3, 2, 1}},
		{"chat_id", MessageFilter{ChatID: 100}, []int64{7, 4, 3, 2, 1}},
		{"user", MessageFilter{UserID: 22}, []int64{3}},
		{"since", MessageFilter{Since: time.UnixMilli(100250)}, []int64{7, 5, 4, 3, 2}},
		{"until", MessageFilter{Until: time.UnixMilli(100500)}, []int64{3, 2, 1}},
		{"combined", MessageFilter{ChatType: "group", ChatID: 100, UserID: 11,
			Since: time.UnixMilli(100250), Until: time.UnixMilli(100750), IncludeRecalled: true}, []int64{2}},
		{"include_recalled", MessageFilter{IncludeRecalled: true}, []int64{7, 6, 5, 4, 3, 2, 1}},
		{"limit_after_filter", MessageFilter{UserID: 22, Limit: 1}, []int64{3}},
		{"limit", MessageFilter{Limit: 2}, []int64{7, 5}},
		{"no_match", MessageFilter{ChatID: 999}, []int64{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.SearchMessages("火锅", tc.f)
			if err != nil || !reflect.DeepEqual(ids(got), tc.want) {
				t.Errorf("ids=%v, want %v, err=%v", ids(got), tc.want, err)
			}
		})
	}
}

func TestSearchMessagesLimit(t *testing.T) {
	s := openTestStore(t)
	for id := int64(1); id <= 60; id++ {
		r := testRecord(id, 100, float64(id))
		r.RawMessage = "needle"
		if err := s.PutMessage(r); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ limit, want int }{{0, 50}, {-1, 50}, {3, 3}, {100, 60}} {
		t.Run(fmt.Sprint(tc.limit), func(t *testing.T) {
			got, err := s.SearchMessages("needle", MessageFilter{Limit: tc.limit})
			if err != nil || len(got) != tc.want {
				t.Fatalf("count=%d, want %d, err=%v", len(got), tc.want, err)
			}
			for i, r := range got {
				if r.MessageID != int64(60-i) {
					t.Errorf("result %d: id=%d, want %d", i, r.MessageID, 60-i)
				}
			}
		})
	}
}

func TestSearchMessagesPhrasesAndSyntax(t *testing.T) {
	s := openTestStore(t)
	for i, text := range []string{
		"今晚吃火锅 hello world",
		"锅里有火 hello world",
		"火红的锅 hello world",
		"今天吃火锅 hello",
		"world HELLO 吃火锅",
		"hello OR world 火锅",
		"hello NEAR world 火锅",
		"raw_message:火锅*",
		"版本v2更新",
		"版本v3更新",
		"测试１２",
		"２测试１",
	} {
		r := testRecord(int64(i+1), 100, float64(i+1))
		r.RawMessage = text
		if err := s.PutMessage(r); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		query string
		want  []int64
	}{
		{"火锅", []int64{8, 7, 6, 5, 4, 1}},
		{"锅火", []int64{}},
		{"火，锅", []int64{8, 7, 6, 5, 4, 3, 2, 1}},
		{"火 锅", []int64{8, 7, 6, 5, 4, 3, 2, 1}},
		{"火锅，hello", []int64{7, 6, 5, 4, 1}},
		{"火锅world", []int64{7, 6, 5, 1}},
		{"hello world", []int64{7, 6, 5, 3, 2, 1}},
		{"hello OR world", []int64{6}},
		{"NEAR(hello world)", []int64{7}},
		{`"hello" -world*`, []int64{7, 6, 5, 3, 2, 1}},
		{"raw_message:火锅*", []int64{8}},
		{"hel", []int64{}},
		{"版本v2更新", []int64{9}},
		{"试１２", []int64{11}},
		{"", []int64{}},
		{" \t，。！？\"*()-:", []int64{}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			got, err := s.SearchMessages(tc.query, MessageFilter{})
			if err != nil || !reflect.DeepEqual(ids(got), tc.want) {
				t.Errorf("ids=%v, want %v, err=%v", ids(got), tc.want, err)
			}
		})
	}
}

func TestPutNotice(t *testing.T) {
	s := openTestStore(t)
	err := s.PutNotice(&NoticeRecord{
		NoticeType: "notify", SubType: "poke",
		ChatID: 100, UserID: 11, Raw: []byte(`{"post_type":"notice"}`), Time: 1729200000,
	})
	if err != nil {
		t.Fatalf("PutNotice: %v", err)
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM notices WHERE notice_type = 'notify'").Scan(&n); err != nil || n != 1 {
		t.Errorf("notice 落库异常: count = %d, err = %v", n, err)
	}
}

func ids(rs []*MessageRecord) []int64 {
	out := make([]int64, len(rs))
	for i, r := range rs {
		out[i] = r.MessageID
	}
	return out
}
