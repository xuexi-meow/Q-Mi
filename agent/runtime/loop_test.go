package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
	agentruntime "Q-Mi/agent/runtime"
	"Q-Mi/agent/tool"
)

type loopDriver struct {
	conversation *agentruntime.Conversation
	responses    []string
	requests     []model.ChatRequest
	tools        []tool.Tool
	observed     []agentruntime.LoopEvent
}

func (d *loopDriver) Prepare(_ context.Context, turn *agentcontext.Turn, _ int) (agentruntime.PreparedTurn, error) {
	return agentruntime.PreparedTurn{
		Turn: turn, Tools: d.tools, Executor: &agentruntime.Executor{Turn: turn},
		Request: model.ChatRequest{Messages: agentcontext.ToWireMessages(d.conversation.Messages)},
	}, nil
}

func (d *loopDriver) Request(_ context.Context, prepared agentruntime.PreparedTurn, _ int) (*model.ChatResponse, error) {
	d.requests = append(d.requests, prepared.Request)
	if len(d.responses) == 0 {
		return nil, errors.New("unexpected request")
	}
	var response model.ChatResponse
	err := json.Unmarshal([]byte(d.responses[0]), &response)
	d.responses = d.responses[1:]
	return &response, err
}

func (d *loopDriver) Observe(event agentruntime.LoopEvent) {
	d.observed = append(d.observed, event)
}

func TestLoopFinishWaitsForToolsAndKeepsCallOrder(t *testing.T) {
	conversation := &agentruntime.Conversation{}
	var mu sync.Mutex
	completed := false
	driver := &loopDriver{
		conversation: conversation,
		responses: []string{`{"choices":[{"message":{"tool_calls":[
			{"id":"slow","type":"function","function":{"name":"slow","arguments":"{}"}},
			{"id":"finish","type":"function","function":{"name":"finish","arguments":"{}"}},
			{"id":"fast","type":"function","function":{"name":"fast","arguments":"{}"}}
		]},"finish_reason":"tool_calls"}]}`},
		tools: []tool.Tool{
			{Name: "slow", Parameters: tool.ObjectParams(nil, nil), Run: func(json.RawMessage) (string, error) {
				time.Sleep(20 * time.Millisecond)
				mu.Lock()
				completed = true
				mu.Unlock()
				return `{"slow":true}`, nil
			}},
			{Name: "fast", Parameters: tool.ObjectParams(nil, nil), Run: func(json.RawMessage) (string, error) {
				return `{"fast":true}`, nil
			}},
		},
	}
	loop := &agentruntime.Loop{Conversation: conversation, Driver: driver, Events: agentruntime.NewEventLog(), MaxRounds: 2}
	if err := loop.Run(context.Background(), &agentcontext.Turn{}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !completed || len(driver.requests) != 1 || len(conversation.Messages) != 4 {
		t.Fatalf("finish did not settle tool batch: completed=%t, requests=%d, messages=%d",
			completed, len(driver.requests), len(conversation.Messages))
	}
	for i, id := range []string{"slow", "finish", "fast"} {
		if conversation.Messages[i+1].Wire.ToolCallID != id {
			t.Fatalf("tool results reordered: %#v", conversation.Messages)
		}
	}
}

func TestLoopPlainContentStaysPrivateAndStopsAfterThreeFailures(t *testing.T) {
	conversation := &agentruntime.Conversation{}
	plain := `{"choices":[{"message":{"content":"private draft"},"finish_reason":"stop"}]}`
	driver := &loopDriver{conversation: conversation, responses: []string{plain, plain, plain}}
	loop := &agentruntime.Loop{Conversation: conversation, Driver: driver, Events: agentruntime.NewEventLog(), MaxRounds: 5}
	err := loop.Run(context.Background(), &agentcontext.Turn{})
	if err == nil || !strings.Contains(err.Error(), "连续三次") || len(driver.requests) != 3 {
		t.Fatalf("protocol failure = %v, requests=%d", err, len(driver.requests))
	}
	if len(conversation.Messages) != 6 ||
		!strings.Contains(conversation.Messages[1].Wire.Content.(string), `"sent":false`) {
		t.Fatalf("missing private protocol feedback: %#v", conversation.Messages)
	}
}

func TestLoopSleepTerminatesOnlyOnSuccessAndRejectsMixedBatch(t *testing.T) {
	for _, tc := range []struct {
		name, calls string
		fail        bool
		requests    int
		actions     int
	}{
		{"success", `{"id":"s","type":"function","function":{"name":"sleep","arguments":"{}"}}`, false, 1, 1},
		{"failure", `{"id":"s","type":"function","function":{"name":"sleep","arguments":"{}"}}`, true, 2, 1},
		{"mixed", `{"id":"s","type":"function","function":{"name":"sleep","arguments":"{}"}},{"id":"a","type":"function","function":{"name":"action","arguments":"{}"}}`, false, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actions := 0
			conversation := &agentruntime.Conversation{}
			action := func(json.RawMessage) (string, error) {
				actions++
				if tc.fail {
					return "", errors.New("cannot persist")
				}
				return "{}", nil
			}
			driver := &loopDriver{conversation: conversation, tools: []tool.Tool{
				{Name: "sleep", Parameters: tool.ObjectParams(nil, nil), Run: action},
				{Name: "action", Parameters: tool.ObjectParams(nil, nil), Run: action},
			}, responses: []string{
				`{"choices":[{"message":{"tool_calls":[` + tc.calls + `]},"finish_reason":"tool_calls"}]}`,
				`{"choices":[{"message":{"tool_calls":[{"id":"f","type":"function","function":{"name":"finish","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
			}}
			loop := &agentruntime.Loop{Conversation: conversation, Driver: driver, Events: agentruntime.NewEventLog()}
			if err := loop.Run(context.Background(), &agentcontext.Turn{}); err != nil {
				t.Fatal(err)
			}
			if len(driver.requests) != tc.requests || actions != tc.actions {
				t.Fatalf("requests=%d actions=%d", len(driver.requests), actions)
			}
		})
	}
}
