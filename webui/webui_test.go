// webui_test.go W1 WebUI 包骨架测试：静态页、Bearer 鉴权、概览 API。
// NapCat 不可达（request 未初始化）时概览字段置空不报错。
package webui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"Q-Mi/storage"
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
