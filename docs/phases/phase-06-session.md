# Phase 06 — Session persistence

> JSON session store: save/load full conversation history to disk.
> Agent integration: REPL auto-saves after each Run; `--resume` loads.

## Frontmatter

| Field | Value |
|---|---|
| Phase | `06` |
| Sub-units | `06-1` … `06-3` |
| Status | `✅ complete` (all 3 sub-units done; see 实现笔记) |
| Owner | chaosbot authors |
| Pre-requisites | Phase 04 (Agent loop), Phase 07 (CLI/REPL) |
| Estimated total LOC | ~150 Go + ~80 test |
| Performance impact | one JSON marshal+write per Run (~1ms for typical sessions) |

## Goal

Persist conversation history to disk so the REPL survives restarts
and `chaosbot run --resume <id>` can continue a previous session.
The session store is a leaf package (imports nothing from chaosbot)
that deals only in `[]provider.Message` and JSON files.

## Design

### session.Store interface

```go
package session

// Store is the persistence boundary. Implementations must be
// safe for sequential use (the agent loop is single-threaded).
type Store interface {
    // Append appends new messages to the session file.
    // The caller is responsible for passing only new messages
    // (not the full history). The store simply appends them.
    Append(ctx context.Context, id string, messages []provider.Message) error

    // Load returns the full history for the given ID.
    // Returns os.ErrNotExist if the session doesn't exist.
    Load(ctx context.Context, id string) ([]provider.Message, error)

    // List returns all session IDs, newest first.
    List(ctx context.Context) ([]string, error)

    // Delete removes the session file.
    Delete(ctx context.Context, id string) error
}
```

### File format: JSONL (JSON Lines)

Each line is one JSON-encoded `provider.Message`. No array wrapping,
no commas, no trailing newline on the last line.

```
{"role":"user","content":"hi"}
{"role":"assistant","content":"hello"}
{"role":"assistant","tool_calls":[{"id":"c1","name":"read_file","arguments":{"path":"/foo.go"}}]}
{"role":"tool","tool_call_id":"c1","name":"read_file","content":"package main"}
```

Advantages over full JSON:
- **Append-only writes**: new messages are appended as new lines.
  No need to read or rewrite existing content.
- **Crash-safe**: a partial write at the end only corrupts the last
  line; reader can stop at the last valid line.
- **Streaming load**: decode line-by-line, no need to hold entire
  file in memory.

### session.FileStore (concrete impl)

```go
// FileStore persists sessions as JSONL files in a directory.
type FileStore struct {
    dir string // e.g. ~/.chaosbot/sessions/
}
```

File layout:
```
~/.chaosbot/sessions/
  <id>.jsonl     # one JSON message per line
```

### Append implementation

```go
func (fs *FileStore) Append(ctx context.Context, id string, messages []provider.Message) error {
    f, err := os.OpenFile(fs.path(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
    if err != nil {
        return err
    }
    defer f.Close()

    w := bufio.NewWriter(f)
    for _, m := range messages {
        line, _ := json.Marshal(m)
        w.Write(line)
        w.WriteByte('\n')
    }
    return w.Flush()
}
```

Key points:
- `os.O_APPEND` — kernel-level atomic append, no seek needed.
- `bufio.Writer` — batch small writes into one syscall.
- Caller passes only new messages; store doesn't need to know
  about offsets or existing content.

### Load implementation

```go
func (fs *FileStore) Load(ctx context.Context, id string) ([]provider.Message, error) {
    f, err := os.Open(fs.path(id))
    if err != nil {
        return nil, err
    }
    defer f.Close()

    var messages []provider.Message
    scanner := bufio.NewScanner(f)
    // Increase buffer for large tool outputs (default 64KB).
    scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
    for scanner.Scan() {
        var m provider.Message
        if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
            return messages, fmt.Errorf("session: corrupt line: %w", err)
        }
        messages = append(messages, m)
    }
    return messages, scanner.Err()
}
```

Key points:
- Scanner buffer increased to 1 MB for large tool outputs.
- Partial read on corrupt line: returns messages read so far + error.
- No `io.ReadAll` — streaming decode, bounded memory.

### Constructor

```go
// NewFileStore creates a FileStore. dir is created if it doesn't exist.
func NewFileStore(dir string) (*FileStore, error)
```

### Atomic writes

The initial file creation (first `Append`) uses tmp+rename for
atomicity. Subsequent appends use `O_APPEND` which is atomic at
the OS level for writes < PIPE_BUF (4 KB on Linux, larger on macOS).
Tool outputs can exceed this, so we fsync after each Append.

### Config

Add to `config.Config`:
```go
SessionsDir string `yaml:"sessions_dir"` // default ~/.chaosbot/sessions/
```

## Sub-units

### 06-1 — session.Store + FileStore

Files: `internal/session/store.go`, `internal/session/filestore.go`

- `Store` interface (4 methods: Append/Load/List/Delete)
- `FileStore` struct + `NewFileStore` constructor
- `Append`: `O_APPEND|O_CREATE`, `bufio.Writer`, fsync; caller passes only new messages
- `Load`: `bufio.Scanner` line-by-line JSON decode, 1 MB buffer
- `List`: `filepath.Glob("*.jsonl")`, sort by mtime
- `Delete`: `os.Remove` (idempotent)
- `NewID()` helper: `<date>-<random4>`

### 06-2 — session tests

Files: `internal/session/filestore_test.go`

Tests (table-driven):
1. **AppendLoad_roundtrip**: append 3 messages, load, assert deep equal
2. **Append_increments**: append 2 messages, then append 1 more, load has 3
3. **Load_notExist**: returns `os.ErrNotExist`
4. **List_empty**: returns empty slice (not error)
5. **List_multiple**: create 3 sessions via Append, list returns 3 IDs
6. **Delete**: append then delete, load returns `os.ErrNotExist`
7. **Delete_notExist**: no error (idempotent)
8. **Load_corruptLine**: file with valid+corrupt lines, returns partial
   results + error
9. **Append_largeOutput**: single message >1 MB, verify no truncation

### 06-3 — Agent integration

Files: changes in `cmd/chaosbot/cli/cli.go`, `cmd/chaosbot/wire.go`

- `replCmd`: after each successful `Agent.Run`, call
  `store.Append(ctx, id, agent.History[lastOffset:])` where
  `lastOffset` tracks how many messages were previously saved.
- `/reset`: call `store.Delete(ctx, id)`, then `agent.Reset()`,
  generate new session ID, reset `lastOffset = 0`.
- `runCmd --resume`: call `store.Load(ctx, resumeID)`, set
  `agent.History = loaded`, `lastOffset = len(loaded)`.
- `runCmd` (no --resume): generate new session ID, `lastOffset = 0`.
- `lastOffset` stored as `cli.sessionOffset int`.

## Test points

### Unit tests (06-2)

All tests use `t.TempDir()` for the store directory — no real filesystem
paths, no cleanup needed.

### Integration tests (06-3)

These live in `cli/cli_test.go` and test the REPL + session interaction:

1. **REPL_autoSave**: run 2 turns via REPL, verify `store.Load` returns
   the full history after the second turn (store received 2 incremental Appends).
2. **REPL_reset_deletesSession**: run 1 turn, `/reset`, verify
   `store.Load` returns `os.ErrNotExist`.
3. **Run_resume_loadsHistory**: append 2 messages to a session, run
   `--resume`, verify the LLM sees the previous turns.

## Risks

- **Session file growth**: a 30-step session with large tool outputs
  can produce a ~1 MB JSONL file. Acceptable for MVP; compression is
  a follow-up.
- **ID collision**: `date-random4` has ~65K combinations per day.
  Collisions overwrite silently. Acceptable for single-user MVP.
- **No locking**: single-process, sequential access. Fine for MVP.
- **Append atomicity**: `O_APPEND` is atomic for small writes on most
  OSes, but large tool outputs (> PIPE_BUF) may interleave with
  concurrent writes. Acceptable since the agent loop is sequential.

## Not in scope

- Session encryption / authentication
- Session compression (gzip)
- Session export / import
- Multi-user session isolation
- Summary persistence (deferred to 04-4c implementation)

---

### 实现笔记 06-1 — Store 接口 + FileStore

**Files**: `internal/session/store.go` (31 lines), `internal/session/filestore.go` (169 lines)

**Store 接口**:
- `Append(ctx, id, messages)` — 调用方传增量 messages,store 只 append
- `Load(ctx, id)` — 读完整 history
- `List(ctx)` — 所有 session ID,按 mtime 倒序
- `Delete(ctx, id)` — 删除文件,idempotent

**FileStore 实现**:
- `NewFileStore(dir)` — 创建目录(0700),返回 store
- `Append`: `os.O_APPEND|O_CREATE` + `bufio.Writer` + `f.Sync()`,每条 message
  marshal 成 JSON 写一行
- `Load`: `bufio.Reader.ReadBytes('\n')` 流式读取,支持任意长度行(大 tool output)
- `List`: `filepath.Glob("*.jsonl")` + sort by mtime
- `Delete`: `os.Remove`,`os.IsNotExist` 不算错
- `NewID()`: `<date>-<random4>`,`crypto/rand` 取 16-bit

**偏差**:
- Spec 原计划用 `bufio.Scanner`(1MB buffer),实现发现 tool output 可能超过
  1MB,改为 `bufio.Reader.ReadBytes` 支持任意长度行
- `TestFileStore_AppendLargeOutput` 1.5MB message 验证

**自验**: `make test` 11/11 session tests PASS;`make test ./...` 90+ total PASS;
`make lint` clean。

### 实现笔记 06-3 — Agent 接入 session

**Files**: `internal/agent/agent.go` (+139 行), `internal/agent/agent_test.go` (+236 行),
`cmd/chaosbot/cli/cli.go` (+43 行), `cmd/chaosbot/wire.go` (+22 行),
`cmd/chaosbot/cli/cli_test.go` (+62 行)

**新增接口**:
- `Agent` interface 新增 `Resume(ctx, id string) error` 和 `SessionID() string` 方法
- `reActAgent` 新增 `Store session.Store` DI 注入字段
- `reActAgent` 新增 `sessionID string` 和 `sessionOffset int` internal 状态字段

**Run 集成**:
- 首次 `Run` 成功时,若 `Store != nil` 且 `sessionID == ""`,生成 `session.NewID()` 并保存
- 每轮 `saveOnSuccess` 调用 `Store.Append(ctx, sessionID, history[sessionOffset:])` 增量持久化
- `sessionOffset = len(history)` 跟新历史对齐,下次 Append 只写增量
- `Reset`:若 `Store != nil && sessionID != ""` 则 `Store.Delete` 删除 session 文件并重置 offset

**Resume 集成**:
- `Resume(ctx, id)` 调用 `Store.Load` 恢复完整 history 到 `a.History`
- 调用 `Store.LoadSummary` 恢复 summarization state (`summaryMsg`, `summaryCursor`)
- `sessionOffset = len(history)` 与 store 对齐

**Bug fix (关键)**:
- 原始 bug:窗口化直接修改原 `history` slice,破坏 `sessionOffset` 与 store 的对应关系
- 修复:窗口化只应用于传给 LLM 的视图,不修改原始累计 `history`
- `history = append(a.History, ...)` 不修改 `a.History` 直到 `saveOnSuccess` 成功后

**CLI 变更** (`cb47db7`):
- `runCmd` 新增 `--session <id>` flag,调用 `agent.Resume` 恢复 session
- REPL 内部自动处理 auto-save,无需 CLI 知道 Store
- `wire.go`:注册 `FileStore` via DI,缺失或初始化失败时降级 `NoopStore`
- `fakeAgent` 更新匹配新接口(`Resume` + `SessionID`)

**测试覆盖**(8 测,全 PASS):
- auto-save on Run success
- no-store fallback (nil Store = no-op)
- Reset deletes session
- Resume loads and continues
- Resume errors on missing session
- Resume errors on corrupt session
- windowing does not break session offset
- sessionOffset stays aligned after multi-turn run

**Layering**:
- `internal/agent/agent.go` 新增 import `chaosbot/internal/session`
- `cmd/chaosbot/wire.go` 注册 `session.FileStore` DI
- `internal/session` 是 leaf 包,不反向依赖 `agent`

**自验**: `make test` 90+ PASS;`make lint` clean;`gofmt -l .` clean。
