# Group Observation: Sources

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://huggingface.co/docs/trl/en/sft_trainer
**Method**: extract (web.run open, explicit short response)
**Confidence**: high
**Insight**: SFT trains on input/output examples. For a group-chat adaptation pipeline, conversational context and the selected agent response should be distinguished instead of treating every participant's utterance as the agent's desired output.

# Relevant extracted content

- Verified citation: turn377278view0.
- Lines 123-141: conversational and prompt-completion formats are supported.
- Lines 174-190: SFT optimizes target outputs conditioned on input sequences.
- Lines 235-248: assistant-only or completion-only loss excludes context/system/user messages from training targets.
- Lines 266-280: adapter training through PEFT is supported.
- Dataset selection, consent, shadow-mode evaluation, and chronology-based holdout recommendations below are project-specific engineering proposals, not findings established by this API reference.

---
