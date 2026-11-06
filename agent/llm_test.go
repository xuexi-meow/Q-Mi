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
	for _, key := range []string{"max_tokens", "temperature", "tools"} {
		if _, present := body[key]; present {
			t.Errorf("zero-value field %q must be omitted from request body, got %v", key, body)
		}
	}
	if body["model"] != "deepseek-chat" {
		t.Errorf("model = %v, want deepseek-chat", body["model"])
	}
}

// toolCallResponseJSON 模拟 DeepSeek/OpenAI 风格的 tool_calls 响应。
const toolCallResponseJSON = `{
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "",
        "reasoning_content": "用户在问最近聊了什么，先查一下",
        "tool_calls": [
          {
            "id": "call_abc",
            "type": "function",
            "function": {"name": "get_recent_messages", "arguments": "{\"limit\":5}"}
          }
        ]
      }
    }
  ]
}`

// TestChatSendsToolsAndParsesToolCalls 请求携带 tools / assistant.tool_calls /
// tool(tool_call_id) 时按 OpenAI 格式序列化；响应解析 message.tool_calls 与
// reasoning_content。
func TestChatSendsToolsAndParsesToolCalls(t *testing.T) {
	srv, captured := fakeOpenAI(t, http.StatusOK, toolCallResponseJSON)
	client := NewClient(srv.URL, "sk-test", 5*time.Second)

	req := ChatRequest{
		Model: "deepseek-chat",
		Messages: []ChatMessage{
			{Role: "system", Content: "sys"},
			{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID:   "call_prev",
					Type: "function",
					Function: ToolFunction{
						Name:      "think",
						Arguments: `{"note":"先查历史"}`,
					},
				}},
			},
			{Role: "tool", ToolCallID: "call_prev", Content: "{}"},
		},
		Tools: []ToolDef{{
			Type: "function",
			Function: ToolSchema{
				Name:        "think",
				Description: "记录思考笔记",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
	}
	resp, err := client.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	// 请求体：tools 数组
	body := captured.jsonBody(t)
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want 1 个工具定义", body["tools"])
	}
	td, _ := tools[0].(map[string]any)
	if td["type"] != "function" {
		t.Errorf("tools[0].type = %v, want function", td["type"])
	}
	fn, _ := td["function"].(map[string]any)
	if fn["name"] != "think" || fn["description"] != "记录思考笔记" {
		t.Errorf("tools[0].function = %v, want think/记录思考笔记", fn)
	}
	params, _ := fn["parameters"].(map[string]any)
	if params["type"] != "object" {
		t.Errorf("tools[0].function.parameters = %v, want {type:object}", fn["parameters"])
	}

	// 请求体：assistant.tool_calls 与 tool.tool_call_id
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %d 条, want 3", len(msgs))
	}
	asst, _ := msgs[1].(map[string]any)
	calls, ok := asst["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("assistant.tool_calls = %v, want 1 个调用", asst["tool_calls"])
	}
	c0, _ := calls[0].(map[string]any)
	if c0["id"] != "call_prev" || c0["type"] != "function" {
		t.Errorf("tool_calls[0] = %v, want id=call_prev type=function", c0)
	}
	c0fn, _ := c0["function"].(map[string]any)
	if c0fn["name"] != "think" || c0fn["arguments"] != `{"note":"先查历史"}` {
		t.Errorf("tool_calls[0].function = %v, want think/{\"note\":\"先查历史\"}", c0fn)
	}
	toolMsg, _ := msgs[2].(map[string]any)
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "call_prev" || toolMsg["content"] != "{}" {
		t.Errorf("messages[2] = %v, want tool/call_prev/{}", toolMsg)
	}

	// 响应：tool_calls 与 reasoning_content
	calls2 := resp.ToolCalls()
	if len(calls2) != 1 {
		t.Fatalf("resp.ToolCalls() = %d 个, want 1", len(calls2))
	}
	if calls2[0].ID != "call_abc" || calls2[0].Type != "function" {
		t.Errorf("ToolCalls[0] = %+v, want id=call_abc type=function", calls2[0])
	}
	if calls2[0].Function.Name != "get_recent_messages" || calls2[0].Function.Arguments != `{"limit":5}` {
		t.Errorf("ToolCalls[0].Function = %+v, want get_recent_messages/{\"limit\":5}", calls2[0].Function)
	}
	if got := resp.ReasoningContent(); got != "用户在问最近聊了什么，先查一下" {
		t.Errorf("ReasoningContent = %q, want 用户在问最近聊了什么，先查一下", got)
	}
}

// TestChatResponsePlainContent 纯文本响应：ToolCalls 为空、ReasoningContent 为空。
func TestChatResponsePlainContent(t *testing.T) {
	srv, _ := fakeOpenAI(t, http.StatusOK, okResponseJSON)
	client := NewClient(srv.URL, "sk-test", 5*time.Second)

	resp, err := client.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-chat",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if len(resp.ToolCalls()) != 0 {
		t.Errorf("纯文本响应不应有 tool_calls: %+v", resp.ToolCalls())
	}
	if resp.ReasoningContent() != "" {
		t.Errorf("ReasoningContent = %q, want 空", resp.ReasoningContent())
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
