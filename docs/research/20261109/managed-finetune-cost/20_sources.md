# Managed Fine-Tuning Cost Sources

The complete verified checkpoints are in findings_fallback.md, used when a checkpoint replacement failed. This index records only visibly verified source identifiers. Initial empty-response tool attempts were discarded.

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://help.aliyun.com/zh/model-studio/model-training-and-deployment-billing
**Method**: extract (web.run; turn830600view0)
**Confidence**: high
**Insight**: Qwen3-8B managed training is token-billed. Training price is separate from inference.

# Relevant Extracted Content

- Training cost = (training tokens + mixed-training tokens) x epochs x unit price.
- Qwen3-8B: CNY 0.006/1000 tokens.
- Qwen3-32B: CNY 0.04/1000 tokens.
- Example (derived): 1M dataset tokens x 3 epochs = CNY 18 for Qwen3-8B, no mixed data.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://help.aliyun.cn/zh/model-studio/model-deployment-token
**Method**: extract (web.run; turn128585view0)
**Confidence**: high
**Insight**: Supported SFT-LoRA fine-tunes can be deployed with usage-metered inference, no always-on GPU bill.

# Relevant Extracted Content

- Requires eligible base model + SFT high-efficiency LoRA; API deployment plan=lora.
- Beijing Qwen3-8B: CNY 0.5/million input tokens; CNY 2/million non-thinking output or CNY 5/million thinking output.
- Beijing Qwen3-32B: CNY 2/million input; CNY 8/million non-thinking output or CNY 20/million thinking output.
- No usage, no fee. Idle for one month releases deployment.
- Singapore region pricing differs; verify selected region and final console quote before submitting.
- Throughput/concurrency controlled by platform, less configurable than dedicated deployment.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://docs.bailian.console.aliyun.com/zh/model-studio/model-training-overview
**Method**: query-search (web.run; turn828536search0)
**Confidence**: high
**Insight**: LoRA is explicitly presented as efficient training for focused scenarios and cost/time constraints.

# Relevant Extracted Content

- High-efficiency training means LoRA.
- Relevant for specific task improvements and cost/time sensitivity.
- Managed console provides model tuning and deployment workflow; no local GPU required.

---
