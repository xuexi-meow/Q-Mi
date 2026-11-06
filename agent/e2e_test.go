package agent

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"Q-Mi/app"
	"Q-Mi/event"
	"Q-Mi/storage"
	"Q-Mi/utils/config"
	"Q-Mi/utils/request"
)

// resetTestState 重置 wiring 的惰性单例,仅测试使用。
// Manager 惰性初始化用布尔标志而非 sync.Once,就是为了这里能重置。
func resetTestState() {
	managerMu.Lock()
	defer managerMu.Unlock()
	manager = nil
	managerInited = false
}

// e2eRecorder 记录假服务的请求。
type e2eRecorder struct {
	mu    sync.Mutex
	paths []string
	bodies []map[string]any
}

func (r *e2eRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.paths)
}

func (r *e2eRecorder) path(i int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paths[i]
}

func (r *e2eRecorder) body(i int) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bodies[i]
}

func (r *e2eRecorder) handle(w http.ResponseWriter, req *http.Request, respBody string) {
	var body map[string]any
	_ = json.NewDecoder(req.Body).Decode(&body)
	r.mu.Lock()
	r.paths = append(r.paths, req.URL.Path)
	r.bodies = append(r.bodies, body)
	r.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(respBody))
}

// TestEndToEndGroupAtMessageReplies 端到端:Dispatch 真实结构的群消息 JSON
// (含 @机器人)→ 防抖合并 → LLM → 假 NapCat 收到回复;
// 再 Dispatch 一条未 @ 的群消息,断言请求数不变。
func TestEndToEndGroupAtMessageReplies(t *testing.T) {
	// app.GetApp() 找不到 Config.yaml 会尝试在 cwd(agent/)创建默认文件;
	// 先放一份最小配置挡住,测试结束删掉。
	const cfgPath = "Config.yaml"
	minimal := "netsetting:\n  host: 127.0.0.1\n  port: 1\n  pattern: \"/\"\n  token: \"\"\nbot:\n  qq_number: 999\n"
	if err := os.WriteFile(cfgPath, []byte(minimal), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(cfgPath) })

	// 假 OpenAI:固定返回 pong-reply
	openai := &e2eRecorder{}
	openaiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		openai.handle(w, r, `{"choices":[{"index":0,"message":{"role":"assistant","content":"pong-reply"}}]}`)
	}))
	defer openaiSrv.Close()

	// 假 NapCat:记录 path+body,返回 retcode 0
	napcat := &e2eRecorder{}
	napcatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		napcat.handle(w, r, `{"status":"ok","retcode":0,"data":{"message_id":1}}`)
	}))
	defer napcatSrv.Close()

	if err := request.Init(&config.Napcat{HTTP: config.NapcatConn{
		Enable: true,
		Host:   napcatSrv.Listener.Addr().(*net.TCPAddr).IP.String(),
		Port:   napcatSrv.Listener.Addr().(*net.TCPAddr).Port,
	}}); err != nil {
		t.Fatal(err)
	}

	// 真实 Store(:memory:)
	st, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	a := app.GetApp()
	savedCfg, savedStore := a.Config, a.Store
	a.Config = &config.Config{
		Bot: &config.Bot{QQ_Number: 999},
		Agent: &config.AgentConfig{
			Enable: true, BaseURL: openaiSrv.URL, APIKey: "sk-test",
			Model: "test-model", PersonaFile: "persona.md",
			Debounce: 20 * time.Millisecond, Temperature: 0.8,
			History: config.AgentHistory{Max: 40, Trim: 20},
		},
	}
	a.Store = st
	t.Cleanup(func() {
		a.Config, a.Store = savedCfg, savedStore
		resetTestState()
	})
	resetTestState()

	// 真实结构的群消息 JSON:at 段 qq=SelfID + text 段
	atMsg := `{
		"post_type":"message","message_type":"group","sub_type":"normal",
		"message_id":1,"group_id":888,"user_id":12345,"self_id":999,
		"time":1794000000,"font":0,
		"message":[
			{"type":"at","data":{"qq":"999"}},
			{"type":"text","data":{"text":"你好"}}
		],
		"raw_message":"[CQ:at,qq=999] 你好",
		"sender":{"user_id":12345,"nickname":"测试用户","sex":"unknown","card":"","role":"member"}
	}`
	event.Dispatch([]byte(atMsg))
	event.Wait()

	waitFor(t, 5*time.Second, func() bool { return napcat.count() == 1 }, "假 NapCat 应收到 1 次回复")
	if got := napcat.path(0); got != "/send_group_msg" {
		t.Errorf("回复 path = %q, want /send_group_msg", got)
	}
	if got := sentTextAt(t, napcat.body(0)); got != "pong-reply" {
		t.Errorf("回复内容 = %q, want %q", got, "pong-reply")
	}
	if got := openai.count(); got != 1 {
		t.Errorf("假 OpenAI 请求数 = %d, want 1", got)
	}

	// 未 @ 的群消息:应被忽略
	noAtMsg := `{
		"post_type":"message","message_type":"group","sub_type":"normal",
		"message_id":2,"group_id":888,"user_id":12345,"self_id":999,
		"time":1794000005,"font":0,
		"message":[{"type":"text","data":{"text":"没人理我"}}],
		"raw_message":"没人理我",
		"sender":{"user_id":12345,"nickname":"测试用户","sex":"unknown","card":"","role":"member"}
	}`
	event.Dispatch([]byte(noAtMsg))
	event.Wait()
	time.Sleep(150 * time.Millisecond) // 等过防抖窗口
	if got := napcat.count(); got != 1 {
		t.Errorf("未 @ 消息不应触发回复,NapCat 请求数 = %d", got)
	}
	if got := openai.count(); got != 1 {
		t.Errorf("未 @ 消息不应调 LLM,请求数 = %d", got)
	}
}

// sentTextAt 从 /send_*_msg 请求体里取出第一个 text 段的内容。
func sentTextAt(t *testing.T, body map[string]any) string {
	t.Helper()
	msg, _ := body["message"].([]any)
	for _, seg := range msg {
		m, ok := seg.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "text" {
			d, _ := m["data"].(map[string]any)
			s, _ := d["text"].(string)
			return s
		}
	}
	return ""
}
