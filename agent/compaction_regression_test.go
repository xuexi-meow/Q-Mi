package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"Q-Mi/utils/config"
)

func TestCompactionUnreadResultsReachModelWithTriggerAndSendReceipt(t *testing.T) {
	m, f, _ := newLiveManager(t, nil)
	m.cfg.Context = config.AgentContext{MaxInputTokens: 8000, SummaryTokens: 256, KeepRecentMessages: 4}
	m.conversation.messages = []ChatMessage{
		{Role: "system", Content: "initial"},
		{Role: "user", Content: strings.Repeat("old record ", 1300)},
	}
	m.conversation.unreadStart = 2
	m.registry = NewRegistry(module{name: "test", tools: func(*ToolContext) []Tool {
		return []Tool{
			{Name: "send_message", Parameters: objectParams(map[string]any{"text": strProp("text")}, []string{"text"}), Run: func(json.RawMessage) (string, error) {
				return `{"message_id":42}`, nil
			}},
			{Name: "get_recent_messages", Parameters: objectParams(nil, nil), Run: func(json.RawMessage) (string, error) {
				return "fresh-history-evidence " + strings.Repeat("history ", 2300), nil
			}},
		}
	}})
	actions, summaries := 0, 0
	f.respond = func(req ChatRequest) string {
		if len(req.Tools) == 0 {
			summaries++
			return `{"choices":[{"index":0,"finish_reason":"length","message":{"role":"assistant","content":null,"reasoning_content":"not a summary"}}],"usage":{"completion_tokens":1024}}`
		}
		actions++
		switch actions {
		case 1:
			return toolCallResp("sent", "send_message", `{"text":"already-sent-once"}`)
		case 2:
			return toolCallResp("history", "get_recent_messages", "{}")
		default:
			return toolCallResp("pause", "finish", "{}")
		}
	}
	m.handleTurn(&Turn{SelfID: 999, Now: time.Now(),
		NewMessages: []TurnMessage{{Text: "current-sticker-trigger"}}})
	var actionRequests []ChatRequest
	for _, req := range requestsSnapshot(f) {
		if len(req.Tools) > 0 {
			actionRequests = append(actionRequests, req)
		}
	}
	if len(actionRequests) != 3 {
		t.Fatalf("want three action requests, got %d (summaries=%d)", len(actionRequests), summaries)
	}
	text := requestText(actionRequests[2])
	for _, want := range []string{"current-sticker-trigger", "fresh-history-evidence", `{"message_id":42}`} {
		if !strings.Contains(text, want) {
			t.Errorf("next action request lost %q after compaction", want)
		}
	}
	requireToolPairs(t, actionRequests[2].Messages)
	receipts := map[string]bool{}
	for _, message := range actionRequests[2].Messages {
		if message.Role == "tool" {
			receipts[message.ToolCallID] = true
		}
	}
	if !receipts["sent"] || !receipts["history"] {
		t.Errorf("recent tool receipts must survive verbatim, got %v", receipts)
	}
	if summaries != 1 {
		t.Errorf("want one useful compaction, not repeated summaries: got %d", summaries)
	}
}

func TestCompactionNeverSummarizesPromptsOrLearnedState(t *testing.T) {
	m, f, cfg := newLiveManager(t, nil)
	store := openTestStore(t)
	now := time.Now()
	for i := 0; i < 8; i++ {
		putRecord(t, store, int64(i+1), "recv", "group", 888, 111, "person",
			strings.Repeat("old-chat ", 240), now.Add(-time.Hour).Unix()+int64(i))
	}
	builder := NewDefaultBuilder(store, cfg)
	builder.SetToolDocs("tool-documentation-must-stay-verbatim")
	m.builder = builder
	m.registry = NewRegistry(module{name: "test", tools: func(*ToolContext) []Tool {
		return []Tool{{Name: "unused", Description: "schema-must-stay-verbatim", Parameters: objectParams(nil, nil)}}
	}})
	if err := AppendNote(m.notesPath, "private-note-not-summary-input"); err != nil {
		t.Fatal(err)
	}
	if err := writeState(m.statePath, "mood-not-summary-input"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.WorkspaceDir, "chat_style.md"), []byte("learned-style-not-summary-input"), 0600); err != nil {
		t.Fatal(err)
	}
	f.respond = func(req ChatRequest) string {
		if len(req.Tools) == 0 {
			return plainResp("old conversation summarized")
		}
		return toolCallResp("pause", "finish", "{}")
	}
	m.handleTurn(&Turn{SelfID: 999, Now: now, NewMessages: []TurnMessage{{Text: "previous-event"}}})
	m.cfg.Context = config.AgentContext{MaxInputTokens: 6000, SummaryTokens: 256, KeepRecentMessages: 2}
	m.handleTurn(&Turn{SelfID: 999, Now: now, NewMessages: []TurnMessage{{Text: "live-event"}}})
	requests := requestsSnapshot(f)
	if len(requests) != 3 {
		t.Fatalf("want initial action, summary and final action request, got %d", len(requests))
	}
	for _, value := range []string{
		"original-persona", "original-guide", "tool-documentation-must-stay-verbatim",
		"schema-must-stay-verbatim", "private-note-not-summary-input",
		"mood-not-summary-input", "learned-style-not-summary-input",
	} {
		if strings.Contains(requestText(requests[1]), value) {
			t.Errorf("non-work prompt %q entered lossy summary input", value)
		}
		if !strings.Contains(requestText(requests[2])+requests[2].Tools[0].Function.Description, value) {
			t.Errorf("action request lost verbatim prompt %q", value)
		}
	}
	if got := contentText(requests[2].Messages[len(requests[2].Messages)-1]); !strings.Contains(got, "live-event") {
		t.Fatalf("state snapshot obscured the trigger as the last user message: %s", got)
	}
}

func TestCompactionSkipsSummaryWhenProtectedTailCannotFit(t *testing.T) {
	m, f := seedCompaction(t, plainResp("not worth requesting"))
	m.cfg.Context.KeepRecentMessages = 2
	m.conversation.messages = append(m.conversation.messages,
		ChatMessage{Role: "assistant", ToolCalls: []ToolCall{{ID: "unseen", Type: "function",
			Function: ToolFunction{Name: "read_file", Arguments: "{}"}}}},
		ChatMessage{Role: "tool", ToolCallID: "unseen", Content: strings.Repeat("unread evidence ", 1400)})
	m.conversation.unreadStart = len(m.conversation.messages) - 2
	before := append([]ChatMessage(nil), m.conversation.messages...)
	err := m.prepareContext(context.Background(), &Turn{SelfID: 999}, nil)
	if err == nil {
		t.Fatal("oversized protected result must report budget error")
	}
	if got := f.count(); got != 0 {
		t.Errorf("summary cannot make protected tail fit; wasted %d requests", got)
	}
	if !reflect.DeepEqual(before, m.conversation.messages) {
		t.Fatal("an unsuccessful budget check destructively rewrote the working context")
	}
}

func TestCompactionDoesNotOverrideRecentWindowToMeetBudget(t *testing.T) {
	m, f, _ := newLiveManager(t, []string{plainResp("must not be requested")})
	m.cfg.Context = config.AgentContext{MaxInputTokens: 5000, SummaryTokens: 256, KeepRecentMessages: 12}
	m.conversation.messages = []ChatMessage{
		{Role: "system", Content: "original system"},
		{Role: "user", Content: strings.Repeat("recent-evidence ", 1500)},
		{Role: "user", Content: "recent-event"},
	}
	m.conversation.unreadStart = 3
	before := append([]ChatMessage(nil), m.conversation.messages...)
	if err := m.prepareContext(context.Background(), &Turn{SelfID: 999}, nil); err == nil {
		t.Fatal("cannot fit without deleting the configured recent window; want a budget error")
	}
	if f.count() != 0 || !reflect.DeepEqual(before, m.conversation.messages) {
		t.Fatal("recent window was sacrificed to fit the budget")
	}
}

func TestSummaryFailureDiagnosesCauseWithoutExposingReasoning(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response string
		want     string
	}{
		{"exhausted", `{"choices":[{"index":0,"finish_reason":"length","message":{"role":"assistant","content":null,"reasoning_content":"secret reasoning"}}],"usage":{"completion_tokens":1024}}`, "length"},
		{"partial", `{"choices":[{"index":0,"finish_reason":"length","message":{"role":"assistant","content":"incomplete summary"}}],"usage":{"completion_tokens":1024}}`, "length"},
		{"empty", `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":null}}]}`, "empty_content"},
		{"tool", toolCallResp("bad", "send_message", "{}"), "unexpected_tool_calls"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, f, _ := newLiveManager(t, []string{tc.response})
			summary := m.summarizeContext(context.Background(), &Turn{SelfID: 999},
				[]ChatMessage{{Role: "user", Content: "archived fact"}})
			if !strings.Contains(summary, "摘要降级") || strings.Contains(summary, "secret reasoning") {
				t.Fatalf("unsafe or missing fallback: %s", summary)
			}
			var diagnostics []string
			for _, event := range m.events.After(0) {
				if event.Kind == KindError {
					diagnostics = append(diagnostics, event.Text)
				}
			}
			if !strings.Contains(strings.Join(diagnostics, "\n"), tc.want) {
				t.Errorf("missing specific %q diagnostic: %v", tc.want, diagnostics)
			}
			if f.count() != 1 {
				t.Errorf("failed summary must not automatically retry: requests=%d", f.count())
			}
		})
	}
}
