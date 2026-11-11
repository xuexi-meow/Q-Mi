package agent

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"Q-Mi/event"
	evmsg "Q-Mi/event/message"
	"Q-Mi/utils/config"
)

// TestEventImageRefsExtracts wiring 从事件消息段提取图片段：Idx 对全部
// image 段计数（含缺 url 的段，与 storage 映射锚一致）；Parsed 未预解析时
// 懒解析（TypeHandler），Data 已填充时直接取。
func TestEventImageRefsExtracts(t *testing.T) {
	msgs := []*evmsg.Message{
		{Type: "text", Parsed: json.RawMessage(`{"text":"看图"}`)},
		{Type: "image", Parsed: json.RawMessage(`{"file":"a.image","url":"https://multimedia.nt.qq.com.cn/1","sub_type":0}`)},
		{Type: "image", Parsed: json.RawMessage(`{"file":"b.image"}`)},
		{Type: "image", Parsed: json.RawMessage(`{"file":"c.image","url":""}`)},
		{Type: "face", Parsed: json.RawMessage(`{"id":"1","url":"https://face.example/1"}`)},
	}
	got := eventImageRefs(msgs)
	want := []imageRef{{Idx: 0, URL: "https://multimedia.nt.qq.com.cn/1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("eventImageRefs = %v, want %v", got, want)
	}
}

// TestEventImageRefsPrefilledDataAndNil Data 已解析（值类型 Image）与 nil 输入。
func TestEventImageRefsPrefilledDataAndNil(t *testing.T) {
	u := "https://multimedia.nt.qq.com.cn/2"
	prefilled := []*evmsg.Message{{Type: "image", Data: evmsg.Image{Url: &u}}}
	got := eventImageRefs(prefilled)
	want := []imageRef{{Idx: 0, URL: u}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("eventImageRefs = %v, want %v", got, want)
	}
	if got := eventImageRefs(nil); got != nil {
		t.Errorf("nil 消息应返回 nil, got %v", got)
	}
}

func TestGroupEventPreservesAddressingThroughInbox(t *testing.T) {
	m := NewManager(&stubBuilder{}, nil, config.AgentConfig{Debounce: time.Hour}, nil, 999)
	m.inbox = &accountInbox{ready: make(chan struct{}, 1)}
	withHookManager(t, m)
	t.Cleanup(func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.inbox.timer != nil {
			m.inbox.timer.Stop()
		}
	})
	event.Dispatch([]byte(`{
		"post_type":"message","message_type":"group","message_id":80,
		"group_id":888,"user_id":111,"self_id":999,"time":100,
		"sender":{"user_id":111,"nickname":"A","role":"member"},
		"message":[{"type":"reply","data":{"id":"42"}},{"type":"at","data":{"qq":"222"}},{"type":"text","data":{"text":"你好香"}}]
	}`))
	event.Wait()
	turn := m.takePending()
	if turn == nil || len(turn.NewMessages) != 1 {
		t.Fatalf("event did not reach inbox: %+v", turn)
	}
	row := messageView(nil, 999, turn.NewMessages[0], "live")
	if row["mentions_self"] != false || row["addressing_known"] != true {
		t.Fatalf("@other must not be @self: %#v", row)
	}
	mentions, _ := row["mentions"].([]string)
	reply, _ := row["reply"].(map[string]any)
	if len(mentions) != 1 || mentions[0] != "222" || reply["message_id"] != "42" {
		t.Fatalf("adapter/inbox discarded addressing: %#v", row)
	}
}

func TestMalformedEventSegmentsDoNotConfirmNoMentions(t *testing.T) {
	for _, data := range []string{`{"qq":`, `null`, `{}`, `{"qq":{"id":999}}`} {
		t.Run(data, func(t *testing.T) {
			segments := eventSegments([]*evmsg.Message{
				{Type: "at", Parsed: json.RawMessage(data)},
				{Type: "text", Parsed: json.RawMessage(`{"text":"看看"}`)},
			})
			row := messageView(nil, 999, TurnMessage{
				ChatType: "group", ChatID: 888, SenderID: 111, Text: "看看",
				Segments: segments,
			}, "live")
			if row["addressing_known"] != false || row["mentions"] != nil {
				t.Fatalf("malformed @ must mean unknown, not confirmed no mentions: %#v", row)
			}
			if row["text"] != "看看" {
				t.Fatalf("fallback must preserve available text: %#v", row)
			}
		})
	}
}
