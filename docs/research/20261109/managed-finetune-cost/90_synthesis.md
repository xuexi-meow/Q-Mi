# Managed Fine-Tuning: Practical Recommendation

Date: 2026-11-09 (Asia/Shanghai).
Scope: official Alibaba Model Studio documentation only; no accounts, purchases, or model calls.

## Recommendation

For a first chat-style experiment with no local GPU, Alibaba Bailian Qwen3-8B + supervised fine-tuning (SFT) + high-efficiency LoRA + usage-metered LoRA deployment is a practical low-cost managed path. Do not select dedicated GPU/hourly serving by accident. Check account/region/model availability and the final console price before starting.

The training itself can be inexpensive: official Qwen3-8B pricing is CNY 0.006 per 1000 training tokens. The multiplier is the actual dataset token count, mixed data if enabled, and number of epochs. For a 1M-token dataset and 3 epochs, the derived training-only cost is CNY 18, without mixing. This excludes data preparation, evaluation calls, failed experiments, and inference.

Official Beijing usage-metered custom LoRA deployment prices for Qwen3-8B are CNY 0.5/million input tokens, CNY 2/million non-thinking output tokens or CNY 5/million thinking output tokens. These prices apply only to eligible LoRA deployments, not arbitrary full fine-tuning. No usage means no inference fee. Regions differ.

Training an 8B model for style does not guarantee it will match a stronger frontier model's contextual wit, knowledge, or tool reliability. Use it first as a controlled comparison, not a whole-agent replacement. Whether to keep it depends on held-out multi-turn dialogue and tool-use tests, not training loss alone.

## Sources

- Training price/formula: turn830600view0, recorded in 20_sources.md and findings_fallback.md.
- Eligible custom LoRA inference pricing/constraints: turn128585view0, recorded in both files.
- LoRA definition and intended trade-off: turn828536search0.

No alternative provider was researched deeply enough to quote; no claim of globally cheapest provider.
