# Context Retention Cost Review

Date: 2026-11-09, Asia/Shanghai.

Question: Should the account-wide agent retain its working conversation across
multiple finish/wake cycles while controlling input and output costs?

Scope: Research and a design recommendation only. No runtime changes in this
turn. The user has confirmed one account-wide reasoning workflow.

Sources: Official provider documentation for cache behavior and billing; local
source code for the current lifecycle. Use native web retrieval because no
dedicated research-browser tool is exposed in this session.

Local evidence:
- agent/manager.go builds msgs as a local variable inside handleTurn.
- finish returns from handleTurn; the next invocation builds messages again.
- agent/context.go reconstructs QQ history and appends notes and chat_style.md.
- agent/usage.go currently accumulates input, output, cache-hit and cache-miss
  token counts, without a per-wake cost comparison.

Alternatives to compare:
1. Rebuild the working conversation on every wake.
2. Retain an unbounded working conversation.
3. Retain a bounded rolling conversation and compact at safe boundaries.

Any proposed thresholds are starting configuration choices, not experimentally
established optima or provider limits.
