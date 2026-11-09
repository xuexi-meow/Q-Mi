# Group Observation: Recommended Learning Loop

Date: 2026-11-09, Asia/Shanghai.

## Verified Basis

- SFT conversational/prompt-completion examples and assistant-only loss: 20_sources.md, citation turn377278view0.
- Shared-context preferred/rejected response pairs: ../group-observation-preferences/20_sources.md, citation turn400843view0.

## Project-Specific Proposal

Use observation as a source of candidates, not automatic approval for training. Split immediate contextual memory from periodic weight updates.

1. Observe a few authorized group conversations. Separate topics, participants, quotation targets, and relationships. Anonymize exported examples; exclude private or sensitive material without consent.
2. Store current group references and continuing personal interests as scoped memory. Keep abstract chat_style.md concise; do not fill it with raw transcripts.
3. Sample interesting conversational moments, not every message. Run the existing agent in offline shadow mode to choose a reply or silence.
4. Let the owner label naturalness, appropriateness, and missed context, or write a better response. Reactions and subsequent replies are weak candidate-selection signals, not automatic correctness labels.
5. Prefer corrected agent outputs and selected persona-appropriate demonstrations. Do not impersonate a human participant's private biography, relationship, or distinctive identity.
6. Build SFT examples with preceding chat as input and only the chosen agent response as target. Preserve actual runtime tool semantics for silence and tool use; do not train the literal text "do not reply" as a public message.
7. Later consider preference tuning if supported by the chosen provider. Prefer owner-approved chosen/rejected pairs over self-grading as sole ground truth.
8. Hold out complete conversations or later time periods. Compare with the current model on contextual wit, relevance, initiative, appropriate silence, privacy, prefix leakage, and tool reliability before deploying.

This is advice only. No collection pipeline, prompt, training job, or bot behavior was changed.
