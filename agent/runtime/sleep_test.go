package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sleepTestSettings() SleepSettings {
	return SleepSettings{Min: 3 * time.Minute, Max: 15 * time.Minute, Window: 30 * time.Second, Threshold: 3}
}

func TestSleepRestoresDeadlineAndDeduplicatesDirectedWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sleep.json")
	now := time.Now()
	store, err := NewSleepStore(path, 99)
	if err != nil {
		t.Fatal(err)
	}
	started, err := store.Start(now, "休息一下", sleepTestSettings())
	if err != nil || started.WakeAt.Before(now.Add(3*time.Minute)) || started.WakeAt.After(now.Add(15*time.Minute)) {
		t.Fatalf("start = %+v, %v", started, err)
	}
	for i := 0; i < 2; i++ {
		woke, err := store.Observe(now, IncomingMessage{MessageID: 1}, true)
		if woke || err != nil {
			t.Fatalf("duplicate woke = %v, %v", woke, err)
		}
	}
	restored, err := NewSleepStore(path, 99)
	if err != nil || !restored.Snapshot(now).WakeAt.Equal(started.WakeAt) || restored.Snapshot(now).DirectedCount != 1 {
		t.Fatalf("restore = %+v, %v", restored, err)
	}
	restored.Observe(now.Add(time.Second), IncomingMessage{MessageID: 2}, true)
	woke, err := restored.Observe(now.Add(2*time.Second), IncomingMessage{MessageID: 3}, true)
	state := restored.Snapshot(now.Add(2 * time.Second))
	if !woke || err != nil || state.Sleeping || !state.PendingWake || state.WakeReason != "messages" {
		t.Fatalf("burst = %+v, %v", state, err)
	}
}

func TestSleepWindowExpiresAndStaleTimersCannotWakeNewSleep(t *testing.T) {
	store, _ := NewSleepStore(filepath.Join(t.TempDir(), "sleep.json"), 99)
	now := time.Now()
	first, _ := store.Start(now, "", sleepTestSettings())
	store.Observe(now, IncomingMessage{MessageID: 1}, true)
	store.Observe(now.Add(31*time.Second), IncomingMessage{MessageID: 2}, true)
	if store.Snapshot(now.Add(31*time.Second)).DirectedCount != 1 {
		t.Fatal("expired signal still counts")
	}
	if woke, _ := store.Wake(now, first.ID, "webui"); !woke {
		t.Fatal("forced wake failed")
	}
	if _, err := store.Start(now, "", sleepTestSettings()); err == nil {
		t.Fatal("new sleep discarded an unreviewed wake")
	}
	store.Acknowledge(first.ID)
	second, err := store.Start(now, "", sleepTestSettings())
	if err != nil {
		t.Fatal(err)
	}
	if woke, _ := store.Wake(now, first.ID, "timer"); woke || !store.Snapshot(now).Sleeping {
		t.Fatal("stale timer woke a different sleep")
	}
	if woke, _ := store.Wake(second.WakeAt, second.ID, "timer"); !woke {
		t.Fatal("natural wake failed")
	}
}

func TestSleepDuplicateCannotVoteAgainAfterWindowExpires(t *testing.T) {
	store, _ := NewSleepStore(filepath.Join(t.TempDir(), "sleep.json"), 99)
	now := time.Now()
	store.Start(now, "", sleepTestSettings())
	store.Observe(now, IncomingMessage{MessageID: 1}, true)
	store.Observe(now.Add(31*time.Second), IncomingMessage{MessageID: 2}, true)
	store.Observe(now.Add(32*time.Second), IncomingMessage{MessageID: 3}, true)
	woke, err := store.Observe(now.Add(33*time.Second), IncomingMessage{MessageID: 1}, true)
	if err != nil || woke || store.Snapshot(now.Add(33*time.Second)).DirectedCount != 2 {
		t.Fatalf("old duplicate counted as new message: woke=%v err=%v", woke, err)
	}
}

func TestSleepPersistenceFailureKeepsPreviousState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sleep.json")
	store, _ := NewSleepStore(path, 99)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(time.Now(), "", sleepTestSettings()); err == nil {
		t.Fatal("start reported success without persistence")
	}
	if store.Snapshot(time.Now()).Sleeping {
		t.Fatal("failed write changed live state")
	}
}

func TestSleepWakeFailureDoesNotReportTransition(t *testing.T) {
	store, _ := NewSleepStore(filepath.Join(t.TempDir(), "sleep.json"), 99)
	state, err := store.Start(time.Now(), "", sleepTestSettings())
	if err != nil {
		t.Fatal(err)
	}
	// A directory at the destination deterministically prevents the atomic rename.
	store.path = t.TempDir()
	woke, err := store.Wake(time.Now(), state.ID, "webui")
	if err == nil || woke || !store.Snapshot(time.Now()).Sleeping {
		t.Fatalf("failed write reported a wake: woke=%v err=%v", woke, err)
	}
}

func TestSleepObservationReportsWhetherMessageWasHeld(t *testing.T) {
	store, _ := NewSleepStore(filepath.Join(t.TempDir(), "sleep.json"), 99)
	now := time.Now()
	state, _ := store.Start(now, "", sleepTestSettings())
	held, woke, err := store.ObserveMessage(now, IncomingMessage{MessageID: 1}, false)
	if !held || woke || err != nil {
		t.Fatalf("sleeping message: held=%v woke=%v err=%v", held, woke, err)
	}
	store.Wake(now, state.ID, "webui")
	held, woke, err = store.ObserveMessage(now, IncomingMessage{MessageID: 2}, true)
	if held || woke || err != nil {
		t.Fatalf("awake message was swallowed: held=%v woke=%v err=%v", held, woke, err)
	}
}

func TestSleepDefersReceiptsAndRetainsScopedMemoriesAfterWake(t *testing.T) {
	store, _ := NewSleepStore(filepath.Join(t.TempDir(), "sleep.json"), 99)
	now := time.Now()
	state, _ := store.Start(now, "", sleepTestSettings())
	store.Observe(now, IncomingMessage{Wake: "background", SenderName: "后台任务", Text: "完成", ChatType: "group", ChatID: 8}, false)
	store.Observe(now, IncomingMessage{Wake: "background", SenderName: "轮询"}, false)
	memory := SleepMemory{ChatType: "group", ChatID: 8, Text: "昨天讨论了关卡", SourceIDs: []int64{7}}
	if err := store.CompleteMemory(state.ID, memory); err != nil {
		t.Fatal(err)
	}
	store.Wake(now, state.ID, "webui")
	snapshot := store.Snapshot(now)
	if len(snapshot.Deferred) != 1 || len(snapshot.Memories) != 1 {
		t.Fatalf("wake lost receipts or memories: %+v", snapshot)
	}
	if err := store.CompleteMemory(state.ID, memory); err == nil {
		t.Fatal("cancelled consolidation wrote memory")
	}
	store.Acknowledge(state.ID)
	if snapshot := store.Snapshot(now); snapshot.PendingWake || len(snapshot.Deferred) != 0 || len(snapshot.Memories) != 1 {
		t.Fatalf("ack cleared durable memory or retained receipt: %+v", snapshot)
	}
}
