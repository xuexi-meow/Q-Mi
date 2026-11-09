# Termux PRoot Browser Feasibility

Based exclusively on `20_sources.md`.

## Relevant To Existing PRoot Deployment

- PRoot-Distro can run Linux userland on Android. Installing a compatible browser inside an already-working distro is a reasonable feasibility experiment, not categorically impossible.
- PRoot has system-call overhead and does not supply real root or kernel namespaces. It is not a VM or strong security boundary.
- Individual browser/agent processes may run long-term; full systemd/OpenRC supervision generally does not work.
- A no-display browser mode is conceptually the right direction, but neither cited repository guarantees compatibility of the particular Chromium build, headless WebGL, or the Minecraft page.
- GPU acceleration, Android background-process lifetime, and whether Chromium requires disabling its sandbox remain unverified in these two sources. Do not assert a guaranteed GPU path, a universal required flag, or background survival.
- If a local launch reports sandbox failure, diagnose the exact error. Do not silently disable the browser sandbox or describe PRoot as replacing it.

## Secondary Finding

The official Termux package repository also maintains a native Chromium recipe in x11-packages. This is an alternative, not a requirement for the user's existing PRoot environment.

## Sources

- https://github.com/termux/proot-distro
- https://raw.githubusercontent.com/termux/termux-packages/master/x11-packages/chromium/build.sh

No tablet access, installation, production browser interaction, implementation, or tests occurred.
