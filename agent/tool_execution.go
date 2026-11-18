package agent

import agentruntime "Q-Mi/agent/runtime"

func (m *Manager) execToolCall(ctx *ToolContext, tools []Tool, call ToolCall) (string, error) {
	return m.toolExecutor(ctx).Execute(tools, call)
}

func (m *Manager) toolExecutor(ctx *ToolContext) *agentruntime.Executor {
	return &agentruntime.Executor{
		Turn: ctx.Turn, Tasks: m.tasks, Approvals: ctx.Approvals,
		ApprovedTools: func() []Tool {
			approved := *ctx
			approved.Approved = true
			return m.toolRegistry().Build(&approved)
		},
	}
}
