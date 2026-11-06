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
