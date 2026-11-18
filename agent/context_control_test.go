package agent

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	agentcontext "Q-Mi/agent/context"
	agentruntime "Q-Mi/agent/runtime"
	"Q-Mi/utils/config"
)

func contextControlManager(t *testing.T) *Manager {
	t.Helper()
	m, _, _ := newLiveManager(t, nil)
	m.conversation.Messages = []agentcontext.Message{
		agentcontext.FromWire(ChatMessage{Role: "system", Content: "prompt"}, agentcontext.KindWork),
		agentcontext.FromWire(ChatMessage{Role: "user", Content: "original"}, agentcontext.KindWork),
	}
	m.conversation.UnreadStart = 2
	m.publishContextInspection("working", m.conversation.Messages, nil)
	return m
}

func TestContextControlPreviewVersionAndBusyDoNotOverwrite(t *testing.T) {
	m := contextControlManager(t)
	version := m.InspectRuntime().WorkingVersion
	text := "edited"
	change := ContextAction{Version: version, Action: "edit", Index: 1, Text: &text, Preview: true}
	result, err := m.ControlContext(context.Background(), change)
	if err != nil || len(result.Affected) != 1 || m.conversation.Messages[1].Wire.Content != "original" {
		t.Fatalf("preview mutated real data: %+v %v", result, err)
	}
	change.Preview = false
	m.runMu.Lock()
	_, err = m.ControlContext(context.Background(), change)
	m.runMu.Unlock()
	if !errors.Is(err, ErrContextBusy) {
		t.Fatalf("write during activity accepted: %v", err)
	}
	if _, err = m.ControlContext(context.Background(), change); err != nil {
		t.Fatal(err)
	}
	if m.conversation.Messages[1].Wire.Content != "edited" {
		t.Fatal("edit did not reach live context")
	}
	if _, err = m.ControlContext(context.Background(), change); !errors.Is(err, ErrInspectionVersion) {
		t.Fatalf("stale edit accepted: %v", err)
	}
}

func TestContextEditReadsOriginalAndKeepsRedactedDisplayReadOnly(t *testing.T) {
	m := contextControlManager(t)
	m.conversation.Messages[1].Wire.Content = `{"token":"original-secret","value":"original"}`
	m.publishContextInspection("working", m.conversation.Messages, nil)
	version := m.InspectRuntime().WorkingVersion
	entry, err := m.EditableContextEntry(version, 1)
	if err != nil || !strings.Contains(entry.Text, "original-secret") {
		t.Fatalf("editor read lossy display instead of original: %+v %v", entry, err)
	}
	shown, err := m.InspectEntry("working", version, 1)
	if err != nil || strings.Contains(shown.Text, "original-secret") {
		t.Fatalf("display leaked original data: %+v %v", shown, err)
	}
	entry, err = m.EditableContextEntry(version, 0)
	if err != nil || entry.Editable || entry.Deletable {
		t.Fatalf("prompt bypassed dedicated file editor: %+v %v", entry, err)
	}
}

func TestBudgetBlockIsVisibleAndResumeDoesNotDuplicateTrigger(t *testing.T) {
	m, f, _ := newLiveManager(t, []string{toolCallResp("finish", "finish", "{}")})
	m.cfg.Context = config.AgentContext{MaxInputTokens: 2048, SummaryTokens: 128}
	m.registry = NewRegistry()
	m.handleTurn(&Turn{ChatType: "group", ChatID: 8, SelfID: 999, Now: time.Now(),
		NewMessages: []TurnMessage{{ChatType: "group", ChatID: 8, Text: strings.Repeat("大", 4000)}}})
	if m.InspectRuntime().Phase != "blocked" {
		t.Fatalf("budget error not distinguishable: %+v", m.InspectRuntime())
	}
	page, err := m.InspectContext(ContextQuery{})
	if err != nil {
		t.Fatal(err)
	}
	index := -1
	for _, e := range page.Entries {
		if e.Kind == "input" {
			index = e.Index
		}
	}
	text := "shortened original trigger"
	result, err := m.ControlContext(context.Background(), ContextAction{Version: page.Version, Action: "edit", Index: index, Text: &text})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ControlContext(context.Background(), ContextAction{Version: result.Version, Action: "resume"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return m.InspectRuntime().Phase == "idle" }, "context activity did not resume")
	reqs := requestsSnapshot(f)
	if len(reqs) != 1 || strings.Count(requestText(reqs[0]), "shortened original trigger") != 1 ||
		strings.Contains(requestText(reqs[0]), strings.Repeat("大", 100)) {
		t.Fatalf("resume replayed trigger or discarded edit: %+v", reqs)
	}
}

func TestManualContextCompactionPreviewIsLocalAndCommitUsesSummary(t *testing.T) {
	m, f := seedCompaction(t, plainResp("manual summary"))
	m.publishContextInspection("working", m.conversation.Messages, nil)
	version := m.InspectRuntime().WorkingVersion
	action := ContextAction{Version: version, Action: "compact", Preview: true}
	result, err := m.ControlContext(context.Background(), action)
	if err != nil || len(result.Affected) == 0 || f.count() != 0 ||
		m.InspectRuntime().WorkingVersion != version || m.conversation.Compactions != 0 {
		t.Fatalf("manual preview was not local: %+v %v", result, err)
	}
	action.Preview = false
	result, err = m.ControlContext(context.Background(), action)
	if err != nil || f.count() != 1 || result.Version == version || m.InspectRuntime().Phase != "idle" {
		t.Fatalf("manual compaction failed: %+v %v", result, err)
	}
	if !strings.Contains(requestText(ChatRequest{Messages: agentcontext.ToWireMessages(m.conversation.Messages)}), "manual summary") {
		t.Fatal("summary did not reach real working context")
	}
}

func TestCancelledContextCompactionKeepsOriginalAndRestoresPhase(t *testing.T) {
	m, f := seedCompaction(t, plainResp("unused summary"))
	m.publishContextInspection("working", m.conversation.Messages, nil)
	m.finishInspection(&agentruntime.BudgetError{Estimated: 6000, Budget: 5000})
	version := m.InspectRuntime().WorkingVersion
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.hook = func(_ int, _ *http.Request) { cancel() }
	_, err := m.ControlContext(ctx, ContextAction{Version: version, Action: "compact"})
	if !errors.Is(err, context.Canceled) || m.InspectRuntime().WorkingVersion != version ||
		m.conversation.Compactions != 0 || m.InspectRuntime().Phase != "blocked" {
		t.Fatalf("cancelled edit committed or left activity busy: %v %+v", err, m.InspectRuntime())
	}
}

func TestContextEditRejectsCancelledOperationBeforeChangingData(t *testing.T) {
	m := contextControlManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	text := "should not save"
	_, err := m.ControlContext(ctx, ContextAction{Version: m.InspectRuntime().WorkingVersion, Action: "edit", Index: 1, Text: &text})
	if !errors.Is(err, context.Canceled) || m.conversation.Messages[1].Wire.Content != "original" {
		t.Fatalf("cancelled operation overwrote working data: %v", err)
	}
}

func TestContextResumeRetainsAuthorizationWithoutRetainingOriginalBody(t *testing.T) {
	setDNDTestConfig(t) // Administrator QQ: 555.
	m := contextControlManager(t)
	m.registry = NewRegistry(module{name: "admin", tools: func(ctx *ToolContext) []Tool {
		if ctx.Admin {
			return []Tool{{Name: "admin_action"}}
		}
		return nil
	}})
	m.rememberContextTurn(&Turn{SelfID: 999, NewMessages: []TurnMessage{{SenderID: 555, Text: "stale body"}}})
	if !batchIsAdmin(m.contextTurn) {
		t.Fatal("resuming an administrator activity lost authorization metadata")
	}
	if len(m.contextTurn.NewMessages) != 1 || m.contextTurn.NewMessages[0].Text != "" {
		t.Fatal("stored original input instead of authorization metadata")
	}
	defs := m.currentContextDefs()
	if len(defs) != 1 || defs[0].Function.Name != "admin_action" {
		t.Fatalf("budget/editor did not use actual resume tools: %+v", defs)
	}
}
