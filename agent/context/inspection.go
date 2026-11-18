package context

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"Q-Mi/agent/model"
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
	FixedTokens     int    `json:"fixed_tokens"`
	ProtectedTokens int    `json:"protected_tokens"`
}

type InspectionEntry struct {
	Index                  int              `json:"index"`
	Role                   string           `json:"role"`
	Kind                   string           `json:"kind"`
	ChatType               string           `json:"chat_type,omitempty"`
	ChatID                 string           `json:"chat_id,omitempty"`
	MessageID              string           `json:"message_id,omitempty"`
	UserID                 string           `json:"user_id,omitempty"`
	Origin                 string           `json:"origin,omitempty"`
	Text                   string           `json:"text"`
	Reasoning              string           `json:"reasoning,omitempty"`
	ToolCalls              []model.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID             string           `json:"tool_call_id,omitempty"`
	Images                 int              `json:"images"`
	EstimatedTokens        int              `json:"estimated_tokens"`
	OriginalRunes          int              `json:"original_runes"`
	Truncated              bool             `json:"truncated"`
	ReasoningOriginalRunes int              `json:"reasoning_original_runes"`
	ReasoningTruncated     bool             `json:"reasoning_truncated"`
	ArgumentsOriginalRunes []int            `json:"arguments_original_runes,omitempty"`
	ArgumentsTruncated     []bool           `json:"arguments_truncated,omitempty"`
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

func BoundInspection(text string, max int) (string, int, bool) {
	n := utf8.RuneCountInString(text)
	if n <= max {
		return text, n, false
	}
	return string([]rune(text)[:max]), n, true
}

// Redact structured data only; prose is not guaranteed secret-free.
func RedactInspection(text string) string {
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

func InspectionMetadata(e *InspectionEntry, text string) {
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

func InspectionCopy(e InspectionEntry, limit int) InspectionEntry {
	e.ToolCalls = append([]model.ToolCall(nil), e.ToolCalls...)
	e.ArgumentsOriginalRunes = append([]int(nil), e.ArgumentsOriginalRunes...)
	e.ArgumentsTruncated = append([]bool(nil), e.ArgumentsTruncated...)
	var truncated bool
	e.Text, _, truncated = BoundInspection(e.Text, limit)
	e.Truncated = e.Truncated || truncated
	e.Reasoning, _, truncated = BoundInspection(e.Reasoning, limit)
	e.ReasoningTruncated = e.ReasoningTruncated || truncated
	for i := range e.ToolCalls {
		e.ToolCalls[i].Function.Arguments, _, truncated = BoundInspection(e.ToolCalls[i].Function.Arguments, limit)
		e.ArgumentsTruncated[i] = e.ArgumentsTruncated[i] || truncated
	}
	return e
}
