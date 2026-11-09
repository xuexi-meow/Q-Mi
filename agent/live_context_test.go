package agent

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"Q-Mi/utils/config"
)

func newLiveManager(t *testing.T, script []string) (*Manager, *fakeLLM, config.AgentConfig) {
	t.Helper()
	cfg := testCfg(time.Millisecond)
	cfg.PersonaFile = writePersonaFile(t, "original-persona")
	cfg.GuideFile = writeGuideFile(t, "original-guide")
	cfg.WorkspaceDir, cfg.ImageDir = t.TempDir(), t.TempDir()
	f, srv := newFakeLLM(t, "")
	f.script = script
	builder := NewDefaultBuilder(nil, cfg)
	m := NewManager(builder, NewClient(srv.URL, "test", time.Second), cfg, nil, 999)
	m.notesPath, m.statePath = filepath.Join(t.TempDir(), "notes.json"), filepath.Join(t.TempDir(), "state.json")
	return m, f, cfg
}

func TestLiveReloadsCorePromptsAndLearnedStateBetweenRequests(t *testing.T) {
	m, f, cfg := newLiveManager(t, []string{
		toolCallResp("note", "think", `{"note":"new-research-step"}`),
		toolCallResp("pause", "finish", "{}"),
	})
	f.hook = func(idx int, _ *http.Request) {
		if idx == 0 {
			for path, text := range map[string]string{
				cfg.PersonaFile: "reloaded-persona", cfg.GuideFile: "reloaded-guide",
				filepath.Join(cfg.WorkspaceDir, "chat_style.md"): "short-human-chat-style",
			} {
				if err := os.WriteFile(path, []byte(text), 0600); err != nil {
					t.Error(err)
				}
			}
		}
	}
	m.handleTurn(&Turn{ChatType: "group", ChatID: 888, SelfID: 999, Now: time.Now(),
		NewMessages: []TurnMessage{{ChatType: "group", ChatID: 888, Text: "hello"}}})
	reqs := requestsSnapshot(f)
	if len(reqs) != 2 {
		t.Fatalf("requests=%d", len(reqs))
	}
	system := contentText(reqs[1].Messages[0])
	if !strings.Contains(system, "reloaded-persona") || !strings.Contains(system, "reloaded-guide") {
		t.Fatalf("core prompts were not hot reloaded: %s", system)
	}
	text := requestText(reqs[1])
	if !strings.Contains(text, "new-research-step") || !strings.Contains(text, "short-human-chat-style") {
		t.Fatalf("learned state missing in next request: %s", text)
	}
	if !reflect.DeepEqual(reqs[0].Messages[1:], reqs[1].Messages[1:len(reqs[0].Messages)]) {
		t.Fatal("hot reload rewrote an existing conversation message")
	}
}

func TestLiveDeletionAppendsReplacementSnapshot(t *testing.T) {
	m, f, cfg := newLiveManager(t, []string{
		toolCallResp("query", "get_recent_messages", "{}"),
		toolCallResp("pause", "finish", "{}"),
	})
	if err := AppendNote(m.notesPath, "obsolete-note"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg.WorkspaceDir, "chat_style.md")
	if err := os.WriteFile(path, []byte("obsolete-style"), 0600); err != nil {
		t.Fatal(err)
	}
	f.hook = func(idx int, _ *http.Request) {
		if idx == 0 {
			if err := os.Remove(m.notesPath); err != nil {
				t.Error(err)
			}
			if err := os.Remove(path); err != nil {
				t.Error(err)
			}
		}
	}
	m.handleTurn(&Turn{SelfID: 999, Now: time.Now(), NewMessages: []TurnMessage{{Text: "hello"}}})
	req := requestsSnapshot(f)[1]
	last := contentText(req.Messages[len(req.Messages)-1])
	if !strings.Contains(last, "[当前状态快照]") || strings.Contains(last, "obsolete-note") ||
		strings.Contains(last, "obsolete-style") {
		t.Fatalf("deletions must supersede old snapshots: %s", last)
	}
}

func TestLiveUnchangedStateDoesNotGrowTail(t *testing.T) {
	m, f, _ := newLiveManager(t, []string{
		toolCallResp("query", "get_recent_messages", "{}"),
		toolCallResp("pause", "finish", "{}"),
	})
	m.handleTurn(&Turn{SelfID: 999, Now: time.Now(), NewMessages: []TurnMessage{{Text: "hello"}}})
	reqs := requestsSnapshot(f)
	if len(reqs[1].Messages) != len(reqs[0].Messages)+2 {
		t.Fatalf("unchanged state was appended again: %+v", reqs[1].Messages)
	}
	if !reflect.DeepEqual(reqs[0].Messages, reqs[1].Messages[:len(reqs[0].Messages)]) {
		t.Fatal("unchanged request prefix was rewritten")
	}
}

func TestLiveCorePromptUsesLastGoodOnEmptyWrite(t *testing.T) {
	guidePath := writeGuideFile(t, "good-guide")
	b := NewDefaultBuilder(nil, config.AgentConfig{
		PersonaFile: writePersonaFile(t, "good-persona"), GuideFile: guidePath,
		ImageDir: t.TempDir(),
	})
	turn := &Turn{SelfID: 999}
	before := b.Build(turn)
	// File edits can briefly expose an empty document during save.
	if err := os.WriteFile(guidePath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	after := b.Build(turn)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("empty write replaced last-good guide")
	}
}

func TestBootstrapOmitsIncomingMessageAlreadyInStorage(t *testing.T) {
	store := openTestStore(t)
	now := time.Now()
	putRecord(t, store, 71, "recv", "group", 888, 111, "person", "unique-incoming", now.Unix())
	b := NewDefaultBuilder(store, config.AgentConfig{
		PersonaFile: writePersonaFile(t, "persona"), GuideFile: writeGuideFile(t, "guide"),
		ImageDir: t.TempDir(), History: config.AgentHistory{Max: 40, Trim: 20},
	})
	messages := b.Build(&Turn{SelfID: 999, Now: now,
		NewMessages: []TurnMessage{{MessageID: 71, ChatType: "group", ChatID: 888, Text: "unique-incoming", Time: now}}})
	text := requestText(ChatRequest{Messages: messages})
	if strings.Count(text, "unique-incoming") != 1 {
		t.Fatalf("incoming message duplicated by bootstrap history: %s", text)
	}
}
