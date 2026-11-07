// stickers_test.go 表情收藏与发送：
// save_sticker 三类段收藏、list/delete 工具、send_message 新增 mface/sticker
// 段（假 NapCat 断言发出去的段类型与字段）、collect_qq_sticker 两步链
// （download_file → add_custom_face，两步失败都报 error）、guide.md 表情
// 对照表与 qq_faces.go 内置表同步。
package agent

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"Q-Mi/storage"
	"Q-Mi/utils/config"
	"Q-Mi/utils/request"
)

// putSegmentsRecord 写入一条带 segments 的消息记录（save_sticker 的数据源）。
func putSegmentsRecord(t *testing.T, s *storage.Store, id int64, segs string) {
	t.Helper()
	putRecordFull(t, s, &storage.MessageRecord{
		MessageID: id, Direction: "recv", ChatType: "group", ChatID: 888,
		UserID: 10001, SenderName: "群友A",
		RawMessage: "[CQ:fake]", Segments: []byte(segs), Time: 1762400000,
	})
}

// sticker 种子构造（storage 包的测试 helper 不可跨包，这里复制一份）。
func faceSticker() storage.Sticker {
	return storage.Sticker{Kind: "face", FaceID: "28", Summary: "憨笑"}
}

func mfaceSticker() storage.Sticker {
	return storage.Sticker{Kind: "mface", EmojiID: "12345", EmojiPackageID: "9", Key: "abcKEY", Summary: "小鸡啄米"}
}

func imageSticker() storage.Sticker {
	return storage.Sticker{Kind: "image", URL: "https://example.com/a.jpg", Summary: "看看"}
}

// TestSaveStickerFromMessage 从消息段收藏：face（数字 id → 表情名做 summary）、
// mface（emoji_id/package_id 兼容数字形态）、image（url + summary）。
func TestSaveStickerFromMessage(t *testing.T) {
	s := openTestStore(t)
	tools, _, _ := testToolSet(t, s)
	putSegmentsRecord(t, s, 1, `[
		{"type":"text","data":{"text":"看这个"}},
		{"type":"face","data":{"id":28,"text":"[表情]"}},
		{"type":"mface","data":{"emoji_id":"12345","emoji_package_id":9,"key":"abcKEY","summary":"小鸡啄米"}},
		{"type":"image","data":{"file":"a.image","url":"https://example.com/a.jpg","summary":"可爱"}},
		{"type":"mface","data":{"emoji_id":67890,"emoji_package_id":"9","key":"k2"}}
	]`)

	var res struct {
		StickerID int64  `json:"sticker_id"`
		Kind      string `json:"kind"`
		Summary   string `json:"summary"`
	}

	// face 段（seg_index=1）：存 face_id，summary 取内置表情名
	out := runToolOK(t, tools, "save_sticker", `{"message_id":1,"seg_index":1}`)
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("输出不是 JSON: %v (%s)", err, out)
	}
	if res.StickerID != 1 || res.Kind != "face" || res.Summary != "憨笑" {
		t.Errorf("face 收藏结果 = %+v, want id=1 kind=face summary=憨笑", res)
	}
	st, err := s.GetSticker(1)
	if err != nil || st == nil {
		t.Fatalf("GetSticker(1): %v, %v", st, err)
	}
	if st.Kind != "face" || st.FaceID != "28" || st.Summary != "憨笑" {
		t.Errorf("face 落库 = %+v, want face_id=28", st)
	}

	// mface 段：emoji_id/package_id/key/summary 全存
	out = runToolOK(t, tools, "save_sticker", `{"message_id":1,"seg_index":2}`)
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.StickerID != 2 || res.Kind != "mface" || res.Summary != "小鸡啄米" {
		t.Errorf("mface 收藏结果 = %s (%v)", out, err)
	}
	if st, _ := s.GetSticker(2); st == nil || st.EmojiID != "12345" || st.EmojiPackageID != "9" || st.Key != "abcKEY" {
		t.Errorf("mface 落库 = %+v", st)
	}

	// image 段：存 url 与 summary
	out = runToolOK(t, tools, "save_sticker", `{"message_id":1,"seg_index":3}`)
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.StickerID != 3 || res.Kind != "image" || res.Summary != "可爱" {
		t.Errorf("image 收藏结果 = %s (%v)", out, err)
	}
	if st, _ := s.GetSticker(3); st == nil || st.URL != "https://example.com/a.jpg" {
		t.Errorf("image 落库 = %+v", st)
	}

	// mface 数字形态的 emoji_id 也能存
	out = runToolOK(t, tools, "save_sticker", `{"message_id":1,"seg_index":4}`)
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.StickerID != 4 {
		t.Errorf("mface 数字 id 收藏结果 = %s (%v)", out, err)
	}
	if st, _ := s.GetSticker(4); st == nil || st.EmojiID != "67890" || st.Summary != "" {
		t.Errorf("mface 数字 id 落库 = %+v", st)
	}

	// seg_index 缺省 0：text 段不可收藏，落到行为与显式传参一致
	runToolErr(t, tools, "save_sticker", `{"message_id":1}`)
}

// TestSaveStickerErrors 参数与数据错误路径：记忆库未启用、消息不存在、
// 段下标越界、非可收藏段类型、image 无 url。
func TestSaveStickerErrors(t *testing.T) {
	s := openTestStore(t)
	tools, _, _ := testToolSet(t, s)

	err := runToolErr(t, tools, "save_sticker", `{"message_id":99}`)
	if !strings.Contains(err.Error(), "没有找到消息") {
		t.Errorf("消息不存在应报「没有找到消息」, got %v", err)
	}

	putSegmentsRecord(t, s, 1, `[{"type":"text","data":{"text":"纯文本"}}]`)
	for _, bad := range []string{
		`{"message_id":1,"seg_index":5}`,  // 越界
		`{"message_id":1,"seg_index":-1}`, // 负数
		`{"message_id":1,"seg_index":0}`,  // text 段
	} {
		runToolErr(t, tools, "save_sticker", bad)
	}
	putSegmentsRecord(t, s, 2, `[{"type":"image","data":{"file":"a.image"}}]`)
	err = runToolErr(t, tools, "save_sticker", `{"message_id":2}`)
	if !strings.Contains(err.Error(), "url") {
		t.Errorf("image 无 url 应报错并提示 url, got %v", err)
	}

	// 记忆库未启用
	noStore, _, _ := testToolSet(t, nil)
	err = runToolErr(t, noStore, "save_sticker", `{"message_id":1}`)
	if !strings.Contains(err.Error(), "记忆库未启用") {
		t.Errorf("无 store 应报「记忆库未启用」, got %v", err)
	}
}

// TestListAndDeleteStickerTools list 返回紧凑 JSON 数组（id/kind/summary）；
// delete 幂等语义在工具层体现：不存在的 id 报 error。
func TestListAndDeleteStickerTools(t *testing.T) {
	s := openTestStore(t)
	tools, _, _ := testToolSet(t, s)

	out := runToolOK(t, tools, "list_stickers", ``)
	if out != "[]" {
		t.Errorf("空收藏 list 输出 = %q, want []", out)
	}

	_, _ = s.AddSticker(faceSticker()) // id1：只凑条数，delete 只针对 id2
	id2, _ := s.AddSticker(mfaceSticker())
	out = runToolOK(t, tools, "list_stickers", ``)
	if strings.ContainsAny(out, "\n\t") {
		t.Errorf("list 输出应为紧凑 JSON: %q", out)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("list 输出不是 JSON 数组: %v (%s)", err, out)
	}
	if len(rows) != 2 || rows[0]["kind"] != "face" || rows[1]["kind"] != "mface" {
		t.Errorf("list 行不符: %v", rows)
	}
	if rows[1]["summary"] != "小鸡啄米" || rows[0]["summary"] != "憨笑" {
		t.Errorf("list summary 不符: %v", rows)
	}

	out = runToolOK(t, tools, "delete_sticker", `{"sticker_id":`+strconv.FormatInt(id2, 10)+`}`)
	if out != `{"deleted":true}` {
		t.Errorf("delete 输出 = %q, want {\"deleted\":true}", out)
	}
	if st, _ := s.GetSticker(id2); st != nil {
		t.Errorf("删除后仍能查到: %+v", st)
	}

	err := runToolErr(t, tools, "delete_sticker", `{"sticker_id":`+strconv.FormatInt(id2, 10)+`}`)
	if !strings.Contains(err.Error(), "没有找到表情") {
		t.Errorf("删除不存在的表情应报「没有找到表情」, got %v", err)
	}
}

// TestSendMessageMFaceSegment mface 段白名单透传：四元组构造 apimsg.Message；
// key 必填、emoji_id/package_id 兼容数字形态。
func TestSendMessageMFaceSegment(t *testing.T) {
	tools, _, _ := testToolSet(t, nil)
	napcat := startFakeNapCat(t)

	runToolOK(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"segments":[`+
		`{"type":"mface","data":{"emoji_id":"12345","emoji_package_id":9,"key":"abcKEY","summary":"小鸡啄米"}}]}`)
	if napcat.count() != 1 {
		t.Fatalf("NapCat 应收到 1 次请求, got %v", napcat.paths)
	}
	segs := napcatSegments(t, napcat, 0)
	if len(segs) != 1 || segs[0]["type"] != "mface" {
		t.Fatalf("应发出 1 段 mface, got %v", segs)
	}
	d, _ := segs[0]["data"].(map[string]any)
	want := map[string]any{"emoji_id": "12345", "emoji_package_id": "9", "key": "abcKEY", "summary": "小鸡啄米"}
	if !reflect.DeepEqual(d, want) {
		t.Errorf("mface data = %v, want %v", d, want)
	}

	// summary 可省略
	runToolOK(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"segments":[`+
		`{"type":"mface","data":{"emoji_id":"e2","emoji_package_id":"3","key":"kk"}}]}`)
	segs = napcatSegments(t, napcat, 1)
	d, _ = segs[0]["data"].(map[string]any)
	if _, ok := d["summary"]; ok {
		t.Errorf("未传 summary 不应出现在 data: %v", d)
	}

	for _, bad := range []string{
		`{"chat_type":"group","chat_id":888,"segments":[{"type":"mface","data":{"emoji_id":"e","emoji_package_id":"1"}}]}`,            // 缺 key
		`{"chat_type":"group","chat_id":888,"segments":[{"type":"mface","data":{"emoji_package_id":"1","key":"k"}}]}`,                 // 缺 emoji_id
		`{"chat_type":"group","chat_id":888,"segments":[{"type":"mface","data":{"emoji_id":"e","key":"k"}}]}`,                         // 缺 package_id
		`{"chat_type":"group","chat_id":888,"segments":[{"type":"mface","data":{"emoji_id":true,"emoji_package_id":"1","key":"k"}}]}`, // 类型非法
	} {
		runToolErr(t, tools, "send_message", bad)
	}
	if napcat.count() != 2 {
		t.Errorf("非法参数不应发消息, NapCat 收到 %d 次", napcat.count())
	}
}

// TestSendMessageStickerSegment sticker 段查表转换：face→face、mface→mface
// （key 必需）、image→image(file=url)；查不到 / 缺 key / 记忆库未启用报错。
func TestSendMessageStickerSegment(t *testing.T) {
	s := openTestStore(t)
	tools, _, _ := testToolSet(t, s)
	napcat := startFakeNapCat(t)

	idFace, _ := s.AddSticker(faceSticker())
	idMface, _ := s.AddSticker(mfaceSticker())
	idImage, _ := s.AddSticker(imageSticker())
	noKey := mfaceSticker()
	noKey.Key = ""
	idNoKey, _ := s.AddSticker(noKey)

	// face → face 段
	runToolOK(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"segments":[{"type":"sticker","data":{"id":`+strconv.FormatInt(idFace, 10)+`}}]}`)
	segs := napcatSegments(t, napcat, 0)
	if len(segs) != 1 || segs[0]["type"] != "face" {
		t.Fatalf("sticker(face) 应转为 face 段, got %v", segs)
	}
	if d, _ := segs[0]["data"].(map[string]any); d["id"] != "28" {
		t.Errorf("face 段 data = %v, want id=28", d)
	}

	// mface → mface 段（带 key 与 summary）
	runToolOK(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"segments":[{"type":"sticker","data":{"id":`+strconv.FormatInt(idMface, 10)+`}}]}`)
	segs = napcatSegments(t, napcat, 1)
	if len(segs) != 1 || segs[0]["type"] != "mface" {
		t.Fatalf("sticker(mface) 应转为 mface 段, got %v", segs)
	}
	d, _ := segs[0]["data"].(map[string]any)
	want := map[string]any{"emoji_id": "12345", "emoji_package_id": "9", "key": "abcKEY", "summary": "小鸡啄米"}
	if !reflect.DeepEqual(d, want) {
		t.Errorf("mface 段 data = %v, want %v", d, want)
	}

	// image → image 段（file=url）
	runToolOK(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"segments":[{"type":"sticker","data":{"id":`+strconv.FormatInt(idImage, 10)+`}}]}`)
	segs = napcatSegments(t, napcat, 2)
	if len(segs) != 1 || segs[0]["type"] != "image" {
		t.Fatalf("sticker(image) 应转为 image 段, got %v", segs)
	}
	if d, _ := segs[0]["data"].(map[string]any); d["file"] != "https://example.com/a.jpg" || d["summary"] != "看看" {
		t.Errorf("image 段 data = %v, want file=url summary=看看", d)
	}

	// 错误路径：查不到 / mface 缺 key / 缺 id
	err := runToolErr(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"segments":[{"type":"sticker","data":{"id":999}}]}`)
	if !strings.Contains(err.Error(), "没有找到表情") {
		t.Errorf("查不到应报「没有找到表情」, got %v", err)
	}
	err = runToolErr(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"segments":[{"type":"sticker","data":{"id":`+strconv.FormatInt(idNoKey, 10)+`}}]}`)
	if !strings.Contains(err.Error(), "key") {
		t.Errorf("mface 缺 key 应报错提示 key, got %v", err)
	}
	runToolErr(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"segments":[{"type":"sticker","data":{}}]}`)
	if napcat.count() != 3 {
		t.Errorf("错误路径不应发消息, NapCat 收到 %d 次", napcat.count())
	}

	// 记忆库未启用时 sticker 段报错
	noStore, _, _ := testToolSet(t, nil)
	err = runToolErr(t, noStore, "send_message", `{"chat_type":"group","chat_id":888,"segments":[{"type":"sticker","data":{"id":1}}]}`)
	if !strings.Contains(err.Error(), "记忆库未启用") {
		t.Errorf("无 store 应报「记忆库未启用」, got %v", err)
	}
}

// startFailP3NapCat 可按 path 注入 retcode≠0 失败的假 NapCat（两步链失败用）。
func startFailP3NapCat(t *testing.T, fail map[string]bool) *p3napcat {
	t.Helper()
	rec := &p3napcat{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.mu.Lock()
		rec.paths = append(rec.paths, r.URL.Path)
		rec.bodies = append(rec.bodies, body)
		rec.mu.Unlock()
		payload := map[string]any{"status": "ok", "retcode": 0, "data": map[string]any{}}
		if fail[r.URL.Path] {
			payload = map[string]any{"status": "failed", "retcode": 1400, "data": nil, "wording": "操作失败"}
		} else if r.URL.Path == "/download_file" {
			payload["data"] = map[string]any{"path": "C:/NapCat/downloads/a.png"}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)
	if err := request.Init(&config.Napcat{HTTP: config.NapcatConn{
		Enable: true, Host: srv.Listener.Addr().(*net.TCPAddr).IP.String(),
		Port: srv.Listener.Addr().(*net.TCPAddr).Port,
	}}); err != nil {
		t.Fatal(err)
	}
	return rec
}

// TestCollectQQSticker 两步链：download_file 拿本地路径 → add_custom_face 入
// QQ 收藏；两步各自失败都报 error 且不继续。
func TestCollectQQSticker(t *testing.T) {
	nap := startP3NapCat(t, func(path string, body map[string]any) any {
		switch path {
		case "/download_file":
			return map[string]any{"path": "C:/NapCat/downloads/a.png"}
		case "/add_custom_face":
			return map[string]any{"res_id": "R1"}
		}
		return map[string]any{}
	})
	tools, _, _ := testToolSet(t, nil)

	out := runToolOK(t, tools, "collect_qq_sticker", `{"url":"https://example.com/a.png"}`)
	if !strings.Contains(out, "C:/NapCat/downloads/a.png") {
		t.Errorf("输出应含下载路径: %s", out)
	}
	if nap.count() != 2 || nap.path(0) != "/download_file" || nap.path(1) != "/add_custom_face" {
		t.Fatalf("应按序调用两步, got %v", nap.paths)
	}
	if str(t, nap.body(0), "url") != "https://example.com/a.png" {
		t.Errorf("download_file 应透传 url: %v", nap.body(0))
	}
	if str(t, nap.body(1), "file") != "C:/NapCat/downloads/a.png" {
		t.Errorf("add_custom_face.file 应为下载返回的本地路径: %v", nap.body(1))
	}

	// 第一步失败：报 error，只调用 1 次
	napFailDL := startFailP3NapCat(t, map[string]bool{"/download_file": true})
	err := runToolErr(t, tools, "collect_qq_sticker", `{"url":"https://example.com/a.png"}`)
	if napFailDL.count() != 1 {
		t.Errorf("download 失败不应继续 add, calls = %d", napFailDL.count())
	}
	if !strings.Contains(err.Error(), "下载") {
		t.Errorf("download 失败报错应提示下载: %v", err)
	}

	// 第二步失败：报 error，两步都调用
	napFailAdd := startFailP3NapCat(t, map[string]bool{"/add_custom_face": true})
	err = runToolErr(t, tools, "collect_qq_sticker", `{"url":"https://example.com/a.png"}`)
	if napFailAdd.count() != 2 {
		t.Errorf("add 失败前应完成两步调用, calls = %d", napFailAdd.count())
	}
	if !strings.Contains(err.Error(), "收藏") {
		t.Errorf("add 失败报错应提示收藏: %v", err)
	}

	// download 结果缺路径 → 报错
	napEmpty := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	err = runToolErr(t, tools, "collect_qq_sticker", `{"url":"https://example.com/a.png"}`)
	if napEmpty.count() != 1 || !strings.Contains(err.Error(), "路径") {
		t.Errorf("download 无路径应报错, calls = %d, err = %v", napEmpty.count(), err)
	}

	// 缺 url 参数
	runToolErr(t, tools, "collect_qq_sticker", `{}`)
}

// TestGuideFaceTableSyncsQQFaces guide.md 的自带表情对照表必须与
// qq_faces.go 的 QQFaceNames 完全一致，条数在 30~60 之间。
func TestGuideFaceTableSyncsQQFaces(t *testing.T) {
	data, err := os.ReadFile("guide.md")
	if err != nil {
		t.Fatalf("读取 guide.md: %v", err)
	}
	fromGuide := map[int]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "---") {
			continue
		}
		cols := strings.Split(strings.Trim(line, "| "), "|")
		if len(cols) != 2 {
			continue
		}
		id, err := strconv.Atoi(strings.TrimSpace(cols[0]))
		if err != nil {
			continue
		}
		fromGuide[id] = strings.TrimSpace(cols[1])
	}
	if len(fromGuide) < 30 || len(fromGuide) > 60 {
		t.Fatalf("guide 对照表条数 = %d, want 30~60", len(fromGuide))
	}
	if !reflect.DeepEqual(fromGuide, QQFaceNames) {
		for id, name := range fromGuide {
			if QQFaceNames[id] != name {
				t.Errorf("guide 表 id=%d 名称 %q, 内置表 %q", id, name, QQFaceNames[id])
			}
		}
		for id, name := range QQFaceNames {
			if fromGuide[id] != name {
				t.Errorf("内置表 id=%d 名称 %q 未出现在 guide 表", id, name)
			}
		}
	}
}
