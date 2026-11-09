package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// newTestStore 在临时目录写基础层/覆盖层并构造 Store。
func newTestStore(t *testing.T, base, overlay string) (*Store, string, string) {
	t.Helper()
	dir := t.TempDir()
	basePath := filepath.Join(dir, "Config.yaml")
	overlayPath := filepath.Join(dir, "Config.webui.yaml")
	if err := os.WriteFile(basePath, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	if overlay != "" {
		if err := os.WriteFile(overlayPath, []byte(overlay), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewStore(Paths{Base: basePath, Overlay: overlayPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, basePath, overlayPath
}

// setOverlayValue 按路径写入嵌套 map（覆盖层变更用）。
func setOverlayValue(root map[string]any, path []string, value any) {
	cur := root
	for _, key := range path[:len(path)-1] {
		next, ok := cur[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[key] = next
		}
		cur = next
	}
	cur[path[len(path)-1]] = value
}

func TestStoreAppliesOverlayLayer(t *testing.T) {
	s, _, _ := newTestStore(t, agentConfigYAML, `agent:
  temperature: 0.3
  model: "overlay-model"
  history:
    max: 60
`)
	cfg := s.Snapshot()
	if cfg.Agent.Temperature != 0.3 {
		t.Fatalf("temperature = %v, want 0.3", cfg.Agent.Temperature)
	}
	if cfg.Agent.Model != "overlay-model" {
		t.Fatalf("model = %q, want overlay-model", cfg.Agent.Model)
	}
	if cfg.Agent.History.Max != 60 {
		t.Fatalf("history.max = %d, want 60", cfg.Agent.History.Max)
	}
	// 覆盖层未触及的字段继承基础层。
	if cfg.Agent.History.Trim != 20 {
		t.Fatalf("history.trim = %d, want 20 (inherited)", cfg.Agent.History.Trim)
	}
	if cfg.Napcat.HTTP.Port != 8080 {
		t.Fatalf("napcat.http.port = %d, want 8080 (inherited)", cfg.Napcat.HTTP.Port)
	}
}

func TestStoreReplacesArraysWholesale(t *testing.T) {
	s, _, _ := newTestStore(t, agentConfigYAML, `bot:
  admin_number:
    - 5678
`)
	cfg := s.Snapshot()
	if !reflect.DeepEqual(cfg.Bot.Admin_Number, []int{5678}) {
		t.Fatalf("admin_number = %v, want [5678] (array replaced, not merged)", cfg.Bot.Admin_Number)
	}
}

func TestStoreNullDeletesKey(t *testing.T) {
	base := strings.Replace(agentConfigYAML, "    - 1234", "    - 1234\n    - 5678", 1)
	s, _, _ := newTestStore(t, base, `bot:
  admin_number: null
`)
	if got := s.Snapshot().Bot.Admin_Number; len(got) != 0 {
		t.Fatalf("admin_number = %v, want empty after null delete", got)
	}
}

func TestStoreRejectsInvalidConfigAtStartup(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "Config.yaml")
	if err := os.WriteFile(basePath, []byte(agentConfigYAML+"webui:\n  enable: true\n  host: \"127.0.0.1\"\n  port: 0\n  token: \"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(Paths{Base: basePath, Overlay: filepath.Join(dir, "Config.webui.yaml")}); err == nil {
		t.Fatal("invalid base config must fail NewStore")
	}
}

func TestStoreKeepsLastGoodOnInvalidReload(t *testing.T) {
	s, _, overlayPath := newTestStore(t, agentConfigYAML, "")
	before := s.Snapshot()
	var reported error
	s.OnError(func(err error) { reported = err })

	if err := os.WriteFile(overlayPath, []byte("netsetting:\n  port: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err == nil {
		t.Fatal("Reload must reject invalid overlay")
	}
	if reported == nil {
		t.Fatal("OnError must be called on reload failure")
	}
	if !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("snapshot must stay unchanged after failed reload")
	}
}

func TestStoreUpdateOverlayValidatesBeforeWrite(t *testing.T) {
	s, _, overlayPath := newTestStore(t, agentConfigYAML, "")
	err := s.UpdateOverlay(func(m map[string]any) {
		setOverlayValue(m, []string{"netsetting", "port"}, 0)
	})
	if err == nil {
		t.Fatal("invalid overlay must be rejected")
	}
	if _, statErr := os.Stat(overlayPath); !os.IsNotExist(statErr) {
		t.Fatalf("invalid overlay must not be written, stat err = %v", statErr)
	}
}

func TestStoreUpdateOverlayAppliesAndPersists(t *testing.T) {
	s, _, overlayPath := newTestStore(t, agentConfigYAML, "")
	if err := s.UpdateOverlay(func(m map[string]any) {
		setOverlayValue(m, []string{"agent", "temperature"}, 0.25)
	}); err != nil {
		t.Fatalf("UpdateOverlay: %v", err)
	}
	if got := s.Snapshot().Agent.Temperature; got != 0.25 {
		t.Fatalf("temperature = %v, want 0.25", got)
	}
	data, err := os.ReadFile(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "temperature: 0.25") {
		t.Fatalf("overlay file missing written value: %s", data)
	}
	if !strings.HasPrefix(string(data), "#") {
		t.Fatalf("overlay file must keep generated header: %s", data)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(overlayPath)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("overlay perm = %o, want 600", perm)
		}
	}
}

func TestStoreSubscribeOnlyOnChange(t *testing.T) {
	s, _, _ := newTestStore(t, agentConfigYAML, "")
	var calls int
	var gotOld, gotNew *Config
	s.Subscribe(func(old, next *Config) {
		calls++
		gotOld, gotNew = old, next
	})

	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("unchanged reload must not notify, calls = %d", calls)
	}
	if err := s.UpdateOverlay(func(m map[string]any) {
		setOverlayValue(m, []string{"agent", "temperature"}, 0.4)
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("changed reload must notify once, calls = %d", calls)
	}
	if gotOld == nil || gotNew == nil || gotNew.Agent.Temperature != 0.4 {
		t.Fatalf("callback payload wrong: old=%v new=%v", gotOld, gotNew)
	}
}

func TestStoreSnapshotStableAcrossReload(t *testing.T) {
	s, _, _ := newTestStore(t, agentConfigYAML, "")
	old := s.Snapshot()
	if err := s.UpdateOverlay(func(m map[string]any) {
		setOverlayValue(m, []string{"agent", "temperature"}, 0.9)
	}); err != nil {
		t.Fatal(err)
	}
	if old.Agent.Temperature == 0.9 {
		t.Fatal("old snapshot must stay immutable")
	}
	if s.Snapshot().Agent.Temperature != 0.9 {
		t.Fatal("new snapshot must carry the change")
	}
	if old == s.Snapshot() {
		t.Fatal("snapshot pointer must be replaced, not mutated")
	}
}

func TestStoreOverlayReturnsCopy(t *testing.T) {
	s, _, _ := newTestStore(t, agentConfigYAML, "agent:\n  temperature: 0.3\n")
	first := s.Overlay()
	first["agent"].(map[string]any)["temperature"] = 9.9
	second := s.Overlay()
	if second["agent"].(map[string]any)["temperature"] != 0.3 {
		t.Fatal("Overlay must return a deep copy")
	}
}

func TestStoreWatchReloadsFileChanges(t *testing.T) {
	s, _, overlayPath := newTestStore(t, agentConfigYAML, "")
	if err := s.Watch(); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if err := os.WriteFile(overlayPath, []byte("agent:\n  temperature: 0.15\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if s.Snapshot().Agent.Temperature == 0.15 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("watcher did not reload overlay, temperature = %v", s.Snapshot().Agent.Temperature)
}

func TestMergeMapsNestedNullAndArrays(t *testing.T) {
	base := map[string]any{
		"a": map[string]any{"x": 1, "y": 2},
		"b": []any{1, 2, 3},
		"c": "keep",
	}
	src := map[string]any{
		"a": map[string]any{"y": 20, "z": 3},
		"b": []any{9},
		"c": nil,
	}
	got := mergeMaps(deepCopyMap(base), src)
	want := map[string]any{
		"a": map[string]any{"x": 1, "y": 20, "z": 3},
		"b": []any{9},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merge = %#v, want %#v", got, want)
	}
	// 源 map 不得被合并过程污染。
	if len(src) != 3 || src["c"] != nil {
		t.Fatalf("src mutated: %#v", src)
	}
	if _, ok := base["a"].(map[string]any)["z"]; ok {
		t.Fatal("base mutated after merge")
	}
}
