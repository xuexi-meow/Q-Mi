package agent

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	evmsg "Q-Mi/event/message"
)

// ---------- 消息插入与旧紧急度标记 ----------

// joinNewMessages builder 替身:把 turn 的新消息文本拼成一条 user 消息,
// 便于断言 LLM 实际收到的内容。
func joinNewMessages(turn *Turn) []ChatMessage {
	parts := make([]string, 0, len(turn.NewMessages))
	for _, m := range turn.NewMessages {
		parts = append(parts, m.Text)
	}
	return []ChatMessage{{Role: "user", Content: strings.Join(parts, "|")}}
}

// plainResp 构造纯文本响应 JSON。
func plainResp(content string) string {
	return `{"choices":[{"index":0,"message":{"role":"assistant","content":"` + content + `"}}]}`
}

// readDebugRecords 读取会话 debug 目录全部记录,按 interrupted 分组返回。
func readDebugRecords(t *testing.T, dir string) (interrupted []debugRecord, normal []debugRecord) {
	t.Helper()
	sub := filepath.Join(dir, "group-888")
	entries, err := os.ReadDir(sub)
	if err != nil {
		t.Fatalf("读取 debug 目录失败: %v", err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(sub, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var rec debugRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			t.Fatalf("debug 记录不是合法 JSON: %v", err)
		}
		if rec.Interrupted {
			interrupted = append(interrupted, rec)
		} else {
			normal = append(normal, rec)
		}
	}
	return interrupted, normal
}

// Urgent arrivals wait for the active request, then join after tool results.
func TestUrgentMessageJoinsSlowTurn(t *testing.T) {
	b := &stubBuilder{fn: joinNewMessages}
	cfg := testCfg(20 * time.Millisecond)
	cfg.Debug = true

	f, srv := newFakeLLM(t, "")
	f.mu.Lock()
	f.script = []string{toolCallResp("query", "get_recent_messages", "{}"), plainResp("新回复")}
	var once sync.Once
	cancelSeen := make(chan struct{})
	released := make(chan struct{})
	t.Cleanup(func() { once.Do(func() { close(released) }) })
	f.hook = func(idx int, r *http.Request) {
		if idx != 0 {
			return
		}
		select {
		case <-r.Context().Done():
			close(cancelSeen)
		case <-released:
		}
	}
	f.mu.Unlock()

	m := NewManager(b, NewClient(srv.URL, "sk-test", 5*time.Second), cfg, nil, 999)
	m.notesPath = filepath.Join(t.TempDir(), "notes.json")
	m.statePath = filepath.Join(t.TempDir(), "state.json")
	m.debugDir = t.TempDir()
	m.images = newImageStore(t.TempDir(), nil) // 防测试下载图片污染包目录
	napcat := startFakeNapCat(t)

	m.Enqueue(msg("group", 888, "第一条"))
	waitFor(t, 3*time.Second, func() bool { return f.count() == 1 }, "第一条应到达 LLM 并阻塞在慢响应上")

	urgent := msg("group", 888, "插队！")
	urgent.Urgent = true
	m.Enqueue(urgent)

	select {
	case <-cancelSeen:
		t.Fatal("紧急消息不应取消正在进行的请求")
	case <-time.After(70 * time.Millisecond):
	}
	once.Do(func() { close(released) })
	waitFor(t, 3*time.Second, func() bool { return f.count() == 2 }, "urgent 消息应加入下一次请求")

	// 新 turn 的 LLM 请求应包含新消息（且只是 pending 里的内容）
	last := f.reqs[1].Messages[len(f.reqs[1].Messages)-1]
	if last.Content != "插队！" {
		t.Errorf("新 turn 请求的末条消息 = %q, want 插队！", last.Content)
	}

	// 查询工具不发言，包含插入消息的下一次请求正常回复。
	waitFor(t, 3*time.Second, func() bool { return napcat.count() == 1 }, "新 turn 应发出回复")
	if got := napcat.sentText(t, 0); got != "新回复" {
		t.Errorf("回复文本 = %q, want 新回复", got)
	}
	time.Sleep(100 * time.Millisecond)
	if got := napcat.count(); got != 1 {
		t.Errorf("仅纯文本响应应发送一次, 发送次数 = %d", got)
	}

	// 两次请求都正常完成，不再有 interrupted 记录。
	interruptedRecs, normalRecs := readDebugRecords(t, m.debugDir)
	if len(interruptedRecs) != 0 {
		t.Fatalf("追加消息不应记录 interrupted=true, got %d", len(interruptedRecs))
	}
	if len(normalRecs) != 2 || normalRecs[1].Content != "新回复" {
		t.Errorf("两次请求均应正常记录, got %+v", normalRecs)
	}
}

// TestNonUrgentDuringSlowTurnDoesNotInterrupt 慢 LLM 期间来非 urgent
// （群未@）消息：不打断，第一条正常完成并回复，新消息走 debounce
// 在第一条完成后才处理。
func TestNonUrgentDuringSlowTurnDoesNotInterrupt(t *testing.T) {
	b := &stubBuilder{fn: joinNewMessages}
	cfg := testCfg(20 * time.Millisecond)

	f, srv := newFakeLLM(t, "")
	f.mu.Lock()
	f.script = []string{plainResp("回复一"), plainResp("回复二")}
	released := make(chan struct{})
	f.hook = func(idx int, r *http.Request) {
		if idx == 0 {
			<-released
		}
	}
	f.mu.Unlock()

	m := NewManager(b, NewClient(srv.URL, "sk-test", 5*time.Second), cfg, nil, 999)
	m.notesPath = filepath.Join(t.TempDir(), "notes.json")
	m.statePath = filepath.Join(t.TempDir(), "state.json")
	m.images = newImageStore(t.TempDir(), nil) // 防测试下载图片污染包目录
	napcat := startFakeNapCat(t)

	m.Enqueue(msg("group", 888, "first"))
	waitFor(t, 3*time.Second, func() bool { return f.count() == 1 }, "第一条应到达 LLM 并阻塞")

	second := msg("group", 888, "second-a")
	second.Urgent = false // 群未@ → 非紧急
	m.Enqueue(second)
	time.Sleep(100 * time.Millisecond) // 远超防抖窗口
	if got := f.count(); got != 1 {
		t.Fatalf("非紧急消息不应打断进行中的思考, LLM 请求数 = %d", got)
	}

	releaseTime := time.Now()
	close(released)
	waitFor(t, 3*time.Second, func() bool { return napcat.count() == 2 }, "两条 turn 都应正常完成并回复")
	if got := napcat.sentText(t, 0); got != "回复一" {
		t.Errorf("第一条应正常完成并回复, got %q（若被错误打断则不会发出）", got)
	}
	if got := napcat.sentText(t, 1); got != "回复二" {
		t.Errorf("第二条的回复 = %q, want 回复二", got)
	}
	if !f.arrived(1).After(releaseTime) {
		t.Error("第二条的请求应在第一条完成后才发出（debounce 排队，不打断）")
	}
	turn2 := b.turn(1)
	if len(turn2.NewMessages) != 1 || turn2.NewMessages[0].Text != "second-a" {
		t.Errorf("第二条 turn 的新消息 = %+v, want [second-a]", turn2.NewMessages)
	}
}

// TestUrgentWhenWorkerIdleStillDebounces worker 空闲时 urgent 消息
// 仍走正常防抖（合并语义不变），不立即投递。
func TestUrgentWhenWorkerIdleStillDebounces(t *testing.T) {
	b := &stubBuilder{fn: joinNewMessages}
	cfg := testCfg(20 * time.Millisecond)
	m, f := newTestManager(t, cfg, b)
	startFakeNapCat(t)

	p1 := msg("private", 111, "私聊一")
	p1.Urgent = true
	m.Enqueue(p1)
	p2 := msg("private", 111, "私聊二")
	p2.Urgent = true
	m.Enqueue(p2)

	waitFor(t, 3*time.Second, func() bool { return f.count() == 1 }, "urgent 消息在 worker 空闲时应仍走防抖合并")
	turn := b.turn(0)
	if len(turn.NewMessages) != 2 || turn.NewMessages[0].Text != "私聊一" || turn.NewMessages[1].Text != "私聊二" {
		t.Errorf("urgent 消息应合并成 1 个 turn, got %+v", turn.NewMessages)
	}
	time.Sleep(150 * time.Millisecond)
	if got := f.count(); got != 1 {
		t.Errorf("不应立即投递产生第二个 turn, LLM 请求数 = %d", got)
	}
}

// TestUrgentFromEventJudgment wiring 的紧急度判定规则（spec §5）：
// 私聊一律紧急；群聊仅当 @ 了机器人自身（按事件自带 self_id）才紧急。
func TestUrgentFromEventJudgment(t *testing.T) {
	atSelf := []*evmsg.Message{{Type: "at", Data: evmsg.At{QQ: "999"}}}
	atOther := []*evmsg.Message{{Type: "at", Data: evmsg.At{QQ: "123"}}}
	plain := []*evmsg.Message{{Type: "text", Data: evmsg.Text{Text: "hi"}}}

	if !urgentFromEvent("private", plain, 999) {
		t.Error("私聊应判定为紧急")
	}
	if !urgentFromEvent("group", atSelf, 999) {
		t.Error("群聊 @ 机器人应判定为紧急")
	}
	if urgentFromEvent("group", atOther, 999) {
		t.Error("群聊 @ 其他人不是紧急")
	}
	if urgentFromEvent("group", plain, 999) {
		t.Error("群聊未 @ 不是紧急")
	}
	if urgentFromEvent("group", atSelf, 0) {
		t.Error("selfID 为 0 时无法判定 @, 应非紧急")
	}
}
