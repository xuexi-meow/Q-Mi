package computer_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"Q-Mi/agent/tool"
	"Q-Mi/agent/tools/computer"
)

func find(t *testing.T, deps *computer.Dependencies, name string) tool.Tool {
	t.Helper()
	for _, item := range computer.Tools(deps) {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("missing %s", name)
	return tool.Tool{}
}

func TestWriteOutsideRequiresExplicitApproval(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	deps := &computer.Dependencies{Workspace: root}
	args := json.RawMessage(`{"path":"../outside.txt","content":"approved content"}`)
	_, err := find(t, deps, "write_file").Run(args)
	var need tool.ApprovalRequired
	if !errors.As(err, &need) {
		t.Fatalf("outside write must request approval: %v", err)
	}
	target := filepath.Join(parent, "outside.txt")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("unapproved write touched target: %v", err)
	}
	deps.Approved = true
	if _, err := find(t, deps, "write_file").Run(args); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "approved content" {
		t.Fatalf("approved target = %q, %v", data, err)
	}
}

func TestApprovedCommandPassesDangerousCommandGate(t *testing.T) {
	deps := &computer.Dependencies{Workspace: t.TempDir()}
	args := json.RawMessage(`{"command":"echo shutdown"}`)
	_, err := find(t, deps, "run_command").Run(args)
	var need tool.ApprovalRequired
	if !errors.As(err, &need) {
		t.Fatalf("ordinary invocation must request approval: %v", err)
	}
	deps.Approved = true
	out, err := find(t, deps, "run_command").Run(args)
	if err != nil {
		t.Fatalf("approved command still denied: %v", err)
	}
	var result struct {
		Output string `json:"output"`
		Exit   int    `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil || result.Exit != 0 {
		t.Fatalf("command result = %s, %v", out, err)
	}
}
