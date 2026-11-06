package event

import (
	"encoding/json"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Q-Mi/event/message"
)

func TestResolveKey(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`{"post_type":"message","message_type":"group"}`, "message.group"},
		{`{"post_type":"message","message_type":"private"}`, "message.private"},
		{`{"post_type":"message_sent","message_type":"group"}`, "message_sent.group"},
		{`{"post_type":"notice","notice_type":"group_upload"}`, "notice.group_upload"},
		{`{"post_type":"notice","notice_type":"friend_add"}`, "notice.friend_add"},
		{`{"post_type":"notice","notice_type":"notify","sub_type":"poke"}`, "notice.notify.poke"},
		{`{"post_type":"notice","notice_type":"notify","sub_type":"group_name"}`, "notice.notify.group_name"},
		{`{"post_type":"request","request_type":"friend"}`, "request.friend"},
		{`{"post_type":"meta_event","meta_event_type":"heartbeat"}`, "meta.heartbeat"},
		{`{}`, ""},
		{`{"post_type":"unknown"}`, "unknown"},
	}
	for _, c := range cases {
		if got := resolveKey([]byte(c.raw)); got != c.want {
			t.Errorf("resolveKey(%s) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestDispatchConcurrent(t *testing.T) {
	var count int32
	RegisterTyped("test", func(e *struct {
		N int `json:"n"`
	}) {
		atomic.AddInt32(&count, 1)
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			Dispatch([]byte(`{"post_type":"test","n":` + strconv.Itoa(n) + `}`))
		}(i)
	}
	wg.Wait()

	// Dispatch 是异步的,等待所有 handler goroutine 处理完
	for i := 0; i < 500; i++ {
		if atomic.LoadInt32(&count) == 100 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&count); got != 100 {
		t.Errorf("count = %d, want 100", got)
	}
}

func TestDispatchUnregistered(t *testing.T) {
	// 未注册的事件不应 panic
	Dispatch([]byte(`{"post_type":"notice","notice_type":"never_registered"}`))
	Dispatch([]byte(`{"post_type":"message","message_type":"never_registered"}`))
	Dispatch([]byte(`not-json`))
}

func TestDispatchContinuesAfterHandlerPanic(t *testing.T) {
	key := t.Name() + "." + strconv.FormatInt(time.Now().UnixNano(), 10)
	done := make(chan struct{}, 1)
	Register(key, func(json.RawMessage) { panic("broken handler") })
	Register(key, func(json.RawMessage) { done <- struct{}{} })
	Dispatch([]byte(`{"post_type":"` + key + `"}`))
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("后续 handler 被前一个 handler 的 panic 跳过")
	}
}

func TestWaitDrainsHandlers(t *testing.T) {
	key := t.Name() + "." + strconv.FormatInt(time.Now().UnixNano(), 10)
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	finished := make(chan struct{})
	Register(key, func(json.RawMessage) {
		close(started)
		<-release
		close(finished)
	})
	Dispatch([]byte(`{"post_type":"` + key + `"}`))
	<-started
	waited := make(chan struct{})
	go func() {
		Wait()
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("事件尚未完成，Wait 已返回")
	case <-time.After(30 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("事件完成后 Wait 未返回")
	}
	select {
	case <-finished:
	default:
		t.Fatal("Wait 返回时仍有未完成事件")
	}
}

func TestMessageSentSegmentsParsed(t *testing.T) {
	key := t.Name() + "." + strconv.FormatInt(time.Now().UnixNano(), 10)
	got := make(chan any, 1)
	RegisterTyped(key, func(e *MessageSentEvent) {
		got <- e.Message[0].Data
	})
	Dispatch([]byte(`{"post_type":"` + key + `","message_type":"private","message":[{"type":"text","data":{"text":"hello"}}]}`))
	Wait()
	select {
	case value := <-got:
		text, ok := value.(message.Text)
		if !ok || text.Text != "hello" {
			t.Fatalf("发出消息段未完成类型解析: %#v", value)
		}
	default:
		t.Fatal("发出消息 handler 未调用")
	}
}
