# Headless Linux And PRoot: Sources

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://developer.chrome.com/docs/automation-and-testing/headless
**Method**: extract (web.run direct open)
**Confidence**: high
**Insight**: Chrome's headless mode runs without visible UI and supports screenshots and remote debugging. Lack of a physical monitor is not itself a reason to require a desktop or X/VNC server.

# Relevant extracted content

- Verified citation: turn785367view0.
- Lines 61-66: headless unattended environment, undisplayed platform windows, enabled via headless flag.
- Lines 103-114: screenshot capture.
- Lines 183-193: remote debugging over WebSocket in headless mode.
- This source does not verify Android PRoot, Linux ARM package availability, WebGL acceleration, or game compatibility.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://packages.debian.org/trixie/chromium
**Method**: extract (web.run direct open)
**Confidence**: high
**Insight**: Debian provides a native arm64 Chromium package; ARM tablets do not intrinsically require x86 emulation. Package installation alone does not establish PRoot runtime compatibility.

# Relevant extracted content

- Verified citation: turn185019view0.
- Lines 181-188: amd64, arm64, armhf and other package architectures are listed.
- Package has supporting graphics/system library dependencies and recommends chromium-sandbox.
- Do not infer that Ubuntu's Chromium package packaging matches Debian.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: F:/Go/Q-Mi/agent/browser.go; F:/Go/Q-Mi/utils/config/config.go
**Method**: extract (PowerShell Get-Content and rg)
**Confidence**: high
**Insight**: Current browser manager supports local Linux executable discovery and an explicit executable path, but no remote CDP endpoint option is present.

# Relevant extracted content

- browser.go searches PATH for chromium, google-chrome, chromium-browser, microsoft-edge on non-Windows.
- newBrowserManager defaults headless to true.
- config agent.browser_executable is a local executable path.
- Browser manager uses chromedp.NewExecAllocator, not a remote allocator.
- Therefore moving only the browser to a tablet while retaining Q-Mi on Windows requires an additional remote-browser connection feature; a configuration path alone is insufficient.
- No build, tablet execution, runtime compatibility test, or application modification was performed.

---
