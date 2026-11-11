package agent

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	mu     sync.Mutex
	paths  []string
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
// 再 Dispatch 一条未 @ 的群消息,P2 起同样入队进思考循环(spec §3),
// 断言两次都触发 LLM 请求与回复。
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
		openai.handle(w, r, twoToolCallResp("send", "send_message", `{"chat_type":"group","chat_id":888,"text":"pong-reply"}`, "done", "finish", "{}"))
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
			History:  config.AgentHistory{Max: 40, Trim: 20},
			ImageDir: t.TempDir(),
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

	// 未 @ 的群消息仍可由模型主动通过工具参与。
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
	waitFor(t, 5*time.Second, func() bool { return openai.count() == 2 }, "未 @ 消息也应触发思考循环")
	waitFor(t, 5*time.Second, func() bool { return napcat.count() == 2 }, "未 @ 消息的回复也应发出")
	if got := napcat.path(1); got != "/send_group_msg" {
		t.Errorf("第二次回复 path = %q, want /send_group_msg", got)
	}
	if got := sentTextAt(t, napcat.body(1)); got != "pong-reply" {
		t.Errorf("第二次回复内容 = %q, want %q", got, "pong-reply")
	}
	// 请求应携带工具定义（思考循环生效的标志）
	if tools, ok := openai.body(1)["tools"].([]any); !ok || len(tools) == 0 {
		t.Errorf("LLM 请求应携带 tools 定义, got %v", openai.body(1)["tools"])
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

// TestEndToEndGroupImageMessageMultimodal P2.7 端到端：群里发图（@机器人），
// 多模态开启时假 LLM 应收到多模态 content 数组（文本部分 + image_url 部分），
// 而非 CQ 码文本；回复照常发出。
func TestEndToEndGroupImageMessageMultimodal(t *testing.T) {
	// app.GetApp() 找不到 Config.yaml 会尝试在 cwd(agent/)创建默认文件;先挡住
	const cfgPath = "Config.yaml"
	minimal := "netsetting:\n  host: 127.0.0.1\n  port: 1\n  pattern: \"/\"\n  token: \"\"\nbot:\n  qq_number: 999\n"
	if err := os.WriteFile(cfgPath, []byte(minimal), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(cfgPath) })

	openai := &e2eRecorder{}
	openaiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		openai.handle(w, r, twoToolCallResp("send", "send_message", `{"chat_type":"group","chat_id":888,"text":"图收到了"}`, "done", "finish", "{}"))
	}))
	defer openaiSrv.Close()

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
			History:    config.AgentHistory{Max: 40, Trim: 20},
			ImageDir:   t.TempDir(),
			Multimodal: boolPtr(true),
		},
	}
	a.Store = st
	t.Cleanup(func() {
		a.Config, a.Store = savedCfg, savedStore
		resetTestState()
	})
	resetTestState()

	// P3-3：builder 会先本地下载图片转 base64 data URL。本地起一个返回真实
	// PNG 魔数的图床，断言 LLM 收到 data: 前缀（不访问真实腾讯多媒体域名）。
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("\x89PNG\r\n\x1a\npng-bytes"))
	}))
	t.Cleanup(imgSrv.Close)
	imgURL := imgSrv.URL + "/download?appid=1407&fileid=abc"
	imgMsg := `{
		"post_type":"message","message_type":"group","sub_type":"normal",
		"message_id":1,"group_id":888,"user_id":12345,"self_id":999,
		"time":1794000000,"font":0,
		"message":[
			{"type":"at","data":{"qq":"999"}},
			{"type":"text","data":{"text":"看这张图"}},
			{"type":"image","data":{"file":"abc.image","sub_type":0,"url":"` + imgURL + `"}}
		],
		"raw_message":"[CQ:at,qq=999] 看这张图 [CQ:image,file=abc.image,subType=0,url=` + imgURL + `]",
		"sender":{"user_id":12345,"nickname":"测试用户","sex":"unknown","card":"","role":"member"}
	}`
	event.Dispatch([]byte(imgMsg))
	event.Wait()

	waitFor(t, 5*time.Second, func() bool { return openai.count() == 1 }, "LLM 应收到请求")
	waitFor(t, 5*time.Second, func() bool { return napcat.count() == 1 }, "应收到回复")

	// LLM 收到的末条消息 content 应为多模态数组
	msgs, _ := openai.body(0)["messages"].([]any)
	if len(msgs) < 2 {
		t.Fatalf("LLM 应收到至少 2 条消息, got %v", msgs)
	}
	last, _ := msgs[len(msgs)-1].(map[string]any)
	parts, ok := last["content"].([]any)
	if !ok {
		t.Fatalf("末条消息 content 应为多模态数组, got %v", last["content"])
	}
	p0, _ := parts[0].(map[string]any)
	if p0["type"] != "text" {
		t.Fatalf("content[0].type = %v, want text", p0)
	}
	if txt, _ := p0["text"].(string); !strings.Contains(txt, "看这张图") || strings.Contains(txt, "CQ:image") {
		t.Errorf("文本部分应含消息文本且不含 CQ 码: %v", p0)
	}
	var gotURL string
	for _, p := range parts[1:] {
		m, _ := p.(map[string]any)
		if m["type"] == "image_url" {
			iu, _ := m["image_url"].(map[string]any)
			gotURL, _ = iu["url"].(string)
		}
	}
	if !strings.HasPrefix(gotURL, "data:image/png;base64,") {
		t.Errorf("image_url = %q, want data:image/png;base64 前缀（本地下载转 base64）", gotURL)
	}
	// 回复照常发出
	if got := sentTextAt(t, napcat.body(0)); got != "图收到了" {
		t.Errorf("回复内容 = %q, want 图收到了", got)
	}
}
