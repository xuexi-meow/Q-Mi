package webui

import (
	"Q-Mi/utils/config"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectionRoutesRequireAuthAndGET(t *testing.T) {
	srv := startTestServer(t, "inspection-token")
	for _, path := range []string{"/api/runtime", "/api/context", "/api/tasks", "/api/context/message?version=1&index=0"} {
		response, _ := get(t, "http://"+srv.Addr+path, "")
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s permits anonymous inspection", path)
		}
		response, body := get(t, "http://"+srv.Addr+path, "inspection-token")
		if path == "/api/context/message?version=1&index=0" {
			if response.StatusCode != 404 && response.StatusCode != 409 {
				t.Fatalf("unexpected detail response: %d", response.StatusCode)
			}
		} else if response.StatusCode != 200 || !json.Valid([]byte(body)) {
			t.Fatalf("%s invalid response: %d %s", path, response.StatusCode, body)
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s permits caching", path)
		}
		response, _ = sendJSON(t, http.MethodPost, "http://"+srv.Addr+path, "inspection-token", "{}")
		if response.StatusCode != 405 || response.Header.Get("Allow") != "GET" {
			t.Fatalf("%s accepts writes", path)
		}
	}
}

func TestInspectionRejectsInvalidQueries(t *testing.T) {
	srv := startTestServer(t, "inspection-token")
	for _, query := range []string{"source=no", "kind=guess", "offset=-1", "limit=x", "chat_type=group", "chat_id=123", "chat_type=group&chat_id=-1"} {
		response, _ := get(t, "http://"+srv.Addr+"/api/context?"+query, "inspection-token")
		if response.StatusCode != 400 {
			t.Fatalf("%s accepted: %d", query, response.StatusCode)
		}
	}
}

func TestPromptUsesConfiguredPath(t *testing.T) {
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "persona.md"))
	store := newConfigStore(t, strings.Replace(configYAML("http://127.0.0.1:1"), `persona_file: "p.md"`, `persona_file: "`+path+`"`, 1))
	saved := configStoreProvider
	configStoreProvider = func() *config.Store { return store }
	t.Cleanup(func() { configStoreProvider = saved })
	w := httptest.NewRecorder()
	handleFile(w, httptest.NewRequest("PUT", "/api/file", strings.NewReader(`{"name":"persona","content":"configured content"}`)))
	if w.Code != 200 {
		t.Fatalf("write failed: %d %s", w.Code, w.Body)
	}
	data, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil || string(data) != "configured content" {
		t.Fatalf("configured path ignored: %q %v", data, err)
	}
	w = httptest.NewRecorder()
	handleFile(w, httptest.NewRequest("GET", "/api/file?name=../../Config", nil))
	if w.Code != 400 {
		t.Fatal("arbitrary filename accepted")
	}
}
