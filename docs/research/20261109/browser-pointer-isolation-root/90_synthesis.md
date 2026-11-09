# Browser Pointer Isolation: Assessment

Date: 2026-11-09, Asia/Shanghai.

## Verified

- Current source defaults the dedicated browser to headless.
- Read-only process inspection confirmed that the running Q-Mi child browser has the headless flag.
- Mouse and keyboard dispatch force Page.bringToFront.
- Chrome's unified Headless creates undisplayed platform windows; it is not a separate desktop.
- Windows ClipCursor operates on a shared cursor.
- W3C Pointer Lock can capture/hide the system cursor and relies on relative mouse movement.

Sources: 20_sources.md and ../browser-pointer-isolation/20_sources.md.

## Unconfirmed

The actual game, its pointer-lock state, foreground changes, and Windows confinement rectangle were not inspected. No live input or local pointer-lock reproduction was performed. Pointer lock is a plausible incident explanation, not a verified root cause.

## Proposed Options

Local hardening is a bounded possible fix: test replacement of unnecessary BringToFront with renderer-focus emulation, add pointer-lock diagnostics and explicit release, and consider an opt-in policy for native capture. Rejecting native pointer lock may prevent a 3D game's normal camera control. Merely drawing another cursor or separating Chrome profiles does not provide OS isolation.

For stronger isolation while retaining native game input, run the browser inside a separate VM or remote system and control it using CDP, not a host-capturing interactive console. GPU/WebGL compatibility and remote endpoint security require validation. A Windows virtual desktop or headless flag alone is not that boundary.

The input tools' ability to click a canvas does not establish support for indefinite held keys or raw relative camera movement. Do not promise full game compatibility before testing.

## Status

Investigation and design advice only; no production code, running browser, account content, mouse state, or model configuration changed. The approved offline-learning design remains queued behind this issue at the user's request.
