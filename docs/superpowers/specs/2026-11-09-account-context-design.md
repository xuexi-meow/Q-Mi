# Account-Wide Working Context

Approved direction: one account-wide serial activity loop, noninterrupting
incoming messages, bounded cross-wake retention and live prompt updates.

## Scheduling

All groups, private chats and background events share one inbox. Preserve
source IDs on every message. Debounce idle arrivals using a bounded window;
while busy, consume pending arrivals before the next model request. Never
cancel an in-flight request for a new message. Finish pauses activity; arrivals
remaining at that boundary start a subsequent wake. Mixed-source batches have
no implicit plain-text reply destination; explicit send_message still works.
Grant legacy administrator tool access only to a batch entirely composed of
verified administrator chat messages, never to synthetic background events.

## Retention And Cost

Retain the working conversation in process memory across finish boundaries.
Retain assistant reasoning_content in that memory when the provider requires it
for tool-bearing requests (DeepSeek thinking mode rejects requests that drop it);
never persist it to storage, include it in summaries, or replay it after restart.
Append tool-call results, including finish,
in protocol order. Bootstrap QQ history only once; omit bootstrap records that
are already represented by incoming messages.

Add agent.context configuration with max_input_tokens (default 64000),
review_after_wakes (default 8), keep_recent_messages (default 12), and
summary_tokens (default 1024). Zero uses defaults; negative or inconsistent
values fail configuration validation. Input sizing is a documented conservative
estimate, not a provider tokenizer or a guarantee for a model's context window.
Include system, tool definitions and images in sizing.

Review before requests. Deduct fixed system/schema/latest-state overhead first, then compact
near 75% of the remaining working budget, or after the wake review threshold when
over half that remaining budget. Small conversations need no summary. The bundled
prompts/tools measured roughly 30000 tokens with the conservative estimator;
64000 leaves working room for the bundled prompts and tools without pretending to be a
provider context limit. It was raised from 48000 once the bundled prompt stopped
fitting the old 75% guard.
Compact only complete old message/tool groups already read by the model.
Only the working record is summarized: persona, guide, tool documentation,
system/developer messages and tool schemas are excluded from summary input.
Bootstrap notes, mood and chat style are separate typed snapshots, never
embedded in work messages. Restore the latest snapshot verbatim before the
retained work; superseded snapshots are replaced, not summarized.
Keep recent groups verbatim (including when there are fewer messages than the
configured window), pin all inputs from the current activity across inserted
arrivals, and never mark newly produced tool results as read before an action
request has actually consumed them. Do not advance the cut past these boundaries.
Only request a summary if its worst-case result fits the hard budget and saves
at least 25% of the remaining working budget, with a 256-token minimum. Do not
re-summarize an existing summary alone. This avoids repeated ineffective requests
even when a large protected tail remains above the soft review threshold.
Preserve goals, decisions, unfinished work, evidence and source references
in a bounded summary. Summary calls have no action tools and never send text
to QQ. Count their usage. On failure, preserve a bounded extract with an explicit
loss warning and log the error; do not retry automatically. Distinguish empty
content, output-budget exhaustion (`finish_reason=length`) and unexpected tool
calls, reporting completion usage and the presence of reasoning without using
reasoning as summary content. Reject requests that still exceed the configured
budget without shrinking prompts or discarding protected work.

No extra requests to keep a provider cache alive. A no-action polling wake
does not advance the meaningful-wake counter. In-memory working context is not
restart persistence; existing notes, chat style and QQ storage remain durable.

## Hot Reload And Prefixes

Before every action request, reload persona/guide from their configured files;
keep last good nonempty contents on transient read errors or empty writes.
Replace only the system message when its actual contents change. Explicit core
prompt edits can invalidate the cache prefix and must take effect regardless.

Reload notes, mood and workspace/chat_style.md at every request boundary.
Append a state replacement block only on changes, including deletions; old
blocks remain historical until compaction, when the latest snapshot replaces
them verbatim outside the summary. Do not rewrite old work messages. Continue
treating learned state and summaries as data,
not authority. Incoming messages and learned state cannot change permissions.

## Verification

Use local HTTP fakes, not real QQ/model accounts. Cover noncancellation,
cross-chat insertion, finish retention and tool-pair closure, mixed routing and
permissions, hot reload during tools, bounded compaction and summary failures,
unchanged-prefix reuse, bootstrap deduplication, and configuration validation.
Run package tests and full repository regression; report environment limits.
