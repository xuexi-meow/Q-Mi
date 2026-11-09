# Browser Pointer Isolation

Based exclusively on `20_sources.md`.

## Verified Findings

1. Pointer Lock is intended to hide/capture the operating system cursor and route relative movement to a page element. This is distinct from ordinary canvas coordinate input. The observed Minecraft symptom is consistent with Pointer Lock, although the actual running page has not been inspected.
2. `document.exitPointerLock()` is the standard release mechanism; requiring it continuously can break games whose camera depends on relative input.
3. CDP exposes mouse and keyboard input dispatch commands. This is a browser input channel, not evidence that a second OS cursor or independent desktop session exists. The current retrieval did not establish a reliable raw relative-motion CDP parameter.
4. Modern headless Chrome runs without visible windows while sharing headful code. It is a reasonable first compatibility experiment, not a documented security/isolation guarantee for every pointer-lock game.

## Recommendations And Limits

- Distinguish "independent browser-directed input" from "independent physical cursor." A page pointer lock can still involve platform behavior.
- Prefer genuine headless browser operation and avoid unconditional foreground activation. Verify against a local pointer-lock fixture before claiming physical input is unaffected.
- If Pointer Lock remains necessary and headless does not work, put the browser on another machine or a VM with browser input supplied through CDP rather than capturing the local client's mouse. This is architectural isolation advice, not an experimentally verified property of this repository.
- Blocking/releasing pointer lock can protect the cursor but may remove Minecraft camera control. Do not advertise it as full game compatibility.
- Do not claim separate Chrome profile, another ordinary window, or Windows virtual desktop supplies independent OS input.

## Sources

- Chrome Headless documentation: https://developer.chrome.com/docs/automation-and-testing/headless
- W3C Pointer Lock: https://w3c.github.io/pointerlock/
- Chrome DevTools Protocol Input index: https://chromedevtools.github.io/devtools-protocol/tot/Input/

No production browser/account was inspected or controlled. No implementation or tests were changed.
