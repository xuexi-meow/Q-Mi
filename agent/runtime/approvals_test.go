package runtime_test

import (
	"testing"
	"time"

	agentruntime "Q-Mi/agent/runtime"
)

func TestApprovalResolvesOnceAndDeliversToOrigin(t *testing.T) {
	deliveries := make(chan agentruntime.Approval, 2)
	manager := agentruntime.NewApprovalManager(nil, func(a agentruntime.Approval, result string, err error) {
		if err != nil || result != "done" {
			t.Errorf("approval result = %q, %v", result, err)
		}
		deliveries <- a
	})
	id, err := manager.Request(agentruntime.Approval{
		Tool: "write_file", ChatType: "group", ChatID: 8, SelfID: 7,
		Run: func(approved bool) (string, error) {
			if !approved {
				t.Error("approval flag missing")
			}
			return "done", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Resolve(id, true); err != nil {
		t.Fatal(err)
	}
	if err := manager.Resolve(id, true); err == nil {
		t.Fatal("approval executed twice")
	}
	select {
	case a := <-deliveries:
		if a.ChatType != "group" || a.ChatID != 8 || a.SelfID != 7 || a.Status != "approved" {
			t.Fatalf("origin changed: %#v", a)
		}
	case <-time.After(time.Second):
		t.Fatal("approval result not delivered")
	}
}
