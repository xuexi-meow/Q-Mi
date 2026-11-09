# PRoot Browser Trial Sources

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://github.com/termux/proot-distro
**Method**: extract (web.run open)
**Confidence**: high
**Insight**: Official PRoot-Distro README provides installation/login commands and limitations; verify the relevant sections before suggesting command syntax.

# Relevant extracted content

- Citation: turn122651view0.
- Initial returned viewport contains repository navigation.
- Do not use navigation text to assert installation syntax.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://github.com/termux/proot-distro
**Method**: extract (web.run find install/login)
**Confidence**: high
**Insight**: Current documented PRoot-Distro accepts OCI image references such as debian:bookworm. Installed versions may differ; avoid assuming the user uses this manager or current syntax.

# Relevant extracted content

- Usable citations: turn709220view0 and turn709220view1.
- Lines 383-385: install ubuntu:24.04 or debian:bookworm; architecture option available.
- Lines 583-599: login command; defaults to root, non-root user option supported.
- Lines 286-287: nested PRoot is not supported; run container-manager commands in Termux, not inside Ubuntu.
- Recommendation: do not delete or replace existing Ubuntu.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://packages.debian.org/trixie/chromium
**Method**: extract (web.run open)
**Confidence**: high
**Insight**: Debian provides a conventional Chromium browser package with ARM64 support. Recommend it as an optional parallel trial rather than removing the existing Ubuntu environment.

# Relevant extracted content

- Citation: turn122651view1.
- Package chromium is the browser component and lists arm64 builds.
- Package availability does not establish sandbox, GPU, game performance, or memory sufficiency on the user's tablet.

---
