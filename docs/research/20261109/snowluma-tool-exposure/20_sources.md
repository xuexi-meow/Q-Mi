# SnowLuma API Exposure Evidence

**Time**: 2026-11-09, Asia/Shanghai
**Source**: local:agent/napcat_catalog.json; local:agent/napcat_gateway_test.go;
local:api/actions_group.go; local:api/actions_friend.go
**Method**: extract (JSON parsing, code inspection, Go tests and race/vet)
**Confidence**: high
**Insight**: The hybrid interface is implemented and verified locally. The
official source snapshot provides 222 canonical actions, while local policy
separately governs execution.

# Relevant extracted content

- Embedded source metadata revision:
  f2ea3354f8bbe8672cbb4ae6a2b4e1fbeff44faf.
- Regression tests observed missing authorization, lost false/sub_type and
  missing outgoing checks before fixes; they now pass.
- Full suite: go test ./... -count=1, exit 0.
- Focused race suite repeated 3 times: exit 0.
- go vet ./...: exit 0.
- No real account calls; no running process was restarted.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://raw.githubusercontent.com/SnowLuma/SnowLuma/main/packages/mcp/src/generated/catalog.json
**Method**: extract (curl to official-catalog.json, JSON inspection)
**Confidence**: high
**Insight**: The official MCP generated catalog supplies machine-readable
action names, aliases, summaries, categories, input/return schemas and examples.
It can back local discovery without inflating the permanent prompt.

# Relevant extracted content

- Raw artifact: official-catalog.json, approximately 584 KB.
- Root actions array; entries include name, aliases, summary, category,
  inputSchema, params, returnsSchema, examples, invariants, readOnly.
- First action _del_group_notice requires positive integer group_id; fid
  and notice_id are optional schema fields with a documented either/or rule.
- inputSchema.additionalProperties can be true; gateway validation should
  reject undocumented top-level keys intentionally to avoid silent typos.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://api.github.com/repos/SnowLuma/SnowLuma/git/trees/main?recursive=1
**Method**: extract (curl to source-tree.json)
**Confidence**: high
**Insight**: Official repository tree retrieved for finding generated schemas,
without downloading or executing the remote project.

# Relevant extracted content

- Raw Git tree response persisted as source-tree.json.
- Discovery only; individual file semantics are not inferred yet.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://snowluma.github.io/zh/docs/api
**Method**: extract (curl downloaded api-index.html, regex link extraction)
**Confidence**: high
**Insight**: The official index was saved successfully as raw HTML. It links
to the official SnowLuma GitHub repository; OpenAPI download is not an href.

# Relevant extracted content

- Raw artifact: api-index.html, approximately 383 KB.
- Official source repository: https://github.com/SnowLuma/SnowLuma
- JavaScript chunks under /_next/static/chunks/ are available if needed.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://snowluma.github.io/zh/docs/api
**Method**: extract (web.run open on continuation)
**Confidence**: low
**Insight**: This tool again returned an empty payload. No conclusions about
remote content follow from this attempt.

# Relevant extracted content

- Empty tool response; curl retrieval required.

---

## Evidence Correction

The web.run calls in this session returned empty tool payloads. Earlier entries
describing parsed web content or a named web reference must not be used as
verified citation evidence. A subsequent successful curl fetch below is the
actual primary-source retrieval. Failed navigation was an empty-ref open,
not a resolved click. Keep this correction explicit for future reuse.

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://snowluma.github.io/zh/docs/api
**Method**: extract (Invoke-WebRequest fallback)
**Confidence**: low
**Insight**: The PowerShell fetch failed due to a closed TLS connection. No further remote content was obtained.

# Relevant extracted content

- Invoke-WebRequest: underlying connection closed while receiving.
- Use only the successful curl retrieval and verified local code for conclusions.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: local:agent/tools.go:1993-2044; local:agent/tools.go:2323-2426
**Method**: extract (Get-Content; checked parent-side after worker audit)
**Confidence**: high
**Insight**: The escape hatch accepts only registered action types but invokes them through a generic path. Documentation discovery and enforcement are separate problems that both need work.

# Relevant extracted content

- Unknown action names are rejected using actionRegistry.
- Non-admin denial checks dangerousNapcatActions only.
- Parameters are unmarshaled into registered request structs.
- Calls then go straight to callNapCat/passthrough.
- napcat_docs has no parameters and returns static napcatActionsJSON.
- Registered types include SetGroupAdmin, GetCookies, GetCredentials,
  SendPacket, BotExit, SetGroupKickMembers and GetClientKey.
- Audit source locations: tools.go:1172 risk map, :1617 wrapper admin check,
  :1097 outgoing validation, toolkit.go:85 prompt documentation generation.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://snowluma.github.io/zh/docs/api
**Method**: extract (web.run open; rendered-page CLI not available in this toolset)
**Confidence**: high
**Insight**: The official index mixes ordinary messaging and browsing with sensitive credentials, raw protocol operations, destructive management and explicitly unsupported actions. Exposing the entire index as executable tools without classification would be inappropriate.

# Relevant extracted content

- HTTP is compatible with OneBot v11: POST /{action}, JSON body, optional bearer token.
- Success requires retcode=0, not merely HTTP 200.
- Categories include extensions, group management, information, albums, streaming, Qzone, group files, friends, messages, system emoji, group information and requests.
- The index explicitly lists credential/cookie/clientkey access, database decryption-key requests and raw SSO packets.
- Some entries are labeled unimplemented, placeholder, unsupported, or awaiting real-device verification.
- The site links to MCP and SDK integrations.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://snowluma.github.io/zh/docs/api
**Method**: extract (curl.exe --location --max-time 25 --fail)
**Confidence**: high
**Insight**: Successfully fetched official server-rendered HTML. The title identifies the page as an API reference generated from a runtime action catalog, with OpenAPI parameters, responses, examples and testing.

# Relevant extracted content

- HTML title: API reference.
- Meta description: runtime-action-catalog generated OpenAPI, parameters,
  responses, examples and trial requests.
- Links in retrieved HTML include Qzone public-feed operations, group-file
  upload and private-file upload action details.
- Output was large and truncated; do not infer exact counts from this fetch.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: local:agent/tools.go; local:agent/toolkit.go; local:agent/napcat_actions.json
**Method**: extract (rg/Get-Content and read-only audit worker)
**Confidence**: high
**Insight**: The existing hybrid design is a useful starting point, but its documentation and policy need strengthening before adding more actions.

# Relevant extracted content

- PromptDocs at toolkit.go:85 generates detailed docs injected into system.
- napcat_docs at tools.go:2037 returns a static common-action JSON, without
  query/action filtering; napcat_actions.json still refers to NapCat/apifox.
- napcat_api checks registered action types, but only seven actions appear
  in dangerousNapcatActions. Audit identified sensitive registered actions
  and generic send paths needing shared authorization/output validation.
- Check exact implementation before classifying any action as unrestricted.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://snowluma.github.io/zh/docs/api (navigation attempt)
**Method**: extract (web.run click)
**Confidence**: low
**Insight**: Navigation failed with invalid arguments; do not treat the attempted subpage as fetched evidence.

# Relevant extracted content

- Tool error: unable to resolve click call due to invalid arguments.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://snowluma.github.io/zh/docs/api
**Method**: extract (web.run open, reference turn978235view0)
**Confidence**: high
**Insight**: A second retrieval exposed navigable action links and confirms the category counts and sensitive/unsupported examples in the first checkpoint.

# Relevant extracted content

- Category counts: 115, 24, 5, 9, 6, 29, 12, 3, 6, 4, 7, 2.
- get_credentials, get_cookies, clientkey retrieval, database-key request and raw SSO sending appear alongside ordinary chat actions.
- Placeholder/unimplemented entries cannot be taken as reliable executable capabilities merely because they appear in the index.
- Next evidence targets: MCP integration, credential access and individual action parameter contracts.

---
