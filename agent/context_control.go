package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	agentcontext "Q-Mi/agent/context"
	agentruntime "Q-Mi/agent/runtime"
)

var ErrContextBusy = errors.New("Agent 正在活动，请等待本次活动结束后再修改上下文")

type ContextAction struct {
	Version        uint64  `json:"version"`
	Action         string  `json:"action"`
	Index          int     `json:"index"`
	Text           *string `json:"text,omitempty"`
	ClearReasoning bool    `json:"clear_reasoning,omitempty"`
	Preview        bool    `json:"preview"`
}

type ContextActionResult struct {
	Version  uint64 `json:"version"`
	Action   string `json:"action"`
	Affected []int  `json:"affected"`
	Before   int    `json:"before"`
	After    int    `json:"after"`
	Budget   int    `json:"budget"`
	Blocked  bool   `json:"blocked"`
	Preview  bool   `json:"preview"`
}

type ContextEditEntry struct {
	Version        uint64 `json:"version"`
	Index          int    `json:"index"`
	Text           string `json:"text"`
	Editable       bool   `json:"editable"`
	Deletable      bool   `json:"deletable"`
	ClearReasoning bool   `json:"clear_reasoning"`
	Reason         string `json:"reason,omitempty"`
	DeleteIndices  []int  `json:"delete_indices"`
}

func (m *Manager) rememberContextTurn(turn *Turn) {
	if turn == nil {
		return
	}
	m.contextTurn = &Turn{SelfID: turn.SelfID, ChatType: turn.ChatType, ChatID: turn.ChatID, Wake: turn.Wake, Now: turn.Now}
	for _, message := range turn.NewMessages {
		m.contextTurn.NewMessages = append(m.contextTurn.NewMessages, TurnMessage{
			SenderID: message.SenderID, SenderName: message.SenderName, Wake: message.Wake,
		})
	}
}

func (m *Manager) checkContextVersion(version uint64) error {
	snapshot := m.snapshot("working")
	if snapshot == nil || version == 0 || snapshot.Version != version {
		return ErrInspectionVersion
	}
	return nil
}

func (m *Manager) EditableContextEntry(version uint64, index int) (ContextEditEntry, error) {
	if !m.runMu.TryLock() {
		return ContextEditEntry{}, ErrContextBusy
	}
	defer m.runMu.Unlock()
	if err := m.checkContextVersion(version); err != nil {
		return ContextEditEntry{}, err
	}
	if index < 0 || index >= len(m.conversation.Messages) {
		return ContextEditEntry{}, ErrInspectionIndex
	}
	message := m.conversation.Messages[index]
	text, editable := agentruntime.EditableText(message)
	_, indices, deleteErr := m.conversation.Change(agentruntime.ContextChange{Action: "delete", Index: index})
	entry := ContextEditEntry{Version: version, Index: index, Text: text, Editable: editable,
		Deletable: deleteErr == nil, DeleteIndices: indices,
		ClearReasoning: editable && message.Wire.ReasoningContent != ""}
	if !editable {
		entry.Reason = "提示词和状态请在 Agent 管理或配置中修改；复杂多段正文保持只读"
	}
	return entry, nil
}

func (m *Manager) currentContextDefs() []ToolDef {
	turn := m.contextTurn
	if turn == nil {
		turn = &Turn{SelfID: m.selfID}
	}
	ctx := &ToolContext{Store: m.store, Turn: turn, Timer: m.timerStore, Images: m.images,
		Tasks: m.tasks, Approvals: m.approvals, Browser: m.browser, Events: m.events,
		Workspace: m.config().WorkspaceDir, NotesPath: m.notesPath, StatePath: m.statePath, Admin: batchIsAdmin(turn)}
	return toolDefs(m.toolRegistry().Build(ctx))
}

func changedContextIndices(before, after []agentcontext.Message) []int {
	indices := []int{}
	cursor := 0
	for i, message := range before {
		found := -1
		for j := cursor; j < len(after); j++ {
			if reflect.DeepEqual(message, after[j]) {
				found = j
				break
			}
		}
		if found < 0 {
			indices = append(indices, i)
		} else {
			cursor = found + 1
		}
	}
	return indices
}

// ControlContext 只在活动空闲时提交，读取、预览与提交共用真实会话和同一版本。
func (m *Manager) ControlContext(ctx context.Context, action ContextAction) (ContextActionResult, error) {
	if !m.runMu.TryLock() {
		return ContextActionResult{}, ErrContextBusy
	}
	unlock := true
	defer func() {
		if unlock {
			m.runMu.Unlock()
		}
	}()
	if err := m.checkContextVersion(action.Version); err != nil {
		return ContextActionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ContextActionResult{}, err
	}
	previousPhase := m.InspectRuntime().Phase
	published := false
	defer func() {
		if action.Action == "compact" && !action.Preview && !published {
			m.setInspectionPhase(previousPhase, 0, "", "", "")
		}
	}()
	if action.Text != nil && (!utf8.ValidString(*action.Text) || utf8.RuneCountInString(*action.Text) > 512000) {
		return ContextActionResult{}, errors.New("正文必须是有效文本且不超过 512000 字符")
	}
	defs, policy := m.currentContextDefs(), m.config().Context.WithDefaults()
	result := ContextActionResult{Version: action.Version, Action: action.Action, Affected: []int{},
		Before: estimatedInputTokens(agentcontext.ToWireMessages(m.conversation.Messages), defs),
		Budget: policy.MaxInputTokens, Preview: action.Preview}
	next := m.conversation
	var err error
	var budgetErr *agentruntime.BudgetError
	switch action.Action {
	case "edit", "delete", "clean", "reset":
		next, result.Affected, err = m.conversation.Change(agentruntime.ContextChange{
			Action: action.Action, Index: action.Index, Text: action.Text, ClearReasoning: action.ClearReasoning})
	case "compact":
		summarize := func(prefix []ChatMessage) string {
			// 预览按摘要输出预算估算，不调用模型、不改变检查状态。
			if action.Preview {
				return strings.Repeat("s", policy.SummaryTokens*3)
			}
			turn := m.contextTurn
			if turn == nil {
				turn = &Turn{SelfID: m.selfID, Now: time.Now()}
			}
			return m.summarizeContext(ctx, turn, prefix)
		}
		_, err = next.CompactContext(policy, defs, summarize)
		if errors.As(err, &budgetErr) {
			err = nil // 清理仍不足时保留实际进展，并返回明确阻塞状态。
		}
		result.Affected = changedContextIndices(m.conversation.Messages, next.Messages)
	case "resume":
		if state, err := m.SleepStatus(); err != nil {
			return result, err
		} else if state.Sleeping {
			return result, errors.New("正在休息，请先从 Agent 管理唤醒")
		}
		if m.contextTurn == nil {
			return result, errors.New("尚无可恢复的活动，请先从 Agent 管理发起唤醒")
		}
	default:
		return result, errors.New("不支持的上下文操作")
	}
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if result.Affected == nil {
		result.Affected = []int{}
	}
	result.After = estimatedInputTokens(agentcontext.ToWireMessages(next.Messages), defs)
	result.Blocked = result.After > policy.MaxInputTokens
	if action.Preview {
		return result, nil
	}
	if action.Action == "resume" && result.Blocked {
		fixed, protected := next.BudgetParts(defs)
		return result, &agentruntime.BudgetError{Estimated: result.After, Budget: policy.MaxInputTokens, Fixed: fixed, Protected: protected}
	}
	if action.Action == "resume" {
		turn := m.contextTurn
		m.setInspectionPhase("preparing", 0, "", "", "")
		unlock = false
		go func() {
			defer m.runMu.Unlock()
			m.runActivity(turn, false)
		}()
		return result, nil
	}
	m.conversation = next
	m.publishContextInspection("working", next.Messages, defs)
	published = true
	if result.Blocked {
		fixed, protected := next.BudgetParts(defs)
		m.finishInspection(&agentruntime.BudgetError{Estimated: result.After, Budget: policy.MaxInputTokens, Fixed: fixed, Protected: protected})
	} else {
		m.finishInspection(nil)
		m.inspectionMu.Lock()
		m.inspectionRuntime.LastError = ""
		m.inspectionMu.Unlock()
	}
	result.Version = m.InspectRuntime().WorkingVersion
	m.events.Record("", 0, KindConfig, "WebUI 上下文操作: "+action.Action)
	return result, nil
}
