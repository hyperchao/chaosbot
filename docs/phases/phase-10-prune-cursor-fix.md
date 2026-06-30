# Phase 10 — PruneHistory cursor fix + Resume lazy-load

> `pruneHistory` resets `committedPrefix` to 0 and the next
> `saveOnSuccess` overwrites the persisted `SummaryInfo.Cursor`
> with a relative value, silently losing coverage of the
> messages that were already trimmed. Resume also loads the
> full on-disk history into memory just to throw most of it
> away. Fix both: cumulative cursor + Store.LoadFrom.

## Frontmatter

| Field | Value |
|---|---|
| Phase | `10` |
| Sub-units | `10-1` … `10-2` |
| Status | `🟡 in progress` (sub-units 10-1 + 10-2 staged, awaiting review/commit) |
| Owner | chaosbot authors |
| Pre-requisites | Phase 06 (session), Phase 04-4b (summary cursor) |
| Estimated total LOC | ~120 Go + ~120 test |
| Performance impact | Resume peak memory drops from O(N) to O(N−cursor); subsequent Runs unchanged |

## Goal

1. **Bug fix**: `pruneHistory` + `saveOnSuccess` round-trip
   preserves cumulative coverage of the summary cursor across
   prunes. Resume on a pruned session does not silently re-feed
   already-committed messages to the LLM.
2. **Memory**: Resume loads only `history[cursor:]` from disk,
   not the full file. For long sessions the saved prefix stays
   out of RSS entirely.

## Problem

### Cursor semantics are broken

Current `saveOnSuccess` writes `SummaryInfo{Cursor: a.committedPrefix}`.
After `pruneHistory` resets `committedPrefix = 0`, the next save
overwrites the on-disk cursor with a smaller value:

```
Run1: committedPrefix=5  → SaveSummary(Cursor=5)  → prune(reset=0)
Run2: committedPrefix=3  → SaveSummary(Cursor=3)  ← overwrites Cursor=5
                              ↑ relative to trimmed slice, not cumulative
Resume: disk has 10 msgs, Cursor=3 → only skips first 3 → 7 msgs re-fed
```

The bug was caught during Phase 08-3 code review.

### Resume loads everything

`Resume` calls `Store.Load(ctx, id)` which returns the full file
contents into `a.History`. For a session that already has a
summary covering the first 80% of messages, those messages are
immediately discarded by `committedPrefix = info.Cursor`. Peak
RSS is O(file size); we never need those bytes in memory.

## Design

### Cumulative cursor via `trimmedTotal`

Add a new field to `reActAgent`:

```go
type reActAgent struct {
    ...
    trimmedTotal int // absolute count of messages already trimmed from disk view
}
```

Semantics:

- `trimmedTotal` is the number of leading on-disk messages the
  agent has conceptually discarded. It is the absolute offset
  in the on-disk file at which `a.History[0]` lives.
- After a fresh `New()` or `Reset()`: `trimmedTotal = 0`.
- After `Resume(id)` with `SummaryInfo.Cursor = C`:
  `trimmedTotal = C` and `a.History[0]` corresponds to disk
  position `C`.
- After `pruneHistory(n)`: `trimmedTotal += n`.
- `saveOnSuccess` persists `SummaryInfo{Cursor: trimmedTotal + committedPrefix}`
  so the cursor is always the absolute on-disk position covered
  by the current summary.
- `committedPrefix` stays an in-memory concept (count of
  in-memory messages already covered by `summaryMsg`). Zero
  after `pruneHistory`; non-zero as the window slides.

This restores the invariant: `SummaryInfo.Cursor` on disk is
always an absolute on-disk offset.

### Store.LoadFrom

Add to `session.Store`:

```go
// LoadFrom returns history[offset:] for the given ID.
// offset is the count of leading messages to skip. Used by
// Resume to avoid loading the already-summarized prefix into
// memory. Returns os.ErrNotExist if the session doesn't exist.
// Returns ErrStaleCursor if offset exceeds the line count;
// the caller should treat the summary as stale and fall back
// to Load() with cursor=0.
LoadFrom(ctx context.Context, id string, offset int) ([]provider.Message, error)
```

Add sentinel:

```go
var ErrStaleCursor = errors.New("session: cursor beyond end of history")
```

Implementations:

- **FileStore**: stream lines, decode and skip `offset` of them,
  return the rest. Open file once, no second pass needed.
- **NoopStore**: returns `os.ErrNotExist` (Resume already
  rejects nil store; NoopStore exists for DI compatibility).

### Resume

```go
func (a *reActAgent) Resume(ctx context.Context, id string) error {
    if a.Store == nil { ... }
    a.summaryMsg = nil
    a.committedPrefix = 0
    a.trimmedTotal = 0
    info, err := a.Store.LoadSummary(ctx, id)
    switch {
    case err == nil:
        // Only trust cursor if we can load from that offset.
        if info.Cursor > 0 {
            tail, lerr := a.Store.LoadFrom(ctx, id, info.Cursor)
            switch {
            case lerr == nil:
                a.trimmedTotal = info.Cursor
                a.History = tail
                if info.Content != "" {
                    a.summaryMsg = &provider.Message{...}
                }
            case errors.Is(lerr, session.ErrStaleCursor):
                // Fall through to full Load below.
            default:
                return fmt.Errorf("agent: resume %s: %w", id, lerr)
            }
        }
    case errors.Is(err, os.ErrNotExist):
        // No summary — fine.
    default:
        return fmt.Errorf("agent: resume %s summary: %w", id, err)
    }
    if a.History == nil {
        // Either no summary, or stale cursor fallback.
        full, err := a.Store.Load(ctx, id)
        if err != nil { return fmt.Errorf("agent: resume %s: %w", id, err) }
        a.History = full
    }
    a.sessionID = id
    a.sessionOffset = len(a.History)
    return nil
}
```

Peak memory: O(`history[cursor:]`) not O(`history`).

## Sub-units

### 10-1 — Store.LoadFrom + ErrStaleCursor

Files: `internal/session/store.go`, `internal/session/filestore.go`,
`internal/session/filestore_test.go`

- Add `ErrStaleCursor` sentinel.
- Add `LoadFrom` to `Store` interface.
- `FileStore.LoadFrom`: stream lines, skip `offset`, decode rest.
  Return `ErrStaleCursor` (wrapped) when `offset > line_count`.
- `NoopStore.LoadFrom`: returns `os.ErrNotExist`.
- Tests (table-driven):
  1. **LoadFrom_offsetZero**: returns full history.
  2. **LoadFrom_offsetMid**: returns `[offset:]`.
  3. **LoadFrom_offsetAtEnd**: returns empty slice + nil error.
  4. **LoadFrom_offsetBeyondEnd**: returns `ErrStaleCursor`.
  5. **LoadFrom_notExist**: returns `os.ErrNotExist`.
  6. **LoadFrom_largeLine**: single message > 1 MiB at offset
     decodes correctly.

### 10-2 — Agent integration + cursor fix

Files: `internal/agent/agent.go`, `internal/agent/agent_test.go`,
`internal/agent/agent_internal_test.go`

- Add `trimmedTotal int` field to `reActAgent`.
- `Resume`: rewrite as above (LoadFrom path + stale fallback).
- `saveOnSuccess`: `SummaryInfo.Cursor = a.trimmedTotal + a.committedPrefix`.
- `pruneHistory`: `a.trimmedTotal += n` after slicing `a.History`.
- Tests:
  1. **Resume_lazyLoadFromCursor**: write 10 messages + summary
     with Cursor=6; Resume; assert `a.History` has 4 messages
     and `a.trimmedTotal == 6`.
  2. **Resume_staleCursor_fallsBackToFull**: write 3 messages +
     summary with Cursor=99; Resume; assert all 3 loaded and
     `a.trimmedTotal == 0`.
  3. **PruneHistoryCursorIsCumulative** (the bug fix): Run1 with
     summarization triggered → SaveSummary; Run2 → SaveSummary;
     inspect second Cursor on disk; assert it equals
     (first Cursor + Run2 commit count).
  4. **SaveOnSuccess_PersistsAbsoluteCursor**: replace/update
     the existing `TestSaveOnSuccess_PersistsSummary` to assert
     the on-disk Cursor matches the in-memory `trimmedTotal +
     committedPrefix`.

## Test points

- Resume with cursor=0: behavior unchanged, full history
  loaded (LoadFrom(ctx, id, 0) ≡ Load(ctx, id)).
- Resume with cursor=N where N < len(history): loads only
  tail; `trimmedTotal=N`; `committedPrefix=0`.
- Resume with cursor >= len(history): `ErrStaleCursor` from
  LoadFrom; falls back to Load; cursor discarded;
  `trimmedTotal=0`.
- Resume with no summary: cursor=0; LoadFrom(0) ≡ full load.
- After Run + SaveSummary + prune, next Run's SaveSummary
  cursor is the previous absolute + new commits (cumulative).
- After Reset: `trimmedTotal=0`.

## Risks

- **Existing on-disk sessions**: cursor semantics change.
  Old sessions with `SummaryInfo.Cursor=N` (relative) would be
  treated as absolute after this change. This could cause
  `ErrStaleCursor` → fallback to full load → works correctly
  (just a memory hit, not a correctness bug). Acceptable for
  MVP; no migration needed.
- **LoadFrom IO**: a single `bufio.Reader` pass with line skip
  is straightforward; same complexity as `Load`.
- **`summaryMsg` semantics**: after Resume with cursor=N,
  `summaryMsg` is restored from disk content. The summary
  covers absolute `[0..N)` which is not in memory. `committedPrefix`
  stays 0 because there's nothing to commit in memory. This
  matches the current behavior.

## Not in scope

- Compaction of the on-disk file (still grows monotonically).
- Encoding `trimmedTotal` into `SummaryInfo` (it's an agent
  internal; only `Cursor` crosses the boundary, now correct).
- Migration script for old relative-cursor sessions.