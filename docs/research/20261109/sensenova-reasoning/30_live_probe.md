# Authorized Live Probe

Date: 2026-11-09 (Asia/Shanghai)

The user authorized one diagnostic model request using project configuration.
The probe read the merged base/overlay config, used the enabled
`sensenova-6.8-flash` candidate and the project's persona, guide, tool docs and
tool schemas. It supplied a synthetic idle polling input, no chat archive,
no saved notes and no learned style. Returned tools were never executed.
No QQ connection or message sending occurred.

## Observed Response

- HTTP 200; model: `sensenova-6.8-flash`.
- Request size: 59107 bytes.
- `choices[0].message` keys: `role`, `reasoning`, `tool_calls`.
- `reasoning` was a nonempty string.
- `content` and `reasoning_content` were absent.
- Tool call: `finish`, arguments `{}`.
- `finish_reason`: `tool_calls`.
- Usage: prompt 14055, completion 47, total 14102.
- `completion_tokens_details.reasoning_tokens`: 42.
- Request ID: `9532b988ac6b4ad8ac5e25ecc310a53b`.

This confirms the configured Flash deployment uses the `reasoning` field.
This sample did not reproduce self-narration inside `content`; it confirms
missing reasoning display, not that every reported output is misclassified.
No request key or authorization header is stored here.

## Offline Regression

Before the fix, HTTP-fixture tests failed for SenseNova and the null-standard-
field fallback, returning empty reasoning. The loop test also failed because
there was no thinking log entry.

The fix accepts `reasoning` as fallback after `reasoning_content`, without
changing final content, tool calls or automatic plain-content sending.
Focused regression tests then passed.
