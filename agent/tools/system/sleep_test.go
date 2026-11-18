package system_test

import (
	"errors"
	"strings"
	"testing"

	agentruntime "Q-Mi/agent/runtime"
	systemtools "Q-Mi/agent/tools/system"
)

func TestSleepToolRequiresExplicitCallAndPropagatesStorageFailure(t *testing.T) {
	var reason string
	tools := systemtools.SleepTools(func(value string) (agentruntime.SleepSnapshot, error) {
		reason = value
		return agentruntime.SleepSnapshot{}, errors.New("disk unavailable")
	}, nil)
	if _, err := run(t, tools, "sleep", `{"reason":"休息一下"}`); err == nil || reason != "休息一下" {
		t.Fatalf("sleep swallowed failure, reason=%q err=%v", reason, err)
	}
	if _, err := run(t, systemtools.SleepTools(nil, nil), "sleep", `{}`); err == nil {
		t.Fatal("unavailable sleep silently succeeded")
	}
}

func TestSleepToolsNeverExposeMemoriesFromOtherChats(t *testing.T) {
	state := agentruntime.SleepSnapshot{Sleeping: true, Memories: []agentruntime.SleepMemory{
		{ChatType: "private", ChatID: 7, Text: "private-secret", SourceIDs: []int64{1}},
	}}
	tools := systemtools.SleepTools(func(string) (agentruntime.SleepSnapshot, error) { return state, nil },
		func() (agentruntime.SleepSnapshot, error) { return state, nil })
	for _, name := range []string{"sleep", "sleep_status"} {
		raw, err := run(t, tools, name, `{}`)
		if err != nil || strings.Contains(raw, "private-secret") {
			t.Fatalf("%s exposed scoped memories: %s %v", name, raw, err)
		}
	}
}
