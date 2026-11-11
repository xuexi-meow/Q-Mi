package request

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	apimsg "Q-Mi/api/message"
	"Q-Mi/utils/config"
)

// startMockNapCat 启动一个模拟 NapCat 的测试服务器,返回地址和收到的请求记录
func startMockNapCat(t *testing.T, respBody string) (addr string, gotPath *string, gotAuth *string, gotBody *map[string]any) {
	t.Helper()
	gotPath = new(string)
	gotAuth = new(string)
	gotBody = new(map[string]any)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotPath = r.URL.Path
		*gotAuth = r.Header.Get("Authorization")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		*gotBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)

	tcpAddr, ok := srv.Listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("无法获取测试服务器端口")
	}
	return tcpAddr.String(), gotPath, gotAuth, gotBody
}

// initTestClient 用测试服务器地址初始化包级客户端
func initTestClient(t *testing.T, addr, token string) {
	t.Helper()
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	err := Init(&config.Napcat{
		HTTP: config.NapcatConn{Enable: true, Host: host, Port: port, Token: token},
	})
	if err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
}

func TestSendGroupMsg(t *testing.T) {
	addr, gotPath, gotAuth, gotBody := startMockNapCat(t,
		`{"status":"ok","retcode":0,"data":{"message_id":777},"message":"","wording":"","stream":"normal-action"}`)
	initTestClient(t, addr, "test-token")

	resp, err := SendGroupMsg(context.Background(), 123456, apimsg.Text("hi"), apimsg.At("10010"))
	if err != nil {
		t.Fatalf("SendGroupMsg 失败: %v", err)
	}

	if *gotPath != "/send_group_msg" {
		t.Errorf("请求路径 = %s, want /send_group_msg", *gotPath)
	}
	if *gotAuth != "Bearer test-token" {
		t.Errorf("鉴权头 = %q, want %q", *gotAuth, "Bearer test-token")
	}

	// 校验请求体:group_id 为数字,message 为消息段数组
	if int64ToFloat((*gotBody)["group_id"]) != 123456 {
		t.Errorf("group_id = %v, want 123456", (*gotBody)["group_id"])
	}
	segs, ok := (*gotBody)["message"].([]any)
	if !ok || len(segs) != 2 {
		t.Fatalf("message 段数量错误: %v", (*gotBody)["message"])
	}
	first, _ := segs[0].(map[string]any)
	if first["type"] != "text" {
		t.Errorf("第一段 type = %v, want text", first["type"])
	}
	data, _ := first["data"].(map[string]any)
	if data["text"] != "hi" {
		t.Errorf("文本内容 = %v, want hi", data["text"])
	}

	if resp.Data.MessageID != 777 {
		t.Errorf("resp.Data.MessageID = %d, want 777", resp.Data.MessageID)
	}
	if resp.Status != "ok" {
		t.Errorf("resp.Status = %s, want ok", resp.Status)
	}
}

func TestRetcodeError(t *testing.T) {
	addr, _, _, _ := startMockNapCat(t,
		`{"status":"failed","retcode":1400,"data":null,"message":"请求参数错误","wording":"请求参数错误","stream":"normal-action"}`)
	initTestClient(t, addr, "")

	_, err := DeleteMsg(context.Background(), 1)
	if err == nil {
		t.Fatal("retcode=1400 应返回错误")
	}
}

func TestRetcodeErrorPreservesMessageAndAction(t *testing.T) {
	addr, _, _, _ := startMockNapCat(t,
		`{"status":"failed","retcode":1400,"data":null,"message":"missing parameter message_id","wording":""}`)
	initTestClient(t, addr, "")
	_, err := DeleteMsg(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "missing parameter message_id") ||
		!strings.Contains(err.Error(), "/delete_msg") || !strings.Contains(err.Error(), "1400") {
		t.Fatalf("model needs the action and concrete upstream error, got %v", err)
	}
}

func TestNotInitialized(t *testing.T) {
	saved := client
	client = nil
	defer func() { client = saved }()

	_, err := SendGroupMsg(context.Background(), 1, apimsg.Text("x"))
	if err == nil {
		t.Fatal("未初始化时应返回错误")
	}
}

// 辅助:JSON 数字解析出来是 float64
func int64ToFloat(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return -1
}
