package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"Q-Mi/utils/config"
)

func TestBuilderReloadsLearnedChatStyleInTail(t *testing.T) {
	workspace := t.TempDir()
	b := NewDefaultBuilder(nil, config.AgentConfig{
		PersonaFile:  writePersonaFile(t, "你是QQ上的AI成员。"),
		GuideFile:    writeGuideFile(t, agentGuideText),
		ImageDir:     t.TempDir(),
		WorkspaceDir: workspace,
	})
	now := time.Unix(1762400000, 0)
	turn := &Turn{
		SelfID: 999, Now: now,
		NewMessages: []TurnMessage{{ChatType: "group", ChatID: 888, Text: "你好", Time: now}},
	}
	before := b.Build(turn)
	if len(before) != 2 {
		t.Fatalf("尚无习惯文件时仍应正常组装上下文, got %d", len(before))
	}

	tools := computerTools(&ToolContext{Workspace: workspace})
	for _, style := range []string{"接梗通常只接半句", "有人明确追问细节才展开解释"} {
		args, err := json.Marshal(map[string]string{"path": "chat_style.md", "content": style})
		if err != nil {
			t.Fatal(err)
		}
		runToolOK(t, tools, "write_file", string(args))
		msgs := b.Build(turn)
		if contentText(msgs[0]) != contentText(before[0]) {
			t.Error("自学习惯不应改变 system 缓存前缀")
		}
		tail := contentText(msgs[len(msgs)-1])
		if !strings.Contains(tail, style) || !strings.Contains(tail, "当前时间:") ||
			strings.Index(tail, style) > strings.Index(tail, "当前时间:") {
			t.Errorf("最新习惯应自动注入本轮消息之前, got %q", tail)
		}
	}
	final := contentText(b.Build(turn)[1])
	if strings.Contains(final, "接梗通常只接半句") {
		t.Error("已覆盖的旧习惯不应残留在下一轮上下文")
	}
	// 重建 builder 后仍读取同一个持久化文件，不依赖短期 notes。
	restarted := NewDefaultBuilder(nil, config.AgentConfig{
		PersonaFile: writePersonaFile(t, "test"), GuideFile: writeGuideFile(t, agentGuideText),
		ImageDir: t.TempDir(), WorkspaceDir: workspace,
	})
	if tail := contentText(restarted.Build(turn)[1]); !strings.Contains(tail, "有人明确追问细节才展开解释") {
		t.Errorf("重建后应保留已学习的聊天习惯, got %q", tail)
	}
}

func TestBuilderBoundsLearnedChatStyle(t *testing.T) {
	workspace := t.TempDir()
	tools := computerTools(&ToolContext{Workspace: workspace})
	args, err := json.Marshal(map[string]string{
		"path": "chat_style.md", "content": strings.Repeat("短", 2000) + "不应注入的溢出内容",
	})
	if err != nil {
		t.Fatal(err)
	}
	runToolOK(t, tools, "write_file", string(args))
	b := NewDefaultBuilder(nil, config.AgentConfig{
		PersonaFile: writePersonaFile(t, "test"), GuideFile: writeGuideFile(t, agentGuideText),
		ImageDir: t.TempDir(), WorkspaceDir: workspace,
	})
	msgs := b.Build(&Turn{
		NewMessages: []TurnMessage{{Text: "hi"}}, Now: time.Now(),
	})
	tail := contentText(msgs[len(msgs)-1])
	if !strings.Contains(tail, strings.Repeat("短", 2000)) ||
		strings.Contains(tail, "不应注入的溢出内容") || !strings.Contains(tail, "截断") {
		t.Errorf("聊天习惯应限制上下文长度并注明截断, got %q", tail)
	}
	// 无效路径不应中断正常对话。
	bad := NewDefaultBuilder(nil, config.AgentConfig{
		PersonaFile: writePersonaFile(t, "test"), GuideFile: writeGuideFile(t, agentGuideText),
		ImageDir: t.TempDir(), WorkspaceDir: filepath.Join(workspace, "chat_style.md"),
	})
	if got := len(bad.Build(&Turn{NewMessages: []TurnMessage{{Text: "hi"}}})); got != 2 {
		t.Errorf("习惯文件不可读时仍应继续组装本轮, got %d", got)
	}
}
