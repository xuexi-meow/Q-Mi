# Compatibility Diagnosis

Date: 2026-11-09 (Asia/Shanghai)

## Verified Locally

- Config.webui.yaml selects `sensenova-6.8-flash` at `https://token.sensenova.cn/v1`.
- `agent/llm.go` only decodes `reasoning_content`. A response using `reasoning` is silently omitted from reasoning logs and UI events.
- `agent/manager.go` logs content as output, logs the recognized reasoning field as reasoning, and automatically replies with content when no tool calls are present.
- Initially no local runtime logs or agent/debug response captures were found.
- A later user-authorized single live request confirmed `message.reasoning`
  on the configured `sensenova-6.8-flash` deployment. It had no `content` and
  called `finish`. No QQ operation was issued. See 30_live_probe.md.

## Verified From Persisted Sources

The official OpenSenseNova 6.8 API examples use `message.reasoning` and streaming `delta.reasoning`, separate from final content. See 10_discovery.md and 20_sources.md. Their model examples are Flash Lite, so the exact response from the user's configured Flash endpoint still needs capture.

## Conclusion

There is a verified client compatibility gap for the SenseNova reasoning field,
now confirmed on the user's configured deployment. This explains absent
reasoning display, but cannot prove why other responses' content contains
self-narration. Content must not be blindly reclassified as reasoning.

Implemented after the live probe:

1. Accept both `reasoning_content` and `reasoning`, preferring the former when
   both are nonempty. The recognized reasoning follows the existing log/UI
   path and stays out of final replies and request history.
2. Add offline HTTP/loop regressions, plus an explicitly gated live diagnostic
   test that skips in normal runs.

Not changed: prompt text, thinking-toggle parameters, ordinary content logging,
legacy plain-content automatic sending, model IDs or config credentials.
The user clarified that the reported self-narration was console output, not
QQ messages. Any remaining narration in `content` needs a matching raw sample.
