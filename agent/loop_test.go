package agent

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"Q-Mi/storage"
	"Q-Mi/utils/config"
)

// toolCallResp 构造一个只含 tool_calls 的响应 JSON。
func toolCallResp(id, name, args string) string {
	return `{"choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[` +
		`{"id":"` + id + `","type":"function","function":{"name":"` + name + `","arguments":` +
		strconv.Quote(args) + `}}]}}]}`
}

// twoToolCallResp 构造含两个 tool_calls 的响应 JSON。
func twoToolCallResp(id1, name1, args1, id2, name2, args2 string) string {
	return `{"choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[` +
		`{"id":"` + id1 + `","type":"function","function":{"name":"` + name1 + `","arguments":` + strconv.Quote(args1) + `}},` +
		`{"id":"` + id2 + `","type":"function","function":{"name":"` + name2 + `","arguments":` + strconv.Quote(args2) + `}}]}}]}`
}

// reasoningToolCallResp 构造带 reasoning/content 与一个 tool_calls 的响应 JSON。
func reasoningToolCallResp(reasoning, content, id, name, args string) string {
	return `{"choices":[{"index":0,"message":{"role":"assistant","content":` + strconv.Quote(content) +
		`,"reasoning_content":` + strconv.Quote(reasoning) + `,"tool_calls":[` +
		`{"id":"` + id + `","type":"function","function":{"name":"` + name + `","arguments":` +
		strconv.Quote(args) + `}}]}}]}`
}

// newLoopManager 构造注入临时 notes/state/debug 路径与真实内存库的 manager。
func newLoopManager(t *testing.T, cfg config.AgentConfig, b *stubBuilder, store *storage.Store, script []string) (*Manager, *fakeLLM, *napcatRec) {
	t.Helper()
	f, srv := newFakeLLM(t, "")
	f.mu.Lock()
	f.script = script
	f.mu.Unlock()
	m := NewManager(b, NewClient(srv.URL, "sk-test", 5*time.Second), cfg, store, 999)
	dir := t.TempDir()
	m.notesPath = filepath.Join(dir, "notes.json")
	m.statePath = filepath.Join(dir, "state.json")
	m.images = newImageStore(filepath.Join(dir, "images"), store) // 兜底图片存储指到临时目录，防测试污染包目录
	napcat := startFakeNapCat(t)
	return m, f, napcat
}

// toolMsg 摘要工具消息的断言辅助。
type toolMsg struct {
	Role       string
	ToolCallID string
	Content    string
}

// TestThinkLoopFullSequence 假 LLM 依次返回 [think 调用, think+get_recent_messages
// 调用, send_message 调用, finish]：断言 tool 消息按序回填、think 落盘 notes.json、
// 假 NapCat 收到 send_message、循环在 finish 后正常终止。
func TestThinkLoopFullSequence(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	script := []string{
		toolCallResp("c1", "think", `{"note":"x"}`),
		twoToolCallResp("c2", "think", `{"note":"先记一笔"}`, "c3", "get_recent_messages", `{"limit":5}`),
		toolCallResp("c4", "send_message", `{"chat_type":"group","chat_id":888,"text":"嗨"}`),
		toolCallResp("c5", "finish", ""),
	}
	s := openTestStore(t)
	m, f, napcat := newLoopManager(t, cfg, b, s, script)

	m.Enqueue(msg("group", 888, "在吗"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 4 }, "思考循环应依次发出 4 次 LLM 请求")
	time.Sleep(150 * time.Millisecond)
	if got := f.count(); got != 4 {
		t.Fatalf("finish 后循环应终止, LLM 请求数 = %d, want 4", got)
	}

	// 每个请求都带全部工具定义
	for i, req := range f.reqs {
		if len(req.Tools) != len(wantToolNames) {
			t.Errorf("请求[%d] 携带 %d 个工具定义, want %d", i, len(req.Tools), len(wantToolNames))
		}
	}

	// 请求1: help 调用结果已按序回填
	if got := len(f.reqs[1].Messages); got != 3 {
		t.Fatalf("请求1 应有 3 条消息(user+assistant+tool), got %d", got)
	}
	if name := f.reqs[1].Messages[1].ToolCalls[0].Function.Name; name != "think" {
		t.Errorf("请求1 的 assistant 调用 = %q, want think", name)
	}
	tool1 := f.reqs[1].Messages[2]
	if tool1.Role != "tool" || tool1.ToolCallID != "c1" {
		t.Errorf("请求1 的 tool 消息 = %+v, want role=tool tool_call_id=c1", tool1)
	}
	if got := contentText(tool1); got != "{}" {
		t.Errorf("think 结果 = %q, want {}", got)
	}

	// 请求2: think + get_recent_messages 两个 tool 消息按序回填
	msgs2 := f.reqs[2].Messages
	if got := len(msgs2); got != 6 {
		t.Fatalf("请求2 应有 6 条消息, got %d: %+v", got, msgs2)
	}
	calls2 := msgs2[3].ToolCalls
	if len(calls2) != 2 || calls2[0].Function.Name != "think" || calls2[1].Function.Name != "get_recent_messages" {
		t.Errorf("请求2 的 assistant.tool_calls = %+v", calls2)
	}
	if msgs2[4].Role != "tool" || msgs2[4].ToolCallID != "c2" || msgs2[4].Content != "{}" {
		t.Errorf("think 的 tool 消息 = %+v, want tool/c2/{}", msgs2[4])
	}
	if msgs2[5].Role != "tool" || msgs2[5].ToolCallID != "c3" {
		t.Errorf("get_recent 的 tool 消息 = %+v, want role=tool tool_call_id=c3", msgs2[5])
	}
	if rows := messageRowsOf(t, contentText(msgs2[5])); len(rows) != 0 {
		t.Errorf("空记忆库返回了消息: %#v", rows)
	}

	// 请求3: send_message 结果 {"message_id":1} 已回填
	msgs3 := f.reqs[3].Messages
	if got := len(msgs3); got != 8 {
		t.Fatalf("请求3 应有 8 条消息, got %d", got)
	}
	if name := msgs3[6].ToolCalls[0].Function.Name; name != "send_message" {
		t.Errorf("请求3 的 assistant 调用 = %q, want send_message", name)
	}
	if msgs3[7].ToolCallID != "c4" || msgs3[7].Content != `{"message_id":1}` {
		t.Errorf("send_message 的 tool 消息 = %+v, want tool/c4/{\"message_id\":1}", msgs3[7])
	}

	// think 已落盘 notes.json（脚本两次 think：x、先记一笔）
	notes, err := LoadNotes(m.notesPath)
	if err != nil || len(notes) != 2 || notes[0].Text != "x" || notes[1].Text != "先记一笔" {
		t.Errorf("think 应落盘 notes.json: notes=%+v err=%v", notes, err)
	}

	// NapCat 收到 send_message 发出的消息
	if napcat.count() != 1 || napcat.paths[0] != "/send_group_msg" {
		t.Fatalf("NapCat 应收到 1 次 /send_group_msg, got %v", napcat.paths)
	}
	if got := napcat.sentText(t, 0); got != "嗨" {
		t.Errorf("发送文本 = %q, want 嗨", got)
	}

	// 请求4（最后一次）的前置消息包含 finish 之前的全部回填
	if last := f.reqs[3].Messages[len(f.reqs[3].Messages)-1]; last.ToolCallID != "c4" {
		t.Errorf("最后一次请求应以 send_message 的 tool 消息结尾, got %+v", last)
	}
}

// TestHandleTurnLoadsNotesAndMoodIntoTurn 每 turn 开头从 notes.json / state.json
// 加载笔记与情绪，填进 Turn（builder 可见）。
func TestHandleTurnLoadsNotesAndMoodIntoTurn(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	m, f, _ := newLoopManager(t, cfg, b, nil, nil)

	if err := AppendNote(m.notesPath, "上轮记的"); err != nil {
		t.Fatal(err)
	}
	if err := writeState(m.statePath, "困了"); err != nil {
		t.Fatal(err)
	}

	m.Enqueue(msg("group", 888, "在吗"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 1 }, "LLM 应收到请求")

	turn := b.turn(0)
	if len(turn.Notes) != 1 || turn.Notes[0].Text != "上轮记的" {
		t.Errorf("turn.Notes = %+v, want 1 条「上轮记的」", turn.Notes)
	}
	if turn.Mood != "困了" {
		t.Errorf("turn.Mood = %q, want 困了", turn.Mood)
	}
}

// TestHandleTurnPlainTextCompat 纯文本响应直接经 bot.Reply 发出（兼容路径）。
func TestHandleTurnPlainTextCompat(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	m, f, napcat := newLoopManager(t, cfg, b, nil, []string{
		`{"choices":[{"index":0,"message":{"role":"assistant","content":"直接回复"}}]}`,
	})

	m.Enqueue(msg("private", 20002, "你好"))
	waitFor(t, 5*time.Second, func() bool { return napcat.count() == 1 }, "纯文本应直接回复")
	if f.count() != 1 {
		t.Errorf("纯文本路径应只有 1 次 LLM 请求, got %d", f.count())
	}
	if napcat.paths[0] != "/send_private_msg" {
		t.Errorf("回复 path = %q, want /send_private_msg", napcat.paths[0])
	}
	if got := napcat.sentText(t, 0); got != "直接回复" {
		t.Errorf("回复文本 = %q, want 直接回复", got)
	}
}

// lockedWriter 并发安全的 bytes.Buffer（handleTurn 的日志在 worker 协程产生，
// 测试协程读取需要锁）。
type lockedWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *lockedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// captureLogsSync 把默认 slog 输出重定向到并发安全缓冲并返回它
// （captureLogs 的异步版，供思考循环这类 worker 协程打日志的场景使用）。
func captureLogsSync(t *testing.T) *lockedWriter {
	t.Helper()
	w := &lockedWriter{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(w, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return w
}

// TestHandleTurnSegmentTypeErrorBackfilled send_message 收到白名单外的
// 段类型时报错并以 {"error":...} 回填思维链，模型可自纠，不发消息。
func TestHandleTurnSegmentTypeErrorBackfilled(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	script := []string{
		toolCallResp("c1", "send_message", `{"chat_type":"group","chat_id":888,"segments":[{"type":"poke","data":{"id":"1"}}]}`),
		toolCallResp("c2", "finish", ""),
	}
	m, f, napcat := newLoopManager(t, cfg, b, nil, script)

	m.Enqueue(msg("group", 888, "在吗"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 2 }, "报错后思维链应继续")
	time.Sleep(100 * time.Millisecond)

	msgs := f.reqs[1].Messages
	last := msgs[len(msgs)-1]
	if last.Role != "tool" || last.ToolCallID != "c1" ||
		!strings.Contains(contentText(last), `"error"`) || !strings.Contains(contentText(last), "poke") {
		t.Errorf("非法段类型应以 error 回填: %+v", last)
	}
	if napcat.count() != 0 {
		t.Errorf("报错路径不应发消息, got %d", napcat.count())
	}
}

// TestHandleTurnLogsThinkingChain 每轮拿到 LLM 响应后在控制台输出思考链
// （P2.5）：reasoning → 「思考」、content → 「输出」、每个工具调用 → 「工具调用」、
// finish → 「结束思考」，均带 chat_type/chat_id/round 字段；工具执行失败
// 也有一条简短 Info。
func TestHandleTurnLogsThinkingChain(t *testing.T) {
	logs := captureLogsSync(t)
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	script := []string{
		// 第 1 轮：带 reasoning + content + 一次工具调用
		reasoningToolCallResp("用户在打招呼，先查查记录再说", "让我想想", "c1", "get_recent_messages", `{"limit":5}`),
		// 第 2 轮：工具执行失败（未知工具）→ 简短 Info
		toolCallResp("c2", "no_such_tool", `{}`),
		// 第 3 轮：finish → 结束思考
		toolCallResp("c3", "finish", ""),
	}
	m, f, _ := newLoopManager(t, cfg, b, nil, script)

	m.Enqueue(msg("group", 888, "在吗"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 3 }, "应依次发出 3 次 LLM 请求")
	time.Sleep(150 * time.Millisecond)

	all := logs.String()
	lines := strings.Split(all, "\n")
	// find 找到含 msg=<msg> 且包含全部 mustContain 片段的日志行。
	find := func(msg string, mustContain ...string) string {
		t.Helper()
		for _, ln := range lines {
			if !strings.Contains(ln, "msg="+msg) {
				continue
			}
			ok := true
			for _, s := range mustContain {
				if !strings.Contains(ln, s) {
					ok = false
					break
				}
			}
			if ok {
				return ln
			}
		}
		return ""
	}

	if ln := find("思考", "reasoning=用户在打招呼，先查查记录再说", "chat_type=group", "chat_id=888", "round=1"); ln == "" {
		t.Errorf("reasoning 应产生「思考」日志行:\n%s", all)
	}
	if ln := find("输出", "content=让我想想", "chat_type=group", "chat_id=888", "round=1"); ln == "" {
		t.Errorf("content 应产生「输出」日志行:\n%s", all)
	}
	if ln := find("工具调用", "tool=get_recent_messages", "args=", "chat_type=group", "chat_id=888", "round=1"); ln == "" {
		t.Errorf("工具调用应产生「工具调用」日志行（含 args）:\n%s", all)
	}
	if ln := find("工具执行失败", "tool=no_such_tool", "chat_type=group", "chat_id=888", "round=2"); ln == "" {
		t.Errorf("工具执行失败应产生简短 Info 日志行:\n%s", all)
	}
	if ln := find("结束思考", "chat_type=group", "chat_id=888", "round=3"); ln == "" {
		t.Errorf("finish 应产生「结束思考」日志行:\n%s", all)
	}
}

func TestHandleTurnSenseNovaReasoningLoggedButNotReplayedOrSent(t *testing.T) {
	logs := captureLogsSync(t)
	cfg := testCfg(20 * time.Millisecond)
	m, f, napcat := newLoopManager(t, cfg, &stubBuilder{}, nil, []string{
		`{"choices":[{"index":0,"message":{"role":"assistant","reasoning":"probe-private-reasoning","tool_calls":[{"id":"c1","type":"function","function":{"name":"get_recent_messages","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
		toolCallResp("c2", "finish", "{}"),
	})
	turn := &Turn{ChatType: "group", ChatID: 888, SelfID: 999, Now: time.Now(),
		NewMessages: []TurnMessage{{ChatType: "group", ChatID: 888, Text: "在吗"}}}
	m.handleTurn(turn)
	if all := logs.String(); !strings.Contains(all, "msg=思考") ||
		!strings.Contains(all, "reasoning=probe-private-reasoning") {
		t.Fatalf("SenseNova reasoning missing from thinking log:\n%s", all)
	}
	if napcat.count() != 0 {
		t.Fatalf("reasoning must not be sent to QQ: %d requests", napcat.count())
	}
	if f.count() != 2 {
		t.Fatalf("tool cycle did not continue: %d requests", f.count())
	}
	wire, err := json.Marshal(f.reqs[1].Messages)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "probe-private-reasoning") {
		t.Fatal("provider reasoning leaked into replayed conversation")
	}
}

// TestHandleTurnMaxToolRoundsForcesEnd 轮数耗尽强制结束，此前已发出的消息有效。
func TestHandleTurnMaxToolRoundsForcesEnd(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	cfg.MaxToolRounds = 2
	script := []string{
		toolCallResp("c1", "send_message", `{"chat_type":"group","chat_id":888,"text":"先发一条"}`),
		toolCallResp("c2", "think", `{"note":"x"}`), // 第 2 轮还在调工具 → 被强制结束
	}
	m, f, napcat := newLoopManager(t, cfg, b, nil, script)

	m.Enqueue(msg("group", 888, "在吗"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 2 }, "应恰好发出 2 次 LLM 请求")
	time.Sleep(150 * time.Millisecond)
	if got := f.count(); got != 2 {
		t.Fatalf("轮数上限后循环应终止, LLM 请求数 = %d, want 2", got)
	}
	if napcat.count() != 1 || napcat.sentText(t, 0) != "先发一条" {
		t.Errorf("上限前发出的消息应有效: count=%d", napcat.count())
	}
}

// TestHandleTurnDefaultMaxToolRoundsZeroFallsBackToTen 零值兜底 10 轮。
func TestHandleTurnDefaultMaxToolRoundsZeroFallsBackToTen(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond) // MaxToolRounds 未配置 = 0
	script := []string{toolCallResp("c1", "think", `{"note":"x"}`)}
	m, f, napcat := newLoopManager(t, cfg, b, nil, script)

	m.Enqueue(msg("group", 888, "在吗"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 10 }, "零值应兜底 10 轮")
	time.Sleep(150 * time.Millisecond)
	if got := f.count(); got != 10 {
		t.Fatalf("循环应在 10 轮强制结束, got %d", got)
	}
	if napcat.count() != 0 {
		t.Errorf("全程未调 send_message 不应发消息, got %d", napcat.count())
	}
}

// TestHandleTurnFinishMarkedNotExecuted finish 只标记不执行：
// 同批其他工具先执行，然后终止（无第 3 次请求）。
func TestHandleTurnFinishMarkedNotExecuted(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	script := []string{
		twoToolCallResp("c1", "think", `{"note":"收尾笔记"}`, "c2", "finish", ""),
	}
	m, f, _ := newLoopManager(t, cfg, b, nil, script)

	m.Enqueue(msg("group", 888, "在吗"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 1 }, "LLM 应收到请求")
	time.Sleep(150 * time.Millisecond)
	if got := f.count(); got != 1 {
		t.Fatalf("finish 应终止循环, LLM 请求数 = %d, want 1", got)
	}
	notes, err := LoadNotes(m.notesPath)
	if err != nil || len(notes) != 1 || notes[0].Text != "收尾笔记" {
		t.Errorf("finish 同批的 think 应先执行: notes=%+v err=%v", notes, err)
	}
}

// TestHandleTurnToolErrorContinues 工具报错以 {"error":...} 回填后思维链继续。
func TestHandleTurnToolErrorContinues(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	script := []string{
		toolCallResp("c1", "no_such_tool", `{}`),                // 未知工具 → {"error"}
		toolCallResp("c2", "send_message", `{"chat_type":"x"}`), // 非法参数 → {"error"}
		toolCallResp("c3", "finish", ""),
	}
	m, f, napcat := newLoopManager(t, cfg, b, nil, script)

	m.Enqueue(msg("group", 888, "在吗"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 3 }, "工具报错后思维链应继续")
	time.Sleep(100 * time.Millisecond)

	// 请求2 消息序列：user, asst(c1), tool(c1 错误), asst(c2), tool(c2 错误)
	msgs := f.reqs[2].Messages
	n := len(msgs)
	if n != 5 {
		t.Fatalf("请求2 消息数 = %d, want 5", n)
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "c1" ||
		!strings.Contains(contentText(msgs[2]), `"error"`) || !strings.Contains(contentText(msgs[2]), "未知工具") {
		t.Errorf("未知工具的回填 = %+v", msgs[2])
	}
	if msgs[n-1].Role != "tool" || msgs[n-1].ToolCallID != "c2" ||
		!strings.Contains(contentText(msgs[n-1]), `"error"`) || !strings.Contains(contentText(msgs[n-1]), "chat_type") {
		t.Errorf("非法参数的回填 = %+v", msgs[n-1])
	}
	if napcat.count() != 0 {
		t.Errorf("报错路径不应发消息, got %d", napcat.count())
	}
}

// TestDebugDumpPerRoundWithToolCalls debug 开启时每轮落盘一条记录，
// 含轮数、工具调用摘要与 interrupted 备用字段。
func TestDebugDumpPerRoundWithToolCalls(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	cfg.Debug = true
	script := []string{
		toolCallResp("c1", "think", `{"note":"n"}`),
		toolCallResp("c2", "finish", ""),
	}
	m, f, _ := newLoopManager(t, cfg, b, nil, script)
	m.debugDir = t.TempDir()

	m.Enqueue(msg("group", 888, "在吗"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 2 }, "LLM 应收到 2 次请求")

	sub := filepath.Join(m.debugDir, "group-888")
	// f.count() 在请求到达假 LLM 时就满足，而 dumpDebug 在拿到响应、
	// 处理完工具后才写盘——轮询等待两条记录落齐，避免读早了。
	waitFor(t, 5*time.Second, func() bool {
		entries, err := os.ReadDir(sub)
		return err == nil && len(entries) == 2
	}, "应每轮落盘 1 条、共 2 条记录")
	entries, err := os.ReadDir(sub)
	if err != nil {
		t.Fatalf("读取 debug 目录失败: %v", err)
	}
	type rec struct {
		Round       int  `json:"round"`
		Interrupted bool `json:"interrupted"`
		ToolCalls   []struct {
			Name string `json:"name"`
		} `json:"tool_calls"`
	}
	var rounds []int
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(sub, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var r rec
		if err := json.Unmarshal(data, &r); err != nil {
			t.Fatalf("debug 记录不是合法 JSON: %v", err)
		}
		rounds = append(rounds, r.Round)
		if r.Interrupted {
			t.Error("未被 interrupted 的记录 interrupted 应为 false")
		}
	}
	if len(rounds) != 2 || rounds[0] != 1 || rounds[1] != 2 {
		t.Errorf("记录轮数 = %v, want [1 2]", rounds)
	}
}

// TestScheduleToolFiresReminderIntoLoop schedule 工具走通全链（P3.5）：
// LLM 调 schedule → TimerStore 到点 → manager.fireTimer 合成 [定时提醒]
// IncomingMessage 入队 → 防抖合成新 turn → 思考循环再次醒来，
// fakeLLM 收到提醒文本；提醒消息发送者为「定时」/SelfID。
func TestScheduleToolFiresReminderIntoLoop(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	script := []string{
		toolCallResp("c1", "schedule", `{"delay_seconds":1,"note":"10分钟后看看他回了没"}`),
		toolCallResp("c2", "finish", ""),
		toolCallResp("c3", "finish", ""), // 定时提醒唤醒的 turn
	}
	m, f, _ := newLoopManager(t, cfg, b, nil, script)
	// 计时器与 notes/state 同模式注入临时路径；fire 回调走 manager 真实链路
	m.timerStore = NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), m.fireTimer)
	// builder 把本轮首条新消息文本透传给 LLM，使提醒文本可断言
	b.mu.Lock()
	b.fn = func(turn *Turn) []ChatMessage {
		text := ""
		if len(turn.NewMessages) > 0 {
			text = turn.NewMessages[0].Text
		}
		return []ChatMessage{{Role: "user", Content: text}}
	}
	b.mu.Unlock()

	m.Enqueue(msg("group", 888, "帮我记个事"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 2 }, "第 1 个 turn 应完成 schedule + finish")
	waitFor(t, 10*time.Second, func() bool { return f.count() == 3 }, "定时提醒应唤醒第 2 个 turn")

	// schedule 结果已回填思维链：含 id 与 fire_at
	last := f.reqs[1].Messages[len(f.reqs[1].Messages)-1]
	if last.Role != "tool" || last.ToolCallID != "c1" ||
		!strings.Contains(contentText(last), `"id"`) || !strings.Contains(contentText(last), `"fire_at"`) {
		t.Errorf("schedule 结果应含 id/fire_at 并回填: %+v (%s)", last, contentText(last))
	}

	// 提醒 turn：SenderName「定时」、SenderID=SelfID、文本 [定时提醒] + note
	turn := b.turn(1)
	if turn.ChatType != "group" || turn.ChatID != 888 {
		t.Errorf("提醒 turn 会话错误: %+v", turn)
	}
	if len(turn.NewMessages) != 1 {
		t.Fatalf("提醒 turn 应恰有 1 条新消息, got %d", len(turn.NewMessages))
	}
	tm := turn.NewMessages[0]
	if tm.SenderName != "定时" || tm.SenderID != 999 {
		t.Errorf("提醒消息发送者 = %q/%d, want 定时/999（SelfID）", tm.SenderName, tm.SenderID)
	}
	if !strings.Contains(tm.Text, "[定时提醒]") || !strings.Contains(tm.Text, "10分钟后看看他回了没") {
		t.Errorf("提醒文本 = %q, want [定时提醒] 前缀 + note", tm.Text)
	}

	// fakeLLM 收到提醒文本（经 builder 进入请求 user 消息）
	if text := requestText(f.reqs[2]); !strings.Contains(text, "[定时提醒]") {
		t.Errorf("提醒文本应进入连续上下文: %s", text)
	}
	// 触发后计时器清空
	if got := m.timerStore.Active(); got != 0 {
		t.Errorf("触发后 Active = %d, want 0", got)
	}
}
