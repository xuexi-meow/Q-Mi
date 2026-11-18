package context_test

import (
	"strings"
	"testing"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
)

func TestInspectionRedactsNestedSecretsWithoutRoundingIDs(t *testing.T) {
	out := agentcontext.RedactInspection(`{"chat_id":9007199254740993,"token":"secret","nested":"{\"api_key\":\"nested-secret\"}","image_url":{"url":"data:private"}}`)
	if !strings.Contains(out, "9007199254740993") || strings.Contains(out, "nested-secret") || strings.Contains(out, "data:private") ||
		strings.Contains(out, `"token":"secret"`) || !strings.Contains(out, "[redacted]") {
		t.Fatalf("inspection redaction = %s", out)
	}
}

func TestInspectionExtractsOnlyRecognizedMessageMetadata(t *testing.T) {
	var entry agentcontext.InspectionEntry
	agentcontext.InspectionMetadata(&entry, "当前时间: now\n"+`{"kind":"qq_message","chat_type":"group","chat_id":9007199254740993,"message_id":"4","user_id":7,"origin":"live"}`)
	if entry.ChatID != "9007199254740993" || entry.MessageID != "4" || entry.UserID != "7" || entry.Origin != "live" {
		t.Fatalf("message identity = %+v", entry)
	}
	var arbitrary agentcontext.InspectionEntry
	agentcontext.InspectionMetadata(&arbitrary, `{"kind":"tool_data","chat_type":"group","chat_id":8}`)
	if arbitrary.ChatID != "" {
		t.Fatalf("arbitrary tool data became message metadata: %+v", arbitrary)
	}
}

func TestInspectionCopyBoundsWithoutMutatingStoredToolCalls(t *testing.T) {
	original := agentcontext.InspectionEntry{
		Text: "长文本内容", Reasoning: "内部工作",
		ToolCalls:              []model.ToolCall{{Function: model.ToolFunction{Arguments: `{"long":"argument"}`}}},
		ArgumentsOriginalRunes: []int{19}, ArgumentsTruncated: []bool{false},
	}
	copied := agentcontext.InspectionCopy(original, 2)
	if copied.Text != "长文" || !copied.Truncated || !copied.ReasoningTruncated || !copied.ArgumentsTruncated[0] ||
		original.ToolCalls[0].Function.Arguments != `{"long":"argument"}` || original.ArgumentsTruncated[0] {
		t.Fatalf("inspection copy changed stored content or lost truncation flags: %+v / %+v", copied, original)
	}
}
