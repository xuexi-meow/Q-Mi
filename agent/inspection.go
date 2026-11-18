package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	agentcontext "Q-Mi/agent/context"
	agentruntime "Q-Mi/agent/runtime"
)

var (
	ErrInspectionVersion = agentcontext.ErrInspectionVersion
	ErrInspectionIndex   = agentcontext.ErrInspectionIndex
)

type ContextMetrics = agentcontext.ContextMetrics
type InspectionEntry = agentcontext.InspectionEntry
type ContextSnapshot = agentcontext.ContextSnapshot
type ContextQuery = agentcontext.ContextQuery
type ContextPage = agentcontext.ContextPage

type RuntimeInspection struct {
	Available      bool           `json:"available"`
	Phase          string         `json:"phase"`
	Round          int            `json:"round"`
	Candidate      string         `json:"candidate"`
	Model          string         `json:"model"`
	Tier           string         `json:"tier"`
	LastError      string         `json:"last_error"`
	StartedAt      time.Time      `json:"started_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	Pending        int            `json:"pending"`
	RequestVersion uint64         `json:"request_version"`
	WorkingVersion uint64         `json:"working_version"`
	LastUsage      Usage          `json:"last_usage"`
	Metrics        ContextMetrics `json:"metrics"`
	Sleep          *SleepSnapshot `json:"sleep,omitempty"`
}

// Worker-owned input is converted into bounded immutable data before publishing.
func (m *Manager) publishInspection(source string, messages []ChatMessage, defs []ToolDef) {
	m.publishContextInspection(source, agentcontext.FromWireMessages(messages), defs)
}

func (m *Manager) publishContextInspection(source string, messages []agentcontext.Message, defs []ToolDef) {
	wireMessages := agentcontext.ToWireMessages(messages)
	s := &ContextSnapshot{Source: source, UpdatedAt: time.Now(), Entries: make([]InspectionEntry, 0, len(messages)+len(defs))}
	s.Metrics = ContextMetrics{EstimatedTokens: estimatedInputTokens(wireMessages, defs),
		Budget: m.config().Context.WithDefaults().MaxInputTokens, MessageCount: len(wireMessages),
		ToolCount: len(defs), Compactions: m.conversation.Compactions}
	view := m.conversation
	view.Messages = messages
	s.Metrics.FixedTokens, s.Metrics.ProtectedTokens = view.BudgetParts(defs)
	for i, msg := range wireMessages {
		text, images := contentTextAndImages(msg.Content)
		e := InspectionEntry{Index: len(s.Entries), Role: msg.Role, Kind: "input",
			ToolCallID: msg.ToolCallID, Images: images, ToolCalls: append([]ToolCall(nil), msg.ToolCalls...),
			EstimatedTokens: estimatedInputTokens([]ChatMessage{msg}, nil) - 8}
		switch {
		case msg.Role == "system" || msg.Role == "developer":
			e.Kind = "prompt"
		case messages[i].Kind == agentcontext.KindState:
			e.Kind = "state"
		case messages[i].Kind == agentcontext.KindSummary:
			e.Kind = "summary"
		case msg.Role == "tool":
			e.Kind = "tool_result"
		case msg.Role == "assistant":
			e.Kind = "output"
		}
		agentcontext.InspectionMetadata(&e, text)
		e.Text, _, e.Truncated = agentcontext.BoundInspection(agentcontext.RedactInspection(text), 64000)
		e.OriginalRunes = utf8.RuneCountInString(text)
		e.Reasoning, _, e.ReasoningTruncated = agentcontext.BoundInspection(agentcontext.RedactInspection(msg.ReasoningContent), 64000)
		e.ReasoningOriginalRunes = utf8.RuneCountInString(msg.ReasoningContent)
		for i := range e.ToolCalls {
			arg := e.ToolCalls[i].Function.Arguments
			bounded, _, trunc := agentcontext.BoundInspection(agentcontext.RedactInspection(arg), 64000)
			e.ToolCalls[i].Function.Arguments = bounded
			e.ArgumentsOriginalRunes = append(e.ArgumentsOriginalRunes, utf8.RuneCountInString(arg))
			e.ArgumentsTruncated = append(e.ArgumentsTruncated, trunc)
		}
		s.Metrics.TextTokens += textTokens(text)
		s.Metrics.ReasoningTokens += textTokens(msg.ReasoningContent)
		s.Metrics.ImageTokens += images * 2048
		s.Entries = append(s.Entries, e)
	}
	for _, def := range defs {
		data, _ := json.Marshal(def)
		text, n, trunc := agentcontext.BoundInspection(agentcontext.RedactInspection(string(data)), 64000)
		s.Entries = append(s.Entries, InspectionEntry{Index: len(s.Entries), Role: "tool_definition",
			Kind: "tool_definition", Text: text, OriginalRunes: n, Truncated: trunc, EstimatedTokens: textTokens(string(data))})
	}
	s.Metrics.ToolTokens = s.Metrics.EstimatedTokens - s.Metrics.TextTokens - s.Metrics.ReasoningTokens - s.Metrics.ImageTokens
	m.inspectionMu.Lock()
	defer m.inspectionMu.Unlock()
	m.inspectionVersion++
	s.Version = m.inspectionVersion
	if source == "request" {
		m.inspectionRequest = s
		m.inspectionRuntime.RequestVersion = s.Version
	} else {
		m.inspectionWorking = s
		m.inspectionRuntime.WorkingVersion = s.Version
	}
	m.inspectionRuntime.Metrics = s.Metrics
	m.inspectionRuntime.UpdatedAt = s.UpdatedAt
}

func (m *Manager) setInspectionPhase(phase string, round int, candidate, model, tier string) {
	m.inspectionMu.Lock()
	defer m.inspectionMu.Unlock()
	r := &m.inspectionRuntime
	if phase == "preparing" && round == 0 {
		r.StartedAt = time.Now()
		r.LastError = ""
		r.Round = 0
	}
	r.Available, r.Phase, r.UpdatedAt = true, phase, time.Now()
	if round > 0 {
		r.Round = round
	}
	if candidate != "" {
		r.Candidate, r.Model, r.Tier = candidate, model, tier
	}
}

func (m *Manager) finishInspection(err error) {
	m.setInspectionPhase("idle", 0, "", "", "")
	if err != nil {
		m.inspectionMu.Lock()
		m.inspectionRuntime.Phase = "error"
		var budgetErr *agentruntime.BudgetError
		if errors.As(err, &budgetErr) {
			m.inspectionRuntime.Phase = "blocked"
		}
		m.inspectionRuntime.LastError, _, _ = agentcontext.BoundInspection(agentcontext.RedactInspection(err.Error()), 1000)
		m.inspectionMu.Unlock()
	}
}

func (m *Manager) InspectRuntime() RuntimeInspection {
	m.inspectionMu.RLock()
	r := m.inspectionRuntime
	m.inspectionMu.RUnlock()
	r.Available = true
	if r.Phase == "" {
		r.Phase = "idle"
	}
	m.mu.Lock()
	if m.inbox != nil {
		r.Pending = m.inbox.Len()
	}
	m.mu.Unlock()
	if m.selfID > 0 {
		if state, err := m.SleepStatus(); err == nil {
			state.Memories, state.Deferred, state.Signals = nil, nil, nil
			state.ObservedIDs = nil
			r.Sleep = &state
			if state.Sleeping {
				r.Phase = "sleeping"
			}
		} else {
			r.LastError = "读取睡眠状态失败：" + err.Error()
		}
	}
	return r
}

func (m *Manager) snapshot(source string) *ContextSnapshot {
	m.inspectionMu.RLock()
	defer m.inspectionMu.RUnlock()
	if source == "request" {
		return m.inspectionRequest
	}
	return m.inspectionWorking
}

func (m *Manager) InspectContext(q ContextQuery) (ContextPage, error) {
	if q.Source == "" {
		q.Source = "working"
	}
	if q.Source != "working" && q.Source != "request" {
		return ContextPage{}, fmt.Errorf("invalid source")
	}
	if q.Limit <= 0 {
		q.Limit = 40
	}
	if q.Limit > 100 {
		q.Limit = 100
	}
	if q.Offset < 0 {
		return ContextPage{}, fmt.Errorf("invalid offset")
	}
	page := ContextPage{ContextSnapshot: ContextSnapshot{Source: q.Source, Entries: []InspectionEntry{}}, Offset: q.Offset, Limit: q.Limit}
	s := m.snapshot(q.Source)
	if s == nil {
		return page, nil
	}
	page.Version, page.UpdatedAt, page.Metrics = s.Version, s.UpdatedAt, s.Metrics
	for _, e := range s.Entries {
		if q.Kind != "" && q.Kind != "all" && q.Kind != e.Kind {
			continue
		}
		if q.ChatType != "" && (q.ChatType != e.ChatType || q.ChatID != e.ChatID) {
			continue
		}
		data, _ := json.Marshal(e)
		if q.Query != "" && !strings.Contains(strings.ToLower(string(data)), strings.ToLower(q.Query)) {
			continue
		}
		page.Total++
		if page.Total > q.Offset && len(page.Entries) < q.Limit {
			page.Entries = append(page.Entries, agentcontext.InspectionCopy(e, 1000))
		}
	}
	return page, nil
}

func (m *Manager) InspectEntry(source string, version uint64, index int) (InspectionEntry, error) {
	s := m.snapshot(source)
	if s == nil || s.Version != version {
		return InspectionEntry{}, ErrInspectionVersion
	}
	if index < 0 || index >= len(s.Entries) {
		return InspectionEntry{}, ErrInspectionIndex
	}
	return agentcontext.InspectionCopy(s.Entries[index], 64000), nil
}

type TaskInspection struct {
	ID                  string     `json:"id"`
	Tool                string     `json:"tool"`
	ChatType            string     `json:"chat_type"`
	ChatID              string     `json:"chat_id"`
	Status              string     `json:"status"`
	StartedAt           time.Time  `json:"started_at"`
	DoneAt              *time.Time `json:"done_at,omitempty"`
	Args                string     `json:"args"`
	Result              string     `json:"result"`
	Error               string     `json:"error"`
	ArgsOriginalRunes   int        `json:"args_original_runes"`
	ResultOriginalRunes int        `json:"result_original_runes"`
	ErrorOriginalRunes  int        `json:"error_original_runes"`
	ArgsTruncated       bool       `json:"args_truncated"`
	ResultTruncated     bool       `json:"result_truncated"`
	ErrorTruncated      bool       `json:"error_truncated"`
}

func taskInspection(t BackgroundTask) TaskInspection {
	v := TaskInspection{ID: strconv.FormatInt(t.ID, 10), Tool: t.Tool, ChatType: t.ChatType,
		ChatID: strconv.FormatInt(t.ChatID, 10), Status: t.Status, StartedAt: t.StartedAt}
	if t.DoneAt != nil {
		done := *t.DoneAt
		v.DoneAt = &done
	}
	v.Args, _, v.ArgsTruncated = agentcontext.BoundInspection(agentcontext.RedactInspection(t.ArgsBrief), 1000)
	v.Result, _, v.ResultTruncated = agentcontext.BoundInspection(agentcontext.RedactInspection(t.Result), 64000)
	v.Error, _, v.ErrorTruncated = agentcontext.BoundInspection(agentcontext.RedactInspection(t.Err), 1000)
	v.ArgsOriginalRunes, v.ResultOriginalRunes, v.ErrorOriginalRunes = utf8.RuneCountInString(t.ArgsBrief), utf8.RuneCountInString(t.Result), utf8.RuneCountInString(t.Err)
	return v
}
