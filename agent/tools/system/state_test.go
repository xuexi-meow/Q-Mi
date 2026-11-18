package system_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/tools/system"
)

func TestStateToolsPersistOnlyValidatedValues(t *testing.T) {
	root := t.TempDir()
	notes := filepath.Join(root, "notes.json")
	state := filepath.Join(root, "state.json")
	for _, item := range system.StateTools(notes, state) {
		switch item.Name {
		case "think":
			if _, err := item.Run(json.RawMessage(`{"note":""}`)); err == nil {
				t.Fatal("empty note accepted")
			}
			if _, err := item.Run(json.RawMessage(`{"note":"next step"}`)); err != nil {
				t.Fatal(err)
			}
		case "update_mood":
			if _, err := item.Run(json.RawMessage(`{"mood":"focused"}`)); err != nil {
				t.Fatal(err)
			}
		case "finish":
			if item.Run != nil {
				t.Fatal("finish must remain a runtime marker")
			}
		}
	}
	saved, err := agentcontext.LoadNotes(notes)
	if err != nil || len(saved) != 1 || saved[0].Text != "next step" {
		t.Fatalf("notes = %#v, %v", saved, err)
	}
	mood, err := agentcontext.ReadState(state)
	if err != nil || mood.Mood != "focused" {
		t.Fatalf("state = %#v, %v", mood, err)
	}
}
