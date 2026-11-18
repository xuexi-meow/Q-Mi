package runtime

import (
	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
	agenttool "Q-Mi/agent/tool"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Executor executes tools without importing any concrete capability package.
type Executor struct {
	Turn          *agentcontext.Turn
	Tasks         *TaskManager
	Approvals     *ApprovalManager
	ApprovedTools func() []agenttool.Tool
}

type Tool = agenttool.Tool
type ToolCall = model.ToolCall

// execToolCall 执行一次工具调用：普通调用同步执行返回结果；Asyncable 工具
// 带 {"_async":true,...} 时转入 TaskManager 后台执行，立即返回任务回执
// （结果完成经 deliverTaskResult 唤醒投递，不阻塞本 turn，spec §2.5 方案 B）。
// 未知工具 / Run 缺失 / 执行失败返回 error，由调用方包装成 {"error": ...} 回填。
func (ctx *Executor) Execute(tools []Tool, call ToolCall) (string, error) {
	var tool *Tool
	for i := range tools {
		if tools[i].Name == call.Function.Name {
			tool = &tools[i]
			break
		}
	}
	if tool == nil {
		return "", fmt.Errorf("未知工具 %q", call.Function.Name)
	}
	if tool.Run == nil {
		return "", fmt.Errorf("工具 %q 不可执行", call.Function.Name)
	}
	if asyncRequested(json.RawMessage(call.Function.Arguments)) && !tool.Asyncable {
		return "", fmt.Errorf("工具 %q 不支持 _async 后台执行", tool.Name)
	}
	args, err := agenttool.ValidateArguments(*tool, call.Function.Arguments)
	if err != nil {
		return "", err
	}
	call.Function.Arguments = string(args)
	if asyncRequested(json.RawMessage(call.Function.Arguments)) {
		if !tool.Asyncable {
			return "", fmt.Errorf("工具 %q 不支持 _async 后台执行", tool.Name)
		}
		if ctx.Turn == nil {
			return "", fmt.Errorf("无会话上下文，无法后台执行 %q", tool.Name)
		}
		args := stripAsyncKey(json.RawMessage(call.Function.Arguments))
		id := ctx.Tasks.Start(BackgroundTask{
			Tool:      tool.Name,
			ArgsBrief: limitRunes(args, 200),
			ChatType:  ctx.Turn.ChatType,
			ChatID:    ctx.Turn.ChatID,
			SelfID:    ctx.Turn.SelfID,
		}, func(context.Context) (string, error) {
			return tool.Run(json.RawMessage(args))
		})
		return marshalResult(map[string]any{
			"task_id": id,
			"status":  "started",
			"note":    "后台执行中，完成后自动送达结果；task_list/task_get 可随时查看，task_cancel 可停止",
		})
	}
	result, toolErr := tool.Run(json.RawMessage(call.Function.Arguments))
	var need agenttool.ApprovalRequired
	if errors.As(toolErr, &need) {
		// 越界/危险操作 → 管理员审批（spec 权限模型）：登记审批单、QQ 通知
		// 管理员，当轮返回回执；管理员「同意 <id>」后以 Approved ctx 重跑并
		// 投递结果，「拒绝」投递拒绝回执。不阻塞本 turn。
		if ctx.Approvals == nil {
			return "", fmt.Errorf("%s（审批通道不可用）", need.Error())
		}
		if ctx.Turn == nil {
			return "", fmt.Errorf("无会话上下文，无法发起审批")
		}
		id, reqErr := ctx.Approvals.Request(Approval{
			Tool:     tool.Name,
			Args:     call.Function.Arguments,
			Reason:   need.Reason,
			ChatType: ctx.Turn.ChatType,
			ChatID:   ctx.Turn.ChatID,
			SelfID:   ctx.Turn.SelfID,
			Run: func(bool) (string, error) {
				if ctx.ApprovedTools != nil {
					for _, approvedTool := range ctx.ApprovedTools() {
						if approvedTool.Name == tool.Name && approvedTool.Run != nil {
							return approvedTool.Run(json.RawMessage(call.Function.Arguments))
						}
					}
				}
				return "", fmt.Errorf("工具 %q 已不可用，无法执行审批操作", tool.Name)
			},
		})
		if reqErr != nil {
			return "", fmt.Errorf("发起审批失败: %w", reqErr)
		}
		return marshalResult(map[string]any{
			"approval_id": id,
			"status":      "pending",
			"note":        "已请求管理员确认，管理员回复「同意 <id>」后自动执行并送达结果；期间你可以继续其他工作",
		})
	}
	return result, toolErr
}

// asyncRequested 参数顶层是否带 "_async": true（保留键，仅在顶层生效）。
func asyncRequested(args json.RawMessage) bool {
	if len(args) == 0 {
		return false
	}
	var peek struct {
		Async bool `json:"_async"`
	}
	if json.Unmarshal(args, &peek) != nil {
		return false
	}
	return peek.Async
}

// stripAsyncKey 剥离顶层 _async 键；后台任务的 Run 不应看到该保留键。
func stripAsyncKey(args json.RawMessage) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(args, &m); err != nil || m == nil {
		return string(args)
	}
	delete(m, "_async")
	out, err := json.Marshal(m)
	if err != nil {
		return string(args)
	}
	return string(out)
}

// toolErrorJSON 工具错误的标准回填形态。
func ToolErrorJSON(err error) string {
	data, mErr := json.Marshal(map[string]string{"error": err.Error()})
	if mErr != nil {
		return `{"error":"工具执行失败"}`
	}
	return string(data)
}

func marshalResult(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("序列化工具结果失败: %w", err)
	}
	return string(data), nil
}

func limitRunes(text string, max int) string {
	runes := []rune(text)
	if len(runes) > max {
		return string(runes[:max])
	}
	return text
}
