# Phase 04-4c — LLM-based summarization

> When sliding window would drop turns, summarize early history first.
> When ErrContextLength fires (estimation wrong), summarize and retry.

## Frontmatter

| Field | Value |
|---|---|
| Phase | `04-4c` |
| Status | `⬜ not started` |
| Owner | chaosbot authors |
| Pre-requisites | Phase 04-4 (sliding window), provider.ErrContextLength sentinel |
| Estimated total LOC | ~120 Go + ~80 test |
| Performance impact | one extra Chat call per summarization trigger |

## Goal

Add LLM-based summarization as the second layer of context window
management. The sliding window (04-4) proactively drops turns based
on token estimation; summarization preserves information from those
dropped turns by compressing them into a summary before dropping.

Two trigger points:
1. **Proactive** (applyWindow): estimate exceeds budget → summarize
   early half → re-check → drop summary if still too big.
2. **Reactive** (Run loop): provider returns ErrContextLength
   (estimation was wrong) → summarize full history → retry step.

## Design

### Summary message format

The summary is a single `provider.Message{Role: RoleUser, Content:
"<summary>"}`. User role because most providers refuse non-trailing
system messages. The summary content is structured:

```
[Conversation summary]
- File paths mentioned: ...
- Key decisions: ...
- Current task state: ...
- Errors encountered: ...
```

### Summarization prompt

A system-level instruction (hardcoded, not configurable) that asks
the LLM to produce a structured summary:

```
Summarize the following conversation fragment concisely.
Preserve: file paths, key decisions, current task state, errors.
Output only the summary, no preamble.
```

The prompt is sent as the sole system message in a fresh Request.
The conversation fragment is sent as a single user message.

### Proactive summarization (applyWindow)

```
applyWindow(history):
  budget = contextBudget()
  if estimate(history) <= budget:
    return history  // no-op

  // Try summarizing the early half
  mid = len(history) / 2
  early = history[:mid]
  recent = history[mid:]
  summary, err = summarizeHistory(early)
  if err != nil:
    // Summarization failed (provider error, ctx cancel).
    // Fall back to plain dropping.
    return dropOldestTurns(history, budget)

  candidate = [summary] + recent
  if estimate(candidate) <= budget:
    return candidate
  // Summary still too big → drop it, return recent only
  return recent
```

### Reactive summarization (Run loop)

When `step()` returns `ErrContextLength`:

```
if errors.Is(err, provider.ErrContextLength):
  summary, sErr = summarizeHistory(history)
  if sErr != nil:
    // Can't summarize → fall back to clear + abort
    a.History = a.History[:0]
    return "", fmt.Errorf("context too long, history cleared: %w", err)
  // Retry with summarized history
  history = [summary]
  continue  // re-enter the loop with compressed history
```

### New function: summarizeHistory

```go
// summarizeHistory calls the LLM to summarize the given
// messages into a single summary message. Returns the
// summary as a RoleUser message. The caller is responsible
// for deciding where to place it in the history.
func (a *reActAgent) summarizeHistory(ctx context.Context, history []provider.Message) (provider.Message, error)
```

Implementation:
1. Build a Request with:
   - System: the summarization prompt
   - Messages: a single user message containing the serialized history
   - Model: a.Cfg.Model (same model, different system prompt)
   - MaxTokens: 1024 (bounded summary length)
2. Call a.Provider.Chat(ctx, req)
3. Return `provider.Message{Role: RoleUser, Content: resp.Content}`

### History serialization for summary input

The conversation fragment sent to the LLM for summarization is
serialized as:

```
[user]: <content>
[assistant]: <content>
[tool]: <name> → <content>
...
```

This is a simple string format, not JSON. The LLM only needs to
read it; no parsing required on output.

### Config

Add to `agent.Config`:

```go
SummaryEnabled bool // default true; false disables summarization
```

When false, applyWindow falls back to plain dropping (no LLM call).
ErrContextLength still triggers clear + abort (no summarization).

## Summary persistence (Phase 06 interaction)

Summary is persisted **separately** from history, not replaced into it.

### Data structures

```go
// Session is the on-disk format (Phase 06).
type Session struct {
    History []provider.Message // full unmodified history
    Summary *SummaryInfo       // nil if never summarized
}

// SummaryInfo is the cached summary with a cursor into History.
type SummaryInfo struct {
    Content string // the summary text (RoleUser message)
    Cursor  int    // index into History: summary covers [0, Cursor)
    Tokens  int    // estimated tokens of the summary content
}
```

### Loading logic

```
loadSession():
  session = readFromDisk()

  if session.Summary != nil && session.Summary.Cursor <= len(session.History):
    // Summary is valid: use [summary_msg] + history[cursor:]
    summaryMsg = Message{Role: RoleUser, Content: session.Summary.Content}
    inMemory = append([]Message{summaryMsg}, session.History[session.Summary.Cursor:]...)
  else:
    // No summary or stale cursor: use full history
    inMemory = session.History

  return inMemory
```

### Saving logic

```
saveSession():
  // Always persist the full history (complete record).
  session.History = a.History  // full, unmodified

  // Persist summary if it exists (from last summarize call).
  session.Summary = a.summaryInfo  // nil if never summarized

  writeToDisk(session)
```

### Summary validity

The cursor tracks "how many messages from History this summary covers."
When new turns are added (Run succeeds), the cursor doesn't change —
the summary only covers the old turns. On the next summarize trigger,
the cursor advances to cover more turns.

Staleness: if `cursor > len(history)` (history was truncated externally
or session was edited), the summary is invalid → re-summarize.

### In-memory representation during Run

```
a.History = [full unmodified history]       // always complete
a.summaryMsg = [summary RoleUser message]    // nil if never summarized
a.summaryCursor = int                        // 0 if no summary

// What the LLM sees (windowed view):
view = [summaryMsg] + a.History[a.summaryCursor:]
// then applyWindow further trims view if needed
```

## Test points

### Unit tests (agent_internal_test)

1. **summarizeHistory_basic**: mock provider returns a summary string;
   verify the returned message is RoleUser with the summary content.

2. **summarizeHistory_providerError**: mock provider returns error;
   verify summarizeHistory propagates the error.

3. **applyWindow_summarizeBeforeDrop**: with a budget that requires
   dropping, verify that summarizeHistory is called and the summary
   replaces early turns.

4. **applyWindow_summarizeFallsBackOn error**: when summarizeHistory
   fails, verify applyWindow falls back to plain dropOldestTurns.

5. **applyWindow_summaryStillTooBig**: when the summary itself exceeds
   budget, verify it's dropped and only recent turns remain.

6. **Run_errContextLength_summarizesAndRetries**: mock provider first
   returns ErrContextLength, then succeeds on retry; verify history
   is summarized and the run completes.

7. **Run_errContextLength_summarizeFails_clearsHistory**: when both
   ErrContextLength fires AND summarizeHistory fails, verify history
   is cleared and error is returned.

8. **SummaryEnabled_false**: when disabled, verify no summarization
   call is made (plain drop).

9. **SummaryCursor_valid**: verify in-memory view is
   `[summaryMsg] + history[cursor:]` when summary exists and cursor
   is valid.

10. **SummaryCursor_stale**: verify full history is used when
    cursor > len(history) (summary invalid).

11. **summaryInfo_after_summarize**: verify summaryInfo is populated
    after a successful summarizeHistory call (content, cursor, tokens).

## Risks

- **Extra LLM call**: summarization adds one Chat round-trip per
  trigger. For a typical 30-step session, it fires at most once
  (proactive) or once (reactive), so total overhead is ~2s.
- **Summary quality**: depends on the LLM's ability to summarize.
  Bad summaries lose context. The safety net (clear history) is
  the fallback.
- **Token cost**: the summary input is the early turns (could be
  large), output is ~1K tokens. Acceptable for the information
  preserved.

## Not in scope

- Streaming summary
- Summary quality evaluation / metrics
- `/summarize` slash command
- Configurable summary prompt (hardcoded for now)
- Session save/load implementation (Phase 06; this spec defines the
  data structures and logic, Phase 06 implements the file I/O)
