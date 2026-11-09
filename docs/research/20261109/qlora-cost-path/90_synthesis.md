# Low-cost Tuning Route

Date: 2026-11-09, Asia/Shanghai.
Evidence: `20_sources.md` in this directory.

## Findings

- LoRA trains a small adapter; QLoRA additionally stores the base training weights in four-bit form to reduce memory. This is a cheaper experimentation route than updating all model weights. Source: Hugging Face PEFT quantization guide and TRL PEFT integration guide.
- Adapter files do not replace the base model. Loading requires the original compatible model and adapter configuration. Source: PEFT checkpoint guide.
- A proprietary chat API cannot be assumed to accept an independently trained adapter. Provider-side tuning or explicit compatible adapter-hosting support is required. This is an engineering inference from adapter loading requirements.
- Renting a GPU only for a short training run does not cover the ongoing inference bill. Continuous self-hosting keeps consuming rented resources; an on-demand hosted endpoint has its own inference charges and availability tradeoffs. This is an architecture/cost-accounting inference, not a current provider price claim.

## Beginner Recommendation

Begin by improving contextual examples and collecting a consented, de-identified evaluation set. If supervised tuning becomes justified, prefer a provider-supported tuning product with transparent training and inference charges. Cloud QLoRA on a modest open-weight Chinese-capable model is a possible later experiment, not the first step for this novice.

Do not promise a hardware requirement, a universal dollar budget, a guaranteed naturalness improvement, or increased subjective consciousness. A tuned small model is not guaranteed to preserve the reasoning/tool-use reliability of a larger API model.

## Evidence Limits

No GPU price was verified. No purchase, account creation, model training, or production endpoint call was performed.
