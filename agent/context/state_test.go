package context_test

import (
	"path/filepath"
	"testing"

	agentcontext "Q-Mi/agent/context"
)

func TestPersistentStateCanBeReadByNextActivity(t *testing.T) {
	root := t.TempDir()
	notesPath := filepath.Join(root, "sub", "notes.json")
	if err := agentcontext.AppendNote(notesPath, "remember the current goal"); err != nil {
		t.Fatal(err)
	}
	notes, err := agentcontext.LoadNotes(notesPath)
	if err != nil || len(notes) != 1 || notes[0].Text != "remember the current goal" {
		t.Fatalf("notes = %#v, %v", notes, err)
	}
	statePath := filepath.Join(root, "state.json")
	if err := agentcontext.WriteState(statePath, "curious"); err != nil {
		t.Fatal(err)
	}
	state, err := agentcontext.ReadState(statePath)
	if err != nil || state.Mood != "curious" || state.Updated.IsZero() {
		t.Fatalf("state = %#v, %v", state, err)
	}
}
