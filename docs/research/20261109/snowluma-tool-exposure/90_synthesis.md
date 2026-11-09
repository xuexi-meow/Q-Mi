# Recommendation

Use a hybrid interface, not one tool per API and not a complete API reference
in the permanent prompt. This recommendation is engineering judgment based
on the verified local implementation and persisted official catalog retrieval
in 20_sources.md. web.run produced no citable payload; curl successfully
retrieved the official generated MCP catalog for implementation.

- Keep high-frequency, behavior-rich wrappers for messages, research and
  account activity. They need readable arguments and reliable result handling.
- Turn napcat_docs into searchable/paged discovery with per-action exact
  parameter schemas, examples, availability and risk metadata.
- Keep only a short capability directory in the permanent prompt. All API
  details should be retrieved when needed.
- Use one execution gateway for registered long-tail actions, with explicit
  validation and identical permissions/output checks to wrappers.
- Classify new actions before enabling them. Credentials, raw packets,
  destructive actions and account shutdown should not gain autonomous
  availability merely through registration.
- First close current escape-hatch policy gaps: generic send paths can bypass
  wrapper text checks, and the dangerous-action map is narrower than the
  registered sensitive action set.

## Implementation

- Imported the official generated catalog: 222 canonical actions and its
  aliases, source metadata pinned to commit
  f2ea3354f8bbe8672cbb4ae6a2b4e1fbeff44faf.
- napcat_docs now supports search, category filtering, bounded paging and
  per-action schemas/examples/results/invariants/policy.
- napcat_api validates JSON Schema, preserves false/zero and opaque nested
  fields, normalizes aliases and uses explicit local allowed/admin/disabled
  policies. New actions are disabled by default.
- Shared authored-content checks cover generic sends, nested forward nodes,
  Qzone posts/comments, announcement content and AI voice text.
- Group name/announcement wrappers now share administrator authorization.
  Fixed false-value omission in request approval, whole-ban and admin removal.
- Added a reproducible catalog updater and docs/napcat-api.md. Catalog loading
  does not fetch anything remotely at runtime.

## Verification

- New integration tests first reproduced permission bypass, outgoing-prefix
  bypass, missing discovery, and dropped explicit false/zero/sub_type.
- go test ./... -count=1 passed.
- go test -race ./agent -run 'TestNapcat|TestDangerousTools|TestSendMessageRejects|TestCompaction|TestContext|TestLive' -count=3 passed.
- go vet ./... passed; git diff --check passed with existing CRLF notices.
- No real QQ/API/model calls or process restart were performed. Upstream
  prose invariants and deployment-version differences still require the
  running server's validation.
