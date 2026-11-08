package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

// writeGuideFile 写一份 agent 格式说明测试文件，返回路径。
func writeGuideFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "guide.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 guide 文件失败: %v", err)
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
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
	}
	b := NewDefaultBuilder(s, cfg)

	msgs := b.Build(&Turn{
		ChatType: "group",
		ChatID:   928952016,
		SelfID:   9527,
		Now:      time.Unix(1762400000, 0),
	})

	if len(msgs) != 1 {
		t.Fatalf("Build 返回 %d 条消息, want 1 (仅 system): %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" {
		t.Errorf("messages[0].Role = %q, want %q", msgs[0].Role, "system")
	}
	want := "你是QQ上的AI成员。\n" + agentGuideText + "\n当前账号QQ: 9527"
	if msgs[0].Content != want {
		t.Errorf("system content = %q, want %q", msgs[0].Content, want)
	}
}

// TestBuildSystemContainsMoodAndAgentGuide system 拼上静态 agent 格式说明段
// 与当前情绪状态行（spec §2.2）；无 mood 时不带情绪行，mood 只追加在尾部
// （缓存前缀稳定）。
func TestBuildMoodInTailNotSystem(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
	}
	b := NewDefaultBuilder(s, cfg)
	now := time.Unix(1762400000, 0)

	withMood := b.Build(&Turn{ChatType: "group", ChatID: 888, SelfID: 9527, Now: now, Mood: "有点开心"})
	sys := contentText(withMood[0])
	if strings.Contains(sys, "当前情绪状态") {
		t.Errorf("情绪不应进 system（会击穿缓存前缀）: %q", sys)
	}
	for _, keyword := range []string{"send_message", "finish", "help", "think", "update_mood"} {
		if !strings.Contains(sys, keyword) {
			t.Errorf("agent 格式说明未提及工具 %s: %q", keyword, sys)
		}
	}
	// 无新消息时 mood 无处安放——带一条新消息验证 tail 注入
	withMood = b.Build(&Turn{
		ChatType: "group", ChatID: 888, SelfID: 9527, Now: now, Mood: "有点开心",
		NewMessages: []TurnMessage{
			{ChatType: "group", ChatID: 888, SenderID: 10001, SenderName: "小明", Text: "在吗", Time: now.Add(-time.Second)},
		},
	})
	first := contentText(withMood[len(withMood)-1])
	want := "当前情绪状态: 有点开心\n当前时间: " + now.Format("2006-01-02 15:04") + "\n"
	if !strings.Contains(first, want) {
		t.Errorf("情绪行应注入首条新消息头部（时间之前）:\ngot:  %q\nwant contains: %q", first, want)
	}

	withoutMood := b.Build(&Turn{ChatType: "group", ChatID: 888, SelfID: 9527, Now: now})
	// system 与带 mood 时逐字节一致：mood 变化不影响缓存前缀
	if contentText(withoutMood[0]) != contentText(withMood[0]) {
		t.Errorf("system 应与 mood 无关:\nwith:    %q\nwithout: %q", withMood[0].Content, withoutMood[0].Content)
	}
	if strings.Contains(contentText(withoutMood[len(withoutMood)-1]), "当前情绪状态") {
		t.Errorf("无 mood 时 tail 不应有情绪行: %q", withoutMood[len(withoutMood)-1].Content)
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
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
	}

	b := NewDefaultBuilder(s, cfg)
	msgs := b.Build(&Turn{ChatType: "private", ChatID: 10001, SelfID: 9527, Now: time.Unix(1762400000, 0)})

	if len(msgs) != 1 || msgs[0].Role != "system" {
		t.Fatalf("缺 persona 时 Build 应仅返回 system, got %+v", msgs)
	}
	if !strings.Contains(contentText(msgs[0]), "你是一个QQ群聊/私聊里的AI成员") {
		t.Errorf("兜底提示词缺失: %q", msgs[0].Content)
	}
	if !strings.Contains(contentText(msgs[0]), "当前账号QQ: 9527") {
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

// TestNewDefaultBuilderMissingGuideFallsBack guide 文件缺失时告警并使用内置
// 兜底格式说明。
func TestNewDefaultBuilderMissingGuideFallsBack(t *testing.T) {
	s := openTestStore(t)
	logs := captureLogs(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   filepath.Join(t.TempDir(), "absent-guide.md"),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
	}

	b := NewDefaultBuilder(s, cfg)
	msgs := b.Build(&Turn{ChatType: "private", ChatID: 10001, SelfID: 9527, Now: time.Unix(1762400000, 0)})

	if !strings.Contains(contentText(msgs[0]), agentGuideText) {
		t.Errorf("guide 缺失应回退内置文本: %q", msgs[0].Content)
	}
	if !strings.Contains(logs.String(), "absent-guide.md") {
		t.Errorf("guide 缺失应告警并包含路径: %s", logs.String())
	}
}

// TestBuilderUsesGuideFileContent guide 文件内容应原样进 system（可编辑提示词）。
func TestBuilderUsesGuideFileContent(t *testing.T) {
	s := openTestStore(t)
	const customGuide = "[自定义格式说明]\n始终用喵语气说话。"
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, customGuide),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
	}

	b := NewDefaultBuilder(s, cfg)
	msgs := b.Build(&Turn{ChatType: "private", ChatID: 10001, SelfID: 9527, Now: time.Unix(1762400000, 0)})

	if !strings.Contains(contentText(msgs[0]), customGuide) {
		t.Errorf("guide 文件内容未进 system: %q", msgs[0].Content)
	}
}

// putRecord 写入一条消息记录。
func putRecord(t *testing.T, s *storage.Store, id int64, direction, chatType string, chatID, userID int64, senderName, raw string, sec int64) {
	t.Helper()
	err := s.PutMessage(&storage.MessageRecord{
		MessageID:  id,
		Direction:  direction,
		ChatType:   chatType,
		ChatID:     chatID,
		UserID:     userID,
		SenderName: senderName,
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
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("messages[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// contentText 取消息内容的字符串形态；content 为多模态数组时返回空串
// （仅用于断言纯文本行的测试）。
func contentText(m ChatMessage) string {
	s, _ := m.Content.(string)
	return s
}

// TestBuildHybridHistoryFormatAndRoles 历史跨会话混合、按时间正序，行格式
// "[MM-DD HH:MM] [群:ID 或 私聊:ID] <昵称(QQ号)/QQ号/'我'> 内容"；
// sent 记 own 为 assistant 且标签"我"；昵称空回退 QQ号；CQ 码原样透传。
func TestBuildHybridHistoryFormatAndRoles(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
	}
	b := NewDefaultBuilder(s, cfg)

	t1 := time.Unix(1762400000, 0)
	t2 := t1.Add(60 * time.Second)
	t3 := t2.Add(60 * time.Second)
	t4 := t3.Add(60 * time.Second)
	t5 := t4.Add(60 * time.Second)
	putRecord(t, s, 1, "recv", "group", 928952016, 3929930584, "香菜不吃屿烬燃", "这个我不背锅！！！！", t1.Unix())
	putRecord(t, s, 2, "recv", "private", 3394859101, 3394859101, "小奶猫雪溪", "在吗", t2.Unix())
	putRecord(t, s, 3, "sent", "group", 928952016, 9527, "我", "收到", t3.Unix())
	putRecord(t, s, 4, "recv", "group", 928952016, 10086, "", "旧库没存昵称", t4.Unix())
	putRecord(t, s, 5, "recv", "group", 928952016, 10002, "路人", "[CQ:face,id=1]哈哈", t5.Unix())

	msgs := b.Build(&Turn{ChatType: "group", ChatID: 928952016, SelfID: 9527, Now: t5.Add(time.Minute)})

	assertMessages(t, msgs, []ChatMessage{
		{Role: "system", Content: "你是QQ上的AI成员。\n" + agentGuideText + "\n当前账号QQ: 9527"},
		{Role: "user", Content: "[" + t1.Format("01-02 15:04") + "] [群:928952016] <香菜不吃屿烬燃(3929930584)> 这个我不背锅！！！！"},
		{Role: "user", Content: "[" + t2.Format("01-02 15:04") + "] [私聊:3394859101] <小奶猫雪溪(3394859101)> 在吗"},
		{Role: "assistant", Content: "[" + t3.Format("01-02 15:04") + "] [群:928952016] <我> 收到"},
		{Role: "user", Content: "[" + t4.Format("01-02 15:04") + "] [群:928952016] <10086> 旧库没存昵称"},
		{Role: "user", Content: "[" + t5.Format("01-02 15:04") + "] [群:928952016] <路人(10002)> [CQ:face,id=1]哈哈"},
	})
}

// TestBuildHistoryExcludesRecalled 默认排除已撤回消息。
func TestBuildHistoryExcludesRecalled(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
	}
	b := NewDefaultBuilder(s, cfg)

	t1 := time.Unix(1762400000, 0)
	t2 := t1.Add(60 * time.Second)
	t3 := t2.Add(60 * time.Second)
	putRecord(t, s, 1, "recv", "group", 928952016, 10001, "群友A", "第一条", t1.Unix())
	putRecord(t, s, 2, "recv", "group", 928952016, 10001, "群友A", "会被撤回", t2.Unix())
	putRecord(t, s, 3, "recv", "group", 928952016, 10001, "群友A", "第三条", t3.Unix())
	if err := s.MarkRecalled(2); err != nil {
		t.Fatalf("MarkRecalled: %v", err)
	}

	msgs := b.Build(&Turn{ChatType: "group", ChatID: 928952016, SelfID: 9527, Now: t3.Add(time.Minute)})

	assertMessages(t, msgs, []ChatMessage{
		{Role: "system", Content: "你是QQ上的AI成员。\n" + agentGuideText + "\n当前账号QQ: 9527"},
		{Role: "user", Content: "[" + t1.Format("01-02 15:04") + "] [群:928952016] <群友A(10001)> 第一条"},
		{Role: "user", Content: "[" + t3.Format("01-02 15:04") + "] [群:928952016] <群友A(10001)> 第三条"},
	})
}

// TestBuildWithNilStoreReturnsOnlySystem storage 未启用（store 为 nil）时
// 历史为空，仅 system 也能对话。
func TestBuildWithNilStoreReturnsOnlySystem(t *testing.T) {
	logs := captureLogs(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
	}
	b := NewDefaultBuilder(nil, cfg)

	msgs := b.Build(&Turn{ChatType: "private", ChatID: 10001, SelfID: 9527, Now: time.Unix(1762400000, 0)})

	assertMessages(t, msgs, []ChatMessage{
		{Role: "system", Content: "你是QQ上的AI成员。\n" + agentGuideText + "\n当前账号QQ: 9527"},
	})
	if strings.Contains(logs.String(), "ERROR") {
		t.Errorf("store 为 nil 不应产生错误日志: %s", logs.String())
	}
}

// TestBuildTrimsHistoryAcrossChatsInBlocks 全局历史超过 history.max 时按
// history.trim 从头整块砍掉（Max=4/Trim=2，群+私聊混合 6 条 → 保留最近 4 条 m3..m6）。
func TestBuildTrimsHistoryAcrossChatsInBlocks(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 4, Trim: 2},
	}
	b := NewDefaultBuilder(s, cfg)

	base := time.Unix(1762400000, 0)
	for i := 1; i <= 6; i++ {
		direction, userID := "recv", int64(20001)
		chatType, chatID, name := "private", int64(20001), "私聊B"
		if i%2 == 0 {
			direction, userID = "sent", int64(9527)
			chatType, chatID, name = "group", int64(928952016), "我"
		}
		putRecord(t, s, int64(i), direction, chatType, chatID, userID, name,
			fmt.Sprintf("m%d", i), base.Add(time.Duration(i)*time.Second).Unix())
	}

	msgs := b.Build(&Turn{ChatType: "group", ChatID: 928952016, SelfID: 9527, Now: base.Add(7 * time.Second)})

	if len(msgs) != 5 {
		t.Fatalf("裁剪后消息条数 = %d, want 5 (system+4): %+v", len(msgs), msgs)
	}
	assertMessages(t, msgs, []ChatMessage{
		{Role: "system", Content: "你是QQ上的AI成员。\n" + agentGuideText + "\n当前账号QQ: 9527"},
		{Role: "user", Content: "[" + base.Add(3*time.Second).Format("01-02 15:04") + "] [私聊:20001] <私聊B(20001)> m3"},
		{Role: "assistant", Content: "[" + base.Add(4*time.Second).Format("01-02 15:04") + "] [群:928952016] <我> m4"},
		{Role: "user", Content: "[" + base.Add(5*time.Second).Format("01-02 15:04") + "] [私聊:20001] <私聊B(20001)> m5"},
		{Role: "assistant", Content: "[" + base.Add(6*time.Second).Format("01-02 15:04") + "] [群:928952016] <我> m6"},
	})
}

// TestBuildHistoryPrefixStableAcrossCalls 回归：群+私聊混合 41→42 条时按块
// 跳删保证非 system 部分前缀字节级一致（prompt cache 前缀不失效）。
func TestBuildHistoryPrefixStableAcrossCalls(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
	}
	b := NewDefaultBuilder(s, cfg)

	base := time.Unix(1762400000, 0)
	for i := 1; i <= 41; i++ {
		chatType, chatID, userID, name := "group", int64(928952016), int64(10001), "群友A"
		if i%2 == 1 {
			chatType, chatID, userID, name = "private", int64(20001), int64(20001), "私聊B"
		}
		putRecord(t, s, int64(i), "recv", chatType, chatID, userID, name,
			fmt.Sprintf("m%02d", i), base.Add(time.Duration(i)*time.Second).Unix())
	}

	first := b.Build(&Turn{ChatType: "group", ChatID: 928952016, SelfID: 9527, Now: base.Add(time.Minute)})
	if len(first) != 22 {
		t.Fatalf("41 条历史裁剪后应剩 21 条 (共 22), got %d", len(first))
	}

	putRecord(t, s, 42, "recv", "group", 928952016, 10001, "群友A", "m42", base.Add(42*time.Second).Unix())
	second := b.Build(&Turn{ChatType: "group", ChatID: 928952016, SelfID: 9527, Now: base.Add(time.Minute)})

	if len(second) != 23 {
		t.Fatalf("42 条历史裁剪后应剩 22 条 (共 23), got %d", len(second))
	}
	for i := range first {
		if !reflect.DeepEqual(first[i], second[i]) {
			t.Fatalf("messages[%d] 前缀被破坏:\nfirst:  %+v\nsecond: %+v", i, first[i], second[i])
		}
	}
	if second[22].Content != "["+base.Add(42*time.Second).Format("01-02 15:04")+"] [群:928952016] <群友A(10001)> m42" {
		t.Errorf("新历史应原样追加在末尾: %+v", second[22])
	}
}

// TestBuildHistoryPrefixStableBeyondQueryCap 回归：库中消息超过 max+trim（查询
// 被 Limit 截断）后，窗口仍以时间水印为锚整块推进——未裁剪时历史 = 上一轮
// 历史原样追加一条；裁剪时 = 上一轮历史砍掉头部 trim 块再追加。相对条数裁剪
// 在此区间会退化为逐条滑窗，每条新消息换头击穿缓存。
func TestBuildHistoryPrefixStableBeyondQueryCap(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
	}
	b := NewDefaultBuilder(s, cfg)

	base := time.Unix(1762400000, 0)
	prev := b.Build(&Turn{ChatType: "group", ChatID: 928952016, SelfID: 9527, Now: base})
	evictions := 0
	for i := 1; i <= 100; i++ {
		putRecord(t, s, int64(i), "recv", "group", 928952016, 10001, "群友A",
			fmt.Sprintf("m%03d", i), base.Add(time.Duration(i)*time.Second).Unix())
		cur := b.Build(&Turn{ChatType: "group", ChatID: 928952016, SelfID: 9527, Now: base.Add(time.Duration(i) * time.Second)})
		histPrev, histCur := prev[1:], cur[1:] // 去 system
		switch {
		case len(histCur) == len(histPrev)+1: // 未裁剪：原样追加
			for j := range histPrev {
				if !reflect.DeepEqual(histPrev[j], histCur[j]) {
					t.Fatalf("i=%d 未裁剪轮历史前缀被破坏 at %d:\nprev: %+v\ncur:  %+v", i, j, histPrev[j], histCur[j])
				}
			}
		case len(histCur) == len(histPrev)-b.trim+1: // 裁剪：砍头部 trim 块再追加
			evictions++
			for j := range histCur[:len(histCur)-1] {
				if !reflect.DeepEqual(histPrev[b.trim+j], histCur[j]) {
					t.Fatalf("i=%d 裁剪轮保留段与上轮尾部不对齐 at %d:\nprev: %+v\ncur:  %+v", i, b.trim+j, histPrev[b.trim+j], histCur[j])
				}
			}
		default:
			t.Fatalf("i=%d 历史条数异常: prev=%d cur=%d", i, len(histPrev), len(histCur))
		}
		prev = cur
	}
	if evictions == 0 {
		t.Fatal("100 条消息应发生至少一次块裁剪")
	}
}

// TestBuildAppendsNewMessagesWithTimeHeader 本轮新消息以 user 角色逐条追加在
// 末尾，行格式与历史一致（含来源标签，昵称空回退 QQ号）；首条头部带当前时间
// （2006-01-02 15:04），时间只出现一次。
func TestBuildAppendsNewMessagesWithTimeHeader(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
	}
	b := NewDefaultBuilder(s, cfg)

	t1 := time.Unix(1762400000, 0)
	t2 := t1.Add(60 * time.Second)
	putRecord(t, s, 1, "recv", "group", 928952016, 10001, "群友A", "早", t1.Unix())
	putRecord(t, s, 2, "sent", "group", 928952016, 9527, "我", "早呀", t2.Unix())

	now := t2.Add(5 * time.Minute)
	m1t := now.Add(-30 * time.Second)
	m2t := now.Add(-10 * time.Second)
	msgs := b.Build(&Turn{
		ChatType: "group",
		ChatID:   928952016,
		SelfID:   9527,
		Now:      now,
		NewMessages: []TurnMessage{
			{ChatType: "group", ChatID: 928952016, SenderID: 10001, SenderName: "小明", Text: "在吗", Time: m1t},
			{ChatType: "group", ChatID: 928952016, SenderID: 10003, SenderName: "", Text: "来打游戏", Time: m2t},
		},
	})

	assertMessages(t, msgs, []ChatMessage{
		{Role: "system", Content: "你是QQ上的AI成员。\n" + agentGuideText + "\n当前账号QQ: 9527"},
		{Role: "user", Content: "[" + t1.Format("01-02 15:04") + "] [群:928952016] <群友A(10001)> 早"},
		{Role: "assistant", Content: "[" + t2.Format("01-02 15:04") + "] [群:928952016] <我> 早呀"},
		{Role: "user", Content: "当前时间: " + now.Format("2006-01-02 15:04") + "\n[" + m1t.Format("01-02 15:04") + "] [群:928952016] <小明(10001)> 在吗"},
		{Role: "user", Content: "[" + m2t.Format("01-02 15:04") + "] [群:928952016] <10003> 来打游戏"},
	})
}

// TestBuildInjectsNotesBeforeFirstNewMessage 思考笔记以 "[思考笔记]" 块注入
// 最终 user 消息块头部（首条新消息内容前缀，位于当前时间之前）；笔记不进
// system；无笔记时不注入。
func TestBuildInjectsNotesBeforeFirstNewMessage(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
	}
	b := NewDefaultBuilder(s, cfg)

	now := time.Unix(1762400000, 0)
	noteT := now.Add(-10 * time.Minute)
	newT := now.Add(-30 * time.Second)
	withNotes := b.Build(&Turn{
		ChatType: "group",
		ChatID:   928952016,
		SelfID:   9527,
		Now:      now,
		Notes: []Note{
			{Time: noteT, Text: "上轮决定先查群名片"},
			{Time: noteT.Add(time.Minute), Text: "他在开玩笑，别当真"},
		},
		NewMessages: []TurnMessage{
			{ChatType: "group", ChatID: 928952016, SenderID: 10001, SenderName: "小明", Text: "在吗", Time: newT},
			{ChatType: "group", ChatID: 928952016, SenderID: 10003, SenderName: "小红", Text: "来打游戏", Time: now.Add(-10 * time.Second)},
		},
	})

	if len(withNotes) != 3 {
		t.Fatalf("无历史时有笔记应返回 system+2 条新消息, got %d: %+v", len(withNotes), withNotes)
	}
	wantFirst := "[思考笔记]\n[" + noteT.Format("01-02 15:04") + "] 上轮决定先查群名片\n[" +
		noteT.Add(time.Minute).Format("01-02 15:04") + "] 他在开玩笑，别当真\n\n" +
		"当前时间: " + now.Format("2006-01-02 15:04") + "\n[" +
		newT.Format("01-02 15:04") + "] [群:928952016] <小明(10001)> 在吗"
	if withNotes[1].Content != wantFirst {
		t.Errorf("首条新消息未带笔记前缀:\ngot:  %q\nwant: %q", withNotes[1].Content, wantFirst)
	}
	if strings.Contains(contentText(withNotes[2]), "[思考笔记]") {
		t.Errorf("笔记只应出现在首条新消息前: %q", withNotes[2].Content)
	}
	if strings.Contains(contentText(withNotes[0]), "[思考笔记]") {
		t.Errorf("笔记不应进 system: %q", withNotes[0].Content)
	}
	withoutNotes := b.Build(&Turn{
		ChatType: "group",
		ChatID:   928952016,
		SelfID:   9527,
		Now:      now,
		NewMessages: []TurnMessage{
			{ChatType: "group", ChatID: 928952016, SenderID: 10001, SenderName: "小明", Text: "在吗", Time: newT},
		},
	})
	if strings.Contains(contentText(withoutNotes[len(withoutNotes)-1]), "[思考笔记]") {
		t.Errorf("无笔记时不应注入: %q", withoutNotes[len(withoutNotes)-1].Content)
	}
	if !strings.HasPrefix(contentText(withoutNotes[len(withoutNotes)-1]), "当前时间: ") {
		t.Errorf("无笔记时首条新消息应以当前时间开头: %q", withoutNotes[len(withoutNotes)-1].Content)
	}
}

// TestBuildSystemStaticAcrossNow 两次不同 Now 的 Build 产出 system 完全相同
// （时间与笔记只进末尾新消息，system 是纯静态缓存锚点）。
func TestBuildSystemStaticAcrossNow(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
	}
	b := NewDefaultBuilder(s, cfg)

	base := time.Unix(1762400000, 0)
	putRecord(t, s, 1, "recv", "group", 928952016, 10001, "群友A", "早", base.Unix())

	turn := func(now time.Time) *Turn {
		return &Turn{
			ChatType: "group",
			ChatID:   928952016,
			SelfID:   9527,
			Now:      now,
			NewMessages: []TurnMessage{
				{ChatType: "group", ChatID: 928952016, SenderID: 10001, Text: "在吗", Time: now},
			},
		}
	}

	first := b.Build(turn(base))
	second := b.Build(turn(base.Add(time.Hour)))

	if !reflect.DeepEqual(first[0], second[0]) {
		t.Fatalf("system 应与 Now 无关:\nfirst:  %+v\nsecond: %+v", first[0], second[0])
	}
	if strings.Contains(contentText(first[0]), "2026") || strings.Contains(contentText(first[0]), "当前时间") {
		t.Errorf("system 不应携带时间: %q", first[0].Content)
	}
	if !strings.Contains(contentText(first[len(first)-1]), "当前时间: "+base.Format("2006-01-02 15:04")) {
		t.Errorf("当前时间应出现在末尾新消息中: %+v", first[len(first)-1])
	}
}

// TestBuildTruncatesLongContent 单条内容超过 20000 字符截断并加 "...(截断)" 标注，
// 历史与本轮新消息同样处理。
func TestBuildTruncatesLongContent(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
	}
	b := NewDefaultBuilder(s, cfg)

	base := time.Unix(1762400000, 0)
	long := strings.Repeat("长", 25000)
	putRecord(t, s, 1, "recv", "group", 928952016, 10001, "", long, base.Unix())

	now := base.Add(time.Minute)
	msgs := b.Build(&Turn{
		ChatType: "group",
		ChatID:   928952016,
		SelfID:   9527,
		Now:      now,
		NewMessages: []TurnMessage{
			{ChatType: "group", ChatID: 928952016, SenderID: 10001, Text: strings.Repeat("新", 21000), Time: now},
		},
	})

	wantTail := strings.Repeat("长", 20000) + "...(截断)"
	wantHistory := "[" + base.Format("01-02 15:04") + "] [群:928952016] <10001> " + wantTail
	if contentText(msgs[1]) != wantHistory {
		t.Errorf("超长历史未按 20000 字符截断: rune 数 = %d", len([]rune(contentText(msgs[1]))))
	}
	wantNew := "当前时间: " + now.Format("2006-01-02 15:04") + "\n[" +
		now.Format("01-02 15:04") + "] [群:928952016] <10001> " + strings.Repeat("新", 20000) + "...(截断)"
	if contentText(msgs[2]) != wantNew {
		t.Errorf("超长新消息未按 20000 字符截断: rune 数 = %d", len([]rune(contentText(msgs[2]))))
	}
}

// ---------- P2.7 多模态上下文 ----------

// TestExtractImageRefs 从消息段 JSON 提取图片段（Idx 对全部 image 段计数，
// 含缺 url 的段，与 storage 映射锚一致）；非 image 段跳过。
func TestExtractImageRefs(t *testing.T) {
	segs := `[
	 {"type":"text","data":{"text":"看图"}},
	 {"type":"image","data":{"file":"a.image","url":"https://multimedia.nt.qq.com.cn/1","sub_type":0}},
	 {"type":"image","data":{"file":"b.image","url":""}},
	 {"type":"image","data":{"file":"c.image"}},
	 {"type":"face","data":{"id":"1","url":"https://face.example/1"}},
	 {"type":"image","data":{"file":"d.image","url":"https://multimedia.nt.qq.com.cn/2"}}
	]`
	got := extractImageRefs([]byte(segs))
	want := []imageRef{
		{Idx: 0, URL: "https://multimedia.nt.qq.com.cn/1"},
		{Idx: 1, URL: ""},
		{Idx: 2, URL: ""},
		{Idx: 3, URL: "https://multimedia.nt.qq.com.cn/2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractImageRefs = %v, want %v", got, want)
	}
}

// TestExtractImageRefsCapsAtFour 最多提取 4 个图片段。
func TestExtractImageRefsCapsAtFour(t *testing.T) {
	segs := `[` +
		`{"type":"image","data":{"url":"https://img.example/1"}},` +
		`{"type":"image","data":{"url":"https://img.example/2"}},` +
		`{"type":"image","data":{"url":"https://img.example/3"}},` +
		`{"type":"image","data":{"url":"https://img.example/4"}},` +
		`{"type":"image","data":{"url":"https://img.example/5"}}]`
	got := extractImageRefs([]byte(segs))
	want := []imageRef{
		{Idx: 0, URL: "https://img.example/1"}, {Idx: 1, URL: "https://img.example/2"},
		{Idx: 2, URL: "https://img.example/3"}, {Idx: 3, URL: "https://img.example/4"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractImageRefs = %v, want %v", got, want)
	}
}

// TestExtractImageRefsInvalidInput 空输入与非法 JSON 返回 nil。
func TestExtractImageRefsInvalidInput(t *testing.T) {
	for _, in := range [][]byte{nil, {}, []byte("not json"), []byte(`{"type":"image"}`)} {
		if got := extractImageRefs(in); got != nil {
			t.Errorf("extractImageRefs(%s) = %v, want nil", in, got)
		}
	}
}

// putRecordFull 写入一条带 segments 的消息记录。
func putRecordFull(t *testing.T, s *storage.Store, r *storage.MessageRecord) {
	t.Helper()
	if err := s.PutMessage(r); err != nil {
		t.Fatalf("PutMessage(%d): %v", r.MessageID, err)
	}
}

// TestBuildHistoryImageRowBecomesMultimodal 含图片 url 的历史行 content 变为
// 多模态数组：text 部分（CQ:image 码替换为 [图片]）+ 图片部分经 imageCache
// 本地下载转为 base64 data URL（P3-3）；相邻无图行保持 string（wire 字节与
// 旧行为一致，保缓存前缀）。
func TestBuildHistoryImageRowBecomesMultimodal(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
		Multimodal:  boolPtr(true),
	}
	b := NewDefaultBuilder(s, cfg)

	imgSrv, _ := servePNG(t, "image/png")
	imgURL := imgSrv.URL + "/a.png"

	t1 := time.Unix(1762400000, 0)
	t2 := t1.Add(60 * time.Second)
	putRecordFull(t, s, &storage.MessageRecord{
		MessageID: 1, Direction: "recv", ChatType: "group", ChatID: 928952016,
		UserID: 10001, SenderName: "小明",
		RawMessage: "[CQ:image,file=a.image,subType=0,url=" + imgURL + "] 看这张图",
		Segments:   []byte(`[{"type":"image","data":{"file":"a.image","url":"` + imgURL + `","sub_type":0}}]`),
		Time:       float64(t1.Unix()),
	})
	putRecord(t, s, 2, "recv", "group", 928952016, 10002, "小红", "纯文本", t2.Unix())

	msgs := b.Build(&Turn{ChatType: "group", ChatID: 928952016, SelfID: 9527, Now: t2.Add(time.Minute)})
	if len(msgs) != 3 {
		t.Fatalf("应返回 system+2 条历史, got %d: %+v", len(msgs), msgs)
	}

	// 带图行 → 多模态数组，图片部分为本地下载的 data URL
	line1 := "[" + t1.Format("01-02 15:04") + "] [群:928952016] <小明(10001)> [图片] 看这张图"
	wantParts := []ContentPart{
		{Type: "text", Text: line1},
		imagePart(pngDataURL),
	}
	gotParts, ok := msgs[1].Content.([]ContentPart)
	if !ok {
		t.Fatalf("带图历史行 content 应为 []ContentPart, got %T: %#v", msgs[1].Content, msgs[1].Content)
	}
	if !reflect.DeepEqual(gotParts, wantParts) {
		t.Errorf("带图行 content =\n%#v\nwant\n%#v", gotParts, wantParts)
	}
	if got := gotParts[1].ImageURL.URL; !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Errorf("image_url 应为 base64 data URL 前缀, got %q", got)
	}
	// wire 字节级校验
	gotJSON, err := json.Marshal(msgs[1])
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(ChatMessage{Role: "user", Content: wantParts})
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("带图行 wire 字节不符:\ngot:  %s\nwant: %s", gotJSON, wantJSON)
	}

	// 无图行 → 仍为 string，字节级与旧行为一致
	line2 := "[" + t2.Format("01-02 15:04") + "] [群:928952016] <小红(10002)> 纯文本"
	if got, ok := msgs[2].Content.(string); !ok || got != line2 {
		t.Fatalf("无图历史行 content 应保持 string %q, got %#v", line2, msgs[2].Content)
	}
	oldJSON, err := json.Marshal(ChatMessage{Role: "user", Content: line2})
	if err != nil {
		t.Fatal(err)
	}
	got2JSON, err := json.Marshal(msgs[2])
	if err != nil {
		t.Fatal(err)
	}
	if string(got2JSON) != string(oldJSON) {
		t.Errorf("无图行 wire 字节被改变:\ngot:  %s\nwant: %s", got2JSON, oldJSON)
	}
}

// TestBuildHistorySentImageRowStaysString 自己发出的带图行（assistant 角色）
// 保持纯文本：OpenAI 兼容 API 不接受 assistant 消息携带 image_url。
func TestBuildHistorySentImageRowStaysString(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
		Multimodal:  boolPtr(true),
	}
	b := NewDefaultBuilder(s, cfg)

	t1 := time.Unix(1762400000, 0)
	const imgURL = "https://multimedia.nt.qq.com.cn/download?appid=1407&fileid=xyz"
	putRecordFull(t, s, &storage.MessageRecord{
		MessageID: 1, Direction: "sent", ChatType: "group", ChatID: 928952016,
		UserID: 9527, SenderName: "我",
		RawMessage: "[CQ:image,file=out.image,url=" + imgURL + "]",
		Segments:   []byte(`[{"type":"image","data":{"file":"out.image","url":"` + imgURL + `"}}]`),
		Time:       float64(t1.Unix()),
	})

	msgs := b.Build(&Turn{ChatType: "group", ChatID: 928952016, SelfID: 9527, Now: t1.Add(time.Minute)})
	if len(msgs) != 2 {
		t.Fatalf("应返回 system+1 条历史, got %d", len(msgs))
	}
	want := "[" + t1.Format("01-02 15:04") + "] [群:928952016] <我> [CQ:image,file=out.image,url=" + imgURL + "]"
	got, ok := msgs[1].Content.(string)
	if !ok || got != want {
		t.Fatalf("assistant 行应保持纯文本 %q, got %#v", want, msgs[1].Content)
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("发出行 role = %q, want assistant", msgs[1].Role)
	}
}

// TestBuildHistoryMultimodalDisabledKeepsRawCQ multimodal=false 时带图历史行
// 仍为纯文本、CQ 码原样透传（不支持图片输入的模型维持原行为）。
func TestBuildHistoryMultimodalDisabledKeepsRawCQ(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
		Multimodal:  boolPtr(false),
	}
	b := NewDefaultBuilder(s, cfg)

	t1 := time.Unix(1762400000, 0)
	const imgURL = "https://multimedia.nt.qq.com.cn/download?appid=1407&fileid=abc"
	putRecordFull(t, s, &storage.MessageRecord{
		MessageID: 1, Direction: "recv", ChatType: "group", ChatID: 928952016,
		UserID: 10001, SenderName: "小明",
		RawMessage: "[CQ:image,file=a.image,subType=0,url=" + imgURL + "] 看这张图",
		Segments:   []byte(`[{"type":"image","data":{"file":"a.image","url":"` + imgURL + `","sub_type":0}}]`),
		Time:       float64(t1.Unix()),
	})

	msgs := b.Build(&Turn{ChatType: "group", ChatID: 928952016, SelfID: 9527, Now: t1.Add(time.Minute)})
	if len(msgs) != 2 {
		t.Fatalf("应返回 system+1 条历史, got %d", len(msgs))
	}
	want := "[" + t1.Format("01-02 15:04") + "] [群:928952016] <小明(10001)> [CQ:image,file=a.image,subType=0,url=" + imgURL + "] 看这张图"
	if got, ok := msgs[1].Content.(string); !ok || got != want {
		t.Fatalf("multimodal=false 时应为纯文本 %q, got %#v", want, msgs[1].Content)
	}
}

// TestBuildNewMessageImagesMultimodal 本轮新消息带 Images 时行内容变为多模态
// 数组（text 部分保留时间头/笔记前缀，其后图片 url 经 imageCache 转为 base64
// data URL，P3-3）；无图行与 multimodal=false 时保持 string。
func TestBuildNewMessageImagesMultimodal(t *testing.T) {
	s := openTestStore(t)
	now := time.Unix(1762400000, 0)
	newT := now.Add(-30 * time.Second)
	imgSrv, _ := servePNG(t, "image/png")
	imgURL := imgSrv.URL + "/img1"

	build := func(multimodal bool) []ChatMessage {
		cfg := config.AgentConfig{
			PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
			GuideFile:   writeGuideFile(t, agentGuideText),
			History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
			Multimodal:  boolPtr(multimodal),
		}
		b := NewDefaultBuilder(s, cfg)
		return b.Build(&Turn{
			ChatType: "group", ChatID: 928952016, SelfID: 9527, Now: now,
			NewMessages: []TurnMessage{
				{ChatType: "group", ChatID: 928952016, SenderID: 10001, SenderName: "小明",
					Text: "看这张图", Images: []imageRef{{Idx: 0, URL: imgURL}}, Time: newT},
				{ChatType: "group", ChatID: 928952016, SenderID: 10002, SenderName: "小红",
					Text: "纯文本", Time: now.Add(-10 * time.Second)},
			},
		})
	}

	// multimodal=true：带图新消息 → 数组
	msgs := build(true)
	if len(msgs) != 3 {
		t.Fatalf("应返回 system+2 条新消息, got %d: %+v", len(msgs), msgs)
	}
	wantText := "当前时间: " + now.Format("2006-01-02 15:04") + "\n[" +
		newT.Format("01-02 15:04") + "] [群:928952016] <小明(10001)> 看这张图"
	wantParts := []ContentPart{
		{Type: "text", Text: wantText},
		imagePart(pngDataURL),
	}
	gotParts, ok := msgs[1].Content.([]ContentPart)
	if !ok {
		t.Fatalf("带图新消息 content 应为 []ContentPart, got %T: %#v", msgs[1].Content, msgs[1].Content)
	}
	if !reflect.DeepEqual(gotParts, wantParts) {
		t.Errorf("带图新消息 content =\n%#v\nwant\n%#v", gotParts, wantParts)
	}
	// 无图新消息 → string
	line2 := "[" + now.Add(-10*time.Second).Format("01-02 15:04") + "] [群:928952016] <小红(10002)> 纯文本"
	if got, ok := msgs[2].Content.(string); !ok || got != line2 {
		t.Errorf("无图新消息 content 应保持 string %q, got %#v", line2, msgs[2].Content)
	}

	// multimodal=false：全部 string
	msgsOff := build(false)
	if got, ok := msgsOff[1].Content.(string); !ok || got != wantText {
		t.Errorf("multimodal=false 时带图新消息应为纯文本, got %#v", msgsOff[1].Content)
	}
}

// TestBuildHistoryImageRowDropsFailedImage 图片下载失败（500）时，image_url
// 部分被剔除、仅剩文本行（P3-3 修订：死链回传会拖垮整个请求，绝不回传原 URL）。
func TestBuildHistoryImageRowDropsFailedImage(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
		Multimodal:  boolPtr(true),
	}
	b := NewDefaultBuilder(s, cfg)

	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(imgSrv.Close)
	imgURL := imgSrv.URL + "/a.png"

	t1 := time.Unix(1762400000, 0)
	putRecordFull(t, s, &storage.MessageRecord{
		MessageID: 1, Direction: "recv", ChatType: "group", ChatID: 928952016,
		UserID: 10001, SenderName: "小明",
		RawMessage: "[CQ:image,file=a.image,subType=0,url=" + imgURL + "] 看这张图",
		Segments:   []byte(`[{"type":"image","data":{"file":"a.image","url":"` + imgURL + `","sub_type":0}}]`),
		Time:       float64(t1.Unix()),
	})

	msgs := b.Build(&Turn{ChatType: "group", ChatID: 928952016, SelfID: 9527, Now: t1.Add(time.Minute)})
	if len(msgs) != 2 {
		t.Fatalf("应返回 system+1 条历史, got %d: %+v", len(msgs), msgs)
	}
	if gotParts, ok := msgs[1].Content.([]ContentPart); ok {
		for _, p := range gotParts {
			if p.Type == "image_url" {
				t.Errorf("下载失败的图片应被剔除, got image_url: %#v", p)
			}
		}
		if len(gotParts) != 1 || gotParts[0].Type != "text" {
			t.Fatalf("剔除后应仅剩文本部分: %#v", gotParts)
		}
	} else if str, isStr := msgs[1].Content.(string); isStr && !strings.Contains(str, "[图片]") {
		t.Errorf("剔除后文本应保留 [图片] 标注: %q", str)
	}
}

func boolPtr(b bool) *bool { return &b }

// TestBuildHistoryImageWindowLastFive 只有从末尾数最后 5 条历史行（按行计数）
// 走多模态转换：7 条带图历史 → 仅最后 5 条 content 是数组，前 2 条（窗口外）
// 保持纯文本——文本已含 [图片] 标注，更早的图不让模型直接看（按需用
// view_message_images 查看）。
func TestBuildHistoryImageWindowLastFive(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
		Multimodal:  boolPtr(true),
	}
	b := NewDefaultBuilder(s, cfg)

	imgSrv, _ := servePNG(t, "image/png")
	imgURL := imgSrv.URL + "/a.png"

	base := time.Unix(1762400000, 0)
	for i := 1; i <= 7; i++ {
		putRecordFull(t, s, &storage.MessageRecord{
			MessageID: int64(i), Direction: "recv", ChatType: "group", ChatID: 928952016,
			UserID: 10001, SenderName: "小明",
			RawMessage: "[CQ:image,file=a.image,subType=0,url=" + imgURL + "] 图" + strconv.Itoa(i),
			Segments:   []byte(`[{"type":"image","data":{"file":"a.image","url":"` + imgURL + `"}}]`),
			Time:       float64(base.Add(time.Duration(i) * time.Second).Unix()),
		})
	}

	msgs := b.Build(&Turn{ChatType: "group", ChatID: 928952016, SelfID: 9527, Now: base.Add(time.Minute)})
	if len(msgs) != 8 {
		t.Fatalf("应返回 system+7 条历史, got %d", len(msgs))
	}
	// 前 2 条（窗口外）保持 string
	for i := 1; i <= 2; i++ {
		if got, ok := msgs[i].Content.(string); !ok {
			t.Errorf("窗口外带图历史 msgs[%d] 应保持纯文本, got %T", i, msgs[i].Content)
		} else if !strings.Contains(got, "[图片]") {
			t.Errorf("窗口外带图历史文本应含 [图片] 标注: %q", got)
		}
	}
	// 最后 5 条（窗口内）是数组
	for i := 3; i <= 7; i++ {
		if _, ok := msgs[i].Content.([]ContentPart); !ok {
			t.Errorf("窗口内带图历史 msgs[%d] 应为多模态数组, got %T", i, msgs[i].Content)
		}
	}
}

// TestBuildHistoryImageWindowCountsAllLines 窗口按行计数、与是否带图无关：
// 6 条历史只有第 1、6 条带图（其余纯文本）→ 窗口是第 2..6 条行，第 1 条带图行
// 在窗口外必须保持纯文本（若按"最近 5 张带图行"计数它就会错误地变成数组）。
func TestBuildHistoryImageWindowCountsAllLines(t *testing.T) {
	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
		Multimodal:  boolPtr(true),
	}
	b := NewDefaultBuilder(s, cfg)

	imgSrv, _ := servePNG(t, "image/png")
	imgURL := imgSrv.URL + "/a.png"

	base := time.Unix(1762400000, 0)
	for i := 1; i <= 6; i++ {
		rec := &storage.MessageRecord{
			MessageID: int64(i), Direction: "recv", ChatType: "group", ChatID: 928952016,
			UserID: 10001, SenderName: "小明",
			Time: float64(base.Add(time.Duration(i) * time.Second).Unix()),
		}
		if i == 1 || i == 6 {
			rec.RawMessage = "[CQ:image,file=a.image,subType=0,url=" + imgURL + "] 图" + strconv.Itoa(i)
			rec.Segments = []byte(`[{"type":"image","data":{"file":"a.image","url":"` + imgURL + `"}}]`)
		} else {
			rec.RawMessage = fmt.Sprintf("纯文本 %d", i)
		}
		putRecordFull(t, s, rec)
	}

	msgs := b.Build(&Turn{ChatType: "group", ChatID: 928952016, SelfID: 9527, Now: base.Add(time.Minute)})
	if len(msgs) != 7 {
		t.Fatalf("应返回 system+6 条历史, got %d", len(msgs))
	}
	if _, ok := msgs[1].Content.([]ContentPart); ok {
		t.Errorf("第 1 条带图行在窗口外（第 6 条/共 6 条）应保持纯文本, got 数组: %#v", msgs[1].Content)
	}
	if _, ok := msgs[6].Content.([]ContentPart); !ok {
		t.Errorf("第 6 条带图行在窗口内应为数组, got %T", msgs[6].Content)
	}
}

// TestMultimodalDropsFailedImages 图片下载失败时该 image_url 部分应被剔除
// （保留文本行），绝不能把注定加载失败的原始 URL 交给提供商——否则整个请求
// 被 400 拒绝。混合场景：成功的图保留，失败的图剔除。
func TestMultimodalDropsFailedImages(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if strings.HasPrefix(r.URL.Path, "/bad") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// 真实 PNG 魔数（嗅探器要求）
		w.Write([]byte("\x89PNG\r\n\x1a\npng-bytes"))
	}))
	defer srv.Close()

	s := openTestStore(t)
	cfg := config.AgentConfig{
		PersonaFile: writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:   writeGuideFile(t, agentGuideText),
		History:     config.AgentHistory{Max: 40, Trim: 20},
		ImageDir:    t.TempDir(),
	}
	b := NewDefaultBuilder(s, cfg)
	b.multimodal = true
	b.images = newImageStoreWithClient(t.TempDir(), s, srv.Client())

	good := srv.URL + "/good.png"
	bad := srv.URL + "/bad.jpg"
	segs := []byte(`[{"type":"image","data":{"url":"` + good + `"}},{"type":"image","data":{"url":"` + bad + `"}}]`)
	rec := &storage.MessageRecord{
		MessageID: 1, Direction: "recv", ChatType: "group", ChatID: 888, UserID: 10,
		RawMessage: `[CQ:image,file=x.png][CQ:image,file=y.jpg]`, Segments: segs, Time: 1762400000,
	}
	if err := s.PutMessage(rec); err != nil {
		t.Fatal(err)
	}

	msgs := b.Build(&Turn{ChatType: "group", ChatID: 888, SelfID: 9527, Now: time.Unix(1762400001, 0)})
	var userRow ChatMessage
	for _, m := range msgs {
		if m.Role == "user" {
			userRow = m
		}
	}
	parts, ok := userRow.Content.([]ContentPart)
	if !ok {
		t.Fatalf("多模态行 content 应为数组, got %T", userRow.Content)
	}
	imageCount := 0
	for _, p := range parts {
		if p.Type == "image_url" {
			imageCount++
			if p.ImageURL == nil || strings.Contains(p.ImageURL.URL, "/bad.jpg") {
				t.Errorf("失败的图片不应保留原始 URL: %+v", p)
			}
		}
	}
	if imageCount != 1 {
		t.Fatalf("image_url 数 = %d, want 1（bad 应被剔除）", imageCount)
	}
}
