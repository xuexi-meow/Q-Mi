# Browser Pointer Isolation Sources

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://developer.chrome.com/docs/automation-and-testing/headless
**Method**: extract (web.run open; browser scraping unavailable to this worker)
**Confidence**: high
**Insight**: Modern headless Chrome creates platform windows without displaying them and shares the headful implementation. This supports trying headless operation, but this documentation does not guarantee that every pointer-lock game works without affecting platform input.

# Relevant extracted content

- Headless runs without visible UI.
- From Chrome 112, platform windows exist but are not displayed.
- Modern headless and headful Chrome share browser code.
- Headless is enabled through the headless command-line option.
- Citation: turn206067view0.

---

**Time**: 2026-11-09 Asia/Shanghai
**Source**: CDP Input.dispatchMouseEvent link from https://chromedevtools.github.io/devtools-protocol/tot/Input/
**Method**: extract (web.run click)
**Confidence**: low
**Insight**: The follow-up command-definition fetch failed; no unsupported claim about protocol movementX support is inferred from this failure.

# Relevant extracted content

- Tool returned invalid click arguments.
- Citation: turn633656view0.

---

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://chromedevtools.github.io/devtools-protocol/tot/Input/#method-dispatchMouseEvent
**Method**: extract (web.run open)
**Confidence**: high
**Insight**: The legacy domain redirects to a compact navigation index rather than the full command definition; the command hyperlink must be followed before drawing parameter conclusions.

# Relevant extracted content

- Input.dispatchMouseEvent appears as link 5.
- Citation: turn209238view0.

---

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://w3c.github.io/pointerlock/
**Method**: extract (web.run open)
**Confidence**: high
**Insight**: Pointer Lock is explicitly allowed to hide and constrain the system cursor using operating-system APIs; it is not a separately allocated virtual mouse. Relative movement and ordinary absolute-coordinate clicks are distinct input requirements.

# Relevant extracted content

- Lines 7-20: pointer lock routes mouse input to one element, hides the system cursor, uses platform APIs, and captures a single resource.
- Lines 73-83: focus and transient activation affect whether requestPointerLock succeeds.
- Lines 119-142: document.exitPointerLock releases the lock.
- Lines 171-182: movementX and movementY encode relative movement.
- Citation: turn546321view0.

---

**Time**: 2026-11-09 Asia/Shanghai
**Source**: https://chromedevtools.github.io/devtools-protocol/tot/Input/
**Method**: extract (web.run open)
**Confidence**: high
**Insight**: The official CDP Input index exposes renderer input commands. Follow the dispatchMouseEvent anchor for coordinate and relative-motion details.

# Relevant extracted content

- Input.dispatchMouseEvent is an official protocol command.
- Input.dispatchKeyEvent and Input.insertText are separate protocol commands.
- Citation: turn990682view0.

---
