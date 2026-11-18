package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
	"Q-Mi/utils/config"
)

func TestContextMessageKindsFollowActivityLifetime(t *testing.T) {
	m, _, _ := newLiveManager(t, []string{toolCallResp("done", "finish", "{}")})
	m.handleTurn(&Turn{SelfID: 999, Now: time.Now(), NewMessages: []TurnMessage{{Text: "first-input"}}})
	assertKind := func(text string, want agentcontext.MessageKind) {
		t.Helper()
		for _, message := range m.conversation.Messages {
			if strings.Contains(contentText(message.Wire), text) {
				if message.Kind != want {
					t.Fatalf("%q kind = %v, want %v", text, message.Kind, want)
				}
				return
			}
		}
		t.Fatalf("message %q missing", text)
	}
	assertKind("first-input", agentcontext.KindActiveInput)
	assertKind("[当前状态快照]", agentcontext.KindState)
	m.handleTurn(&Turn{SelfID: 999, Now: time.Now(), NewMessages: []TurnMessage{{Text: "second-input"}}})
	assertKind("first-input", agentcontext.KindWork)
	assertKind("second-input", agentcontext.KindActiveInput)
	assertKind("[当前状态快照]", agentcontext.KindState)
}

func TestContextMetadataSurvivesTextOnlyRequestInspection(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := newModelServer(t, http.StatusOK, okCompletion, bodyCh)
	cfg := config.AgentConfig{Models: map[string]config.ModelTier{
		"chat": {Candidates: []config.ModelCandidate{candidate("text", server.URL, "m", "tools")}},
	}}
	m := &Manager{cfg: cfg, pool: buildPool(cfg, nil)}
	messages := []agentcontext.Message{
		{Wire: ChatMessage{Role: "user", Content: "state"}, Kind: agentcontext.KindState},
		{Wire: ChatMessage{Role: "user", Content: "summary"}, Kind: agentcontext.KindSummary},
		{Wire: ChatMessage{Role: "user", Content: []ContentPart{
			{Type: "text", Text: "photo"}, imagePart("data:image/png;base64,AAAA"),
		}}, Kind: agentcontext.KindActiveInput},
	}
	m.conversation.Messages = messages
	defs := []ToolDef{{Type: "function", Function: ToolSchema{Name: "finish", Parameters: objectParams(nil, nil)}}}
	if _, _, _, err := m.chatWithWorking(context.Background(), &Turn{},
		ChatRequest{Messages: agentcontext.ToWireMessages(messages), Tools: defs},
		messages, model.Need{Tools: true, Vision: true}, 1, 0); err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(<-bodyCh, &body); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body["messages"]), `"Kind"`) ||
		strings.Contains(string(body["messages"]), `"Wire"`) ||
		strings.Contains(string(body["messages"]), `"image_url"`) {
		t.Fatalf("request leaked metadata or images: %s", body["messages"])
	}
	request, err := m.InspectContext(ContextQuery{Source: "request"})
	if err != nil || len(request.Entries) != 4 {
		t.Fatalf("request inspection missing: %+v / %v", request, err)
	}
	for i, want := range []string{"state", "summary", "input"} {
		if got := request.Entries[i].Kind; got != want {
			t.Errorf("request entry %d kind = %q, want %q", i, got, want)
		}
	}
	if request.Entries[2].Images != 0 || !strings.Contains(request.Entries[2].Text, "图片已省略") {
		t.Fatalf("request inspection does not match downgraded request: %+v", request.Entries[2])
	}
	m.publishContextInspection("working", m.conversation.Messages, defs)
	working, err := m.InspectContext(ContextQuery{Source: "working"})
	if err != nil || working.Entries[2].Images != 1 ||
		m.conversation.Messages[2].Kind != agentcontext.KindActiveInput {
		t.Fatalf("request downgrade mutated working context: %+v / %v", working, err)
	}
}

func TestContextMessageKindsDoNotAffectTokenEstimate(t *testing.T) {
	for _, kind := range []agentcontext.MessageKind{
		agentcontext.KindWork, agentcontext.KindState, agentcontext.KindSummary, agentcontext.KindActiveInput,
	} {
		messages := []agentcontext.Message{{Wire: ChatMessage{Role: "user", Content: "hello world!"}, Kind: kind}}
		if got := estimatedInputTokens(agentcontext.ToWireMessages(messages), nil); got != 20 {
			t.Fatalf("kind %v affected wire token estimate: got %d, want 20", kind, got)
		}
	}
}
