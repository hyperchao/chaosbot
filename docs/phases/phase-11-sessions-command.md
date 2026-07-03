# Phase 11 — `/sessions` 命令

> 编号列表会话选择器：打印会话列表，输入编号确认恢复。

## Frontmatter

| Field | Value |
|---|---|
| Phase | `11` |
| Sub-units | `11-1` … `11-2` |
| Status | `✅ complete` |
| Owner | chaosbot authors |
| Pre-requisites | Phase 06 (session persistence), Phase 07 (CLI) |
| Estimated total LOC | ~120 Go |
| Actual LOC | ~135 Go |

## Goal

在 REPL 中添加 `/sessions` 命令，列出并恢复会话：

1. **编号列表**：打印所有会话（ID + 摘要预览）
2. **数字选择**：输入编号后回车确认恢复
3. **即时恢复**：确认后调用 `Agent.Resume()`

## UI Design

```
> /sessions
1. 20240630-3a2f  [no summary]
2. 20240629-1b4c  Go concurrency patterns...
3. 20240628-7d8e  auth module refactor...
enter number to resume (or Ctrl-C to cancel): 2
resuming 20240629-1b4c...
```

交互流程：
1. `/sessions` 打印编号列表（摘要超长截断至 50 字符）
2. 提示输入编号，输入后回车
3. 空输入/Ctrl-C 取消，返回 REPL
4. 无效编号提示错误并返回 REPL

## Public API

### CLI 结构变更

```go
// cmd/chaosbot/cli/cli.go
type CLI struct {
    Agent    agent.Agent      `di:"type"`
    Registry *agent.Registry  `di:"type"`
    Config   *config.Config  `di:"type"`
    Store    session.Store   `di:"type"`   // NEW: 注入 session store
    In       io.Reader       `di:"alias:in"`
    Out      io.Writer       `di:"alias:out"`
    ErrOut   io.Writer       `di:"alias:errout"`
    Version  string          `di:"alias:version"`
}
```

### Wire 变更

无需修改 wire.go：CLI struct 添加 `di:"type"` tag 后，di 库自动填充 Store（wire.go 已注册 session.Store）。

### REPL Slash Command

```
/sessions              # 打开交互式选择器
```

### Error Cases

| Case | Output |
|------|--------|
| No sessions dir configured | `sessions: no session store configured (set sessions_dir in config)` |
| No sessions exist | `sessions: no saved sessions` |
| Resume failed | `sessions: resume <id>: <error>` |
| Ctrl+C / Esc | 取消，返回 REPL |

## Implementation Strategy

### 编号列表方案

1. 调用 `Store.List()` 获取所有会话 ID（按 mtime 新到旧）
2. 对每个 ID 调用 `Store.LoadSummary()` 获取摘要
3. 打印编号列表，摘要截断至 50 字符
4. `bufio.Reader.ReadString('\n')` 读取用户输入
5. `fmt.Sscanf` 解析数字，无效输入提示错误
6. 调用 `Agent.Resume()` 恢复选中会话

### 数据获取

```go
ids, err := c.Store.List(ctx)
items := make([]sessionItem, 0, len(ids))
for _, id := range ids {
    summary, _ := c.Store.LoadSummary(ctx, id)
    items = append(items, sessionItem{id: id, summary: summary.Content})
}
```

## Sub-units

### 11-1 — 核心实现

- `cmd/chaosbot/cli/cli.go`:
  - 添加 `Store session.Store` 字段
  - 添加 `sessionsCmd() error` 方法（编号列表交互）
  - 添加 `readLine() string` 辅助方法
  - REPL 中添加 `/sessions` slash command 分支

### 11-2 — 测试

- `cmd/chaosbot/cli/cli_test.go`:
  - `TestReplComplete_SlashPrefix` 更新（6 条目）
  - 所有 `buildCLI`/`buildREPL` 注册 `fakeStore`

## Test Points

| Test | Sub-unit | Type |
|---|---|---|
| `TestReplComplete_SlashPrefix` | 11-1 | unit |
| `TestREPL_SessionsCommand_Help` | 11-2 | integration |

## Key Design Decisions

### 1. 为什么不用 TUI 光标交互

纯手工 ANSI escape 序列做 TUI 定位/清屏/键盘交互非常脆弱，不同终端环境表现不一致。编号列表只需打印 + 读数字，可靠性高一个量级。

### 2. 不支持删除会话

保持 MVP 简洁。删除会话可后续添加 `/sessions delete` 命令。

## Performance Impact

- `Store.List()` + `Store.LoadSummary()` 循环
- N 个会话 = N+1 次文件 stat/read
- 对于 100 个会话，总 I/O < 1 秒（SSD）

## Dependencies

无新增依赖。复用 stdlib `bufio`、`fmt`、`strings`。

## Implementation Notes

### 11-1 — Core implementation

`sessionsCmd` + `readLine` 方法加在 `versionCmd` 之后；`sessionItem` struct 加在 `sessionsCmd` 之前。commit `tbd`.

### 11-2 — Tests

`fakeStore` 实现 `session.Store` 接口；所有 test helper 注册 `fakeStore`。commit `tbd`.
