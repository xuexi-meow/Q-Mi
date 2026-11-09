# Termux Browser Sources

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://github.com/termux/proot-distro#limitations
**Method**: extract (web.run find URL, pattern Limitations)
**Confidence**: high
**Insight**: PRoot translates paths and traces system calls; it supplies neither real root nor kernel namespaces. Browser kernel-sandbox and GPU compatibility must be verified separately, and PRoot is not a substitute for VM security isolation.

# Relevant extracted content

- Lines 1166-1173: ptrace overhead; fake root; no cgroups/namespaces; individual long-running processes can run, full systemd/OpenRC supervision generally cannot.
- Lines 1176-1177: no PID/network/IPC isolation or seccomp for image build execution.
- Lines 1180-1181: Android blocks some proc data; backups do not preserve running-process memory.
- No source statement located guaranteeing Chromium GPU/WebGL acceleration or defining Android background killing policy.
- Citation: turn111646view0.

---

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://raw.githubusercontent.com/termux/termux-packages/master/x11-packages/chromium/build.sh
**Method**: extract (web.run open, response_length short)
**Confidence**: high
**Insight**: Termux maintains a native Chromium recipe in x11-packages; native Termux Chromium is a real candidate, not categorically impossible. The build recipe alone does not confirm the user's mirror, installed device compatibility, WebGL performance, or headless game behavior.

# Relevant extracted content

- Description is Chromium web browser.
- Depends on gtk3, libx11, mesa and other Linux/browser libraries.
- Excludes i686; contains aarch64, arm, x86_64 architecture branches.
- On-device package building is marked unsupported; this does not mean installing a prebuilt package is unsupported.
- Citation: turn977695view0.
- Master branch is moving; version string is deliberately not used as a dated release claim.

---

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://github.com/termux/proot-distro
**Method**: extract (web.run open at line 280)
**Confidence**: high
**Insight**: Direct URL reopening yields valid handle turn389015view0. README explicitly describes Linux userland on Android, without a separate kernel module.

# Relevant extracted content

- Lines 195-198: chroot-like rootless environment.
- Lines 242-250: Linux userland on Android, same OCI images used on servers.
- Lines 286-287: nested proot unsupported.
- Lines 339-340: native architecture choices include aarch64 and arm.
- Citation: turn389015view0.

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://github.com/termux/proot-distro
**Method**: extract (web.run open, response_length short)
**Confidence**: high
**Insight**: PRoot-Distro is a rootless Linux userland environment, not a VM or Docker kernel-isolated sandbox. Its limitations and GPU/background details need the limitations section before making stronger claims.

# Relevant extracted content

- Lines 195-198: rootless proot containers provide a chroot-like environment without root.
- Lines 242-250: Linux userland can run on Android without kernel modules or Docker daemon.
- Lines 255-262: available from Termux package manager; proot dependency installed automatically.
- Initial source handle could not be resolved on a subsequent find; re-fetch confirmed the source as turn706406view0.

---

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://github.com/termux/proot-distro
**Method**: extract (web.run re-open after unresolved source handle)
**Confidence**: high
**Insight**: Repository README fetched successfully; proceed to its limitations section. No stronger GPU or sandbox conclusion is asserted from the unrelated viewport excerpt.

# Relevant extracted content

- Verified page title: A utility for managing proot containers.
- Citation: turn706406view0.

---
