package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

const maxBrowserTabs = 20

func initializeBrowserTab(tab *browserTab) error {
	// The first Run owns the target reader's lifetime, so use the tab context.
	timer := time.AfterFunc(10*time.Second, tab.cancel)
	defer timer.Stop()
	_, err := chromedp.Run(tab.ctx, chromedp.Func(func(context.Context, *chromedp.Target) error { return nil }))
	return err
}

func validateBrowserURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		len(raw) > 4096 || u.User != nil {
		return errors.New("仅允许完整 http/https URL（不含内嵌凭据）；本地文件请用工作区工具")
	}
	return nil
}

type browserNavigateArgs struct {
	Action string `json:"action"`
	URL    string `json:"url"`
}

func navigateBrowser(c context.Context, tab *browserTab, p browserNavigateArgs) (map[string]any, error) {
	tab.invalidate()
	var action chromedp.Action[chromedp.Void]
	switch p.Action {
	case "back", "forward":
		action = chromedp.Func(func(c context.Context, t *chromedp.Target) error {
			return navigateBrowserHistory(c, t, p.Action == "forward")
		})
	case "reload":
		action = chromedp.Reload()
	default:
		action = chromedp.Navigate(p.URL)
	}
	if _, err := chromedp.Run(c, action); err != nil {
		return nil, err
	}
	title, err := chromedp.Run(c, chromedp.Title())
	if err != nil {
		return nil, err
	}
	location, err := chromedp.Run(c, chromedp.Location())
	if err != nil {
		return nil, err
	}
	return map[string]any{"title": truncateRunes(title, 200), "url": truncateRunes(location, 1000), "tab_id": tab.id}, nil
}

func navigateBrowserHistory(c context.Context, t *chromedp.Target, forward bool) error {
	history, err := cdp.Call(c, t, page.GetNavigationHistory, cdp.Empty{})
	if err != nil {
		return err
	}
	index := history.CurrentIndex - 1
	if forward {
		index = history.CurrentIndex + 1
	}
	if index < 0 || index >= int64(len(history.Entries)) {
		return errors.New("没有对应的历史页面")
	}
	tree, err := cdp.Call(c, t, page.GetFrameTree, cdp.Empty{})
	if err != nil {
		return err
	}
	events, cancel := context.WithCancel(c)
	defer cancel()
	committed := make(chan struct{}, 1)
	if err := chromedp.Listen(events, func(ev any) {
		matched := false
		switch e := ev.(type) {
		case *page.EventFrameNavigated:
			matched = e.Frame.ParentID == ""
		case *page.EventNavigatedWithinDocument:
			matched = e.FrameID == tree.FrameTree.Frame.ID
		}
		if matched {
			select {
			case committed <- struct{}{}:
			default:
			}
		}
	}); err != nil {
		return err
	}
	if _, err := cdp.Call(c, t, page.NavigateToHistoryEntry, page.NavigateToHistoryEntryParams{EntryID: history.Entries[index].ID}); err != nil {
		return err
	}
	// BFCache restoration commits without a fresh lifecycle "init"/load event.
	select {
	case <-committed:
	case <-c.Done():
		return c.Err()
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, err := chromedp.Run(c, chromedp.Evaluate[bool](`document.readyState !== "loading"`))
		if err == nil && ready {
			return nil
		}
		select {
		case <-ticker.C:
		case <-c.Done():
			return c.Err()
		}
	}
}

func browserNavigateTool(ctx *ToolContext) Tool {
	return Tool{
		Name: "browser_navigate", Description: "当前页打开 URL，或前进/后退/刷新。",
		Parameters: objectParams(map[string]any{"url": strProp("http/https URL"), "action": strProp("默认 open；back/forward/reload 时不传 url")}, nil),
		Run: func(args json.RawMessage) (string, error) {
			var p browserNavigateArgs
			if err := decodeArgs(args, &p); err != nil {
				return "", err
			}
			if p.Action == "" {
				p.Action = "open"
			}
			switch p.Action {
			case "open":
				if err := validateBrowserURL(p.URL); err != nil {
					return "", err
				}
			case "back", "forward", "reload":
				if p.URL != "" {
					return "", errors.New("back/forward/reload 不接受 url")
				}
			default:
				return "", errors.New("未知导航 action")
			}
			if ctx.Browser == nil {
				return "", errors.New("浏览器不可用")
			}
			var result map[string]any
			err := ctx.Browser.withTab(false, func(c context.Context, tab *browserTab) (err error) {
				result, err = navigateBrowser(c, tab, p)
				return err
			})
			if err != nil {
				return "", err
			}
			return marshalCompact(result)
		},
	}
}

func (b *browserManager) syncTabs(c context.Context) ([]*target.Info, int, error) {
	infos, err := chromedp.Targets(c)
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].TargetID < infos[j].TargetID })
	live := map[string]bool{}
	for _, info := range infos {
		if info.Type == "page" {
			live[string(info.TargetID)] = true
		}
	}
	for id, tab := range b.tabs {
		if !live[id] {
			if tab.cancel != nil {
				tab.cancel()
			}
			delete(b.tabs, id)
		}
	}
	var selected []*target.Info
	omitted := 0
	for _, info := range infos {
		id := string(info.TargetID)
		if info.Type != "page" {
			continue
		}
		if b.tabs[id] == nil {
			// Only adopt popups from this manager, never unrelated DevTools targets.
			if b.tabs[string(info.OpenerID)] == nil {
				continue
			}
			if len(b.tabs) >= maxBrowserTabs {
				omitted++
				continue
			}
			tab := &browserTab{id: id}
			tab.ctx, tab.cancel = chromedp.NewContext(b.rootCtx, chromedp.WithTargetID(info.TargetID), chromedp.WithListener(tab.onEvent))
			err := initializeBrowserTab(tab)
			if err != nil {
				tab.cancel()
				return nil, omitted, err
			}
			b.tabs[id] = tab
		}
		selected = append(selected, info)
	}
	if b.tabs[b.active] == nil && len(selected) > 0 {
		b.active = string(selected[0].TargetID)
		b.tabs[b.active].invalidate()
	}
	return selected, omitted, nil
}

func browserTabsTool(ctx *ToolContext) Tool {
	return Tool{
		Name: "browser_tabs", Description: "列出标签页与弹窗，或新建、切换、关闭；最多 20 页。",
		Parameters: objectParams(map[string]any{
			"action": strProp("list/new/switch/close"), "tab_id": strProp("switch/close 目标"),
			"url": strProp("new 可选 http/https URL，省略为空白页"),
		}, []string{"action"}),
		Run: func(args json.RawMessage) (string, error) {
			var p struct {
				Action string `json:"action"`
				TabID  string `json:"tab_id"`
				URL    string `json:"url"`
			}
			if err := decodeArgs(args, &p); err != nil {
				return "", err
			}
			switch p.Action {
			case "list", "new":
			case "switch", "close":
				if p.TabID == "" {
					return "", errors.New("缺少 tab_id")
				}
			default:
				return "", errors.New("未知标签页 action")
			}
			if p.URL != "" {
				if p.Action != "new" {
					return "", errors.New("仅 new 接受 url")
				}
				if err := validateBrowserURL(p.URL); err != nil {
					return "", err
				}
			}
			b := ctx.Browser
			if b == nil {
				return "", errors.New("浏览器不可用")
			}
			if err := b.ensure(); err != nil {
				return "", err
			}
			b.mu.Lock()
			defer b.mu.Unlock()
			c, cancel := context.WithTimeout(b.rootCtx, browserActionTimeout)
			defer cancel()
			infos, omitted, err := b.syncTabs(c)
			if err != nil {
				return "", err
			}
			switch p.Action {
			case "list":
				rows := make([]map[string]any, 0, len(infos))
				for _, info := range infos {
					rows = append(rows, map[string]any{"tab_id": string(info.TargetID), "title": truncateRunes(info.Title, 128), "url": truncateRunes(info.URL, 1000)})
				}
				return marshalCompact(map[string]any{"tabs": rows, "active_tab_id": b.active, "omitted": omitted})
			case "new":
				if len(b.tabs) >= maxBrowserTabs {
					return "", errors.New("标签页已达上限，请先关闭")
				}
				tab := &browserTab{}
				tab.ctx, tab.cancel = chromedp.NewContext(b.rootCtx, chromedp.WithListener(tab.onEvent))
				err := initializeBrowserTab(tab)
				if err != nil {
					tab.cancel()
					return "", err
				}
				tab.id = string(chromedp.FromContext(tab.ctx).Target.TargetID)
				if old := b.tabs[b.active]; old != nil {
					old.invalidate()
				}
				b.tabs[tab.id], b.active = tab, tab.id
				if p.URL != "" {
					var result map[string]any
					err := b.runTabLocked(tab, false, func(c context.Context, t *browserTab) (err error) {
						result, err = navigateBrowser(c, t, browserNavigateArgs{Action: "open", URL: p.URL})
						return err
					})
					if err != nil {
						return "", fmt.Errorf("新标签页 %s 已创建：%w", tab.id, err)
					}
					return marshalCompact(result)
				}
				return marshalCompact(map[string]any{"tab_id": tab.id, "url": "about:blank"})
			case "switch":
				tab := b.tabs[p.TabID]
				if tab == nil {
					return "", errors.New("未知 tab_id，请 list")
				}
				if _, err := cdp.Call(c, chromedp.FromContext(b.rootCtx).Browser, target.ActivateTarget, target.ActivateTargetParams{TargetID: target.ID(p.TabID)}); err != nil {
					return "", err
				}
				if old := b.tabs[b.active]; old != nil {
					old.invalidate()
				}
				tab.invalidate()
				b.active = p.TabID
				return marshalCompact(map[string]any{"tab_id": p.TabID})
			case "close":
				tab := b.tabs[p.TabID]
				if tab == nil {
					return "", errors.New("未知 tab_id，请 list")
				}
				if len(b.tabs) <= 1 {
					return "", errors.New("保留至少一个标签页")
				}
				if _, err := cdp.Call(c, chromedp.FromContext(b.rootCtx).Browser, target.CloseTarget, target.CloseTargetParams{TargetID: target.ID(p.TabID)}); err != nil {
					return "", err
				}
				if tab.cancel != nil {
					tab.cancel()
				}
				delete(b.tabs, p.TabID)
				if b.active == p.TabID {
					b.active = ""
					for _, info := range infos {
						if next := b.tabs[string(info.TargetID)]; next != nil {
							b.active = next.id
							next.invalidate()
							break
						}
					}
				}
				return marshalCompact(map[string]any{"closed": p.TabID, "active_tab_id": b.active})
			}
			return "", errors.New("未知标签页 action")
		},
	}
}

func browserDialogTool(ctx *ToolContext) Tool {
	return Tool{
		Name: "browser_dialog", Description: "查看或处理网页对话框，不自动接受。",
		Parameters: objectParams(map[string]any{"action": strProp("get/accept/dismiss"), "text": strProp("accept prompt 的文本，最多 10000 字符")}, []string{"action"}),
		Run: func(args json.RawMessage) (string, error) {
			var p struct {
				Action string `json:"action"`
				Text   string `json:"text"`
			}
			if err := decodeArgs(args, &p); err != nil {
				return "", err
			}
			if p.Action != "get" && p.Action != "accept" && p.Action != "dismiss" {
				return "", errors.New("未知对话框 action")
			}
			if utf8.RuneCountInString(p.Text) > 10000 {
				return "", errors.New("text 最多 10000 字符")
			}
			if ctx.Browser == nil {
				return "", errors.New("浏览器不可用")
			}
			var result any
			err := ctx.Browser.withTab(true, func(c context.Context, tab *browserTab) error {
				tab.mu.Lock()
				dialog := tab.dialog
				tab.mu.Unlock()
				if p.Action == "get" {
					if dialog == nil {
						result = map[string]any{"pending": false}
					} else {
						result = dialog
					}
					return nil
				}
				if dialog == nil {
					return errors.New("没有待处理的网页对话框")
				}
				if p.Text != "" && (p.Action != "accept" || dialog.Type != "prompt") {
					return errors.New("仅 accept prompt 接受 text")
				}
				_, err := cdp.Call(c, chromedp.FromContext(c).Target, page.HandleJavaScriptDialog, page.HandleJavaScriptDialogParams{Accept: p.Action == "accept", PromptText: p.Text})
				if err != nil {
					return err
				}
				// The closed event clears only the dialog it belongs to; a new
				// dialog may already have opened by the time this command returns.
				result = map[string]any{"handled": true}
				return nil
			})
			if err != nil {
				return "", err
			}
			return marshalCompact(result)
		},
	}
}
