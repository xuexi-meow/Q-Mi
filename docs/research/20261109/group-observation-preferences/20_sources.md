# Group Observation And Preference Data

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://huggingface.co/docs/trl/en/dpo_trainer
**Method**: extract (web.open; readable official page response)
**Confidence**: high
**Insight**: TRL DPO uses a shared prompt with preferred and dispreferred completions; ordinary unlabeled group transcripts are not yet that preference dataset.

# Relevant extracted content

- Verified citation ID: `turn400843view0`.
- Lines 126-146 document both standard and conversational preference formats.
- Lines 131-141 demonstrate explicit `prompt`, `chosen`, and `rejected` fields.
- Lines 170-176 explain training on two completions of the same prompt with a preference between them.
- Lines 262-280 document PEFT adapter integration; DPO need not update all parameters.
- Practical inference: observing a chat can supply context and candidate examples, but someone must select what was good and compare it against a worse alternative before it becomes useful preference supervision.
- Practical inference: learning from every observed reply indiscriminately risks rewarding the wrong behavior; the official page establishes the preference-data requirement, not a guarantee of conversational naturalness.

---
