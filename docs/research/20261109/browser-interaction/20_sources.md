# Browser Interaction Source Checkpoints

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://chromedevtools.github.io/devtools-protocol/tot/Input/
**Method**: extract (main-agent web.run open; official redirect/index page)
**Confidence**: high
**Insight**: Main-agent verification confirms the official input API lists mouse, keyboard, text, drag, touch and scroll operations.

# Relevant extracted content

- The retrieved index lists dispatchMouseEvent, dispatchKeyEvent, insertText, dispatchDragEvent and synthesizeScrollGesture.
- Coordinate semantics and compatibility constraints remain documented in the detailed checkpoints below.
- web.run reference: turn656351view0.

---

**Time**: 2026-11-09 Asia/Shanghai
**Source**: F:/Go/Q-Mi/agent/browser.go; F:/Go/Q-Mi/agent/browser_test.go; F:/Go/Q-Mi/agent/toolkit.go; F:/Go/Q-Mi/go.mod
**Method**: extract (PowerShell Get-Content and rg; read only)
**Confidence**: high
**Insight**: Current limits are in Q-Mi's exposed tool layer, not a lack of browser automation primitives.

# Relevant extracted content

- browser.go:43 stores a single tabCtx; :130 routes all operations to it.
- browser.go:106 uses a persistent dedicated browser profile; :152 currently accepts file:// navigation.
- browser.go:136 exposes navigate/read_page/click/type/scroll/screenshot only.
- browser.go:376 limits discovery to a[href], button, input, textarea, select, [role="button"]; no canvas surfaces or arbitrary contenteditable.
- browser.go:406 invokes JavaScript el.click(), rather than a pointer sequence; :427 sets input/textarea value and dispatches synthetic input/Enter events.
- browser.go:297 scrolls the top-level window, not a pointer-targeted nested scroll container.
- browser.go:314 describes a fixed default 1280x800 screenshot, but the actual PNG dimensions and CSS viewport are not returned.
- browser.go:330 uses FullScreenshot(70); :340 hardcodes PNG MIME. Official pinned dependency checkpoint below explains the mismatch.
- browser_test.go:45 covers one DOM page with navigate/read/type/click/screenshot; :143 covers nil browser context. No canvas, nested wheel, hover/drag, keyboard, dialogs or tabs tests exist.
- toolkit.go:221-223 describes only DOM idx input followed by screenshots.
- go.mod:9-10 pins cdproto v0.159.0 and chromedp v0.23.0.

---

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://pkg.go.dev/github.com/chromedp/chromedp@v0.23.0
**Method**: query-search (web.run search)
**Confidence**: high
**Insight**: Current full-page screenshot MIME labeling is inconsistent with the pinned dependency: quality 70 produces JPEG but Q-Mi labels all screenshots PNG.

# Relevant extracted content

- Pinned-version FullScreenshot documentation states quality 100 yields PNG and other values yield JPEG.
- Local browser.go:330 calls FullScreenshot(70); browser.go:340 unconditionally prefixes data:image/png;base64.
- Proposed extension should derive MIME from real image bytes or request PNG explicitly.
- web.run reference: turn404769search2.

---

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://pkg.go.dev/github.com/chromedp/chromedp
**Method**: query-search (web.run search)
**Confidence**: high
**Insight**: Existing chromedp directly supports coordinate mouse input and navigation; replacing the project's browser stack is unnecessary for the proposed bounded extension.

# Relevant extracted content

- MouseClickXY sends a pressed/released left click to x,y; MouseEvent dispatches a specified event at x,y.
- KeyEventNode dispatches a keyboard event on an element; NavigateBack is exposed.
- The project pins chromedp v0.23.0; latest online documentation must be cross-checked with the pinned package before implementation.
- web.run reference: turn446992search3.

---

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://chromedevtools.github.io/devtools-protocol/tot/DOM/
**Method**: query-search (web.run search)
**Confidence**: high
**Insight**: Upload support deliberately hands local paths to the page, so it needs explicit application path boundaries rather than an unrestricted CDP passthrough.

# Relevant extracted content

- DOM.setFileInputFiles sets an array of file paths on a file input selected by nodeId, backendNodeId, or objectId.
- web.run reference: turn446992search1.

---

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://chromedevtools.github.io/devtools-protocol/tot/Browser/
**Method**: query-search (web.run search)
**Confidence**: high
**Insight**: Downloads can be denied or directed into a managed directory and observed as they progress. The model should not control arbitrary destination paths or treat a started download as completed.

# Relevant extracted content

- setDownloadBehavior has deny/allow/allowAndName/default; allow modes require downloadPath and can enable events.
- downloadWillBegin includes URL and suggestedFilename; downloadProgress reports inProgress/completed/canceled and byte counts.
- Completed filePath is not guaranteed present and the file itself is not guaranteed to exist; verify the filesystem outcome.
- web.run reference: turn446992search0.

---

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://chromedevtools.github.io/devtools-protocol/index.html
**Method**: query-search (web.run search)
**Confidence**: high
**Insight**: Tip-of-tree protocol documentation is not a promise that every installed Chrome/Edge supports every experimental method.

# Relevant extracted content

- Official protocol index warns that tip-of-tree changes frequently and can break.
- Experimental drag/download APIs require browser-version compatibility handling.
- web.run reference: turn446992search6.

---

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://chromedevtools.github.io/devtools-protocol/tot/Input/
**Method**: query-search (web.run search; browser interactions intentionally not launched)
**Confidence**: high
**Insight**: CDP exposes browser-input primitives for mouse, keyboard, text insertion, drag events, and scrolling gestures. Mouse coordinates are relative to the main-frame viewport in CSS pixels, not raw screenshot bitmap pixels.

# Relevant extracted content

- dispatchMouseEvent supports pressed, released, moved, and wheel events, button and clickCount; x/y use main-frame viewport CSS pixels.
- dispatchKeyEvent covers key down/up, modifiers, and key codes; insertText supports text such as IME and emoji input.
- dispatchDragEvent and synthesizeScrollGesture are experimental and must be checked against the installed browser/protocol.
- web.run reference: turn957077search0.
- The initial web.run open produced no usable payload/reference; the search retrieval above is the actual cited evidence.

---

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://chromedevtools.github.io/devtools-protocol/tot/Page/
**Method**: query-search (web.run search)
**Confidence**: high
**Insight**: Screenshot geometry, JavaScript dialogs, and back/forward history can be handled without adding a second automation framework.

# Relevant extracted content

- getLayoutMetrics exposes cssLayoutViewport, cssVisualViewport and cssContentSize; older device-pixel properties are deprecated.
- captureScreenshot accepts capture geometry; handleJavaScriptDialog accepts/dismisses alert, confirm, prompt and beforeunload.
- getNavigationHistory returns entries/current index; navigateToHistoryEntry and reload exist.
- web.run reference: turn204666search0.

---

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://chromedevtools.github.io/devtools-protocol/tot/Target/
**Method**: query-search (web.run search)
**Confidence**: high
**Insight**: CDP supports tabs and popups, but Q-Mi must explicitly manage target contexts and lifecycle rather than keep a single fixed tab.

# Relevant extracted content

- getTargets, createTarget, activateTarget, closeTarget and attachToTarget are documented.
- targetCreated/targetDestroyed/targetInfoChanged events support popup discovery and stale-tab invalidation.
- A BrowserContext is isolated like an incognito profile; this does not itself provide application-level permission policy.
- web.run reference: turn204666search1.

---
