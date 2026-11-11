package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"time"

	"github.com/chromedp/chromedp"
)

type browserGeometry struct {
	URL           string  `json:"url"`
	Width         float64 `json:"viewport_width"`
	Height        float64 `json:"viewport_height"`
	ScrollX       float64 `json:"scroll_x"`
	ScrollY       float64 `json:"scroll_y"`
	Scale         float64 `json:"device_scale"`
	ContentWidth  float64 `json:"content_width"`
	ContentHeight float64 `json:"content_height"`
}

type browserSnapshot struct {
	browserGeometry
	ID       string
	Revision uint64
	FullPage bool
	Created  time.Time
}

func readBrowserGeometry(c context.Context) (browserGeometry, error) {
	return chromedp.Run(c, chromedp.Evaluate[browserGeometry](`({
		url:location.href,viewport_width:innerWidth,viewport_height:innerHeight,
		scroll_x:scrollX,scroll_y:scrollY,device_scale:devicePixelRatio,
		content_width:Math.max(innerWidth,document.documentElement.scrollWidth),
		content_height:Math.max(innerHeight,document.documentElement.scrollHeight)})`))
}

// captureBrowser 截当前视口或整页，返回元数据与编码后的图片字节。
// attach=true 时把图片附到本轮上下文（供模型直接看）；false 用于存盘，
// 不消耗视觉 token。截图的尺寸/像素/字节上限与页面稳定性校验都在这里统一做。
func captureBrowser(ctx *ToolContext, fullPage, attach bool) (map[string]any, []byte, error) {
	if ctx == nil || ctx.Browser == nil {
		return nil, nil, errors.New("浏览器不可用")
	}
	if attach && ctx.Turn == nil {
		return nil, nil, errors.New("无会话上下文，截图无处附加")
	}
	browser := ctx.Browser
	var result map[string]any
	var encoded []byte
	err := browser.withTab(false, func(c context.Context, tab *browserTab) error {
		tab.mu.Lock()
		revision := tab.revision
		navigation := tab.navigation
		tab.mu.Unlock()
		g, err := readBrowserGeometry(c)
		if err != nil {
			return err
		}
		w, h := g.Width, g.Height
		if fullPage {
			w, h = g.ContentWidth, g.ContentHeight
		}
		if w <= 0 || h <= 0 || g.Scale <= 0 || w*g.Scale > 8192 || h*g.Scale > 8192 ||
			w*h*g.Scale*g.Scale > 8_000_000 {
			return errors.New("截图过大（最多 800 万像素、单边 8192）；请滚动后截视口")
		}
		var buf []byte
		if fullPage {
			buf, err = chromedp.Run(c, chromedp.FullScreenshot(70))
		} else {
			buf, err = chromedp.Run(c, chromedp.CaptureScreenshot())
		}
		if err != nil {
			return err
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(buf))
		if err != nil {
			return err
		}
		if len(buf) > 8<<20 {
			return errors.New("截图编码超过 8MB")
		}
		after, err := readBrowserGeometry(c)
		if err != nil {
			return err
		}
		if g != after {
			return errors.New("截图期间页面尺寸或位置变化，请重试")
		}
		tab.mu.Lock()
		defer tab.mu.Unlock()
		if tab.navigation != navigation || (!fullPage && tab.revision != revision) {
			return errors.New("截图期间页面切换，请重试")
		}
		browser.shotSeq++
		now := time.Now()
		id := fmt.Sprintf("%s:%d", tab.id, browser.shotSeq)
		tab.snapshot = &browserSnapshot{browserGeometry: g, ID: id, Revision: tab.revision, FullPage: fullPage, Created: now}
		if attach {
			ctx.Turn.AddPendingImage("data:image/" + format + ";base64," + base64.StdEncoding.EncodeToString(buf))
		}
		encoded = buf
		result = map[string]any{
			"attached": attach, "bytes": len(buf), "mime": "image/" + format,
			"image_width": cfg.Width, "image_height": cfg.Height,
			"viewport_width": g.Width, "viewport_height": g.Height,
			"scroll_x": g.ScrollX, "scroll_y": g.ScrollY, "device_scale": g.Scale,
			"tab_id": tab.id, "snapshot_id": id, "full_page": fullPage,
			"url": truncateRunes(g.URL, 1000), "captured_at": now.Format(time.RFC3339Nano),
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return result, encoded, nil
}

func browserScreenshotTool(ctx *ToolContext) Tool {
	return Tool{
		Name: "browser_screenshot", Description: "截图并附图，返回尺寸、CSS 视口坐标、tab_id/snapshot_id。只看不留存时用它；要存文件用 browser_save_screenshot。",
		Parameters: objectParams(map[string]any{"full_page": boolProp("整页图仅查看，不能用于坐标操作；默认视口图")}, nil),
		Run: func(args json.RawMessage) (string, error) {
			var p struct {
				FullPage bool `json:"full_page"`
			}
			if err := decodeArgs(args, &p); err != nil {
				return "", err
			}
			result, _, err := captureBrowser(ctx, p.FullPage, true)
			if err != nil {
				return "", err
			}
			return marshalCompact(result)
		},
	}
}
