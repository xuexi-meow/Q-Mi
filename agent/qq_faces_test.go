package agent

import (
	"fmt"
	"strings"
	"testing"

	"Q-Mi/storage"
)

// napPaths 取假 NapCat 收到的全部路径（副本）。
func napPaths(n *p3napcat) []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.paths...)
}

// qqFaceStub 模拟 SnowLuma 的收藏表情扩展接口。
func qqFaceStub(t *testing.T) *p3napcat {
	t.Helper()
	return startP3NapCat(t, func(path string, body map[string]any) any {
		switch path {
		case "/download_file":
			return map[string]any{"path": "/tmp/qmi-face.png"}
		case "/add_custom_face":
			return map[string]any{"emoji_id": "abc123def456"}
		case "/fetch_custom_face_detail":
			return []map[string]any{
				{"emoji_id": "abc123def456", "resId": "res-1", "url": "http://cdn/a.png", "md5": "md5-1", "desc": ""},
				{"emoji_id": "ffff0000", "resId": "res-2", "url": "http://cdn/b.png", "md5": "md5-2", "desc": "猫猫点头"},
			}
		case "/set_custom_face_desc":
			return map[string]any{}
		case "/send_custom_face":
			return map[string]any{"message_id": 42}
		case "/send_group_msg", "/send_private_msg":
			return map[string]any{"message_id": 41}
		}
		return map[string]any{}
	})
}

func TestSaveStickerAddsToQQFavorites(t *testing.T) {
	s := openTestStore(t)
	srv := newPNGServer(t)
	images := newImageStoreWithClient(t.TempDir(), s, srv.Client())
	putRecordFull(t, s, &storage.MessageRecord{
		MessageID: 20, Direction: "recv", ChatType: "group", ChatID: 888,
		UserID: 10001, SenderName: "群友A", RawMessage: "[CQ:image]",
		Segments: []byte(`[{"type":"image","data":{"url":"` + srv.URL + `/a.png","summary":"猫猫"}}]`),
		Time:     1762400000,
	})
	nap := qqFaceStub(t)
	tools, _ := testToolSetForViewImages(t, s, images)

	out := objOf(t, runToolOK(t, tools, "save_sticker", `{"message_id":20,"note":"猫猫点头"}`))
	if out["qq_saved"] != true || out["qq_emoji_id"] != "abc123def456" {
		t.Fatalf("收藏回执 = %s", out)
	}
	id := int64(out["sticker_id"].(float64))
	st, err := s.GetSticker(id)
	if err != nil || st == nil {
		t.Fatalf("GetSticker: %v %v", st, err)
	}
	if st.QQEmojiID != "abc123def456" || st.QQResID != "res-1" || st.QQMD5 != "md5-1" {
		t.Fatalf("QQ 收藏字段未落库: %+v", st)
	}
	if st.QQDesc != "猫猫点头" {
		t.Fatalf("备注未同步: %q", st.QQDesc)
	}
	want := []string{"/download_file", "/add_custom_face", "/fetch_custom_face_detail", "/set_custom_face_desc"}
	if nap.count() != len(want) {
		t.Fatalf("调用链 = %v", napPaths(nap))
	}
	for i, p := range want {
		if nap.path(i) != p {
			t.Fatalf("第 %d 步 = %s, want %s", i, nap.path(i), p)
		}
	}
	if body := nap.body(1); body["file"] != "/tmp/qmi-face.png" {
		t.Fatalf("add_custom_face file = %v", body["file"])
	}
	if body := nap.body(3); body["desc"] != "猫猫点头" || body["emoji_id"] != "abc123def456" {
		t.Fatalf("set_custom_face_desc body = %v", body)
	}
}

func TestSaveStickerCanSkipQQ(t *testing.T) {
	s := openTestStore(t)
	srv := newPNGServer(t)
	images := newImageStoreWithClient(t.TempDir(), s, srv.Client())
	putSegmentsRecord(t, s, 21, `[{"type":"image","data":{"url":"`+srv.URL+`/a.png"}}]`)
	nap := qqFaceStub(t)
	tools, _ := testToolSetForViewImages(t, s, images)

	out := objOf(t, runToolOK(t, tools, "save_sticker", `{"message_id":21,"qq":false}`))
	if out["qq_saved"] != false {
		t.Fatalf("qq=false 不应加入收藏: %s", out)
	}
	if nap.count() != 0 {
		t.Fatalf("qq=false 不应调用协议端: %v", napPaths(nap))
	}
}

func TestSendMessageRoutesSingleQQStickerToCustomFace(t *testing.T) {
	s := openTestStore(t)
	id, err := s.AddSticker(storage.Sticker{
		Kind: "image", URL: "http://cdn/x.png", Summary: "猫猫", QQEmojiID: "abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	nap := qqFaceStub(t)
	tools, _, _ := testToolSet(t, s)

	out := objOf(t, runToolOK(t, tools, "send_message",
		fmt.Sprintf(`{"chat_type":"group","chat_id":888,"segments":[{"type":"sticker","data":{"id":%d}}]}`, id)))
	if out["as"] != "qq_custom_face" || out["sent"] != true {
		t.Fatalf("应以收藏表情发送: %s", out)
	}
	if nap.count() != 1 || nap.path(0) != "/send_custom_face" {
		t.Fatalf("调用链 = %v", napPaths(nap))
	}
	body := nap.body(0)
	if body["emoji_id"] != "abc123" || body["group_id"] != float64(888) {
		t.Fatalf("send_custom_face body = %v", body)
	}
}

func TestSendMessageRejectsMixedQQSticker(t *testing.T) {
	s := openTestStore(t)
	id, err := s.AddSticker(storage.Sticker{
		Kind: "image", URL: "http://cdn/x.png", QQEmojiID: "abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	nap := qqFaceStub(t)
	tools, _, _ := testToolSet(t, s)

	err = runToolErr(t, tools, "send_message",
		fmt.Sprintf(`{"chat_type":"group","chat_id":888,"segments":[{"type":"text","data":{"text":"看"}},{"type":"sticker","data":{"id":%d}}]}`, id))
	if !strings.Contains(err.Error(), "只能单独") {
		t.Fatalf("应提示只能单独发送: %v", err)
	}
	if nap.count() != 0 {
		t.Fatalf("不应发出任何请求: %v", napPaths(nap))
	}
}

func TestSendQQFaceWithText(t *testing.T) {
	nap := qqFaceStub(t)
	tools, _, _ := testToolSet(t, nil)

	out := objOf(t, runToolOK(t, tools, "send_qq_face",
		`{"chat_type":"group","chat_id":888,"emoji_id":"abc123","text":"笑死"}`))
	if out["text_sent"] != true || out["as"] != "qq_custom_face" {
		t.Fatalf("回执 = %s", out)
	}
	if got := napPaths(nap); len(got) != 2 ||
		got[0] != "/send_group_msg" || got[1] != "/send_custom_face" {
		t.Fatalf("调用链 = %v", got)
	}
}

func TestListQQFacesFiltersByDesc(t *testing.T) {
	qqFaceStub(t)
	tools, _, _ := testToolSet(t, nil)

	all := objOf(t, runToolOK(t, tools, "list_qq_faces", `{}`))
	if all["total"] != float64(2) {
		t.Fatalf("应列出 2 条: %s", all)
	}
	filtered := objOf(t, runToolOK(t, tools, "list_qq_faces", `{"query":"猫猫"}`))
	if filtered["total"] != float64(1) {
		t.Fatalf("过滤结果 = %s", filtered)
	}
}

func TestSyncQQFacesImportsOnce(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.AddSticker(storage.Sticker{Kind: "image", URL: "http://x", QQEmojiID: "ffff0000"}); err != nil {
		t.Fatal(err)
	}
	qqFaceStub(t)
	tools, _, _ := testToolSet(t, s)

	out := objOf(t, runToolOK(t, tools, "sync_qq_faces", `{}`))
	if out["imported"] != float64(1) || out["skipped"] != float64(1) {
		t.Fatalf("首次导入 = %s", out)
	}
	list, err := s.ListStickers()
	if err != nil {
		t.Fatal(err)
	}
	var imported *storage.Sticker
	for i := range list {
		if list[i].QQEmojiID == "abc123def456" {
			imported = &list[i]
		}
	}
	if imported == nil || imported.Note != "" || imported.Summary != "" || imported.QQMD5 != "md5-1" {
		t.Fatalf("导入记录 = %+v", imported)
	}

	again := objOf(t, runToolOK(t, tools, "sync_qq_faces", `{}`))
	if again["imported"] != float64(0) || again["skipped"] != float64(2) {
		t.Fatalf("二次导入应全部跳过: %s", again)
	}
}

func TestUpdateStickerSyncsQQDesc(t *testing.T) {
	s := openTestStore(t)
	id, err := s.AddSticker(storage.Sticker{
		Kind: "image", URL: "http://x", QQEmojiID: "abc123", QQResID: "res-1", QQMD5: "md5-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	nap := qqFaceStub(t)
	tools, _, _ := testToolSet(t, s)

	out := objOf(t, runToolOK(t, tools, "update_sticker",
		fmt.Sprintf(`{"sticker_id":%d,"note":"新备注"}`, id)))
	if out["qq_desc_synced"] != true {
		t.Fatalf("应同步 QQ 备注: %s", out)
	}
	if nap.count() != 1 || nap.path(0) != "/set_custom_face_desc" {
		t.Fatalf("调用链 = %v", napPaths(nap))
	}
	if body := nap.body(0); body["desc"] != "新备注" || body["res_id"] != "res-1" {
		t.Fatalf("body = %v", body)
	}
}

func TestUpdateStickerPushesLegacyLocalToQQ(t *testing.T) {
	s := openTestStore(t)
	id, err := s.AddSticker(storage.Sticker{Kind: "image", URL: "http://cdn/old.png", Summary: "旧图"})
	if err != nil {
		t.Fatal(err)
	}
	nap := qqFaceStub(t)
	tools, _, _ := testToolSet(t, s)

	out := objOf(t, runToolOK(t, tools, "update_sticker",
		fmt.Sprintf(`{"sticker_id":%d,"qq":true}`, id)))
	if out["qq_saved"] != true || out["qq_emoji_id"] != "abc123def456" {
		t.Fatalf("补录回执 = %s", out)
	}
	st, _ := s.GetSticker(id)
	if st.QQEmojiID != "abc123def456" || st.QQResID != "res-1" || st.QQMD5 != "md5-1" {
		t.Fatalf("补录字段 = %+v", st)
	}
	paths := napPaths(nap)
	want := []string{"/download_file", "/add_custom_face", "/fetch_custom_face_detail"}
	if len(paths) < len(want) {
		t.Fatalf("调用链 = %v", paths)
	}
	for i, p := range want {
		if paths[i] != p {
			t.Fatalf("第 %d 步 = %s, want %s", i, paths[i], p)
		}
	}
}
