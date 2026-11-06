package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// newLoopManager 构造注入临时 notes/state/debug 路径与真实内存库的 manager。
func newLoopManager(t *testing.T, cfg config.AgentConfig, b *stubBuilder, store *storage.Store, script []string) (*Manager, *fakeLLM, *napcatRec) {
	t.Helper()
	f, srv := newFakeLLM(t, "")
	f.mu.Lock()
	f.script = script
	f.mu.Unlock()
	m := NewManager(b, NewClient(srv.URL, "sk-test", 5*time.Second), cfg, store)
	dir := t.TempDir()
	m.notesPath = filepath.Join(dir, "notes.json")
	m.statePath = filepath.Join(dir, "state.json")
	napcat := startFakeNapCat(t)
	return m, f, napcat
}

// toolMsg 摘要工具消息的断言辅助。
type toolMsg struct {
	Role       string
	ToolCallID string
	Content    string
}

// TestThinkLoopFullSequence 假 LLM 依次返回 [help 调用, think+get_recent_messages
// 调用, send_message 调用, finish]：断言 tool 消息按序回填、think 落盘 notes.json、
// 假 NapCat 收到 send_message、循环在 finish 后正常终止。
func TestThinkLoopFullSequence(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	script := []string{
		toolCallResp("c1", "help", ""),
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

	// 每个请求都带全部 7 个工具定义
	for i, req := range f.reqs {
		if len(req.Tools) != 7 {
			t.Errorf("请求[%d] 携带 %d 个工具定义, want 7", i, len(req.Tools))
		}
	}

	// 请求1: help 调用结果已按序回填
	if got := len(f.reqs[1].Messages); got != 3 {
		t.Fatalf("请求1 应有 3 条消息(user+assistant+tool), got %d", got)
	}
	if name := f.reqs[1].Messages[1].ToolCalls[0].Function.Name; name != "help" {
		t.Errorf("请求1 的 assistant 调用 = %q, want help", name)
	}
	tool1 := f.reqs[1].Messages[2]
	if tool1.Role != "tool" || tool1.ToolCallID != "c1" {
		t.Errorf("请求1 的 tool 消息 = %+v, want role=tool tool_call_id=c1", tool1)
	}
	var helpItems []map[string]any
	if err := json.Unmarshal([]byte(tool1.Content), &helpItems); err != nil {
		t.Fatalf("help 结果不是 JSON 数组: %v (%s)", err, tool1.Content)
	}
	if len(helpItems) != 7 {
		t.Errorf("help 结果应列 7 个工具, got %d", len(helpItems))
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
	if msgs2[5].Role != "tool" || msgs2[5].ToolCallID != "c3" || msgs2[5].Content != "[]" {
		t.Errorf("get_recent 的 tool 消息 = %+v, want tool/c3/[]", msgs2[5])
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

	// think 已落盘 notes.json
	notes, err := LoadNotes(m.notesPath)
	if err != nil || len(notes) != 1 || notes[0].Text != "先记一笔" {
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

// TestHandleTurnMaxToolRoundsForcesEnd 轮数耗尽强制结束，此前已发出的消息有效。
func TestHandleTurnMaxToolRoundsForcesEnd(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	cfg.MaxToolRounds = 2
	script := []string{
		toolCallResp("c1", "send_message", `{"chat_type":"group","chat_id":888,"text":"先发一条"}`),
		toolCallResp("c2", "help", ""), // 第 2 轮还在调工具 → 被强制结束
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
	script := []string{toolCallResp("c1", "help", "")}
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
		!strings.Contains(msgs[2].Content, `"error"`) || !strings.Contains(msgs[2].Content, "未知工具") {
		t.Errorf("未知工具的回填 = %+v", msgs[2])
	}
	if msgs[n-1].Role != "tool" || msgs[n-1].ToolCallID != "c2" ||
		!strings.Contains(msgs[n-1].Content, `"error"`) || !strings.Contains(msgs[n-1].Content, "chat_type") {
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
	entries, err := os.ReadDir(sub)
	if err != nil {
		t.Fatalf("读取 debug 目录失败: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("应每轮落盘 1 条、共 2 条记录, got %d", len(entries))
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
