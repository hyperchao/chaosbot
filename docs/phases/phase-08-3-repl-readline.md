# Phase 08-3 — REPL readline（历史 + 行编辑 + Tab 补全）

## Frontmatter

| Field | Value |
|---|---|
| Phase | `08-3` |
| Sub-units | `08-3` |
| Status | `✅ complete` (1 sub-unit done; see 实现笔记) |
| Owner | chaosbot authors |
| Pre-requisites | Phase 07-4 (REPL) |
| Estimated total LOC | ~120 Go LOC |
| Performance impact | +2 直接依赖 (`github.com/peterh/liner` + `golang.org/x/term`) |

## Goal

替换 REPL 中的 `bufio.Scanner` 为 readline 库，提供：
1. **行编辑** — 左右移动、删除、退格
2. **历史翻查** — ↑/↓ 箭头键翻历次输入
3. **Tab 自动补全** — 补全 `/` 命令 (`/reset`, `/exit`, `/help`) + tool 名称

## Public API / interface

无新增公共 API。`CLI.replCmd()` 内部实现变更，外部行为不变。

## Data structures

### 新增 struct：`replCompleter`

```go
type replCompleter struct {
    tools []string // tool names from agent.Registry
}

func (c *replCompleter) Do(line []rune, pos int) (newLine [][]rune, offset int)
```

静态补全列表：
- `/reset`, `/exit`, `/quit`, `/help`, `/tools` — 始终可用
- tool 名称列表 — 从 `agent.Registry.Names()` 获取

## Dependencies

新增：`github.com/chzyer/readline` v1.5 — 纯 Go，无 CGO，无外部 C 库依赖。当前 4 直接依赖，新增后 5 ≤ 8 预算。

为什么 stdlib 不够：Go stdlib 没有 terminal raw mode + line editing 能力。`bufio.Scanner` 不支持 arrow keys / history / tab completion（需要 VT100 转义序列解析 + 终端 raw mode）。

## Test points

| Test | 描述 |
|---|---|
| `TestReplCompleter_SlashCommands` | Tab 以 `/` 开头 → 列出所有命令 |
| `TestReplCompleter_ToolNames` | Tab 非 `/` 开头 → 模糊匹配工具名 |
| `TestReplCompleter_NoMatch` | 无匹配 → 不补全，返回 nil |
| REPL smoke test | 启动 REPL、输入、箭头键历史、Tab 补全、`/exit` |

## Risks

- `chzyer/readline` 在 macOS Terminal.app + Linux xterm 兼容性好；Windows cmd.exe / PowerShell 可能需要额外终端模拟器（已知限制，非阻塞）。
- readline 库接管了 stdin/stdout，`c.In`/`c.Out` DI 注入的 `os.Stdin`/`os.Stdout` 需要在 readline 实例构造时传入而非复用 `bufio.Scanner`。

## Sub-units

- `08-3` REPL readline ✅

## 实现笔记

_（待实现后填写）_

### 08-3 — REPL readline

**实际实施：**

- **替换**：`chzyer/readline` → `peterh/liner` v1.2.2（更稳定，docker/geth 在使用；修复 macOS Terminal Ctrl-A 光标定位 + 左右箭头双击问题）
- 新增 `golang.org/x/term` v0.30.0 用于跨平台 `IsTerminal` 检测
- `replCmd()` 通过 `*os.File` 类型断言 + `term.IsTerminal` 检测终端：
  - 终端 → `replLiner()`（history + 行编辑 + Tab 补全）
  - 非终端 → `replScanner()`（原 bufio.Scanner，pipe/CI/test 兼容）
- `replComplete` 替代 `replCompleter`：liner 的 completer 签名更简单（`func(string) []string`）
- dispatch 逻辑抽出为 `replDispatch()`，两个循环共享

**偏差：**
- 改用 `liner` 替代 spec 中计划的 `chzyer/readline`：后者在 macOS 上存在已知光标定位问题（Ctrl-A 不到行首、方向键需按两次），且已 2 年未维护
- 直接依赖从 4 → 6，仍在 ≤8 预算内
- tab 补全仅支持 `/` 命令（暂不支持 tool 名，因 `agent.Agent` 接口不暴露工具列表）

**提交指针：** 待提交
