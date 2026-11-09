// browser.go 浏览器板块：chromedp（CDP）驱动本机真实 Chrome/Edge。
// 交互模型为"读页面拿元素索引 → 按索引点击/输入"（browser-use 风格），
// 模型不需要写 CSS 选择器；截图走 pendingImages 多模态管线让模型亲眼看
// 页面。浏览器进程懒启动、全局单实例、多标签页串行操作；持久化 profile
// （agent/browser-profile，可留存网页登录态）。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
)

// browserPageTextLimit read_page 的正文截断（进思维链必须限量）。
const browserPageTextLimit = 8 << 10

// browserElementLimit read_page 的可交互元素上限。
const browserElementLimit = 150

// browserActionTimeout 单次浏览器操作超时。
const browserActionTimeout = 60 * time.Second

// browserManager 浏览器单实例管理器。action 全程持锁串行（单标签页）。
type browserManager struct {
	mu       sync.Mutex
	exe      string
	headless bool
	profile  string

	allocCancel context.CancelFunc
	tabCancel   context.CancelFunc
	tabCtx      context.Context
	started     bool
	startErr    error
	rootCtx     context.Context
	tabs        map[string]*browserTab
	active      string
	shotSeq     uint64
}

// findBrowserExecutable 按配置 → Chrome 常见路径 → Edge 常见路径探测。
func findBrowserExecutable(cfgPath string) string {
	if strings.TrimSpace(cfgPath) != "" {
		return cfgPath
	}
	var candidates []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
		base := os.Getenv(env)
		if base == "" {
			continue
		}
		candidates = append(candidates,
			filepath.Join(base, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`),
		)
	}
	if runtime.GOOS == "windows" {
		for _, p := range candidates {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
		return ""
	}
	// 非 Windows：PATH 探测
	for _, name := range []string{"chromium", "google-chrome", "chromium-browser", "microsoft-edge"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// ensure 懒启动浏览器进程（失败结果缓存，避免每次工具调用都白试）。
func (b *browserManager) ensure() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return b.startErr
	}
	b.started = true
	exe := findBrowserExecutable(b.exe)
	if exe == "" {
		b.startErr = errors.New("未找到 Chrome/Edge，配置 agent.browser_executable 指定浏览器路径")
		return b.startErr
	}
	if err := os.MkdirAll(b.profile, 0o755); err != nil {
		b.startErr = err
		return err
	}
	opts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("headless", b.headless),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("window-size", "1280,800"),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.ExecPath(exe),
		chromedp.UserDataDir(b.profile),
	}
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	tab := &browserTab{}
	tabCtx, tabCancel := chromedp.NewContext(allocCtx, chromedp.WithListener(tab.onEvent))
	// 预热：真正拉起浏览器进程
	if _, err := chromedp.Run(tabCtx, chromedp.Location()); err != nil {
		allocCancel()
		tabCancel()
		b.startErr = fmt.Errorf("启动浏览器失败（%s）: %w", exe, err)
		return b.startErr
	}
	b.allocCancel = allocCancel
	b.tabCtx = tabCtx
	b.tabCancel = tabCancel
	b.rootCtx = tabCtx
	tab.ctx = tabCtx
	tab.id = string(chromedp.FromContext(tabCtx).Target.TargetID)
	b.tabs = map[string]*browserTab{tab.id: tab}
	b.active = tab.id
	denyCtx, cancel := context.WithTimeout(tabCtx, browserActionTimeout)
	defer cancel()
	if _, err := cdp.Call(denyCtx, chromedp.FromContext(tabCtx).Browser, browser.SetDownloadBehavior,
		browser.SetDownloadBehaviorParams{Behavior: browser.SetDownloadBehaviorBehaviorDeny}); err != nil {
		tabCancel()
		allocCancel()
		b.startErr = fmt.Errorf("无法禁用浏览器直接下载: %w", err)
		return b.startErr
	}
	return nil
}

// action 串行执行一次浏览器操作（带超时）。
func (b *browserManager) action(fn func(ctx context.Context) error) error {
	return b.withTab(false, func(c context.Context, _ *browserTab) error { return fn(c) })
}

// browserTools browser 板块。ctx.Browser 为 nil 时工具报错。
func browserTools(ctx *ToolContext) []Tool {
	return []Tool{
		browserNavigateTool(ctx),
		{
			Name:        "browser_read_page",
			Description: "读当前页正文（最多 8KB）及控件 idx，供 click/type 使用。",
			Parameters:  objectParams(nil, nil),
			Run: func(json.RawMessage) (string, error) {
				if ctx.Browser == nil {
					return "", errors.New("浏览器不可用")
				}
				var text string
				var els []map[string]any
				err := ctx.Browser.action(func(c context.Context) (aerr error) {
					text, aerr = chromedp.Run(c, chromedp.Evaluate[string](jsPageText()))
					if aerr != nil {
						return aerr
					}
					els, aerr = chromedp.Run(c, chromedp.Evaluate[[]map[string]any](jsInteractiveElements(browserElementLimit)))
					return aerr
				})
				if err != nil {
					return "", err
				}
				if len(text) > browserPageTextLimit {
					text = strings.ToValidUTF8(text[:browserPageTextLimit], "") + "\n…(截断)"
				}
				return marshalCompact(map[string]any{"text": text, "elements": els})
			},
		},
		{
			Name:        "browser_click",
			Description: "点击 read_page 返回的控件 idx。",
			Parameters: objectParams(map[string]any{
				"idx": intProp("控件 idx"),
			}, []string{"idx"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Idx int `json:"idx"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if ctx.Browser == nil {
					return "", errors.New("浏览器不可用")
				}
				err := ctx.Browser.withTab(false, func(c context.Context, tab *browserTab) error {
					tab.invalidate()
					point, err := chromedp.Run(c, chromedp.Evaluate[map[string]float64](fmt.Sprintf(jsClick(), p.Idx)))
					if err != nil {
						return err
					}
					if point == nil {
						return fmt.Errorf("元素 %d 不存在、被遮挡或不可点击，请重新 read_page/截图", p.Idx)
					}
					x, y := point["x"], point["y"]
					if err := dispatchBrowserMouse(c, tab, browserMouseArgs{Action: "click", X: &x, Y: &y}); err != nil {
						return err
					}
					_, err = chromedp.Run(c, chromedp.Sleep(300*time.Millisecond))
					return err
				})
				if err != nil {
					return "", err
				}
				return marshalCompact(map[string]any{"clicked": p.Idx})
			},
		},
		{
			Name:        "browser_type",
			Description: "填写控件 idx；submit=true 提交。",
			Parameters: objectParams(map[string]any{
				"idx":    intProp("控件 idx"),
				"text":   strProp("文本"),
				"submit": boolProp("输入后提交"),
			}, []string{"idx", "text"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Idx    int    `json:"idx"`
					Text   string `json:"text"`
					Submit bool   `json:"submit"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if ctx.Browser == nil {
					return "", errors.New("浏览器不可用")
				}
				var ok bool
				err := ctx.Browser.mutate(func(c context.Context) (aerr error) {
					ok, aerr = chromedp.Run(c, chromedp.Evaluate[bool](fmt.Sprintf(jsType(), p.Idx, jsQuote(p.Text), p.Submit)))
					if aerr != nil {
						return aerr
					}
					_, aerr = chromedp.Run(c, chromedp.Sleep(300*time.Millisecond))
					return aerr
				})
				if err != nil {
					return "", err
				}
				if !ok {
					return "", fmt.Errorf("元素 %d 不是可输入元素或页面已变化（重新 read_page）", p.Idx)
				}
				return marshalCompact(map[string]any{"typed": p.Idx, "submit": p.Submit})
			},
		},
		{
			Name:        "browser_scroll",
			Description: "滚动顶层页面；正数向下，负数向上。",
			Parameters: objectParams(map[string]any{
				"pixels": intProp("滚动像素，正下负上"),
			}, []string{"pixels"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Pixels int `json:"pixels"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if ctx.Browser == nil {
					return "", errors.New("浏览器不可用")
				}
				if p.Pixels < -10000 || p.Pixels > 10000 {
					return "", errors.New("pixels 范围为 -10000..10000")
				}
				err := ctx.Browser.mutate(func(c context.Context) (aerr error) {
					_, aerr = chromedp.Run(c, chromedp.Evaluate[any](fmt.Sprintf("window.scrollBy(0, %d)", p.Pixels)))
					if aerr != nil {
						return aerr
					}
					_, aerr = chromedp.Run(c, chromedp.Sleep(200*time.Millisecond))
					return aerr
				})
				if err != nil {
					return "", err
				}
				return "{}", nil
			},
		},
		browserScreenshotTool(ctx),
		browserMouseTool(ctx),
		browserKeyboardTool(ctx),
		browserTabsTool(ctx),
		browserDialogTool(ctx),
	}
}

// jsQuote 把文本转成 JS 字符串字面量（注入 Evaluate 前防注入）。
func jsQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// interactiveSelector 可交互元素选择器（read/click/type 共用同一查询顺序）。
const interactiveSelector = `'a[href],button,input,textarea,select,[role="button"]'`

// jsPageText 页面可见文本。
func jsPageText() string {
	return `(() => {
		const el = document.body;
		return el ? el.innerText.replace(/\n{3,}/g, '\n\n').trim() : '';
	})()`
}

// jsInteractiveElements 可交互元素清单（与 jsClick/jsType 同一选择器顺序）。
func jsInteractiveElements(limit int) string {
	return fmt.Sprintf(`(() => {
		const els = [...document.querySelectorAll(%s)];
		return els.slice(0, %d).map((el, i) => ({
			idx: i,
			tag: el.tagName.toLowerCase(),
			text: ((el.innerText || el.value || el.placeholder || el.getAttribute('aria-label') || '') + '').trim().slice(0, 80),
			type: el.type || ''
		})).filter(e => (e.text && e.tag !== 'input' && e.tag !== 'textarea') || ['text','search','email','password','number','tel','url'].includes(e.type) || e.tag === 'textarea');
	})()`, interactiveSelector, limit)
}

// jsClick resolves a visible point; the actual click uses trusted pointer input.
func jsClick() string {
	return fmt.Sprintf(`(() => {
		const els = [...document.querySelectorAll(%s)];
		const el = els[%%d];
		if (!el || el.disabled) return null;
		el.scrollIntoView({block: 'center'});
		const r = el.getBoundingClientRect();
		const left=Math.max(0,r.left), right=Math.min(innerWidth,r.right);
		const top=Math.max(0,r.top), bottom=Math.min(innerHeight,r.bottom);
		if (right<=left || bottom<=top) return null;
		const x=(left+right)/2, y=(top+bottom)/2;
		const hit=document.elementFromPoint(x,y);
		if (!hit || (hit!==el && !el.contains(hit))) return null;
		return {x,y};
	})()`, interactiveSelector)
}

// jsType 按索引输入（原生 setter 触发 React/Vue 监听）。
func jsType() string {
	return fmt.Sprintf(`((i, text, submit) => {
		const els = [...document.querySelectorAll(%s)];
		const el = els[i];
		if (!el || (el.tagName !== 'INPUT' && el.tagName !== 'TEXTAREA')) return false;
		el.focus();
		const proto = el.tagName === 'TEXTAREA' ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype;
		const setter = Object.getOwnPropertyDescriptor(proto, 'value').set;
		setter.call(el, text);
		el.dispatchEvent(new Event('input', {bubbles: true}));
		if (submit) {
			if (el.form && el.form.requestSubmit) { el.form.requestSubmit(); }
			else { el.dispatchEvent(new KeyboardEvent('keydown', {key: 'Enter', keyCode: 13, bubbles: true})); }
		}
		return true;
	})(%%d, %%s, %%v)`, interactiveSelector)
}

// defaultBrowserProfileDir 浏览器持久化 profile（网页登录态留存）。
const defaultBrowserProfileDir = "agent/browser-profile"

// newBrowserManager 构造浏览器管理器（懒启动，首次工具调用才拉起进程）。
func newBrowserManager(exe, profile string) *browserManager {
	return &browserManager{exe: exe, headless: true, profile: profile}
}

// Close 关闭浏览器进程（进程退出/测试收尾）。
func (b *browserManager) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, tab := range b.tabs {
		if tab.cancel != nil {
			tab.cancel()
		}
	}
	b.tabs = nil
	b.startErr = errors.New("浏览器已关闭")
	if b.tabCancel != nil {
		b.tabCancel()
		b.tabCancel = nil
	}
	if b.allocCancel != nil {
		b.allocCancel()
		b.allocCancel = nil
	}
}

// globalBrowser defaultNewManager 创建的进程级浏览器实例（退出清理用）。
var globalBrowser *browserManager

// CloseBrowser 关闭进程级浏览器实例（main 退出时调用）；nil 安全。
func CloseBrowser() {
	if globalBrowser != nil {
		globalBrowser.Close()
	}
}
