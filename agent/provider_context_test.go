package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
	"Q-Mi/utils/config"
)

func TestProviderContextLimitCompactsOnceWithoutReplayingTools(t *testing.T) {
	for _, rejectAgain := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovers", true: "blocks_after_one_retry"}[rejectAgain], func(t *testing.T) {
			actions, summaries := 0, 0
			var last ChatRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req ChatRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				if len(req.Tools) == 0 {
					summaries++
					_, _ = w.Write([]byte(plainResp("provider recovery summary")))
					return
				}
				actions++
				last = req
				if actions == 1 || rejectAgain {
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":{"code":"context_length_exceeded","message":"maximum context length exceeded"}}`))
					return
				}
				_, _ = w.Write([]byte(toolCallResp("done", "finish", "{}")))
			}))
			defer server.Close()
			m, _, _ := newLiveManager(t, nil)
			m.cfg.Context = config.AgentContext{MaxInputTokens: 64000, SummaryTokens: 256, KeepRecentMessages: 12}
			m.pool = model.NewPool(m.cfg, model.NewClient(server.URL, "test", time.Second))
			m.conversation.Messages = agentcontext.FromWireMessages([]ChatMessage{
				{Role: "system", Content: "initial"},
				{Role: "user", Content: strings.Repeat("old-data ", 1500)},
				{Role: "assistant", ToolCalls: []ToolCall{{ID: "sent", Type: "function", Function: ToolFunction{Name: "send_message", Arguments: "{}"}}}},
				{Role: "tool", ToolCallID: "sent", Content: `{"sent":true}`},
			})
			m.conversation.UnreadStart = len(m.conversation.Messages)
			m.handleTurn(&Turn{SelfID: 999, NewMessages: []TurnMessage{{Text: "current input"}}})
			if actions != 2 || summaries != 1 {
				t.Fatalf("provider recovery must be bounded: actions=%d summaries=%d", actions, summaries)
			}
			if !strings.Contains(requestText(last), "provider recovery summary") ||
				strings.Count(requestText(last), "current input") != 1 {
				t.Fatal("retry lost recovered context or duplicated trigger")
			}
			requireToolPairs(t, last.Messages)
			wantPhase := "idle"
			if rejectAgain {
				wantPhase = "blocked"
			}
			if got := m.InspectRuntime().Phase; got != wantPhase {
				t.Fatalf("phase=%s want=%s", got, wantPhase)
			}
			if rejectAgain && !strings.Contains(m.InspectRuntime().LastError, "context_length_exceeded") {
				t.Fatal("provider diagnostic was lost")
			}
		})
	}
}

func TestOtherProviderBadRequestDoesNotCompactOrRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid tool arguments"}}`))
	}))
	defer server.Close()
	m, _, _ := newLiveManager(t, nil)
	m.pool = model.NewPool(m.cfg, model.NewClient(server.URL, "test", time.Second))
	m.handleTurn(&Turn{SelfID: 999, NewMessages: []TurnMessage{{Text: "current input"}}})
	if calls != 1 || m.conversation.Compactions != 0 || m.InspectRuntime().Phase != "error" {
		t.Fatalf("unrelated error caused context recovery: calls=%d phase=%s", calls, m.InspectRuntime().Phase)
	}
}

func TestProviderContextLimitWithoutOldWorkBlocksWithoutRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"code":"context_length_exceeded"}}`))
	}))
	defer server.Close()
	m, _, _ := newLiveManager(t, nil)
	m.pool = model.NewPool(m.cfg, model.NewClient(server.URL, "test", time.Second))
	m.handleTurn(&Turn{SelfID: 999, NewMessages: []TurnMessage{{Text: "protected input"}}})
	if calls != 1 || m.InspectRuntime().Phase != "blocked" {
		t.Fatalf("retried without reclaimable work: calls=%d phase=%s", calls, m.InspectRuntime().Phase)
	}
	if !strings.Contains(requestText(ChatRequest{Messages: agentcontext.ToWireMessages(m.conversation.Messages)}), "protected input") {
		t.Fatal("provider rejection discarded current input")
	}
}
