# Learning Persistence: Recommended Design

Date: 2026-11-09, Asia/Shanghai.

## Existing Versus Proposed

Existing: saved workspace chat_style.md is reloaded into context; this survives process restart while the file is preserved. It is not weight training.

Proposed: keep three separate durable assets:

1. Scoped memories and concise style references for immediate context use.
2. Owner-approved, consented, redacted training examples plus held-out evaluation cases.
3. Versioned training artifacts: compatible base-model identifier/revision, adapter weights/configuration where exportable, training settings, and evaluation results. For managed services, record the platform model/deployment identifiers and check retention/export policy.

The inference route must load or call the trained version. Saving examples alone does not apply their learning, and routing back to the original base/API does not retain the tuned parameters.

Recommend immutable versioned releases, baseline/previous-version retention, tested rollback, and independent backups of data and settings. For subsequent training, evaluate older cases as well as new cases; mixing representative approved older examples is a proposed safeguard, not a guarantee against regression.

Changing model families should not assume adapter portability; the preserved dataset and evaluation set provide a path to retraining on another compatible platform.

No production code, prompt, training job, or paid service was changed.
