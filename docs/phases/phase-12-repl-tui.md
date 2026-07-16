# Phase 12: REPL 重构为 Bubbletea TUI

## Frontmatter

| Field | Value |
|---|---|
| Phase | `12` |
| Sub-units | `12-1` … `12-5` |
| Status | `⬜ not started` |
| Owner | chaosbot authors |
| Pre-requisites | Phase 07 (REPL), Phase 08-3 (liner) |
| Estimated total LOC | ~500 Go LOC across all sub-units |
| Performance impact | TBD; bubbletea runtime overhead minimal |

## Goal

将 chaosbot REPL 从 liner 迁移到 bubbletea TUI 框架，获得：
1. **状态栏**：显示 model、context 使用率等实时信息
2. **Tool Trace**：LLM 调用工具时实时显示
3. **Shift+Enter 多行输入**：符合直觉的换行方式
4. **多行粘贴**：一次粘贴完整内容，不被拆分成多个命令

## Public API / interface

```go
// cmd/chaosbot/tui/main.go
func Run(a agent.Agent, cfg *config.Config) error
```

## Data structures

```go
// cmd/chaosbot/tui/model.go
type Model struct {
    Input      string
    CursorRow  int
    CursorCol  int
    Agent      agent.Agent
    History    []Message
    Pending    bool
    ToolCalls  []ToolCall
    ModelName  string
    ContextUsed, ContextMax, ContextPct int
    ToolCount  int
    Width, Height int
}

type Message struct {
    Role    string
    Content string
}

type ToolCall struct {
    Name, Args, Status, Detail string
}
```

## Sub-units

| 子单元 | 内容 | 目标 |
|--------|------|------|
| 12-1 | 骨架：Model + View + Update 框架 | 可启动空 TUI |
| 12-2 | Agent 集成：goroutine + 事件通道 | 对话功能可用 |
| 12-3 | Tool Trace 实时显示 | 工具调用可见 |
| 12-4 | 状态栏 + 上下文使用率 | 实时信息显示 |
| 12-5 | 兼容性 + 测试 | `/reset` 等命令保留 |

## 实现笔记

> _待填充_