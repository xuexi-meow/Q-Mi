package agent

import (
	"encoding/json"
	"reflect"
	"testing"

	evmsg "Q-Mi/event/message"
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
