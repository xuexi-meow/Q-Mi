# Verified Managed Fine-Tuning Sources

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://help.aliyun.com/zh/model-studio/model-training-and-deployment-billing
**Method**: extract (web.run open; turn830600view0)
**Confidence**: high
**Insight**: Actual visible output verifies Qwen3-8B training costs CNY 0.006/1000 tokens, separately from deployment. A 1M-token dataset trained for three epochs costs CNY 18 before any mixing or other services.

# Relevant Extracted Content

- L35: trained model requires deployment before evaluation/calling.
- L38: total = (training tokens + mixed-training tokens) x epochs x training unit price.
- L64: Qwen3-32B CNY 0.04/1000 tokens.
- L67: Qwen3-8B CNY 0.006/1000 tokens.
- L74: Qwen2.5-7B-Instruct CNY 0.006/1000 tokens.
- CNY 18 is derived arithmetic: 1,000,000 / 1000 x 0.006 x 3; not an all-in training project guarantee.
- Word limit 200.

---

This file supersedes unverified reference identifiers in 20_sources.md. The initial web.run tool requests produced empty outputs. Only sources recorded below have visible results.

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://docs.bailian.console.aliyun.com/zh/model-studio/model-training-overview ; https://help.aliyun.com/zh/model-studio/qwen3-8b ; https://help.aliyun.cn/zh/model-studio/model-deployment-token
**Method**: query-search (web.run; turn828536search0, turn828536search1, turn828536search10)
**Confidence**: high
**Insight**: Official discovery confirms inexpensive managed Qwen3-8B training and eligible token-metered LoRA deployment; the latter is essential to avoid GPU-hour serving charges.

# Relevant Extracted Content

- Official overview training formula: (training dataset tokens + mixed-training tokens) x epochs x training unit price.
- Qwen3-8B training CNY 0.006 per thousand tokens.
- Qwen3-32B training CNY 0.04 per thousand tokens.
- High-efficiency mode is LoRA, intended for particular scenarios and cost/time-sensitive training.
- Base Qwen3-8B Beijing inference prices: input CNY 0.5 per million tokens; output CNY 2 per million non-thinking or CNY 5 per million thinking. These base-model prices must not be assumed to cover fine-tuned serving until deployment table is read.
- Official LoRA deployment page explicitly says only LoRA fine-tunes supported, usage-metered, no usage means no charge, suited to evaluation or low-cost moderate-concurrency/latency scenarios.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://help.aliyun.cn/zh/model-studio/model-deployment-token
**Method**: extract (web.run open with response_length short; turn128585view0)
**Confidence**: high
**Insight**: Official table directly confirms custom LoRA models' token-based serving prices in Beijing, not just base model prices. This provides a genuinely cheap end-to-end experiment without an always-on GPU.

# Relevant Extracted Content

- Lines 29-41: only eligible SFT high-efficiency LoRA models qualify; no usage means no cost; concurrency/generation speed platform-controlled; API plan=lora.
- Beijing Qwen3-8B input CNY 0.5/million tokens; output non-thinking CNY 2/million, thinking CNY 5/million.
- Beijing Qwen3-32B input CNY 2/million; output non-thinking CNY 8/million, thinking CNY 20/million.
- Beijing Qwen2.5-7B input CNY 0.5/million; output CNY 1/million.
- Singapore prices materially differ. Do not mix region quotes.
- Idle for one month automatically releases token-metered deployment.
- This document has wordlim 200: summarize sparingly.

---
