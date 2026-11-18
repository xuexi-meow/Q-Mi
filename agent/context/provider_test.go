package context_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/utils/config"
)

type stateProvider struct {
	name string
	read func(agentcontext.ProviderInput) (agentcontext.StateFragment, error)
}

func (p stateProvider) Name() string { return p.name }
func (p stateProvider) State(input agentcontext.ProviderInput) (agentcontext.StateFragment, error) {
	return p.read(input)
}

func stateSnapshot(t *testing.T, text string) map[string]json.RawMessage {
	t.Helper()
	start, end := strings.IndexByte(text, '{'), strings.LastIndexByte(text, '}')
	if start < 0 || end < start {
		t.Fatalf("missing state JSON: %q", text)
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text[start:end+1]), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestStateProvidersPreserveOrderAndCannotRewriteMessages(t *testing.T) {
	builder := agentcontext.NewDefaultBuilder(nil, config.AgentConfig{WorkspaceDir: t.TempDir(), ImageDir: t.TempDir()})
	turn := &agentcontext.Turn{
		SelfID: 99, ChatType: "group", ChatID: 8, Now: time.Unix(100, 0),
		NewMessages: []agentcontext.TurnMessage{{ChatType: "group", ChatID: 8, Text: "original input"}},
	}
	before := builder.BuildWorkingMessages(turn)
	scope := agentcontext.ProviderScope{SelfID: 99, ChatType: "group", ChatID: 8}
	var calls []string
	makeProvider := func(name, text string) stateProvider {
		return stateProvider{name: name, read: func(input agentcontext.ProviderInput) (agentcontext.StateFragment, error) {
			if input.Scope != scope || !input.Now.Equal(time.Unix(100, 0)) {
				t.Fatalf("wrong provider input: %+v", input)
			}
			calls = append(calls, name)
			return agentcontext.StateFragment{Scope: input.Scope, Text: text}, nil
		}}
	}
	providers := []agentcontext.StateProvider{makeProvider("first", "reference one"), makeProvider("second", "reference two")}
	builder.SetStateProviders(providers...)
	providers[0] = makeProvider("replacement", "must not appear")
	after := builder.BuildWorkingMessages(turn)

	if !reflect.DeepEqual(calls, []string{"first", "second"}) {
		t.Fatalf("provider order changed or registration slice was retained: %v", calls)
	}
	if len(before) != len(after) || !reflect.DeepEqual(before[0], after[0]) ||
		!reflect.DeepEqual(before[2:], after[2:]) {
		t.Fatalf("providers changed system/input messages: before=%+v after=%+v", before, after)
	}
	state := stateSnapshot(t, after[1].Wire.Content.(string))
	var fragments []struct {
		Provider string                     `json:"provider"`
		Scope    agentcontext.ProviderScope `json:"scope"`
		Text     string                     `json:"text"`
	}
	if err := json.Unmarshal(state["references"], &fragments); err != nil {
		t.Fatal(err)
	}
	if len(fragments) != 2 || fragments[0].Provider != "first" || fragments[0].Text != "reference one" ||
		fragments[1].Provider != "second" || fragments[1].Text != "reference two" ||
		fragments[0].Scope != scope || fragments[1].Scope != scope {
		t.Fatalf("wrong reference identity/order: %+v", fragments)
	}
}

func TestStateProvidersRejectForeignScopeAndKeepCoreStateOnFailure(t *testing.T) {
	builder := agentcontext.NewDefaultBuilder(nil, config.AgentConfig{WorkspaceDir: t.TempDir(), ImageDir: t.TempDir()})
	scope := agentcontext.ProviderScope{SelfID: 99, ChatType: "group", ChatID: 8}
	builder.SetStateProviders(
		stateProvider{name: "broken", read: func(agentcontext.ProviderInput) (agentcontext.StateFragment, error) {
			return agentcontext.StateFragment{}, errors.New("unavailable")
		}},
		stateProvider{name: "foreign", read: func(input agentcontext.ProviderInput) (agentcontext.StateFragment, error) {
			input.Scope.ChatID = 9
			return agentcontext.StateFragment{Scope: input.Scope, Text: "private group nine"}, nil
		}},
		stateProvider{name: "valid", read: func(input agentcontext.ProviderInput) (agentcontext.StateFragment, error) {
			return agentcontext.StateFragment{Scope: input.Scope, Text: "current reference"}, nil
		}},
	)
	text := builder.StateText(&agentcontext.Turn{
		SelfID: scope.SelfID, ChatType: scope.ChatType, ChatID: scope.ChatID, Mood: "calm",
		Notes: []agentcontext.Note{{Text: "keep this note"}},
	})
	state := stateSnapshot(t, text)
	if string(state["mood"]) != `"calm"` || !strings.Contains(string(state["notes"]), "keep this note") {
		t.Fatalf("optional provider failure dropped core state: %q", text)
	}
	if strings.Contains(text, "private group nine") || strings.Contains(text, `"provider":"broken"`) ||
		!strings.Contains(text, `"provider":"valid"`) {
		t.Fatalf("provider isolation failed: %q", text)
	}
}

func TestStateProvidersRequireUnambiguousChatAndAccount(t *testing.T) {
	for _, scope := range []agentcontext.ProviderScope{
		{}, {SelfID: 99}, {SelfID: 99, ChatType: "group"}, {SelfID: 99, ChatType: "other", ChatID: 8},
		{ChatType: "group", ChatID: 8},
	} {
		t.Run(fmt.Sprintf("%s/%d/%d", scope.ChatType, scope.ChatID, scope.SelfID), func(t *testing.T) {
			builder := agentcontext.NewDefaultBuilder(nil, config.AgentConfig{WorkspaceDir: t.TempDir(), ImageDir: t.TempDir()})
			builder.SetStateProviders(stateProvider{name: "must not run", read: func(agentcontext.ProviderInput) (agentcontext.StateFragment, error) {
				t.Fatal("provider invoked without an unambiguous account/chat")
				return agentcontext.StateFragment{}, nil
			}})
			text := builder.StateText(&agentcontext.Turn{SelfID: scope.SelfID, ChatType: scope.ChatType, ChatID: scope.ChatID})
			if _, ok := stateSnapshot(t, text)["references"]; ok {
				t.Fatalf("empty optional references changed legacy state JSON: %q", text)
			}
		})
	}
}
