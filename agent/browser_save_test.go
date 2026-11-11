package agent

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestResolveScreenshotPath(t *testing.T) {
	root := t.TempDir()
	ctx := &ToolContext{Workspace: root}
	now := time.Date(2026, 11, 9, 23, 5, 6, 789_000_000, time.Local)
	wantDefault := "browser-20261109-230506.789.png"

	cases := []struct{ name, in, want string }{
		{"relative with ext", "shots/a.png", filepath.Join(root, "shots", "a.png")},
		{"relative no ext", "shots/a", filepath.Join(root, "shots", "a.png")},
		{"other ext normalized", "shots/a.JPG", filepath.Join(root, "shots", "a.png")},
		{"trailing slash", "shots/", filepath.Join(root, "shots", wantDefault)},
		{"empty uses workspace", "", filepath.Join(root, wantDefault)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveScreenshotPath(ctx, tc.in, now)
			if err != nil {
				t.Fatalf("resolve(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("resolve(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	existing := filepath.Join(root, "existing")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveScreenshotPath(ctx, "existing", now); err != nil ||
		got != filepath.Join(existing, wantDefault) {
		t.Fatalf("existing dir = %q/%v", got, err)
	}

	outside := filepath.Join(t.TempDir(), "x.png")
	got, err := resolveScreenshotPath(ctx, outside, now)
	var oe errOutside
	if !errors.As(err, &oe) {
		t.Fatalf("outside path should return errOutside, got %v", err)
	}
	if got != outside {
		t.Fatalf("outside abs = %q, want %q", got, outside)
	}
}

func TestBrowserSaveScreenshotWritesPNG(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(testBrowserPage))
	}))
	t.Cleanup(srv.Close)

	ctx := testBrowserCtx(t)
	tools := buildTools(ctx)
	runToolOK(t, tools, "browser_navigate", `{"url":"`+srv.URL+`"}`)

	out := objOf(t, runToolOK(t, tools, "browser_save_screenshot", `{"path":"shots/shot.png"}`))
	if out["saved"] != true {
		t.Fatalf("保存回执 = %s", out)
	}
	if out["path"] != "shots/shot.png" {
		t.Fatalf("path = %v, want shots/shot.png", out["path"])
	}
	if np, _ := out["napcat_path"].(string); np == "" {
		t.Fatalf("缺少 napcat_path: %s", out)
	}
	if len(ctx.Turn.pendingImages) != 0 {
		t.Fatalf("存盘不应附图，pendingImages=%d", len(ctx.Turn.pendingImages))
	}

	file := filepath.Join(ctx.Workspace, "shots", "shot.png")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("截图未落盘: %v", err)
	}
	if !bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("文件不是 PNG: %x", data[:min(8, len(data))])
	}

	again := objOf(t, runToolOK(t, tools, "browser_save_screenshot", `{"path":"shots/shot.png"}`))
	if again["overwritten"] != true {
		t.Fatalf("同路径应标记覆盖: %s", again)
	}
}

func TestBrowserSaveScreenshotOutsideNeedsApproval(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(testBrowserPage))
	}))
	t.Cleanup(srv.Close)

	ctx := testBrowserCtx(t)
	tools := buildTools(ctx)
	runToolOK(t, tools, "browser_navigate", `{"url":"`+srv.URL+`"}`)

	outside := filepath.Join(t.TempDir(), "outside.png")
	args := fmt.Sprintf(`{"path":%s}`, strconv.Quote(outside))

	err := runToolErr(t, tools, "browser_save_screenshot", args)
	var need approvalNeeded
	if !errors.As(err, &need) {
		t.Fatalf("工作区外应要求审批，got %v", err)
	}
	if _, statErr := os.Stat(outside); !os.IsNotExist(statErr) {
		t.Fatal("审批前不应落盘")
	}

	// 管理员同意后放行。
	ctx.Approved = true
	runToolOK(t, tools, "browser_save_screenshot", args)
	if _, statErr := os.Stat(outside); statErr != nil {
		t.Fatalf("审批后应写入: %v", statErr)
	}
}
