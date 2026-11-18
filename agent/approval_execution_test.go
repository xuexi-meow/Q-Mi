package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"Q-Mi/utils/config"
)

func TestApprovedFileWriteRebuildsToolWithApprovedContext(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "approved.txt")
	results := make(chan error, 1)
	approvals := NewApprovalManager(nil, func(_ Approval, _ string, err error) {
		results <- err
	})
	ctx := &ToolContext{
		Workspace: filepath.Join(parent, "workspace"),
		Approvals: approvals,
		Turn:      &Turn{ChatType: "group", ChatID: 8, SelfID: 99},
	}
	mgr := &Manager{registry: NewRegistry(enabledModules(config.AgentConfig{})...)}
	args, err := json.Marshal(map[string]string{"path": target, "content": "approved"})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := mgr.execToolCall(ctx, buildTools(ctx), ToolCall{
		ID: "write-1", Function: ToolFunction{Name: "write_file", Arguments: string(args)},
	})
	if err != nil {
		t.Fatal(err)
	}
	var pending struct {
		ID int64 `json:"approval_id"`
	}
	if err := json.Unmarshal([]byte(receipt), &pending); err != nil || pending.ID == 0 {
		t.Fatalf("missing approval receipt: %s, %v", receipt, err)
	}
	if err := approvals.Resolve(pending.ID, true); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-results:
		if err != nil {
			t.Fatalf("approved write still denied: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval did not deliver result")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "approved" {
		t.Fatalf("approved file = %q, %v", data, err)
	}
	if ctx.Approved {
		t.Fatal("approval leaked back into ordinary tool context")
	}
}
