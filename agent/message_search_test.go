package agent

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestSearchMessagesHistoryFilters(t *testing.T) {
	store := openTestStore(t)
	tools, _, _ := testToolSet(t, store)
	start := time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)
	for _, rec := range []struct {
		id, chat, user int64
		kind, text     string
		minute         int
	}{
		{1, 888, 7001, "group", "约定前的闲聊", -1},
		{2, 888, 7001, "group", "约定周五见", 0},
		{3, 888, 7002, "group", "约定周六见", 1},
		{4, 888, 888, "private", "约定不要带到群里", 1},
		{5, 889, 7001, "group", "约定另一个群", 1},
		{6, 888, 7001, "group", "约定时间的上边界", 2},
		{7, 888, 7001, "group", "后来的闲聊", 3},
		{8, 888, 7002, "group", "撤回的约定", 1},
	} {
		// Deliberately duplicate names: sender identity must use QQ, not nickname.
		putRecord(t, store, rec.id, "recv", rec.kind, rec.chat, rec.user, "same-name", rec.text,
			start.Add(time.Duration(rec.minute)*time.Minute).Unix())
	}
	if err := store.MarkRecalled(8); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, args string
		want       []int64
	}{
		{"group-without-keyword", `{"chat_type":"group","chat_id":888}`, []int64{1, 2, 3, 6, 7}},
		{"private-without-keyword", `{"chat_type":"private","chat_id":888}`, []int64{4}},
		{"sender", `{"user_id":7001}`, []int64{1, 2, 5, 6, 7}},
		{"time-window", `{"chat_type":"group","chat_id":888,"since":"2026-11-09T02:00:00Z","until":"2026-11-09T02:02:00Z"}`, []int64{2, 3}},
		{"all-filters", `{"query":"约定","chat_type":"group","chat_id":888,"user_id":7001,"since":"2026-11-09T02:00:00Z","until":"2026-11-09T02:02:00Z"}`, []int64{2}},
		{"offset-zone", `{"chat_type":"group","chat_id":888,"since":"2026-11-09T10:00:00+08:00","until":"2026-11-09T10:02:00+08:00"}`, []int64{2, 3}},
		{"since-only", `{"chat_type":"group","chat_id":888,"since":"2026-11-09T02:02:00Z"}`, []int64{6, 7}},
		{"until-only", `{"chat_type":"group","chat_id":888,"until":"2026-11-09T02:00:00Z"}`, []int64{1}},
		{"local-time", searchWindowArgs(t, start.In(time.Local).Format("2006-01-02 15:04"), start.Add(2*time.Minute).In(time.Local).Format("2006-01-02 15:04:05")), []int64{2, 3}},
		{"latest-limited", `{"chat_type":"group","chat_id":888,"limit":2}`, []int64{6, 7}},
		{"keyword-trimmed", `{"query":"  周五  "}`, []int64{2}},
		{"blank-keyword-with-target", `{"query":" \n ","chat_type":"private","chat_id":888}`, []int64{4}},
		{"empty-result", `{"chat_type":"group","chat_id":9999}`, []int64{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := runToolOK(t, tools, "search_messages", tc.args)
			got := make([]int64, 0)
			for _, row := range messageRowsOf(t, out) {
				got = append(got, int64(row["message_id"].(float64)))
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("search returned %v, want %v: %s", got, tc.want, out)
			}
		})
	}
}

func TestSearchMessagesHistoryLocalDateWindow(t *testing.T) {
	store := openTestStore(t)
	tools, _, _ := testToolSet(t, store)
	midnight := time.Date(2026, 11, 9, 0, 0, 0, 0, time.Local)
	putRecord(t, store, 1, "recv", "group", 888, 7001, "friend", "before the day", midnight.Add(-time.Second).Unix())
	putRecord(t, store, 2, "recv", "group", 888, 7001, "friend", "start of the day", midnight.Unix())
	putRecord(t, store, 3, "recv", "group", 888, 7001, "friend", "end of the day", midnight.AddDate(0, 0, 1).Add(-time.Second).Unix())
	putRecord(t, store, 4, "recv", "group", 888, 7001, "friend", "next day", midnight.AddDate(0, 0, 1).Unix())
	rows := messageRowsOf(t, runToolOK(t, tools, "search_messages", `{"since":"2026-11-09","until":"2026-11-10"}`))
	if len(rows) != 2 || rows[0]["message_id"] != float64(2) || rows[1]["message_id"] != float64(3) {
		t.Fatalf("local dates must cover [midnight, next midnight): %#v", rows)
	}
}

func searchWindowArgs(t *testing.T, since, until string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"chat_type": "group", "chat_id": 888, "since": since, "until": until,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSearchMessagesHistoryRejectsInvalidFilters(t *testing.T) {
	tools, _, _ := testToolSet(t, openTestStore(t))
	for _, args := range []string{
		`{}`, `{"query":""}`, `{"query":" \t "}`, `{"limit":20}`,
		`{"query":"test","chat_type":"channel"}`,
		`{"chat_id":888}`,
		`{"query":"test","chat_id":-1}`, `{"query":"test","user_id":-1}`,
		`{"query":"test","since":"yesterday"}`, `{"until":"11-09 10:00"}`,
		`{"since":"2026-11-10","until":"2026-11-09"}`,
		`{"since":"2026-11-09","until":"2026-11-09"}`,
		`{"since":"2026-02-30"}`, `{"user_id":"not-a-number"}`, `{invalid`,
	} {
		t.Run(args, func(t *testing.T) {
			runToolErr(t, tools, "search_messages", args)
		})
	}
	runToolErr(t, buildTools(&ToolContext{}), "search_messages", `{"chat_type":"group","chat_id":888}`)
}

func TestSearchMessagesHistoryBoundsAndEvidence(t *testing.T) {
	store := openTestStore(t)
	tools, _, _ := testToolSet(t, store)
	for id := int64(1); id <= 65; id++ {
		putRecord(t, store, id, "recv", "group", 888, 7001, "same-name", "evidence", 1794189600+id)
	}
	for _, tc := range []struct {
		args  string
		count int
		first float64
	}{
		{`{"chat_type":"group","chat_id":888}`, 20, 46},
		{`{"chat_type":"group","chat_id":888,"limit":999}`, 50, 16},
		{`{"chat_type":"group","chat_id":888,"limit":0}`, 20, 46},
	} {
		rows := messageRowsOf(t, runToolOK(t, tools, "search_messages", tc.args))
		if len(rows) != tc.count || rows[0]["message_id"] != tc.first {
			t.Fatalf("wrong latest bounded window for %s: %v", tc.args, rows)
		}
		if rows[0]["user_id"] != float64(7001) {
			t.Fatalf("sender QQ missing from evidence: %#v", rows[0])
		}
		ts, ok := rows[0]["timestamp"].(string)
		if !ok {
			t.Fatalf("full timestamp missing from evidence: %#v", rows[0])
		}
		parsed, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil || parsed.Unix() != 1794189600+int64(tc.first) {
			t.Fatalf("invalid timestamp %q: %v", ts, err)
		}
	}
}

func TestSearchMessagesHistoryCanReadMutedArchiveWithoutChangingRecent(t *testing.T) {
	setDNDTestConfig(t)
	store := openTestStore(t)
	tools := buildTools(&ToolContext{Store: store, Turn: &Turn{SelfID: 999}})
	putRecord(t, store, 1, "recv", "group", 888, 7001, "friend", "archived ordinary message", 1)
	runToolOK(t, tools, "chat_dnd", `{"action":"add","chat_type":"group","chat_id":888}`)
	if out := runToolOK(t, tools, "get_recent_messages", `{"chat_type":"group","chat_id":888}`); len(messageRowsOf(t, out)) != 0 {
		t.Fatalf("recent query leaked muted messages: %s", out)
	}
	rows := messageRowsOf(t, runToolOK(t, tools, "search_messages", `{"chat_type":"group","chat_id":888,"user_id":7001}`))
	if len(rows) != 1 || rows[0]["message_id"] != float64(1) {
		t.Fatalf("explicit history lookup cannot read archive: %#v", rows)
	}
	if out := runToolOK(t, tools, "get_recent_messages", `{"chat_type":"group","chat_id":888}`); len(messageRowsOf(t, out)) != 0 {
		t.Fatalf("explicit search changed the DND setting: %s", out)
	}
}
