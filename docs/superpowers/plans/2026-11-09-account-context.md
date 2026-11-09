# Account Context Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep autonomous account activity continuous across wakes without
interrupting requests or allowing unbounded context growth.

**Architecture:** One debounced account inbox feeds a serial worker. A dedicated
working-context unit owns retention, sizing and safe compaction. The existing
builder gains incremental and live-state methods without breaking custom builders.

**Tech Stack:** Go, existing OpenAI-compatible HTTP client, existing SQLite
message storage, standard-library tests and local HTTP fakes.

**Spec:** docs/superpowers/specs/2026-11-09-account-context-design.md

## Global Constraints

- Preserve uncommitted prompt, learning-file and outgoing-message guard changes.
- No new dependencies, real account calls or raw reasoning replay.
- Zero context settings select defaults; negative/inconsistent settings fail.
- Keep message source metadata and administrator boundaries.
- Execute inline in this session; no additional agent delegation or commits.

### Task 1: Account Inbox And Retention

**Files:** agent/manager.go, agent/inbox.go, agent/continuity_test.go,
agent/interrupt_test.go, agent/eventlog_test.go.

**Interfaces:** Enqueue(IncomingMessage) remains the event entry point.
takePending() *Turn transfers pending messages at safe boundaries.
handleTurn(*Turn) owns the serialized working conversation.

- [x] Add local-HTTP tests: block the first request, enqueue an urgent message
  from another chat, verify no cancellation and insertion after tool results.
- [x] Run `go test ./agent -run 'TestAccount|TestFinishRetains' -count=1`;
  observe failures for missing retention/insertion.
- [x] Replace per-chat workers and cancellation with one pending inbox, a
  buffered wake signal and bounded idle debounce. Preserve immutable turn batches.
- [x] Retain assistant/tool history, close finish calls and keep explicit
  routing for mixed-source batches. Recompute privileges on each new batch.
- [x] Update superseded cancellation tests to assert the new contract.

### Task 2: Incremental Builder And Live State

**Files:** agent/context.go, agent/live_context_test.go, agent/loop_test.go.

**Interfaces:** DefaultBuilder.BuildContinuation(*Turn) []ChatMessage omits
bootstrap history; SystemMessage(*Turn) ChatMessage reloads core prompts;
StateText(*Turn) string formats an authoritative-current data snapshot.

- [x] Test editing guide/persona and chat_style.md between tool responses,
  changing/deleting notes, stable old prefixes and bootstrap deduplication.
- [x] Run `go test ./agent -run 'TestLive|TestBootstrap' -count=1`;
  confirm the intended failing assertions.
- [x] Add the incremental builder methods, last-good core prompt reload and
  state-change-only tail injection. Keep custom ContextBuilder compatibility.
- [x] Verify existing context, image and learning tests remain valid.

### Task 3: Bounded Compaction And Configuration

**Files:** agent/conversation.go, agent/conversation_test.go,
utils/config/config.go, utils/config/config_test.go, Config.example.yaml.

**Interfaces:** workingConversation owns messages, meaningful wake count and
latest live snapshot; prepareContext(ctx, turn, defs) error performs safe
compaction before a request. Config: AgentContext described in the spec.

- [x] Add tests with deliberately small budgets, complete multi-tool groups,
  a scripted summary response, summary HTTP failure and oversized live input.
- [x] Run `go test ./agent ./utils/config -run 'TestContext|TestCompaction' -count=1`;
  observe missing sizing/compaction/configuration failures.
- [x] Implement conservative sizing (text, calls, schemas and fixed image
  allowance), group-safe cuts, tool-free bounded summary requests, usage
  recording, bounded failure fallback and a final budget guard.
- [x] Add per-wake usage logs and document approximate budgets and defaults.

### Task 4: Regression And Documentation

**Files:** agent/guide.md, agent/polling.go, agent/wiring.go, relevant tests.

- [x] Update descriptions of finish, incoming insertion and hot reload.
- [x] Format Go changes with gofmt.
- [x] Run package-focused regression, then `go test ./... -count=1`.
- [x] Run focused repeated concurrency tests and `git diff --check`.
- [x] Review the diff for stale interrupt behavior, routing mistakes,
  incomplete tool pairs, permission carryover and unintended file changes.

## Verification Results

- Full repository tests passed twice after the first scheduling migration,
  including the final source changes.
- `go test -race ./agent -run 'TestAccount|TestFinishRetains|TestLive|TestCompaction|TestContext' -count=3`
  passed, covering the new inbox, retention, state reload and compaction paths.
- `git diff --check` passed; Git reported only LF/CRLF normalization warnings.
- The real bundled prompt/schema estimate motivated increasing the default
  input budget from 32000 to 48000 and subtracting static overhead when deciding
  whether the working conversation needs compaction.
- No real QQ or paid model calls, no commits and no production process restart.
- Unrelated `.workbuddy/` content appeared during execution and was left alone.
