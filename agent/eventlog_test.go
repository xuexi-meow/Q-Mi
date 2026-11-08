// eventlog_test.go W1 WebUI 事件环形缓冲与导出钩子（spec §3.1）：
// 环形回绕、After 增量快照、并发安全、钩子 nil 安全与 Wake 集成。
package agent

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestEventLogRecordAfterOrdering Record 后 After 返回 ID 递增的完整快照；
// After(after) 只返回 ID > after 的部分。
func TestEventLogRecordAfterOrdering(t *testing.T) {
	l := NewEventLog()
	l.Record("group", 888, KindTurnStart, "开始")
	l.Record("group", 888, KindReasoning, "想一想")
	l.Record("group", 888, KindFinish, "结束")

	all := l.After(0)
	if len(all) != 3 {
		t.Fatalf("After(0) 返回 %d 条, want 3", len(all))
	}
	for i, e := range all {
		if e.ID != uint64(i+1) {
			t.Errorf("events[%d].ID = %d, want %d", i, e.ID, i+1)
		}
	}
	if all[0].Kind != KindTurnStart || all[0].Text != "开始" ||
		all[0].ChatType != "group" || all[0].ChatID != 888 {
		t.Errorf("events[0] = %+v", all[0])
	}
	if all[0].Time.IsZero() {
		t.Error("event.Time 应为记录时刻, got zero")
	}
	if all[2].Kind != KindFinish {
		t.Errorf("events[2].Kind = %q, want %q", all[2].Kind, KindFinish)
	}

	tail := l.After(2)
	if len(tail) != 1 || tail[0].ID != 3 {
		t.Errorf("After(2) = %+v, want 仅 ID=3", tail)
	}
	if got := l.After(3); len(got) != 0 {
		t.Errorf("After(3) 应为空, got %+v", got)
	}
}

// TestEventLogRingWrapsAtCapacity 超过容量 500 后丢最旧的：
// 全量快照恒为最近 500 条且 ID 连续递增。
func TestEventLogRingWrapsAtCapacity(t *testing.T) {
	l := NewEventLog()
	const extra = 7
	for i := 0; i < eventLogCapacity+extra; i++ {
		l.Record("", 0, KindOutput, strings.Repeat("x", i%3))
	}
	all := l.After(0)
	if len(all) != eventLogCapacity {
		t.Fatalf("环形缓冲应只保留 %d 条, got %d", eventLogCapacity, len(all))
	}
	firstID := uint64(extra + 1)
	for i, e := range all {
		want := firstID + uint64(i)
		if e.ID != want {
			t.Fatalf("events[%d].ID = %d, want %d（回绕后应丢弃最旧）", i, e.ID, want)
		}
	}
	// After 环绕中的 ID 增量也正确
	if got := l.After(uint64(eventLogCapacity + extra - 3)); len(got) != 3 {
		t.Errorf("After(最后第4条ID) 应返回 3 条, got %d", len(got))
	}
}

// TestEventLogConcurrentRecord 并发 Record 不丢不重（-race 下运行）。
func TestEventLogConcurrentRecord(t *testing.T) {
	l := NewEventLog()
	const workers, per = 8, 100
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				l.Record("group", 888, KindOutput, "并发")
			}
		}()
	}
	wg.Wait()
	all := l.After(0)
	if len(all) != eventLogCapacity {
		t.Fatalf("并发写入后应保留 %d 条, got %d", eventLogCapacity, len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].ID != all[i-1].ID+1 {
			t.Fatalf("ID 不连续: %d 之后是 %d", all[i-1].ID, all[i].ID)
		}
	}
}

// TestEventsAfterNilSafe agent 管理器未初始化时导出钩子返回空，不 panic。
func TestEventsAfterNilSafe(t *testing.T) {
	managerMu.Lock()
	saved, savedInited := manager, managerInited
	manager, managerInited = nil, true
	managerMu.Unlock()
	t.Cleanup(func() {
		managerMu.Lock()
		manager, managerInited = saved, savedInited
		managerMu.Unlock()
	})

	if evs := EventsAfter(0); len(evs) != 0 {
		t.Errorf("manager 为 nil 时 EventsAfter 应返回空, got %+v", evs)
	}
}

// TestWakeNilManager agent 未启用时 Wake 返回错误。
func TestWakeNilManager(t *testing.T) {
	managerMu.Lock()
	saved, savedInited := manager, managerInited
	manager, managerInited = nil, true
	managerMu.Unlock()
	t.Cleanup(func() {
		managerMu.Lock()
		manager, managerInited = saved, savedInited
		managerMu.Unlock()
	})

	if err := Wake("测试"); err == nil {
		t.Error("manager 为 nil 时 Wake 应返回错误")
	}
}

// TestWakeEnqueuesSelfMessage manager 存在且 selfID>0 时 Wake 注入
// private:self 会话的 [WebUI] 前缀消息（spec §3.2）。
func TestWakeEnqueuesSelfMessage(t *testing.T) {
	b := &stubBuilder{}
	m, _ := newTestManager(t, testCfg(20*time.Millisecond), b)
	m.selfID = 999

	managerMu.Lock()
	saved, savedInited := manager, managerInited
	manager, managerInited = m, true
	managerMu.Unlock()
	t.Cleanup(func() {
		managerMu.Lock()
		manager, managerInited = saved, savedInited
		managerMu.Unlock()
	})

	if err := Wake("起来干活"); err != nil {
		t.Fatalf("Wake 返回错误: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool { return b.callCount() == 1 }, "Wake 应唤醒一个 turn")
	turn := b.turn(0)
	if turn.ChatType != "private" || turn.ChatID != 999 {
		t.Errorf("Wake turn 会话 = %s/%d, want private/999", turn.ChatType, turn.ChatID)
	}
	if len(turn.NewMessages) != 1 {
		t.Fatalf("Wake turn 应含 1 条消息, got %d", len(turn.NewMessages))
	}
	tm := turn.NewMessages[0]
	if tm.SenderID != 999 || tm.SenderName != "WebUI" || !strings.HasPrefix(tm.Text, "[WebUI] ") {
		t.Errorf("Wake 消息 = %+v, want SenderID=999 SenderName=WebUI Text 前缀 [WebUI]", tm)
	}
	if !strings.Contains(tm.Text, "起来干活") {
		t.Errorf("Wake 消息文本 = %q, want 含原文", tm.Text)
	}
}

// TestWakeSelfIDZero manager 存在但 selfID=0（未配置 QQ 号）时 Wake 报错。
func TestWakeSelfIDZero(t *testing.T) {
	b := &stubBuilder{}
	m, _ := newTestManager(t, testCfg(20*time.Millisecond), b)
	m.selfID = 0

	managerMu.Lock()
	saved, savedInited := manager, managerInited
	manager, managerInited = m, true
	managerMu.Unlock()
	t.Cleanup(func() {
		managerMu.Lock()
		manager, managerInited = saved, savedInited
		managerMu.Unlock()
	})

	err := Wake("测试")
	if err == nil {
		t.Fatal("selfID=0 时 Wake 应返回错误")
	}
	if b.callCount() != 0 {
		t.Errorf("selfID=0 时不应入队, builder 收到 %d 次", b.callCount())
	}
}

// TestMoodGetAndNotesListHooks MoodGet 读 agent/state.json、NotesList 读
// agent/notes.json（相对工作目录）；文件缺失返回零值不报错。
func TestMoodGetAndNotesListHooks(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if got := MoodGet(); got != "" {
		t.Errorf("无状态文件时 MoodGet = %q, want 空串", got)
	}
	if notes := NotesList(); len(notes) != 0 {
		t.Errorf("无笔记文件时 NotesList 应为空, got %+v", notes)
	}
	if err := writeState(defaultStatePath, "有点困"); err != nil {
		t.Fatal(err)
	}
	if err := AppendNote(defaultNotesPath, "记一下"); err != nil {
		t.Fatal(err)
	}
	if got := MoodGet(); got != "有点困" {
		t.Errorf("MoodGet = %q, want 有点困", got)
	}
	notes := NotesList()
	if len(notes) != 1 || notes[0].Text != "记一下" {
		t.Errorf("NotesList = %+v, want 1 条「记一下」", notes)
	}
}

// TestTimerCountNilSafe W3 起 TimerCount 返回真实计数；manager 未启用时为 0
// （注入 nil 避免触发 getManager 惰性初始化读到真实配置）。
func TestTimerCountNilSafe(t *testing.T) {
	withHookManager(t, nil)
	if got := TimerCount(); got != 0 {
		t.Errorf("TimerCount = %d, want 0", got)
	}
}

// TestEventKindConstants Kind 常量与 spec §3.1 一致，避免手滑写错字符串。
func TestEventKindConstants(t *testing.T) {
	want := map[string]struct{}{
		KindTurnStart: {}, KindReasoning: {}, KindOutput: {}, KindToolCall: {},
		KindToolResult: {}, KindReply: {}, KindError: {}, KindFinish: {}, KindInterrupted: {},
	}
	if len(want) != 9 {
		t.Fatal("kind 常量有重复值")
	}
	for _, k := range []string{"turn_start", "reasoning", "output", "tool_call", "tool_result", "reply", "error", "finish", "interrupted"} {
		if _, ok := want[k]; !ok {
			t.Errorf("缺少 kind 常量 %q", k)
		}
	}
}

// TestNilEventLogRecordRecordSafe 零值/nil *EventLog 的 Record 不 panic
// （Manager 测试直接构造字段时兜底）。
func TestNilEventLogRecordRecordSafe(t *testing.T) {
	var l *EventLog
	l.Record("group", 1, KindOutput, "noop")
	if evs := l.After(0); len(evs) != 0 {
		t.Errorf("nil EventLog After 应为空, got %+v", evs)
	}
	_ = errors.New // 保持 import
}

// kindsOf 抽取事件 kind 序列的断言辅助。
func kindsOf(evs []Event) []string {
	kinds := make([]string, len(evs))
	for i, e := range evs {
		kinds[i] = e.Kind
	}
	return kinds
}

func kindsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestHandleTurnRecordsThinkingChainEvents handleTurn 各关键点位写入事件：
// 进入 turn（turn_start 带新消息摘要）、每轮用量（usage）、reasoning、
// output、tool_call、tool 错误结果（tool_result）、finish。
func TestHandleTurnRecordsThinkingChainEvents(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	script := []string{
		reasoningToolCallResp("先查查记录", "让我想想", "c1", "no_such_tool", `{}`),
		toolCallResp("c2", "finish", ""),
	}
	m, f, _ := newLoopManager(t, cfg, b, nil, script)

	m.Enqueue(msg("group", 888, "在吗"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 2 }, "应依次发出 2 次 LLM 请求")
	time.Sleep(150 * time.Millisecond)

	evs := m.events.After(0)
	kinds := kindsOf(evs)
	want := []string{KindTurnStart, KindUsage, KindReasoning, KindOutput, KindToolCall, KindToolResult, KindUsage, KindFinish}
	if !kindsEqual(kinds, want) {
		t.Fatalf("事件 kind 序列 = %v, want %v", kinds, want)
	}
	if evs[0].ChatType != "group" || evs[0].ChatID != 888 {
		t.Errorf("turn_start 会话 = %s/%d, want group/888", evs[0].ChatType, evs[0].ChatID)
	}
	if evs[0].Text != "测试用户: 在吗" {
		t.Errorf("turn_start 摘要 = %q, want 「测试用户: 在吗」", evs[0].Text)
	}
	if evs[1].Kind != KindUsage || evs[1].Text != "hit=0 miss=0 out=0" {
		t.Errorf("usage 事件 = %q, want hit=0 miss=0 out=0", evs[1].Text)
	}
	if evs[2].Text != "先查查记录" {
		t.Errorf("reasoning 事件文本 = %q, want 全文", evs[2].Text)
	}
	if evs[3].Text != "让我想想" {
		t.Errorf("output 事件文本 = %q, want 全文", evs[3].Text)
	}
	if !strings.Contains(evs[4].Text, "no_such_tool") || !strings.Contains(evs[4].Text, "{}") {
		t.Errorf("tool_call 事件 = %q, want 含工具名与参数", evs[4].Text)
	}
	if !strings.Contains(evs[5].Text, "no_such_tool") || !strings.Contains(evs[5].Text, "未知工具") {
		t.Errorf("tool_result 事件 = %q, want 含工具名与错误简述", evs[5].Text)
	}
}

// TestHandleTurnRecordsReplyAndErrorEvents 纯文本回复成功 → reply 事件；
// 空回复 → error 事件；LLM 请求失败 → error 事件。
func TestHandleTurnRecordsReplyAndErrorEvents(t *testing.T) {
	t.Run("reply on success", func(t *testing.T) {
		b := &stubBuilder{}
		cfg := testCfg(20 * time.Millisecond)
		m, f, napcat := newLoopManager(t, cfg, b, nil, []string{
			`{"choices":[{"index":0,"message":{"role":"assistant","content":"直接回复"}}]}`,
		})
		m.Enqueue(msg("private", 20002, "你好"))
		waitFor(t, 5*time.Second, func() bool { return napcat.count() == 1 }, "纯文本应直接回复")
		time.Sleep(100 * time.Millisecond)
		_ = f
		evs := m.events.After(0)
		if len(evs) == 0 || evs[len(evs)-1].Kind != KindReply || evs[len(evs)-1].Text != "直接回复" {
			t.Errorf("成功回复应以 reply 事件收尾, got %+v", evs)
		}
	})

	t.Run("error on empty reply", func(t *testing.T) {
		b := &stubBuilder{}
		cfg := testCfg(20 * time.Millisecond)
		m, f, _ := newLoopManager(t, cfg, b, nil, []string{
			`{"choices":[{"index":0,"message":{"role":"assistant","content":""}}]}`,
		})
		m.Enqueue(msg("group", 888, "在吗"))
		waitFor(t, 5*time.Second, func() bool { return f.count() == 1 }, "LLM 应收到请求")
		time.Sleep(150 * time.Millisecond)
		evs := m.events.After(0)
		if len(evs) == 0 || evs[len(evs)-1].Kind != KindError {
			t.Errorf("空回复应以 error 事件收尾, got %+v", evs)
		}
	})

	t.Run("error on llm failure", func(t *testing.T) {
		b := &stubBuilder{}
		cfg := testCfg(20 * time.Millisecond)
		f, srv := newFakeLLM(t, "pong")
		m := NewManager(b, NewClient(srv.URL, "sk-test", 5*time.Second), cfg, nil, 999)
		srv.Close() // 立即关闭使 LLM 请求失败
		m.Enqueue(msg("group", 888, "在吗"))
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if evs := m.events.After(0); len(evs) > 0 && evs[len(evs)-1].Kind == KindError {
				if !strings.Contains(evs[len(evs)-1].Text, "connect") && !strings.Contains(evs[len(evs)-1].Text, "refused") &&
					!strings.Contains(evs[len(evs)-1].Text, "EOF") && !strings.Contains(evs[len(evs)-1].Text, "reset") &&
					!strings.Contains(evs[len(evs)-1].Text, "timeout") {
					t.Errorf("LLM 失败 error 事件 = %q, want 连接类错误", evs[len(evs)-1].Text)
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		_ = f
		t.Fatal("LLM 请求失败未产生 error 事件")
	})
}

// TestHandleTurnRecordsMaxRoundsAndInterrupted 轮数上限 → error「轮数上限」；
// 紧急消息打断 → interrupted 事件。
func TestHandleTurnRecordsMaxRoundsAndInterrupted(t *testing.T) {
	t.Run("max rounds", func(t *testing.T) {
		b := &stubBuilder{}
		cfg := testCfg(20 * time.Millisecond)
		cfg.MaxToolRounds = 2
		script := []string{toolCallResp("c1", "help", "")}
		m, f, _ := newLoopManager(t, cfg, b, nil, script)
		m.Enqueue(msg("group", 888, "在吗"))
		waitFor(t, 5*time.Second, func() bool { return f.count() == 2 }, "应恰好发出 2 次 LLM 请求")
		time.Sleep(150 * time.Millisecond)
		evs := m.events.After(0)
		if len(evs) == 0 || evs[len(evs)-1].Kind != KindError || !strings.Contains(evs[len(evs)-1].Text, "轮数上限") {
			t.Errorf("轮数上限应以 error「轮数上限」收尾, got %+v", evs)
		}
	})

	t.Run("interrupted", func(t *testing.T) {
		b := &stubBuilder{}
		cfg := testCfg(60 * time.Millisecond)
		m, f, _ := newLoopManager(t, cfg, b, nil, []string{toolCallResp("c1", "help", "")})
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

		m.Enqueue(msg("group", 888, "慢慢想"))
		<-started
		// 紧急消息打断（spec §5）
		urgent := msg("group", 888, "插话")
		urgent.Urgent = true
		m.Enqueue(urgent)
		waitFor(t, 5*time.Second, func() bool { return b.callCount() == 2 }, "打断后应重开新 turn")
		close(released)

		waitFor(t, 5*time.Second, func() bool {
			for _, e := range m.events.After(0) {
				if e.Kind == KindInterrupted {
					return true
				}
			}
			return false
		}, "打断路径应产生 interrupted 事件")
	})
}
