package runtime

import (
	"fmt"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
	"Q-Mi/utils/config"
)

type CompactionResult struct {
	Compacted bool
	Before    int
	After     int
	Removed   int
}

type BudgetError struct {
	Estimated int
	Budget    int
	Fixed     int
	Protected int
	Cause     error
}

func (e *BudgetError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("模型拒绝上下文长度，本地估算 %d tokens、配置预算 %d；固定内容约 %d，受保护内容约 %d。"+
			"自动压缩后仍无法继续，请编辑、清理或重置上下文后恢复活动。原始错误：%v",
			e.Estimated, e.Budget, e.Fixed, e.Protected, e.Cause)
	}
	return fmt.Sprintf("上下文估算 %d tokens 超出预算 %d；固定提示/状态/工具约 %d，含当前输入与未读记录的受保护内容约 %d。"+
		"已尝试回收过期状态与压缩旧记录；请在上下文页编辑、清理或重置后恢复活动。", e.Estimated, e.Budget, e.Fixed, e.Protected)
}

func (e *BudgetError) Unwrap() error { return e.Cause }

func (c *Conversation) BudgetParts(defs []model.ToolDef) (fixed, protected int) {
	var prompts, pinned []model.ChatMessage
	for i, message := range c.Messages {
		isPrompt := message.Wire.Role == "system" || message.Wire.Role == "developer"
		if isPrompt {
			prompts = append(prompts, message.Wire)
		}
		if isPrompt || message.Kind == agentcontext.KindActiveInput ||
			(i >= c.UnreadStart && message.Kind != agentcontext.KindState) {
			pinned = append(pinned, message.Wire)
		}
	}
	if c.State != "" {
		state := model.ChatMessage{Role: "user", Content: c.State}
		prompts = append(prompts, state)
		pinned = append(pinned, state)
	}
	return agentcontext.EstimatedInputTokens(prompts, defs), agentcontext.EstimatedInputTokens(pinned, defs)
}

// PrepareContext 保护提示词、当前输入和未读结果；超限时逐步放宽近期窗口。
// summarize 由组装层提供，模型路由、用量和失败降级不属于会话状态。
func (c *Conversation) PrepareContext(policy config.AgentContext, defs []model.ToolDef, summarize func([]model.ChatMessage) string) (CompactionResult, error) {
	return c.prepareContext(policy, defs, summarize, false)
}

// CompactContext 由管理员显式触发，忽略日常复查阈值，仍保护当前输入和未读回执。
func (c *Conversation) CompactContext(policy config.AgentContext, defs []model.ToolDef, summarize func([]model.ChatMessage) string) (CompactionResult, error) {
	return c.prepareContext(policy, defs, summarize, true)
}

func (c *Conversation) prepareContext(policy config.AgentContext, defs []model.ToolDef, summarize func([]model.ChatMessage) string, force bool) (CompactionResult, error) {
	policy = policy.WithDefaults()
	estimate := func(messages []agentcontext.Message) int {
		return agentcontext.EstimatedInputTokens(agentcontext.ToWireMessages(messages), defs)
	}
	size := estimate(c.Messages)
	result := CompactionResult{Before: size, After: size}
	if size > policy.MaxInputTokens || force {
		latest := -1
		for i, message := range c.Messages {
			if message.Kind == agentcontext.KindState {
				latest = i
			}
		}
		messages := make([]agentcontext.Message, 0, len(c.Messages))
		unread := 0
		for i, message := range c.Messages {
			if message.Kind == agentcontext.KindState && i != latest {
				result.Removed++
				continue
			}
			messages = append(messages, message)
			if i < c.UnreadStart {
				unread++
			}
		}
		if result.Removed > 0 {
			c.Messages, c.UnreadStart = messages, unread
			result.Compacted = true
			c.Compactions++
			size = estimate(c.Messages)
		}
	}
	head := 0
	for head < len(c.Messages) && c.Messages[head].Wire.Role == "system" {
		head++
	}
	fixed, protected := c.BudgetParts(defs)
	available := policy.MaxInputTokens - fixed
	softLimit := fixed + available*3/4
	review := available > 0 && (force || size >= softLimit ||
		(c.Wakes >= policy.ReviewAfterWakes && size > fixed+available/2))
	if review {
		maxCut := len(c.Messages)
		if c.UnreadStart < maxCut {
			maxCut = c.UnreadStart
		}
		cut := len(c.Messages) - policy.KeepRecentMessages
		if cut < head {
			cut = head
		}
		if cut > maxCut {
			cut = maxCut
		}
		cut = agentcontext.SafeCut(c.Messages, cut)
		messages, unreadStart := c.WithSummary(head, cut, "")
		projected := estimate(messages) + policy.SummaryTokens
		minSavings := available / 4
		if minSavings < 256 {
			minSavings = 256
		}
		if size > policy.MaxInputTokens || force {
			minSavings = 1
			// 先尝试原窗口，确实无法容纳时才向后寻找下一个完整批次边界。
			limit := maxCut
			if limit >= len(c.Messages) {
				limit = len(c.Messages) - 1
			}
			for candidate := cut + 1; candidate <= limit &&
				(projected > policy.MaxInputTokens || size-projected < minSavings || cut == head); candidate++ {
				safe := agentcontext.SafeCut(c.Messages, candidate)
				if safe <= cut {
					continue
				}
				cut = safe
				messages, unreadStart = c.WithSummary(head, cut, "")
				projected = estimate(messages) + policy.SummaryTokens
			}
		}
		var prefix []model.ChatMessage
		newWork := false
		for i := head; i < cut; i++ {
			message := c.Messages[i]
			if CompactableWork(message) {
				prefix = append(prefix, message.Wire)
				newWork = newWork || message.Kind != agentcontext.KindSummary
			}
		}
		if cut > head && (newWork || force) && projected <= policy.MaxInputTokens && size-projected >= minSavings {
			messages[head].Wire.Content = SummaryBlock(summarize(prefix))
			c.UnreadStart = unreadStart
			c.Messages, c.Wakes = messages, 0
			c.Compactions++
			result.Compacted = true
			result.Removed += len(prefix)
		}
	}
	result.After = estimate(c.Messages)
	if result.After > policy.MaxInputTokens {
		return result, &BudgetError{Estimated: result.After, Budget: policy.MaxInputTokens, Fixed: fixed, Protected: protected}
	}
	return result, nil
}
