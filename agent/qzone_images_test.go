package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"Q-Mi/utils/config"
)

func TestQzoneImagesBrowseKeepsLinksWithoutDownloading(t *testing.T) {
	srv, hits := servePNG(t, "image/png")
	startP3NapCat(t, func(string, map[string]any) any {
		return map[string]any{
			"feeds": []any{
				map[string]any{"uin": 10001, "nickname": "friend", "time": 1700000000,
					"tid": "t1", "content": "photo", "images": []string{srv.URL + "/a.png", srv.URL + "/b.png"}},
				map[string]any{"uin": 10002, "nickname": "friend2", "time": 1700000000,
					"tid": "t2", "content": "unknown images", "images": nil},
			},
			"has_more": false, "next_cursor": nil,
		}
	})
	tools, turn := testToolSetForViewImages(t, nil, newImageStore(t.TempDir(), nil))
	out := runToolOK(t, tools, "browse_qzone_feeds", `{}`)
	feeds := objOf(t, out)["feeds"].([]any)
	first := feeds[0].(map[string]any)
	if got := first["images"]; !reflect.DeepEqual(got, []any{srv.URL + "/a.png", srv.URL + "/b.png"}) {
		t.Fatalf("browse discarded usable image links: %#v", first)
	}
	if first["image_count"] != float64(2) {
		t.Fatalf("wrong image count: %#v", first)
	}
	if unknown := feeds[1].(map[string]any); unknown["image_count"] != nil {
		t.Fatalf("unknown images must not be reported as zero: %#v", unknown)
	}
	if hits.Load() != 0 || len(turn.pendingImages) != 0 {
		t.Fatal("browsing must not download or attach images without an explicit view")
	}
	args, _ := json.Marshal(map[string]any{"urls": []string{srv.URL + "/b.png"}})
	runToolOK(t, tools, "view_qzone_images", string(args))
	if hits.Load() != 1 || len(turn.pendingImages) != 1 || turn.pendingImages[0] != pngDataURL {
		t.Fatal("selected feed image did not reach the attachment queue")
	}
}

func TestQzoneImagesReadPreservesOptionalLinks(t *testing.T) {
	startP3NapCat(t, func(_ string, args map[string]any) any {
		out := map[string]any{"tid": args["tid"], "content": "post", "comment_num": 0, "comments": []any{}}
		switch args["tid"] {
		case "with-images":
			out["images"] = []string{"https://image.example/a.jpg"}
		case "empty-images":
			out["images"] = []string{}
		}
		return out
	})
	tools, _, _ := testToolSet(t, nil)
	for _, tc := range []struct {
		tid   string
		links any
		count any
	}{
		{"with-images", []any{"https://image.example/a.jpg"}, float64(1)},
		{"empty-images", []any{}, float64(0)},
		{"unknown-images", nil, nil},
	} {
		t.Run(tc.tid, func(t *testing.T) {
			out := objOf(t, runToolOK(t, tools, "read_qzone_post", `{"tid":"`+tc.tid+`"}`))
			if !reflect.DeepEqual(out["images"], tc.links) || out["image_count"] != tc.count {
				t.Fatalf("read lost image links or invented an image count: %#v", out)
			}
		})
	}
}

func TestQzoneImagesViewReportsFailuresWithoutAttachingURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dead":
			http.Error(w, "expired", http.StatusForbidden)
		case "/not-image":
			_, _ = w.Write([]byte("not an image"))
		default:
			_, _ = w.Write(pngBytes)
		}
	}))
	t.Cleanup(srv.Close)
	tools, turn := testToolSetForViewImages(t, nil, newImageStore(t.TempDir(), nil))
	args, _ := json.Marshal(map[string]any{"urls": []string{srv.URL + "/dead", srv.URL + "/ok", srv.URL + "/not-image"}})
	text := runToolOK(t, tools, "view_qzone_images", string(args))
	out := objOf(t, text)
	if out["total"] != float64(3) || out["attached"] != float64(1) {
		t.Fatalf("wrong attachment status: %s", text)
	}
	if !reflect.DeepEqual(out["attached_urls"], []any{srv.URL + "/ok"}) {
		t.Fatalf("cannot associate attached image with its source: %s", text)
	}
	skipped, ok := out["skipped"].([]any)
	if !ok || len(skipped) != 2 {
		t.Fatalf("missing failure details: %s", text)
	}
	for i, path := range []string{"/dead", "/not-image"} {
		row := skipped[i].(map[string]any)
		if row["url"] != srv.URL+path || row["reason"] == nil || row["reason"] == "" {
			t.Fatalf("unidentified failed image: %#v", row)
		}
	}
	if len(turn.pendingImages) != 1 || turn.pendingImages[0] != pngDataURL {
		t.Fatalf("failed URL was attached as if it were an image: %#v", turn.pendingImages)
	}
	if strings.Contains(text, "base64,") {
		t.Fatal("image bytes must not be embedded in the textual tool result")
	}
	turn.pendingImages = nil
	args, _ = json.Marshal(map[string]any{"urls": []string{srv.URL + "/dead"}})
	out = objOf(t, runToolOK(t, tools, "view_qzone_images", string(args)))
	if out["attached"] != float64(0) || len(turn.pendingImages) != 0 {
		t.Fatalf("all-failed view claimed to attach images: %#v", out)
	}
}

func TestQzoneImagesViewValidatesBeforeDownloading(t *testing.T) {
	srv, hits := servePNG(t, "image/png")
	tools, turn := testToolSetForViewImages(t, nil, newImageStore(t.TempDir(), nil))
	tooMany := make([]string, 6)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("%s/%d.png", srv.URL, i)
	}
	manyArgs, _ := json.Marshal(map[string]any{"urls": tooMany})
	for _, args := range []string{
		`{}`, `{"urls":[]}`, `{"urls":null}`, `{"urls":"wrong"}`, `{invalid`,
		`{"urls":[""]}`, `{"urls":["file:///secret.png"]}`, `{"urls":["data:image/png;base64,AAAA"]}`,
		`{"urls":["https://"]}`, `{"urls":["https://user:password@example.com/a.png"]}`,
		`{"urls":["` + srv.URL + `/ok.png","file:///secret.png"]}`,
		string(manyArgs),
	} {
		t.Run(args, func(t *testing.T) {
			runToolErr(t, tools, "view_qzone_images", args)
		})
	}
	if hits.Load() != 0 || len(turn.pendingImages) != 0 {
		t.Fatal("invalid request caused a partial download or attachment")
	}
	for _, ctx := range []*ToolContext{{}, {Turn: turn}, {Images: newImageStore(t.TempDir(), nil)}} {
		runToolErr(t, buildTools(ctx), "view_qzone_images", `{"urls":["`+srv.URL+`/ok.png"]}`)
	}
}

func TestQzoneImagesViewLimitsEachCallAndReusesCache(t *testing.T) {
	srv, hits := servePNG(t, "image/png")
	tools, turn := testToolSetForViewImages(t, nil, newImageStore(t.TempDir(), nil))
	urls := make([]string, 5)
	for i := range urls {
		urls[i] = fmt.Sprintf("%s/%d.png", srv.URL, i)
	}
	args, _ := json.Marshal(map[string]any{"urls": urls})
	for i := 0; i < 2; i++ {
		turn.pendingImages = nil
		out := objOf(t, runToolOK(t, tools, "view_qzone_images", string(args)))
		if out["attached"] != float64(5) || len(turn.pendingImages) != 5 {
			t.Fatalf("five selected images did not attach: %#v", out)
		}
	}
	if hits.Load() != 5 {
		t.Fatalf("view bypassed the shared image cache: %d downloads", hits.Load())
	}
}

func TestQzoneImagesReachNextVisionRequest(t *testing.T) {
	srv, _ := servePNG(t, "image/png")
	args, _ := json.Marshal(map[string]any{"urls": []string{srv.URL + "/a.png"}})
	cfg := testCfg(time.Millisecond)
	cfg.Models = map[string]config.ModelTier{
		"chat": {Candidates: []config.ModelCandidate{
			{Name: "text", Model: "text-model", Capabilities: []string{"tools"}},
			{Name: "vision", Model: "vision-model", Capabilities: []string{"tools", "vision"}},
		}},
	}
	m, f, napcat := newLoopManager(t, cfg, &stubBuilder{}, nil, []string{
		toolCallResp("view", "view_qzone_images", string(args)),
		toolCallResp("done", "finish", "{}"),
	})
	turn := &Turn{SelfID: 999, ChatType: "group", ChatID: 888, Now: time.Now()}
	m.handleTurn(turn)
	if f.count() != 2 {
		t.Fatalf("view did not continue to the next model request: %d", f.count())
	}
	if f.reqs[0].Model != "text-model" || f.reqs[1].Model != "vision-model" {
		t.Fatalf("image view failed to route to vision: %s -> %s", f.reqs[0].Model, f.reqs[1].Model)
	}
	images := 0
	for _, message := range f.reqs[1].Messages {
		if message.Role == "tool" && strings.Contains(contentText(message), "base64,") {
			t.Fatal("image bytes leaked into the tool text")
		}
		parts, _ := message.Content.([]any)
		for _, part := range parts {
			p, ok := part.(map[string]any)
			if !ok || p["type"] != "image_url" {
				continue
			}
			images++
			if message.Role != "user" || p["image_url"].(map[string]any)["url"] != pngDataURL {
				t.Fatalf("image bytes not delivered in a multimodal user message: %#v", p)
			}
		}
	}
	if images != 1 || len(turn.pendingImages) != 0 || napcat.count() != 0 {
		t.Fatalf("attachment delivery/cleanup failed or sent to QQ: images=%d pending=%d QQ=%d",
			images, len(turn.pendingImages), napcat.count())
	}
}
