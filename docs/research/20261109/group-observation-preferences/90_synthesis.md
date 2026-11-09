# Practical Conclusion

Date: 2026-11-09, Asia/Shanghai.
Source: `20_sources.md`, Hugging Face TRL DPO Trainer, citation `turn400843view0`.

TRL DPO explicitly supports a conversation context (`prompt`) with a preferred response (`chosen`) and a dispreferred response (`rejected`). This supports collecting "same context, good continuation versus poor continuation" examples for later preference training, including adapter-based training.

Observing group chats can provide raw contextual material, but observation alone is not preference training or automatic weight updates. Human selection or another carefully designed labeling process remains necessary; a human message is not automatically the right target, and no cited source guarantees that DPO produces more natural group-chat behavior.
