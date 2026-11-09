package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"Q-Mi/utils/config"
)

func TestCompactionCanCompressReadWorkWithinOneWake(t *testing.T) {
	m, f, _ := newLiveManager(t, nil)
	m.cfg.Context = config.AgentContext{MaxInputTokens: 5000, SummaryTokens: 256, KeepRecentMessages: 2}
	m.registry = NewRegistry(module{name: "large", tools: func(*ToolContext) []Tool {
		return []Tool{
			{Name: "large_result", Parameters: objectParams(nil, nil),
				Run: func(json.RawMessage) (string, error) { return "large-evidence " + strings.Repeat("data ", 1800), nil }},
			{Name: "checkpoint", Parameters: objectParams(nil, nil),
				Run: func(json.RawMessage) (string, error) {
					return "fresh-checkpoint " + strings.Repeat("progress ", 230), nil
				}},
		}
	}})
	actions := 0
	f.respond = func(req ChatRequest) string {
		if len(req.Tools) == 0 {
			return plainResp("Read large_result; evidence retained in the research file.")
		}
		actions++
		switch actions {
		case 1:
			return toolCallResp("large", "large_result", "{}")
		case 2:
			if !strings.Contains(requestText(req), "large-evidence") {
				t.Error("large result was compressed before the model could read it")
			}
			return toolCallResp("progress", "checkpoint", "{}")
		default:
			text := requestText(req)
			if !strings.Contains(text, "Read large_result") || !strings.Contains(text, "fresh-checkpoint") ||
				!strings.Contains(text, "continue research") || strings.Contains(text, "large-evidence") {
				t.Error("expected summary of read work, verbatim checkpoint and active trigger")
			}
			return toolCallResp("done", "finish", "{}")
		}
	}
	m.handleTurn(&Turn{SelfID: 999, Now: time.Now(), NewMessages: []TurnMessage{{Text: "continue research"}}})
	if got := f.count(); got != 4 || actions != 3 {
		t.Fatalf("read work should compact within the wake: requests=%d actions=%d, want 4/3", got, actions)
	}
}

func TestCompactionPreservesCurrentStateOutsideSummary(t *testing.T) {
	m, _ := seedCompaction(t, plainResp("old material summarized"))
	m.conversation.state = "[当前状态快照]\ncurrent-note-must-remain\n[/当前状态快照]"
	m.conversation.messages[5] = ChatMessage{Role: "user", Content: m.conversation.state}
	m.conversation.unreadStart = len(m.conversation.messages)
	if err := m.prepareContext(context.Background(), &Turn{SelfID: 999}, nil); err != nil {
		t.Fatal(err)
	}
	text := requestText(ChatRequest{Messages: m.conversation.messages})
	if !strings.Contains(text, "current-note-must-remain") {
		t.Fatal("compaction delegated the current live state to a lossy summary")
	}
}

func seedCompaction(t *testing.T, summaryResponse string) (*Manager, *fakeLLM) {
	t.Helper()
	m, f, _ := newLiveManager(t, []string{
		summaryResponse, toolCallResp("done", "finish", "{}"),
	})
	m.registry = NewRegistry()
	m.cfg.Context = config.AgentContext{
		MaxInputTokens: 5000, SummaryTokens: 256, KeepRecentMessages: 3,
	}
	m.conversation.messages = []ChatMessage{
		{Role: "system", Content: "initial"},
		{Role: "user", Content: "pending-goal " + strings.Repeat("archived-data ", 1500)},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "old1", Type: "function", Function: ToolFunction{Name: "read_file", Arguments: `{"path":"research.md"}`}},
			{ID: "old2", Type: "function", Function: ToolFunction{Name: "get_recent_messages", Arguments: "{}"}},
		}},
		{Role: "tool", ToolCallID: "old1", Content: "earlier evidence"},
		{Role: "tool", ToolCallID: "old2", Content: "earlier chat"},
		{Role: "user", Content: "recent-question"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "recent1", Type: "function", Function: ToolFunction{Name: "read_file", Arguments: "{}"}},
			{ID: "recent2", Type: "function", Function: ToolFunction{Name: "read_file", Arguments: "{}"}},
		}},
		{Role: "tool", ToolCallID: "recent1", Content: "recent-evidence-one"},
		{Role: "tool", ToolCallID: "recent2", Content: "recent-evidence-two"},
	}
	m.conversation.unreadStart = len(m.conversation.messages)
	return m, f
}

func TestCompactionSummarizesWithoutToolsAndKeepsCompleteRecentGroup(t *testing.T) {
	m, f := seedCompaction(t, plainResp("Continue pending-goal; evidence in research.md."))
	m.handleTurn(&Turn{SelfID: 999, Now: time.Now(), NewMessages: []TurnMessage{{Text: "new-event"}}})
	reqs := requestsSnapshot(f)
	if len(reqs) != 2 {
		t.Fatalf("want summary + action request, got %d", len(reqs))
	}
	if len(reqs[0].Tools) != 0 || reqs[0].MaxTokens != 256 {
		t.Fatalf("summary request must be bounded and tool-free: %+v", reqs[0])
	}
	text := requestText(reqs[1])
	if !strings.Contains(text, "Continue pending-goal") || !strings.Contains(text, "new-event") ||
		!strings.Contains(text, "recent-evidence-one") || !strings.Contains(text, "recent-evidence-two") {
		t.Fatalf("compaction lost summary or recent evidence: %s", text)
	}
	if strings.Count(text, "archived-data") > 10 {
		t.Fatal("old material was not compacted")
	}
	requireToolPairs(t, reqs[1].Messages)
}

func TestCompactionFailureUsesBoundedExtractAndContinues(t *testing.T) {
	m, f := seedCompaction(t, "invalid-json")
	m.handleTurn(&Turn{SelfID: 999, Now: time.Now(), NewMessages: []TurnMessage{{Text: "new-event"}}})
	reqs := requestsSnapshot(f)
	if len(reqs) != 2 {
		t.Fatalf("summary failure must not discard the wake or loop on retries, got %d requests", len(reqs))
	}
	if text := requestText(reqs[1]); !strings.Contains(text, "摘要降级") || !strings.Contains(text, "new-event") {
		t.Fatalf("missing explicit fallback warning and live event: %s", text)
	}
	requireToolPairs(t, reqs[1].Messages)
}

func TestContextRejectsOversizedUncompactableRequest(t *testing.T) {
	m, f, _ := newLiveManager(t, []string{toolCallResp("done", "finish", "{}")})
	m.cfg.Context = config.AgentContext{MaxInputTokens: 2048, SummaryTokens: 128}
	m.registry = NewRegistry()
	m.handleTurn(&Turn{SelfID: 999, NewMessages: []TurnMessage{{Text: strings.Repeat("大", 8000)}}})
	if f.count() != 0 {
		t.Fatalf("oversized live input must not be sent, got %d requests", f.count())
	}
	events := m.events.After(0)
	if len(events) == 0 || events[len(events)-1].Kind != KindError {
		t.Fatalf("oversized input must report an error, got %+v", events)
	}
}

func TestContextSmallWakesDoNotSummarizeEveryReview(t *testing.T) {
	m, f, _ := newLiveManager(t, []string{toolCallResp("done", "finish", "{}")})
	m.cfg.Context = config.AgentContext{ReviewAfterWakes: 2}
	m.registry = NewRegistry()
	for i := 0; i < 10; i++ {
		m.handleTurn(&Turn{SelfID: 999, Now: time.Now(), NewMessages: []TurnMessage{{Text: "small-event"}}})
	}
	if got := f.count(); got != 10 {
		t.Fatalf("small context should not generate extra summary requests: %d", got)
	}
}

func TestContextDefaultBudgetFitsBundledPromptsAndTools(t *testing.T) {
	cfg := testCfg(time.Millisecond)
	cfg.PersonaFile, cfg.GuideFile = "persona.md", "guide.md"
	cfg.ImageDir, cfg.WorkspaceDir = t.TempDir(), t.TempDir()
	builder := NewDefaultBuilder(nil, cfg)
	registry := NewRegistry(enabledModules(cfg)...)
	builder.SetToolDocs(registry.PromptDocs())
	messages := builder.Build(&Turn{SelfID: 999, Now: time.Now(),
		NewMessages: []TurnMessage{{Text: "hello"}}})
	size := estimatedInputTokens(messages, toolDefs(registry.Build(&ToolContext{})))
	if size >= cfg.Context.WithDefaults().MaxInputTokens*3/4 {
		t.Fatalf("bundled prompts/tools leave insufficient working budget: estimate=%d", size)
	}
	t.Logf("bundled prompts/tools estimate=%d tokens", size)
}

func TestContextWakeReviewExcludesFixedSchemaOverhead(t *testing.T) {
	m, f, _ := newLiveManager(t, []string{toolCallResp("done", "finish", "{}")})
	m.cfg.Context = config.AgentContext{MaxInputTokens: 6000, ReviewAfterWakes: 2,
		SummaryTokens: 128, KeepRecentMessages: 2}
	m.registry = NewRegistry(module{name: "schema", tools: func(*ToolContext) []Tool {
		return []Tool{{Name: "unused", Description: strings.Repeat("schema ", 1600),
			Parameters: objectParams(nil, nil)}}
	}})
	for i := 0; i < 8; i++ {
		m.handleTurn(&Turn{SelfID: 999, Now: time.Now(), NewMessages: []TurnMessage{{Text: "small"}}})
	}
	if got := f.count(); got != 8 {
		t.Fatalf("fixed tool schemas caused unnecessary summaries: requests=%d, want 8", got)
	}
}

func TestCompactionArrivalsEnterNextActionRequest(t *testing.T) {
	m, f := seedCompaction(t, plainResp("old material summarized"))
	m.inbox = &accountInbox{ready: make(chan struct{}, 1)}
	f.hook = func(idx int, _ *http.Request) {
		if idx == 0 {
			m.Enqueue(msg("private", 222, "arrived-during-summary"))
		}
	}
	m.handleTurn(&Turn{SelfID: 999, Now: time.Now(), NewMessages: []TurnMessage{{Text: "first-event"}}})
	reqs := requestsSnapshot(f)
	if len(reqs) < 2 || !strings.Contains(requestText(reqs[1]), "arrived-during-summary") {
		t.Fatal("an arrival during summary was omitted from the next action request")
	}
}

func TestContextPollingWakeCountTracksOnlyActivity(t *testing.T) {
	m, _, _ := newLiveManager(t, []string{
		toolCallResp("idle", "finish", "{}"),
		toolCallResp("query", "get_recent_messages", "{}"),
		toolCallResp("pause", "finish", "{}"),
		plainResp("hello"),
	})
	startFakeNapCat(t)
	poll := func() *Turn {
		return &Turn{SelfID: 999, Wake: WakeBackground, ChatType: "private", ChatID: 999, Now: time.Now(),
			NewMessages: []TurnMessage{{Wake: WakeBackground, SenderName: "轮询", Text: "[定时轮询]"}}}
	}
	m.handleTurn(poll())
	if m.conversation.wakes != 0 {
		t.Fatal("idle polling counted as a meaningful wake")
	}
	m.handleTurn(poll())
	m.handleTurn(poll())
	if m.conversation.wakes != 2 {
		t.Fatalf("tool work and plain-text activity should both count, got %d", m.conversation.wakes)
	}
}
