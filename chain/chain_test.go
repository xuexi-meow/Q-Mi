package chain

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	apimsg "Q-Mi/api/message"
)

// toRaw 把链序列化成 OneBot 上报格式,便于逐段断言
func toRaw(t *testing.T, c Chain) []map[string]any {
	t.Helper()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("序列化链失败: %v", err)
	}
	var segs []map[string]any
	if err := json.Unmarshal(data, &segs); err != nil {
		t.Fatalf("反序列化链失败: %v", err)
	}
	return segs
}

func segField(t *testing.T, seg map[string]any, key string) any {
	t.Helper()
	data, ok := seg["data"].(map[string]any)
	if !ok {
		t.Fatalf("段 %v 缺少 data 对象", seg["type"])
	}
	v, ok := data[key]
	if !ok {
		t.Fatalf("段 %s 缺少字段 %s", seg["type"], key)
	}
	return v
}

func TestChainCompose(t *testing.T) {
	c := New().Reply("10001").At("123456").Text(" 签到成功！").Face("5")
	segs := toRaw(t, c)

	wantTypes := []string{"reply", "at", "text", "face"}
	if len(segs) != len(wantTypes) {
		t.Fatalf("段数 = %d, 期望 %d", len(segs), len(wantTypes))
	}
	for i, want := range wantTypes {
		if segs[i]["type"] != want {
			t.Errorf("第 %d 段 type = %v, 期望 %s", i, segs[i]["type"], want)
		}
	}

	if id := segField(t, segs[0], "id"); id != "10001" {
		t.Errorf("reply.id = %v, 期望 10001", id)
	}
	if qq := segField(t, segs[1], "qq"); qq != "123456" {
		t.Errorf("at.qq = %v, 期望 123456", qq)
	}
	if text := segField(t, segs[2], "text"); text != " 签到成功！" {
		t.Errorf("text = %v, 期望 签到成功！", text)
	}
	if face := segField(t, segs[3], "id"); face != "5" {
		t.Errorf("face.id = %v, 期望 5", face)
	}
}

func TestAtAll(t *testing.T) {
	segs := toRaw(t, New().AtAll())
	if len(segs) != 1 || segs[0]["type"] != "at" {
		t.Fatalf("AtAll 应产生单个 at 段, 得到 %v", segs)
	}
	if qq := segField(t, segs[0], "qq"); qq != "all" {
		t.Errorf("at.qq = %v, 期望 all", qq)
	}
}

func TestSimpleSegments(t *testing.T) {
	segs := toRaw(t, New().
		Dice("1").
		RPS("2").
		Poke("group", "789").
		Json(map[string]any{"k": "v"}).
		Forward("fwd-1").
		MFace("emoji-1", "pkg-1"))

	wantTypes := []string{"dice", "rps", "poke", "json", "forward", "mface"}
	if len(segs) != len(wantTypes) {
		t.Fatalf("段数 = %d, 期望 %d", len(segs), len(wantTypes))
	}
	for i, want := range wantTypes {
		if segs[i]["type"] != want {
			t.Errorf("第 %d 段 type = %v, 期望 %s", i, segs[i]["type"], want)
		}
	}

	if v := segField(t, segs[2], "type"); v != "group" {
		t.Errorf("poke.type = %v, 期望 group", v)
	}
	if v := segField(t, segs[3], "data"); v == nil {
		t.Error("json.data 不应为空")
	}
}

func TestMediaURLPassthrough(t *testing.T) {
	c := New().
		Image("https://example.com/a.png").
		Image("base64://QUJD").
		Image("file:///C:/pics/a.png").
		Record("https://example.com/a.mp3").
		Video("https://example.com/a.mp4").
		File("https://example.com/a.zip")

	// 文件值在序列化后断言
	segs := toRaw(t, c)
	wantFiles := []string{
		"https://example.com/a.png",
		"base64://QUJD",
		"file:///C:/pics/a.png",
		"https://example.com/a.mp3",
		"https://example.com/a.mp4",
		"https://example.com/a.zip",
	}
	for i, want := range wantFiles {
		if got := segField(t, segs[i], "file"); got != want {
			t.Errorf("第 %d 段 file = %v, 期望 %s", i, got, want)
		}
	}
}

func TestLocalFileToBase64(t *testing.T) {
	content := []byte("fake png bytes \x00\x01\x02")
	path := filepath.Join(t.TempDir(), "sign.png")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	segs := toRaw(t, New().Image(path).Record(path))
	want := "base64://" + base64.StdEncoding.EncodeToString(content)
	for i, segType := range []string{"image", "record"} {
		if segs[i]["type"] != segType {
			t.Errorf("第 %d 段 type = %v, 期望 %s", i, segs[i]["type"], segType)
		}
		if got := segField(t, segs[i], "file"); got != want {
			t.Errorf("第 %d 段 file 未正确转 base64: %v", i, got)
		}
	}
}

func TestMissingFilePassthrough(t *testing.T) {
	// 读不到的路径不应中断链,原样透传交给 NapCat 报错
	want := "E:/definitely/not/exist.png"
	segs := toRaw(t, New().Image(want))
	if got := segField(t, segs[0], "file"); got != want {
		t.Errorf("file = %v, 期望原样透传 %s", got, want)
	}
}

func TestRawEscapeHatch(t *testing.T) {
	c := New().
		Text("点歌:").
		Raw(apimsg.Music("qq", apimsg.StringPtr("123"), nil, nil, nil, nil, nil))

	segs := toRaw(t, c)
	if len(segs) != 2 {
		t.Fatalf("段数 = %d, 期望 2", len(segs))
	}
	if segs[1]["type"] != "music" {
		t.Errorf("第 2 段 type = %v, 期望 music", segs[1]["type"])
	}
	if v := segField(t, segs[1], "type"); v != "qq" {
		t.Errorf("music.type = %v, 期望 qq", v)
	}
}

func TestNewAggregates(t *testing.T) {
	c := New(apimsg.Text("a"), apimsg.At("1")).Text("b")
	if len(c) != 3 {
		t.Fatalf("链长度 = %d, 期望 3", len(c))
	}
	segs := toRaw(t, c)
	if segs[0]["type"] != "text" || segs[1]["type"] != "at" || segs[2]["type"] != "text" {
		t.Errorf("聚合顺序错误: %v", segs)
	}
}
