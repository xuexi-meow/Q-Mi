package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"net/http"
)

func TestInspectionSnapshotDoesNotAliasConversation(t *testing.T) {
	m := &Manager{}
	messages := []ChatMessage{{Role: "assistant", Content: "draft", ToolCalls: []ToolCall{{
		ID: "a", Type: "function", Function: ToolFunction{Name: "finish", Arguments: "{}"},
	}}}}
	m.publishInspection("working", messages, nil)
	messages[0].ToolCalls[0].Function.Name = "changed"
	page, err := m.InspectContext(ContextQuery{Source: "working", Limit: 40})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ToolCalls[0].Function.Name != "finish" {
		t.Fatalf("snapshot aliases conversation: %+v / %v", page, err)
	}
	page.Entries[0].ToolCalls[0].Function.Name = "caller-change"
	again, _ := m.InspectContext(ContextQuery{Source: "working", Limit: 40})
	if again.Entries[0].ToolCalls[0].Function.Name != "finish" {
		t.Fatal("query aliases published snapshot")
	}
}

func TestInspectionRequestIsNonblockingAndFinishPublishesTools(t *testing.T) {
	m, f, _ := newLoopManager(t,testCfg(time.Millisecond),&stubBuilder{},nil,nil)
	entered, release, done := make(chan struct{}),make(chan struct{}),make(chan struct{})
	f.mu.Lock()
	f.hook=func(_ int,_ *http.Request){close(entered);<-release}
	f.mu.Unlock()
	go func(){defer close(done);m.handleTurn(&Turn{ChatType:"group",ChatID:888,Now:time.Now(),NewMessages:[]TurnMessage{{Text:"hello"}}})}()
	select {case <-entered:case <-time.After(3*time.Second):t.Fatal("request did not start")}
	r:=m.InspectRuntime()
	if r.Phase!="requesting"||r.RequestVersion==0||r.Candidate=="" {close(release);t.Fatalf("actual request not published: %+v",r)}
	close(release)
	select {case <-done:case <-time.After(3*time.Second):t.Fatal("finish did not complete")}
	r=m.InspectRuntime()
	if r.Phase!="idle"||r.WorkingVersion<=r.RequestVersion {t.Fatalf("finish not finalized: %+v",r)}
	p,_:=m.InspectContext(ContextQuery{Kind:"tool_result"})
	if len(p.Entries)!=1||p.Entries[0].ToolCallID!="done" {t.Fatalf("tool result missing: %+v",p)}
}

func TestInspectionDoesNotWaitForActivity(t *testing.T) {
	m := &Manager{}
	m.runMu.Lock()
	defer m.runMu.Unlock()
	done := make(chan RuntimeInspection, 1)
	go func() { done <- m.InspectRuntime() }()
	select {
	case got := <-done:
		if got.Phase != "idle" || !got.Available {
			t.Fatalf("unexpected runtime: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("inspection waits for model activity")
	}
}

func TestInspectionRedactionMetadataBoundsAndVersions(t *testing.T) {
	m := &Manager{}
	m.publishInspection("working", []ChatMessage{
		{Role: "system", Content: strings.Repeat("人", 65000)},
		{Role: "user", Content: `{"kind":"qq_message","chat_type":"group","chat_id":9007199254740993,"message_id":7,"user_id":8,"origin":"live","text":"hello","token":"secret-value","nested":"{\"password\":\"nested-secret\"}"}`},
		{Role: "user", Content: []ContentPart{{Type: "text", Text: "photo"}, imagePart("data:image/png;base64,PRIVATE")}},
		{Role: "assistant", Content: "I guessed group:123"},
	}, nil)
	page, _ := m.InspectContext(ContextQuery{Source: "working", ChatType: "group", ChatID: "9007199254740993", Limit: 1})
	if page.Total != 1 || len(page.Entries) != 1 || page.Entries[0].MessageID != "7" {
		t.Fatalf("source metadata/filter lost: %+v", page)
	}
	data, _ := json.Marshal(page)
	if strings.Contains(string(data), "secret-value") || strings.Contains(string(data), "nested-secret") {
		t.Fatal("structured secrets leaked")
	}
	all, _ := m.InspectContext(ContextQuery{Limit: 40})
	if !all.Entries[0].Truncated || all.Entries[0].OriginalRunes != 65000 || len([]rune(all.Entries[0].Text)) != 1000 {
		t.Fatal("list truncation not explicit")
	}
	detail, err := m.InspectEntry("working", all.Version, 0)
	if err != nil || len([]rune(detail.Text)) != 64000 || !detail.Truncated {
		t.Fatalf("detail not bounded: %v", err)
	}
	data, _ = json.Marshal(all)
	if strings.Contains(string(data), "PRIVATE") || all.Entries[2].Images != 1 || all.Entries[3].ChatID != "" {
		t.Fatal("image leaked or prose metadata guessed")
	}
	m.publishInspection("working", nil, nil)
	if _, err := m.InspectEntry("working", all.Version, 0); err != ErrInspectionVersion {
		t.Fatalf("stale version not rejected: %v", err)
	}
}
