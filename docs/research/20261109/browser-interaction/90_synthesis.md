# Q-Mi Browser Interaction Extension

Date: 2026-11-09, Asia/Shanghai.

Scope: research and design only. No browser was launched, no QQ/model was called, and no production or test code was changed for this research. Source evidence is in `20_sources.md`.

## Finding

The current browser tools are DOM-index driven. `agent/browser.go:376` excludes canvas from discovery, `:406` calls JavaScript `el.click()`, and `:297` scrolls only the window. The manager holds one tab (`:43`, `:130`). Existing tests cover a basic DOM page only (`agent/browser_test.go:45`).

The existing chromedp/CDP stack is sufficient for screenshot-based coordinate input, text/keyboard input, wheel/hover/drag sequences, dialogs and multiple tabs. No framework migration is needed. This supports canvas/WebGL interfaces through visible coordinates, but does not expose their internal objects or guarantee every website accepts automation.

Primary evidence:
- CDP Input: mouse coordinates use main-frame viewport CSS pixels; mouse, keyboard and text APIs are available. Reference `turn957077search0`.
- chromedp MouseClickXY/MouseEvent: reference `turn446992search3`.
- CDP Page geometry/dialog/history: reference `turn204666search0`.
- CDP Target tabs/popups: reference `turn204666search1`.

## Suggested Approval Scope

Phase 1 should expose ordinary browser interaction rather than an unrestricted JavaScript/CDP escape hatch:

1. Keep existing DOM tools for cheap, precise ordinary controls.
2. Add `browser_mouse` with move/click/double_click/right_click/drag/wheel. Use finite, bounded CSS viewport coordinates and bounded event counts. Pointer-targeted wheel supports nested scroll containers. Drag uses move/press/move/release sequences; HTML5 drag-and-drop may need a separately tested fallback, not a blanket compatibility claim.
3. Add `browser_keyboard` with text insertion and validated key/key-combination input. Allow a focused canvas/editor/contenteditable without requiring an input idx. Bound text length and release modifiers/buttons in cleanup on errors.
4. Add `browser_tabs` for list/new/switch/close; preserve single-action serialization. Discover popup targets and return stable owned-tab IDs. Add back/forward/reload using current-tab history.
5. Add dialog inspection/accept/dismiss for alert/confirm/prompt; never auto-accept dialogs. Handle dialog events asynchronously so a blocked page action can still be resolved.
6. Strengthen `browser_screenshot`: return actual image dimensions, CSS viewport dimensions, scroll offsets, device scale, tab ID, URL, capture time and screenshot ID. Fix full-page MIME: pinned chromedp FullScreenshot(70) yields JPEG while browser.go:340 labels it PNG (`turn404769search2`).

For coordinate actions, require the screenshot/tab identity and reject snapshots invalidated by navigation, tab switching, resize or intervening actions. Distinguish viewport from full-page screenshots: full-page document coordinates cannot be passed directly as viewport input. Scroll first and obtain a fresh viewport capture. Page animations or remote updates can still move targets, so refresh and visually verify rather than claiming perfect freshness.

Have ordinary tools return compact structured results and attach screenshots on demand or after uncertain visual actions. Do not attach an image to every key/mouse event. Cap screenshot dimensions and aggregate result size.

## File Operations

Treat upload/download support as a separate bounded phase or clearly included approval item:

- Upload uses DOM.setFileInputFiles (`turn446992search1`). Realpath/symlink-aware checks must restrict automatic uploads to the workspace; outside-workspace paths require the existing administrator approval flow. Sending a local file to a website is data disclosure, not merely a local read.
- Download uses a controlled workspace directory and URL/GUID metadata (`turn446992search0`). Sanitize server filenames, enforce size/count/time limits, verify completion and the resulting file. Never auto-open or execute downloaded files.
- Keep profile/cookies/tokens inaccessible to ordinary browser result tools. Do not expose arbitrary raw CDP, Runtime evaluation, unrestricted local file browsing or native OS dialogs.
- Existing file:// navigation at browser.go:152 needs explicit review against the computer module's workspace/approval boundary.

## Limits and Safety

This should be described as fuller interaction inside Q-Mi's dedicated browser, not unlimited control of the user's desktop or every browser function.

Webpage content remains untrusted data, not an instruction source. Existing persistent-login browser profile (`browser.go:106`) makes account actions real side effects. Preserve approval for destructive, public, payment, credential and sensitive-file actions. Low-level mouse access cannot reliably infer the business meaning of an arbitrary canvas target; do not pretend a selector/URL heuristic proves the action is safe.

Do not promise CAPTCHA bypass, browser-chrome/native dialog control, universal cross-origin DOM access or perfect automation compatibility. Check experimental APIs against the installed Chrome/Edge version; tip-of-tree protocol explicitly has no backward-compatibility guarantee (`turn446992search6`).

## Verification Required Before Shipping

- Pure validation tests: invalid coordinates/NaN/ranges, snapshot/tab identity, action/text limits, modifier cleanup, upload traversal/symlink rejection and download filenames.
- Local browser fixture: canvas records actual pointer events; keyboard-focused canvas/editor; hover menu; right/double click; nested wheel; drag; popup/tab lifecycle; history; JavaScript dialogs.
- Screenshot tests: real dimensions/MIME, CSS-to-bitmap scaling, full-page versus viewport coordinates and invalidated snapshots.
- Verify visible state changes and pending-image attachment, not only a tool's success string.
- Browser integration tests should use temporary isolated profiles and local pages. No user-account browsing is needed.

## User-Facing Recommendation

Approve Phase 1 as coordinate mouse + keyboard/text + nested scrolling/drag + tabs/history/dialogs + screenshot geometry/verification. Keep uploads/downloads bounded by the existing workspace and administrator approval policy. Ask for approval of this scope before implementation.
