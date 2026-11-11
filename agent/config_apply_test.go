package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"Q-Mi/utils/config"
)

// applyTestConfigYAML 一份可通过校验的最小配置（Store 用）。
const applyTestConfigYAML = `bot:
  qq_number: 1000
  admin_number: [1]
netsetting:
  host: "0.0.0.0"
  port: 9090
  pattern: "/Q-Mi"
  token: ""
napcat:
  ws:
    enable: false
    host: "127.0.0.1"
    port: 8080
  http:
    enable: false
    host: "127.0.0.1"
    port: 8080
storage:
  enable: false
  path: "Q-Mi.db"
agent:
  enable: true
  base_url: "http://127.0.0.1:1"
  api_key: "k"
  model: "m"
  persona_file: "p.md"
  guide_file: "g.md"
  debounce: 3s
  max_tool_rounds: 5
  temperature: 0.8
  history:
    max: 40
    trim: 20
webui:
  enable: false
  host: "127.0.0.1"
  port: 8848
  token: ""
`

func TestApplyConfigUpdatesHotFieldsAtNextRequest(t *testing.T) {
	cfg := testCfg(time.Millisecond)
	m, f := newTestManager(t, cfg, &stubBuilder{})
	f.script = []string{toolCallResp("done", "finish", "{}")}

	next := cfg
	next.Temperature = 0.2
	next.MaxToolRounds = 1
	m.ApplyConfig(next)

	m.handleTurn(&Turn{SelfID: 999, Now: time.Now(),
		NewMessages: []TurnMessage{{ChatType: "group", ChatID: 888, Text: "hi"}}})

	reqs := requestsSnapshot(f)
	if len(reqs) == 0 {
		t.Fatal("expected a model request")
	}
	if reqs[0].Temperature != 0.2 {
		t.Fatalf("request temperature = %v, want 0.2", reqs[0].Temperature)
	}
}

func TestApplyConfigRebuildsToolRegistry(t *testing.T) {
	cfg := testCfg(time.Millisecond)
	m, _ := newTestManager(t, cfg, &stubBuilder{})
	before := len(m.toolRegistry().Build(&ToolContext{}))

	next := cfg
	next.Modules = map[string]bool{"sticker": false}
	m.ApplyConfig(next)

	after := len(m.toolRegistry().Build(&ToolContext{}))
	if after >= before {
		t.Fatalf("disabling sticker must drop tools: before=%d after=%d", before, after)
	}
	for _, tool := range m.toolRegistry().Build(&ToolContext{}) {
		if tool.Name == "list_stickers" {
			t.Fatal("sticker module still loaded after ApplyConfig")
		}
	}
}

func TestApplyConfigRebuildsModelPool(t *testing.T) {
	cfg := testCfg(time.Millisecond)
	m, _ := newTestManager(t, cfg, &stubBuilder{})

	next := cfg
	next.BaseURL = "http://127.0.0.1:2"
	next.APIKey = "new-key"
	m.ApplyConfig(next)

	slot, tier, err := m.modelPool().pick(WakeChat, modelNeed{Tools: true}, nil)
	if err != nil {
		t.Fatalf("pick after rebuild: %v", err)
	}
	if tier != WakeChat || slot.client == nil {
		t.Fatalf("chat tier unusable after rebuild: tier=%s slot=%+v", tier, slot)
	}
	if slot.client.baseURL != "http://127.0.0.1:2" {
		t.Fatalf("chat client baseURL = %q, want rebuilt endpoint", slot.client.baseURL)
	}
}

func TestApplyConfigUpdatesToolChoiceAtNextRequest(t *testing.T) {
	cfg := testCfg(time.Millisecond)
	m, f := newTestManager(t, cfg, &stubBuilder{})
	f.script = []string{toolCallResp("done", "finish", "{}")}
	next := cfg
	next.ToolChoice = "required"
	m.ApplyConfig(next)

	m.handleTurn(&Turn{SelfID: 999, Now: time.Now(),
		NewMessages: []TurnMessage{{ChatType: "group", ChatID: 888, Text: "hi"}}})
	reqs := requestsSnapshot(f)
	if len(reqs) == 0 {
		t.Fatal("expected a model request")
	}
	if reqs[0].ToolChoice != "required" {
		t.Fatalf("request tool_choice = %q, want required after hot reload", reqs[0].ToolChoice)
	}
}

func TestAttachConfigStoreAppliesOverlayChange(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "Config.yaml")
	overlayPath := filepath.Join(dir, "Config.webui.yaml")
	if err := os.WriteFile(basePath, []byte(applyTestConfigYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := config.NewStore(config.Paths{Base: basePath, Overlay: overlayPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	m, _ := newTestManager(t, *store.Snapshot().Agent, &stubBuilder{})
	m.AttachConfigStore(store)

	if err := store.UpdateOverlay(func(root map[string]any) {
		agentMap, _ := root["agent"].(map[string]any)
		if agentMap == nil {
			agentMap = map[string]any{}
			root["agent"] = agentMap
		}
		agentMap["temperature"] = 0.33
	}); err != nil {
		t.Fatalf("UpdateOverlay: %v", err)
	}

	if got := m.config().Temperature; got != 0.33 {
		t.Fatalf("manager temperature = %v, want 0.33 after hot reload", got)
	}
}

func TestBuilderApplyUpdatesHistoryAndMultimodal(t *testing.T) {
	cfg := testCfg(time.Millisecond)
	b := NewDefaultBuilder(nil, cfg)

	off := false
	next := cfg
	next.History = config.AgentHistory{Max: 7, Trim: 3}
	next.Multimodal = &off
	next.WorkspaceDir = t.TempDir()
	b.Apply(next)

	if b.max != 7 || b.trim != 3 {
		t.Fatalf("history = %d/%d, want 7/3", b.max, b.trim)
	}
	if b.multimodal {
		t.Fatal("multimodal must be disabled after Apply")
	}
	if b.workspace != next.WorkspaceDir {
		t.Fatalf("workspace = %q, want %q", b.workspace, next.WorkspaceDir)
	}
}
