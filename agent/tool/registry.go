package tool

import (
	"fmt"
	"strings"
)

// Module 是静态注册与文档单元。C 由组装层指定，注册表不依赖具体服务。
type Module[C any] interface {
	Name() string
	OneLiner() string
	Intro() string
	Tools(*C) []Tool
}

// Registry 按传入顺序保存模块，构造后只读。
type Registry[C any] struct {
	modules []Module[C]
}

func NewRegistry[C any](modules ...Module[C]) *Registry[C] {
	return &Registry[C]{modules: append([]Module[C](nil), modules...)}
}

func (r *Registry[C]) Build(ctx *C) []Tool {
	if ctx == nil {
		ctx = new(C)
	}
	var out []Tool
	for _, m := range r.modules {
		out = append(out, m.Tools(ctx)...)
	}
	return out
}

// PromptDocs 生成确定性的行为说明。参数契约只保留在 API schema 中。
func (r *Registry[C]) PromptDocs() string {
	var b strings.Builder
	b.WriteString("# 工具使用说明\n\n")
	b.WriteString("## 目录\n\n")
	for _, m := range r.modules {
		var names []string
		for _, t := range m.Tools(new(C)) {
			names = append(names, t.Name)
		}
		fmt.Fprintf(&b, "- %s：%s（%s）\n", m.Name(), m.OneLiner(), strings.Join(names, " "))
	}
	b.WriteString("\n调用参数通过 API tool_calls 传递，字段/类型/必填项以 tools 中的参数 schema 为准；下文只补充行为规则和示例，不重复参数表。\n\n")
	for _, m := range r.modules {
		b.WriteString("## " + m.Name() + "\n\n")
		if m.OneLiner() != "" {
			b.WriteString(m.OneLiner() + "\n\n")
		}
		if m.Intro() != "" {
			b.WriteString(m.Intro() + "\n\n")
		}
		for _, t := range m.Tools(new(C)) {
			b.WriteString("### " + t.Name + "\n\n")
			if t.Description != "" {
				b.WriteString(t.Description + "\n\n")
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
