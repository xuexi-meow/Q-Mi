package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newModelStore 在 agent 基础配置后追加一段 2 空格缩进的 models 片段。
func newModelStore(t *testing.T, fragment string) (*Store, error) {
	t.Helper()
	dir := t.TempDir()
	basePath := filepath.Join(dir, "Config.yaml")
	body := agentConfigYAML + fragment
	if err := os.WriteFile(basePath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return NewStore(Paths{Base: basePath, Overlay: filepath.Join(dir, "Config.webui.yaml")})
}

func TestLegacySingleModelTierNormalizes(t *testing.T) {
	tier := ModelTier{BaseURL: "https://x.example", APIKey: "k", Model: "m", Capabilities: []string{"tools"}}
	cands := tier.EffectiveCandidates("chat")
	if len(cands) != 1 {
		t.Fatalf("candidates = %d, want 1", len(cands))
	}
	if cands[0].Name != "chat" || cands[0].Model != "m" || cands[0].BaseURL != "https://x.example" {
		t.Fatalf("normalized candidate = %+v", cands[0])
	}
	if got := (ModelTier{}).EffectiveCandidates("x"); got != nil {
		t.Fatalf("empty tier must yield no candidate, got %+v", got)
	}
}

func TestValidateAcceptsMultiCandidateTier(t *testing.T) {
	store, err := newModelStore(t, `  models:
    chat:
      strategy: priority
      candidates:
        - name: deepseek
          base_url: "https://api.deepseek.com"
          api_key: "sk-1"
          model: "deepseek-flash"
          capabilities: [tools, vision]
        - name: zhipu
          base_url: "https://open.bigmodel.cn/api/paas/v4"
          api_key_env: ZHIPU_API_KEY
          model: "glm-4.6v-flash"
          capabilities: [tools, vision]
          cooldown: 2m
    video:
      enable: false
      candidates: []
  max_attempts: 2
  wake_routes:
    background: chat
`)
	if err != nil {
		t.Fatalf("valid multi-candidate config rejected: %v", err)
	}
	defer store.Close()
	tier := store.Snapshot().Agent.Models["chat"]
	if len(tier.EffectiveCandidates("chat")) != 2 {
		t.Fatalf("candidates not decoded: %+v", tier)
	}
	if store.Snapshot().Agent.MaxAttempts != 2 {
		t.Fatal("max_attempts not decoded")
	}
}

func TestValidateRejectsDuplicateCandidateNames(t *testing.T) {
	_, err := newModelStore(t, `  models:
    chat:
      candidates:
        - name: same
          base_url: "https://a.example"
          api_key: k
          model: m1
        - name: same
          base_url: "https://b.example"
          api_key: k
          model: m2
`)
	if err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate candidate names must be rejected, err = %v", err)
	}
}

func TestValidateRejectsUnsupportedStrategy(t *testing.T) {
	_, err := newModelStore(t, `  models:
    chat:
      strategy: weighted
      candidates:
        - name: a
          base_url: "https://a.example"
          api_key: k
          model: m
`)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("weighted strategy must be rejected for now, err = %v", err)
	}
}

func TestValidateRejectsBothKeyFields(t *testing.T) {
	_, err := newModelStore(t, `  models:
    chat:
      candidates:
        - name: a
          base_url: "https://a.example"
          api_key: k
          api_key_env: A_KEY
          model: m
`)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("api_key + api_key_env must be rejected, err = %v", err)
	}
}

func TestValidateRejectsUnknownWakeRoute(t *testing.T) {
	_, err := newModelStore(t, `  models:
    chat:
      candidates:
        - name: a
          base_url: "https://a.example"
          api_key: k
          model: m
  wake_routes:
    background: cheap
`)
	if err == nil || !strings.Contains(err.Error(), "unknown tier") {
		t.Fatalf("unknown wake route target must be rejected, err = %v", err)
	}
}

func TestValidateRejectsEmptyEnabledTier(t *testing.T) {
	_, err := newModelStore(t, `  models:
    chat:
      candidates: []
`)
	if err == nil || !strings.Contains(err.Error(), "at least one candidate") {
		t.Fatalf("enabled tier without candidates must be rejected, err = %v", err)
	}
}

func TestToolChoiceConfigValidationAndDecode(t *testing.T) {
	for _, fragment := range []string{
		"  tool_choice: none\n",
		"  models:\n    chat:\n      model: m\n      tool_choice: typo\n",
		"  models:\n    chat:\n      candidates:\n        - name: a\n          model: m\n          tool_choice: typo\n",
	} {
		store, err := newModelStore(t, fragment)
		if store != nil {
			store.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "tool_choice") {
			t.Errorf("invalid action tool choice must be rejected: fragment=%s err=%v", fragment, err)
		}
	}
	store, err := newModelStore(t, "  tool_choice: required\n  models:\n    chat:\n      model: m\n      tool_choice: auto\n")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := store.Snapshot().Agent
	if cfg.ToolChoice != "required" || cfg.Models["chat"].EffectiveCandidates("chat")[0].ToolChoice != "auto" {
		t.Fatal("tool_choice must survive YAML decode and legacy tier normalization")
	}
}
