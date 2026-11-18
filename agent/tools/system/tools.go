// Package system 提供计时、后台任务查询和行为回溯工具。
package system

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	agentcontext "Q-Mi/agent/context"
	agentruntime "Q-Mi/agent/runtime"
	"Q-Mi/agent/tool"
)

type Timers interface {
	Schedule(time.Duration, string, string, int64, int64) (agentruntime.ScheduledTimer, error)
	Cancel(string) bool
}

type Tasks interface {
	List(string) []agentruntime.BackgroundTask
	Get(int64) (agentruntime.BackgroundTask, bool)
	Cancel(int64) (agentruntime.BackgroundTask, error)
}

type Events interface {
	After(uint64) []agentruntime.Event
}

type Dependencies struct {
	Turn   *agentcontext.Turn
	Timer  Timers
	Tasks  Tasks
	Events Events
}

type Tool = tool.Tool
type Event = agentruntime.Event

const timeLayout = "01-02 15:04"

func chatSource(chatType string, chatID int64) string {
	name := chatType
	switch chatType {
	case "group":
		name = "群"
	case "private":
		name = "私聊"
	}
	return "[" + name + ":" + strconv.FormatInt(chatID, 10) + "]"
}

func decodeArgs(args json.RawMessage, value any) error {
	if len(args) == 0 {
		return nil
	}
	if err := json.Unmarshal(args, value); err != nil {
		return fmt.Errorf("工具参数不是合法 JSON: %w", err)
	}
	return nil
}

func marshalCompact(value any) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}

// Tools 系统板块：定时器、后台任务管理、行为回溯。
func Tools(ctx *Dependencies) []Tool {
	if ctx == nil {
		ctx = &Dependencies{}
	}
	timer := ctx.Timer
	// schedule 的会话上下文在构造期从 turn 捕获（工具 Run 拿不到 turn）。
	var curChatType string
	var curChatID, curSelfID int64
	if turn := ctx.Turn; turn != nil {
		curChatType, curChatID, curSelfID = turn.ChatType, turn.ChatID, turn.SelfID
	}
	tools := []Tool{
		// P3-5 定时器工具：到点经 fire 回调合成为 [定时提醒] 消息排队唤醒。
		{
			Name:        "schedule",
			Description: "定一个一次性闹钟：delay_seconds 秒后你会收到一条 [定时提醒] 开头的消息（note 原样带给你）。提醒走正常消息队列排队，不会打断你正在做的思考。适合控制发言节奏、延时跟进（如「10分钟后看看他回了没」）、定时打卡；循环任务到点后自己再 schedule 一次。",
			Parameters: tool.ObjectParams(map[string]any{
				"delay_seconds": tool.IntProp("多少秒后提醒，1 ~ 604800（最长 7 天）"),
				"note":          tool.StringProp("到点提醒内容：写清楚到时要做什么、发给谁（会话与内容）"),
			}, []string{"delay_seconds", "note"}),
			Usage: "示例——十分钟后在当前群跟进：\n`{\"delay_seconds\":600,\"note\":\"在群 928952016 看看他回了没，回了就接话\"}`\n错误形态：delay 超出 1~604800 或 note 为空 → 报错回填。",
			Run: func(args json.RawMessage) (string, error) {
				if timer == nil {
					return "", errors.New("定时器不可用")
				}
				var p struct {
					DelaySeconds int64  `json:"delay_seconds"`
					Note         string `json:"note"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.Note == "" {
					return "", fmt.Errorf("缺少必填参数 note")
				}
				if p.DelaySeconds < 1 || p.DelaySeconds > 604800 {
					return "", fmt.Errorf("delay_seconds 需在 1 ~ 604800 秒之间（最长 7 天），got %d", p.DelaySeconds)
				}
				rec, err := timer.Schedule(time.Duration(p.DelaySeconds)*time.Second, p.Note, curChatType, curChatID, curSelfID)
				if err != nil {
					return "", err
				}
				return marshalCompact(map[string]any{
					"id":      rec.ID,
					"fire_at": rec.FireAt.Format("01-02 15:04:05"),
				})
			},
		},
		{
			Name:        "cancel_timer",
			Description: "取消一个还没到点的定时提醒（传 schedule 返回的 id）。已触发或已取消的 id 会报错。",
			Parameters: tool.ObjectParams(map[string]any{
				"timer_id": tool.StringProp("schedule 返回的定时器 id"),
			}, []string{"timer_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if timer == nil {
					return "", errors.New("定时器不可用")
				}
				var p struct {
					TimerID string `json:"timer_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.TimerID == "" {
					return "", fmt.Errorf("缺少必填参数 timer_id")
				}
				if !timer.Cancel(p.TimerID) {
					return "", fmt.Errorf("没有找到定时器 %q（可能已触发或已取消）", p.TimerID)
				}
				return marshalCompact(map[string]any{"cancelled": true})
			},
		},
	}
	// 后台任务管理（方案 B 的可见性/控制面）。
	tasks := ctx.Tasks
	tools = append(tools,
		Tool{
			Name:        "task_list",
			Description: "查看你的后台任务列表（_async 发起的任务）：id/工具/状态/耗时/参数摘要。结果全文用 task_get。",
			Parameters: tool.ObjectParams(map[string]any{
				"status": tool.StringProp("可选，按状态过滤：running / done / failed / cancelled / all，缺省 all"),
			}, nil),
			Run: func(args json.RawMessage) (string, error) {
				if tasks == nil {
					return "", errors.New("后台任务不可用")
				}
				var p struct {
					Status string `json:"status"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				list := tasks.List(p.Status)
				now := time.Now()
				rows := make([]map[string]any, 0, len(list))
				for _, t := range list {
					row := map[string]any{
						"id": t.ID, "tool": t.Tool, "status": t.Status,
						"args": t.ArgsBrief,
					}
					end := now
					if t.DoneAt != nil {
						end = *t.DoneAt
					}
					row["elapsed_seconds"] = int(end.Sub(t.StartedAt).Seconds())
					if t.Err != "" {
						row["err"] = t.Err
					}
					rows = append(rows, row)
				}
				return marshalCompact(rows)
			},
		},
		Tool{
			Name:        "task_get",
			Description: "查一个后台任务的详情（含完成结果全文）。",
			Parameters: tool.ObjectParams(map[string]any{
				"task_id": tool.IntProp("task_list 里的任务 id"),
			}, []string{"task_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if tasks == nil {
					return "", errors.New("后台任务不可用")
				}
				var p struct {
					TaskID int64 `json:"task_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				t, ok := tasks.Get(p.TaskID)
				if !ok {
					return "", fmt.Errorf("没有找到任务 %d", p.TaskID)
				}
				return marshalCompact(t)
			},
		},
		Tool{
			Name:        "task_cancel",
			Description: "停止一个还在运行的后台任务（结果不再投递）。已完成/已取消的任务会报错。",
			Parameters: tool.ObjectParams(map[string]any{
				"task_id": tool.IntProp("task_list 里的任务 id"),
			}, []string{"task_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if tasks == nil {
					return "", errors.New("后台任务不可用")
				}
				var p struct {
					TaskID int64 `json:"task_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				t, err := tasks.Cancel(p.TaskID)
				if err != nil {
					return "", err
				}
				return marshalCompact(map[string]any{"cancelled": true, "task_id": t.ID, "tool": t.Tool})
			},
		},
	)

	// 行为回溯：EventLog（与 webui 思考链同源）暴露给模型。
	events := ctx.Events
	tools = append(tools,
		Tool{
			Name:        "self_events",
			Description: "回溯你自己的近期行为流（思考/工具调用/输出/回复，按时间倒序）。用于回忆上一轮做了什么、为什么没回、调过什么工具，防止前后割裂。只覆盖最近几百条事件，更早的靠消息库与思考笔记。",
			Parameters: tool.ObjectParams(map[string]any{
				"kind":    tool.StringProp("可选，按类型过滤：turn_start / reasoning / output / tool_call / tool_result / reply / finish / usage / interrupted / error"),
				"chat_id": tool.IntProp("可选，按会话 ID 过滤"),
				"limit":   tool.IntProp("可选，返回条数，默认 50，最大 200"),
			}, nil),
			Run: func(args json.RawMessage) (string, error) {
				if events == nil {
					return "", errors.New("行为事件流不可用")
				}
				var p struct {
					Kind   string `json:"kind"`
					ChatID int64  `json:"chat_id"`
					Limit  int    `json:"limit"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				limit := p.Limit
				if limit <= 0 {
					limit = 50
				}
				if limit > 200 {
					limit = 200
				}
				all := events.After(0)
				var picked []Event
				for i := len(all) - 1; i >= 0 && len(picked) < limit; i-- { // 新→旧
					e := all[i]
					if p.Kind != "" && e.Kind != p.Kind {
						continue
					}
					if p.ChatID != 0 && e.ChatID != p.ChatID {
						continue
					}
					picked = append(picked, e)
				}
				lines := make([]string, 0, len(picked))
				for _, e := range picked {
					text := strings.ReplaceAll(e.Text, "\n", " ")
					lines = append(lines, fmt.Sprintf("#%d [%s] %s %s: %s",
						e.ID, e.Time.Format(timeLayout), chatSource(e.ChatType, e.ChatID), e.Kind, text))
				}
				return strings.Join(lines, "\n"), nil
			},
		},
	)
	return tools
}
