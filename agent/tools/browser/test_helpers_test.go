package browser

import (
	"encoding/json"
	"testing"
	"time"
)

func findTool(t *testing.T, tools []Tool, name string) Tool {
	t.Helper()
	for _, item := range tools {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("missing tool %s", name)
	return Tool{}
}

func runToolOK(t *testing.T, tools []Tool, name, args string) string {
	t.Helper()
	out, err := findTool(t, tools, name).Run(json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func runToolErr(t *testing.T, tools []Tool, name, args string) error {
	t.Helper()
	_, err := findTool(t, tools, name).Run(json.RawMessage(args))
	if err == nil {
		t.Fatalf("%s: expected error", name)
	}
	return err
}

func objOf(t *testing.T, text string) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(description)
}
