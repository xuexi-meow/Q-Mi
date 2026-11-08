package agent

import (
	"reflect"
	"strings"
	"testing"

	"Q-Mi/utils/config"
)

// stubModule 测试用板块。
func stubModule(name, oneliner, intro string, tools ...Tool) ToolModule {
	return module{
		name:     name,
		oneliner: oneliner,
		intro:    intro,
		tools: func(*ToolContext) []Tool {
			return tools
		},
	}
}

func stubTool(name string) Tool {
	return Tool{Name: name, Description: name + " 一句话", Parameters: objectParams(nil, nil)}
}

// TestRegistryBuildOrderFixed Build 顺序 = 注册顺序，多次构建字节级一致；
// 模块顺序不由 map/启停状态决定（缓存硬规则）。
func TestRegistryBuildOrderFixed(t *testing.T) {
	r := NewRegistry(
		stubModule("core", "核心", "", stubTool("finish"), stubTool("send_message")),
		stubModule("admin", "管理", "", stubTool("kick_member")),
	)
	ctx := &ToolContext{}
	first := r.Build(ctx)
	got := toolNames(first)
	want := []string{"finish", "send_message", "kick_member"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Build 顺序 = %v, want %v", got, want)
	}
	second := r.Build(&ToolContext{Admin: true})
	if !reflect.DeepEqual(toolNames(second), want) {
		t.Errorf("Admin ctx 下顺序不应改变: %v", toolNames(second))
	}
}

// TestRegistryBuildNilCtx nil ctx 等价零值 ctx；模块构造期不得解引用。
func TestRegistryBuildNilCtx(t *testing.T) {
	r := NewRegistry(stubModule("core", "", "", stubTool("a")))
	if got := toolNames(r.Build(nil)); len(got) != 1 || got[0] != "a" {
		t.Errorf("nil ctx Build = %v", got)
	}
}

// TestPromptDocsShape 文档含目录与每工具段；顺序与注册一致。
func TestPromptDocsShape(t *testing.T) {
	r := NewRegistry(
		stubModule("core", "核心思考与发送", "思考循环礼仪……",
			Tool{Name: "finish", Description: "结束本轮思考", Parameters: objectParams(nil, nil),
				Usage: "无参数。调用即结束。"}),
		stubModule("qzone", "QQ 空间", "", stubTool("post_qzone")),
	)
	doc := r.PromptDocs()
	for _, want := range []string{
		"# 工具使用说明", "- core：核心思考与发送", "- qzone：",
		"## core", "思考循环礼仪……", "### finish", "无参数。调用即结束。",
		"### post_qzone", "post_qzone 一句话",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("文档缺 %q:\n%s", want, doc)
		}
	}
	if strings.Index(doc, "core") > strings.Index(doc, "qzone") {
		t.Errorf("文档顺序应与注册一致:\n%s", doc)
	}
}

// TestPromptDocsAsyncableNote Asyncable 工具自动带后台执行说明。
func TestPromptDocsAsyncableNote(t *testing.T) {
	r := NewRegistry(stubModule("core", "", "", Tool{
		Name: "upload_group_file", Description: "传文件", Parameters: objectParams(nil, nil),
		Asyncable: true,
	}))
	if doc := r.PromptDocs(); !strings.Contains(doc, `"_async": true`) {
		t.Errorf("Asyncable 工具文档应含 _async 说明:\n%s", doc)
	}
}

// TestPromptDocsDeterministic 文档必须确定性：两次生成逐字节一致（缓存硬规则）。
func TestPromptDocsDeterministic(t *testing.T) {
	r := NewRegistry(
		stubModule("core", "核心", "说明", stubTool("a"), stubTool("b")),
		stubModule("admin", "管理", "", stubTool("kick_member")),
	)
	a, b := r.PromptDocs(), r.PromptDocs()
	if a != b {
		t.Fatalf("PromptDocs 两次输出不一致（含时间戳/随机/运行时状态即违规）:\n---a---\n%s\n---b---\n%s", a, b)
	}
}

// TestEnabledModulesFiltersByConfig 配置关闭的板块不加载，缺省键 = 启用。
func TestEnabledModulesFiltersByConfig(t *testing.T) {
	all := enabledModules(config.AgentConfig{})
	if len(all) != 7 {
		t.Fatalf("缺省板块数 = %d, want 7", len(all))
	}
	filtered := enabledModules(config.AgentConfig{Modules: map[string]bool{
		"qzone": false,
		"admin": false,
	}})
	names := make([]string, 0, len(filtered))
	for _, m := range filtered {
		names = append(names, m.Name())
	}
	want := []string{"core", "memory", "sticker", "computer", "system"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("过滤后板块 = %v, want %v", names, want)
	}
	// 显式 true 也保留
	kept := enabledModules(config.AgentConfig{Modules: map[string]bool{"core": true, "qzone": false}})
	if len(kept) != 6 {
		t.Errorf("显式 true 应保留, got %d 板块", len(kept))
	}
}
