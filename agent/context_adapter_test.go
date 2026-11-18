package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/storage"
	"Q-Mi/utils/config"
)

func TestDefaultContextBuilderPreservesLegacyFallbackPrompts(t *testing.T) {
	dir := t.TempDir()
	builder := newDefaultContextBuilder(nil, config.AgentConfig{
		PersonaFile: filepath.Join(dir, "missing-persona.md"),
		GuideFile:   filepath.Join(dir, "missing-guide.md"),
		ImageDir:    filepath.Join(dir, "images"),
	})

	system := builder.SystemMessage(&Turn{SelfID: 999})
	content, ok := system.Content.(string)
	if !ok || !strings.Contains(content, fallbackPersona) || !strings.Contains(content, agentGuideText) {
		t.Fatalf("default context builder did not preserve legacy fallbacks: %#v", system.Content)
	}
	if builder.Images() == nil {
		t.Fatal("default context builder did not expose its image cache")
	}
}

func TestDefaultBuilderPublicEntryScopesHistoryAndPreservesMessageLayout(t *testing.T) {
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Unix(1_762_400_000, 0)
	for _, record := range []*storage.MessageRecord{
		{MessageID: 1, Direction: "recv", ChatType: "group", ChatID: 8, UserID: 11, SenderName: "A", RawMessage: "older", Time: float64(now.Unix() - 2)},
		{MessageID: 2, Direction: "recv", ChatType: "group", ChatID: 8, UserID: 12, SenderName: "B", RawMessage: "newer", Time: float64(now.Unix() - 1)},
		{MessageID: 4, Direction: "recv", ChatType: "private", ChatID: 7, UserID: 7, RawMessage: "private secret", Time: float64(now.Unix())},
	} {
		if err := store.PutMessage(record); err != nil {
			t.Fatal(err)
		}
	}

	dir := t.TempDir()
	personaPath := filepath.Join(dir, "persona.md")
	guidePath := filepath.Join(dir, "guide.md")
	for path, content := range map[string]string{personaPath: "persona", guidePath: "guide"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.AgentConfig{
		PersonaFile: personaPath,
		GuideFile:   guidePath,
		ImageDir:    filepath.Join(dir, "images"),
		History:     config.AgentHistory{Max: 8, Trim: 2},
	}
	builder := NewDefaultBuilder(store, cfg)
	builder.SetToolDocs("shared tools")

	turn := &Turn{
		ChatType: "group", ChatID: 8, SelfID: 999, Now: now,
		Mood: "calm", Notes: []Note{{Time: now.Add(-time.Minute), Text: "remember"}},
		NewMessages: []TurnMessage{
			{MessageID: 3, ChatType: "group", ChatID: 8, SenderID: 13, SenderName: "C", Text: "incoming", Time: now},
		},
	}
	for name, messages := range map[string][]ChatMessage{
		"normal": builder.Build(turn), "working": builder.BuildWorking(turn),
		"continuation": builder.BuildContinuation(turn),
	} {
		for _, message := range messages {
			if strings.Contains(contentText(message), "private secret") {
				t.Fatalf("%s leaked another chat's history", name)
			}
		}
		if contentText(messages[0]) != "persona\nguide\n\nshared tools\n当前账号QQ: 999" {
			t.Fatalf("%s changed the system prefix: %#v", name, messages[0])
		}
		if !strings.Contains(contentText(messages[len(messages)-1]), `"message_id":3`) {
			t.Fatalf("%s lost the active input at the tail", name)
		}
	}
	working := builder.BuildWorkingMessages(turn)
	if len(working) != 5 || working[3].Kind != agentcontext.KindState || working[4].Kind != agentcontext.KindActiveInput {
		t.Fatalf("working layout = %#v, want system, 2 history rows, state and active input", working)
	}
}

func TestContextBuilderAdapterPreservesManagerContract(t *testing.T) {
	dir := t.TempDir()
	personaPath := filepath.Join(dir, "persona.md")
	guidePath := filepath.Join(dir, "guide.md")
	for path, content := range map[string]string{
		personaPath: "persona",
		guidePath:   "guide",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	multimodal := true
	nextPersonaPath := filepath.Join(dir, "persona-next.md")
	if err := os.WriteFile(nextPersonaPath, []byte("persona next"), 0o600); err != nil {
		t.Fatal(err)
	}
	builder := agentcontext.NewDefaultBuilder(nil, config.AgentConfig{
		PersonaFile:  personaPath,
		GuideFile:    guidePath,
		ImageDir:     filepath.Join(dir, "images"),
		WorkspaceDir: filepath.Join(dir, "workspace"),
		Multimodal:   &multimodal,
	})
	adapter := newContextBuilderAdapter(builder)
	adapter.SetToolDocs("tool docs")

	m := NewManager(adapter, nil, testCfg(time.Hour), nil, 999)
	if m.images == nil || m.images.inner != builder.Images() {
		t.Fatal("manager and context builder do not share the image store")
	}

	turn := &Turn{
		ChatType: "group",
		ChatID:   8,
		SelfID:   999,
		Now:      time.Unix(1_762_400_000, 0),
		NewMessages: []TurnMessage{{
			MessageID: 3, ChatType: "group", ChatID: 8, SenderID: 13,
			Text: "incoming", Time: time.Unix(1_762_400_000, 0),
		}},
	}
	built := adapter.Build(turn)
	if len(built) != 2 || built[0].Role != "system" || !strings.Contains(built[0].Content.(string), "tool docs") {
		t.Fatalf("Build() = %#v, want system prompt plus incoming message and tool docs", built)
	}
	if !strings.Contains(built[1].Content.(string), "incoming") {
		t.Fatalf("Build() last message = %#v, want incoming message", built[1])
	}

	working := adapter.BuildWorkingMessages(turn)
	if len(working) != 3 || working[1].Kind != agentcontext.KindState || working[2].Kind != agentcontext.KindActiveInput {
		t.Fatalf("working message kinds = %#v, want state then active input", working)
	}
	continuation := adapter.BuildContinuation(turn)
	if len(continuation) != 2 || strings.Contains(continuation[0].Content.(string), "[当前状态快照]") {
		t.Fatalf("continuation = %#v, want system and new input without state replay", continuation)
	}
	if adapter.StateText(turn) == "" || adapter.SystemMessage(turn).Role != "system" {
		t.Fatal("adapter did not expose state and system message")
	}

	next := config.AgentConfig{
		PersonaFile:  nextPersonaPath,
		GuideFile:    guidePath,
		WorkspaceDir: filepath.Join(dir, "next-workspace"),
		History:      config.AgentHistory{Max: 12, Trim: 4},
	}
	adapter.Apply(next)
	if got := adapter.SystemMessage(turn).Content.(string); !strings.Contains(got, "persona next") {
		t.Fatalf("Apply() did not update the prompt path: %q", got)
	}
}
