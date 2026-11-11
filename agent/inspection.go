package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInspectionVersion = errors.New("上下文版本已更新，请刷新列表")
	ErrInspectionIndex   = errors.New("上下文条目不存在")
)

type ContextMetrics struct {
	EstimatedTokens int    `json:"estimated_tokens"`
	Budget          int    `json:"budget"`
	MessageCount    int    `json:"message_count"`
	ToolCount       int    `json:"tool_count"`
	Compactions     uint64 `json:"compactions"`
	TextTokens      int    `json:"text_tokens"`
	ReasoningTokens int    `json:"reasoning_tokens"`
	ImageTokens     int    `json:"image_tokens"`
	ToolTokens      int    `json:"tool_tokens"`
}

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
}

type InspectionEntry struct {
	Index                  int        `json:"index"`
	Role                   string     `json:"role"`
	Kind                   string     `json:"kind"`
	ChatType               string     `json:"chat_type,omitempty"`
	ChatID                 string     `json:"chat_id,omitempty"`
	MessageID              string     `json:"message_id,omitempty"`
	UserID                 string     `json:"user_id,omitempty"`
	Origin                 string     `json:"origin,omitempty"`
	Text                   string     `json:"text"`
	Reasoning              string     `json:"reasoning,omitempty"`
	ToolCalls              []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID             string     `json:"tool_call_id,omitempty"`
	Images                 int        `json:"images"`
	EstimatedTokens        int        `json:"estimated_tokens"`
	OriginalRunes          int        `json:"original_runes"`
	Truncated              bool       `json:"truncated"`
	ReasoningOriginalRunes int        `json:"reasoning_original_runes"`
	ReasoningTruncated     bool       `json:"reasoning_truncated"`
	ArgumentsOriginalRunes []int      `json:"arguments_original_runes,omitempty"`
	ArgumentsTruncated     []bool     `json:"arguments_truncated,omitempty"`
}

type ContextSnapshot struct {
	Version   uint64            `json:"version"`
	Source    string            `json:"source"`
	UpdatedAt time.Time         `json:"updated_at"`
	Metrics   ContextMetrics    `json:"metrics"`
	Entries   []InspectionEntry `json:"entries"`
}

type ContextQuery struct {
	Source, Kind, ChatType, ChatID, Query string
	Offset, Limit                         int
}

type ContextPage struct {
	ContextSnapshot
	Total  int `json:"total"`
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

func boundInspection(text string, max int) (string, int, bool) {
	n := utf8.RuneCountInString(text)
	if n <= max {
		return text, n, false
	}
	return string([]rune(text)[:max]), n, true
}

// Redact structured data only; prose is not guaranteed secret-free.
func redactInspection(text string) string {
	var v any
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	if d.Decode(&v) != nil || !json.Valid([]byte(text)) {
		return text
	}
	var walk func(any, int) any
	walk = func(v any, depth int) any {
		if depth > 32 {
			return "[depth limit]"
		}
		switch x := v.(type) {
		case map[string]any:
			for key, val := range x {
				switch strings.ToLower(key) {
				case "api_key", "token", "authorization", "password", "secret":
					x[key] = "[redacted]"
				case "image_url":
					x[key] = "[image omitted]"
				default:
					x[key] = walk(val, depth+1)
				}
			}
		case []any:
			for i := range x {
				x[i] = walk(x[i], depth+1)
			}
		case string:
			if json.Valid([]byte(x)) {
				var nested any
				decoder := json.NewDecoder(strings.NewReader(x))
				decoder.UseNumber()
				if decoder.Decode(&nested) == nil {
					if _, scalar := nested.(string); !scalar {
						data, _ := json.Marshal(walk(nested, depth+1))
						return string(data)
					}
				}
			}
		}
		return v
	}
	data, err := json.Marshal(walk(v, 0))
	if err != nil {
		return "[unavailable]"
	}
	return string(data)
}

func inspectionMetadata(e *InspectionEntry, text string) {
	candidate := strings.TrimSpace(text)
	if !json.Valid([]byte(candidate)) {
		// Only known builder wrappers may precede an actual JSON record.
		if !strings.HasPrefix(candidate, "当前时间: ") && !strings.HasPrefix(candidate, "当前情绪状态: ") &&
			!strings.HasPrefix(candidate, "[思考笔记]") && !strings.HasPrefix(candidate, "[聊天习惯]") {
			return
		}
		lines := strings.Split(candidate, "\n")
		candidate = lines[len(lines)-1]
	}
	var row map[string]json.RawMessage
	if json.Unmarshal([]byte(candidate), &row) != nil {
		return
	}
	kind := segmentString(row["kind"])
	if kind != "qq_message" && kind != "background_event" {
		return
	}
	e.ChatType = segmentString(row["chat_type"])
	if e.ChatType != "group" && e.ChatType != "private" {
		e.ChatType = ""
		return
	}
	e.ChatID = segmentString(row["chat_id"])
	e.MessageID = segmentString(row["message_id"])
	e.UserID = segmentString(row["user_id"])
	e.Origin = segmentString(row["origin"])
}

func inspectionCopy(e InspectionEntry, limit int) InspectionEntry {
	e.ToolCalls = append([]ToolCall(nil), e.ToolCalls...)
	e.ArgumentsOriginalRunes = append([]int(nil), e.ArgumentsOriginalRunes...)
	e.ArgumentsTruncated = append([]bool(nil), e.ArgumentsTruncated...)
	var truncated bool
	e.Text, _, truncated = boundInspection(e.Text, limit)
	e.Truncated = e.Truncated || truncated
	e.Reasoning, _, truncated = boundInspection(e.Reasoning, limit)
	e.ReasoningTruncated = e.ReasoningTruncated || truncated
	for i := range e.ToolCalls {
		e.ToolCalls[i].Function.Arguments, _, truncated = boundInspection(e.ToolCalls[i].Function.Arguments, limit)
		e.ArgumentsTruncated[i] = e.ArgumentsTruncated[i] || truncated
	}
	return e
}

// Worker-owned input is converted into bounded immutable data before publishing.
func (m *Manager) publishInspection(source string, messages []ChatMessage, defs []ToolDef) {
	s := &ContextSnapshot{Source: source, UpdatedAt: time.Now(), Entries: make([]InspectionEntry, 0, len(messages)+len(defs))}
	s.Metrics = ContextMetrics{EstimatedTokens: estimatedInputTokens(messages, defs),
		Budget: m.config().Context.WithDefaults().MaxInputTokens, MessageCount: len(messages),
		ToolCount: len(defs), Compactions: m.conversation.compactions}
	for _, msg := range messages {
		text, images := contentTextAndImages(msg.Content)
		e := InspectionEntry{Index: len(s.Entries), Role: msg.Role, Kind: "input",
			ToolCallID: msg.ToolCallID, Images: images, ToolCalls: append([]ToolCall(nil), msg.ToolCalls...),
			EstimatedTokens: estimatedInputTokens([]ChatMessage{msg}, nil) - 8}
		switch {
		case msg.Role == "system" || msg.Role == "developer":
			e.Kind = "prompt"
		case msg.kind == contextState:
			e.Kind = "state"
		case msg.kind == contextSummary:
			e.Kind = "summary"
		case msg.Role == "tool":
			e.Kind = "tool_result"
		case msg.Role == "assistant":
			e.Kind = "output"
		}
		inspectionMetadata(&e, text)
		e.Text, _, e.Truncated = boundInspection(redactInspection(text), 64000)
		e.OriginalRunes = utf8.RuneCountInString(text)
		e.Reasoning, _, e.ReasoningTruncated = boundInspection(redactInspection(msg.ReasoningContent), 64000)
		e.ReasoningOriginalRunes = utf8.RuneCountInString(msg.ReasoningContent)
		for i := range e.ToolCalls {
			arg := e.ToolCalls[i].Function.Arguments
			bounded, _, trunc := boundInspection(redactInspection(arg), 64000)
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
		text, n, trunc := boundInspection(redactInspection(string(data)), 64000)
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
		m.inspectionRuntime.LastError, _, _ = boundInspection(redactInspection(err.Error()), 1000)
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
		r.Pending = len(m.inbox.pending)
	}
	m.mu.Unlock()
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
			page.Entries = append(page.Entries, inspectionCopy(e, 1000))
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
	return inspectionCopy(s.Entries[index], 64000), nil
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
	v.Args, _, v.ArgsTruncated = boundInspection(redactInspection(t.ArgsBrief), 1000)
	v.Result, _, v.ResultTruncated = boundInspection(redactInspection(t.Result), 64000)
	v.Error, _, v.ErrorTruncated = boundInspection(redactInspection(t.Err), 1000)
	v.ArgsOriginalRunes, v.ResultOriginalRunes, v.ErrorOriginalRunes = utf8.RuneCountInString(t.ArgsBrief), utf8.RuneCountInString(t.Result), utf8.RuneCountInString(t.Err)
	return v
}
