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

func browserScreenshotTool(ctx *ToolContext) Tool {
	return Tool{
		Name: "browser_screenshot", Description: "截图并附图，返回尺寸、CSS 视口坐标、tab_id/snapshot_id。",
		Parameters: objectParams(map[string]any{"full_page": boolProp("整页图仅查看，不能用于坐标操作；默认视口图")}, nil),
		Run: func(args json.RawMessage) (string, error) {
			var p struct {
				FullPage bool `json:"full_page"`
			}
			if err := decodeArgs(args, &p); err != nil {
				return "", err
			}
			if ctx.Browser == nil {
				return "", errors.New("浏览器不可用")
			}
			if ctx.Turn == nil {
				return "", errors.New("无会话上下文，截图无处附加")
			}
			var result map[string]any
			err := ctx.Browser.withTab(false, func(c context.Context, tab *browserTab) error {
				tab.mu.Lock()
				revision := tab.revision
				navigation := tab.navigation
				tab.mu.Unlock()
				g, err := readBrowserGeometry(c)
				if err != nil {
					return err
				}
				w, h := g.Width, g.Height
				if p.FullPage {
					w, h = g.ContentWidth, g.ContentHeight
				}
				if w <= 0 || h <= 0 || g.Scale <= 0 || w*g.Scale > 8192 || h*g.Scale > 8192 ||
					w*h*g.Scale*g.Scale > 8_000_000 {
					return errors.New("截图过大（最多 800 万像素、单边 8192）；请滚动后截视口")
				}
				var buf []byte
				if p.FullPage {
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
				if tab.navigation != navigation || (!p.FullPage && tab.revision != revision) {
					return errors.New("截图期间页面切换，请重试")
				}
				ctx.Browser.shotSeq++
				now := time.Now()
				id := fmt.Sprintf("%s:%d", tab.id, ctx.Browser.shotSeq)
				tab.snapshot = &browserSnapshot{browserGeometry: g, ID: id, Revision: tab.revision, FullPage: p.FullPage, Created: now}
				ctx.Turn.AddPendingImage("data:image/" + format + ";base64," + base64.StdEncoding.EncodeToString(buf))
				result = map[string]any{
					"attached": true, "bytes": len(buf), "mime": "image/" + format,
					"image_width": cfg.Width, "image_height": cfg.Height,
					"viewport_width": g.Width, "viewport_height": g.Height,
					"scroll_x": g.ScrollX, "scroll_y": g.ScrollY, "device_scale": g.Scale,
					"tab_id": tab.id, "snapshot_id": id, "full_page": p.FullPage,
					"url": truncateRunes(g.URL, 1000), "captured_at": now.Format(time.RFC3339Nano),
				}
				return nil
			})
			if err != nil {
				return "", err
			}
			return marshalCompact(result)
		},
	}
}
