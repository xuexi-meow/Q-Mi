package agent

import "Q-Mi/agent/tools/browser"

type browserManager = browser.Manager

const defaultBrowserProfileDir = browser.DefaultProfileDir

func newBrowserManager(executable, profile string) *browserManager {
	return browser.NewManager(executable, profile)
}

func browserTools(ctx *ToolContext) []Tool {
	return browser.Tools(&browser.Dependencies{
		Browser: ctx.Browser, Turn: ctx.Turn,
		Workspace: ctx.Workspace, Approved: ctx.Approved,
	})
}

var globalBrowser *browserManager

// CloseBrowser 由 main 在退出时关闭组装层持有的浏览器。
func CloseBrowser() {
	if globalBrowser != nil {
		globalBrowser.Close()
	}
}
