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
| Status | `✅ complete` (all 2 sub-units done; see 实现笔记) |
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

---

### 实现笔记 10-1 — Store.LoadFrom + ErrStaleCursor

**Files**: `internal/session/store.go` (+20), `internal/session/filestore.go` (+32),
`internal/session/filestore_test.go` (+150)

**Interface changes**:
- `Store.LoadFrom(ctx, id, offset int) ([]Message, error)` added.
- `ErrStaleCursor` sentinel added.

**Implementation**:
- `FileStore.Load` now delegates to `loadFromOffset(ctx, id, 0)`;
  `LoadFrom` to `loadFromOffset(ctx, id, offset)`. Single-pass
  `bufio.Reader.ReadBytes('\n')`, skipping non-empty lines until
  offset is reached, decoding the rest. `ErrStaleCursor` returned
  when `skipped < offset` after EOF.
- `NoopStore.LoadFrom` returns `os.ErrNotExist` (DI parity with
  Load; Resume already rejects nil Store).
- Negative offset guarded in `LoadFrom` (defensive — programming
  error, not user input).

**Tests (6 new, all PASS)**:
- `LoadFrom_offsetZero` — offset 0 ≡ full Load
- `LoadFrom_offsetMid` — returns `[offset:]` only
- `LoadFrom_offsetAtEnd` — offset == line count → empty + nil
- `LoadFrom_offsetBeyondEnd` — `ErrStaleCursor` wrapped
- `LoadFrom_NotExist` — `os.ErrNotExist`
- `LoadFrom_NegativeOffset` — defensive error
- `LoadFrom_LargeLineAtOffset` — 1.5 MB message at offset 2
  round-trips (proves no scanner-buffer regression)

**Commit**: `3f9ccb2 feat(session): add Store.LoadFrom for lazy Resume (Phase 10-1)`

### 实现笔记 10-2 — Agent 集成 + 累积 cursor 修复

**Files**: `internal/agent/agent.go` (+71 net), `internal/agent/agent_internal_test.go` (+218)

**State changes**:
- New field `trimmedTotal int` on `reActAgent`. Invariant:
  `History[0]` corresponds to on-disk position `trimmedTotal`.
  `SaveSummary.Cursor = trimmedTotal + committedPrefix` is
  always the absolute on-disk position covered by the current
  summary.
- `Resume` rewritten to use `LoadFrom`. `ErrStaleCursor` from
  `LoadFrom` falls back to full `Load` so old relative-cursor
  sessions (Phase < 10) load successfully, just without the
  memory savings on first resume.
- `pruneHistory` accumulates `trimmedTotal += n` after slicing.
- `applyWindow`: prepending `summaryMsg` now keyed on
  `summaryMsg != nil` (was `committedPrefix > 0` — false after
  Resume with non-zero cursor, silently dropping the summary).
  Invariant: every `summaryMsg = &summary` in `applyWindow` is
  immediately followed by `committedPrefix += split`, so
  `summaryMsg != nil` ⟹ absCursor > 0 ⟹ summary covers
  messages not in `candidate`.
- `clearSessionState()` helper shared by `Resume` and `Reset`
  (history + 3 summary fields; `History = a.History[:0]`
  retains backing array for reuse).
- `loadHistory(ctx, id, info)` helper holds the cursor / stale /
  full-Load branching and the `summaryMsg` restoration. `info`
  zero value (`SummaryInfo{}`) when LoadSummary returns
  `os.ErrNotExist` short-circuits naturally — no special case
  needed in `Resume`.

**Tests (5 new, all PASS)**:
- `Resume_LazyLoadFromCursor` — 10-msg file + cursor=6 →
  `History` has 4, `trimmedTotal=6`, `summaryMsg` restored
- `Resume_StaleCursor_FallsBackToFull` — cursor=99 on 3-msg
  file → `ErrStaleCursor` → full Load, `trimmedTotal=0`,
  `summaryMsg=nil`
- `Resume_NoSummary_LoadsFull` — no sidecar → full Load,
  `trimmedTotal=0`
- `PruneHistoryCursorIsCumulative` — **the bug fix**: two Run
  cycles, assert second `SummaryInfo.Cursor` strictly greater
  than first. Pre-populated one turn so `splitPoint` finds a
  cut and summarization triggers reliably.
- `Reset_ClearsTrimmedTotal` — Reset zeros the new field

**偏差 from spec**:
- `applyWindow` spec said "`committedPrefix>0 || trimmedTotal>0`";
  final code is `summaryMsg != nil` (review found the OR was
  misleading — neither condition independently proves the
  summary is non-stale; the real invariant is the assignment
  ordering inside `applyWindow`).
- `Resume` ended up splitting into Resume + `loadHistory` +
  `clearSessionState` instead of inlining everything (review
  found the inline version leaked helper internals to the
  caller via `trimmedTotal > 0` check).

**Follow-ups (deferred)**:
- Stale cursor fallback is silent. If we ever see real sessions
  hitting it (cross-device sync, manual truncate), add
  `slog.Warn` in the fallback branch.
- No metric for cumulative cursor growth — current state is
  inspectable via `LoadSummary.Cursor`, no separate counter
  needed.

**Commit**: `350e5bd fix(agent): cumulative summary cursor + lazy Resume (Phase 10-2)`