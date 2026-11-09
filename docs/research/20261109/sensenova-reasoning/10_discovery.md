# SenseNova Reasoning Compatibility

**Time**: 2026-11-09, Asia/Shanghai
**Source**: web.run search: site.sensenova.cn "sensenova-6.8-flash" thinking; site.sensenova.cn "6.8" "reasoning_content"
**Method**: query-search (web.run)
**Confidence**: high for the official repository example, provisional for Flash-specific behavior.
**Insight**: The official OpenSenseNova API documentation uses message.reasoning rather than message.reasoning_content. The search found a Flash release page pointing to the platform documentation; the GitHub API example currently describes Flash Lite and must not be assumed to fully specify Flash.

# Relevant Extracted Content

- Official API source: https://github.com/OpenSenseNova/SenseNova6.8/blob/main/API_CN.md
- Response example separates `content` and `reasoning`.
- Official release: https://www.sensetime.com/cn/news/sensenova-6-8-flash-release-20261103-1421
- Platform documentation: https://platform.sensenova.cn/docs
- Third-party issues were discovered but are not evidence for the project's actual response shape.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: web.run search: "token.sensenova.cn" "reasoning" "6.8"; "sensenova-6.8-flash" "thinking"; "sensenova-6.8-flash" "enable_thinking"
**Method**: query-search (web.run)
**Confidence**: high for official Lite examples; unresolved for Flash-specific toggles.
**Insight**: Official Chinese and English API examples both name `reasoning`. Third-party pages discuss different deployed models and settings; they are not relied on to infer Flash configuration.

# Relevant Extracted Content

- https://github.com/OpenSenseNova/SenseNova6.8/blob/main/API_CN.md documents streaming `delta.reasoning` separately from `delta.content`.
- https://github.com/OpenSenseNova/SenseNova6.8/blob/main/API.md documents non-streaming `message.reasoning` separately from `message.content`.
- These examples identify `sensenova-6.8-flash-lite`; no verified Flash-specific thinking flag was found.

---
