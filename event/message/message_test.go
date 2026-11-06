package message

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestTypeHandlerStringfFromJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		data any
		want string
	}{
		{"text", `{"type":"text","data":{"text":"你好，Q-Mi"}}`, Text{Text: "你好，Q-Mi"}, "你好，Q-Mi"},
		{"at", `{"type":"at","data":{"qq":"1234"}}`, At{QQ: "1234"}, "@1234"},
		{"reply", `{"type":"reply","data":{"id":"42"}}`, Reply{ID: "42"}, "回复消息[42]"},
		{"face", `{"type":"face","data":{"id":"66"}}`, Face{ID: "66"}, "表情[66]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var msg Message
			if err := json.Unmarshal([]byte(tc.raw), &msg); err != nil {
				t.Fatal(err)
			}
			got, err := msg.TypeHandler()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.data) || !reflect.DeepEqual(msg.Data, tc.data) {
				t.Fatalf("TypeHandler = %#v, Data = %#v, want value %#v", got, msg.Data, tc.data)
			}
			formatter, ok := msg.Data.(interface{ Stringf() string })
			if !ok {
				t.Fatalf("Data (%T) does not implement Stringf", msg.Data)
			}
			if got := formatter.Stringf(); got != tc.want {
				t.Errorf("Stringf = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTypeHandlerPreservesOtherSegmentValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want any
	}{
		{"mface", `{"type":"mface","data":{"emoji_id":"1","emoji_package_id":"2"}}`, MFace{EmojiID: "1", EmojiPackageID: "2"}},
		{"dice", `{"type":"dice","data":{"result":"6"}}`, Dice{Result: "6"}},
		{"rps", `{"type":"rps","data":{"result":"2"}}`, RPS{Result: "2"}},
		{"poke", `{"type":"poke","data":{"type":"1","id":"2"}}`, Poke{Type: "1", ID: "2"}},
		{"image", `{"type":"image","data":{"file":"image.png"}}`, Image{File: "image.png"}},
		{"record", `{"type":"record","data":{"file":"record.amr"}}`, Record{File: "record.amr"}},
		{"video", `{"type":"video","data":{"file":"video.mp4"}}`, Video{File: "video.mp4"}},
		{"file", `{"type":"file","data":{"file":"file.txt"}}`, File{File: "file.txt"}},
		{"json", `{"type":"json","data":{"data":{"key":"value"}}}`, Json{Data: map[string]any{"key": "value"}}},
		{"music", `{"type":"music","data":{"type":"qq"}}`, Music{Type: "qq"}},
		{"forward", `{"type":"forward","data":{"id":"42"}}`, Forward{ID: "42"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var msg Message
			if err := json.Unmarshal([]byte(tc.raw), &msg); err != nil {
				t.Fatal(err)
			}
			got, err := msg.TypeHandler()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) || !reflect.DeepEqual(msg.Data, tc.want) {
				t.Errorf("TypeHandler = %#v, Data = %#v, want value %#v", got, msg.Data, tc.want)
			}
		})
	}
}

func TestTypeHandlerUnknownPreservesRawData(t *testing.T) {
	const raw = `{"type":"future-segment","data":{"nested":[1,{"text":"unchanged"}],"extra":true}}`
	const data = `{"nested":[1,{"text":"unchanged"}],"extra":true}`
	var msg Message
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatal(err)
	}
	got, err := msg.TypeHandler()
	if err != nil || got != nil || msg.Data != nil {
		t.Fatalf("unknown TypeHandler = %#v, %v, Data = %#v", got, err, msg.Data)
	}
	if string(msg.Parsed) != data {
		t.Errorf("raw data = %s, want %s", msg.Parsed, data)
	}
	encoded, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != raw {
		t.Errorf("round trip = %s, want %s", encoded, raw)
	}
}

func TestTypeHandlerAcceptsRealImagePayload(t *testing.T) {
	// NapCat 真实上报:sub_type/emoji_package_id 为数字,file_size 为字符串
	const raw = `{"type":"image","data":{"summary":"","file":"E0F522ED.jpg","sub_type":0,"url":"https://example.invalid/x","file_size":"233621","emoji_id":123,"emoji_package_id":456}}`
	var msg Message
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatal(err)
	}
	got, err := msg.TypeHandler()
	if err != nil {
		t.Fatalf("真实图片段解析失败: %v", err)
	}
	img, ok := got.(Image)
	if !ok {
		t.Fatalf("类型 = %T, want Image", got)
	}
	if img.File != "E0F522ED.jpg" || img.SubType == nil || *img.SubType != "0" ||
		img.FileSize == nil || *img.FileSize != 233621 ||
		img.EmojiID == nil || *img.EmojiID != "123" ||
		img.EmojiPackageID == nil || *img.EmojiPackageID != "456" {
		t.Errorf("字段映射错误: %+v", img)
	}
}

func TestTypeHandlerMFaceNumericIDs(t *testing.T) {
	// mface 的 emoji_id/emoji_package_id 真实上报同样可能是数字
	var msg Message
	if err := json.Unmarshal([]byte(`{"type":"mface","data":{"emoji_id":1,"emoji_package_id":2}}`), &msg); err != nil {
		t.Fatal(err)
	}
	got, err := msg.TypeHandler()
	if err != nil {
		t.Fatalf("mface 数字 id 解析失败: %v", err)
	}
	mf, ok := got.(MFace)
	if !ok || mf.EmojiID != "1" || mf.EmojiPackageID != "2" {
		t.Fatalf("mface 字段映射错误: %#v", got)
	}
}

func TestTypeHandlerRejectsInvalidSegmentData(t *testing.T) {
	var msg Message
	if err := json.Unmarshal([]byte(`{"type":"text","data":{"text":123}}`), &msg); err != nil {
		t.Fatal(err)
	}
	got, err := msg.TypeHandler()
	if err == nil || got != nil || msg.Data != nil {
		t.Errorf("TypeHandler = %#v, %v, Data = %#v; want nil, error, nil", got, err, msg.Data)
	}
}
