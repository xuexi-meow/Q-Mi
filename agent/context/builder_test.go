package context_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
	"Q-Mi/storage"
	"Q-Mi/utils/config"
)

func TestImageStoreDownloadsAndPersistsByMessageAnchor(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0, 'I', 'H', 'D', 'R'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(png)
	}))
	defer server.Close()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	images := agentcontext.NewImageStore(t.TempDir(), store)

	got := images.DataURLFor(81, 2, server.URL+"/image")
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	if got != want {
		t.Fatalf("DataURLFor() = %q, want %q", got, want)
	}
	refs, err := store.ImageRefs(81)
	if err != nil || len(refs) != 1 || refs[0].Idx != 2 {
		t.Fatalf("persisted image refs = %+v, %v", refs, err)
	}
	if _, err := os.Stat(images.PathForHash(refs[0].Sha256)); err != nil {
		t.Fatalf("content-addressed image was not saved: %v", err)
	}
}

func TestMessageViewResolvesMentionsAndScopedReply(t *testing.T) {
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.PutMessage(&storage.MessageRecord{
		MessageID: 21, Direction: "recv", ChatType: "group", ChatID: 8,
		UserID: 42, SenderName: "小明", RawMessage: "原消息",
	}); err != nil {
		t.Fatal(err)
	}
	message := agentcontext.TurnMessage{
		ChatType: "group", ChatID: 8, MessageID: 22, SenderID: 99,
		Segments: json.RawMessage(`[{"type":"reply","data":{"id":"21"}},{"type":"at","data":{"qq":"99"}},{"type":"text","data":{"text":"看这里"}}]`),
	}

	view := agentcontext.MessageView(store, 99, message, "live")
	if view["addressing_known"] != true || view["mentions_self"] != true {
		t.Fatalf("addressing metadata = %#v", view)
	}
	reply, ok := view["reply"].(map[string]any)
	if !ok || reply["status"] != "known" || reply["user_id"] != int64(42) || reply["text"] != "原消息" {
		t.Fatalf("reply metadata = %#v", view["reply"])
	}
	if got := view["text"]; got != "@99 看这里" {
		t.Fatalf("rendered text = %#v, want @99 看这里", got)
	}

	message.ChatID = 9
	view = agentcontext.MessageView(store, 99, message, "live")
	reply = view["reply"].(map[string]any)
	if reply["status"] != "unknown" || reply["user_id"] != nil || reply["text"] != nil {
		t.Fatalf("cross-chat reply leaked metadata: %#v", reply)
	}
}

func TestDefaultBuilderBuildsPersonaGuideAndOrderedMessages(t *testing.T) {
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Unix(1_762_400_000, 0)
	for _, record := range []*storage.MessageRecord{
		{MessageID: 1, Direction: "recv", ChatType: "group", ChatID: 8, UserID: 11, RawMessage: "older", Time: float64(now.Unix() - 2)},
		{MessageID: 2, Direction: "recv", ChatType: "group", ChatID: 8, UserID: 12, RawMessage: "newer", Time: float64(now.Unix() - 1)},
	} {
		if err := store.PutMessage(record); err != nil {
			t.Fatal(err)
		}
	}

	dir := t.TempDir()
	personaPath := filepath.Join(dir, "persona.md")
	guidePath := filepath.Join(dir, "guide.md")
	if err := os.WriteFile(personaPath, []byte("persona"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(guidePath, []byte("guide"), 0o600); err != nil {
		t.Fatal(err)
	}
	builder := agentcontext.NewDefaultBuilder(store, config.AgentConfig{
		PersonaFile: personaPath,
		GuideFile:   guidePath,
		ImageDir:    filepath.Join(dir, "images"),
		History:     config.AgentHistory{Max: 20, Trim: 5},
	})
	messages := builder.BuildMessages(&agentcontext.Turn{
		ChatType: "group", ChatID: 8, SelfID: 99, Now: now,
		NewMessages: []agentcontext.TurnMessage{
			{MessageID: 3, ChatType: "group", ChatID: 8, SenderID: 13, Text: "incoming", Time: now},
		},
	})

	if len(messages) != 4 {
		t.Fatalf("got %d messages, want system + 2 history + 1 incoming", len(messages))
	}
	system, ok := messages[0].Wire.Content.(string)
	if !ok || !strings.Contains(system, "persona\nguide") {
		t.Fatalf("system prefix missing persona/guide: %#v", messages[0].Wire.Content)
	}
	if got := messages[1].Wire.Content; !strings.Contains(contentString(got), "older") {
		t.Fatalf("first history message = %#v, want older", got)
	}
	if got := messages[2].Wire.Content; !strings.Contains(contentString(got), "newer") {
		t.Fatalf("second history message = %#v, want newer", got)
	}
	if got := messages[3].Wire.Content; !strings.Contains(contentString(got), "incoming") {
		t.Fatalf("last message = %#v, want incoming", got)
	}
}

func TestDefaultBuilderHistoryIsScopedToCurrentChat(t *testing.T) {
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for _, record := range []*storage.MessageRecord{
		{MessageID: 1, Direction: "recv", ChatType: "group", ChatID: 8, UserID: 11, RawMessage: "same chat history", Time: 100},
		{
			MessageID: 2, Direction: "recv", ChatType: "group", ChatID: 9, UserID: 12,
			RawMessage: "other group history", Segments: []byte(`[{"type":"image","data":{"url":"https://example.invalid/image"}}]`),
			Time: 101,
		},
	} {
		if err := store.PutMessage(record); err != nil {
			t.Fatal(err)
		}
	}

	multimodal := true
	builder := agentcontext.NewDefaultBuilder(store, config.AgentConfig{
		History:    config.AgentHistory{Max: 20, Trim: 5},
		Multimodal: &multimodal,
	})
	builder.SetImageResolver(func(_ int64, _ int, _ string) string {
		return "data:image/png;base64,AAAA"
	})
	messages := builder.BuildMessages(&agentcontext.Turn{
		ChatType: "group", ChatID: 8, Now: time.Unix(102, 0),
		NewMessages: []agentcontext.TurnMessage{{
			ChatType: "group", ChatID: 8, MessageID: 3, SenderID: 13,
			Text: "current input", Time: time.Unix(102, 0),
		}},
	})

	var history string
	for _, message := range messages[1 : len(messages)-1] {
		history += contentString(message.Wire.Content) + "\n"
	}
	if !strings.Contains(history, "same chat history") {
		t.Fatalf("current chat history was omitted: %q", history)
	}
	if strings.Contains(history, "other group history") {
		t.Fatalf("history from another group leaked into current context: %q", history)
	}
	for _, message := range messages {
		if parts, ok := message.Wire.Content.([]model.ContentPart); ok {
			for _, part := range parts {
				if part.Type == "image_url" {
					t.Fatalf("image from another group leaked into current context: %#v", parts)
				}
			}
		}
	}
}

func TestDefaultBuilderUsesInjectedFallbackPrompts(t *testing.T) {
	dir := t.TempDir()
	missingPersona := filepath.Join(dir, "missing-persona.md")
	missingGuide := filepath.Join(dir, "missing-guide.md")
	builder := agentcontext.NewDefaultBuilderWithFallbacks(nil, config.AgentConfig{
		PersonaFile: missingPersona,
		GuideFile:   missingGuide,
	}, "legacy persona fallback", "legacy guide fallback")

	system := builder.SystemMessage(&agentcontext.Turn{}).Wire.Content.(string)
	if !strings.Contains(system, "legacy persona fallback") ||
		!strings.Contains(system, "legacy guide fallback") {
		t.Fatalf("system prompt did not preserve injected fallbacks: %q", system)
	}
}

func TestHistoryWindowAnchorDoesNotDiscardAnotherChatsOlderMessages(t *testing.T) {
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, record := range []*storage.MessageRecord{
		{MessageID: 1, ChatType: "group", ChatID: 8, Direction: "recv", RawMessage: "first", Time: 100},
		{MessageID: 2, ChatType: "group", ChatID: 8, Direction: "recv", RawMessage: "second", Time: 101},
		{MessageID: 3, ChatType: "group", ChatID: 8, Direction: "recv", RawMessage: "third", Time: 102},
		{MessageID: 4, ChatType: "group", ChatID: 9, Direction: "recv", RawMessage: "older other chat", Time: 10},
	} {
		if err := store.PutMessage(record); err != nil {
			t.Fatal(err)
		}
	}
	builder := agentcontext.NewDefaultBuilder(store, config.AgentConfig{
		ImageDir: t.TempDir(), WorkspaceDir: t.TempDir(),
		History: config.AgentHistory{Max: 2, Trim: 1},
	})
	builder.BuildMessages(&agentcontext.Turn{ChatType: "group", ChatID: 8})
	messages := builder.BuildMessages(&agentcontext.Turn{ChatType: "group", ChatID: 9})
	if len(messages) != 2 || !strings.Contains(contentString(messages[1].Wire.Content), "older other chat") {
		t.Fatalf("another chat's eviction watermark discarded valid history: %#v", messages)
	}
}

func TestDefaultBuilderBuildsMultimodalInputSequence(t *testing.T) {
	dir := t.TempDir()
	personaPath := filepath.Join(dir, "persona.md")
	if err := os.WriteFile(personaPath, []byte("persona"), 0o600); err != nil {
		t.Fatal(err)
	}
	multimodal := true
	builder := agentcontext.NewDefaultBuilder(nil, config.AgentConfig{
		PersonaFile: personaPath, ImageDir: filepath.Join(dir, "images"),
		Multimodal: &multimodal,
	})
	builder.SetImageResolver(func(_ int64, _ int, _ string) string {
		return "data:image/png;base64,AAAA"
	})
	messages := builder.BuildMessages(&agentcontext.Turn{
		Now: time.Unix(1_762_400_000, 0),
		NewMessages: []agentcontext.TurnMessage{{
			MessageID: 7, Text: "photo", Images: []agentcontext.ImageRef{{Idx: 0, URL: "https://example.invalid/photo"}},
		}},
	})
	content, ok := messages[len(messages)-1].Wire.Content.([]model.ContentPart)
	if !ok || len(content) != 2 || content[0].Type != "text" || content[1].Type != "image_url" {
		t.Fatalf("multimodal content = %#v, want text then image_url", messages[len(messages)-1].Wire.Content)
	}
	if content[1].ImageURL == nil || content[1].ImageURL.URL != "data:image/png;base64,AAAA" {
		t.Fatalf("image content = %#v", content[1])
	}
}

func TestDefaultBuilderResolvesImagesThroughItsImageStore(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0, 'I', 'H', 'D', 'R'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(png)
	}))
	defer server.Close()
	multimodal := true
	builder := agentcontext.NewDefaultBuilder(nil, config.AgentConfig{
		ImageDir: t.TempDir(), Multimodal: &multimodal,
	})
	messages := builder.BuildMessages(&agentcontext.Turn{
		Now: time.Unix(1_762_400_000, 0),
		NewMessages: []agentcontext.TurnMessage{{
			MessageID: 91, Text: "photo", Images: []agentcontext.ImageRef{{Idx: 0, URL: server.URL + "/image"}},
		}},
	})
	parts, ok := messages[len(messages)-1].Wire.Content.([]model.ContentPart)
	if !ok || len(parts) != 2 || parts[1].ImageURL == nil ||
		parts[1].ImageURL.URL != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png) {
		t.Fatalf("builder image content = %#v, want downloaded data URL", messages[len(messages)-1].Wire.Content)
	}
}

func contentString(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	if parts, ok := content.([]model.ContentPart); ok && len(parts) > 0 {
		return parts[0].Text
	}
	return ""
}
