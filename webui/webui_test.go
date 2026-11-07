// webui_test.go W1 WebUI 包骨架测试：静态页、Bearer 鉴权、概览 API。
// NapCat 不可达（request 未初始化）时概览字段置空不报错。
package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"Q-Mi/agent"
	"Q-Mi/storage"
	"Q-Mi/utils/config"
	"Q-Mi/utils/request"
)

// openOverviewStore 进程内 SQLite 库（概览统计测试用）。
func openOverviewStore(t *testing.T) *storage.Store {
	t.Helper()
	s, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// startTestServer 起 WebUI（端口 0 = 随机端口），测试结束后 Shutdown。
func startTestServer(t *testing.T, token string) *http.Server {
	t.Helper()
	srv, err := Start(Config{Enable: true, Host: "127.0.0.1", Port: 0, Token: token})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv
}

func get(t *testing.T, url, token string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

// TestStartServesIndexAndStatic / 返回内嵌 index.html；/static/ 返回静态资源；
// 未知 /api 路由 404。
func TestStartServesIndexAndStatic(t *testing.T) {
	srv := startTestServer(t, "tok")
	base := "http://" + srv.Addr

	resp, body := get(t, base+"/", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET / Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(body, "Q-Mi") {
		t.Error("index 页应包含 Q-Mi 标识")
	}

	resp, body = get(t, base+"/static/alpine.min.js", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /static/alpine.min.js = %d, want 200", resp.StatusCode)
	}
	if len(body) == 0 {
		t.Error("alpine.min.js 不应为空")
	}

	resp, _ = get(t, base+"/api/nope", "tok")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /api/nope = %d, want 404", resp.StatusCode)
	}
}

// TestAPIRequiresBearerToken /api/ 无 token 或 token 不匹配 → 401。
func TestAPIRequiresBearerToken(t *testing.T) {
	srv := startTestServer(t, "sekrit")
	base := "http://" + srv.Addr

	for _, tc := range []struct {
		name  string
		token string
	}{
		{"no header", ""},
		{"wrong token", "wrong"},
		{"prefix only", "Bearer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := get(t, base+"/api/overview", tc.token)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("GET /api/overview (token=%q) = %d, want 401", tc.token, resp.StatusCode)
			}
		})
	}
}

// TestOverviewWithTokenAndStore 携带正确 token 时 /api/overview 返回 200，
// JSON 各字段存在；storage Stats 走真实内存库。
func TestOverviewWithTokenAndStore(t *testing.T) {
	s := openOverviewStore(t)
	if err := s.PutMessage(&storage.MessageRecord{MessageID: 1, Direction: "recv", ChatType: "group",
		ChatID: 888, UserID: 11, RawMessage: "你好", Time: 1729200000}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutMessage(&storage.MessageRecord{MessageID: 2, Direction: "recv", ChatType: "group",
		ChatID: 888, UserID: 12, RawMessage: "在吗", Time: 1729200001}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutNotice(&storage.NoticeRecord{NoticeType: "notify", SubType: "poke",
		ChatID: 888, UserID: 11, Raw: []byte(`{}`), Time: 1729200002}); err != nil {
		t.Fatal(err)
	}
	saved := storeProvider
	storeProvider = func() *storage.Store { return s }
	t.Cleanup(func() { storeProvider = saved })

	srv := startTestServer(t, "tok")
	resp, body := get(t, "http://"+srv.Addr+"/api/overview", "tok")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/overview = %d, want 200: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var ov map[string]any
	if err := json.Unmarshal([]byte(body), &ov); err != nil {
		t.Fatalf("响应不是合法 JSON: %v (%s)", err, body)
	}
	for _, key := range []string{"napcat", "uptime_seconds", "messages", "notices", "mood", "notes_count", "timers_count"} {
		if _, ok := ov[key]; !ok {
			t.Errorf("overview 缺少字段 %q: %s", key, body)
		}
	}
	if ov["messages"] != float64(2) {
		t.Errorf("messages = %v, want 2", ov["messages"])
	}
	if ov["notices"] != float64(1) {
		t.Errorf("notices = %v, want 1", ov["notices"])
	}
	napcat, ok := ov["napcat"].(map[string]any)
	if !ok {
		t.Fatalf("napcat 应为对象: %s", body)
	}
	if _, ok := napcat["online"]; !ok {
		t.Error("napcat.online 缺失")
	}
	if _, ok := napcat["version"]; !ok {
		t.Error("napcat.version 缺失")
	}
	if ov["uptime_seconds"].(float64) < 0 {
		t.Errorf("uptime_seconds = %v, want >= 0", ov["uptime_seconds"])
	}
}

// TestOverviewNapcatUnreachableNoStore request 未初始化（NapCat 不可达）、
// store 未启用（provider 返回 nil）时概览仍 200：napcat 字段置空、
// 消息/通知计数置 -1，不 panic。
func TestOverviewNapcatUnreachableNoStore(t *testing.T) {
	saved := storeProvider
	storeProvider = func() *storage.Store { return nil }
	t.Cleanup(func() { storeProvider = saved })

	srv := startTestServer(t, "tok")
	resp, body := get(t, "http://"+srv.Addr+"/api/overview", "tok")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/overview = %d, want 200: %s", resp.StatusCode, body)
	}
	var ov map[string]any
	if err := json.Unmarshal([]byte(body), &ov); err != nil {
		t.Fatalf("响应不是合法 JSON: %v (%s)", err, body)
	}
	if ov["messages"] != float64(-1) || ov["notices"] != float64(-1) {
		t.Errorf("无 store 时 messages/notices 应为 -1, got %v/%v (%s)", ov["messages"], ov["notices"], body)
	}
	napcat := ov["napcat"].(map[string]any)
	if napcat["online"] != false || napcat["version"] != "" {
		t.Errorf("NapCat 不可达时 napcat = %v, want online=false version=\"\"", napcat)
	}
}

// withTestStore 注入内存库到 storeProvider，测试结束恢复。
func withTestStore(t *testing.T, s *storage.Store) {
	t.Helper()
	saved := storeProvider
	storeProvider = func() *storage.Store { return s }
	t.Cleanup(func() { storeProvider = saved })
}

// seedStreamMessages 消息流测试数据：group/100 五条(时间递增)、
// private/77 一条、group/200 一条含关键词"火锅"。
func seedStreamMessages(t *testing.T, s *storage.Store) {
	t.Helper()
	recs := []*storage.MessageRecord{
		{MessageID: 1, Direction: "recv", ChatType: "group", ChatID: 100, UserID: 11, SenderName: "小明", RawMessage: "早上好", Time: 1729200001},
		{MessageID: 2, Direction: "sent", ChatType: "group", ChatID: 100, UserID: 999, SenderName: "我", RawMessage: "早", Time: 1729200002},
		{MessageID: 3, Direction: "recv", ChatType: "group", ChatID: 100, UserID: 11, SenderName: "小明", RawMessage: "中午吃啥", Time: 1729200003},
		{MessageID: 4, Direction: "recv", ChatType: "group", ChatID: 100, UserID: 12, SenderName: "小红", RawMessage: "吃火锅吧", Time: 1729200004, Recalled: true},
		{MessageID: 5, Direction: "recv", ChatType: "group", ChatID: 100, UserID: 11, SenderName: "小明", RawMessage: "晚上开黑", Time: 1729200005},
		{MessageID: 6, Direction: "recv", ChatType: "private", ChatID: 77, UserID: 77, SenderName: "阿七", RawMessage: "私聊在吗", Time: 1729200006},
		{MessageID: 7, Direction: "recv", ChatType: "group", ChatID: 200, UserID: 33, SenderName: "老王", RawMessage: "火锅走起", Time: 1729200007},
	}
	for _, r := range recs {
		if err := s.PutMessage(r); err != nil {
			t.Fatalf("seed %d: %v", r.MessageID, err)
		}
	}
}

// TestAPIChats GET /api/chats 返回会话摘要倒序列表。
func TestAPIChats(t *testing.T) {
	s := openOverviewStore(t)
	seedStreamMessages(t, s)
	withTestStore(t, s)
	srv := startTestServer(t, "tok")

	resp, body := get(t, "http://"+srv.Addr+"/api/chats", "tok")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/chats = %d, want 200: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var chats []map[string]any
	if err := json.Unmarshal([]byte(body), &chats); err != nil {
		t.Fatalf("响应不是合法 JSON 数组: %v (%s)", err, body)
	}
	if len(chats) != 3 {
		t.Fatalf("会话数 = %d, want 3: %s", len(chats), body)
	}
	// 按最新时间倒序: group/200 → private/77 → group/100
	wantOrder := []struct {
		chatType string
		chatID   float64
		count    float64
		lastText string
	}{
		{"group", 200, 1, "火锅走起"},
		{"private", 77, 1, "私聊在吗"},
		{"group", 100, 5, "晚上开黑"},
	}
	for i, w := range wantOrder {
		c := chats[i]
		if c["chat_type"] != w.chatType || c["chat_id"] != w.chatID ||
			c["count"] != w.count || c["last_text"] != w.lastText {
			t.Errorf("chats[%d] = %v, want chat=%s/%v count=%v last_text=%q", i, c, w.chatType, w.chatID, w.count, w.lastText)
		}
		if _, ok := c["last_time"]; !ok {
			t.Errorf("chats[%d] 缺少 last_time", i)
		}
	}
}

// TestAPIChatsNoStore store 未启用时 /api/chats 返回空数组而非 null。
func TestAPIChatsNoStore(t *testing.T) {
	withTestStore(t, nil)
	srv := startTestServer(t, "tok")

	resp, body := get(t, "http://"+srv.Addr+"/api/chats", "tok")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/chats = %d, want 200: %s", resp.StatusCode, body)
	}
	if body != "[]\n" {
		t.Errorf("无 store 时应返回空数组 [], 得到 %s", body)
	}
}

// TestAPIMessages GET /api/messages 分页查询：字段形态、limit 缺省/上限、
// before_time 游标、缺参 400。
func TestAPIMessages(t *testing.T) {
	s := openOverviewStore(t)
	seedStreamMessages(t, s)
	withTestStore(t, s)
	srv := startTestServer(t, "tok")
	base := "http://" + srv.Addr

	// 基本查询：新→旧、JSON 字段形态
	resp, body := get(t, base+"/api/messages?chat_type=group&chat_id=100", "tok")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/messages = %d, want 200: %s", resp.StatusCode, body)
	}
	var msgs []map[string]any
	if err := json.Unmarshal([]byte(body), &msgs); err != nil {
		t.Fatalf("响应不是合法 JSON: %v (%s)", err, body)
	}
	if len(msgs) != 4 { // 已撤回的 msg 4 默认排除
		t.Fatalf("消息数 = %d, want 4: %s", len(msgs), body)
	}
	m := msgs[0]
	if m["message_id"] != float64(5) || m["chat_type"] != "group" || m["chat_id"] != float64(100) ||
		m["sender_name"] != "小明" || m["text"] != "晚上开黑" || m["raw_message"] != "晚上开黑" ||
		m["time"] != float64(1729200005) || m["recalled"] != false {
		t.Errorf("消息字段形态错误: %v", m)
	}

	// limit 生效
	_, body = get(t, base+"/api/messages?chat_type=group&chat_id=100&limit=2", "tok")
	_ = json.Unmarshal([]byte(body), &msgs)
	if len(msgs) != 2 || msgs[0]["message_id"] != float64(5) || msgs[1]["message_id"] != float64(3) {
		t.Errorf("limit=2 应取最新 2 条: %s", body)
	}

	// before_time 游标：只取 time < 1729200003
	_, body = get(t, base+"/api/messages?chat_type=group&chat_id=100&before_time=1729200003", "tok")
	_ = json.Unmarshal([]byte(body), &msgs)
	if len(msgs) != 2 || msgs[0]["message_id"] != float64(2) || msgs[1]["message_id"] != float64(1) {
		t.Errorf("before_time 游标错误: %s", body)
	}

	// 缺 chat_id / 非法 chat_id → 400
	resp, _ = get(t, base+"/api/messages?chat_type=group", "tok")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("缺 chat_id = %d, want 400", resp.StatusCode)
	}
	resp, _ = get(t, base+"/api/messages?chat_type=group&chat_id=abc", "tok")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("chat_id 非法 = %d, want 400", resp.StatusCode)
	}

	// limit 非法值 → 默认 50,不报错;limit 超上限 → 截到 200
	resp, _ = get(t, base+"/api/messages?chat_type=group&chat_id=100&limit=999999", "tok")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("limit 超上限 = %d, want 200", resp.StatusCode)
	}
	resp, _ = get(t, base+"/api/messages?chat_type=group&chat_id=100&limit=abc", "tok")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("limit 非法 = %d, want 200", resp.StatusCode)
	}
}

// TestAPIMessagesLimitCap 210 条数据下 limit=1000 截到 200、缺省取 50。
func TestAPIMessagesLimitCap(t *testing.T) {
	s := openOverviewStore(t)
	for id := int64(1); id <= 210; id++ {
		if err := s.PutMessage(&storage.MessageRecord{MessageID: id, Direction: "recv",
			ChatType: "group", ChatID: 500, UserID: 1, RawMessage: "刷屏", Time: float64(1729200000 + id)}); err != nil {
			t.Fatal(err)
		}
	}
	withTestStore(t, s)
	srv := startTestServer(t, "tok")
	base := "http://" + srv.Addr + "/api/messages?chat_type=group&chat_id=500"

	_, body := get(t, base+"&limit=1000", "tok")
	var msgs []map[string]any
	_ = json.Unmarshal([]byte(body), &msgs)
	if len(msgs) != 200 {
		t.Errorf("limit=1000 应截到 200, 得到 %d", len(msgs))
	}
	_, body = get(t, base, "tok")
	_ = json.Unmarshal([]byte(body), &msgs)
	if len(msgs) != 50 {
		t.Errorf("缺省 limit 应为 50, 得到 %d", len(msgs))
	}
}

// TestAPISearch POST /api/search：body 解析、过滤、limit 缺省与上限、
// 非法 body / 空 query 400、GET 405。
func TestAPISearch(t *testing.T) {
	s := openOverviewStore(t)
	seedStreamMessages(t, s)
	withTestStore(t, s)
	srv := startTestServer(t, "tok")
	base := "http://" + srv.Addr

	post := func(t *testing.T, url, body string) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer tok")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp, string(b)
	}

	// 基本搜索：msg 7 命中（msg 4"吃火锅吧"已撤回,默认排除）
	resp, body := post(t, base+"/api/search", `{"query":"火锅"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/search = %d, want 200: %s", resp.StatusCode, body)
	}
	var msgs []map[string]any
	if err := json.Unmarshal([]byte(body), &msgs); err != nil {
		t.Fatalf("响应不是合法 JSON: %v (%s)", err, body)
	}
	if len(msgs) != 1 || msgs[0]["message_id"] != float64(7) {
		t.Errorf("搜索结果错误: %s", body)
	}

	// 过滤：命中 msg 1；换 chat_id 后无结果
	_, body = post(t, base+"/api/search", `{"query":"早上","chat_type":"group","chat_id":100,"limit":1}`)
	_ = json.Unmarshal([]byte(body), &msgs)
	if len(msgs) != 1 || msgs[0]["message_id"] != float64(1) {
		t.Errorf("过滤错误: %s", body)
	}
	_, body = post(t, base+"/api/search", `{"query":"早上","chat_id":200}`)
	_ = json.Unmarshal([]byte(body), &msgs)
	if len(msgs) != 0 {
		t.Errorf("chat_id 过滤后应无结果: %s", body)
	}

	// 非法 JSON → 400
	resp, _ = post(t, base+"/api/search", `{bad json`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("非法 JSON = %d, want 400", resp.StatusCode)
	}
	// 空 query → 400
	resp, _ = post(t, base+"/api/search", `{"query":""}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("空 query = %d, want 400", resp.StatusCode)
	}
	// GET → 405
	resp, _ = get(t, base+"/api/search", "tok")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/search = %d, want 405", resp.StatusCode)
	}
}

// TestAPISearchLimitCap 120 条命中下 limit=500 截到 100。
func TestAPISearchLimitCap(t *testing.T) {
	s := openOverviewStore(t)
	for id := int64(1); id <= 120; id++ {
		if err := s.PutMessage(&storage.MessageRecord{MessageID: id, Direction: "recv",
			ChatType: "group", ChatID: 600, UserID: 1, RawMessage: "大海捞针", Time: float64(1729200000 + id)}); err != nil {
			t.Fatal(err)
		}
	}
	withTestStore(t, s)
	srv := startTestServer(t, "tok")

	req, _ := http.NewRequest(http.MethodPost, "http://"+srv.Addr+"/api/search",
		strings.NewReader(`{"query":"捞针","limit":500}`))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var msgs []map[string]any
	_ = json.Unmarshal(body, &msgs)
	if len(msgs) != 100 {
		t.Errorf("limit=500 应截到 100, 得到 %d: %.100s", len(msgs), body)
	}
}

// TestAPIMessageFields 消息行补齐 direction/user_id 字段（recv/sent 区分与
// 昵称(QQ号) 展示用），且 text 应为 CQ:image 码替换为 [图片] 的展示文本
// （raw_message 保留原始 CQ 码）。
func TestAPIMessageFields(t *testing.T) {
	s := openOverviewStore(t)
	if err := s.PutMessage(&storage.MessageRecord{MessageID: 1, Direction: "recv",
		ChatType: "group", ChatID: 100, UserID: 11, SenderName: "小明",
		RawMessage: "[CQ:image,file=a.image,subType=0,url=https://x/y] 看这张图",
		Time:       1729200001}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutMessage(&storage.MessageRecord{MessageID: 2, Direction: "sent",
		ChatType: "group", ChatID: 100, UserID: 999, SenderName: "我",
		RawMessage: "收到", Time: 1729200002}); err != nil {
		t.Fatal(err)
	}
	withTestStore(t, s)
	srv := startTestServer(t, "tok")

	_, body := get(t, "http://"+srv.Addr+"/api/messages?chat_type=group&chat_id=100", "tok")
	var msgs []map[string]any
	if err := json.Unmarshal([]byte(body), &msgs); err != nil {
		t.Fatalf("响应不是合法 JSON: %v (%s)", err, body)
	}
	if len(msgs) != 2 {
		t.Fatalf("消息数 = %d, want 2: %s", len(msgs), body)
	}
	byID := map[int64]map[string]any{}
	for _, m := range msgs {
		byID[int64(m["message_id"].(float64))] = m
	}
	m1 := byID[1]
	if m1 == nil {
		t.Fatalf("缺少 message_id=1: %s", body)
	}
	if m1["direction"] != "recv" {
		t.Errorf("direction = %v, want recv（前端 recv/sent 区分依赖该字段）", m1["direction"])
	}
	if m1["user_id"] != float64(11) {
		t.Errorf("user_id = %v, want 11（前端 昵称(QQ号) 展示依赖该字段）", m1["user_id"])
	}
	if m1["text"] != "[图片] 看这张图" {
		t.Errorf("text = %q, want %q（CQ:image 应替换为 [图片]）", m1["text"], "[图片] 看这张图")
	}
	if m1["raw_message"] != "[CQ:image,file=a.image,subType=0,url=https://x/y] 看这张图" {
		t.Errorf("raw_message 应保留原始 CQ 码: %v", m1["raw_message"])
	}
	m2 := byID[2]
	if m2 == nil {
		t.Fatalf("缺少 message_id=2: %s", body)
	}
	if m2["direction"] != "sent" {
		t.Errorf("direction = %v, want sent", m2["direction"])
	}
	if m2["text"] != "收到" {
		t.Errorf("无 CQ 码时 text 应与 raw_message 一致: %v", m2["text"])
	}
}

// TestAPIMessagesNoStore store 未启用时 /api/messages 返回空数组而非 500/null。
func TestAPIMessagesNoStore(t *testing.T) {
	withTestStore(t, nil)
	srv := startTestServer(t, "tok")

	resp, body := get(t, "http://"+srv.Addr+"/api/messages?chat_type=group&chat_id=100", "tok")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("无 store GET /api/messages = %d, want 200: %s", resp.StatusCode, body)
	}
	if body != "[]\n" {
		t.Errorf("无 store 时应返回空数组 [], 得到 %s", body)
	}
}

// TestAPISearchNoStore store 未启用时 /api/search 返回空数组而非 500/null。
func TestAPISearchNoStore(t *testing.T) {
	withTestStore(t, nil)
	srv := startTestServer(t, "tok")

	req, _ := http.NewRequest(http.MethodPost, "http://"+srv.Addr+"/api/search",
		strings.NewReader(`{"query":"任意"}`))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("无 store POST /api/search = %d, want 200: %s", resp.StatusCode, body)
	}
	if string(body) != "[]\n" {
		t.Errorf("无 store 时应返回空数组 [], 得到 %s", body)
	}
}

// TestAPIEvents /api/events 透传 agent.EventsAfter：agent 未启用时返回
// 200 + JSON 数组；after 参数缺失/非法按 0 处理不报错。
func TestAPIEvents(t *testing.T) {
	srv := startTestServer(t, "tok")
	base := "http://" + srv.Addr

	for _, tc := range []string{"", "?after=0", "?after=abc", "?after=-5"} {
		resp, body := get(t, base+"/api/events"+tc, "tok")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/events%s = %d, want 200: %s", tc, resp.StatusCode, body)
		}
		var events []map[string]any
		if err := json.Unmarshal([]byte(body), &events); err != nil {
			t.Fatalf("after=%q 响应不是 JSON 数组: %v (%s)", tc, err, body)
		}
		if len(events) != 0 {
			t.Errorf("after=%q agent 未启用应为空数组, 得到 %s", tc, body)
		}
	}
}

// ================= W3 agent 控制页（notes/mood/timers/wake/file） =================

// recBuilder 记录 Build 调用的桩 builder（webui 测试构造真实 Manager 用）。
type recBuilder struct {
	mu    sync.Mutex
	calls int
}

func (b *recBuilder) Build(turn *agent.Turn) []agent.ChatMessage {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	return []agent.ChatMessage{{Role: "user", Content: "hi"}}
}

func (b *recBuilder) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

// withTestAgent 注入真实 Manager（沿用 W1 的 manager 注入模式，经
// agent.SetManagerForTest）：stub builder + 假 LLM、毫秒级防抖；t.Chdir 使
// notes/state/timers/persona/guide 等默认相对路径全部落到临时目录。
func withTestAgent(t *testing.T, selfID int64) *recBuilder {
	t.Helper()
	t.Chdir(t.TempDir())
	b := &recBuilder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"pong"}}]}`))
	}))
	t.Cleanup(srv.Close)
	cfg := config.AgentConfig{Enable: true, Model: "test-model", Debounce: 5 * time.Millisecond}
	m := agent.NewManager(b, agent.NewClient(srv.URL, "sk-test", 5*time.Second), cfg, nil, selfID)
	t.Cleanup(agent.SetManagerForTest(m))
	return b
}

// sendJSON 带鉴权发送任意方法 + JSON body 的请求，返回响应与 body 文本。
func sendJSON(t *testing.T, method, u, token, body string) (*http.Response, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, u, rd)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(b)
}

// TestAPINotesW3 /api/notes 增删查全链路；空 text/非法 body/越界删除 400、
// 不支持的方法 405。
func TestAPINotesW3(t *testing.T) {
	withTestAgent(t, 999)
	srv := startTestServer(t, "tok")
	base := "http://" + srv.Addr

	// 初始为空数组（非 null）
	resp, body := get(t, base+"/api/notes", "tok")
	if resp.StatusCode != http.StatusOK || body != "[]\n" {
		t.Fatalf("GET /api/notes 初始 = %d %s, want 200 []", resp.StatusCode, body)
	}

	// 新增两条
	for _, text := range []string{"买奶茶", "修 WebUI"} {
		resp, body = sendJSON(t, http.MethodPost, base+"/api/notes", "tok",
			fmt.Sprintf(`{"text":%q}`, text))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /api/notes (%s) = %d, want 200: %s", text, resp.StatusCode, body)
		}
	}

	// 列表形态：index/time/text
	_, body = get(t, base+"/api/notes", "tok")
	var notes []map[string]any
	if err := json.Unmarshal([]byte(body), &notes); err != nil {
		t.Fatalf("响应不是 JSON 数组: %v (%s)", err, body)
	}
	if len(notes) != 2 || notes[0]["text"] != "买奶茶" || notes[1]["text"] != "修 WebUI" {
		t.Fatalf("notes = %s", body)
	}
	if _, ok := notes[0]["index"].(float64); !ok {
		t.Errorf("notes[0].index 应为数字: %v", notes[0])
	}
	if _, ok := notes[0]["time"].(float64); !ok {
		t.Errorf("notes[0].time 应为 unix 秒数字: %v", notes[0])
	}

	// 删除下标 1 → 剩「买奶茶」
	resp, body = sendJSON(t, http.MethodDelete, base+"/api/notes/1", "tok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /api/notes/1 = %d, want 200: %s", resp.StatusCode, body)
	}
	_, body = get(t, base+"/api/notes", "tok")
	_ = json.Unmarshal([]byte(body), &notes)
	if len(notes) != 1 || notes[0]["text"] != "买奶茶" {
		t.Errorf("删除后 notes = %s, want 剩「买奶茶」", body)
	}

	// 校验失败
	resp, _ = sendJSON(t, http.MethodPost, base+"/api/notes", "tok", `{"text":"  "}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("空 text = %d, want 400", resp.StatusCode)
	}
	resp, _ = sendJSON(t, http.MethodPost, base+"/api/notes", "tok", `{bad`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("非法 JSON = %d, want 400", resp.StatusCode)
	}
	resp, _ = sendJSON(t, http.MethodDelete, base+"/api/notes/99", "tok", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("越界删除 = %d, want 400", resp.StatusCode)
	}
	resp, _ = sendJSON(t, http.MethodDelete, base+"/api/notes/abc", "tok", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("非数字下标 = %d, want 400", resp.StatusCode)
	}
	resp, _ = sendJSON(t, http.MethodPut, base+"/api/notes", "tok", `{"text":"x"}`)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("PUT /api/notes = %d, want 405", resp.StatusCode)
	}
}

// TestAPINotesNoAgent agent 未启用：GET 200 空数组、POST 503 带友好提示。
func TestAPINotesNoAgent(t *testing.T) {
	srv := startTestServer(t, "tok")
	base := "http://" + srv.Addr

	resp, body := get(t, base+"/api/notes", "tok")
	if resp.StatusCode != http.StatusOK || body != "[]\n" {
		t.Fatalf("GET /api/notes（未启用）= %d %s, want 200 []", resp.StatusCode, body)
	}
	resp, body = sendJSON(t, http.MethodPost, base+"/api/notes", "tok", `{"text":"x"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /api/notes（未启用）= %d, want 503: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "agent 未启用") {
		t.Errorf("503 响应应含友好提示: %s", body)
	}
}

// TestAPIMoodW3 /api/mood 读写往返；非法 body 400、不支持方法 405。
func TestAPIMoodW3(t *testing.T) {
	withTestAgent(t, 999)
	srv := startTestServer(t, "tok")
	base := "http://" + srv.Addr

	var mood struct {
		Mood string `json:"mood"`
	}
	resp, body := get(t, base+"/api/mood", "tok")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/mood = %d: %s", resp.StatusCode, body)
	}
	_ = json.Unmarshal([]byte(body), &mood)
	if mood.Mood != "" {
		t.Errorf("初始 mood = %q, want 空串", mood.Mood)
	}

	resp, body = sendJSON(t, http.MethodPost, base+"/api/mood", "tok", `{"mood":"开心"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/mood = %d: %s", resp.StatusCode, body)
	}
	_, body = get(t, base+"/api/mood", "tok")
	_ = json.Unmarshal([]byte(body), &mood)
	if mood.Mood != "开心" {
		t.Errorf("保存后 mood = %q, want 开心", mood.Mood)
	}

	resp, _ = sendJSON(t, http.MethodPost, base+"/api/mood", "tok", `{bad`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("非法 JSON = %d, want 400", resp.StatusCode)
	}
	resp, _ = sendJSON(t, http.MethodDelete, base+"/api/mood", "tok", "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("DELETE /api/mood = %d, want 405", resp.StatusCode)
	}
}

// TestAPITimersW3 /api/timers 列表/新增/取消；延迟 0、超 7 天、非数字 400。
func TestAPITimersW3(t *testing.T) {
	withTestAgent(t, 999)
	srv := startTestServer(t, "tok")
	base := "http://" + srv.Addr

	resp, body := get(t, base+"/api/timers", "tok")
	if resp.StatusCode != http.StatusOK || body != "[]\n" {
		t.Fatalf("GET /api/timers 初始 = %d %s, want 200 []", resp.StatusCode, body)
	}

	resp, body = sendJSON(t, http.MethodPost, base+"/api/timers", "tok",
		`{"delay_seconds":60,"note":"喝水"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/timers = %d: %s", resp.StatusCode, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.ID == "" {
		t.Fatalf("POST 响应应含非空 id: %s (%v)", body, err)
	}

	_, body = get(t, base+"/api/timers", "tok")
	var timers []map[string]any
	if err := json.Unmarshal([]byte(body), &timers); err != nil {
		t.Fatalf("响应不是 JSON 数组: %v (%s)", err, body)
	}
	if len(timers) != 1 || timers[0]["note"] != "喝水" ||
		timers[0]["chat_type"] != "private" || timers[0]["chat_id"] != float64(999) {
		t.Errorf("timers = %s, want 1 条 private/999「喝水」", body)
	}
	if _, ok := timers[0]["fire_at"].(string); !ok {
		t.Errorf("fire_at 应为字符串时间: %v", timers[0])
	}

	for _, tc := range []string{
		`{"delay_seconds":0,"note":"x"}`,
		`{"delay_seconds":604801,"note":"x"}`,
		`{"delay_seconds":"abc","note":"x"}`,
		`{bad`,
	} {
		resp, _ = sendJSON(t, http.MethodPost, base+"/api/timers", "tok", tc)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST /api/timers %s = %d, want 400", tc, resp.StatusCode)
		}
	}

	resp, body = sendJSON(t, http.MethodDelete, base+"/api/timers/"+created.ID, "tok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /api/timers/{id} = %d: %s", resp.StatusCode, body)
	}
	resp, _ = sendJSON(t, http.MethodDelete, base+"/api/timers/"+created.ID, "tok", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("重复取消 = %d, want 400", resp.StatusCode)
	}
}

// TestAPITimersWakeNoAgent agent 未启用：timers POST / wake 503，timers GET 200。
func TestAPITimersWakeNoAgent(t *testing.T) {
	srv := startTestServer(t, "tok")
	base := "http://" + srv.Addr

	resp, body := sendJSON(t, http.MethodPost, base+"/api/timers", "tok", `{"delay_seconds":60,"note":"x"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("POST /api/timers（未启用）= %d, want 503: %s", resp.StatusCode, body)
	}
	resp, body = sendJSON(t, http.MethodPost, base+"/api/wake", "tok", `{"text":"x"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("POST /api/wake（未启用）= %d, want 503: %s", resp.StatusCode, body)
	}
	resp, _ = get(t, base+"/api/timers", "tok")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/timers（未启用）= %d, want 200", resp.StatusCode)
	}
}

// TestAPIWakeW3 手动唤醒：入队成功并驱动一个 turn；空文本/非法 body 400、GET 405。
func TestAPIWakeW3(t *testing.T) {
	b := withTestAgent(t, 999)
	srv := startTestServer(t, "tok")
	base := "http://" + srv.Addr

	resp, body := sendJSON(t, http.MethodPost, base+"/api/wake", "tok", `{"text":"起来干活"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/wake = %d: %s", resp.StatusCode, body)
	}
	// 防抖（5ms）后应驱动一个 turn
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b.count() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if b.count() < 1 {
		t.Error("wake 后应驱动一个 turn（builder 被调用）")
	}

	resp, _ = sendJSON(t, http.MethodPost, base+"/api/wake", "tok", `{"text":"   "}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("空 text = %d, want 400", resp.StatusCode)
	}
	resp, _ = sendJSON(t, http.MethodPost, base+"/api/wake", "tok", `{bad`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("非法 JSON = %d, want 400", resp.StatusCode)
	}
	resp, _ = get(t, base+"/api/wake", "tok")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/wake = %d, want 405", resp.StatusCode)
	}
}

// TestAPIFileW3 persona/guide 白名单编辑器：GET 缺文件返回空内容、PUT 覆盖写
// （0600）、白名单外/缺参数 400（杜绝路径穿越）。
func TestAPIFileW3(t *testing.T) {
	t.Chdir(t.TempDir()) // 白名单路径 agent/persona.md、agent/guide.md 落到临时目录
	srv := startTestServer(t, "tok")
	base := "http://" + srv.Addr

	var fileResp struct {
		Content string `json:"content"`
	}

	// 缺文件 → 200 空内容
	resp, body := get(t, base+"/api/file?name=persona", "tok")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/file?name=persona = %d: %s", resp.StatusCode, body)
	}
	_ = json.Unmarshal([]byte(body), &fileResp)
	if fileResp.Content != "" {
		t.Errorf("缺文件 content = %q, want 空串", fileResp.Content)
	}

	// PUT 覆盖写
	resp, body = sendJSON(t, http.MethodPut, base+"/api/file", "tok",
		`{"name":"persona","content":"你是 Q-Mi。"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/file = %d: %s", resp.StatusCode, body)
	}
	data, err := os.ReadFile(filepath.Join("agent", "persona.md"))
	if err != nil {
		t.Fatalf("persona.md 应已写入: %v", err)
	}
	if string(data) != "你是 Q-Mi。" {
		t.Errorf("persona.md 内容 = %q", string(data))
	}
	// 0600 权限由 os.WriteFile 保证；Windows 不模拟 unix 权限位，仅非 Windows 断言
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(filepath.Join("agent", "persona.md")); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("persona.md 权限 = %v, want 0600（err=%v）", info, err)
		}
	}

	// GET 回读
	_, body = get(t, base+"/api/file?name=persona", "tok")
	_ = json.Unmarshal([]byte(body), &fileResp)
	if fileResp.Content != "你是 Q-Mi。" {
		t.Errorf("GET 回读 content = %q", fileResp.Content)
	}

	// 白名单外 → 400（GET 与 PUT 都拒绝）
	for _, name := range []string{"secrets", "../Config.yaml", "notes.json"} {
		resp, _ = get(t, base+"/api/file?name="+url.QueryEscape(name), "tok")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET name=%q = %d, want 400", name, resp.StatusCode)
		}
		resp, _ = sendJSON(t, http.MethodPut, base+"/api/file", "tok",
			fmt.Sprintf(`{"name":%q,"content":"x"}`, name))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("PUT name=%q = %d, want 400", name, resp.StatusCode)
		}
	}

	// 缺 name / 非法 body / 缺字段 / POST
	resp, _ = get(t, base+"/api/file", "tok")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("GET 缺 name = %d, want 400", resp.StatusCode)
	}
	resp, _ = sendJSON(t, http.MethodPut, base+"/api/file", "tok", `{bad`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PUT 非法 JSON = %d, want 400", resp.StatusCode)
	}
	resp, _ = sendJSON(t, http.MethodPut, base+"/api/file", "tok", `{"content":"x"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PUT 缺 name = %d, want 400", resp.StatusCode)
	}
	resp, _ = sendJSON(t, http.MethodPost, base+"/api/file", "tok", `{"name":"persona","content":"x"}`)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/file = %d, want 405", resp.StatusCode)
	}
}

// ================= W4 QQ 管理页（friends/groups/members/napcat 直调） =================

// napcatMock 假 NapCat：记录收到的 path/鉴权头/请求体与调用次数，回复固定
// retcode=0 信封（data 为构造时给定的 JSON 片段），并把 request 层客户端指向它。
type napcatMock struct {
	Path  string
	Auth  string
	Body  map[string]any
	Calls int
}

// newNapcatMock 起假 NapCat 并初始化 request 客户端（NapCat token 固定 nap-tok）。
func newNapcatMock(t *testing.T, data string) *napcatMock {
	t.Helper()
	m := &napcatMock{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.Path = r.URL.Path
		m.Auth = r.Header.Get("Authorization")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.Body = body
		m.Calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":` + data + `,"message":"","wording":"","stream":"normal-action"}`))
	}))
	t.Cleanup(ts.Close)
	pointRequestAt(t, ts.URL)
	return m
}

// pointRequestAt 把 request 层默认客户端指向给定 httptest base URL。
func pointRequestAt(t *testing.T, baseURL string) {
	t.Helper()
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Napcat{HTTP: config.NapcatConn{Enable: true, Host: u.Hostname(), Port: port, Token: "nap-tok"}}
	if err := request.Init(cfg); err != nil {
		t.Fatalf("request.Init: %v", err)
	}
}

// TestAPIFriendsW4 GET /api/friends 走假 NapCat：断言转发路径/鉴权与字段提取。
func TestAPIFriendsW4(t *testing.T) {
	m := newNapcatMock(t, `[{"user_id":11,"nickname":"小明","remark":"老友","level":3}]`)
	srv := startTestServer(t, "tok")

	resp, body := get(t, "http://"+srv.Addr+"/api/friends", "tok")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/friends = %d, want 200: %s", resp.StatusCode, body)
	}
	var friends []map[string]any
	if err := json.Unmarshal([]byte(body), &friends); err != nil {
		t.Fatalf("响应不是 JSON 数组: %v (%s)", err, body)
	}
	if len(friends) != 1 {
		t.Fatalf("好友数 = %d, want 1: %s", len(friends), body)
	}
	f := friends[0]
	if f["user_id"] != float64(11) || f["nickname"] != "小明" || f["remark"] != "老友" || f["level"] != float64(3) {
		t.Errorf("好友字段提取错误: %v", f)
	}
	if m.Path != "/get_friend_list" {
		t.Errorf("NapCat 收到路径 = %s, want /get_friend_list", m.Path)
	}
	if m.Auth != "Bearer nap-tok" {
		t.Errorf("NapCat 鉴权头 = %q, want Bearer nap-tok", m.Auth)
	}
}

// TestAPIGroupsW4 GET /api/groups 走假 NapCat：断言路径与字段提取。
func TestAPIGroupsW4(t *testing.T) {
	m := newNapcatMock(t, `[{"group_id":100,"group_name":"测试群","member_count":42}]`)
	srv := startTestServer(t, "tok")

	resp, body := get(t, "http://"+srv.Addr+"/api/groups", "tok")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/groups = %d, want 200: %s", resp.StatusCode, body)
	}
	var groups []map[string]any
	if err := json.Unmarshal([]byte(body), &groups); err != nil {
		t.Fatalf("响应不是 JSON 数组: %v (%s)", err, body)
	}
	if len(groups) != 1 {
		t.Fatalf("群数 = %d, want 1: %s", len(groups), body)
	}
	g := groups[0]
	if g["group_id"] != float64(100) || g["group_name"] != "测试群" || g["member_count"] != float64(42) {
		t.Errorf("群字段提取错误: %v", g)
	}
	if m.Path != "/get_group_list" {
		t.Errorf("NapCat 收到路径 = %s, want /get_group_list", m.Path)
	}
}

// TestAPIMembersW4 GET /api/members?group_id= 走假 NapCat：断言路径/参数、
// 超过 100 条时截断并带 total/truncated 标记。
func TestAPIMembersW4(t *testing.T) {
	var b strings.Builder
	b.WriteByte('[')
	for i := 1; i <= 105; i++ {
		if i > 1 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"user_id":%d,"nickname":"成员%d","card":"","role":"member"}`, i, i)
	}
	b.WriteByte(']')
	m := newNapcatMock(t, b.String())
	srv := startTestServer(t, "tok")

	resp, body := get(t, "http://"+srv.Addr+"/api/members?group_id=888", "tok")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/members = %d, want 200: %.200s", resp.StatusCode, body)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("响应不是 JSON 对象: %v (%.200s)", err, body)
	}
	if raw["total"] != float64(105) || raw["returned"] != float64(100) || raw["truncated"] != true {
		t.Errorf("total/returned/truncated = %v/%v/%v, want 105/100/true", raw["total"], raw["returned"], raw["truncated"])
	}
	members, ok := raw["members"].([]any)
	if !ok || len(members) != 100 {
		t.Fatalf("members 应为 100 条, got %v", raw["members"])
	}
	first := members[0].(map[string]any)
	if first["user_id"] != float64(1) || first["nickname"] != "成员1" {
		t.Errorf("members[0] 字段错误: %v", first)
	}
	if m.Path != "/get_group_member_list" {
		t.Errorf("NapCat 收到路径 = %s, want /get_group_member_list", m.Path)
	}
	if m.Body["group_id"] != float64(888) {
		t.Errorf("NapCat 收到 group_id = %v, want 888", m.Body["group_id"])
	}
}

// TestAPIMembersRequiresGroupIDW4 缺 group_id 或非法值 → 400，不触达 NapCat。
func TestAPIMembersRequiresGroupIDW4(t *testing.T) {
	m := newNapcatMock(t, `[]`)
	srv := startTestServer(t, "tok")
	base := "http://" + srv.Addr

	for _, q := range []string{"", "?group_id=", "?group_id=abc", "?group_id=0"} {
		resp, _ := get(t, base+"/api/members"+q, "tok")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET /api/members%q = %d, want 400", q, resp.StatusCode)
		}
	}
	if m.Calls != 0 {
		t.Errorf("参数校验失败不应触达 NapCat, calls = %d", m.Calls)
	}
}

// TestAPINapcatW4 POST /api/napcat 直调：白名单内放行透传、白名单外 400、
// 危险动作无 confirm 400 且不触达 NapCat、带 confirm 放行且 confirm 不透传、
// GET 返回白名单全集与危险动作清单。
func TestAPINapcatW4(t *testing.T) {
	m := newNapcatMock(t, `{"done":true}`)
	srv := startTestServer(t, "tok")
	base := "http://" + srv.Addr

	// 白名单内：动作名与参数透传，data 原样返回
	resp, body := sendJSON(t, http.MethodPost, base+"/api/napcat", "tok",
		`{"action":"send_like","params":{"user_id":11,"times":1}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/napcat = %d, want 200: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `"done":true`) {
		t.Errorf("data 应原样透传: %s", body)
	}
	if m.Path != "/send_like" {
		t.Errorf("NapCat 收到路径 = %s, want /send_like", m.Path)
	}
	if m.Body["user_id"] != float64(11) || m.Body["times"] != float64(1) {
		t.Errorf("params 透传错误: %v", m.Body)
	}

	// 白名单外 → 400
	resp, _ = sendJSON(t, http.MethodPost, base+"/api/napcat", "tok", `{"action":"no_such_action"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("未知动作 = %d, want 400", resp.StatusCode)
	}

	// 危险动作无 confirm → 400 且不触达 NapCat
	callsBefore := m.Calls
	resp, body = sendJSON(t, http.MethodPost, base+"/api/napcat", "tok",
		`{"action":"set_group_kick","params":{"group_id":1,"user_id":2}}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("危险动作无 confirm = %d, want 400: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "confirm") {
		t.Errorf("400 提示应说明需要 confirm: %s", body)
	}
	if m.Calls != callsBefore {
		t.Errorf("危险动作无 confirm 不应触达 NapCat, calls = %d", m.Calls)
	}

	// 危险动作带 confirm=true → 放行，且 confirm 不透传给 NapCat
	resp, body = sendJSON(t, http.MethodPost, base+"/api/napcat", "tok",
		`{"action":"set_group_kick","params":{"group_id":1,"user_id":2,"confirm":true}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("危险动作带 confirm = %d, want 200: %s", resp.StatusCode, body)
	}
	if m.Path != "/set_group_kick" {
		t.Errorf("NapCat 收到路径 = %s, want /set_group_kick", m.Path)
	}
	if m.Body["user_id"] != float64(2) {
		t.Errorf("params 透传错误: %v", m.Body)
	}
	if _, has := m.Body["confirm"]; has {
		t.Errorf("confirm 不应透传给 NapCat: %v", m.Body)
	}

	// GET 返回白名单全集与危险动作清单（前端下拉用）
	resp, body = get(t, base+"/api/napcat", "tok")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/napcat = %d, want 200: %s", resp.StatusCode, body)
	}
	var list struct {
		Actions   []string `json:"actions"`
		Dangerous []string `json:"dangerous"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("响应不是 JSON 对象: %v (%s)", err, body)
	}
	if len(list.Actions) < 100 {
		t.Errorf("actions 全集应覆盖全部动作, got %d 个", len(list.Actions))
	}
	for _, want := range []string{"send_like", "get_friend_list", "get_group_list", "get_group_member_list"} {
		if !containsStr(list.Actions, want) {
			t.Errorf("actions 缺少 %q", want)
		}
	}
	for _, want := range []string{"set_group_kick", "set_group_leave", "delete_friend", "set_qq_avatar", "set_qq_profile", "set_group_name"} {
		if !containsStr(list.Dangerous, want) {
			t.Errorf("dangerous 缺少 %q", want)
		}
	}
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestAPINapcatUnreachableW4 NapCat 不可达（连接拒绝）时 friends 与 napcat
// 直调返回 502 + 简短 JSON 错误（非 500 堆栈）。
func TestAPINapcatUnreachableW4(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close() // 立刻关闭 → 该端口连接拒绝
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	if err := request.Init(&config.Napcat{HTTP: config.NapcatConn{
		Enable: true, Host: host, Port: port, Token: "nap-tok"}}); err != nil {
		t.Fatal(err)
	}

	srv := startTestServer(t, "tok")
	base := "http://" + srv.Addr

	resp, body := get(t, base+"/api/friends", "tok")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("NapCat 不可达 GET /api/friends = %d, want 502: %s", resp.StatusCode, body)
	}
	var e1 map[string]any
	if json.Unmarshal([]byte(body), &e1) != nil || e1["error"] == "" {
		t.Errorf("502 应为 {\"error\":...} 简短 JSON: %s", body)
	}

	resp, body = sendJSON(t, http.MethodPost, base+"/api/napcat", "tok", `{"action":"send_like"}`)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("NapCat 不可达 POST /api/napcat = %d, want 502: %s", resp.StatusCode, body)
	}
	var e2 map[string]any
	if json.Unmarshal([]byte(body), &e2) != nil || e2["error"] == "" {
		t.Errorf("502 应为 {\"error\":...} 简短 JSON: %s", body)
	}
}
