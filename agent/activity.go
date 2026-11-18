package agent

import (
	"context"
	"fmt"
	"log/slog"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
	agentruntime "Q-Mi/agent/runtime"
	"Q-Mi/utils/config"
)

// activityDriver 是组装层到运行时的适配：准备上下文、选择模型、发布检查信息。
type activityDriver struct {
	manager *Manager
	config  config.AgentConfig
	defs    []ToolDef
}

func (d *activityDriver) Prepare(ctx context.Context, turn *Turn, _ int) (agentruntime.PreparedTurn, error) {
	m := d.manager
	newInput := false
	for {
		turn = m.refreshLiveContext(turn)
		m.rememberContextTurn(turn)
		toolCtx := &ToolContext{
			Store: m.store, Turn: turn, Timer: m.timerStore, Images: m.images,
			Tasks: m.tasks, Approvals: m.approvals, Browser: m.browser,
			Events: m.events, Workspace: d.config.WorkspaceDir,
			NotesPath: m.notesPath, StatePath: m.statePath, Admin: batchIsAdmin(turn),
			SleepStart: m.RequestSleep, SleepStatus: m.SleepStatus,
		}
		tools := m.toolRegistry().Build(toolCtx)
		defs := toolDefs(tools)
		d.defs = defs
		revision := m.conversation.Compactions
		if err := m.prepareContext(ctx, turn, defs); err != nil {
			slog.Warn("上下文预算不足，暂停活动", "err", err)
			return agentruntime.PreparedTurn{}, err
		}
		if next := m.takePending(); next != nil {
			turn = next
			m.loadTurnState(turn)
			m.beginTurn(turn)
			newInput = true
			continue
		}
		if revision != m.conversation.Compactions {
			continue
		}
		return agentruntime.PreparedTurn{
			Turn: turn, Tools: tools, Executor: m.toolExecutor(toolCtx), NewInput: newInput,
			Request: ChatRequest{
				Model: d.config.Model, Messages: agentcontext.ToWireMessages(m.conversation.Messages),
				Temperature: d.config.Temperature, Tools: defs,
			},
		}, nil
	}
}

func (d *activityDriver) Request(ctx context.Context, prepared agentruntime.PreparedTurn, round int) (*ChatResponse, error) {
	m := d.manager
	attempts := d.config.MaxAttempts
	if attempts <= 0 {
		attempts = defaultMaxAttempts
	}
	turn, req := prepared.Turn, prepared.Request
	response, slot, tier, err := m.chatWithWorking(ctx, turn, req, m.conversation.Messages, needForMessages(req.Messages), attempts, round)
	if err != nil {
		if model.IsContextLimitError(err) {
			fixed, protected := m.conversation.BudgetParts(req.Tools)
			return nil, &agentruntime.BudgetError{
				Estimated: estimatedInputTokens(req.Messages, req.Tools), Budget: d.config.Context.WithDefaults().MaxInputTokens,
				Fixed: fixed, Protected: protected, Cause: err,
			}
		}
		return nil, err
	}
	candidate := ""
	if slot != nil {
		candidate = slot.Name()
	}
	u := response.Usage
	m.recordUsageFor(candidate, u)
	m.events.Record(turn.ChatType, turn.ChatID, KindUsage,
		fmt.Sprintf("hit=%d miss=%d out=%d candidate=%s tier=%s",
			u.PromptCacheHitTokens, u.PromptCacheMissTokens, u.CompletionTokens, candidate, tier))
	slog.Info("用量", "chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round, "tier", tier,
		"candidate", candidate, "cache_hit", u.PromptCacheHitTokens, "cache_miss", u.PromptCacheMissTokens,
		"prompt", u.PromptTokens, "output", u.CompletionTokens)
	return response, nil
}

func (d *activityDriver) RecoverContext(ctx context.Context, prepared agentruntime.PreparedTurn, cause error) (bool, error) {
	if !model.IsContextLimitError(cause) {
		return false, nil
	}
	m := d.manager
	m.events.Record(prepared.Turn.ChatType, prepared.Turn.ChatID, KindError,
		"模型拒绝上下文长度，尝试压缩已读工作记录；本次活动最多重试一次")
	result, err := m.conversation.CompactContext(d.config.Context, prepared.Request.Tools,
		func(prefix []ChatMessage) string { return m.summarizeContext(ctx, prepared.Turn, prefix) })
	m.publishContextInspection("working", m.conversation.Messages, prepared.Request.Tools)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return err == nil && result.Compacted && result.After < result.Before, nil
}

func (d *activityDriver) Observe(event agentruntime.LoopEvent) {
	m := d.manager
	if event.Phase != "" {
		m.setInspectionPhase(event.Phase, event.Round, "", "", "")
	}
	if event.Debug && d.config.Debug {
		m.dumpDebug(event.Prepared.Turn, event.Prepared.Request, event.Round,
			event.Response, event.Results, event.Err, false)
	}
	if event.Publish {
		m.publishContextInspection("working", m.conversation.Messages, event.Prepared.Request.Tools)
	}
}
