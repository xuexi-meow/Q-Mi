package model_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"Q-Mi/agent/model"
	"Q-Mi/utils/config"
)

func candidate(name string, capabilities ...string) config.ModelCandidate {
	return config.ModelCandidate{
		Name: name, BaseURL: "https://example.test", APIKey: "test-key",
		Model: name + "-model", Capabilities: capabilities,
	}
}

func pick(t *testing.T, pool *model.Pool, wake string, need model.Need, skip map[string]bool) (*model.Candidate, string) {
	t.Helper()
	slot, tier, err := pool.Pick(wake, need, skip)
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	return slot, tier
}

func TestPoolPriorityEnableAndSkip(t *testing.T) {
	disabled := false
	first := candidate("disabled", "tools")
	first.Enable = &disabled
	cfg := config.AgentConfig{
		ToolChoice: "required",
		Models: map[string]config.ModelTier{"chat": {Candidates: []config.ModelCandidate{
			first, candidate("primary", "tools"), candidate("secondary", "tools"),
		}}},
	}
	pool := model.NewPool(cfg, nil)
	slot, tier := pick(t, pool, "", model.Need{Tools: true}, nil)
	if slot.Name() != "primary" || slot.Model() != "primary-model" || tier != "chat" || slot.ToolChoice() != "required" {
		t.Fatalf("unexpected primary selection: %s/%s/%s/%s", slot.Name(), slot.Model(), tier, slot.ToolChoice())
	}
	slot, _ = pick(t, pool, "", model.Need{Tools: true}, map[string]bool{slot.Key(): true})
	if slot.Name() != "secondary" {
		t.Fatalf("skip selected %q, want secondary", slot.Name())
	}
}

func TestPoolCapabilityFilter(t *testing.T) {
	cfg := config.AgentConfig{Models: map[string]config.ModelTier{"chat": {Candidates: []config.ModelCandidate{
		candidate("text", "tools"), candidate("vision", " TOOLS ", " Vision "), candidate("video", "video"),
	}}}}
	pool := model.NewPool(cfg, nil)
	for _, tc := range []struct {
		name string
		need model.Need
		want string
	}{
		{"tools", model.Need{Tools: true}, "text"},
		{"vision", model.Need{Tools: true, Vision: true}, "vision"},
		{"video", model.Need{Video: true}, "video"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slot, _ := pick(t, pool, "chat", tc.need, nil)
			if slot.Name() != tc.want {
				t.Fatalf("selected %q, want %q", slot.Name(), tc.want)
			}
		})
	}
	if _, _, err := pool.Pick("chat", model.Need{Tools: true, Video: true}, nil); !errors.Is(err, model.ErrNoModel) {
		t.Fatalf("unsupported capabilities: %v", err)
	}
}

func TestPoolCooldownAndExpiry(t *testing.T) {
	cfg := config.AgentConfig{Models: map[string]config.ModelTier{"chat": {Candidates: []config.ModelCandidate{
		candidate("primary", "tools"), candidate("secondary", "tools"),
	}}}}
	pool := model.NewPool(cfg, nil)
	primary, _ := pick(t, pool, "chat", model.Need{Tools: true}, nil)
	primary.MarkFailure(time.Now())
	slot, _ := pick(t, pool, "chat", model.Need{Tools: true}, nil)
	if slot.Name() != "secondary" {
		t.Fatalf("cooling primary selected: %s", slot.Name())
	}
	// 无需等待真实冷却时间；失败时间已在默认冷却窗口之前。
	primary.MarkFailure(time.Now().Add(-6 * time.Minute))
	primary.MarkSuccess()
	slot, _ = pick(t, pool, "chat", model.Need{Tools: true}, nil)
	if slot != primary {
		t.Fatal("expired primary did not become eligible again")
	}
}

func TestPoolConfiguredCooldown(t *testing.T) {
	for _, tc := range []struct {
		cooldown string
		want     string
	}{
		{"0s", "primary"},
		{"1h", "secondary"},
		{"invalid", "secondary"},
	} {
		t.Run(tc.cooldown, func(t *testing.T) {
			primary := candidate("primary", "tools")
			primary.Cooldown = tc.cooldown
			pool := model.NewPool(config.AgentConfig{Models: map[string]config.ModelTier{
				"chat": {Candidates: []config.ModelCandidate{primary, candidate("secondary", "tools")}},
			}}, nil)
			slot, _ := pick(t, pool, "chat", model.Need{Tools: true}, nil)
			slot.MarkFailure(time.Now())
			slot, _ = pick(t, pool, "chat", model.Need{Tools: true}, nil)
			if slot.Name() != tc.want {
				t.Fatalf("selected %q, want %q", slot.Name(), tc.want)
			}
		})
	}
}

func TestPoolRoutesAndFallsBackToLegacy(t *testing.T) {
	legacy := model.NewClient("https://legacy.test", "key", time.Second)
	cfg := config.AgentConfig{
		Model: "legacy-model",
		Models: map[string]config.ModelTier{
			"cheap": {Candidates: []config.ModelCandidate{candidate("cheap", "tools")}},
		},
		WakeRoutes: map[string]string{"background": "cheap"},
	}
	pool := model.NewPool(cfg, legacy)
	slot, tier := pick(t, pool, "background", model.Need{Tools: true}, nil)
	if tier != "cheap" || slot.Name() != "cheap" || slot.Client() == legacy {
		t.Fatalf("unexpected routed selection: %s/%s", slot.Name(), tier)
	}
	slot.MarkFailure(time.Now())
	slot, tier = pick(t, pool, "background", model.Need{Tools: true}, nil)
	if tier != "chat" || slot.Name() != "legacy" || slot.Client() != legacy {
		t.Fatalf("expected legacy fallback, got %s/%s", slot.Name(), tier)
	}
	slot, tier = pick(t, pool, "unknown", model.Need{Tools: true, Vision: true}, nil)
	if tier != "chat" || slot.Client() != legacy {
		t.Fatal("legacy without capability declarations must remain unrestricted")
	}
	if _, _, err := pool.Pick("chat", model.Need{}, map[string]bool{slot.Key(): true}); !errors.Is(err, model.ErrNoModel) {
		t.Fatalf("exhausted legacy: %v", err)
	}
}

func TestPoolLegacyConfigurationAndExplicitChat(t *testing.T) {
	legacy := model.NewClient("https://legacy.test", "key", time.Second)
	disabled := false
	for _, tc := range []struct {
		name string
		cfg  config.AgentConfig
		want string
	}{
		{"legacy", config.AgentConfig{}, "legacy"},
		{"disabled tier", config.AgentConfig{Models: map[string]config.ModelTier{
			"chat": {Enable: &disabled},
		}}, "legacy"},
		{"disabled candidate", config.AgentConfig{Models: map[string]config.ModelTier{
			"chat": {Candidates: []config.ModelCandidate{{Name: "disabled", Enable: &disabled}}},
		}}, "legacy"},
		{"explicit chat", config.AgentConfig{Models: map[string]config.ModelTier{
			"chat": {BaseURL: "https://chat.test", APIKey: "key", Model: "explicit"},
		}}, "explicit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := model.NewPool(tc.cfg, legacy)
			slot, tier := pick(t, pool, "background", model.Need{Tools: true}, nil)
			if tier != "chat" {
				t.Fatalf("tier = %q", tier)
			}
			if tc.want == "legacy" {
				if slot.Name() != "legacy" || slot.Client() != legacy {
					t.Fatal("expected legacy client")
				}
			} else if slot.Model() != "explicit" || slot.Client() == legacy {
				t.Fatal("explicit chat must replace legacy client")
			}
			if _, _, err := pool.Pick("chat", model.Need{}, map[string]bool{slot.Key(): true}); !errors.Is(err, model.ErrNoModel) {
				t.Fatalf("single candidate should be exhausted: %v", err)
			}
		})
	}
}

func TestPoolCandidateCredentials(t *testing.T) {
	t.Setenv("QMI_POOL_TEST_KEY", "")
	missing := candidate("missing", "tools")
	missing.APIKey, missing.APIKeyEnv = "", "QMI_POOL_TEST_KEY"
	inherited := config.ModelCandidate{Name: "inherited", Model: "inherited-model"}
	explicit := candidate("explicit", "tools")
	explicit.ToolChoice = "auto"
	legacy := model.NewClient("https://legacy.test", "key", time.Second)
	cfg := config.AgentConfig{ToolChoice: "required", Models: map[string]config.ModelTier{
		"chat": {Candidates: []config.ModelCandidate{missing, inherited, explicit}},
	}}
	slot, _ := pick(t, model.NewPool(cfg, legacy), "", model.Need{Tools: true}, nil)
	if slot.Name() != "inherited" || slot.Client() != legacy {
		t.Fatal("missing key must be skipped; empty endpoint/key must reuse legacy")
	}
	t.Setenv("QMI_POOL_TEST_KEY", "env-key")
	slot, _ = pick(t, model.NewPool(cfg, legacy), "", model.Need{Tools: true}, nil)
	if slot.Name() != "missing" || slot.Client() == legacy {
		t.Fatal("environment key must enable an independent client")
	}
	slot, _ = pick(t, model.NewPool(cfg, legacy), "", model.Need{Tools: true}, map[string]bool{
		"missing@missing-model": true, "inherited@inherited-model": true,
	})
	if slot.Name() != "explicit" || slot.ToolChoice() != "auto" {
		t.Fatal("candidate tool choice must override the top-level default")
	}
}

func TestPoolCandidateClientSendsConfiguredKey(t *testing.T) {
	t.Setenv("QMI_POOL_TEST_KEY", "environment-key")
	for _, tc := range []struct {
		name, key, want string
	}{
		{"environment", "", "Bearer environment-key"},
		{"explicit overrides environment", "explicit-key", "Bearer explicit-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headers <- r.Header.Get("Authorization")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
			}))
			t.Cleanup(server.Close)
			pool := model.NewPool(config.AgentConfig{Models: map[string]config.ModelTier{
				"chat": {Candidates: []config.ModelCandidate{{
					Name: "configured", Model: "configured-model", BaseURL: server.URL,
					APIKey: tc.key, APIKeyEnv: "QMI_POOL_TEST_KEY",
				}}},
			}}, nil)
			slot, _ := pick(t, pool, "chat", model.Need{}, nil)
			response, err := slot.Client().Chat(context.Background(), model.ChatRequest{
				Model: slot.Model(), Messages: []model.ChatMessage{{Role: "user", Content: "hi"}},
			})
			if err != nil || response.TextContent() != "ok" {
				t.Fatalf("configured client request failed: %v", err)
			}
			if got := <-headers; got != tc.want {
				t.Fatalf("authorization = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPoolNilAndUnavailableClients(t *testing.T) {
	for _, pool := range []*model.Pool{nil, model.NewPool(config.AgentConfig{}, nil)} {
		if _, _, err := pool.Pick("", model.Need{}, nil); !errors.Is(err, model.ErrNoModel) {
			t.Fatalf("unavailable pool: %v", err)
		}
	}
}

func TestRetryableError(t *testing.T) {
	for _, status := range []int{400, 401, 402, 403, 404, 422, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			err := fmt.Errorf("wrapped: %w", &model.HTTPError{Status: status})
			want := status == 401 || status == 402 || status == 403 || status == 429 || status >= 500
			if got := model.IsRetryableError(err); got != want {
				t.Fatalf("retryable = %v, want %v", got, want)
			}
		})
	}
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, false}, {errors.New("invalid request"), false}, {context.Canceled, false},
		{fmt.Errorf("wrapped: %w", context.DeadlineExceeded), true},
		{&net.DNSError{Err: "lookup failed"}, true},
	} {
		if got := model.IsRetryableError(tc.err); got != tc.want {
			t.Fatalf("retryable(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestNeedAndStripImagesDoNotMutateMessages(t *testing.T) {
	image := model.ContentPart{Type: "image_url", ImageURL: &struct {
		URL string `json:"url"`
	}{URL: "data:image/png;base64,AAAA"}}
	parts := []model.ContentPart{{Type: "text", Text: "photo"}, image}
	messages := []model.ChatMessage{{
		Role: "assistant", Content: parts, ToolCallID: "call-1",
		ReasoningContent: "private", ToolCalls: []model.ToolCall{{ID: "call-1"}},
	}, {Role: "tool", Content: "{}"}}
	if got := model.NeedForMessages(messages); got != (model.Need{Tools: true, Vision: true}) {
		t.Fatalf("need = %+v", got)
	}
	stripped := model.StripImageParts(messages)
	wantParts := []model.ContentPart{{Type: "text", Text: "photo"}, {Type: "text", Text: "（1 张图片已省略）"}}
	if !reflect.DeepEqual(stripped[0].Content, wantParts) {
		t.Fatalf("stripped content = %#v", stripped[0].Content)
	}
	if !reflect.DeepEqual(messages[0].Content, parts) {
		t.Fatal("original image content was mutated")
	}
	if stripped[0].ReasoningContent != "private" || stripped[0].ToolCallID != "call-1" || stripped[0].ToolCalls[0].ID != "call-1" || stripped[1].Content != "{}" {
		t.Fatal("stripping images changed unrelated fields")
	}
	if got := model.NeedForMessages(stripped); got != (model.Need{Tools: true}) {
		t.Fatalf("stripped need = %+v", got)
	}
}
