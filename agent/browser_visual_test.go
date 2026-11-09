package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

const visualBrowserFixture = `<!doctype html><title>Visual fixture</title>
<style>body{margin:0;height:1800px}canvas{position:absolute;left:20px;top:20px}
#edit{position:absolute;left:20px;top:250px;width:300px;height:80px;border:1px solid}
#scroll{position:absolute;left:400px;top:20px;width:200px;height:180px;overflow:auto}
#hover{position:absolute;left:20px;top:360px;width:150px;height:60px}
#result{position:absolute;left:20px;top:450px}</style>
<canvas id="canvas" width="300" height="200" tabindex="0"></canvas>
<div id="edit" contenteditable="true"></div>
<div id="scroll"><div style="height:1600px">Nested scroll</div></div>
<div id="hover" onmouseenter="this.textContent='hovered'">Hover here</div>
<p id="result"></p>
<button style="position:absolute;left:700px;top:20px"
 onclick="window.popup=window.open('/popup','_blank')">Popup</button>
<button style="position:absolute;left:700px;top:70px"
 onclick="document.querySelector('#result').textContent=prompt('Question','seed')">Prompt</button>
<button style="position:absolute;left:700px;top:120px"
 onclick="document.querySelector('#result').textContent=confirm('Confirm?')">Confirm</button>
<script>
const canvas=document.querySelector('#canvas'), g=canvas.getContext('2d');
g.fillStyle='#1269bb';g.fillRect(0,0,300,200);
window.events={down:0,up:0,drag:0,double:0,right:0,trusted:true};
canvas.onpointerdown=e=>{events.down++;events.trusted&&=e.isTrusted;canvas.focus()};
canvas.onpointerup=e=>{events.up++;events.trusted&&=e.isTrusted};
canvas.onpointermove=e=>{if(e.buttons===1)events.drag++};
canvas.onclick=e=>{g.fillStyle='#ee1122';g.fillRect(e.offsetX-5,e.offsetY-5,10,10)};
canvas.ondblclick=()=>events.double++;
canvas.oncontextmenu=e=>{e.preventDefault();events.right++};
</script>`

func visualBrowser(t *testing.T) (*ToolContext, []Tool, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/popup" || r.URL.Path == "/second" {
			_, _ = w.Write([]byte("<!doctype html><title>" + r.URL.Path + "</title><p>Other page</p>"))
			return
		}
		_, _ = w.Write([]byte(visualBrowserFixture))
	}))
	t.Cleanup(srv.Close)
	ctx := testBrowserCtx(t)
	tools := buildTools(ctx)
	runToolOK(t, tools, "browser_navigate", `{"url":"`+srv.URL+`"}`)
	return ctx, tools, srv.URL
}

func browserEval[T any](t *testing.T, ctx *ToolContext, expression string) T {
	t.Helper()
	var value T
	err := ctx.Browser.action(func(c context.Context) (err error) {
		value, err = chromedp.Run(c, chromedp.Evaluate[T](expression))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func browserShot(t *testing.T, tools []Tool) map[string]any {
	t.Helper()
	result := objOf(t, runToolOK(t, tools, "browser_screenshot", `{}`))
	if _, ok := result["tab_id"].(string); !ok {
		t.Fatalf("screenshot missing tab identity: %#v", result)
	}
	return result
}

func visualMouse(t *testing.T, tools []Tool, fields map[string]any) string {
	t.Helper()
	shot := browserShot(t, tools)
	fields["snapshot_id"], fields["tab_id"] = shot["snapshot_id"], shot["tab_id"]
	args, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return runToolOK(t, tools, "browser_mouse", string(args))
}

func shotImage(t *testing.T, ctx *ToolContext) (image.Image, string) {
	t.Helper()
	du := ctx.Turn.pendingImages[len(ctx.Turn.pendingImages)-1]
	parts := strings.SplitN(du, ",", 2)
	buf, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	img, format, err := image.Decode(bytes.NewReader(buf))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(du, "data:image/"+format+";base64,") {
		t.Fatalf("MIME does not match actual %s image", format)
	}
	return img, format
}

func TestBrowserVisualCanvasMouseAndPixels(t *testing.T) {
	ctx, tools, _ := visualBrowser(t)
	shot := browserShot(t, tools)
	if shot["snapshot_id"] == nil || shot["tab_id"] == nil ||
		shot["viewport_width"].(float64) <= 0 || shot["scroll_y"] != float64(0) {
		t.Fatalf("missing viewport metadata: %#v", shot)
	}
	before, _ := shotImage(t, ctx)
	if before.Bounds().Dx() != int(shot["image_width"].(float64)) ||
		before.Bounds().Dy() != int(shot["image_height"].(float64)) {
		t.Fatalf("wrong bitmap geometry: %#v", shot)
	}
	visualMouse(t, tools, map[string]any{"action": "click", "x": 75, "y": 75})
	browserShot(t, tools)
	after, _ := shotImage(t, ctx)
	if before.At(75, 75) == after.At(75, 75) {
		t.Fatal("canvas click did not visibly change its pixel")
	}
	for _, action := range []string{"double_click", "right_click"} {
		visualMouse(t, tools, map[string]any{"action": action, "x": 100, "y": 100})
	}
	visualMouse(t, tools, map[string]any{"action": "drag", "x": 80, "y": 80, "to_x": 220, "to_y": 160})
	ev := browserEval[map[string]any](t, ctx, "window.events")
	if ev["trusted"] != true || ev["double"] != float64(1) || ev["right"] != float64(1) ||
		ev["drag"].(float64) < 2 || ev["down"] != ev["up"] {
		t.Fatalf("real pointer sequences missing or left button held: %#v", ev)
	}
	visualMouse(t, tools, map[string]any{"action": "move", "x": 50, "y": 380})
	if browserEval[string](t, ctx, "document.querySelector('#hover').textContent") != "hovered" {
		t.Fatal("hover did not fire")
	}
	visualMouse(t, tools, map[string]any{"action": "wheel", "x": 450, "y": 60, "delta_y": 220})
	waitFor(t, 2*time.Second, func() bool {
		return browserEval[float64](t, ctx, "document.querySelector('#scroll').scrollTop") > 0
	}, "nested scroller should receive wheel")
	if browserEval[float64](t, ctx, "window.scrollY") != 0 {
		t.Fatal("wheel scrolled the window instead of targeted container")
	}
}

func TestBrowserVisualKeyboardAndSnapshotFreshness(t *testing.T) {
	ctx, tools, _ := visualBrowser(t)
	visualMouse(t, tools, map[string]any{"action": "click", "x": 60, "y": 270})
	runToolOK(t, tools, "browser_keyboard", `{"text":"hello"}`)
	runToolOK(t, tools, "browser_keyboard", `{"key":"CTRL+A"}`)
	count := len(ctx.Turn.pendingImages)
	runToolOK(t, tools, "browser_keyboard", `{"text":"你好 canvas"}`)
	if browserEval[string](t, ctx, "document.querySelector('#edit').textContent") != "你好 canvas" {
		t.Fatal("focused contenteditable or keyboard shortcut did not work")
	}
	if len(ctx.Turn.pendingImages) != count {
		t.Fatal("keyboard unexpectedly attached a screenshot")
	}
	old := browserShot(t, tools)
	args, _ := json.Marshal(map[string]any{"action": "click", "x": 75, "y": 75,
		"snapshot_id": old["snapshot_id"], "tab_id": old["tab_id"]})
	runToolOK(t, tools, "browser_mouse", string(args))
	runToolErr(t, tools, "browser_mouse", string(args))
	old = browserShot(t, tools)
	args, _ = json.Marshal(map[string]any{"action": "click", "x": 75, "y": 75,
		"snapshot_id": old["snapshot_id"], "tab_id": old["tab_id"]})
	runToolOK(t, tools, "browser_scroll", `{"pixels":200}`)
	runToolErr(t, tools, "browser_mouse", string(args))
	for _, tc := range []struct{ args, errorPart string }{
		{`{"action":"click","x":-1,"y":20}`, "坐标"},
		{`{"action":"click","x":99999,"y":20}`, "坐标"},
		{`{"action":"click","y":20}`, "缺少坐标"},
		{`{"action":"drag","x":10,"y":10}`, "终点"},
		{`{"action":"drag","x":10,"y":10,"to_x":99999,"to_y":20}`, "坐标"},
		{`{"action":"wheel","x":10,"y":10,"delta_y":999999}`, "滚轮"},
		{`{"action":"unknown","x":10,"y":10}`, "action"},
	} {
		shot := browserShot(t, tools)
		var fields map[string]any
		if err := json.Unmarshal([]byte(tc.args), &fields); err != nil {
			t.Fatal(err)
		}
		fields["snapshot_id"], fields["tab_id"] = shot["snapshot_id"], shot["tab_id"]
		args, _ := json.Marshal(fields)
		err := runToolErr(t, tools, "browser_mouse", string(args))
		if !strings.Contains(err.Error(), tc.errorPart) {
			t.Fatalf("wrong validation path: %v", err)
		}
	}
	for _, bad := range []string{`{}`, `{"key":"CTRL+UnknownKey"}`, `{"key":"A","text":"both"}`,
		`{"text":"` + strings.Repeat("x", 10001) + `"}`} {
		runToolErr(t, tools, "browser_keyboard", bad)
	}
}

func TestBrowserVisualKeyboardCase(t *testing.T) {
	ctx, tools, _ := visualBrowser(t)
	visualMouse(t, tools, map[string]any{"action": "click", "x": 60, "y": 270})
	for _, key := range []string{"A", "SHIFT+b", "c"} {
		args, _ := json.Marshal(map[string]string{"key": key})
		runToolOK(t, tools, "browser_keyboard", string(args))
	}
	if got := browserEval[string](t, ctx, "document.querySelector('#edit').textContent"); got != "ABc" {
		t.Fatalf("key case not preserved: %q", got)
	}
}

func TestBrowserVisualFullScreenshotMIMEAndLimits(t *testing.T) {
	ctx, tools, _ := visualBrowser(t)
	full := objOf(t, runToolOK(t, tools, "browser_screenshot", `{"full_page":true}`))
	fullImage, _ := shotImage(t, ctx)
	if float64(fullImage.Bounds().Dy()) <= full["viewport_height"].(float64) {
		t.Fatalf("full-page screenshot only captured viewport: %#v", full)
	}
	args, _ := json.Marshal(map[string]any{"action": "click", "x": 75, "y": 75,
		"snapshot_id": full["snapshot_id"], "tab_id": full["tab_id"]})
	runToolErr(t, tools, "browser_mouse", string(args))
	runToolErr(t, tools, "browser_screenshot", `{invalid`)
	browserEval[bool](t, ctx, "!!(document.body.style.height='100000px')")
	runToolErr(t, tools, "browser_screenshot", `{"full_page":true}`)
}

func TestBrowserTabsHistoryPopupAndDialogs(t *testing.T) {
	ctx, tools, url := visualBrowser(t)
	first := browserShot(t, tools)["tab_id"].(string)
	newTab := objOf(t, runToolOK(t, tools, "browser_tabs", `{"action":"new","url":"`+url+`/second"}`))
	second := newTab["tab_id"].(string)
	if second == first {
		t.Fatal("new tab reused the previous target")
	}
	runToolOK(t, tools, "browser_navigate", `{"url":"`+url+`/third"}`)
	runToolOK(t, tools, "browser_navigate", `{"action":"back"}`)
	if !strings.HasSuffix(browserEval[string](t, ctx, "location.href"), "/second") {
		t.Fatal("back did not restore history")
	}
	runToolOK(t, tools, "browser_navigate", `{"action":"forward"}`)
	if !strings.HasSuffix(browserEval[string](t, ctx, "location.href"), "/third") {
		t.Fatal("forward did not restore history")
	}
	runToolOK(t, tools, "browser_navigate", `{"action":"reload"}`)
	runToolOK(t, tools, "browser_tabs", `{"action":"switch","tab_id":"`+first+`"}`)
	page := runToolOK(t, tools, "browser_read_page", `{}`)
	runToolOK(t, tools, "browser_click", `{"idx":`+itoa(elementIdx(t, page, `"text":"Popup"`))+`}`)
	if !browserEval[bool](t, ctx, "!!window.popup") {
		t.Fatal("DOM click did not grant the user gesture required to open a popup")
	}
	waitFor(t, 2*time.Second, func() bool {
		result := objOf(t, runToolOK(t, tools, "browser_tabs", `{"action":"list"}`))
		return len(result["tabs"].([]any)) == 3
	}, "popup should appear in tab list")
	start := time.Now()
	_, err := findTool(t, tools, "browser_click").Run(json.RawMessage(`{"idx":` + itoa(elementIdx(t, page, `"text":"Prompt"`)) + `}`))
	if err == nil || !strings.Contains(err.Error(), "browser_dialog") || time.Since(start) > 5*time.Second {
		t.Fatalf("dialog should return promptly for explicit handling (%v): %v", time.Since(start), err)
	}
	dialog := objOf(t, runToolOK(t, tools, "browser_dialog", `{"action":"get"}`))
	if dialog["type"] != "prompt" || dialog["message"] != "Question" {
		t.Fatalf("pending prompt metadata missing: %#v", dialog)
	}
	runToolOK(t, tools, "browser_dialog", `{"action":"accept","text":"answer"}`)
	waitFor(t, time.Second, func() bool {
		return browserEval[string](t, ctx, "document.querySelector('#result').textContent") == "answer"
	}, "prompt answer should reach page")
	runToolErr(t, tools, "browser_click", `{"idx":`+itoa(elementIdx(t, page, `"text":"Confirm"`))+`}`)
	runToolOK(t, tools, "browser_dialog", `{"action":"dismiss"}`)
	waitFor(t, time.Second, func() bool {
		return browserEval[string](t, ctx, "document.querySelector('#result').textContent") == "false"
	}, "confirm should be dismissed")
	runToolOK(t, tools, "browser_tabs", `{"action":"close","tab_id":"`+second+`"}`)
	runToolErr(t, tools, "browser_tabs", `{"action":"switch","tab_id":"`+second+`"}`)
	old := browserShot(t, tools)
	blank := objOf(t, runToolOK(t, tools, "browser_tabs", `{"action":"new"}`))["tab_id"].(string)
	runToolOK(t, tools, "browser_tabs", `{"action":"switch","tab_id":"`+first+`"}`)
	args, _ := json.Marshal(map[string]any{"action": "click", "x": 75, "y": 75,
		"snapshot_id": old["snapshot_id"], "tab_id": first})
	runToolErr(t, tools, "browser_mouse", string(args))
	runToolOK(t, tools, "browser_tabs", `{"action":"close","tab_id":"`+first+`"}`)
	runToolOK(t, tools, "browser_tabs", `{"action":"switch","tab_id":"`+blank+`"}`)
	runToolOK(t, tools, "browser_navigate", `{"url":"`+url+`/second"}`)
	if !strings.HasSuffix(browserEval[string](t, ctx, "location.href"), "/second") {
		t.Fatal("closing initial tab killed the remaining browser")
	}
}

func TestBrowserVisualDPIAndChangedGeometry(t *testing.T) {
	ctx, tools, _ := visualBrowser(t)
	old := browserShot(t, tools)
	err := ctx.Browser.action(func(c context.Context) error {
		_, err := cdp.Call(c, chromedp.FromContext(c).Target, emulation.SetDeviceMetricsOverride,
			emulation.SetDeviceMetricsOverrideParams{Width: 390, Height: 844, DeviceScaleFactor: 2, Mobile: false})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"action": "click", "x": 75, "y": 75,
		"snapshot_id": old["snapshot_id"], "tab_id": old["tab_id"]})
	runToolErr(t, tools, "browser_mouse", string(args))
	shot := browserShot(t, tools)
	if shot["viewport_width"] != float64(390) || shot["image_width"] != float64(780) {
		t.Fatalf("CSS/bitmap scaling not reported: %#v", shot)
	}
	before, _ := shotImage(t, ctx)
	visualMouse(t, tools, map[string]any{"action": "click", "x": 75, "y": 75})
	browserShot(t, tools)
	after, _ := shotImage(t, ctx)
	if before.At(150, 150) == after.At(150, 150) {
		t.Fatal("CSS coordinates were treated as bitmap pixels")
	}
	old = browserShot(t, tools)
	browserEval[bool](t, ctx, `!!window.scrollTo(0,200)`)
	args, _ = json.Marshal(map[string]any{"action": "click", "x": 75, "y": 75,
		"snapshot_id": old["snapshot_id"], "tab_id": old["tab_id"]})
	runToolErr(t, tools, "browser_mouse", string(args))
}

func TestBrowserNavigationDoesNotExposeLocalFiles(t *testing.T) {
	tools := buildTools(&ToolContext{Workspace: t.TempDir()})
	for _, url := range []string{"file:///C:/Windows/win.ini", "javascript:alert(1)", "http://", "data:text/html,hello"} {
		args, _ := json.Marshal(map[string]string{"url": url})
		err := runToolErr(t, tools, "browser_navigate", string(args))
		if strings.Contains(err.Error(), "浏览器不可用") {
			t.Fatalf("URL should be rejected before browser access: %s", url)
		}
	}
}
