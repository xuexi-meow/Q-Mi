package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltinToolDefinitionsRemainStable(t *testing.T) {
	tools := NewRegistry(defaultModules()...).Build(&ToolContext{})
	got, err := json.MarshalIndent(toolDefs(tools), "", "  ")
	if err != nil {
		t.Fatalf("marshal tool definitions: %v", err)
	}
	got = append(got, '\n')

	path := filepath.Join("testdata", "builtin-tool-definitions.json")
	if os.Getenv("UPDATE_TOOL_DEFINITIONS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create fixture directory: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write tool definition fixture: %v", err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tool definition fixture: %v; run with UPDATE_TOOL_DEFINITIONS=1 to create it", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("built-in tool definitions changed; run with UPDATE_TOOL_DEFINITIONS=1 only when the change is intentional")
	}
}
