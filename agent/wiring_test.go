package agent

import (
	"encoding/json"
	"reflect"
	"testing"

	evmsg "Q-Mi/event/message"
)

// TestEventImageURLsExtractsOnlyNonEmpty wiring 从事件消息段提取图片 URL：
// 只取 type=="image" 且 url 非空的段；Parsed 未预解析时懒解析（TypeHandler），
// Data 已填充时直接取。
func TestEventImageURLsExtractsOnlyNonEmpty(t *testing.T) {
	msgs := []*evmsg.Message{
		{Type: "text", Parsed: json.RawMessage(`{"text":"看图"}`)},
		{Type: "image", Parsed: json.RawMessage(`{"file":"a.image","url":"https://multimedia.nt.qq.com.cn/1","sub_type":0}`)},
		{Type: "image", Parsed: json.RawMessage(`{"file":"b.image"}`)},
		{Type: "image", Parsed: json.RawMessage(`{"file":"c.image","url":""}`)},
		{Type: "face", Parsed: json.RawMessage(`{"id":"1","url":"https://face.example/1"}`)},
	}
	got := eventImageURLs(msgs)
	want := []string{"https://multimedia.nt.qq.com.cn/1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("eventImageURLs = %v, want %v", got, want)
	}
}

// TestEventImageURLsPrefilledDataAndNil Data 已解析（值类型 Image）与 nil 输入。
func TestEventImageURLsPrefilledDataAndNil(t *testing.T) {
	u := "https://multimedia.nt.qq.com.cn/2"
	prefilled := []*evmsg.Message{{Type: "image", Data: evmsg.Image{Url: &u}}}
	got := eventImageURLs(prefilled)
	want := []string{"https://multimedia.nt.qq.com.cn/2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("eventImageURLs = %v, want %v", got, want)
	}
	if got := eventImageURLs(nil); got != nil {
		t.Errorf("nil 消息应返回 nil, got %v", got)
	}
}
