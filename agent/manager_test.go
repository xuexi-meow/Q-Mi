package agent

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"Q-Mi/utils/config"
	"Q-Mi/utils/request"
)

// ---------- 测试桩 ----------

// stubBuilder 记录收到的 Turn,行为可注入。
type stubBuilder struct {
	mu    sync.Mutex
	calls int
	turns []*Turn
	fn    func(turn *Turn) []ChatMessage
}

func (b *stubBuilder) Build(turn *Turn) []ChatMessage {
	b.mu.Lock()
	b.calls++
	b.turns = append(b.turns, turn)
	fn := b.fn
	b.mu.Unlock()
	if fn != nil {
		return fn(turn)
	}
	return []ChatMessage{{Role: "user", Content: "hi"}}
}

func (b *stubBuilder) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func (b *stubBuilder) turn(i int) *Turn {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.turns[i]
}

// fakeLLM 假 OpenAI:记录每个请求体与到达时间,返回固定 content。
// script 非空时按序循环返回脚本里的原始响应 JSON(用于思考循环测试);
// hook 非空时在记录请求后、返回响应前调用(可阻塞模拟慢响应,可经
// r.Context() 观察客户端取消,用于插话打断测试)。
// 请求逐个串行处理(模拟真实客户端等响应才发下一请求),
// 到达时间因此可用来断言"串行不打断"。
type fakeLLM struct {
	mu      sync.Mutex
	serial  sync.Mutex
	reqs    []ChatRequest
	arrival []time.Time
	script  []string
	hook    func(idx int, r *http.Request)
	respond func(ChatRequest) string
}

func newFakeLLM(t *testing.T, content string) (*fakeLLM, *httptest.Server) {
	t.Helper()
	f := &fakeLLM{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.serial.Lock()
		defer f.serial.Unlock()
		var req ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		idx := len(f.reqs)
		f.reqs = append(f.reqs, req)
		f.arrival = append(f.arrival, time.Now())
		script := f.script
		hook := f.hook
		respond := f.respond
		f.mu.Unlock()
		if hook != nil {
			hook(idx, r)
		}
		resp := map[string]any{}
		var body []byte
		if respond != nil {
			body = []byte(respond(req))
		} else if len(script) > 0 {
			body = []byte(script[idx%len(script)])
		} else {
			resp = map[string]any{
				"choices": []map[string]any{
					{"index": 0, "message": map[string]any{"role": "assistant", "content": content}},
				},
			}
			body, _ = json.Marshal(resp)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeLLM) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func (f *fakeLLM) arrived(i int) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.arrival[i]
}

// napcatRec 假 NapCat:记录 path 与请求体,返回 retcode 0。
type napcatRec struct {
	mu     sync.Mutex
	paths  []string
	bodies []map[string]any
}

func startFakeNapCat(t *testing.T) *napcatRec {
	t.Helper()
	rec := &napcatRec{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.mu.Lock()
		rec.paths = append(rec.paths, r.URL.Path)
		rec.bodies = append(rec.bodies, body)
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":1}}`))
	}))
	t.Cleanup(srv.Close)
	if err := request.Init(&config.Napcat{HTTP: config.NapcatConn{
		Enable: true, Host: srv.Listener.Addr().(*net.TCPAddr).IP.String(),
		Port: srv.Listener.Addr().(*net.TCPAddr).Port,
	}}); err != nil {
		t.Fatal(err)
	}
	return rec
}

func (n *napcatRec) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.paths)
}

func (n *napcatRec) sentText(t *testing.T, i int) string {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	msg, _ := n.bodies[i]["message"].([]any)
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

// ---------- 公共助手 ----------

func testCfg(debounce time.Duration) config.AgentConfig {
	return config.AgentConfig{
		Enable: true, Model: "test-model", Debounce: debounce,
		Temperature: 0.8, History: config.AgentHistory{Max: 40, Trim: 20},
	}
}

func newTestManager(t *testing.T, cfg config.AgentConfig, b *stubBuilder) (*Manager, *fakeLLM) {
	t.Helper()
	f, srv := newFakeLLM(t, "pong")
	f.script = []string{reasoningToolCallResp("", "pong", "done", "finish", "{}")}
	m := NewManager(b, NewClient(srv.URL, "sk-test", 5*time.Second), cfg, nil, 999)
	m.notesPath = filepath.Join(t.TempDir(), "notes.json")
	m.statePath = filepath.Join(t.TempDir(), "state.json")
	// stub builder 无图片存储，manager 兜底的那份改指到临时目录，
	// 避免测试下载图片时把 agent/images 落到包目录（甚至 agent/agent/images）
	m.images = newImageStore(t.TempDir(), m.store)
	t.Cleanup(func() { srv.Close() })
	return m, f
}

func msg(chatType string, chatID int64, text string) IncomingMessage {
	return IncomingMessage{
		ChatType: chatType, ChatID: chatID, SelfID: 999,
		SenderID: 111, SenderName: "测试用户", Text: text,
		Time: time.Now(),
	}
}

func waitFor(t *testing.T, d time.Duration, cond func() bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", desc)
}

// ---------- 测试 ----------

// a) 防抖合并:debounce 窗口内连发 3 条 → 只合成 1 个 turn、LLM 只收 1 次请求。
func TestEnqueueDebouncesIntoSingleTurn(t *testing.T) {
	b := &stubBuilder{}
	m, f := newTestManager(t, testCfg(20*time.Millisecond), b)
	startFakeNapCat(t) // 回复走假 NapCat,避免打到其他测试留下的旧地址

	for _, s := range []string{"一", "二", "三"} {
		m.Enqueue(msg("group", 888, s))
	}

	waitFor(t, 3*time.Second, func() bool { return f.count() == 1 }, "LLM 应收到恰好 1 次请求")
	turn := b.turn(0)
	if turn.ChatType != "group" || turn.ChatID != 888 {
		t.Errorf("turn 会话错误: %+v", turn)
	}
	if len(turn.NewMessages) != 3 {
		t.Fatalf("turn 应合并 3 条新消息,得到 %d", len(turn.NewMessages))
	}
	for i, tm := range turn.NewMessages {
		if tm.ChatType != "group" || tm.ChatID != 888 {
			t.Errorf("新消息[%d] 应自带会话 group/888, got %s/%d", i, tm.ChatType, tm.ChatID)
		}
	}
	for i, want := range []string{"一", "二", "三"} {
		if turn.NewMessages[i].Text != want {
			t.Errorf("新消息[%d] = %q, want %q(按到达顺序)", i, turn.NewMessages[i].Text, want)
		}
	}
	time.Sleep(150 * time.Millisecond) // 等过第二个防抖窗口
	if got := f.count(); got != 1 {
		t.Errorf("防抖后 LLM 请求应仍为 1 次,得到 %d", got)
	}
}

// b) 串行不打断:第一批处理阻塞期间入队第二批,第二批必须等第一批完成后才开始。
func TestWorkerSerializesTurnsWithoutInterruption(t *testing.T) {
	b := &stubBuilder{}
	m, f := newTestManager(t, testCfg(20*time.Millisecond), b)
	startFakeNapCat(t)

	started := make(chan struct{})
	released := make(chan struct{})
	f.mu.Lock()
	f.hook = func(idx int, r *http.Request) {
		if idx == 0 {
			close(started)
			<-released
		}
	}
	f.mu.Unlock()

	m.Enqueue(msg("group", 888, "first"))
	<-started // 第一批已到 LLM 且阻塞

	m.Enqueue(msg("group", 888, "second-a"))
	m.Enqueue(msg("group", 888, "second-b"))
	time.Sleep(100 * time.Millisecond) // 远超防抖窗口
	if got := f.count(); got != 1 {
		t.Fatalf("第一批阻塞期间第二批不应打断,LLM 请求数 = %d", got)
	}

	releaseTime := time.Now()
	close(released)
	waitFor(t, 3*time.Second, func() bool { return f.count() == 2 }, "第二批应在第一批完成后到达")
	if !f.arrived(1).After(releaseTime) {
		t.Error("第二批请求应在第一批释放之后才发出")
	}
	turn := b.turn(1)
	if len(turn.NewMessages) != 2 || turn.NewMessages[0].Text != "second-a" || turn.NewMessages[1].Text != "second-b" {
		t.Errorf("第二批 turn 内容/顺序错误: %+v", turn.NewMessages)
	}
}

// c) worker panic 存活:builder 第一次 panic、第二次正常,仍能回复。
func TestWorkerSurvivesBuilderPanic(t *testing.T) {
	b := &stubBuilder{}
	b.fn = func(turn *Turn) []ChatMessage {
		if b.callCount() == 1 {
			panic("builder 炸了")
		}
		return []ChatMessage{{Role: "user", Content: "ok"}}
	}
	m, f := newTestManager(t, testCfg(20*time.Millisecond), b)
	f.script = []string{twoToolCallResp("send", "send_message", `{"chat_type":"group","chat_id":888,"text":"pong"}`, "done", "finish", "{}")}
	napcat := startFakeNapCat(t)

	m.Enqueue(msg("group", 888, "第一轮"))
	waitFor(t, 3*time.Second, func() bool { return b.callCount() == 1 }, "builder 第一次应被调用")

	m.Enqueue(msg("group", 888, "第二轮"))
	waitFor(t, 3*time.Second, func() bool { return f.count() == 1 }, "panic 后 worker 应继续处理第二轮")
	waitFor(t, 3*time.Second, func() bool { return napcat.count() == 1 }, "第二轮应发出回复")
	if got := napcat.sentText(t, 0); got != "pong" {
		t.Errorf("回复内容 = %q, want %q", got, "pong")
	}
	if napcat.count() != 1 {
		t.Errorf("panic 的一轮不应发送消息,发送次数 = %d", napcat.count())
	}
}

// d) debug 落盘:cfg.Debug 时写 <debugDir>/<chat_type>-<chat_id>/<unix_nano>.json,含请求 JSON。
func TestDebugDumpWritesRequestJSON(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	cfg.Debug = true
	m, f := newTestManager(t, cfg, b)
	m.debugDir = t.TempDir()
	startFakeNapCat(t)

	m.Enqueue(msg("group", 888, "你好"))
	waitFor(t, 3*time.Second, func() bool { return f.count() == 1 }, "LLM 应收到请求")
	waitFor(t, 3*time.Second, func() bool {
		entries, err := os.ReadDir(m.debugDir)
		return err == nil && len(entries) > 0
	}, "debug 目录应生成")

	sub := filepath.Join(m.debugDir, "group-888")
	entries, err := os.ReadDir(sub)
	if err != nil {
		t.Fatalf("读取会话 debug 目录失败: %v", err)
	}
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".json") {
		t.Fatalf("应恰好落盘 1 个 .json 文件,得到 %v", entries)
	}
	if _, err := strconv.ParseInt(strings.TrimSuffix(entries[0].Name(), ".json"), 10, 64); err != nil {
		t.Errorf("文件名应为 unix_nano 时间戳: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(sub, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Request json.RawMessage `json:"request"`
		Content string          `json:"response_content"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("debug 文件应为合法 JSON: %v", err)
	}
	var req ChatRequest
	if err := json.Unmarshal(rec.Request, &req); err != nil {
		t.Fatalf("request 字段应为 ChatRequest JSON: %v", err)
	}
	if req.Model != "test-model" || len(req.Messages) == 0 {
		t.Errorf("请求 JSON 不完整: model=%q messages=%d", req.Model, len(req.Messages))
	}
	if rec.Content != "pong" {
		t.Errorf("response_content = %q, want %q", rec.Content, "pong")
	}
}

// 空回复不发送。
func TestEmptyContentNotSent(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	emptyF, emptySrv := newFakeLLM(t, "   ")
	m := NewManager(b, NewClient(emptySrv.URL, "sk-test", 5*time.Second), cfg, nil, 999)
	m.notesPath = filepath.Join(t.TempDir(), "notes.json")
	m.statePath = filepath.Join(t.TempDir(), "state.json")
	napcat := startFakeNapCat(t)

	m.Enqueue(msg("private", 111, "你好"))
	waitFor(t, 3*time.Second, func() bool { return emptyF.count() == 3 }, "空输出应在有限纠错后停止")
	time.Sleep(100 * time.Millisecond)
	if got := napcat.count(); got != 0 {
		t.Errorf("空回复不应发送消息,发送次数 = %d", got)
	}
}

// TestEnqueueCarriesImagesIntoTurn IncomingMessage.Images 透传到 TurnMessage
// （P2.7 多模态：wiring 提取的图片 URL 随防抖合并进入 turn）。
func TestEnqueueCarriesImagesIntoTurn(t *testing.T) {
	b := &stubBuilder{}
	m, f := newTestManager(t, testCfg(20*time.Millisecond), b)
	startFakeNapCat(t)

	im := msg("group", 888, "看图")
	im.Images = []imageRef{{Idx: 0, URL: "https://multimedia.nt.qq.com.cn/1"}}
	m.Enqueue(im)

	waitFor(t, 3*time.Second, func() bool { return f.count() == 1 }, "LLM 应收到请求")
	turn := b.turn(0)
	if len(turn.NewMessages) != 1 {
		t.Fatalf("turn 应有 1 条新消息, got %d", len(turn.NewMessages))
	}
	got := turn.NewMessages[0].Images
	want := []imageRef{{Idx: 0, URL: "https://multimedia.nt.qq.com.cn/1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TurnMessage.Images = %v, want %v", got, want)
	}
}

// TestHandleTurnAttachesPendingImagesAfterTools 工具（view_message_images）成功
// 后 pendingImages 非空 → 当轮工具执行完即追加一条 user 多模态消息（text
// 「这是你刚才要求查看的图片」+ image_url data URL）进 msgs 并清空
// pendingImages，下一轮 LLM 请求可见图片。pendingImages 只在本会话串行
// worker（handleTurn）内读写，顺序执行无并发问题。
func TestHandleTurnAttachesPendingImagesAfterTools(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	script := []string{
		toolCallResp("c1", "view_message_images", `{"message_id":7}`),
		toolCallResp("c2", "finish", ""),
	}
	s := openTestStore(t)
	imgSrv, _ := servePNG(t, "image/png")
	putImageRecord(t, s, 7, imgSrv.URL+"/a.png", "ABC.image")
	m, f, _ := newLoopManager(t, cfg, b, s, script)

	m.Enqueue(msg("group", 888, "看看那张图"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 2 }, "图片应随下一轮请求送出")
	time.Sleep(150 * time.Millisecond)
	if got := f.count(); got != 2 {
		t.Fatalf("finish 后循环应终止, LLM 请求数 = %d, want 2", got)
	}

	// 请求2：user, assistant(c1), tool(c1), user(图片)
	msgs := f.reqs[1].Messages
	if len(msgs) != 4 {
		t.Fatalf("请求2 应有 4 条消息, got %d: %+v", len(msgs), msgs)
	}
	tool := msgs[2]
	if tool.Role != "tool" || tool.ToolCallID != "c1" || !strings.Contains(contentText(tool), "已附在下一条消息") {
		t.Errorf("view_message_images 的 tool 消息 = %+v (%s)", tool, contentText(tool))
	}
	imgMsg := msgs[3]
	if imgMsg.Role != "user" {
		t.Fatalf("图片消息 role = %q, want user", imgMsg.Role)
	}
	// fakeLLM 收到的是 JSON 反序列化后的 wire 形态（[]any + map）
	arr, ok := imgMsg.Content.([]any)
	if !ok {
		t.Fatalf("图片消息 content 应为多模态数组, got %T: %#v", imgMsg.Content, imgMsg.Content)
	}
	if len(arr) != 2 {
		t.Fatalf("图片消息应有 text+image_url 两部分, got %d: %#v", len(arr), arr)
	}
	p0, ok := arr[0].(map[string]any)
	if !ok || p0["type"] != "text" {
		t.Fatalf("parts[0] 应为 text, got %#v", arr[0])
	}
	if text, _ := p0["text"].(string); !strings.Contains(text, "来自上一轮工具结果") {
		t.Errorf("text 部分不符: %q", text)
	}
	p1, ok := arr[1].(map[string]any)
	if !ok || p1["type"] != "image_url" {
		t.Fatalf("parts[1] 应为 image_url, got %#v", arr[1])
	}
	iu, ok := p1["image_url"].(map[string]any)
	if !ok {
		t.Fatalf("image_url 字段缺失: %#v", p1)
	}
	if url, _ := iu["url"].(string); !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Errorf("image_url 应为 data URL: %.40q", url)
	}

	// pendingImages 已清空
	if got := len(b.turn(0).pendingImages); got != 0 {
		t.Errorf("pendingImages 应已清空, got %d", got)
	}
}
