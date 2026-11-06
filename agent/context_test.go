package agent

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"Q-Mi/storage"
	"Q-Mi/utils/config"
)

// openTestStore 打开内存库供上下文测试使用。
func openTestStore(t *testing.T) *storage.Store {
	t.Helper()
	s, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) 失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// writePersonaFile 写入临时 persona 文件并返回路径。
func writePersonaFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "persona.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 persona 文件失败: %v", err)
	}
	return path
}

// captureLogs 把默认 slog 输出重定向到缓冲区，返回该缓冲区。
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

// TestBuildEmptyTurnReturnsOnlySystem 历史为空且无新消息时只返回 system。
func TestBuildEmptyTurnReturnsOnlySystem(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		History:     config.AgentHistory{Max: 40, Trim: 20},
	}
	b := NewDefaultBuilder(s, cfg)

	msgs := b.Build(&Turn{
		ChatType: "group",
		ChatID:   123456,
		SelfID:   9527,
		Now:      time.Unix(1762400000, 0),
	})

	if len(msgs) != 1 {
		t.Fatalf("Build 返回 %d 条消息, want 1 (仅 system): %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" {
		t.Errorf("messages[0].Role = %q, want %q", msgs[0].Role, "system")
	}
	want := "你是QQ上的AI成员。\n当前账号QQ: 9527"
	if msgs[0].Content != want {
		t.Errorf("system content = %q, want %q", msgs[0].Content, want)
	}
}

// TestNewDefaultBuilderMissingPersonaWarnsOnceAndFallsBack persona 文件缺失时
// 构造期告警恰好一次，system 使用内置兜底提示词。
func TestNewDefaultBuilderMissingPersonaWarnsOnceAndFallsBack(t *testing.T) {
	s := openTestStore(t)
	logs := captureLogs(t)
	missing := filepath.Join(t.TempDir(), "absent-persona.md")
	cfg := config.AgentConfig{
		PersonaFile: missing,
		History:     config.AgentHistory{Max: 40, Trim: 20},
	}

	b := NewDefaultBuilder(s, cfg)
	msgs := b.Build(&Turn{ChatType: "private", ChatID: 10001, SelfID: 9527, Now: time.Unix(1762400000, 0)})

	if len(msgs) != 1 || msgs[0].Role != "system" {
		t.Fatalf("缺 persona 时 Build 应仅返回 system, got %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "你是一个QQ群聊/私聊里的AI成员") {
		t.Errorf("兜底提示词缺失: %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "当前账号QQ: 9527") {
		t.Errorf("system 未携带自身QQ号: %q", msgs[0].Content)
	}

	warns := 0
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		if !strings.Contains(line, "WARN") {
			t.Errorf("persona 缺失只应产生告警日志, got: %s", line)
			continue
		}
		if !strings.Contains(line, "absent-persona.md") {
			t.Errorf("告警应包含 persona 路径: %s", line)
		}
		warns++
	}
	if warns != 1 {
		t.Errorf("persona 缺失应恰好告警一次, got %d 次: %s", warns, logs.String())
	}
}

// putRecord 写入一条消息记录。
func putRecord(t *testing.T, s *storage.Store, id int64, direction, chatType string, chatID, userID int64, raw string, sec int64) {
	t.Helper()
	err := s.PutMessage(&storage.MessageRecord{
		MessageID:  id,
		Direction:  direction,
		ChatType:   chatType,
		ChatID:     chatID,
		UserID:     userID,
		RawMessage: raw,
		Time:       float64(sec),
	})
	if err != nil {
		t.Fatalf("PutMessage(%d): %v", id, err)
	}
}

// assertMessages 逐条比较 role 与 content。
func assertMessages(t *testing.T, got, want []ChatMessage) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("消息条数 = %d, want %d\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("messages[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestBuildHistoryFormatAndRoles 历史按时间正序、格式 "[MM-DD HH:MM] <QQ号或'我'> 内容"、
// sent 记 own 为 assistant，CQ 码原样透传不解析。
func TestBuildHistoryFormatAndRoles(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		History:     config.AgentHistory{Max: 40, Trim: 20},
	}
	b := NewDefaultBuilder(s, cfg)

	t1 := time.Unix(1762400000, 0)
	t2 := t1.Add(60 * time.Second)
	t3 := t2.Add(60 * time.Second)
	putRecord(t, s, 1, "recv", "group", 123456, 10001, "你好", t1.Unix())
	putRecord(t, s, 2, "sent", "group", 123456, 9527, "你好呀", t2.Unix())
	putRecord(t, s, 3, "recv", "group", 123456, 10002, "[CQ:face,id=1]哈哈", t3.Unix())

	msgs := b.Build(&Turn{ChatType: "group", ChatID: 123456, SelfID: 9527, Now: t3.Add(time.Minute)})

	assertMessages(t, msgs, []ChatMessage{
		{Role: "system", Content: "你是QQ上的AI成员。\n当前账号QQ: 9527"},
		{Role: "user", Content: "[" + t1.Format("01-02 15:04") + "] <10001> 你好"},
		{Role: "assistant", Content: "[" + t2.Format("01-02 15:04") + "] <我> 你好呀"},
		{Role: "user", Content: "[" + t3.Format("01-02 15:04") + "] <10002> [CQ:face,id=1]哈哈"},
	})
}

// TestBuildHistoryExcludesRecalled 默认排除已撤回消息。
func TestBuildHistoryExcludesRecalled(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		History:     config.AgentHistory{Max: 40, Trim: 20},
	}
	b := NewDefaultBuilder(s, cfg)

	t1 := time.Unix(1762400000, 0)
	t2 := t1.Add(60 * time.Second)
	t3 := t2.Add(60 * time.Second)
	putRecord(t, s, 1, "recv", "group", 123456, 10001, "第一条", t1.Unix())
	putRecord(t, s, 2, "recv", "group", 123456, 10001, "会被撤回", t2.Unix())
	putRecord(t, s, 3, "recv", "group", 123456, 10001, "第三条", t3.Unix())
	if err := s.MarkRecalled(2); err != nil {
		t.Fatalf("MarkRecalled: %v", err)
	}

	msgs := b.Build(&Turn{ChatType: "group", ChatID: 123456, SelfID: 9527, Now: t3.Add(time.Minute)})

	assertMessages(t, msgs, []ChatMessage{
		{Role: "system", Content: "你是QQ上的AI成员。\n当前账号QQ: 9527"},
		{Role: "user", Content: "[" + t1.Format("01-02 15:04") + "] <10001> 第一条"},
		{Role: "user", Content: "[" + t3.Format("01-02 15:04") + "] <10001> 第三条"},
	})
}

// TestBuildWithNilStoreReturnsOnlySystem storage 未启用（store 为 nil）时
// 历史为空，仅 system 也能对话。
func TestBuildWithNilStoreReturnsOnlySystem(t *testing.T) {
	logs := captureLogs(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		History:     config.AgentHistory{Max: 40, Trim: 20},
	}
	b := NewDefaultBuilder(nil, cfg)

	msgs := b.Build(&Turn{ChatType: "private", ChatID: 10001, SelfID: 9527, Now: time.Unix(1762400000, 0)})

	assertMessages(t, msgs, []ChatMessage{
		{Role: "system", Content: "你是QQ上的AI成员。\n当前账号QQ: 9527"},
	})
	if strings.Contains(logs.String(), "ERROR") {
		t.Errorf("store 为 nil 不应产生错误日志: %s", logs.String())
	}
}

// TestBuildTrimsHistoryInBlocks 历史超过 history.max 时按 history.trim
// 从头整块砍掉（Max=4/Trim=2，6 条 → 保留最近 4 条 m3..m6）。
func TestBuildTrimsHistoryInBlocks(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		History:     config.AgentHistory{Max: 4, Trim: 2},
	}
	b := NewDefaultBuilder(s, cfg)

	base := time.Unix(1762400000, 0)
	for i := 1; i <= 6; i++ {
		direction, userID := "recv", int64(10001)
		if i%2 == 0 {
			direction, userID = "sent", int64(9527)
		}
		putRecord(t, s, int64(i), direction, "group", 123456, userID,
			fmt.Sprintf("m%d", i), base.Add(time.Duration(i)*time.Second).Unix())
	}

	msgs := b.Build(&Turn{ChatType: "group", ChatID: 123456, SelfID: 9527, Now: base.Add(7 * time.Second)})

	if len(msgs) != 5 {
		t.Fatalf("裁剪后消息条数 = %d, want 5 (system+4): %+v", len(msgs), msgs)
	}
	assertMessages(t, msgs, []ChatMessage{
		{Role: "system", Content: "你是QQ上的AI成员。\n当前账号QQ: 9527"},
		{Role: "user", Content: "[" + base.Add(3*time.Second).Format("01-02 15:04") + "] <10001> m3"},
		{Role: "assistant", Content: "[" + base.Add(4*time.Second).Format("01-02 15:04") + "] <我> m4"},
		{Role: "user", Content: "[" + base.Add(5*time.Second).Format("01-02 15:04") + "] <10001> m5"},
		{Role: "assistant", Content: "[" + base.Add(6*time.Second).Format("01-02 15:04") + "] <我> m6"},
	})
}

// TestBuildHistoryPrefixStableAcrossCalls 回归：41→42 条时按块跳删保证
// 非 system 部分前缀字节级一致（prompt cache 前缀不失效）。
func TestBuildHistoryPrefixStableAcrossCalls(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		History:     config.AgentHistory{Max: 40, Trim: 20},
	}
	b := NewDefaultBuilder(s, cfg)

	base := time.Unix(1762400000, 0)
	for i := 1; i <= 41; i++ {
		putRecord(t, s, int64(i), "recv", "group", 123456, 10001,
			fmt.Sprintf("m%02d", i), base.Add(time.Duration(i)*time.Second).Unix())
	}

	first := b.Build(&Turn{ChatType: "group", ChatID: 123456, SelfID: 9527, Now: base.Add(time.Minute)})
	if len(first) != 22 {
		t.Fatalf("41 条历史裁剪后应剩 21 条 (共 22), got %d", len(first))
	}

	putRecord(t, s, 42, "recv", "group", 123456, 10001, "m42", base.Add(42*time.Second).Unix())
	second := b.Build(&Turn{ChatType: "group", ChatID: 123456, SelfID: 9527, Now: base.Add(time.Minute)})

	if len(second) != 23 {
		t.Fatalf("42 条历史裁剪后应剩 22 条 (共 23), got %d", len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("messages[%d] 前缀被破坏:\nfirst:  %+v\nsecond: %+v", i, first[i], second[i])
		}
	}
	if second[22].Content != "["+base.Add(42*time.Second).Format("01-02 15:04")+"] <10001> m42" {
		t.Errorf("新消息应原样追加在末尾: %+v", second[22])
	}
}

// TestBuildAppendsNewMessagesWithTimeHeader 本轮新消息以 user 角色逐条追加在
// 末尾，首条头部带当前时间（2006-01-02 15:04），格式与历史一致（用QQ号不用昵称）。
func TestBuildAppendsNewMessagesWithTimeHeader(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		History:     config.AgentHistory{Max: 40, Trim: 20},
	}
	b := NewDefaultBuilder(s, cfg)

	t1 := time.Unix(1762400000, 0)
	t2 := t1.Add(60 * time.Second)
	putRecord(t, s, 1, "recv", "group", 123456, 10001, "早", t1.Unix())
	putRecord(t, s, 2, "sent", "group", 123456, 9527, "早呀", t2.Unix())

	now := t2.Add(5 * time.Minute)
	m1t := now.Add(-30 * time.Second)
	m2t := now.Add(-10 * time.Second)
	msgs := b.Build(&Turn{
		ChatType: "group",
		ChatID:   123456,
		SelfID:   9527,
		Now:      now,
		NewMessages: []TurnMessage{
			{SenderID: 10001, SenderName: "小明", Text: "在吗", Time: m1t},
			{SenderID: 10003, SenderName: "小红", Text: "来打游戏", Time: m2t},
		},
	})

	assertMessages(t, msgs, []ChatMessage{
		{Role: "system", Content: "你是QQ上的AI成员。\n当前账号QQ: 9527"},
		{Role: "user", Content: "[" + t1.Format("01-02 15:04") + "] <10001> 早"},
		{Role: "assistant", Content: "[" + t2.Format("01-02 15:04") + "] <我> 早呀"},
		{Role: "user", Content: "当前时间: " + now.Format("2006-01-02 15:04") + "\n[" + m1t.Format("01-02 15:04") + "] <10001> 在吗"},
		{Role: "user", Content: "[" + m2t.Format("01-02 15:04") + "] <10003> 来打游戏"},
	})
}

// TestBuildSystemStaticAcrossNow 两次不同 Now 的 Build 产出 system 完全相同
// （时间只进末尾新消息，system 是纯静态缓存锚点）。
func TestBuildSystemStaticAcrossNow(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		History:     config.AgentHistory{Max: 40, Trim: 20},
	}
	b := NewDefaultBuilder(s, cfg)

	base := time.Unix(1762400000, 0)
	putRecord(t, s, 1, "recv", "group", 123456, 10001, "早", base.Unix())

	turn := func(now time.Time) *Turn {
		return &Turn{
			ChatType: "group",
			ChatID:   123456,
			SelfID:   9527,
			Now:      now,
			NewMessages: []TurnMessage{
				{SenderID: 10001, Text: "在吗", Time: now},
			},
		}
	}

	first := b.Build(turn(base))
	second := b.Build(turn(base.Add(time.Hour)))

	if first[0] != second[0] {
		t.Fatalf("system 应与 Now 无关:\nfirst:  %+v\nsecond: %+v", first[0], second[0])
	}
	if strings.Contains(first[0].Content, "2026") || strings.Contains(first[0].Content, "当前时间") {
		t.Errorf("system 不应携带时间: %q", first[0].Content)
	}
	if !strings.Contains(first[len(first)-1].Content, "当前时间: "+base.Format("2006-01-02 15:04")) {
		t.Errorf("当前时间应出现在末尾新消息中: %+v", first[len(first)-1])
	}
}

// TestBuildTruncatesLongContent 单条内容超过 20000 字符截断并加 "...(截断)" 标注，
// 历史与本轮新消息同样处理。
func TestBuildTruncatesLongContent(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		History:     config.AgentHistory{Max: 40, Trim: 20},
	}
	b := NewDefaultBuilder(s, cfg)

	base := time.Unix(1762400000, 0)
	long := strings.Repeat("长", 25000)
	putRecord(t, s, 1, "recv", "group", 123456, 10001, long, base.Unix())

	now := base.Add(time.Minute)
	msgs := b.Build(&Turn{
		ChatType: "group",
		ChatID:   123456,
		SelfID:   9527,
		Now:      now,
		NewMessages: []TurnMessage{
			{SenderID: 10001, Text: strings.Repeat("新", 21000), Time: now},
		},
	})

	wantTail := strings.Repeat("长", 20000) + "...(截断)"
	wantHistory := "[" + base.Format("01-02 15:04") + "] <10001> " + wantTail
	if msgs[1].Content != wantHistory {
		t.Errorf("超长历史未按 20000 字符截断: rune 数 = %d", len([]rune(msgs[1].Content)))
	}
	wantNew := "当前时间: " + now.Format("2006-01-02 15:04") + "\n[" +
		now.Format("01-02 15:04") + "] <10001> " + strings.Repeat("新", 20000) + "...(截断)"
	if msgs[2].Content != wantNew {
		t.Errorf("超长新消息未按 20000 字符截断: rune 数 = %d", len([]rune(msgs[2].Content)))
	}
}
