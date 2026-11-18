package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
	"Q-Mi/utils/config"
)

// candidate 构造一个自建接入的候选（测试用）。
func candidate(name, url, model string, caps ...string) config.ModelCandidate {
	return config.ModelCandidate{Name: name, BaseURL: url, APIKey: "k", Model: model, Capabilities: caps}
}

// newModelServer 起一个返回固定状态码/响应体的模型服务；bodyCh 可选收集请求体。
func newModelServer(t *testing.T, status int, body string, bodyCh chan<- []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bodyCh != nil {
			var buf []byte
			dec := json.NewDecoder(r.Body)
			var raw any
			_ = dec.Decode(&raw)
			buf, _ = json.Marshal(raw)
			select {
			case bodyCh <- buf:
			default:
			}
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const okCompletion = `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`

func TestChatFailoverOnServerError(t *testing.T) {
	bad := newModelServer(t, http.StatusInternalServerError, "", nil)
	good := newModelServer(t, http.StatusOK, okCompletion, nil)
	cfg := config.AgentConfig{Models: map[string]config.ModelTier{
		"chat": {Candidates: []config.ModelCandidate{
			candidate("bad", bad.URL, "m1", "tools"),
			candidate("good", good.URL, "m2", "tools"),
		}},
	}}
	m := &Manager{pool: buildPool(cfg, nil)}
	resp, slot, tier, err := m.chat(context.Background(), &Turn{},
		ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}}, model.Need{Tools: true}, 2)
	if err != nil {
		t.Fatalf("failover should succeed on second candidate: %v", err)
	}
	if slot.Name() != "good" || tier != "chat" || resp.TextContent() != "ok" {
		t.Fatalf("unexpected result: slot=%s tier=%s content=%q", slot.Name(), tier, resp.TextContent())
	}
}

func TestChatRetriesImageSizeLimitAsTextOnly(t *testing.T) {
	var requests []ChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests = append(requests, req)
		if len(requests) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"image call nova inspection failed: over image size limit 10","code":1902}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okCompletion))
	}))
	t.Cleanup(server.Close)

	cfg := config.AgentConfig{Models: map[string]config.ModelTier{
		"chat": {Candidates: []config.ModelCandidate{candidate("vision", server.URL, "m", "tools", "vision")}},
	}}
	m := &Manager{pool: buildPool(cfg, nil)}
	req := ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: []ContentPart{
			{Type: "text", Text: "请看这张图"},
			imagePart("data:image/png;base64,AAAA"),
		}}},
		Tools: []ToolDef{{Type: "function", Function: ToolSchema{Name: "finish"}}},
	}

	resp, _, _, err := m.chat(context.Background(), &Turn{}, req, model.Need{Tools: true, Vision: true}, 1)
	if err != nil {
		t.Fatalf("text-only retry should recover from provider image limit: %v", err)
	}
	if resp.TextContent() != "ok" || len(requests) != 2 {
		t.Fatalf("response=%v, request count=%d; want successful retry after one image rejection", resp, len(requests))
	}
	parts, ok := requests[1].Messages[0].Content.([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("retry content = %#v, want original text and omitted-image notice", requests[1].Messages[0].Content)
	}
	for i, part := range parts {
		item, ok := part.(map[string]any)
		if !ok || item["type"] != "text" {
			t.Fatalf("retry content part %d = %#v, want text part", i, part)
		}
	}
	if len(requests[1].Tools) != 1 || requests[1].Model != "m" {
		t.Fatalf("retry lost model/tool request settings: %+v", requests[1])
	}
}

// TestChatFailoverOnAccountErrors 账号/凭据/限流类错误换候选就能成功。
func TestChatFailoverOnAccountErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			bad := newModelServer(t, status, "", nil)
			good := newModelServer(t, http.StatusOK, okCompletion, nil)
			cfg := config.AgentConfig{Models: map[string]config.ModelTier{
				"chat": {Candidates: []config.ModelCandidate{
					candidate("bad", bad.URL, "m1", "tools"),
					candidate("good", good.URL, "m2", "tools"),
				}},
			}}
			m := &Manager{pool: buildPool(cfg, nil)}
			resp, slot, _, err := m.chat(context.Background(), &Turn{},
				ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}}, model.Need{Tools: true}, 2)
			if err != nil {
				t.Fatalf("status %d should fail over to next candidate: %v", status, err)
			}
			if slot.Name() != "good" || resp.TextContent() != "ok" {
				t.Fatalf("status %d picked %s/%q", status, slot.Name(), resp.TextContent())
			}
		})
	}
}

func TestChatDoesNotFailoverOnClientError(t *testing.T) {
	bad := newModelServer(t, http.StatusBadRequest, "", nil)
	var fallbackRequests atomic.Int32
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackRequests.Add(1)
		_, _ = w.Write([]byte(okCompletion))
	}))
	t.Cleanup(good.Close)
	cfg := config.AgentConfig{Models: map[string]config.ModelTier{
		"chat": {Candidates: []config.ModelCandidate{
			candidate("bad", bad.URL, "m1", "tools"),
			candidate("good", good.URL, "m2", "tools"),
		}},
	}}
	m := &Manager{pool: buildPool(cfg, nil)}
	_, _, _, err := m.chat(context.Background(), &Turn{},
		ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}}, model.Need{Tools: true}, 2)
	if err == nil {
		t.Fatal("400 must not be retried on another candidate")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusBadRequest {
		t.Fatalf("expected HTTPError 400, got %v", err)
	}
	if got := fallbackRequests.Load(); got != 0 {
		t.Fatalf("400 must not reach the fallback candidate, got %d requests", got)
	}
}

func TestChatStripsImagesForTextOnlyCandidate(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	srv := newModelServer(t, http.StatusOK, okCompletion, bodyCh)
	cfg := config.AgentConfig{Models: map[string]config.ModelTier{
		"chat": {Candidates: []config.ModelCandidate{candidate("text", srv.URL, "m", "tools")}},
	}}
	m := &Manager{pool: buildPool(cfg, nil)}
	msgs := []ChatMessage{{Role: "user", Content: []ContentPart{
		{Type: "text", Text: "看图"},
		imagePart("data:image/png;base64,AAAA"),
	}}}
	if _, _, _, err := m.chat(context.Background(), &Turn{}, ChatRequest{Messages: msgs},
		needForMessages(msgs), 1); err != nil {
		t.Fatalf("chat: %v", err)
	}
	select {
	case body := <-bodyCh:
		if strings.Contains(string(body), "image_url") {
			t.Fatalf("text-only candidate must not receive image parts: %s", body)
		}
		if !strings.Contains(string(body), "图片已省略") {
			t.Fatalf("stripped message should note omitted images: %s", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("model server did not receive request")
	}
}

func TestStripImagesPreservesContextMetadata(t *testing.T) {
	original := []agentcontext.Message{agentcontext.FromWire(ChatMessage{
		Role: "user", ReasoningContent: "private",
		Content: []ContentPart{{Type: "text", Text: "photo"}, imagePart("data:image/png;base64,AAAA")},
	}, agentcontext.KindActiveInput)}
	stripped := append([]agentcontext.Message(nil), original...)
	stripped[0].Wire = stripImageParts([]ChatMessage{stripped[0].Wire})[0]
	if stripped[0].Kind != agentcontext.KindActiveInput || stripped[0].Wire.ReasoningContent != "private" {
		t.Fatalf("image fallback lost context metadata: %+v", stripped[0])
	}
	if parts := original[0].Wire.Content.([]ContentPart); parts[1].Type != "image_url" {
		t.Fatal("image fallback mutated working context")
	}
	if parts := stripped[0].Wire.Content.([]ContentPart); parts[1].Type != "text" {
		t.Fatal("image fallback did not remove the image")
	}
}

func TestUsageByModelSnapshot(t *testing.T) {
	m := &Manager{}
	m.recordUsageFor("deepseek", Usage{PromptTokens: 10, CompletionTokens: 2, PromptCacheHitTokens: 8})
	m.recordUsageFor("deepseek", Usage{PromptTokens: 5, CompletionTokens: 1, PromptCacheMissTokens: 5})
	m.recordUsageFor("glm", Usage{PromptTokens: 3, CompletionTokens: 1})

	byModel := m.UsageByModel()
	if byModel["deepseek"].Requests != 2 || byModel["deepseek"].PromptTokens != 15 ||
		byModel["deepseek"].CacheHit != 8 || byModel["deepseek"].CacheMiss != 5 {
		t.Fatalf("deepseek usage = %+v", byModel["deepseek"])
	}
	if byModel["glm"].Requests != 1 || byModel["glm"].PromptTokens != 3 {
		t.Fatalf("glm usage = %+v", byModel["glm"])
	}
	if m.usage.Requests != 3 {
		t.Fatalf("aggregate requests = %d, want 3", m.usage.Requests)
	}
}
