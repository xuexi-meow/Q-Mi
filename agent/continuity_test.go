package agent

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"Q-Mi/app"
	"Q-Mi/utils/config"
)

func requestsSnapshot(f *fakeLLM) []ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ChatRequest(nil), f.reqs...)
}

func requestText(req ChatRequest) string {
	var parts []string
	for _, message := range req.Messages {
		parts = append(parts, contentText(message))
	}
	return strings.Join(parts, "\n")
}

func requireToolPairs(t *testing.T, messages []ChatMessage) {
	t.Helper()
	for i, message := range messages {
		if len(message.ToolCalls) == 0 {
			continue
		}
		for j, call := range message.ToolCalls {
			k := i + 1 + j
			if k >= len(messages) || messages[k].Role != "tool" || messages[k].ToolCallID != call.ID {
				t.Fatalf("unclosed tool call %q at %d: %+v", call.ID, i, messages)
			}
		}
	}
}

func TestFinishRetainsWorkingContextAcrossChats(t *testing.T) {
	b := &stubBuilder{fn: joinNewMessages}
	m, f, _ := newLoopManager(t, testCfg(time.Millisecond), b, nil, []string{
		twoToolCallResp("research", "think", `{"note":"continue experiment"}`, "pause", "finish", "{}"),
		toolCallResp("pause2", "finish", "{}"),
	})
	m.handleTurn(&Turn{ChatType: "group", ChatID: 888, SelfID: 999, Now: time.Now(),
		NewMessages: []TurnMessage{{ChatType: "group", ChatID: 888, Text: "research-first"}}})
	m.handleTurn(&Turn{ChatType: "private", ChatID: 222, SelfID: 999, Now: time.Now(),
		NewMessages: []TurnMessage{{ChatType: "private", ChatID: 222, Text: "later-message"}}})

	reqs := requestsSnapshot(f)
	if len(reqs) != 2 {
		t.Fatalf("requests=%d, want 2", len(reqs))
	}
	text := requestText(reqs[1])
	if !strings.Contains(text, "research-first") || !strings.Contains(text, "later-message") {
		t.Fatalf("finish lost working context: %s", text)
	}
	requireToolPairs(t, reqs[1].Messages)
	found := false
	for _, message := range reqs[1].Messages {
		found = found || message.ToolCallID == "pause"
	}
	if !found {
		t.Fatal("finish must have a result before the conversation is reused")
	}
}

func TestAccountInsertsUrgentWithoutCancelling(t *testing.T) {
	for _, destination := range []string{"same", "other"} {
		t.Run(destination, func(t *testing.T) {
			b := &stubBuilder{fn: joinNewMessages}
			m, f, _ := newLoopManager(t, testCfg(20*time.Millisecond), b, nil, []string{
				toolCallResp("lookup", "get_recent_messages", "{}"),
				toolCallResp("pause", "finish", "{}"),
			})
			release := make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			cancelled := make(chan struct{}, 1)
			f.mu.Lock()
			f.hook = func(idx int, r *http.Request) {
				if idx == 0 {
					select {
					case <-release:
					case <-r.Context().Done():
						cancelled <- struct{}{}
					}
				}
			}
			f.mu.Unlock()
			m.Enqueue(msg("group", 888, "original-message"))
			waitFor(t, time.Second, func() bool { return f.count() == 1 }, "initial request")
			next := msg("group", 888, "inserted-message")
			if destination == "other" {
				next.ChatType, next.ChatID = "private", 222
			}
			next.Urgent = true
			m.Enqueue(next)
			select {
			case <-cancelled:
				t.Error("incoming message cancelled the active request")
			case <-time.After(60 * time.Millisecond):
			}
			once.Do(func() { close(release) })
			waitFor(t, time.Second, func() bool { return f.count() >= 2 }, "continued request")
			req := requestsSnapshot(f)[1]
			if text := requestText(req); !strings.Contains(text, "original-message") ||
				!strings.Contains(text, "inserted-message") {
				t.Fatalf("message restarted instead of joining conversation: %s", text)
			}
			requireToolPairs(t, req.Messages)
			found := false
			for _, message := range req.Messages {
				found = found || message.ToolCallID == "lookup"
			}
			if !found {
				t.Fatal("insertion discarded the in-flight tool response")
			}
		})
	}
}

func TestAccountMixedSourcesHaveNoImplicitReply(t *testing.T) {
	b := &stubBuilder{fn: joinNewMessages}
	m, f, napcat := newLoopManager(t, testCfg(30*time.Millisecond), b, nil, []string{plainResp("ambiguous")})
	m.Enqueue(msg("group", 888, "group-message"))
	m.Enqueue(msg("private", 222, "private-message"))
	waitFor(t, time.Second, func() bool { return f.count() > 0 }, "mixed wake")
	time.Sleep(80 * time.Millisecond)
	if napcat.count() != 0 {
		t.Fatalf("ambiguous plain text was sent to %d destinations", napcat.count())
	}
	if f.count() != 1 {
		t.Fatalf("account debounce must merge chats, got %d requests", f.count())
	}
	turn := b.turn(0)
	if len(turn.NewMessages) != 2 || turn.NewMessages[0].ChatID != 888 ||
		turn.NewMessages[1].ChatID != 222 {
		t.Fatalf("mixed inbox lost source metadata: %+v", turn.NewMessages)
	}
}

func TestAccountUsesConfiguredTierModel(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(time.Millisecond)
	cfg.Models = map[string]config.ModelTier{WakeBackground: {Model: "background-model"}}
	m, f, _ := newLoopManager(t, cfg, b, nil, []string{toolCallResp("pause", "finish", "{}")})
	m.handleTurn(&Turn{Wake: WakeBackground, SelfID: 999})
	if got := requestsSnapshot(f)[0].Model; got != "background-model" {
		t.Fatalf("model=%q, want background-model", got)
	}
}

func TestAccountMixedBatchDoesNotInheritAdminPrivilege(t *testing.T) {
	a := app.GetApp()
	previous := a.Config
	a.Config = &config.Config{Bot: &config.Bot{Admin_Number: []int{555}}}
	t.Cleanup(func() { a.Config = previous })
	for _, tc := range []struct {
		name     string
		messages []TurnMessage
		want     bool
	}{
		{"admin only", []TurnMessage{{SenderID: 555}}, true},
		{"mixed", []TurnMessage{{SenderID: 555}, {SenderID: 111}}, false},
		{"background", []TurnMessage{{SenderID: 555, Wake: WakeBackground}}, false},
		{"no sender", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := batchIsAdmin(&Turn{NewMessages: tc.messages}); got != tc.want {
				t.Fatalf("batch privilege=%v, want %v", got, tc.want)
			}
		})
	}
}
