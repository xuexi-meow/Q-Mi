package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"Q-Mi/utils/config"
)

func TestActionRequestUsesConfiguredToolChoice(t *testing.T) {
	for _, override := range []string{"", "auto"} {
		t.Run("candidate-"+override, func(t *testing.T) {
			cfg := testCfg(time.Millisecond)
			if err := json.Unmarshal([]byte(`{"ToolChoice":"required"}`), &cfg); err != nil {
				t.Fatal(err)
			}
			if override != "" {
				var candidate config.ModelCandidate
				if err := json.Unmarshal([]byte(`{"Name":"candidate","Model":"test-model","ToolChoice":"auto"}`), &candidate); err != nil {
					t.Fatal(err)
				}
				cfg.Models = map[string]config.ModelTier{"chat": {Candidates: []config.ModelCandidate{candidate}}}
			}
			m, f, _ := newLoopManager(t, cfg, &stubBuilder{}, nil, []string{toolCallResp("done", "finish", "{}")})
			m.handleTurn(&Turn{SelfID: 999})
			raw, _ := json.Marshal(f.reqs[0])
			var request map[string]any
			_ = json.Unmarshal(raw, &request)
			want := "required"
			if override != "" {
				want = override
			}
			if request["tool_choice"] != want {
				t.Fatalf("tool_choice=%v, want %s", request["tool_choice"], want)
			}
			if request["response_format"] != nil {
				t.Fatal("tool-only action requests must not force assistant-content JSON")
			}
		})
	}
}

func TestExecToolCallValidatesBeforeSideEffects(t *testing.T) {
	m, _, _ := newLoopManager(t, testCfg(time.Millisecond), &stubBuilder{}, nil, nil)
	called := 0
	tools := []Tool{{
		Name:       "side_effect",
		Parameters: objectParams(map[string]any{"text": strProp("text")}, []string{"text"}),
		Run:        func(json.RawMessage) (string, error) { called++; return "{}", nil },
	}}
	for _, args := range []string{`{bad`, `null`, `[]`, `{}`, `{"text":1}`, `{"text":"ok","wrong_key":true}`} {
		_, err := m.execToolCall(&ToolContext{}, tools, ToolCall{Function: ToolFunction{Name: "side_effect", Arguments: args}})
		if err == nil {
			t.Errorf("invalid arguments must be rejected: %s", args)
		}
	}
	if called != 0 {
		t.Fatalf("invalid arguments executed %d side effects", called)
	}
	if _, err := m.execToolCall(&ToolContext{}, tools, ToolCall{Function: ToolFunction{Name: "side_effect", Arguments: `{"text":"ok"}`}}); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatal("valid arguments should execute exactly once")
	}
}

func TestInvalidFinishArgumentsAreBackfilled(t *testing.T) {
	m, f, _ := newLoopManager(t, testCfg(time.Millisecond), &stubBuilder{}, nil, []string{
		toolCallResp("bad", "finish", `{"send_message":"draft"}`),
		toolCallResp("done", "finish", `{}`),
	})
	m.handleTurn(&Turn{SelfID: 999})
	if f.count() != 2 {
		t.Fatalf("invalid finish should not silently end activity: requests=%d", f.count())
	}
	last := f.reqs[1].Messages[len(f.reqs[1].Messages)-1]
	if last.ToolCallID != "bad" || !strings.Contains(contentText(last), "error") {
		t.Fatalf("invalid finish missing error feedback: %+v", last)
	}
}

func TestMalformedToolEnvelopeCannotSend(t *testing.T) {
	for _, body := range []string{
		`{"choices":[{"message":{"tool_calls":[{"id":"","type":"function","function":{"name":"send_message","arguments":"{\"chat_type\":\"group\",\"chat_id\":888,\"text\":\"draft\"}"}}]}}]}`,
		`{"choices":[{"finish_reason":"length","message":{"tool_calls":[{"id":"cut","type":"function","function":{"name":"send_message","arguments":"{\"chat_type\":\"group\",\"chat_id\":888,\"text\":\"draft\"}"}}]}}]}`,
		`{"choices":[{"message":{"tool_calls":[{"id":"same","type":"function","function":{"name":"send_message","arguments":"{\"chat_type\":\"group\",\"chat_id\":888,\"text\":\"draft\"}"}},{"id":"same","type":"function","function":{"name":"finish","arguments":"{}"}}]}}]}`,
	} {
		m, f, napcat := newLoopManager(t, testCfg(time.Millisecond), &stubBuilder{}, nil, []string{body, toolCallResp("done", "finish", "{}")})
		m.handleTurn(&Turn{SelfID: 999, ChatType: "group", ChatID: 888})
		if napcat.count() != 0 || f.count() != 2 {
			t.Fatalf("invalid/incomplete tool envelope must repair without sending: sends=%d requests=%d", napcat.count(), f.count())
		}
		requireToolPairs(t, f.reqs[1].Messages)
	}
}

func TestOversizeToolResultRemainsValidJSON(t *testing.T) {
	m, f, _ := newLoopManager(t, testCfg(time.Millisecond), &stubBuilder{}, nil, []string{
		toolCallResp("large", "get_recent_messages", `{}`),
		toolCallResp("done", "finish", `{}`),
	})
	m.registry = NewRegistry()
	// Use the normal registry interface with a deterministic local tool.
	m.registry.modules = []ToolModule{module{name: "large", tools: func(*ToolContext) []Tool {
		return []Tool{
			{Name: "get_recent_messages", Parameters: objectParams(nil, nil), Run: func(json.RawMessage) (string, error) {
				return `{"data":"` + strings.Repeat(`\"`, maxContentRunes) + `"}`, nil
			}},
			{Name: "finish", Parameters: objectParams(nil, nil)},
		}
	}}}
	m.handleTurn(&Turn{SelfID: 999})
	if f.count() != 2 {
		t.Fatalf("requests=%d, want 2", f.count())
	}
	last := contentText(f.reqs[1].Messages[len(f.reqs[1].Messages)-1])
	if !json.Valid([]byte(last)) || utf8.RuneCountInString(last) > maxContentRunes {
		t.Fatalf("oversize result broke JSON or budget: runes=%d, valid=%v", utf8.RuneCountInString(last), json.Valid([]byte(last)))
	}
	var result map[string]any
	_ = json.Unmarshal([]byte(last), &result)
	if result["truncated"] != true {
		t.Fatalf("partial output must be labeled as truncated: %.100s", last)
	}
}
