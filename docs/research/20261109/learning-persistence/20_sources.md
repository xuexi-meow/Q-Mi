# Learning Persistence: Sources

**Time**: 2026-11-09, Asia/Shanghai
**Source**: https://huggingface.co/docs/peft/en/developer_guides/checkpoint
**Method**: extract (web.run open, explicit short response)
**Confidence**: high
**Insight**: LoRA-style learned parameters persist in adapter checkpoint files and require a compatible base model; they are separate from a conversation context or memory document.

# Relevant extracted content

- Verified citation: turn827355view0.
- Lines 108-117: parameter-efficient checkpoints hold adapter parameters, not all base parameters; saving creates adapter weights and configuration.
- Lines 142-146: configuration records base-model identity and revision.
- Lines 154-157: weights and configuration are required for conversion/loading.
- Lines 217-250: LoRA configuration specifies base model, dimensions, target modules, and related settings.
- Cloud retention/export policies were not established by this source and must not be guaranteed.

---

**Time**: 2026-11-09, Asia/Shanghai
**Source**: F:/Go/Q-Mi/agent/context.go:466
**Method**: extract (PowerShell Get-Content)
**Confidence**: high
**Insight**: The project already reads durable chat-style reference text from the workspace. This is contextual memory, not a trained model checkpoint.

# Relevant extracted content

- readChatStyle resolves workspace chat_style.md and opens the saved file.
- It bounds input to maxChatStyleRunes and returns empty content for a missing file.
- The function comment states it reloads durable learning each turn without changing the static system prefix.
- File persistence does not imply the model weights are updated.

---
