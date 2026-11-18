package agent

import (
	"path/filepath"
	"testing"

	agenttool "Q-Mi/agent/tool"
)

func TestToolContractWorksWithExistingExecutor(t *testing.T) {
	ctx := &ToolContext{NotesPath: filepath.Join(t.TempDir(), "notes.json")}
	var tools []agenttool.Tool = NewRegistry(defaultModules()...).Build(ctx)

	result, err := (&Manager{}).execToolCall(ctx, tools, ToolCall{
		Function: ToolFunction{Name: "think", Arguments: `{"note":"contract compatibility"}`},
	})
	if err != nil {
		t.Fatalf("execute tool contract: %v", err)
	}
	if result != "{}" {
		t.Fatalf("result = %q, want {}", result)
	}
	notes, err := LoadNotes(ctx.NotesPath)
	if err != nil {
		t.Fatalf("load notes: %v", err)
	}
	if len(notes) != 1 || notes[0].Text != "contract compatibility" {
		t.Fatalf("notes = %+v, want one persisted note", notes)
	}
}

func TestToolContractKeepsArgumentValidationBeforeExecution(t *testing.T) {
	for _, args := range []string{
		`{"note":`,
		`{}`,
		`{"note":"must not persist","_async":true}`,
	} {
		t.Run(args, func(t *testing.T) {
			ctx := &ToolContext{NotesPath: filepath.Join(t.TempDir(), "notes.json")}
			var tools []agenttool.Tool = coreTools(ctx)

			_, err := (&Manager{}).execToolCall(ctx, tools, ToolCall{
				Function: ToolFunction{Name: "think", Arguments: args},
			})
			if err == nil {
				t.Fatal("invalid arguments should be rejected")
			}
			notes, err := LoadNotes(ctx.NotesPath)
			if err != nil {
				t.Fatalf("load notes: %v", err)
			}
			if len(notes) != 0 {
				t.Fatalf("invalid call persisted notes: %+v", notes)
			}
		})
	}
}
