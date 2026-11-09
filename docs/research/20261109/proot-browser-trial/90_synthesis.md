# PRoot Browser Trial Recommendation

Date: 2026-11-09, Asia/Shanghai.

User: existing Ubuntu PRoot, 8 GB tablet, willing to install Debian.

Recommendation: worth a bounded browser launch/screenshot trial; 8 GB is not a measured guarantee of available RAM or 3D-game performance. Do not remove Ubuntu. Debian is a simpler package candidate because it offers arm64 Chromium directly, whereas Ubuntu Noble chromium-browser is a transition to Snap.

Ask users to create/login to Debian with their existing manager outside Ubuntu; do not assume current proot-distro OCI syntax works on an unknown installed manager version.

Inside Debian, apt update and install chromium, fonts-noto-cjk, ca-certificates. Test --version and a local data-URL screenshot with headless mode before moving Q-Mi. No desktop/VNC is required for the headless smoke test. If sandbox errors appear, inspect them rather than automatically adding --no-sandbox. PRoot is not substitute security isolation.

Moving only the browser still needs the project's currently missing remote-CDP connection path; moving the whole app requires separate compatibility testing. No code or tablet change performed here.

Evidence:

- 20_sources.md: official Debian package and PRoot documentation.
- ../proot-browser-install-ubuntu/20_sources.md: turn744451search1, Ubuntu transitional package.
- ../proot-headless/20_sources.md: Chrome headless, turn785367view0.
