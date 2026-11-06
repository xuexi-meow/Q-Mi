package bot

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"Q-Mi/app"
	"Q-Mi/chain"
	"Q-Mi/event/message"
	"Q-Mi/utils/config"
	"Q-Mi/utils/request"
)

func seg(t *testing.T, raw string) *message.Message {
	t.Helper()
	var m message.Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if _, err := m.TypeHandler(); err != nil {
		t.Fatal(err)
	}
	return &m
}

func TestTextExtractsOnlyTextSegments(t *testing.T) {
	msgs := []*message.Message{
		seg(t, `{"type":"reply","data":{"id":"42"}}`),
		seg(t, `{"type":"at","data":{"qq":"123"}}`),
		seg(t, `{"type":"text","data":{"text":"  你好，"}}`),
		seg(t, `{"type":"face","data":{"id":"6"}}`),
		seg(t, `{"type":"text","data":{"text":"世界 "}}`),
		nil,
	}
	if got := Text(msgs); got != "你好，世界" {
		t.Errorf("Text = %q, want %q", got, "你好，世界")
	}
	if got := Text(nil); got != "" {
		t.Errorf("Text(nil) = %q, want empty", got)
	}
}

func TestTextParsesLazilyWhenDataMissing(t *testing.T) {
	msgs := []*message.Message{
		{Type: "text", Parsed: json.RawMessage(`{"text":"直接解析"}`)},
	}
	if got := Text(msgs); got != "直接解析" {
		t.Errorf("Text = %q, want %q", got, "直接解析")
	}
}

func setBotConfig(t *testing.T, qq int, admins []int) {
	t.Helper()
	a := app.GetApp()
	saved := a.Config
	a.Config = &config.Config{Bot: &config.Bot{QQ_Number: qq, Admin_Number: admins}}
	t.Cleanup(func() { a.Config = saved })
}

func TestAtMeMatchesSelfQQOnly(t *testing.T) {
	setBotConfig(t, 999, nil)
	msgs := []*message.Message{
		seg(t, `{"type":"at","data":{"qq":"999"}}`),
		seg(t, `{"type":"text","data":{"text":"在吗"}}`),
	}
	if !AtMe(msgs) {
		t.Error("@机器人应命中")
	}
	others := []*message.Message{seg(t, `{"type":"at","data":{"qq":"123"}}`)}
	if AtMe(others) {
		t.Error("@他人不应命中")
	}
	if AtMe(nil) {
		t.Error("空消息不应命中")
	}
}

func TestAtUserIDMatchesGivenSelfID(t *testing.T) {
	msgs := []*message.Message{
		seg(t, `{"type":"at","data":{"qq":"1661489232"}}`),
	}
	if !AtUserID(msgs, 1661489232) {
		t.Error("@给定 self_id 应命中")
	}
	if AtUserID(msgs, 0) || AtUserID(msgs, 42) {
		t.Error("self_id 不符不应命中")
	}
	if AtUserID(nil, 1661489232) {
		t.Error("空消息不应命中")
	}
}

func TestIsAdmin(t *testing.T) {
	setBotConfig(t, 999, []int{1234, 5678})
	if !IsAdmin(1234) {
		t.Error("配置内的 QQ 应是管理员")
	}
	if IsAdmin(9999) {
		t.Error("配置外的 QQ 不应是管理员")
	}
}

func TestReplyRoutesByChatType(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":1}}`))
	}))
	defer srv.Close()
	addr := srv.Listener.Addr().(*net.TCPAddr)
	err := request.Init(&config.Napcat{HTTP: config.NapcatConn{
		Enable: true, Host: addr.IP.String(), Port: addr.Port,
	}})
	if err != nil {
		t.Fatal(err)
	}

	if err := Reply(context.Background(), "private", 777, chain.New().Text("你好")); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/send_private_msg" || bodyID(t, gotBody["user_id"]) != 777 {
		t.Errorf("私聊路由错误: path=%s body=%v", gotPath, gotBody)
	}

	if err := Reply(context.Background(), "group", 888, chain.New().At("1").Text(" 好")); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/send_group_msg" || bodyID(t, gotBody["group_id"]) != 888 {
		t.Errorf("群聊路由错误: path=%s body=%v", gotPath, gotBody)
	}

	if err := Reply(context.Background(), "unknown", 1, nil); err == nil {
		t.Error("未知会话类型应报错")
	}
}

func bodyID(t *testing.T, v any) int64 {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("字段不是数字: %v", v)
	}
	return int64(f)
}
