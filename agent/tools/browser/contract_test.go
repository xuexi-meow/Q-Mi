package browser_test

import (
	"encoding/json"
	"strings"
	"testing"

	"Q-Mi/agent/tools/browser"
)

func TestUnavailableBrowserReturnsErrorWithoutStartingProcess(t *testing.T) {
	for _, item := range browser.Tools(&browser.Dependencies{Workspace: t.TempDir()}) {
		if item.Name != "browser_navigate" {
			continue
		}
		if _, err := item.Run(json.RawMessage(`{"url":"https://example.com"}`)); err == nil ||
			!strings.Contains(err.Error(), "浏览器不可用") {
			t.Fatalf("nil browser = %v", err)
		}
		return
	}
	t.Fatal("missing navigation tool")
}
