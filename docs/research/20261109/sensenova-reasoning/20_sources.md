# Source Checkpoints

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://github.com/OpenSenseNova/SenseNova6.8/blob/main/API_CN.md
**Method**: extract (web.run open; no rendered browser backend used)
**Confidence**: high for documented Lite response, provisional for configured Flash.
**Insight**: The official example explicitly separates final `content` from `reasoning`. The local client only captures `reasoning_content`; this confirms an interoperability gap but does not prove the exact shape returned by the user's Flash deployment.

# Relevant Extracted Content

- Section 4.3 response: `message.content` is final answer, `message.reasoning` is reasoning.
- The documented model is `sensenova-6.8-flash-lite`, not the configured `sensenova-6.8-flash`.
- Section 6: replay content only; do not replay reasoning.
- No documented thinking-toggle request field was found in the inspected example.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://www.sensetime.com/cn/news/sensenova-6-8-flash-release-20261103-1421
**Method**: extract (web.run open)
**Confidence**: high
**Insight**: The official announcement confirms Flash availability and links to the platform docs. Marketing descriptions do not establish response fields.

# Relevant Extracted Content

- Page title: SenseNova 6.8 Flash formal release.
- Links to https://platform.sensenova.cn/docs for the Token Plan.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://platform.sensenova.cn/docs
**Method**: extract (web.run open)
**Confidence**: low
**Insight**: The platform documentation timed out, so Flash-specific reasoning toggles and return fields are not verified from this page.

# Relevant Extracted Content

- Fetch failed with timeout.

---
