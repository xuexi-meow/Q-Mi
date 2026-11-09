package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Q-Mi/utils/config"
)

// candidate 构造一个自建接入的候选（测试用）。
func candidate(name, url, model string, caps ...string) config.ModelCandidate {
	return config.ModelCandidate{Name: name, BaseURL: url, APIKey: "k", Model: model, Capabilities: caps}
}

func TestPoolPriorityAndEnable(t *testing.T) {
	cfg := config.AgentConfig{Models: map[string]config.ModelTier{
		"chat": {Candidates: []config.ModelCandidate{
			candidate("first", "https://a.example", "m1", "tools"),
			candidate("second", "https://b.example", "m2", "tools"),
		}},
	}}
	pool := buildPool(cfg, NewClient("https://legacy.example", "k", time.Second))

	slot, tier, err := pool.pick("", modelNeed{Tools: true}, nil)
	if err != nil || slot.name != "first" || tier != "chat" {
		t.Fatalf("pick = %v/%s/%v, want first/chat", slot, tier, err)
	}

	cfg.Models["chat"].Candidates[0].Enable = boolPtr(false)
	pool = buildPool(cfg, NewClient("https://legacy.example", "k", time.Second))
	if slot, _, _ = pool.pick("", modelNeed{Tools: true}, nil); slot.name != "second" {
		t.Fatalf("disabled candidate must be skipped, got %s", slot.name)
	}

	cfg.Models["chat"].Candidates[1].Enable = boolPtr(false)
	pool = buildPool(cfg, NewClient("https://legacy.example", "k", time.Second))
	if slot, _, _ = pool.pick("", modelNeed{Tools: true}, nil); slot.name != "legacy" {
		t.Fatalf("all candidates disabled must fall back to legacy, got %s", slot.name)
	}
}

func TestPoolCapabilityFilterAndCooldown(t *testing.T) {
	cfg := config.AgentConfig{Models: map[string]config.ModelTier{
		"chat": {Candidates: []config.ModelCandidate{
			candidate("text-only", "https://a.example", "m1", "tools"),
			candidate("vision-capable", "https://b.example", "m2", "tools", "vision"),
		}},
	}}
	pool := buildPool(cfg, nil)

	slot, _, err := pool.pick("", modelNeed{Tools: true, Vision: true}, nil)
	if err != nil || slot.name != "vision-capable" {
		t.Fatalf("vision need must skip text-only candidate, got %v/%v", slot, err)
	}

	// 两个候选都不支持 vision 时不报错，而是显式失败（由调用方降级图片）。
	cfg.Models["chat"].Candidates[1].Capabilities = []string{"tools"}
	pool = buildPool(cfg, nil)
	if _, _, err := pool.pick("", modelNeed{Tools: true, Vision: true}, nil); err == nil {
		t.Fatal("no vision-capable candidate must fail selection")
	}

	// 冷却：第一个候选失败后跳到第二个。
	slot.markFailure(time.Now())
	pool = buildPool(cfg, nil)
	pool.tiers["chat"][0].markFailure(time.Now())
	if slot, _, _ := pool.pick("", modelNeed{Tools: true}, nil); slot.name != "vision-capable" {
		t.Fatalf("cooling candidate must be skipped, got %s", slot.name)
	}
}

func TestPoolWakeRoutes(t *testing.T) {
	cfg := config.AgentConfig{
		Models: map[string]config.ModelTier{
			"cheap": {Candidates: []config.ModelCandidate{candidate("cheap-1", "https://cheap.example", "m", "tools")}},
		},
		WakeRoutes: map[string]string{WakeBackground: "cheap"},
	}
	pool := buildPool(cfg, NewClient("https://legacy.example", "k", time.Second))
	slot, tier, err := pool.pick(WakeBackground, modelNeed{Tools: true}, nil)
	if err != nil || tier != "cheap" || slot.name != "cheap-1" {
		t.Fatalf("route background→cheap failed: %v/%s/%v", slot, tier, err)
	}
	if _, tier, _ := pool.pick(WakeChat, modelNeed{Tools: true}, nil); tier != "chat" {
		t.Fatalf("chat wake must stay chat tier, got %s", tier)
	}
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
		ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}}, modelNeed{Tools: true}, 2)
	if err != nil {
		t.Fatalf("failover should succeed on second candidate: %v", err)
	}
	if slot.name != "good" || tier != "chat" || resp.TextContent() != "ok" {
		t.Fatalf("unexpected result: slot=%s tier=%s content=%q", slot.name, tier, resp.TextContent())
	}
}

func TestChatDoesNotFailoverOnClientError(t *testing.T) {
	bad := newModelServer(t, http.StatusBadRequest, "", nil)
	good := newModelServer(t, http.StatusOK, okCompletion, nil)
	cfg := config.AgentConfig{Models: map[string]config.ModelTier{
		"chat": {Candidates: []config.ModelCandidate{
			candidate("bad", bad.URL, "m1", "tools"),
			candidate("good", good.URL, "m2", "tools"),
		}},
	}}
	m := &Manager{pool: buildPool(cfg, nil)}
	_, _, _, err := m.chat(context.Background(), &Turn{},
		ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}}, modelNeed{Tools: true}, 2)
	if err == nil {
		t.Fatal("400 must not be retried on another candidate")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusBadRequest {
		t.Fatalf("expected HTTPError 400, got %v", err)
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
