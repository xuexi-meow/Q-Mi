// toolkit.go 工具体系标准化（spec docs/specs/2026-11-08-tool-registry-design.md）：
// ToolModule = 注册单元 + 文档单元 + 配置启停单元；ToolRegistry 按固定顺序
// 拼接模块、生成全量工具文档（PromptDocs，启动时一次、进程内不可变，注入
// system 静态区）。无热插拔：模块启停走配置+重启。
package agent

import (
	"fmt"
	"sort"
	"strings"

	"Q-Mi/storage"
)

// ToolContext 一次 turn 的工具运行时依赖，模块按需取用。字段与 manager
// 的运行时状态一一对应，模块闭包捕获本结构体的字段而非散参数。
type ToolContext struct {
	Store     *storage.Store // 可为 nil（记忆库未启用，查询类工具报错）
	Turn      *Turn          // 当前会话上下文（chat/self/pendingImages；可为 nil）
	Timer     *TimerStore    // 可为 nil（schedule/cancel_timer 报错）
	Images    *imageStore    // 可为 nil（view_message_images/自动带图报错）
	Tasks     *TaskManager   // 可为 nil（_async/任务管理工具报错）
	Events    *EventLog      // 可为 nil（self_events 报错）
	NotesPath string
	StatePath string
	Admin     bool // 本 turn 发送者是否管理员（危险操作门槛）
}

// module ToolModule 的最小实现：静态元信息 + 工具构造闭包。
type module struct {
	name, oneliner, intro string
	tools                 func(*ToolContext) []Tool
}

func (m module) Name() string     { return m.name }
func (m module) OneLiner() string { return m.oneliner }
func (m module) Intro() string    { return m.intro }
func (m module) Tools(ctx *ToolContext) []Tool {
	return m.tools(ctx)
}

// ToolModule 功能板块接口。实现建议直接用 module 结构体字面量。
type ToolModule interface {
	Name() string     // 稳定标识（配置 modules 键）
	OneLiner() string // 一句话（目录用，≤60 字）
	Intro() string    // 板块级说明（何时用这组工具、礼仪约束，可空）
	Tools(*ToolContext) []Tool
}

// ToolRegistry 模块注册表：无热插拔、无运行时变更，构造后只读。
type ToolRegistry struct {
	modules []ToolModule
}

// NewRegistry 按传入顺序固定注册模块（顺序即 tools 数组顺序与文档顺序，
// 由注册代码决定，禁止 map 遍历）。配置关闭的模块由调用方在构造前过滤。
func NewRegistry(modules ...ToolModule) *ToolRegistry {
	return &ToolRegistry{modules: modules}
}

// Build 拼接全部启用模块的工具，顺序 = 注册顺序。ctx 各字段可为 nil/零值，
// 工具构造期不得解引用（依赖在 Run 内判空）。
func (r *ToolRegistry) Build(ctx *ToolContext) []Tool {
	if ctx == nil {
		ctx = &ToolContext{}
	}
	var out []Tool
	for _, m := range r.modules {
		out = append(out, m.Tools(ctx)...)
	}
	return out
}

// PromptDocs 全量工具文档 markdown：目录（板块+一句话+工具名清单）+ 每板块
// Intro + 每工具 Description+参数表（从 Parameters schema 自动渲染）+Usage。
// 启动时生成一次注入 system 静态区；输出必须确定性（lint 测试断言两次调用
// 逐字节一致）。
func (r *ToolRegistry) PromptDocs() string {
	var b strings.Builder
	b.WriteString("# 工具使用说明\n\n")
	b.WriteString("## 目录\n\n")
	for _, m := range r.modules {
		fmt.Fprintf(&b, "- %s：%s（%s）\n", m.Name(), m.OneLiner(), strings.Join(toolNames(m.Tools(nil)), " "))
	}
	b.WriteString("\n调用参数经 function calling 传递；每个工具的参数明细/示例见下文。\n\n")
	for _, m := range r.modules {
		b.WriteString("## " + m.Name() + "\n\n")
		if m.OneLiner() != "" {
			b.WriteString(m.OneLiner() + "\n\n")
		}
		if m.Intro() != "" {
			b.WriteString(m.Intro() + "\n\n")
		}
		for _, t := range m.Tools(nil) {
			b.WriteString("### " + t.Name + "\n\n")
			if t.Description != "" {
				b.WriteString(t.Description + "\n\n")
			}
			if ps := renderParams(t.Parameters); ps != "" {
				b.WriteString(ps + "\n")
			}
			if t.Usage != "" {
				b.WriteString(t.Usage + "\n\n")
			}
			if t.Asyncable {
				b.WriteString("支持 `\"_async\": true` 后台执行：立即返回任务回执（task_id），完成后自动送达结果，不阻塞本次思考；task_list/task_get 可随时查看，task_cancel 可停止。\n\n")
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// renderParams 把 objectParams 形态的 JSON schema 渲染成参数表文本：
// `- 名称 (类型) [必填]: 描述`，键名字母序（确定性）。非对象/无参数返回空串。
func renderParams(params any) string {
	schema, ok := params.(map[string]any)
	if !ok {
		return ""
	}
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return ""
	}
	req := map[string]bool{}
	if list, ok := schema["required"].([]string); ok {
		for _, name := range list {
			req[name] = true
		}
	}
	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("参数：\n")
	for _, n := range names {
		p, _ := props[n].(map[string]any)
		typ, _ := p["type"].(string)
		desc, _ := p["description"].(string)
		mark := ""
		if req[n] {
			mark = " [必填]"
		}
		fmt.Fprintf(&b, "- %s (%s)%s: %s\n", n, typ, mark, desc)
	}
	return b.String()
}

// defaultModules 内置板块（固定顺序；name 对应配置 agent.modules 键）。
func defaultModules() []ToolModule {
	return []ToolModule{
		module{name: "core", oneliner: "发消息、记思考笔记、更新情绪、结束思考",
			intro: "一个思考轮内可多次 send_message 分条发送；不调用即本轮不回复。finish 是唯一的轮终止工具。",
			tools: coreTools},
		module{name: "memory", oneliner: "查聊天记录、搜历史、看消息图片",
			tools: memoryTools},
		module{name: "sticker", oneliner: "表情收藏与 QQ 账号级收藏表情",
			tools: stickerTools},
		module{name: "qzone", oneliner: "QQ 空间：发/删/浏览/读/赞/评/资料",
			intro: "全部以 tid/target_uin 定位，与 feed_ref 不能混用；空间是公开内容，发布/删除前想清楚。",
			tools: func(ctx *ToolContext) []Tool { return qzoneTools(ctx.Admin) }},
		module{name: "admin", oneliner: "QQ 管理：群/好友/文件/请求处理 + napcat 逃生舱",
			intro: "危险操作（踢人/退群/设管理员/改名资料类逃生舱动作）需要管理员身份，非管理员调用返回权限错误。",
			tools: func(ctx *ToolContext) []Tool { return qqTools(ctx.Admin) }},
		module{name: "system", oneliner: "定时器、后台任务管理、行为回溯",
			tools: systemTools},
	}
}

// toolNames 模块工具名清单。
func toolNames(tools []Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	return names
}
