package agent

import (
	"context"
	"errors"
	"sync"

	"github.com/chromedp/cdproto/page"
)

var errBrowserDialog = errors.New("网页对话框待处理；操作可能已生效，请用 browser_dialog get/accept/dismiss，不要重复提交")

type browserTab struct {
	id     string
	ctx    context.Context
	cancel context.CancelFunc

	mu           sync.Mutex // Events must never acquire the manager's action lock.
	revision     uint64
	navigation   uint64
	dialog       *browserDialog
	actionCancel context.CancelFunc
	snapshot     *browserSnapshot
}

type browserDialog struct {
	Type          string `json:"type"`
	Message       string `json:"message"`
	DefaultPrompt string `json:"default_prompt,omitempty"`
}

func (t *browserTab) onEvent(ev any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch e := ev.(type) {
	case *page.EventFrameNavigated, *page.EventNavigatedWithinDocument:
		t.navigation++
		t.revision++
		t.snapshot = nil
	case *page.EventFrameResized:
		t.revision++
		t.snapshot = nil
	case *page.EventJavascriptDialogOpening:
		t.dialog = &browserDialog{
			Type: string(e.Type), Message: truncateRunes(e.Message, 1000),
			DefaultPrompt: truncateRunes(e.DefaultPrompt, 1000),
		}
		t.revision++
		t.snapshot = nil
		if t.actionCancel != nil {
			t.actionCancel()
		}
	case *page.EventJavascriptDialogClosed:
		t.dialog = nil
		t.revision++
		t.snapshot = nil
	}
}

func (t *browserTab) invalidate() {
	t.mu.Lock()
	t.revision++
	t.snapshot = nil
	t.mu.Unlock()
}

func (b *browserManager) withTab(allowDialog bool, fn func(context.Context, *browserTab) error) error {
	if err := b.ensure(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	tab := b.tabs[b.active]
	if tab == nil {
		return errors.New("当前标签页不存在，请 browser_tabs list")
	}
	return b.runTabLocked(tab, allowDialog, fn)
}

// runTabLocked also serves new tabs while the manager's action lock is held.
func (b *browserManager) runTabLocked(tab *browserTab, allowDialog bool, fn func(context.Context, *browserTab) error) error {
	c, cancel := context.WithTimeout(tab.ctx, browserActionTimeout)
	defer cancel()
	if !allowDialog {
		tab.mu.Lock()
		if tab.dialog != nil {
			tab.mu.Unlock()
			return errBrowserDialog
		}
		tab.actionCancel = cancel
		tab.mu.Unlock()
		defer func() {
			tab.mu.Lock()
			tab.actionCancel = nil
			tab.mu.Unlock()
		}()
	}
	err := fn(c, tab)
	tab.mu.Lock()
	pending := tab.dialog != nil
	tab.mu.Unlock()
	if pending && !allowDialog {
		return errBrowserDialog
	}
	return err
}

func (b *browserManager) mutate(fn func(context.Context) error) error {
	return b.withTab(false, func(c context.Context, tab *browserTab) error {
		tab.invalidate()
		return fn(c)
	})
}
