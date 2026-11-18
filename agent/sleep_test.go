package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcontext "Q-Mi/agent/context"
	agentruntime "Q-Mi/agent/runtime"
	"Q-Mi/storage"
	"Q-Mi/utils/config"
)

func TestSleepHoldsAlreadyTakenTurnAndRejectsContextResume(t *testing.T) {
	m, f, _ := newLoopManager(t, testCfg(time.Millisecond), &stubBuilder{}, openTestStore(t), nil)
	m.handleTurn(&Turn{SelfID: 999, NewMessages: []TurnMessage{{Text: "before sleep"}}})
	version := m.InspectRuntime().WorkingVersion
	controller, _ := m.sleepController()
	controller.Start(time.Now(), "", agentruntime.SleepSettings{Min: time.Minute, Max: time.Minute, Window: time.Second, Threshold: 2})
	m.handleTurn(&Turn{SelfID: 999, NewMessages: []TurnMessage{{Text: "pending before sleep"}}})
	if f.count() != 1 {
		t.Fatal("an already-taken turn ran after sleep started")
	}
	_, err := m.ControlContext(context.Background(), ContextAction{Version: version, Action: "resume"})
	if err == nil || f.count() != 1 {
		t.Fatalf("resume bypassed sleep: %v", err)
	}
}

func TestSleepWakeExcerptKeepsDirectedAnchorsWhenChatIsBusy(t *testing.T) {
	st := openTestStore(t)
	m, _, _ := newLoopManager(t, testCfg(time.Millisecond), &stubBuilder{}, st, nil)
	controller, _ := m.sleepController()
	now := time.Now().Add(-time.Minute).Truncate(time.Second)
	state, _ := controller.Start(now, "", agentruntime.SleepSettings{Min: time.Minute, Max: time.Minute, Window: 30 * time.Second, Threshold: 5})
	for i := int64(1); i <= 10; i++ {
		st.PutMessage(&storage.MessageRecord{MessageID: i, ChatType: "group", ChatID: 8, UserID: 111,
			RawMessage: "chat message", Time: float64(now.Add(time.Duration(i) * time.Second).Unix()),
			Segments: json.RawMessage(`[{"type":"at","data":{"qq":"999"}}]`)})
	}
	controller.Observe(now.Add(time.Second), IncomingMessage{MessageID: 1, ChatType: "group", ChatID: 8}, true)
	controller.Wake(time.Now(), state.ID, "webui")
	turn, err := m.sleepWakeTurn(controller.Snapshot(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(turn.NewMessages)
	if !strings.Contains(string(raw), `\"message_id\":1,`) {
		t.Fatalf("directed anchor lost in latest-message excerpt: %s", raw)
	}
}

func TestSleepDirectedTargetsUseActualSegmentsAndReplyAuthor(t *testing.T) {
	st := openTestStore(t)
	for _, record := range []*storage.MessageRecord{
		{MessageID: 70, ChatType: "group", ChatID: 8, UserID: 999, RawMessage: "own"},
		{MessageID: 71, ChatType: "group", ChatID: 8, UserID: 111, RawMessage: "other"},
		{MessageID: 72, ChatType: "group", ChatID: 9, UserID: 999, RawMessage: "foreign"},
	} {
		if err := st.PutMessage(record); err != nil {
			t.Fatal(err)
		}
	}
	m, _, _ := newLoopManager(t, testCfg(time.Millisecond), &stubBuilder{}, st, nil)
	for _, tc := range []struct {
		name, kind, segments string
		sender               int64
		want                 bool
	}{
		{"self at", "group", `[{"type":"at","data":{"qq":"999"}}]`, 111, true},
		{"other at", "group", `[{"type":"at","data":{"qq":"111"}}]`, 111, false},
		{"all", "group", `[{"type":"at","data":{"qq":"all"}}]`, 111, false},
		{"own reply", "group", `[{"type":"reply","data":{"id":"70"}}]`, 111, true},
		{"other reply", "group", `[{"type":"reply","data":{"id":"71"}}]`, 111, false},
		{"foreign reply", "group", `[{"type":"reply","data":{"id":"72"}}]`, 111, false},
		{"plain nickname", "group", `[{"type":"text","data":{"text":"@999"}}]`, 111, false},
		{"private", "private", `[]`, 111, true},
		{"self echo", "private", `[]`, 999, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := IncomingMessage{ChatType: tc.kind, ChatID: 8, SelfID: 999, SenderID: tc.sender,
				Segments: json.RawMessage(tc.segments), Mentioned: true}
			if got := m.sleepDirected(message); got != tc.want {
				t.Fatalf("directed=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestSleepBlocksMessagesAndPollingButArchiveSurvives(t *testing.T) {
	st := openTestStore(t)
	builder := &stubBuilder{}
	m, f, _ := newLoopManager(t, testCfg(time.Millisecond), builder, st, nil)
	controller, err := m.sleepController()
	if err != nil {
		t.Fatal(err)
	}
	controller.Start(time.Now(), "", agentruntime.SleepSettings{Min: time.Minute, Max: time.Minute, Window: 30 * time.Second, Threshold: 5})
	message := msg("group", 8, "睡眠期间的消息")
	message.MessageID = 81
	m.Enqueue(message)
	m.Enqueue(IncomingMessage{Wake: WakeBackground, SenderName: "轮询", SelfID: 999})
	if f.count() != 0 || m.inbox != nil {
		t.Fatal("sleeping account started a chat activity")
	}
	record, err := st.GetMessage(81)
	if err != nil || record == nil || record.RawMessage != "睡眠期间的消息" {
		t.Fatalf("archive=%+v err=%v", record, err)
	}
	if _, err := m.ForceWake(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second*3, func() bool { return f.count() == 1 }, "forced wake")
	m.runMu.Lock()
	defer m.runMu.Unlock()
	raw, _ := json.Marshal(builder.turn(0).NewMessages)
	text := string(raw)
	if !strings.Contains(text, "离线") || !strings.Contains(text, "睡眠期间的消息") {
		t.Fatalf("wake lost archived context: %s", text)
	}
}

func TestSleepMemoryProviderNeverReturnsAnotherScope(t *testing.T) {
	st := openTestStore(t)
	st.PutMessage(&storage.MessageRecord{MessageID: 1, ChatType: "private", ChatID: 8, UserID: 111, RawMessage: "私聊信息"})
	m, _, _ := newLoopManager(t, testCfg(time.Millisecond), &stubBuilder{}, st, nil)
	controller, _ := m.sleepController()
	state, _ := controller.Start(time.Now(), "", agentruntime.SleepSettings{Min: time.Minute, Max: time.Minute, Window: time.Second, Threshold: 2})
	controller.CompleteMemory(state.ID, agentruntime.SleepMemory{ChatType: "private", ChatID: 8, Text: "私聊信息", SourceIDs: []int64{1}})
	provider := sleepMemoryProvider{manager: m}
	input := agentcontext.ProviderInput{Scope: agentcontext.ProviderScope{SelfID: 999, ChatType: "group", ChatID: 8}}
	fragment, err := provider.State(input)
	if err != nil || fragment.Text != "" {
		t.Fatalf("private memory leaked to group: %+v %v", fragment, err)
	}
	input.Scope.ChatType = "private"
	fragment, err = provider.State(input)
	if err != nil || !strings.Contains(fragment.Text, "私聊信息") {
		t.Fatalf("own scope not recalled: %+v %v", fragment, err)
	}
	st.MarkRecalled(1)
	fragment, err = provider.State(input)
	if err != nil || fragment.Text != "" {
		t.Fatalf("recalled evidence still used: %+v %v", fragment, err)
	}
}

func TestSleepRestoreExpiredDeadlineAndNaturalWakeWithoutMessages(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "timer", true: "expired restart"}[expired], func(t *testing.T) {
			m, f, _ := newLoopManager(t, testCfg(time.Millisecond), &stubBuilder{}, openTestStore(t), nil)
			controller, _ := m.sleepController()
			now := time.Now()
			if expired {
				now = now.Add(-time.Minute)
			}
			started, err := controller.Start(now, "", agentruntime.SleepSettings{Min: time.Second, Max: time.Second, Window: time.Second, Threshold: 2})
			if err != nil {
				t.Fatal(err)
			}
			if err := m.RestoreSleep(); err != nil {
				t.Fatal(err)
			}
			waitFor(t, 3*time.Second, func() bool { s, _ := m.SleepStatus(); return !s.Sleeping && !s.PendingWake && f.count() == 1 }, "natural wake")
			state, _ := m.SleepStatus()
			if state.WakeReason != "timer" || !state.WakeAt.Equal(started.WakeAt) {
				t.Fatalf("resampled restored sleep: %+v", state)
			}
		})
	}
}

func TestSleepShutdownCancelsMaintenanceAndKeepsDeadline(t *testing.T) {
	m, f, _ := newLoopManager(t, testCfg(time.Millisecond), &stubBuilder{}, openTestStore(t), nil)
	controller, _ := m.sleepController()
	state, _ := controller.Start(time.Now(), "", agentruntime.SleepSettings{Min: time.Minute, Max: time.Minute, Window: time.Second, Threshold: 2})
	ctx, cancel := context.WithCancel(context.Background())
	m.sleepCancel = cancel
	m.stopSleep()
	if ctx.Err() == nil {
		t.Fatal("shutdown did not cancel maintenance")
	}
	m.launchSleepWake(state.ID)
	if m.sleepWakeID != "" || f.count() != 0 || !controller.Snapshot(time.Now()).Sleeping {
		t.Fatal("shutdown lost durable sleep or started a wake")
	}
	// State stays available on disk for the next process.
	if _, err := os.Stat(filepath.Join(filepath.Dir(m.statePath), "sleep.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSleepBurstWakeCancelsStreamingMaintenanceAndDoesNotWaitForIt(t *testing.T) {
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": connected\n\n"))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(cancelled)
		<-release
	}))
	defer server.Close()
	defer close(release)
	cfg := testCfg(time.Millisecond)
	cfg.Models = map[string]config.ModelTier{"dream": {Model: "small-model", BaseURL: server.URL, APIKey: "test"}}
	cfg.WakeRoutes = map[string]string{WakeBackground: "dream"}
	st := openTestStore(t)
	st.PutMessage(&storage.MessageRecord{MessageID: 91, ChatType: "group", ChatID: 8, UserID: 111,
		RawMessage: "刚才聊的第二关还没有过", Time: float64(time.Now().Add(-time.Minute).Unix())})
	m, f, _ := newLoopManager(t, cfg, &stubBuilder{}, st, nil)
	defer m.stopSleep()
	_, err := m.RequestSleep("休息")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("maintenance never started")
	}
	// Change the route only for subsequent foreground requests. The in-flight
	// maintenance call keeps its old candidate and remains cancellable.
	next := cfg
	next.WakeRoutes = map[string]string{WakeBackground: "chat"}
	m.ApplyConfig(next)
	for i := int64(1); i <= 4; i++ {
		message := msg("group", 8, "叫你一下")
		message.MessageID = 100 + i
		message.Segments = json.RawMessage(`[{"type":"at","data":{"qq":"999"}}]`)
		m.Enqueue(message)
		m.Enqueue(message)
	}
	state, _ := m.SleepStatus()
	if !state.Sleeping || state.DirectedCount != 4 {
		t.Fatalf("woke too soon: %+v", state)
	}
	last := msg("group", 8, "醒醒")
	last.MessageID = 105
	last.Segments = json.RawMessage(`[{"type":"at","data":{"qq":"999"}}]`)
	m.Enqueue(last)
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("wake did not cancel streaming request")
	}
	waitFor(t, 2*time.Second, func() bool { s, _ := m.SleepStatus(); return f.count() == 1 && !s.PendingWake }, "wake proceeds while remote worker still pending")
	state, _ = m.SleepStatus()
	if state.Sleeping || state.WakeReason != "messages" || len(state.Memories) != 0 || state.Consolidation != "interrupted" {
		t.Fatalf("late maintenance changed waking state: %+v", state)
	}
}

func TestSleepMemoryUsesBoundedToolFreeRequestsAndChecksEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body, finish string
		want               bool
	}{
		{"valid", `{"summary":"小林仍在研究第二关。","source_ids":[91]}`, "stop", true},
		{"invented source", `{"summary":"没有依据","source_ids":[99999]}`, "stop", false},
		{"foreign field", `{"summary":"内容","source_ids":[91],"chat_id":9}`, "stop", false},
		{"truncated", `{"summary":"内容","source_ids":[91]}`, "length", false},
		{"trailing content", `{"summary":"内容","source_ids":[91]} {}`, "stop", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t)
			record := &storage.MessageRecord{MessageID: 91, ChatType: "group", ChatID: 8, UserID: 111,
				RawMessage: "第二关还是没过", Time: float64(time.Now().Add(-time.Minute).Unix())}
			if err := st.PutMessage(record); err != nil {
				t.Fatal(err)
			}
			response, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": tc.body}, "finish_reason": tc.finish,
			}}})
			m, f, _ := newLoopManager(t, testCfg(time.Millisecond), &stubBuilder{}, st, []string{string(response)})
			controller, _ := m.sleepController()
			state, _ := controller.Start(time.Now(), "", agentruntime.SleepSettings{Min: time.Minute, Max: time.Minute, Window: time.Second, Threshold: 2})
			err := m.consolidateSleepScope(context.Background(), controller, state, "group", 8, []*storage.MessageRecord{record})
			if (err == nil) != tc.want {
				t.Fatalf("result=%v want success=%v", err, tc.want)
			}
			if f.count() != 1 || len(f.reqs[0].Tools) != 0 || f.reqs[0].MaxTokens <= 0 || estimatedInputTokens(f.reqs[0].Messages, nil) > 4000 {
				t.Fatalf("unbounded request: %+v", f.reqs)
			}
			if format := f.reqs[0].ResponseFormat; format == nil || format.Type != "json_object" {
				t.Fatal("maintenance request did not enable JSON output")
			}
			snapshot := controller.Snapshot(time.Now())
			if (len(snapshot.Memories) > 0) != tc.want {
				t.Fatalf("invalid result committed: %+v", snapshot.Memories)
			}
		})
	}
}
