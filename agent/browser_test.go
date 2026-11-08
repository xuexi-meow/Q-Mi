package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testBrowserCtx 带真实浏览器的 ToolContext（本机无 Chrome/Edge 时跳过）。
func testBrowserCtx(t *testing.T) *ToolContext {
	t.Helper()
	if testing.Short() {
		t.Skip("跳过真实浏览器")
	}
	if findBrowserExecutable("") == "" {
		t.Skip("本机未找到 Chrome/Edge，跳过浏览器测试")
	}
	bm := newBrowserManager("", t.TempDir())
	t.Cleanup(func() {
		bm.Close()
		time.Sleep(500 * time.Millisecond) // 等 Crashpad 释放 profile 文件
	})
	return &ToolContext{
		Browser:   bm,
		Workspace: t.TempDir(),
		Turn:      &Turn{ChatType: "group", ChatID: 888, SelfID: 999},
	}
}

// testBrowserPage 交互测试页：链接/按钮（点击改变文本）/输入框。
const testBrowserPage = `<!DOCTYPE html>
<html><head><title>测试页</title></head><body>
<h1>测试页</h1>
<p id="status">初始状态</p>
<a href="#sec">跳转锚点</a>
<input id="box" type="text" placeholder="输入点什么">
<button id="btn" onclick="document.getElementById('status').textContent='按钮已点击 ' + document.getElementById('box').value">点我</button>
</body></html>`

// TestBrowserNavigateReadClickType 全链路：navigate → read_page → type →
// click → 验证页面状态变化；screenshot 附图。
func TestBrowserNavigateReadClickType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testBrowserPage))
	}))
	t.Cleanup(srv.Close)

	ctx := testBrowserCtx(t)
	tools := buildTools(ctx)

	out := runToolOK(t, tools, "browser_navigate", `{"url":"`+srv.URL+`"}`)
	if !strings.Contains(out, "测试页") {
		t.Errorf("navigate 应返回标题: %s", out)
	}

	page := runToolOK(t, tools, "browser_read_page", `{}`)
	if !strings.Contains(page, "初始状态") || !strings.Contains(page, `"idx":`) {
		t.Fatalf("read_page 缺正文或元素索引: %s", page)
	}

	// 找到输入框 idx 并输入
	boxIdx := elementIdx(t, page, "input")
	runToolOK(t, tools, "browser_type", `{"idx":`+itoa(boxIdx)+`,"text":"你好"}`)
	// 找到按钮 idx 并点击
	btnIdx := elementIdx(t, page, `"tag":"button"`)
	runToolOK(t, tools, "browser_click", `{"idx":`+itoa(btnIdx)+`}`)

	page2 := runToolOK(t, tools, "browser_read_page", `{}`)
	if !strings.Contains(page2, "按钮已点击 你好") {
		t.Errorf("点击+输入未生效: %s", page2)
	}

	// 截图附图
	shot := runToolOK(t, tools, "browser_screenshot", `{}`)
	if !strings.Contains(shot, `"attached":true`) {
		t.Errorf("截图回执不符: %s", shot)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(ctx.Turn.pendingImages) > 0 && strings.HasPrefix(ctx.Turn.pendingImages[0], "data:image/png;base64,") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("截图应进入 pendingImages")
}

// elementIdx 从 read_page 输出里找指定元素的下标。
func elementIdx(t *testing.T, page, marker string) int {
	t.Helper()
	i := strings.Index(page, marker)
	if i < 0 {
		t.Fatalf("页面元素缺 %s: %s", marker, page)
	}
	// 从 marker 附近向前找最近的 "idx":N
	seg := page[maxInt(0, i-120):i]
	idx := -1
	for j := len(seg) - 1; j >= 0; j-- {
		if strings.HasPrefix(seg[j:], `"idx":`) {
			rest := seg[j+6:]
			n := 0
			for _, c := range rest {
				if c < '0' || c > '9' {
					break
				}
				n = n*10 + int(c-'0')
			}
			if n > 0 || strings.HasPrefix(rest, "0") {
				idx = n
			}
			break
		}
	}
	if idx < 0 {
		t.Fatalf("未找到 idx: %s", seg)
	}
	return idx
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestBrowserManagerNilSafe ctx.Browser 为 nil 时工具报错不 panic。
func TestBrowserManagerNilSafe(t *testing.T) {
	tools := buildTools(&ToolContext{Workspace: t.TempDir()})
	if err := runToolErr(t, tools, "browser_navigate", `{"url":"https://example.com"}`); !strings.Contains(err.Error(), "浏览器不可用") {
		t.Errorf("nil browser 应报错: %v", err)
	}
	_ = context.Background()
}
