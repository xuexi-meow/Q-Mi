package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeOpenAI 起一个假 OpenAI 服务，记录收到的请求并返回给定响应。
func fakeOpenAI(t *testing.T, status int, body string) (*httptest.Server, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		captured.path = r.URL.Path
		captured.method = r.Method
		captured.authorization = r.Header.Get("Authorization")
		captured.contentType = r.Header.Get("Content-Type")
		captured.body = raw
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

type capturedRequest struct {
	path          string
	method        string
	authorization string
	contentType   string
	body          []byte
}

func (c *capturedRequest) jsonBody(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(c.body, &m); err != nil {
		t.Fatalf("captured body is not JSON: %v (body=%s)", err, c.body)
	}
	return m
}

const okResponseJSON = `{
  "choices": [
    {
      "index": 0,
      "message": {"role": "assistant", "content": "你好，我是 Q-Mi"}
    }
  ]
}`

func TestChatSuccess(t *testing.T) {
	srv, captured := fakeOpenAI(t, http.StatusOK, okResponseJSON)
	client := NewClient(srv.URL, "sk-test", 5*time.Second)

	resp, err := client.Chat(context.Background(), ChatRequest{
		Model: "deepseek-chat",
		Messages: []ChatMessage{
			{Role: "system", Content: "你是 QQ 上的 AI 成员"},
			{Role: "user", Content: "在吗"},
		},
		MaxTokens:   512,
		Temperature: 0.8,
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp == nil || resp.Content() != "你好，我是 Q-Mi" {
		t.Fatalf("Chat = %+v, want content %q", resp, "你好，我是 Q-Mi")
	}

	if captured.method != http.MethodPost {
		t.Errorf("method = %q, want POST", captured.method)
	}
	if captured.path != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", captured.path)
	}
	if captured.authorization != "Bearer sk-test" {
		t.Errorf("authorization = %q, want %q", captured.authorization, "Bearer sk-test")
	}
	if captured.contentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", captured.contentType)
	}

	body := captured.jsonBody(t)
	if body["model"] != "deepseek-chat" {
		t.Errorf("model = %v, want deepseek-chat", body["model"])
	}
	if body["max_tokens"] != float64(512) {
		t.Errorf("max_tokens = %v, want 512", body["max_tokens"])
	}
	if body["temperature"] != 0.8 {
		t.Errorf("temperature = %v, want 0.8", body["temperature"])
	}
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages = %v, want 2 entries", body["messages"])
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "你是 QQ 上的 AI 成员" {
		t.Errorf("messages[0] = %v, want system/你是 QQ 上的 AI 成员", first)
	}
	second, _ := msgs[1].(map[string]any)
	if second["role"] != "user" || second["content"] != "在吗" {
		t.Errorf("messages[1] = %v, want user/在吗", second)
	}
}

func TestChatOmitsZeroValueFields(t *testing.T) {
	srv, captured := fakeOpenAI(t, http.StatusOK, okResponseJSON)
	client := NewClient(srv.URL, "sk-test", 5*time.Second)

	if _, err := client.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-chat",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	body := captured.jsonBody(t)
	for _, key := range []string{"max_tokens", "temperature"} {
		if _, present := body[key]; present {
			t.Errorf("zero-value field %q must be omitted from request body, got %v", key, body)
		}
	}
	if body["model"] != "deepseek-chat" {
		t.Errorf("model = %v, want deepseek-chat", body["model"])
	}
}

func TestChatHTTPError(t *testing.T) {
	srv, _ := fakeOpenAI(t, http.StatusInternalServerError, `{"error": {"message": "boom"}}`)
	client := NewClient(srv.URL, "sk-test", 5*time.Second)

	resp, err := client.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-chat",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatalf("Chat must fail on HTTP 500, got resp %+v", resp)
	}
	if resp != nil {
		t.Errorf("resp = %+v, want nil on error", resp)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q should mention HTTP status 500", err)
	}
}

func TestChatBadJSON(t *testing.T) {
	srv, _ := fakeOpenAI(t, http.StatusOK, "<html>not json</html>")
	client := NewClient(srv.URL, "sk-test", 5*time.Second)

	resp, err := client.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-chat",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatalf("Chat must fail on invalid JSON body, got resp %+v", resp)
	}
	if resp != nil {
		t.Errorf("resp = %+v, want nil on error", resp)
	}
}

func TestChatTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(okResponseJSON))
	}))
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL, "sk-test", 50*time.Millisecond)

	resp, err := client.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-chat",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatalf("Chat must fail on timeout, got resp %+v", resp)
	}
	if resp != nil {
		t.Errorf("resp = %+v, want nil on error", resp)
	}
}

func TestChatEmptyChoices(t *testing.T) {
	srv, _ := fakeOpenAI(t, http.StatusOK, `{"choices": []}`)
	client := NewClient(srv.URL, "sk-test", 5*time.Second)

	resp, err := client.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-chat",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatalf("Chat must fail when choices is empty, got resp %+v", resp)
	}
	if resp != nil {
		t.Errorf("resp = %+v, want nil on error", resp)
	}
}
