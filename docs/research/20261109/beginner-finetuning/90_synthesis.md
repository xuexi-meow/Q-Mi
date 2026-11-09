# Beginner Fine-Tuning: Recommendation

Date: 2026-11-09, Asia/Shanghai.

## Evidence

- Main verified source checkpoint: 20_sources.md, usable citations turn675751view0 and turn479679view0.
- Managed service checkpoint and synthesis: ../managed-finetune-cost/20_sources.md and ../managed-finetune-cost/90_synthesis.md; usable citations turn830600view0 and turn128585view0.
- No production code, account configuration, training job, paid model call, or deployment was changed.

## Beginner Path

1. First collect about 50 contextual dialogue situations for baseline evaluation. This is a suggested small experiment, not a claim that every platform accepts 50 training examples.
2. Use a few appropriate demonstrations and relevant memories with the existing model before buying training. Retain the comparison cases.
3. If useful conversational behavior remains inconsistent, curate a training set of contextual inputs and desired responses with consent and redaction. Set aside whole conversations for evaluation to reduce overlap.
4. For a low-cost trial without local hardware, consider Alibaba Bailian Beijing Qwen3-8B, SFT efficient LoRA, then supported token-based deployment. Check console eligibility, dataset requirements, and estimate before paying.
5. Compare on unseen multi-turn conversation, appropriate silence, tool-use reliability, privacy, and prefix leakage. Do not replace the whole current agent based solely on training loss or a few funny outputs.

## Costs

Official managed-service checkpoint lists Qwen3-8B training at CNY 0.006 per thousand training tokens. A 1M-token dataset over three epochs, without mixed data, is a derived CNY 18 training charge. This is an illustration, not a full project quote or required dataset size.

Eligible Beijing LoRA deployment lists CNY 0.5/million input tokens and CNY 2/million non-thinking output tokens, or CNY 5/million thinking output tokens. Regions, account eligibility, and supported models matter. Data preparation, evaluation, repeated runs, and serving are separate cost categories.

## Limits

LoRA is not a transferable personality file for arbitrary APIs. It requires its compatible base or explicit provider support. Fine-tuning a smaller model does not establish equivalence to a stronger existing model in comprehension or tool use. No guaranteed cost-to-humanlike-conversation conversion is asserted.
