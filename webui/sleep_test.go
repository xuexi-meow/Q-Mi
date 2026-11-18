package webui

import (
	"encoding/json"
	"strings"
	"testing"

	"Q-Mi/agent"
	"Q-Mi/utils/config"
)

func TestSleepRoutesRequireAuthMethodsAndAvailableAgent(t *testing.T) {
	restore := agent.SetManagerForTest(nil)
	defer restore()
	srv := startTestServer(t, "sleep-token")
	base := "http://" + srv.Addr
	for _, path := range []string{"/api/sleep", "/api/sleep/wake"} {
		res, _ := get(t, base+path, "")
		if res.StatusCode != 401 {
			t.Fatalf("unauthorized access: %s %d", path, res.StatusCode)
		}
	}
	res, _ := get(t, base+"/api/sleep/wake", "sleep-token")
	if res.StatusCode != 405 || res.Header.Get("Allow") != "POST" {
		t.Fatalf("wake allowed GET: %d", res.StatusCode)
	}
	res, _ = sendJSON(t, "POST", base+"/api/sleep", "sleep-token", `{}`)
	if res.StatusCode != 405 || res.Header.Get("Allow") != "GET" {
		t.Fatalf("status allowed POST: %d", res.StatusCode)
	}
	res, raw := get(t, base+"/api/sleep", "sleep-token")
	if res.StatusCode != 503 || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("disabled status: %d %s", res.StatusCode, raw)
	}
	res, raw = sendJSON(t, "POST", base+"/api/sleep/wake", "sleep-token", "")
	if res.StatusCode != 503 {
		t.Fatalf("disabled wake: %d %s", res.StatusCode, raw)
	}
}

func TestSleepStatusAndWakeNeedNoMessageDraft(t *testing.T) {
	m := agent.NewManager(contextControlBuilder{}, nil, config.AgentConfig{
		WorkspaceDir: t.TempDir(), ImageDir: t.TempDir(),
	}, nil, 999)
	restore := agent.SetManagerForTest(m)
	defer restore()
	srv := startTestServer(t, "sleep-token")
	base := "http://" + srv.Addr
	for _, method := range []string{"GET", "POST"} {
		path := "/api/sleep"
		if method == "POST" {
			path += "/wake"
		}
		res, raw := sendJSON(t, method, base+path, "sleep-token", "")
		var state agent.SleepSnapshot
		if res.StatusCode != 200 || json.Unmarshal([]byte(raw), &state) != nil || state.SelfID != 999 || state.Sleeping {
			t.Fatalf("awake operation failed: %d %s", res.StatusCode, raw)
		}
		if strings.Contains(raw, `"signals"`) || strings.Contains(raw, `"deferred"`) {
			t.Fatalf("internal queue exposed: %s", raw)
		}
	}
}

func TestSleepConfigSaveValidatesAndPublishesHotChange(t *testing.T) {
	store := newConfigStore(t, configYAML("http://127.0.0.1:1"))
	before := store.Snapshot()
	code, body := callConfig(t, handleConfigPut, "PUT", `{"agent":{"sleep":{"enabled":true,"min_seconds":120,"max_seconds":300,"window_seconds":20,"threshold":6}}}`)
	if code != 200 {
		t.Fatalf("sleep config rejected: %d %s", code, body)
	}
	current := store.Snapshot()
	if current.Agent.Sleep.MinSeconds != 120 || current.Agent.Sleep.Threshold != 6 ||
		!strings.Contains(config.Diff(before, current).Summary(), "agent.sleep(hot)") {
		t.Fatalf("sleep config did not reach runtime subscribers: %+v", current.Agent.Sleep)
	}
	code, body = callConfig(t, handleConfigPut, "PUT", `{"agent":{"sleep":{"min_seconds":400,"max_seconds":300}}}`)
	if code != 400 || store.Snapshot().Agent.Sleep.MinSeconds != 120 {
		t.Fatalf("invalid bounds applied: %d %s", code, body)
	}
}
