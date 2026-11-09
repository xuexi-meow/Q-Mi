package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"Q-Mi/utils/config"
)

// configYAML 一份可校验的最小配置；modelURL 为模型连通性测试目标。
func configYAML(modelURL string) string {
	return `bot:
  qq_number: 1000
  admin_number: [1]
netsetting:
  host: "0.0.0.0"
  port: 9090
  pattern: "/Q-Mi"
  token: "super-secret-token"
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
  base_url: "` + modelURL + `"
  api_key: "sk-1234567890abcdef"
  model: "legacy-model"
  persona_file: "p.md"
  guide_file: "g.md"
  debounce: 3s
  max_tool_rounds: 5
  temperature: 0.8
  history:
    max: 40
    trim: 20
  models:
    chat:
      candidates:
        - name: deepseek
          base_url: "` + modelURL + `"
          api_key: "sk-candidate-key-9999"
          model: "deepseek-flash"
          capabilities: [tools, vision]
webui:
  enable: false
  host: "127.0.0.1"
  port: 8848
  token: "webui-token-1234"
`
}

// newConfigStore 建临时配置仓库并把 webui 的取用接缝指向它。
func newConfigStore(t *testing.T, yaml string) *config.Store {
	t.Helper()
	dir := t.TempDir()
	base := filepath.Join(dir, "Config.yaml")
	if err := os.WriteFile(base, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := config.NewStore(config.Paths{Base: base, Overlay: filepath.Join(dir, "Config.webui.yaml")})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	previous := configStoreProvider
	configStoreProvider = func() *config.Store { return store }
	t.Cleanup(func() { configStoreProvider = previous })
	return store
}

// callConfig 直接调用 handler，返回状态码与响应体。
func callConfig(t *testing.T, handler http.HandlerFunc, method, body string) (int, []byte) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/api/config", reader)
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func TestConfigGetMasksSecrets(t *testing.T) {
	newConfigStore(t, configYAML("http://127.0.0.1:1"))
	code, body := callConfig(t, handleConfigGet, http.MethodGet, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, body)
	}
	var view configView
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.Paths.Base == "" || view.Paths.Overlay == "" {
		t.Fatalf("paths missing: %+v", view.Paths)
	}
	agentMap, _ := view.Effective["agent"].(map[string]any)
	key, _ := agentMap["api_key"].(string)
	if strings.Contains(key, "1234567890") || !strings.Contains(key, "****") {
		t.Fatalf("api_key not masked: %q", key)
	}
	napcat, _ := view.Effective["napcat"].(map[string]any)
	ws, _ := napcat["ws"].(map[string]any)
	if tok, _ := ws["token"].(string); tok != "" {
		t.Fatalf("empty token should stay empty, got %q", tok)
	}
	if len(view.RestartFields) == 0 || len(view.HotFields) == 0 {
		t.Fatal("restart/hot field hints must be present")
	}
}

func TestConfigPutAppliesHotChange(t *testing.T) {
	store := newConfigStore(t, configYAML("http://127.0.0.1:1"))
	code, body := callConfig(t, handleConfigPut, http.MethodPut, `{"agent":{"temperature":0.35,"max_tool_rounds":9}}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, body)
	}
	if got := store.Snapshot().Agent.Temperature; got != 0.35 {
		t.Fatalf("temperature = %v, want 0.35", got)
	}
	if got := store.Snapshot().Agent.MaxToolRounds; got != 9 {
		t.Fatalf("max_tool_rounds = %d, want 9", got)
	}
	overlay, err := os.ReadFile(store.Paths().Overlay)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(overlay), "temperature: 0.35") {
		t.Fatalf("overlay missing written value: %s", overlay)
	}
}

func TestConfigPutRejectsRestartField(t *testing.T) {
	store := newConfigStore(t, configYAML("http://127.0.0.1:1"))
	code, body := callConfig(t, handleConfigPut, http.MethodPut, `{"netsetting":{"port":9999}}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", code, body)
	}
	if !strings.Contains(string(body), "需要重启") {
		t.Fatalf("error should explain restart requirement: %s", body)
	}
	if got := store.Snapshot().NetSetting.Port; got != 9090 {
		t.Fatalf("restart field must not change, port = %d", got)
	}
}

func TestConfigPutRejectsInvalidValue(t *testing.T) {
	store := newConfigStore(t, configYAML("http://127.0.0.1:1"))
	code, body := callConfig(t, handleConfigPut, http.MethodPut, `{"agent":{"max_tool_rounds":0}}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", code, body)
	}
	if got := store.Snapshot().Agent.MaxToolRounds; got != 5 {
		t.Fatalf("invalid write must not apply, max_tool_rounds = %d", got)
	}
}

func TestConfigPutKeepsMaskedSecret(t *testing.T) {
	store := newConfigStore(t, configYAML("http://127.0.0.1:1"))
	original := store.Snapshot().Agent.APIKey
	code, body := callConfig(t, handleConfigPut, http.MethodPut,
		`{"agent":{"api_key":"`+maskSecret(original)+`","temperature":0.2}}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, body)
	}
	if got := store.Snapshot().Agent.APIKey; got != original {
		t.Fatalf("masked secret must be kept, got %q", got)
	}
	overlay := store.Overlay()
	agentMap, _ := overlay["agent"].(map[string]any)
	if _, exists := agentMap["api_key"]; exists {
		t.Fatalf("masked api_key must not be written into overlay: %+v", agentMap)
	}
	if got := store.Snapshot().Agent.Temperature; got != 0.2 {
		t.Fatalf("temperature = %v, want 0.2", got)
	}
}

func TestConfigValidationEndpointDoesNotWrite(t *testing.T) {
	store := newConfigStore(t, configYAML("http://127.0.0.1:1"))
	code, body := callConfig(t, handleConfigValidate, http.MethodPost, `{"agent":{"temperature":0.1}}`)
	if code != http.StatusOK || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("validate failed: %d %s", code, body)
	}
	if store.Snapshot().Agent.Temperature != 0.8 {
		t.Fatal("validate must not apply changes")
	}
	if _, err := os.Stat(store.Paths().Overlay); !os.IsNotExist(err) {
		t.Fatalf("validate must not write overlay, stat err = %v", err)
	}
}

func TestModelTestHitsEndpoint(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	newConfigStore(t, configYAML(srv.URL))
	code, body := callConfig(t, handleModelTest, http.MethodPost, `{"tier":"chat","name":"deepseek"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true {
		t.Fatalf("test result = %s", body)
	}
	if !strings.HasSuffix(gotPath, "/chat/completions") {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotAuth != "Bearer sk-candidate-key-9999" {
		t.Fatalf("candidate key not used: %q", gotAuth)
	}
}

func TestModelTestUnknownCandidate(t *testing.T) {
	newConfigStore(t, configYAML("http://127.0.0.1:1"))
	code, body := callConfig(t, handleModelTest, http.MethodPost, `{"tier":"chat","name":"nope"}`)
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", code, body)
	}
}

// TestAPIConfigRoute 走真实路由表，确认鉴权与注册都接上了。
func TestAPIConfigRoute(t *testing.T) {
	newConfigStore(t, configYAML("http://127.0.0.1:1"))
	srv := startTestServer(t, "tok")
	base := "http://" + srv.Addr

	if resp, _ := get(t, base+"/api/config", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token must be 401, got %d", resp.StatusCode)
	}
	resp, body := get(t, base+"/api/config", "tok")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "restart_fields") {
		t.Fatalf("GET /api/config = %d %s", resp.StatusCode, body)
	}

	resp2, body2 := sendJSON(t, http.MethodPut, base+"/api/config", "tok", `{"agent":{"temperature":0.4}}`)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/config = %d %s", resp2.StatusCode, body2)
	}
	if !strings.Contains(body2, "0.4") {
		t.Fatalf("PUT response should reflect new value: %s", body2)
	}
}
