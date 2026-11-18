package runtime_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
	agentruntime "Q-Mi/agent/runtime"
	"Q-Mi/agent/tool"
)

func TestExecutorRejectsInvalidArgumentsBeforeSideEffects(t *testing.T) {
	executor := &agentruntime.Executor{}
	ran := false
	tools := []tool.Tool{{
		Name: "operation", Parameters: tool.ObjectParams(map[string]any{"id": tool.IntProp("ID")}, []string{"id"}),
		Run: func(json.RawMessage) (string, error) { ran = true; return "{}", nil },
	}}
	_, err := executor.Execute(tools, model.ToolCall{Function: model.ToolFunction{
		Name: "operation", Arguments: `{"id":"not-an-integer"}`,
	}})
	if err == nil || ran {
		t.Fatalf("invalid arguments reached operation: ran=%t, err=%v", ran, err)
	}
}

func TestExecutorBackgroundResultKeepsOriginAndStripsAsync(t *testing.T) {
	results := make(chan agentruntime.BackgroundTask, 1)
	executor := &agentruntime.Executor{
		Turn: &agentcontext.Turn{ChatType: "private", ChatID: 8, SelfID: 7},
		Tasks: agentruntime.NewTaskManager(func(task agentruntime.BackgroundTask) {
			results <- task
		}),
	}
	tools := []tool.Tool{{
		Name: "operation", Asyncable: true, Parameters: tool.ObjectParams(nil, nil),
		Run: func(args json.RawMessage) (string, error) {
			if strings.Contains(string(args), "_async") {
				t.Error("reserved async flag reached operation")
			}
			return `{"done":true}`, nil
		},
	}}
	receipt, err := executor.Execute(tools, model.ToolCall{Function: model.ToolFunction{
		Name: "operation", Arguments: `{"_async":true}`,
	}})
	if err != nil || !strings.Contains(receipt, `"status":"started"`) {
		t.Fatalf("receipt = %s, %v", receipt, err)
	}
	select {
	case task := <-results:
		if task.ChatType != "private" || task.ChatID != 8 || task.SelfID != 7 || task.Result != `{"done":true}` {
			t.Fatalf("background result = %#v", task)
		}
	case <-time.After(time.Second):
		t.Fatal("background result missing")
	}
}
