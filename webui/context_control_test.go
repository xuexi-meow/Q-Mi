package webui

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"Q-Mi/agent"
	"Q-Mi/utils/config"
)

type contextControlBuilder struct{}

func (contextControlBuilder) Build(*agent.Turn) []agent.ChatMessage {
	return []agent.ChatMessage{{Role: "system", Content: "prompt"},
		{Role: "user", Content: `{"token":"original-secret","value":"body"}`}}
}

func TestContextControlHTTPPreviewApplyAndConflict(t *testing.T) {
	m := agent.NewManager(contextControlBuilder{}, nil, config.AgentConfig{
		Debounce: time.Millisecond, WorkspaceDir: t.TempDir(), ImageDir: t.TempDir(),
	}, nil, 999)
	restore := agent.SetManagerForTest(m)
	defer restore()
	m.Enqueue(agent.IncomingMessage{SelfID: 999, ChatType: "private", ChatID: 999, Text: "trigger"})
	deadline := time.Now().Add(2 * time.Second)
	for agent.RuntimeInspect().WorkingVersion == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	version := agent.RuntimeInspect().WorkingVersion
	if version == 0 {
		t.Fatal("activity did not publish a working snapshot")
	}
	srv := startTestServer(t, "edit-token")
	base := "http://" + srv.Addr
	var query struct {
		Version uint64 `json:"version"`
	}
	res, raw := get(t, base+"/api/context", "edit-token")
	if res.StatusCode != 200 || json.Unmarshal([]byte(raw), &query) != nil {
		t.Fatalf("context list unavailable: %d %s", res.StatusCode, raw)
	}
	params := "?version=" + strconv.FormatUint(query.Version, 10) + "&index=1"
	res, raw = get(t, base+"/api/context/edit"+params, "edit-token")
	if res.StatusCode != 200 || !strings.Contains(raw, "original-secret") {
		t.Fatalf("editor did not receive raw original: %d %s", res.StatusCode, raw)
	}
	text := "edited body"
	body := agent.ContextAction{Version: version, Action: "edit", Index: 1, Text: &text, Preview: true}
	data, _ := json.Marshal(body)
	res, raw = sendJSON(t, "POST", base+"/api/context/action", "edit-token", string(data))
	if res.StatusCode != 200 || agent.RuntimeInspect().WorkingVersion != version {
		t.Fatalf("preview changed snapshot: %d %s", res.StatusCode, raw)
	}
	body.Preview = false
	data, _ = json.Marshal(body)
	res, raw = sendJSON(t, "POST", base+"/api/context/action", "edit-token", string(data))
	if res.StatusCode != 200 || agent.RuntimeInspect().WorkingVersion == version {
		t.Fatalf("edit was not applied: %d %s", res.StatusCode, raw)
	}
	page, err := agent.ContextInspect(agent.ContextQuery{})
	if err != nil || len(page.Entries) < 2 || page.Metrics.MessageCount != 2 || page.Entries[1].Text != text {
		t.Fatalf("HTTP write did not update live context: %v, message count=%d", err, page.Metrics.MessageCount)
	}
	res, _ = sendJSON(t, "POST", base+"/api/context/action", "edit-token", string(data))
	if res.StatusCode != 409 {
		t.Fatalf("stale HTTP write accepted: %d", res.StatusCode)
	}
}

func TestContextControlRoutesAuthenticateAndValidateBeforeAccess(t *testing.T) {
	restore := agent.SetManagerForTest(nil)
	defer restore()
	srv := startTestServer(t, "edit-token")
	base := "http://" + srv.Addr
	for _, path := range []string{"/api/context/edit?version=1&index=0", "/api/context/action"} {
		res, _ := get(t, base+path, "")
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("anonymous context access accepted: %s %d", path, res.StatusCode)
		}
	}
	res, _ := get(t, base+"/api/context/edit?version=1&index=0", "edit-token")
	if res.StatusCode != 503 || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("missing agent status/cache: %d", res.StatusCode)
	}
	res, _ = get(t, base+"/api/context/action", "edit-token")
	if res.StatusCode != 405 || res.Header.Get("Allow") != "POST" {
		t.Fatalf("action permits GET: %d", res.StatusCode)
	}
	for _, body := range []string{
		`{`, `{}`, `{"version":1,"action":"other"}`,
		`{"version":1,"action":"reset","source":"request"}`,
		`{"version":1,"action":"reset"} {}`,
		`{"version":1,"action":"delete"}`,
		`{"version":1,"action":"edit","text":"x"}`,
		`{"version":1,"action":"edit","index":-1,"text":"x"}`,
		`{"version":1,"action":"reset","text":"` + strings.Repeat("x", 3*1024*1024) + `"}`,
	} {
		res, text := sendJSON(t, "POST", base+"/api/context/action", "edit-token", body)
		if res.StatusCode != 400 && res.StatusCode != 413 {
			t.Fatalf("invalid action accepted: status=%d response=%s", res.StatusCode, text)
		}
	}
	res, _ = sendJSON(t, "POST", base+"/api/context/action", "edit-token", `{"version":1,"action":"clean","preview":true}`)
	if res.StatusCode != 503 {
		t.Fatalf("action initialized disabled agent: %d", res.StatusCode)
	}
}
