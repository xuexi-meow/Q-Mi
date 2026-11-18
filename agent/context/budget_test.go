package context_test

import (
	"strings"
	"testing"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
)

func TestEstimatedTokensIncludeImagesReasoningAndToolCalls(t *testing.T) {
	base := model.ChatMessage{Role: "assistant", Content: "abc"}
	if got := agentcontext.EstimatedInputTokens([]model.ChatMessage{base}, nil); got != 17 {
		t.Fatalf("plain token estimate = %d, want 17", got)
	}
	base.ReasoningContent = "中文"
	base.Content = []model.ContentPart{{Type: "text", Text: "abc"}, {Type: "image_url"}}
	if got := agentcontext.EstimatedInputTokens([]model.ChatMessage{base}, nil); got != 2069 {
		t.Fatalf("vision/reasoning estimate = %d, want 2069", got)
	}
	before := agentcontext.EstimatedInputTokens([]model.ChatMessage{base}, nil)
	base.ToolCalls = []model.ToolCall{{ID: "x", Type: "function", Function: model.ToolFunction{Name: "read", Arguments: `{}`}}}
	if got := agentcontext.EstimatedInputTokens([]model.ChatMessage{base}, nil); got <= before {
		t.Fatal("tool call payload was omitted from the estimate")
	}
}

func TestSummaryTranscriptBoundsTextAndOmitsImageBytes(t *testing.T) {
	messages := []model.ChatMessage{{Role: "user", Content: []model.ContentPart{
		{Type: "text", Text: strings.Repeat("evidence ", 400)},
		{Type: "image_url", ImageURL: &struct {
			URL string `json:"url"`
		}{URL: "data:image/png;base64,PRIVATE"}},
	}}}
	out := agentcontext.SummaryTranscript(messages, 80)
	if agentcontext.TextTokens(out) > 80 || strings.Contains(out, "PRIVATE") || !strings.Contains(out, "图片") {
		t.Fatalf("summary transcript exceeded budget or exposed bytes: %q", out)
	}
}
