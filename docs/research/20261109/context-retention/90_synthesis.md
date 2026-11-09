# Cross-Wake Working Context Recommendation

Date: 2026-11-09, Asia/Shanghai.
Status: Design recommendation only; no runtime implementation in this turn.

## Evidence And Limits

- `agent/manager.go:427` creates the message conversation locally for a turn.
- `agent/manager.go:576-579` returns when finish is present.
- `agent/llm.go:138` explicitly documents that reasoning_content is captured,
  not injected into conversation history.
- `agent/usage.go:18-23` accumulates prompt, output, hit, miss and request counts.
- Existing context reconstruction supplies durable state separately, as
  recorded in `00_plan.md`. Losing the local trace is not total memory loss.
- Official documentation retrieval failed: see `10_discovery.md` and
  `20_sources.md`. No verified provider price, cache lifetime or guaranteed
  cache behavior was obtained. No savings percentage is claimed.

## Alternatives

1. Rebuild every wake: simple and bounded, but discards tool working context.
2. Retain indefinitely: continuity, but no bounded input/history growth.
3. Recommended: retain a bounded account-wide working conversation across
   finish/wake boundaries; compact old material at safe boundaries.

## Proposed Policy

- Finish pauses execution, not memory. Retain messages, tool outcomes and
  unfinished work, not an assumed recoverable hidden reasoning process.
- Use context-token budget as the primary bound. As a provisional tuning
  starting point, review after roughly 6-10 meaningful wakeups, not every poll.
  This is a configurable heuristic, not a measured optimum.
- No unconditional summary every wake, and no model call just to keep a
  cache warm. A deliberate scheduled research wake still counts as meaningful.
- Summarize only when needed; preserve goals, decisions, pending work,
  necessary evidence/file pointers and recent exchanges.
- Keep long-term chat habits in chat_style.md, independent of rolling context.
- Append incoming messages at the next request boundary. Do not claim to
  modify an already-running nonstreaming provider request.
- Complete tool-call/result pairs before reuse or compaction.
- Reload mutable notes/style before subsequent model requests, appending
  versioned changes rather than rewriting old events. Core prompt edits
  require an explicit replacement/rebase policy.
- Compare usage per meaningful wake before and after enabling retention.
  When configured model prices are verified, include hit input, miss input,
  output and amortized compaction cost. A hit ratio alone cannot measure cost.

## Decision

Recommend extending retention with bounded rolling context, not simply
raising a fixed wake count. Confirm the policy before architecture changes.
