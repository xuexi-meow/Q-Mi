package context_test

import (
	"encoding/json"
	"reflect"
	"testing"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
)

func TestMessageKindStaysOutsideWireJSON(t *testing.T) {
	for _, kind := range []agentcontext.MessageKind{
		agentcontext.KindWork, agentcontext.KindState, agentcontext.KindSummary, agentcontext.KindActiveInput,
	} {
		message := agentcontext.FromWire(model.ChatMessage{
			Role: "user", Content: "current state", ToolCallID: "lookup",
			ReasoningContent: "replayed reasoning",
			ToolCalls: []model.ToolCall{{ID: "call", Type: "function",
				Function: model.ToolFunction{Name: "finish", Arguments: "{}"}}},
		}, kind)
		if message.Kind != kind {
			t.Fatalf("kind = %v, want %v", message.Kind, kind)
		}
		data, err := json.Marshal(agentcontext.ToWire(message))
		if err != nil {
			t.Fatal(err)
		}
		const want = `{"role":"user","content":"current state","tool_calls":[{"id":"call","type":"function","function":{"name":"finish","arguments":"{}"}}],"tool_call_id":"lookup","reasoning_content":"replayed reasoning"}`
		if string(data) != want {
			t.Fatalf("wire JSON includes metadata or lost fields: %s", data)
		}
	}
}

func TestMessageWireConversionPreservesMultimodalContent(t *testing.T) {
	wire := model.ChatMessage{Role: "user", Content: []model.ContentPart{
		{Type: "text", Text: "photo"},
		{Type: "image_url", ImageURL: &struct {
			URL string `json:"url"`
		}{URL: "data:image/png;base64,AAAA"}},
	}}
	message := agentcontext.FromWire(wire, agentcontext.KindActiveInput)
	if got := agentcontext.ToWire(message); !reflect.DeepEqual(got, wire) {
		t.Fatalf("multimodal content changed: %+v", got)
	}
}

func TestMessageSliceConversionDoesNotAliasEnvelopes(t *testing.T) {
	input := []model.ChatMessage{{Role: "user", Content: "first"}, {Role: "tool", Content: "result", ToolCallID: "a"}}
	messages := agentcontext.FromWireMessages(input)
	messages[0].Kind = agentcontext.KindState
	input[0].Content = "changed input"
	output := agentcontext.ToWireMessages(messages)
	output[0].Content = "changed output"
	if messages[0].Wire.Content != "first" || messages[0].Kind != agentcontext.KindState {
		t.Fatalf("wire conversion overwrote context metadata or content: %+v", messages[0])
	}
	if messages[1].Kind != agentcontext.KindWork || output[1].ToolCallID != "a" {
		t.Fatalf("wire order, tool receipt or default kind lost: %+v", messages)
	}
}
