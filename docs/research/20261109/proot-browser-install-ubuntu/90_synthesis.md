# Ubuntu PRoot Chromium Install Caveat

Based on `20_sources.md`.

Ubuntu Noble's `chromium-browser` package is a transition to Chromium Snap, not a standalone browser .deb, and requires snapd. Therefore do not recommend a blind `apt install chromium-browser` as the expected working PRoot browser installation path.

Combined with the already persisted PRoot limitations at `../termux-browser/20_sources.md` (no real root/namespaces and generally no full init), the practical recommendation is to prefer a distro that offers a native compatible Chromium .deb, or separately verify another browser packaging route. This is an installation recommendation, not a claim that every Snap attempt is logically impossible.

No installation, package modification, or remote-device access occurred.
