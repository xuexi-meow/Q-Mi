package model_test

import (
	"encoding/json"
	"testing"

	"Q-Mi/agent/model"
)

func TestResponseMessagePreservesReasoningReplay(t *testing.T) {
	response := &model.ChatResponse{}
	response.Choices = append(response.Choices, struct {
		Index        int                   `json:"index"`
		Message      model.ResponseMessage `json:"message"`
		FinishReason string                `json:"finish_reason"`
	}{Message: model.ResponseMessage{
		Content: "private work", ReasoningContent: "reasoning", Reasoning: "fallback",
	}, FinishReason: "tool_calls"})
	raw, err := json.Marshal(response)
	if err != nil || !json.Valid(raw) || response.ReplayReasoning() != "reasoning" ||
		response.ReasoningContent() != "reasoning" || response.TextContent() != "private work" {
		t.Fatalf("response round trip = %s, %v", raw, err)
	}
}
