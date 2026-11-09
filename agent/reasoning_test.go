package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestToolLoopPassesBackReasoningContent 思考模式 + tools：模型给出的
// reasoning_content 必须随 assistant 消息回传，否则下一次请求会被 API 拒绝。
func TestToolLoopPassesBackReasoningContent(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(time.Millisecond)
	script := []string{
		reasoningToolCallResp("先记一下", "", "c1", "think", `{"note":"x"}`),
		toolCallResp("c2", "finish", "{}"),
	}
	m, f, _ := newLoopManager(t, cfg, b, nil, script)
	m.notesPath = t.TempDir() + "/notes.json"

	m.Enqueue(msg("group", 888, "在吗"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 2 }, "应发出 2 次 LLM 请求")

	reqs := requestsSnapshot(f)
	var assistant *ChatMessage
	for i := range reqs[1].Messages {
		if reqs[1].Messages[i].Role == "assistant" {
			assistant = &reqs[1].Messages[i]
		}
	}
	if assistant == nil {
		t.Fatal("第二次请求缺少 assistant 消息")
	}
	if assistant.ReasoningContent != "先记一下" {
		t.Fatalf("assistant reasoning_content = %q, want 先记一下", assistant.ReasoningContent)
	}
	if len(assistant.ToolCalls) == 0 {
		t.Fatal("assistant 消息应保留 tool_calls")
	}
}

func TestReasoningContentWireFormat(t *testing.T) {
	data, err := json.Marshal(ChatMessage{Role: "assistant", Content: "ok", ReasoningContent: "想一想"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"reasoning_content":"想一想"`) {
		t.Fatalf("wire 里缺少 reasoning_content: %s", data)
	}

	empty, err := json.Marshal(ChatMessage{Role: "assistant", Content: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(empty), "reasoning_content") {
		t.Fatalf("空 reasoning 不应序列化: %s", empty)
	}
}

func TestContextEstimateCountsReasoning(t *testing.T) {
	base := []ChatMessage{{Role: "assistant", Content: "ok"}}
	withReasoning := []ChatMessage{{
		Role: "assistant", Content: "ok",
		ReasoningContent: strings.Repeat("想", 300),
	}}
	before := estimatedInputTokens(base, nil)
	after := estimatedInputTokens(withReasoning, nil)
	if after <= before {
		t.Fatalf("reasoning 必须计入预算: before=%d after=%d", before, after)
	}
}
