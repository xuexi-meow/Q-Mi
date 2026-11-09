# Headless In Existing Tablet PRoot: Assessment

Date: 2026-11-09, Asia/Shanghai.

## Recommendation

A headless browser on no-monitor Linux is a supported browser mode; physical monitor and full desktop are not prerequisites. The user's existing PRoot distro is a reasonable trial environment, not a guaranteed compatible device.

Debian offers arm64 Chromium. Avoid prescribing desktop x86 Chrome binaries or assuming all distro Chromium packages are equivalent. Ask for installed distro/version, architecture, and RAM before installation instructions.

PRoot has system-call tracing overhead, no real root or kernel namespace isolation. It is not a VM or browser sandbox. Do not prescribe no-sandbox unconditionally.

Separate milestones: browser launches, normal page renders, screenshot works, WebGL works, specific 3D game and inputs work, long-running process remains available. Passing the early milestones does not prove the later ones.

## Project Boundary

Current Q-Mi starts a local browser with NewExecAllocator, discovers Linux executables in PATH, and supports browser_executable. Running browser and application together inside PRoot fits that design, subject to build and runtime verification.

Moving only the browser to the tablet while leaving Q-Mi on Windows requires a remote CDP connection feature not currently exposed by this code. If implemented later, use authenticated/encrypted/private access; do not publicly expose a debugging port.

## Evidence

- 20_sources.md: turn785367view0, Chrome headless; turn185019view0, Debian arm64 package; local code inspection.
- ../termux-browser/20_sources.md: turn111646view0, PRoot limitations.

No application changes, install, build, tablet access, or runtime compatibility test occurred. Browser bugfix and approved offline-learning implementation remain deferred.
