package system_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	agentcontext "Q-Mi/agent/context"
	agentruntime "Q-Mi/agent/runtime"
	"Q-Mi/agent/tool"
	systemtools "Q-Mi/agent/tools/system"
)

func run(t *testing.T, tools []tool.Tool, name, args string) (string, error) {
	t.Helper()
	for _, item := range tools {
		if item.Name == name {
			return item.Run(json.RawMessage(args))
		}
	}
	t.Fatalf("missing tool %q", name)
	return "", nil
}

func TestScheduleUsesOnlyBoundTurnAndSupportsCancellation(t *testing.T) {
	timers := agentruntime.NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), nil)
	tools := systemtools.Tools(&systemtools.Dependencies{
		Timer: timers, Turn: &agentcontext.Turn{ChatType: "group", ChatID: 8, SelfID: 99},
	})
	out, err := run(t, tools, "schedule", `{"delay_seconds":60,"note":"follow up"}`)
	if err != nil {
		t.Fatal(err)
	}
	rows := timers.List()
	if len(rows) != 1 || rows[0].ChatType != "group" || rows[0].ChatID != 8 || rows[0].SelfID != 99 || !strings.Contains(out, rows[0].ID) {
		t.Fatalf("timer source/output = %+v, %s", rows, out)
	}
	if _, err := run(t, tools, "cancel_timer", `{"timer_id":"`+rows[0].ID+`"}`); err != nil || timers.Active() != 0 {
		t.Fatalf("cancellation = %v, active=%d", err, timers.Active())
	}
}

func TestSelfEventsPreservesLegacyTextAndFilters(t *testing.T) {
	events := agentruntime.NewEventLog()
	events.Record("group", 8, agentruntime.KindOutput, "first\nline")
	events.Record("private", 9, agentruntime.KindOutput, "private")
	events.Record("group", 8, agentruntime.KindFinish, "finished")
	tools := systemtools.Tools(&systemtools.Dependencies{Events: events})
	out, err := run(t, tools, "self_events", `{"kind":"output","chat_id":8,"limit":1}`)
	if err != nil || !strings.HasPrefix(out, "#1 [") || !strings.Contains(out, "[群:8] output: first line") || strings.Contains(out, "private") {
		t.Fatalf("filtered event text = %q, %v", out, err)
	}
}

func TestMissingSystemServicesReturnErrorsAtExecutionOnly(t *testing.T) {
	tools := systemtools.Tools(nil)
	for _, name := range []string{"schedule", "cancel_timer", "task_list", "task_get", "task_cancel", "self_events"} {
		if _, err := run(t, tools, name, `{}`); err == nil {
			t.Fatalf("%s silently accepted an unavailable service", name)
		}
	}
}
