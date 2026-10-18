package storage

import (
	"path/filepath"
	"reflect"
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

func ids(rs []*MessageRecord) []int64 {
	out := make([]int64, len(rs))
	for i, r := range rs {
		out[i] = r.MessageID
	}
	return out
}
