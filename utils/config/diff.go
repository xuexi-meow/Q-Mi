// diff.go 配置变更分级（spec 2026-11-09 §5）：区分「热生效」与「必须重启」，
// 供 Store 订阅者决定应用范围，并给 WebUI/日志提供可读的变更清单。
package config

import (
	"reflect"
	"strings"
)

// 变更级别。
const (
	LevelHot     = "hot"     // 下一次请求/下一轮直接生效
	LevelRestart = "restart" // 已变更但当前进程不应用，需重启
)

// Change 单个字段变更。
type Change struct {
	Field string
	Level string
}

// Report 一次配置对比结果。
type Report struct {
	Changes []Change
}

func (r Report) Changed() bool { return len(r.Changes) > 0 }

// RestartRequired 是否存在需要重启才生效的变更。
func (r Report) RestartRequired() bool {
	for _, c := range r.Changes {
		if c.Level == LevelRestart {
			return true
		}
	}
	return false
}

// Fields 返回指定级别的变更字段名（保持检测顺序，便于日志稳定）。
func (r Report) Fields(level string) []string {
	var out []string
	for _, c := range r.Changes {
		if c.Level == level {
			out = append(out, c.Field)
		}
	}
	return out
}

// Summary 变更摘要，形如 "agent.temperature(hot), netsetting(restart)"。
func (r Report) Summary() string {
	parts := make([]string, 0, len(r.Changes))
	for _, c := range r.Changes {
		parts = append(parts, c.Field+"("+c.Level+")")
	}
	return strings.Join(parts, ", ")
}

// Diff 对比两份配置，按 spec §5 的分级表输出变更清单。
// 任一侧为 nil 时返回空报告（启动阶段不做变更判定）。
func Diff(old, next *Config) Report {
	var r Report
	if old == nil || next == nil {
		return r
	}
	hot := func(field string, changed bool) {
		if changed {
			r.Changes = append(r.Changes, Change{Field: field, Level: LevelHot})
		}
	}
	restart := func(field string, changed bool) {
		if changed {
			r.Changes = append(r.Changes, Change{Field: field, Level: LevelRestart})
		}
	}

	if old.Bot != nil && next.Bot != nil {
		restart("bot.qq_number", old.Bot.QQ_Number != next.Bot.QQ_Number)
		hot("bot.admin_number", !reflect.DeepEqual(old.Bot.Admin_Number, next.Bot.Admin_Number))
	} else {
		restart("bot", old.Bot != next.Bot)
	}

	restart("netsetting", !reflect.DeepEqual(old.NetSetting, next.NetSetting))
	restart("napcat", !reflect.DeepEqual(old.Napcat, next.Napcat))
	restart("storage", !reflect.DeepEqual(old.Storage, next.Storage))

	if old.WebUI != nil && next.WebUI != nil {
		restart("webui.enable", old.WebUI.Enable != next.WebUI.Enable)
		restart("webui.host", old.WebUI.Host != next.WebUI.Host)
		restart("webui.port", old.WebUI.Port != next.WebUI.Port)
		restart("webui.token", old.WebUI.Token != next.WebUI.Token)
	} else {
		restart("webui", old.WebUI != next.WebUI)
	}

	switch {
	case old.Agent == nil && next.Agent == nil:
		// 都未配置，无变更
	case old.Agent == nil || next.Agent == nil:
		restart("agent", true)
	default:
		prev, cur := *old.Agent, *next.Agent
		restart("agent.enable", prev.Enable != cur.Enable)
		restart("agent.browser_executable", prev.BrowserExec != cur.BrowserExec)
		restart("agent.workspace_dir", prev.WorkspaceDir != cur.WorkspaceDir)
		restart("agent.image_dir", prev.ImageDir != cur.ImageDir)

		hot("agent.endpoint", prev.BaseURL != cur.BaseURL || prev.APIKey != cur.APIKey || prev.Model != cur.Model)
		hot("agent.models", !reflect.DeepEqual(prev.Models, cur.Models))
		hot("agent.modules", !reflect.DeepEqual(prev.Modules, cur.Modules))
		hot("agent.persona_file", prev.PersonaFile != cur.PersonaFile)
		hot("agent.guide_file", prev.GuideFile != cur.GuideFile)
		hot("agent.temperature", prev.Temperature != cur.Temperature)
		hot("agent.debug", prev.Debug != cur.Debug)
		hot("agent.multimodal", !reflect.DeepEqual(prev.Multimodal, cur.Multimodal))
		hot("agent.max_tool_rounds", prev.MaxToolRounds != cur.MaxToolRounds)
		hot("agent.debounce", prev.Debounce != cur.Debounce)
		hot("agent.self_wake", prev.SelfWake != cur.SelfWake)
		hot("agent.history", !reflect.DeepEqual(prev.History, cur.History))
		hot("agent.context", !reflect.DeepEqual(prev.Context, cur.Context))
	}
	return r
}
