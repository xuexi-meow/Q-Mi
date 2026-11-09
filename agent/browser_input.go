package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

type browserMouseArgs struct {
	Action     string   `json:"action"`
	TabID      string   `json:"tab_id"`
	SnapshotID string   `json:"snapshot_id"`
	X          *float64 `json:"x"`
	Y          *float64 `json:"y"`
	ToX        *float64 `json:"to_x"`
	ToY        *float64 `json:"to_y"`
	DeltaX     float64  `json:"delta_x"`
	DeltaY     float64  `json:"delta_y"`
}

func browserMouseTool(ctx *ToolContext) Tool {
	return Tool{
		Name: "browser_mouse", Description: "视口坐标鼠标操作，动作后旧图失效。",
		Parameters: objectParams(map[string]any{
			"action": strProp("click/double_click/right_click/move/drag/wheel"),
			"tab_id": strProp("截图页 ID"), "snapshot_id": strProp("视口图 ID，2 分钟有效"),
			"x":       map[string]any{"type": "number", "description": "CSS x"},
			"y":       map[string]any{"type": "number", "description": "CSS y"},
			"to_x":    map[string]any{"type": "number", "description": "drag 终点 x"},
			"to_y":    map[string]any{"type": "number", "description": "drag 终点 y"},
			"delta_x": map[string]any{"type": "number", "description": "wheel 横移，-2000..2000"},
			"delta_y": map[string]any{"type": "number", "description": "wheel 纵移，正下负上，-2000..2000"},
		}, []string{"action", "tab_id", "snapshot_id", "x", "y"}),
		Run: func(args json.RawMessage) (string, error) {
			var p browserMouseArgs
			if err := decodeArgs(args, &p); err != nil {
				return "", err
			}
			switch p.Action {
			case "click", "double_click", "right_click", "move", "drag", "wheel":
			default:
				return "", errors.New("未知鼠标 action")
			}
			if p.X == nil || p.Y == nil || (p.Action == "drag" && (p.ToX == nil || p.ToY == nil)) {
				return "", errors.New("缺少坐标 x/y 或拖动终点 to_x/to_y")
			}
			if !finiteBrowserNumber(p.DeltaX) || !finiteBrowserNumber(p.DeltaY) ||
				math.Abs(p.DeltaX) > 2000 || math.Abs(p.DeltaY) > 2000 {
				return "", errors.New("滚轮量必须有限且绝对值不超过 2000")
			}
			if ctx.Browser == nil {
				return "", errors.New("浏览器不可用")
			}
			err := ctx.Browser.withTab(false, func(c context.Context, tab *browserTab) error {
				tab.mu.Lock()
				snapshot := tab.snapshot
				revision := tab.revision
				tab.mu.Unlock()
				if snapshot == nil || p.TabID != tab.id || p.SnapshotID != snapshot.ID ||
					snapshot.FullPage || snapshot.Revision != revision || time.Since(snapshot.Created) > 2*time.Minute {
					return errors.New("截图不存在、已过期或不是当前视口，请 browser_screenshot 后重试")
				}
				g, err := readBrowserGeometry(c)
				if err != nil {
					return err
				}
				if snapshot.browserGeometry != g {
					tab.invalidate()
					return errors.New("页面尺寸或滚动位置已变化，请重新截图")
				}
				check := func(x, y float64) bool {
					return finiteBrowserNumber(x) && finiteBrowserNumber(y) && x >= 0 && y >= 0 && x < g.Width && y < g.Height
				}
				if !check(*p.X, *p.Y) || (p.Action == "drag" && !check(*p.ToX, *p.ToY)) {
					return errors.New("坐标必须在当前 CSS 视口内")
				}
				tab.mu.Lock()
				if tab.revision != revision {
					tab.mu.Unlock()
					return errors.New("页面已变化，请重新截图")
				}
				tab.snapshot = nil
				tab.revision++
				tab.mu.Unlock()
				return dispatchBrowserMouse(c, tab, p)
			})
			if err != nil {
				return "", err
			}
			return marshalCompact(map[string]any{"action": p.Action, "tab_id": p.TabID, "snapshot_invalidated": true})
		},
	}
}

func finiteBrowserNumber(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func dispatchBrowserMouse(c context.Context, tab *browserTab, p browserMouseArgs) error {
	x, y := *p.X, *p.Y
	t := chromedp.FromContext(c).Target
	if _, err := cdp.Call(c, t, page.BringToFront, cdp.Empty{}); err != nil {
		return err
	}
	send := func(typ input.DispatchMouseEventType, button input.MouseButton, buttons, count int64) error {
		params := input.DispatchMouseEventParams{Type: typ, X: x, Y: y, Button: button, Buttons: buttons, ClickCount: count}
		if typ == input.DispatchMouseEventTypeMouseWheel {
			// CDP requires both deltas even when zero; the generated type omits zero.
			return t.Call(c, input.DispatchMouseEvent.Method, struct {
				input.DispatchMouseEventParams
				DeltaX float64 `json:"deltaX"`
				DeltaY float64 `json:"deltaY"`
			}{params, p.DeltaX, p.DeltaY}, nil)
		}
		_, err := cdp.Call(c, t, input.DispatchMouseEvent, params)
		return err
	}
	if p.Action == "wheel" {
		if err := send(input.DispatchMouseEventTypeMouseMoved, input.MouseButtonNone, 0, 0); err != nil {
			return err
		}
		return send(input.DispatchMouseEventTypeMouseWheel, input.MouseButtonNone, 0, 0)
	}
	if p.Action == "move" {
		return send(input.DispatchMouseEventTypeMouseMoved, input.MouseButtonNone, 0, 0)
	}
	button, buttons := input.MouseButtonLeft, int64(1)
	if p.Action == "right_click" {
		button, buttons = input.MouseButtonRight, 2
	}
	pressed := false
	defer func() {
		if pressed {
			// Cleanup uses the live tab, not the canceled action context.
			cleanup, cancel := context.WithTimeout(tab.ctx, 2*time.Second)
			defer cancel()
			_, _ = cdp.Call(cleanup, t, input.DispatchMouseEvent, input.DispatchMouseEventParams{
				Type: input.DispatchMouseEventTypeMouseReleased, X: x, Y: y, Button: button, ClickCount: 1,
			})
		}
	}()
	if err := send(input.DispatchMouseEventTypeMouseMoved, input.MouseButtonNone, 0, 0); err != nil {
		return err
	}
	count := int64(1)
	if p.Action == "double_click" {
		count = 2
	}
	for click := int64(1); click <= count; click++ {
		pressed = true
		if err := send(input.DispatchMouseEventTypeMousePressed, button, buttons, click); err != nil {
			return err
		}
		if p.Action == "drag" {
			startX, startY := x, y
			for step := 1; step <= 12; step++ {
				x = startX + (*p.ToX-startX)*float64(step)/12
				y = startY + (*p.ToY-startY)*float64(step)/12
				if err := send(input.DispatchMouseEventTypeMouseMoved, button, buttons, 0); err != nil {
					return err
				}
			}
		}
		if err := send(input.DispatchMouseEventTypeMouseReleased, button, 0, click); err != nil {
			return err
		}
		pressed = false
	}
	return nil
}

func browserKeyboardTool(ctx *ToolContext) Tool {
	return Tool{
		Name: "browser_keyboard", Description: "焦点输入：text 或 key，二选一。",
		Parameters: objectParams(map[string]any{
			"text": strProp("最多 10000 字符"), "key": strProp("如 CTRL+A、Enter、SHIFT+Tab；修饰键 CTRL/ALT/SHIFT/META"),
		}, nil),
		Run: func(args json.RawMessage) (string, error) {
			var p struct {
				Text *string `json:"text"`
				Key  *string `json:"key"`
			}
			if err := decodeArgs(args, &p); err != nil {
				return "", err
			}
			if (p.Text == nil) == (p.Key == nil) {
				return "", errors.New("text/key 必须且只能提供一项")
			}
			var key string
			var modifiers chromedp.Modifier
			if p.Text != nil && utf8.RuneCountInString(*p.Text) > 10000 {
				return "", errors.New("text 最多 10000 字符")
			}
			if p.Key != nil {
				var err error
				key, modifiers, err = parseBrowserKey(*p.Key)
				if err != nil {
					return "", err
				}
			}
			if ctx.Browser == nil {
				return "", errors.New("浏览器不可用")
			}
			err := ctx.Browser.withTab(false, func(c context.Context, tab *browserTab) error {
				tab.invalidate()
				if _, err := cdp.Call(c, chromedp.FromContext(c).Target, page.BringToFront, cdp.Empty{}); err != nil {
					return err
				}
				if p.Text != nil {
					_, err := cdp.Call(c, chromedp.FromContext(c).Target, input.InsertText, input.InsertTextParams{Text: *p.Text})
					return err
				}
				// Modifiers are scoped to each dispatched key event, never latched.
				_, err := chromedp.Run(c, chromedp.KeyEvent(key, chromedp.KeyModifiers(modifiers)))
				if err != nil {
					cleanup, cancel := context.WithTimeout(tab.ctx, 2*time.Second)
					defer cancel()
					for _, r := range key {
						for _, event := range kb.Encode(r) {
							if event.Type == input.DispatchKeyEventTypeKeyUp {
								event.Modifiers = 0
								_, _ = cdp.Call(cleanup, chromedp.FromContext(c).Target, input.DispatchKeyEvent, *event)
							}
						}
					}
				}
				return err
			})
			if err != nil {
				return "", err
			}
			return `{"sent":true,"snapshot_invalidated":true}`, nil
		},
	}
}

func parseBrowserKey(value string) (string, chromedp.Modifier, error) {
	parts := strings.Split(value, "+")
	var modifiers chromedp.Modifier
	for _, part := range parts[:len(parts)-1] {
		switch strings.ToUpper(part) {
		case "CTRL", "CONTROL":
			modifiers |= chromedp.ModifierCtrl
		case "ALT":
			modifiers |= chromedp.ModifierAlt
		case "SHIFT":
			modifiers |= chromedp.ModifierShift
		case "META", "CMD":
			modifiers |= chromedp.ModifierMeta
		default:
			return "", 0, fmt.Errorf("未知修饰键 %q", part)
		}
	}
	last := parts[len(parts)-1]
	keys := map[string]string{
		"ENTER": kb.Enter, "TAB": kb.Tab, "ESCAPE": kb.Escape, "ESC": kb.Escape,
		"BACKSPACE": kb.Backspace, "DELETE": kb.Delete, "HOME": kb.Home, "END": kb.End,
		"ARROWUP": kb.ArrowUp, "ARROWDOWN": kb.ArrowDown, "ARROWLEFT": kb.ArrowLeft, "ARROWRIGHT": kb.ArrowRight,
		"PAGEUP": kb.PageUp, "PAGEDOWN": kb.PageDown, "SPACE": " ", "PLUS": "+",
	}
	if key, ok := keys[strings.ToUpper(last)]; ok {
		return key, modifiers, nil
	}
	if utf8.RuneCountInString(last) == 1 && []rune(last)[0] >= 32 && []rune(last)[0] <= 126 {
		if modifiers&chromedp.ModifierShift != 0 {
			last = strings.ToUpper(last)
		} else if modifiers != 0 {
			last = strings.ToLower(last)
		}
		return last, modifiers, nil
	}
	return "", 0, errors.New("不支持的 key；用 Enter/Tab/方向键等名称、单个 ASCII 字符或组合键")
}
