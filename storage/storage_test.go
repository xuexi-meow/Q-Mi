package storage

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestOpenCreatesSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	want := []string{"messages", "notices", "messages_fts"}
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

func TestOpenIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatalf("第一次 Open 失败: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	s2, err := Open(path) // 重复建表应幂等
	if err != nil {
		t.Fatalf("第二次 Open 失败: %v", err)
	}
	s2.Close()
}
