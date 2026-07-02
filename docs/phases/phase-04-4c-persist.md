# Phase 04-4c-persist — Summary persistence

> When session is saved with a summary, restart should preserve the
> summary so the LLM doesn't re-summarize on every reload.

## Frontmatter

| Field | Value |
|---|---|
| Phase | `04-4c-persist` |
| Status | `✅ complete` (landed in commit `8203f97` together with ErrContextLength handling; see 实现笔记; progress.md row added retroactively) |
| Owner | chaosbot authors |
| Pre-requisites | Phase 04-4c (summarization), Phase 06 (session.Store + FileStore) |
| Estimated total LOC | ~80 Go + ~120 test |
| Performance impact | one extra small file write per save; one extra file read per Resume |

## Goal

Make `summaryMsg` survive a process restart. Currently the
`reActAgent.summaryMsg` field is purely in-memory: every save
persists only the new history turn, and every `Resume` reads
only history. A user who summarized 5 turns, ran the binary, and
restarted would have to re-summarize from scratch on the next
Run — both wasteful (extra Chat call) and lossy (the summary
cursor may no longer match if the model behaves differently
across calls).

We add a **sidecar file** `<id>.summary.json` next to the
existing `<id>.jsonl` and wire it through `FileStore` and the
agent's `Resume` / `saveOnSuccess` paths.

## Design

### Why sidecar, not JSONL header

JSONL is append-only: the existing `FileStore.Append` uses
`O_APPEND` + `bufio.Writer` + `fsync` to keep history writes
atomic per line. A summary in the first line would either:

- require rewriting the whole file every time the summary
  changes (breaking append-only), or
- accumulate orphan summary lines (one per re-summarization,
  each overwriting the previous semantically but not on disk).

A separate file keeps each format's lifecycle clean:
`<id>.jsonl` stays append-only and O(1)-append; `<id>.summary.json`
is overwritten atomically (tmp + rename) on each summary update.

### Summary is a cache, not source of truth

If the sidecar is missing or corrupt, the agent falls back to
the in-memory path: no summary on Resume, re-summarize lazily
on first `ErrContextLength` or on the next proactive trigger.
The full history in `<id>.jsonl` is the authoritative record.

### Data structures

```go
// SummaryInfo persists the last computed summary alongside
// the history it covers. Cursor is an index into the full
// history list (history[Cursor:] is not summarized).
type SummaryInfo struct {
    Content string `json:"content"`
    Cursor  int    `json:"cursor"`
    Tokens  int    `json:"tokens"`
}
```

`Cursor` is **the count of leading history messages the summary
covers**, not a byte offset. Cursor 5 means "summary covers
history[0:5]; history[5:] is verbatim".

### FileStore API additions

```go
// SaveSummary persists the summary for the session.
// Overwrites any previous summary file (atomic via tmp+rename).
// Returns nil on success, error on disk failure.
SaveSummary(ctx context.Context, id string, info SummaryInfo) error

// LoadSummary reads the summary sidecar. Returns
// (SummaryInfo, nil) on success, (_, os.ErrNotExist) when
// no summary has ever been saved, (_, wrapped-error) on
// read/decode failure.
LoadSummary(ctx context.Context, id) (SummaryInfo, error)
```

`Delete(id)` is updated to remove both `<id>.jsonl` and
`<id>.summary.json`. The summary file is best-effort: missing
is OK.

`List` continues to enumerate `<id>.jsonl` files; a session
file without a sidecar is a valid session without a summary.

### Agent state additions

`reActAgent` **merges** the existing `trimOffset` and the new
`summaryCursor` into a single field. Both mean "how much of
`a.History` has been committed out as already-handled (trimmed
or summarized)"; splitting them in two was a source of
off-by-one bugs in the previous implementation (e.g. the
proactive-summary-fallback case where `summaryMsg` was set but
`trimOffset` advanced less than `summaryCursor`).

```go
committedPrefix int // count of leading messages from a.History
                   // already represented by summaryMsg (if set)
                   // or just trimmed away; next step shows
                   // [summaryMsg?] + a.History[committedPrefix:] to LLM
```

Restored from `SummaryInfo.Cursor` on Resume (when summary is
loaded); zero otherwise. Updated to `start + split` when
proactive summarization fires in `applyWindow`.

### Agent lifecycle wiring

**Resume(ctx, id)**:
1. `Store.Load(ctx, id)` → `a.History` (existing behavior)
2. `Store.LoadSummary(ctx, id)`:
   - returns `os.ErrNotExist` → `a.summaryMsg = nil; a.committedPrefix = 0`
   - returns valid `SummaryInfo`:
     - if `info.Cursor <= len(history)` → set `a.summaryMsg = Message{Content: info.Content}`,
       `a.committedPrefix = info.Cursor`
     - if `info.Cursor > len(history)` → **stale** (history was truncated
       externally): discard summary, `a.summaryMsg = nil`
3. `a.committedPrefix` may already be > 0 from the summary load (no extra reset)
4. `a.sessionID = id`, `a.sessionOffset = len(history)` (existing)

**saveOnSuccess(ctx, history)** (existing append stays unchanged):
- if `a.summaryMsg != nil` and `a.committedPrefix > 0`:
  - estimate tokens of `a.summaryMsg.Content` (reuse
    `EstimateTokensDefault`)
  - `Store.SaveSummary(ctx, id, SummaryInfo{Content, Cursor: a.committedPrefix, Tokens})`

**Reset**:
- clear `a.summaryMsg = nil; a.committedPrefix = 0` (already clears
  `summaryMsg`; rename the existing `trimOffset` clear to use the
  new field name)

### applyWindow changes

The biggest correctness change: the persisted summary must
appear in the LLM's view on Resume, even before any new
proactive trigger fires.

`applyWindow` reads `committedPrefix` instead of the old
`trimOffset`/`summaryCursor` pair; `start` is just
`min(committedPrefix, len(history))` — no more `max(...)` since
the field already encodes both notions.

```go
func (a *reActAgent) applyWindow(...) (view, trim, error):
    start := min(a.committedPrefix, len(history))
    candidate := history[start:]
    view := candidate
    if a.summaryMsg != nil && a.committedPrefix > 0:
        view = append([]provider.Message{*a.summaryMsg}, candidate...)
    budget := a.contextBudget()
    if estimate(view) <= budget:
        return view, start, nil
    // ... existing proactive + dropOldestTurns paths ...
```

When the proactive path inside `applyWindow` re-summarizes, it
updates `summaryMsg` and sets `a.committedPrefix = start + split`
(the new boundary). On the fallback (summary is too big → drop
oldest turns), `summaryMsg` and `committedPrefix` are **not**
updated — the fallback returns plain dropped history without
the summary, so committing `committedPrefix = start + split`
would mark more history as "summarized" than actually was. The
fallback's trim is returned directly to the caller.

### summarizeHistory change (correctness fix)

`summarizeHistory(ctx, history)` must prepend `a.summaryMsg`
internally so the LLM has continuity with the previously
summarized prefix. Previously only the proactive path did this
manually; the reactive path forgot, so re-summarization after a
proactive summary would re-compress the entire history from
scratch — losing the previous summary's information.

```go
func (a *reActAgent) summarizeHistory(ctx, history []Message) (Message, error):
    msgs := history
    if a.summaryMsg != nil:
        msgs = append([]Message{*a.summaryMsg}, history...)
    fragment := serializeHistoryFragment(msgs)
    // ... existing Chat call ...
```

Both proactive (applyWindow) and reactive (Run loop) call
`summarizeHistory` and get the same correct behavior.

### Reactive path guards (Run loop)

When `step` returns `ErrContextLength`, the Run loop:

1. If `Cfg.SummaryEnabled == false`: don't try to recover —
   clear history and return the error. Without summarization
   we cannot reduce the input, so re-trying is futile.
2. If `summarizedOnce == true` (already tried once this Run):
   also give up. `[summary]` should always fit (it's capped at
   1024 tokens), but extreme prompt sizes or model behavior
   could still trigger another ErrContextLength; we don't want
   an infinite loop.
3. Otherwise: `summarizeHistory(history)`, set
   `a.summaryMsg = &summary`, `a.committedPrefix = 0`,
   `history = []provider.Message{summary}`, set
   `summarizedOnce = true`, continue.

The `summarizedOnce` flag is per-Run: it resets at the start of
each `Run` and is set to true after one reactive summary.
Stored on `reActAgent` so it persists across loop iterations.

### SummaryEnabled gate consistency

Both proactive and reactive paths gate on `Cfg.SummaryEnabled`.
With `SummaryEnabled = false`:
- `applyWindow` falls back to plain `dropOldestTurns` on budget
  overflow (existing behavior).
- Run loop: on `ErrContextLength`, clear history and return the
  error (no summarization retry).

### Test points

**FileStore tests** (`filestore_test.go`):
1. `SaveSummary_LoadSummary_roundtrip` — save, load, compare.
2. `LoadSummary_NotExist` — returns `os.ErrNotExist` (or wrapped),
   no error of other kind.
3. `SaveSummary_overwrites` — save twice, second wins.
4. `Delete_removesSummary` — Delete clears both `.jsonl` and
   `.summary.json`.
5. `List_worksWithoutSummary` — sessions with no sidecar are
   still listed.

**Agent tests** (`agent_test.go`):
6. `Resume_restoresSummary` — pre-seed history + summary file,
   Resume, verify `a.summaryMsg` non-nil with same content and
   `a.summaryCursor == seeded cursor`.
7. `Resume_staleSummary_discarded` — summary file with
   `Cursor > len(history)` → `a.summaryMsg = nil`.
8. `Resume_noSummary_stillWorks` — no sidecar → `a.summaryMsg = nil`,
   `a.summaryCursor == 0`.
9. `saveOnSuccess_persistsSummary` — after a successful Run that
   triggered summarization, the sidecar file exists with the
   summary content.
10. `applyWindow_includesSummary` — with `summaryMsg` set, the
    view returned by `applyWindow` starts with the summary
    message and the trailing messages come from `history[cursor:]`.
11. `Reset_clearsCommittedPrefix` — `committedPrefix` reset
    to 0 after Reset (covers the renamed field).
12. `summarizeHistory_includesPreviousSummary` — after a
    proactive summary, calling `summarizeHistory` again
    prepends the existing `summaryMsg` so the new summary
    is incremental (doesn't re-compress the prior summary's
    content from scratch).
13. `Run_reactiveSummarizesOnceOnly` — script two consecutive
    `ErrContextLength` responses; verify only one
    `summarizeHistory` call fires, then the loop gives up
    on the second error.
14. `Run_reactiveSummaryDisabled_clearsHistory` —
    `Cfg.SummaryEnabled = false`, script
    `ErrContextLength`; verify the agent returns the error
    immediately without trying to summarize.

## Risks

- **Disk write amplification**: each summarization writes
  `<id>.summary.json` once (small, atomic). Cheap.
- **Two-file inconsistency window**: a crash between
  `<id>.jsonl` Append and `<id>.summary.json` save leaves
  history without the matching summary. Resume treats the
  summary as authoritative only if it parses; missing is OK.
  Worst case: summary is stale, LLM sees `[stale summary] + history`,
  user runs again and the next proactive trigger re-summarizes
  fresh.
- **Cursor arithmetic**: with the merged `committedPrefix`
  field there's only one number to keep in sync; the previous
  `max(trimOffset, summaryCursor)` arithmetic in `applyWindow`
  is gone.

## Not in scope

- Streaming summary updates
- Compressing old summary versions (history is the source of truth)
- Multi-device sync
- Concurrent writes from multiple processes (single-user MVP)
- A session format migration for old `<id>.jsonl` files that
  pre-date the sidecar — they're valid sessions without summaries.

---

### 实现笔记 — Summary persistence landed in `8203f97`

**Commit**: `8203f97 agent: compress on ErrContextLength without step error-return hack`

虽然 commit message 主要讲 reactive ErrContextLength 重构，**Summary persistence 的核心实现也在同一次提交里落地**：
- `session.Store` 接口加 `SaveSummary` / `LoadSummary`
- `FileStore.summaryPath` + atomic tmp+rename 写 `<id>.summary.json`
- `SummaryInfo` 结构（含 `Content` / `Cursor` / `Tokens`）
- `reActAgent.Resume` 调用 `LoadSummary` 恢复 `summaryMsg`
- `reActAgent.saveOnSuccess` 在有 `summaryMsg` 或 `committedPrefix>0` 时持久化
- `reActAgent.Reset` 通过 `Store.Delete` 一并删 sidecar

**后续 commit 增量改进**：
- `c3278ab fix(agent): always persist cursor in SummaryInfo even when summarization is disabled` — 即使 summaryEnabled=false 也要存 cursor-only sidecar,让 Resume 时 `committedPrefix` 能恢复
- `c172e28 test(08-1): boundary and error-path coverage` — 加 SaveSummary roundtrip / not exist / overwrite / read-only dir 失败 / Delete 同步清 sidecar 5 个测
- `6472e94 fix(agent): commitTrim remove min cap` — 修 commitTrim bug
- `3f9ccb2` (`10-1`) — Store.LoadFrom;Resume 走 LoadFrom
- `350e5bd` (`10-2`) — `trimmedTotal` 引入,cursor 改为绝对偏移,saveOnSuccess 写 `trimmedTotal+committedPrefix`;Resume stale cursor fallback

**Phase 10 改进**:stored SummaryInfo.Cursor 在 Phase 10 之前是**相对**已 trim 历史的偏移,有 bug;改成绝对磁盘偏移后,旧 session resume 时 LoadFrom 可能命中 `ErrStaleCursor`,fallback 到全 Load + 丢 summary(单次代价,不影响后续)。

**Spec doc 状态债务**:spec doc (`phase-04-4c-persist.md`) 是 commit `8203f97` 同批次写的,但 Status 字段停留在 `⬜ not started` 没填;progress.md 也只在 `04-4b/c` 一行里隐式覆盖。本次 retroactive 修正:Status → ✅,新增独立 progress.md 行。