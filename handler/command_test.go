package handler

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"Q-Mi/app"
	"Q-Mi/bot"
	"Q-Mi/event"
	"Q-Mi/utils/config"
	"Q-Mi/utils/request"
)

func TestPingCommandRepliesInGroup(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":1}}`))
	}))
	defer srv.Close()
	addr := srv.Listener.Addr().(*net.TCPAddr)
	if err := request.Init(&config.Napcat{HTTP: config.NapcatConn{
		Enable: true, Host: addr.IP.String(), Port: addr.Port,
	}}); err != nil {
		t.Fatal(err)
	}
	a := app.GetApp()
	a.Config = &config.Config{NetSetting: a.Config.NetSetting, Napcat: &config.Napcat{}, Bot: &config.Bot{}}

	event.Dispatch([]byte(`{"post_type":"message","message_type":"group","group_id":888,
		"user_id":10,"raw_message":"/ping","message":[{"type":"text","data":{"text":"/ping"}}]}`))
	event.Wait()

	if gotPath != "/send_group_msg" {
		t.Fatalf("命令未触发回复: path=%q", gotPath)
	}
	segs, _ := gotBody["message"].([]any)
	if len(segs) == 0 {
		t.Fatal("回复消息为空")
	}
	first, _ := segs[0].(map[string]any)
	if first["type"] != "text" {
		t.Fatalf("回复段类型 = %v", first["type"])
	}
	if data := first["data"].(map[string]any); data["text"] != "pong" {
		t.Fatalf("回复内容 = %v, want pong", data["text"])
	}
	cmd, _ := bot.MatchCommand("/ping")
	if cmd == nil || cmd.Name != "ping" {
		t.Fatalf("/ping 命令未注册: %v", cmd)
	}
}
