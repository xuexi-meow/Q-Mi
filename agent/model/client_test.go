package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientChatUsesWireProtocol(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok","reasoning_content":"private thought"}}]}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", time.Second)
	response, err := client.Chat(context.Background(), ChatRequest{
		Model:    "test-model",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if response.TextContent() != "ok" || response.ReplayReasoning() != "private thought" {
		t.Fatalf("unexpected response: content=%q reasoning=%q", response.TextContent(), response.ReplayReasoning())
	}
	for _, key := range []string{"max_tokens", "temperature", "tools", "tool_choice"} {
		if _, ok := request[key]; ok {
			t.Errorf("zero-value field %q should be omitted", key)
		}
	}
	messages, ok := request["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %#v", request["messages"])
	}
	message, _ := messages[0].(map[string]any)
	if message["role"] != "user" || message["content"] != "hello" {
		t.Errorf("message = %#v", message)
	}
}
