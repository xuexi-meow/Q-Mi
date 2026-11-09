# Browser Pointer Isolation: Root Investigation

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://developer.chrome.com/docs/automation-and-testing/headless
**Method**: extract (web.run open)
**Confidence**: high
**Insight**: Current Chrome Headless uses unified browser code and creates platform windows without displaying them. Headless should not be described as a separate OS desktop or a guarantee against every native input interaction.

# Relevant extracted content

- Verified citation: turn320438view0.
- Lines 61-62: unattended, no visible UI; Chrome 112 introduced creating undisplayed platform windows.
- Lines 81-88: unified Headless/headful implementation; legacy headless is a separate shell since Chrome 132.
- Source does not establish that pointer lock caused the user's incident or guarantee a proposed fix.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-clipcursor
**Method**: extract (web.run open)
**Confidence**: high
**Insight**: Windows cursor clipping is a shared-resource operation, not a separate per-application cursor. Adding a browser profile or a rendered cursor alone is not OS input isolation.

# Relevant extracted content

- Verified citation: turn619951view0.
- Lines 34-36: ClipCursor confines the cursor to a screen rectangle and adjusts subsequent SetCursorPos movements.
- Lines 60-62: cursor is a shared resource; applications must release confinement when relinquishing control.
- No ClipCursor or other native-input function was invoked during this investigation.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: Chromium pointer-lock and CDP discovery results
**Method**: query-search (web.run)
**Confidence**: low
**Insight**: Chromium's Windows mouse-lock implementation is a candidate explanation because it calls the native cursor-clipping API; focus emulation is a candidate alternative to bringing a page forward. Neither establishes an incident-level diagnosis.

# Relevant extracted content

- turn891322search0: desktop_window_tree_host_win.cc implements mouse locking using ClipCursor.
- turn891322search1: Emulation.setFocusEmulationEnabled simulates a focused active page.
- turn891322search4: Chrome pointer-lock permission documentation explains 3D looking input.
- turn891322search10: Chromium Mouse Lock design documentation describes persistence, hidden cursor, and explicit release.
- Only primary Chromium/Chrome/CDP documentation will be used; unrelated search results are not evidence.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: local code and read-only process inspection, F:/Go/Q-Mi
**Method**: extract (PowerShell Get-Content, rg, elevated Get-CimInstance after permission denial)
**Confidence**: high
**Insight**: The running dedicated browser is actually headless. Input actions are sent through CDP, but force BringToFront. The exact native cursor-confinement cause remains unconfirmed.

# Relevant extracted content

- agent/browser.go:386 creates a manager with headless true.
- agent/browser_input.go:111 and :215 unconditionally call Page.bringToFront before mouse/keyboard input.
- Q-Mi PID 25844, started 2026-11-09 20:23:03; its direct browser child PID 15740 has headless and dedicated-profile flags.
- Child subprocesses need not repeat the main process's headless argument; their missing flag is not evidence that the browser is headed.
- No browser page, screenshot, logged-in account content, or OS pointer was accessed or controlled during this inspection.
- No production code or active browser session was changed.

---
