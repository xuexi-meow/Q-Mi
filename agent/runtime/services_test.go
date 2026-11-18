package runtime_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	agentruntime "Q-Mi/agent/runtime"
)

func TestTaskCompletionPreservesSourceAndResult(t *testing.T) {
	delivered := make(chan agentruntime.BackgroundTask, 1)
	tasks := agentruntime.NewTaskManager(func(task agentruntime.BackgroundTask) { delivered <- task })
	id := tasks.Start(agentruntime.BackgroundTask{
		Tool: "download", ChatType: "group", ChatID: 8, SelfID: 99,
	}, func(context.Context) (string, error) { return `{"saved":true}`, nil })
	select {
	case task := <-delivered:
		if task.ID != id || task.ChatType != "group" || task.ChatID != 8 || task.SelfID != 99 ||
			task.Result != `{"saved":true}` || task.Status != agentruntime.TaskDone {
			t.Fatalf("completion lost source or result: %+v", task)
		}
	case <-time.After(time.Second):
		t.Fatal("task completion was not delivered")
	}
}

func TestRuntimeTimerPreservesSourceAndCancellation(t *testing.T) {
	fired := make(chan agentruntime.ScheduledTimer, 1)
	timers := agentruntime.NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), func(timer agentruntime.ScheduledTimer) { fired <- timer })
	item, err := timers.Schedule(time.Hour, "later", "private", 8, 99)
	if err != nil {
		t.Fatal(err)
	}
	if item.ChatType != "private" || item.ChatID != 8 || item.SelfID != 99 || !timers.Cancel(item.ID) || timers.Active() != 0 {
		t.Fatalf("timer source or cancellation changed: %+v", item)
	}
	if timers.Cancel(item.ID) {
		t.Fatal("cancelled timer remained active")
	}
	select {
	case <-fired:
		t.Fatal("cancelled timer fired")
	default:
	}
}

func TestRuntimeEventsKeepLiveRunIdentity(t *testing.T) {
	events := agentruntime.NewEventLog()
	run := events.BeginRun("group", 8, 1, "primary", "model")
	cursor := events.Cursor()
	events.UpdateRun(run, agentruntime.RunUpdate{ReasoningDelta: "thinking", ReceivedBytes: 42})
	rows, next, reset := events.WaitAfter(context.Background(), cursor, 0)
	if reset || next <= cursor || len(rows) != 1 || rows[0].RunID != run ||
		rows[0].Reasoning != "thinking" || rows[0].ChatID != 8 || rows[0].ReceivedBytes != 42 {
		t.Fatalf("live event = %+v, cursor=%d, reset=%v", rows, next, reset)
	}
}
