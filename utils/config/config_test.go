package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

const validConfigYAML = `bot:
  qq_number: 0
  admin_number:
    - 1234
netsetting:
  host: "0.0.0.0"
  port: 9090
  token: ""
  pattern: "/Q-Mi"
napcat:
  ws:
    enable: false
    host: "127.0.0.1"
    port: 8080
  http:
    enable: true
    host: "127.0.0.1"
    port: 8080
storage:
  enable: true
  path: "Q-Mi.db"
`

// agentConfigYAML 在有效配置上追加启用的 agent 块，供 agent 相关测试复用。
const agentConfigYAML = validConfigYAML + `agent:
  enable: true
  base_url: "https://api.example.com/v1"
  api_key: "sk-test"
  model: "deepseek-chat"
  persona_file: "agent/persona.md"
  guide_file: "agent/guide.md"
  debounce: 3s
  temperature: 0.8
  debug: false
  multimodal: true
  max_tool_rounds: 5
  history:
    max: 40
    trim: 20
`

func configTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return filepath.Join(dir, "Config.yaml")
}

func writeTestConfig(t *testing.T, filename, content string) {
	t.Helper()
	if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfigCreatesReloadableDefault(t *testing.T) {
	filename := configTestDir(t)
	first := new(Config).LoadConfig()
	if first == nil {
		t.Fatal("first LoadConfig returned nil")
	}
	if first.NetSetting.Host != "0.0.0.0" || first.NetSetting.Port != 9090 || first.NetSetting.Pattern != "/Q-Mi" {
		t.Fatalf("unexpected default listener: %+v", first.NetSetting)
	}
	contents, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.ContainsRune(contents, '\t') {
		t.Error("generated YAML contains tabs")
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Errorf("file mode = %o, want 600", info.Mode().Perm())
	}
	second := new(Config).LoadConfig()
	if !reflect.DeepEqual(first, second) {
		t.Errorf("reloaded config = %+v, want %+v", second, first)
	}
}

func TestMakeNewConfigDoesNotOverwrite(t *testing.T) {
	filename := configTestDir(t)
	writeTestConfig(t, filename, validConfigYAML)
	if makeNewConfig() {
		t.Error("creating an existing config must fail")
	}
	got, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != validConfigYAML {
		t.Error("existing config was overwritten")
	}
}

func TestLoadConfigCurrentConfiguration(t *testing.T) {
	filename := configTestDir(t)
	writeTestConfig(t, filename, validConfigYAML)
	got := new(Config).LoadConfig()
	want := &Config{
		Bot:        &Bot{QQ_Number: 0, Admin_Number: []int{1234}},
		NetSetting: &NetSetting{Host: "0.0.0.0", Port: 9090, Pattern: "/Q-Mi"},
		Napcat: &Napcat{
			WS:   NapcatConn{Host: "127.0.0.1", Port: 8080},
			HTTP: NapcatConn{Enable: true, Host: "127.0.0.1", Port: 8080},
		},
		Storage: &Storage{Enable: true, Path: "Q-Mi.db"},
		Agent:   &AgentConfig{},
		WebUI:   &WebUIConfig{}, // 旧配置缺 webui 块 → 零值（enable=false 不生效）
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadConfig = %+v, want %+v", got, want)
	}
	contents, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != validConfigYAML {
		t.Error("loading modified the existing configuration")
	}
}

func TestContextRejectsInvalidBudgets(t *testing.T) {
	for _, setting := range []string{
		"max_input_tokens: -1", "review_after_wakes: -1",
		"keep_recent_messages: -1", "summary_tokens: -1",
		"max_input_tokens: 1024\n    summary_tokens: 2048",
	} {
		t.Run(setting, func(t *testing.T) {
			filename := configTestDir(t)
			content := strings.Replace(agentConfigYAML, "  debounce: 3s",
				"  debounce: 3s\n  context:\n    "+setting, 1)
			writeTestConfig(t, filename, content)
			if got := new(Config).LoadConfig(); got != nil {
				t.Fatalf("invalid context budget accepted: %s", setting)
			}
		})
	}
}

func TestContextConfigurationLoadsNestedBudgets(t *testing.T) {
	filename := configTestDir(t)
	content := strings.Replace(agentConfigYAML, "  debounce: 3s", `  debounce: 3s
  context:
    max_input_tokens: 64000
    review_after_wakes: 6
    keep_recent_messages: 16
    summary_tokens: 768`, 1)
	writeTestConfig(t, filename, content)
	got := new(Config).LoadConfig()
	want := AgentContext{MaxInputTokens: 64000, ReviewAfterWakes: 6, KeepRecentMessages: 16, SummaryTokens: 768}
	if got == nil || got.Agent.Context != want {
		t.Fatalf("nested context configuration did not round-trip: %+v", got)
	}
}

func TestLoadConfigRejectsInvalidConfiguration(t *testing.T) {
	cases := []struct {
		name  string
		old   string
		new   string
		field string
	}{
		{"invalid YAML", "    - 1234", "\t- 1234", "err="},
		{"invalid field type", "port: 9090", "port: invalid", "netsetting"},
		{"empty listener host", `host: "0.0.0.0"`, `host: ""`, "netsetting.host"},
		{"blank listener host", `host: "0.0.0.0"`, `host: "  "`, "netsetting.host"},
		{"zero listener port", "port: 9090", "port: 0", "netsetting.port"},
		{"negative listener port", "port: 9090", "port: -1", "netsetting.port"},
		{"large listener port", "port: 9090", "port: 65536", "netsetting.port"},
		{"missing listener path", `  pattern: "/Q-Mi"`, "", "netsetting.pattern"},
		{"empty listener path", `pattern: "/Q-Mi"`, `pattern: ""`, "netsetting.pattern"},
		{"relative listener path", `pattern: "/Q-Mi"`, `pattern: "Q-Mi"`, "netsetting.pattern"},
		{"method pattern", `pattern: "/Q-Mi"`, `pattern: "POST /Q-Mi"`, "netsetting.pattern"},
		{"wildcard pattern", `pattern: "/Q-Mi"`, `pattern: "/{name}"`, "netsetting.pattern"},
		{"remainder wildcard", `pattern: "/Q-Mi"`, `pattern: "/{name...}"`, "netsetting.pattern"},
		{"end wildcard", `pattern: "/Q-Mi"`, `pattern: "/Q-Mi/{$}"`, "netsetting.pattern"},
		{"star pattern", `pattern: "/Q-Mi"`, `pattern: "/Q-Mi/*"`, "netsetting.pattern"},
		{"space in path", `pattern: "/Q-Mi"`, `pattern: "/Q Mi"`, "netsetting.pattern"},
		{"tab in path", `pattern: "/Q-Mi"`, `pattern: "/Q\tMi"`, "netsetting.pattern"},
		{"unicode space in path", `pattern: "/Q-Mi"`, `pattern: "/Q\u00a0Mi"`, "netsetting.pattern"},
		{"encoded space", `pattern: "/Q-Mi"`, `pattern: "/Q%20Mi"`, "netsetting.pattern"},
		{"control in path", `pattern: "/Q-Mi"`, `pattern: "/Q%00Mi"`, "netsetting.pattern"},
		{"query in path", `pattern: "/Q-Mi"`, `pattern: "/Q-Mi?x=1"`, "netsetting.pattern"},
		{"empty query in path", `pattern: "/Q-Mi"`, `pattern: "/Q-Mi?"`, "netsetting.pattern"},
		{"fragment in path", `pattern: "/Q-Mi"`, `pattern: "/Q-Mi#fragment"`, "netsetting.pattern"},
		{"invalid escape", `pattern: "/Q-Mi"`, `pattern: "/Q%zzMi"`, "netsetting.pattern"},
		{"backslash in path", `pattern: "/Q-Mi"`, `pattern: "/Q\\Mi"`, "netsetting.pattern"},
		{"empty storage path", `path: "Q-Mi.db"`, `path: ""`, "storage.path"},
		{"blank storage path", `path: "Q-Mi.db"`, `path: "  "`, "storage.path"},
		{"empty HTTP host", "enable: true\n    host: \"127.0.0.1\"", "enable: true\n    host: \"\"", "napcat.http.host"},
		{"blank HTTP host", "enable: true\n    host: \"127.0.0.1\"", "enable: true\n    host: \"  \"", "napcat.http.host"},
		{"zero HTTP port", "    port: 8080\nstorage:", "    port: 0\nstorage:", "napcat.http.port"},
		{"negative HTTP port", "    port: 8080\nstorage:", "    port: -1\nstorage:", "napcat.http.port"},
		{"large HTTP port", "    port: 8080\nstorage:", "    port: 65536\nstorage:", "napcat.http.port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filename := configTestDir(t)
			content := strings.Replace(validConfigYAML, tc.old, tc.new, 1)
			if content == validConfigYAML {
				t.Fatal("test replacement did not modify the fixture")
			}
			writeTestConfig(t, filename, content)
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			if got := new(Config).LoadConfig(); got != nil {
				t.Error("invalid configuration was accepted")
			}
			if !strings.Contains(logs.String(), tc.field) {
				t.Errorf("error log must identify %q, got %q", tc.field, logs.String())
			}
			got, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != content {
				t.Error("invalid existing config was overwritten")
			}
		})
	}
}

func TestLoadConfigAcceptsValidBoundariesAndDisabledServices(t *testing.T) {
	for _, tc := range []struct {
		name string
		old  string
		new  string
	}{
		{"minimum listener port", "port: 9090", "port: 1"},
		{"maximum listener port", "port: 9090", "port: 65535"},
		{"root path", `pattern: "/Q-Mi"`, `pattern: "/"`},
		{"trailing slash", `pattern: "/Q-Mi"`, `pattern: "/Q-Mi/"`},
		{"nested path", `pattern: "/Q-Mi"`, `pattern: "/Q-Mi/events"`},
		{"disabled storage", "enable: true\n  path: \"Q-Mi.db\"", "enable: false\n  path: \"\""},
		{"disabled HTTP", "enable: true\n    host: \"127.0.0.1\"\n    port: 8080", "enable: false\n    host: \"\"\n    port: 0"},
		{"minimum HTTP port", "    port: 8080\nstorage:", "    port: 1\nstorage:"},
		{"maximum HTTP port", "    port: 8080\nstorage:", "    port: 65535\nstorage:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filename := configTestDir(t)
			writeTestConfig(t, filename, strings.Replace(validConfigYAML, tc.old, tc.new, 1))
			if new(Config).LoadConfig() == nil {
				t.Fatal("valid configuration was rejected")
			}
		})
	}
}

func TestLoadConfigDefaultAgentValues(t *testing.T) {
	configTestDir(t)
	got := new(Config).LoadConfig()
	if got == nil {
		t.Fatal("LoadConfig returned nil")
	}
	want := &AgentConfig{
		Enable:        false,
		BaseURL:       "",
		APIKey:        "",
		Model:         "",
		PersonaFile:   "agent/persona.md",
		GuideFile:     "agent/guide.md",
		Debounce:      3 * time.Second,
		SelfWake:      "10m",
		Temperature:   0.8,
		Debug:         false,
		Multimodal:    boolPtr(true),
		MaxToolRounds: 10,
		History:       AgentHistory{Max: 40, Trim: 20},
	}
	if !reflect.DeepEqual(got.Agent, want) {
		t.Errorf("default agent config = %+v, want %+v", got.Agent, want)
	}
}

func TestLoadConfigAgentBlock(t *testing.T) {
	filename := configTestDir(t)
	writeTestConfig(t, filename, agentConfigYAML)
	got := new(Config).LoadConfig()
	if got == nil {
		t.Fatal("LoadConfig returned nil for valid agent configuration")
	}
	want := &AgentConfig{
		Enable:        true,
		BaseURL:       "https://api.example.com/v1",
		APIKey:        "sk-test",
		Model:         "deepseek-chat",
		PersonaFile:   "agent/persona.md",
		GuideFile:     "agent/guide.md",
		Debounce:      3 * time.Second,
		SelfWake:      "", // 旧配置缺 self_wake 键 → 空串（运行期按默认 10 分钟处理）
		Temperature:   0.8,
		Debug:         false,
		Multimodal:    boolPtr(true),
		MaxToolRounds: 5,
		History:       AgentHistory{Max: 40, Trim: 20},
	}
	if !reflect.DeepEqual(got.Agent, want) {
		t.Errorf("agent config = %+v, want %+v", got.Agent, want)
	}
	contents, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != agentConfigYAML {
		t.Error("loading modified the existing configuration")
	}
}

// TestLoadConfigAgentMultimodalToggle multimodal 显式设 false 时解析为 false
// （不支持图片输入的模型关闭多模态，维持纯文本+原始 CQ 码）。
func TestLoadConfigAgentMultimodalToggle(t *testing.T) {
	filename := configTestDir(t)
	content := strings.Replace(agentConfigYAML, "multimodal: true", "multimodal: false", 1)
	if content == agentConfigYAML {
		t.Fatal("test replacement did not modify the fixture")
	}
	writeTestConfig(t, filename, content)
	got := new(Config).LoadConfig()
	if got == nil || got.Agent == nil {
		t.Fatal("LoadConfig returned nil for valid agent configuration")
	}
	if got.Agent.MultimodalEnabled() {
		t.Error("multimodal: false 应解析为关闭")
	}
}

// TestLoadConfigAgentMultimodalMissingDefaultsOn 旧配置文件缺 multimodal 键
// 时默认开启（bool 零值陷阱：缺键不能静默关闭多模态）。
func TestLoadConfigAgentMultimodalMissingDefaultsOn(t *testing.T) {
	filename := configTestDir(t)
	content := strings.Replace(agentConfigYAML, "\n  multimodal: true", "", 1)
	if content == agentConfigYAML {
		t.Fatal("test replacement did not modify the fixture")
	}
	writeTestConfig(t, filename, content)
	got := new(Config).LoadConfig()
	if got == nil || got.Agent == nil {
		t.Fatal("LoadConfig returned nil for valid agent configuration")
	}
	if got.Agent.Multimodal != nil {
		t.Errorf("缺键时 Multimodal 应为 nil, got %v", *got.Agent.Multimodal)
	}
	if !got.Agent.MultimodalEnabled() {
		t.Error("缺 multimodal 键应默认开启")
	}
}

// TestLoadConfigAgentSelfWake self_wake 各合法取值按原样解析进 AgentConfig
// （空串/"off"/"0" 的语义在运行期由 agent 包解释，配置层不重写字段值）。
func TestLoadConfigAgentSelfWake(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want string
	}{
		{"explicit off", `self_wake: "off"`, "off"},
		{"explicit zero", `self_wake: "0"`, "0"},
		{"one hour", `self_wake: "1h"`, "1h"},
		{"minimum boundary", `self_wake: "30s"`, "30s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filename := configTestDir(t)
			content := strings.Replace(agentConfigYAML, "debounce: 3s", "debounce: 3s\n  "+tc.yaml, 1)
			if content == agentConfigYAML {
				t.Fatal("test replacement did not modify the fixture")
			}
			writeTestConfig(t, filename, content)
			got := new(Config).LoadConfig()
			if got == nil || got.Agent == nil {
				t.Fatal("LoadConfig returned nil for valid self_wake configuration")
			}
			if got.Agent.SelfWake != tc.want {
				t.Errorf("SelfWake = %q, want %q", got.Agent.SelfWake, tc.want)
			}
		})
	}
}

func TestLoadConfigRejectsInvalidAgentConfiguration(t *testing.T) {
	cases := []struct {
		name  string
		old   string
		new   string
		field string
	}{
		{"base_url without scheme", `base_url: "https://api.example.com/v1"`, `base_url: "api.example.com/v1"`, "agent.base_url"},
		{"base_url with ftp scheme", `base_url: "https://api.example.com/v1"`, `base_url: "ftp://api.example.com/v1"`, "agent.base_url"},
		{"base_url without host", `base_url: "https://api.example.com/v1"`, `base_url: "http://"`, "agent.base_url"},
		{"empty base_url", `base_url: "https://api.example.com/v1"`, `base_url: ""`, "agent.base_url"},
		{"blank base_url", `base_url: "https://api.example.com/v1"`, `base_url: "  "`, "agent.base_url"},
		{"empty model", `model: "deepseek-chat"`, `model: ""`, "agent.model"},
		{"blank model", `model: "deepseek-chat"`, `model: "  "`, "agent.model"},
		{"empty api_key", `api_key: "sk-test"`, `api_key: ""`, "agent.api_key"},
		{"blank api_key", `api_key: "sk-test"`, `api_key: "  "`, "agent.api_key"},
		{"empty persona_file", `persona_file: "agent/persona.md"`, `persona_file: ""`, "agent.persona_file"},
		{"empty guide_file", `guide_file: "agent/guide.md"`, `guide_file: ""`, "agent.guide_file"},
		{"history max below trim", "max: 40", "max: 10", "agent.history.max"},
		{"zero history trim", "trim: 20", "trim: 0", "agent.history.trim"},
		{"negative history trim", "trim: 20", "trim: -1", "agent.history.trim"},
		{"zero debounce", "debounce: 3s", "debounce: 0s", "agent.debounce"},
		{"negative debounce", "debounce: 3s", "debounce: -1s", "agent.debounce"},
		{"non-duration debounce", "debounce: 3s", "debounce: fast", "agent"},
		{"zero max_tool_rounds", "max_tool_rounds: 5", "max_tool_rounds: 0", "agent.max_tool_rounds"},
		{"negative max_tool_rounds", "max_tool_rounds: 5", "max_tool_rounds: -1", "agent.max_tool_rounds"},
		{"self_wake below minimum", "debounce: 3s", "debounce: 3s\n  self_wake: \"500ms\"", "agent.self_wake"},
		{"self_wake not a duration", "debounce: 3s", "debounce: 3s\n  self_wake: \"fast\"", "agent.self_wake"},
		{"self_wake below 30s boundary", "debounce: 3s", "debounce: 3s\n  self_wake: \"29s\"", "agent.self_wake"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filename := configTestDir(t)
			content := strings.Replace(agentConfigYAML, tc.old, tc.new, 1)
			if content == agentConfigYAML {
				t.Fatal("test replacement did not modify the fixture")
			}
			writeTestConfig(t, filename, content)
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			if got := new(Config).LoadConfig(); got != nil {
				t.Error("invalid agent configuration was accepted")
			}
			if !strings.Contains(logs.String(), tc.field) {
				t.Errorf("error log must identify %q, got %q", tc.field, logs.String())
			}
			got, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != content {
				t.Error("invalid existing config was overwritten")
			}
		})
	}
}

func TestLoadConfigAcceptsValidAgentBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		old  string
		new  string
	}{
		{"history max equals trim", "max: 40", "max: 20"},
		{"minimum positive trim", "trim: 20", "trim: 1"},
		{"minimum max_tool_rounds", "max_tool_rounds: 5", "max_tool_rounds: 1"},
		{"short debounce", "debounce: 3s", "debounce: 1ms"},
		{"http scheme base_url", `base_url: "https://api.example.com/v1"`, `base_url: "http://127.0.0.1:8000/v1"`},
		{"base_url without path", `base_url: "https://api.example.com/v1"`, `base_url: "https://api.example.com"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filename := configTestDir(t)
			writeTestConfig(t, filename, strings.Replace(agentConfigYAML, tc.old, tc.new, 1))
			if new(Config).LoadConfig() == nil {
				t.Fatal("valid agent configuration was rejected")
			}
		})
	}
}

func TestLoadConfigSkipsAgentValidationWhenDisabled(t *testing.T) {
	filename := configTestDir(t)
	// agent.enable=false 时其余 agent 字段即使非法也不阻断启动。
	content := strings.Replace(validConfigYAML, "storage:", `agent:
  enable: false
  base_url: "not a url"
  api_key: ""
  model: ""
  persona_file: ""
  guide_file: ""
  debounce: 0s
  history:
    max: 0
    trim: 0
storage:`, 1)
	if content == validConfigYAML {
		t.Fatal("test replacement did not modify the fixture")
	}
	writeTestConfig(t, filename, content)
	if new(Config).LoadConfig() == nil {
		t.Fatal("disabled agent with invalid values must not be rejected")
	}
}

// webuiConfigYAML 在有效配置上追加启用的 webui 块，供 webui 相关测试复用。
const webuiConfigYAML = validConfigYAML + `webui:
  enable: true
  host: "127.0.0.1"
  port: 6077
  token: "test-token"
`

func TestLoadConfigDefaultWebUIValues(t *testing.T) {
	configTestDir(t)
	got := new(Config).LoadConfig()
	if got == nil {
		t.Fatal("LoadConfig returned nil")
	}
	want := &WebUIConfig{Enable: false, Host: "127.0.0.1", Port: 6077, Token: ""}
	if !reflect.DeepEqual(got.WebUI, want) {
		t.Errorf("default webui config = %+v, want %+v", got.WebUI, want)
	}
}

func TestLoadConfigWebUIBlock(t *testing.T) {
	filename := configTestDir(t)
	writeTestConfig(t, filename, webuiConfigYAML)
	got := new(Config).LoadConfig()
	if got == nil {
		t.Fatal("LoadConfig returned nil for valid webui configuration")
	}
	want := &WebUIConfig{Enable: true, Host: "127.0.0.1", Port: 6077, Token: "test-token"}
	if !reflect.DeepEqual(got.WebUI, want) {
		t.Errorf("webui config = %+v, want %+v", got.WebUI, want)
	}
	contents, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != webuiConfigYAML {
		t.Error("loading modified the existing configuration")
	}
}

func TestLoadConfigRejectsInvalidWebUIConfiguration(t *testing.T) {
	cases := []struct {
		name  string
		old   string
		new   string
		field string
	}{
		{"empty token", `token: "test-token"`, `token: ""`, "webui.token"},
		{"blank token", `token: "test-token"`, `token: "  "`, "webui.token"},
		{"empty host", "host: \"127.0.0.1\"\n  port: 6077", "host: \"\"\n  port: 6077", "webui.host"},
		{"blank host", "host: \"127.0.0.1\"\n  port: 6077", "host: \"  \"\n  port: 6077", "webui.host"},
		{"zero port", "port: 6077", "port: 0", "webui.port"},
		{"negative port", "port: 6077", "port: -1", "webui.port"},
		{"large port", "port: 6077", "port: 65536", "webui.port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filename := configTestDir(t)
			content := strings.Replace(webuiConfigYAML, tc.old, tc.new, 1)
			if content == webuiConfigYAML {
				t.Fatal("test replacement did not modify the fixture")
			}
			writeTestConfig(t, filename, content)
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			if got := new(Config).LoadConfig(); got != nil {
				t.Error("invalid webui configuration was accepted")
			}
			if !strings.Contains(logs.String(), tc.field) {
				t.Errorf("error log must identify %q, got %q", tc.field, logs.String())
			}
			got, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != content {
				t.Error("invalid existing config was overwritten")
			}
		})
	}
}

func TestLoadConfigAcceptsValidWebUIBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		old  string
		new  string
	}{
		{"minimum port", "port: 6077", "port: 1"},
		{"maximum port", "port: 6077", "port: 65535"},
		{"disabled with empty token", "enable: true\n  host: \"127.0.0.1\"\n  port: 6077\n  token: \"test-token\"", "enable: false\n  host: \"\"\n  port: 0\n  token: \"\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filename := configTestDir(t)
			writeTestConfig(t, filename, strings.Replace(webuiConfigYAML, tc.old, tc.new, 1))
			if new(Config).LoadConfig() == nil {
				t.Fatal("valid webui configuration was rejected")
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }
