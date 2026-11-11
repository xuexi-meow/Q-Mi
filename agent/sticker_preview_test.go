package agent

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"Q-Mi/storage"
)

const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

func newPNGServer(t *testing.T) *httptest.Server {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSaveStickerCachesPreviewAndNote(t *testing.T) {
	s := openTestStore(t)
	srv := newPNGServer(t)
	images := newImageStoreWithClient(t.TempDir(), s, srv.Client())
	putSegmentsRecord(t, s, 10, `[{"type":"image","data":{"url":"`+srv.URL+`/a.png","summary":"猫猫"}}]`)
	tools, turn := testToolSetForViewImages(t, s, images)

	out := objOf(t, runToolOK(t, tools, "save_sticker", `{"message_id":10,"note":"打招呼用"}`))
	if out["has_preview"] != true {
		t.Fatalf("应缓存预览: %s", out)
	}
	if out["note"] != "打招呼用" || out["summary"] != "猫猫" {
		t.Fatalf("回执字段不对: %s", out)
	}
	if len(turn.pendingImages) != 0 {
		t.Fatalf("收藏不应附图，pending=%d", len(turn.pendingImages))
	}
	id := int64(out["sticker_id"].(float64))
	st, err := s.GetSticker(id)
	if err != nil || st == nil {
		t.Fatalf("GetSticker: %v %v", st, err)
	}
	if st.Note != "打招呼用" || st.PreviewSHA == "" || st.PreviewMime != "image/png" {
		t.Fatalf("落库字段 = %+v", st)
	}
	if path := stickerPreviewPath(images, st.PreviewSHA); path == "" {
		t.Fatal("预览文件不存在")
	}

	list := runToolOK(t, tools, "list_stickers", `{}`)
	if !strings.Contains(list, `"note":"打招呼用"`) || !strings.Contains(list, `"has_preview":true`) {
		t.Fatalf("list_stickers 缺少备注/预览标记: %s", list)
	}
}

func TestViewStickerAttachesPreview(t *testing.T) {
	s := openTestStore(t)
	srv := newPNGServer(t)
	images := newImageStoreWithClient(t.TempDir(), s, srv.Client())
	putSegmentsRecord(t, s, 11, `[{"type":"image","data":{"url":"`+srv.URL+`/a.png","summary":"猫猫"}}]`)
	tools, _ := testToolSetForViewImages(t, s, images)
	saved := objOf(t, runToolOK(t, tools, "save_sticker", `{"message_id":11,"note":"打招呼"}`))
	id := int64(saved["sticker_id"].(float64))

	viewTools, turn := testToolSetForViewImages(t, s, images)
	out := objOf(t, runToolOK(t, viewTools, "view_sticker", fmt.Sprintf(`{"sticker_id":%d}`, id)))
	if out["attached"] != float64(1) {
		t.Fatalf("attached = %v, want 1 (%s)", out["attached"], out)
	}
	if len(turn.pendingImages) != 1 || !strings.HasPrefix(turn.pendingImages[0], "data:image/png;base64,") {
		t.Fatalf("应附图到下一轮，pending=%v", turn.pendingImages)
	}
}

func TestViewStickerReportsMissingPreview(t *testing.T) {
	s := openTestStore(t)
	images := newImageStoreWithClient(t.TempDir(), s, http.DefaultClient)
	id, err := s.AddSticker(storage.Sticker{Kind: "face", FaceID: "28", Summary: "憨笑"})
	if err != nil {
		t.Fatal(err)
	}
	tools, turn := testToolSetForViewImages(t, s, images)
	out := objOf(t, runToolOK(t, tools, "view_sticker", fmt.Sprintf(`{"sticker_id":%d}`, id)))
	if out["attached"] != float64(0) {
		t.Fatalf("face 不应附图: %s", out)
	}
	missing, _ := out["missing"].([]any)
	if len(missing) != 1 {
		t.Fatalf("missing = %v", out["missing"])
	}
	if len(turn.pendingImages) != 0 {
		t.Fatal("无预览时不应附图")
	}
}

func TestUpdateStickerNoteAndName(t *testing.T) {
	s := openTestStore(t)
	tools, _, _ := testToolSet(t, s)
	putSegmentsRecord(t, s, 12, `[{"type":"mface","data":{"emoji_id":"12345","emoji_package_id":"9","key":"k","summary":"[动画表情]"}}]`)
	saved := objOf(t, runToolOK(t, tools, "save_sticker", `{"message_id":12}`))
	id := int64(saved["sticker_id"].(float64))

	out := objOf(t, runToolOK(t, tools, "update_sticker",
		fmt.Sprintf(`{"sticker_id":%d,"note":"猫猫点头","name":"点头猫"}`, id)))
	if out["note"] != "猫猫点头" || out["name"] != "点头猫" {
		t.Fatalf("更新回执 = %s", out)
	}
	if out["display"] != "猫猫点头" {
		t.Fatalf("display 应优先备注: %s", out)
	}
	st, _ := s.GetSticker(id)
	if st.Note != "猫猫点头" || st.Summary != "点头猫" {
		t.Fatalf("落库 = %+v", st)
	}

	runToolErr(t, tools, "update_sticker", fmt.Sprintf(`{"sticker_id":%d}`, id))
	runToolErr(t, tools, "update_sticker", `{"sticker_id":999,"note":"x"}`)
}

func TestMfacePreviewDerivation(t *testing.T) {
	md5ID := "9f56b8605ec9739c019df342e4a8694a"
	want := "https://gxh.vip.qq.com/club/item/parcel/item/9f/" + md5ID + "/300x300.png"
	if got := mfaceCDNURL(md5ID); got != want {
		t.Fatalf("mfaceCDNURL = %q, want %q", got, want)
	}
	if got := mfaceCDNURL("12345"); got != "" {
		t.Fatalf("非 md5 的 emoji_id 不应推导: %q", got)
	}
	cands := stickerPreviewCandidates("mface", "http://cdn/a.png", md5ID)
	if len(cands) != 3 || cands[0] != "http://cdn/a.png" || cands[1] != want ||
		!strings.HasSuffix(cands[2], "300x300.gif") {
		t.Fatalf("候选顺序 = %v", cands)
	}
	if got := stickerPreviewCandidates("face", "http://x", ""); len(got) != 0 {
		t.Fatalf("face 不应有预览候选: %v", got)
	}
}

func TestEnsureStickerPreviewRetriesWhenMissing(t *testing.T) {
	s := openTestStore(t)
	srv := newPNGServer(t)
	images := newImageStoreWithClient(t.TempDir(), s, srv.Client())
	id, err := s.AddSticker(storage.Sticker{Kind: "image", URL: srv.URL + "/x.png", Summary: "旧图"})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := s.GetSticker(id)
	turn := &Turn{ChatType: "group", ChatID: 888, SelfID: 999}
	du := ensureStickerPreview(&ToolContext{Store: s, Images: images, Turn: turn}, st)
	if !strings.HasPrefix(du, "data:image/png;base64,") {
		t.Fatalf("重试后应拿到预览: %q", du)
	}
	reloaded, _ := s.GetSticker(id)
	if reloaded.PreviewSHA == "" {
		t.Fatal("重试成功应写回 preview_sha")
	}
}
