# Phase 03 — Agent tool surface (3 sub-units)

> The tool boundary. `internal/agent` exposes `agent.Tool` and
> `agent.Registry`; `internal/tools/<x>` implements them.

## Frontmatter

| Field | Value |
|---|---|
| Phase | `03` |
| Sub-units | `03-1` … `03-3` |
| Status | `⬜ not started` |
| Owner | chaosbot authors |
| Pre-requisites | Phase 02 (`provider.ToolSpec` exists in `internal/provider/provider.go`) |
| Estimated total LOC | ~80 Go + ~150 test LOC across 3 sub-units |
| Performance impact | none; `Registry` is a `map` + slice, no new direct deps |

## Goal

Define the tool boundary behind a `agent.Tool` interface, ship a
`Registry` that the agent loop (Phase 04) can ask for "all tool
specs to send to the LLM" and "invoke this tool by name", and cover
both with table-driven tests.

`internal/agent` depends on `provider.ToolSpec` (for the `Specs()`
output), and **nothing else** from `provider`. Concrete tools
(`internal/tools/<area>`) depend on `agent` only — never on
`provider` or on each other.

## Public API / interface (target shape, filled by 03-1 + 03-2)

```go
package agent

import (
    "context"
    "encoding/json"
    "errors"

    "chaosbot/internal/provider"
)

// Tool is the boundary between the agent loop and one capability
// (filesystem, shell, web, time, ...).
type Tool interface {
    Name() string                              // model-visible identifier
    Description() string                       // short doc for the LLM
    Parameters() json.RawMessage               // JSON Schema for the args
    Invoke(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry holds the set of tools available to one session.
type Registry struct { /* map[string]Tool */ }

func NewRegistry() *Registry
func (r *Registry) Register(t Tool)
func (r *Registry) Specs() []provider.ToolSpec
func (r *Registry) Names() []string
func (r *Registry) Invoke(ctx context.Context, name string, args json.RawMessage) (string, error)

var ErrToolNotFound = errors.New("agent: tool not found")
```

## Data structures

- `Tool` is the single interface every capability must satisfy. **Three
  getters + one executor** (not a single `Spec() provider.ToolSpec`):
  the split keeps concrete tools free of any `provider` import, and
  matches what the agent loop actually does (read 3 fields, then call
  1 method).

- `Registry` is a `map[string]Tool` plus a constructor and dispatch
  methods. **Not safe for concurrent use** — same model as
  `provider.Provider`; the agent loop is single-threaded per
  `architecture.md` §6. The constructor is **no-arg** (`NewRegistry()`);
  tools are added via `Register` (one-at-a-time, last-write-wins),
  matching Go idiom (`http.ServeMux` / `flag.FlagSet` /
  `sync.WaitGroup`). Composition root in `main.go` does
  `for _, t := range defaultTools { r.Register(t) }`.

- `Registry.Specs()` (sub-unit 03-2) builds `[]provider.ToolSpec` from
  the registered tools. **Order is unspecified** (map iteration);
  `Names()` returns a sorted copy for deterministic display in
  `chaosbot tools` and REPL `/tools`.

- `ErrToolNotFound` is a package-level sentinel returned by `Invoke`
  when the name isn't registered. Wrapped with `%w` and the name
  appended: `fmt.Errorf("%w: %q", ErrToolNotFound, name)`. The agent
  loop surfaces it to the LLM as a tool message (the model gets to
  recover); it does **not** abort the loop.

## Test points

| Test | Sub-unit |
|---|---|
| `TestTool_InterfaceSatisfied` (compile-time `var _ Tool = (*fakeTool)(nil)`) | 03-3 |
| `TestNewRegistry_Empty` | 03-3 |
| `TestNewRegistry_RegistersAll` | 03-3 |
| `TestNewRegistry_DuplicateName_SecondWins` | 03-3 |
| `TestRegistry_Register_Adds` | 03-3 |
| `TestRegistry_Register_Overwrites` | 03-3 |
| `TestRegistry_Specs_MatchesToolFields` | 03-3 |
| `TestRegistry_Specs_Empty` | 03-3 |
| `TestRegistry_Names_Sorted` | 03-3 |
| `TestRegistry_Invoke_DispatchesAndReturnsResult` | 03-3 |
| `TestRegistry_Invoke_PropagatesToolError` | 03-3 |
| `TestRegistry_Invoke_NotFound_ReturnsErrToolNotFound` | 03-3 |
| `TestRegistry_Invoke_RespectsContextCancellation` | 03-3 |
| `TestRegistry_Invoke_PassesRawArgsUnchanged` | 03-3 |

## Risks

- **Tool interface bloat**. Tempting to add a single
  `Spec() provider.ToolSpec` later; resist. Keep the interface small
  so 3rd-party tools don't have to import `internal/provider`.

- **Duplicate names in `Register`**. Three choices: skip / overwrite
  / panic. **Proposed: overwrite silently** (last-write-wins). Reasoning:
  same-name tools in different packages are usually a copy-paste bug;
  the right behavior is "last one wins, devs notice via the registry"
  rather than crash at startup, while still being deterministic
  (no random map order surprises). The same rule covers REPL
  `/tools` reloads.

- **Ordered vs map-backed Registry**. A `[]Tool` would preserve
  insertion order at O(n) `Invoke` lookup. Map wins for O(1) dispatch;
  `Names()` returns sorted for deterministic display.

- **Concurrency**. Not safe for concurrent use, by design. If a later
  phase adds fan-out, switch to `sync.RWMutex` at the same time as
  `provider.Provider` (so the invariant stays "agent-loop-only").

## Performance impact

- `NewRegistry`: O(n) memory copy.
- `Specs()`: O(n) slice build, O(n) garbage (negligible vs the LLM
  round-trip that follows).
- `Invoke`: O(1) map lookup + tool execution.
- `Names()`: O(n log n) sort over a small slice (n ≤ ~10 tools in MVP).

No new direct dependencies. Stays within the 8-depend budget
(currently 1, `go-openai`).

## Sub-units

- `03-1`  `agent.Tool` 接口 + `Registry` 类型 + `NewRegistry` + `Register`
  + `Invoke` + `ErrToolNotFound`(`internal/agent/tool.go`,估计 ~70 Go LOC)
- `03-2`  `Registry.Specs()` + `Registry.Names()`(`[]provider.ToolSpec` 转换,
  同一文件,估计 ~25 Go LOC)
- `03-3`  全套单测(表驱动 + 手写 `fakeTool`,`internal/agent/tool_test.go`,
  估计 ~150 Go LOC)

## 实现笔记

> Filled as each sub-unit lands.

### 03-1 — `agent.Tool` 接口 + `Registry`

- `internal/agent/tool.go` **~63 行**(纯代码)。
  - `Tool` interface:4 方法(`Name` / `Description` / `Parameters` / `Invoke`),
    godoc 齐全,所有参数都有解释。
  - `Registry` struct:`map[string]Tool` 一个字段,零值不可用,强制走 `NewRegistry`。
  - `NewRegistry() *Registry`:**无参**(Option B,跟 Go 惯用法对齐
    `http.ServeMux` / `flag.FlagSet` / `sync.WaitGroup`);map 不预分配
    (工具个位数,无意义)。
  - `Register(t Tool)`:唯一的添加工具路径;last-write-wins 处理重名
    (REPL `/tools` reload 用)。
  - `Invoke(ctx, name, args) (string, error)`:O(1) map 查找,未命中时
    `fmt.Errorf("%w: %q", ErrToolNotFound, name)` 包装,`errors.Is` 可识别。
  - `ErrToolNotFound` sentinel:`var ErrToolNotFound = errors.New("agent: tool not found")`。
- **零外部依赖**。仅 import `context` / `encoding/json` / `errors` / `fmt`(全 stdlib)。
- **零 `provider` import**(本单元)。`provider.ToolSpec` 在 03-2 引入。
- **Layering 校验**:`grep -l "internal/provider" internal/agent/*.go` → 空。
  符合 `architecture.md` 第 22 行规则。
- **并发契约**:`Registry` 非并发安全,与 `provider.Provider` 一致。
  agent loop 单线程 per `architecture.md` §6。
- **设计回退记录**:初版是 `NewRegistry(tools []Tool) + Register`,
  评审时被指出"两条添加路径"是冗余,改为无参构造 + 单一 `Register`。
  main.go 写法 `for _, t := range defaultTools { r.Register(t) }`。
- 自验:`go build ./...` / `go vet ./...` / `gofmt -l .` 全绿;
  `go test -race -count=1 ./...` 13/13 PASS(agent 包 `[no test files]`,
  03-3 补)。

**已知偏差(已修)**:`make build` 在 03-1 之前是坏的(自 Phase 02-3 引入
`internal/provider` 之后),原因是 Makefile 的 `$(PKG) := ./...` 配合 `-o $(BIN)`
(文件路径)对多 package 项目非法。修法:Makefile 新增 `BUILD_PKG := ./cmd/chaosbot`,
build 目标改用 `$(BUILD_PKG)`,test/lint 仍走 `$(PKG) := ./...`。验证
`make build` 输出 1.5 MB 二进制,`make test` 13/13 PASS,`make lint` clean。

### 03-2 — `Registry.Specs()` + `Registry.Names()`

- 同一文件 `internal/agent/tool.go`,**新增 27 行**(含 godoc)。
  - `Specs() []provider.ToolSpec`:遍历 `r.tools` map,构造 `[]provider.ToolSpec`。
    map 迭代顺序,**未指定**;调用方别依赖顺序。`out` 容量预分配 `len(r.tools)`,
    零分配热路径。
  - `Names() []string`:遍历 map 收集 name,`sort.Strings` 排序后返回(用于
    `chaosbot tools` / REPL `/tools` 列表展示)。
- **首次引入 `internal/provider` import**。Layering 校验:
  - `grep "internal/provider/openai" internal/agent/*.go` → 空(没有 concrete impl 依赖)
  - `grep "internal/provider" internal/agent/*.go` → 只有 `tool.go` 里的 `"chaosbot/internal/provider"`,
    符合 `architecture.md` 第 22 行(只允许 import 接口包,禁 import concrete)。
- 性能:`Specs()` 一次 Chat round 调一次,O(n) slice 构造,n ≤ 10 时可忽略;
  `Names()` 调频更低(CLI/REPL 展示),O(n log n) 排序。
- 自验:`make build` 1.5 MB / `make test` 13/13 PASS / `make lint` clean。

### 03-3 — `agent` 包单测套件

- `internal/agent/tool_test.go` **187 行**,外部 test 包 `agent_test`(同 `provider_test.go` 的模式)。
- **`fakeTool` 手写测试桩**(25 行):4 方法 + 1 字段(`invokeFunc`)让测试程序化返回 / 错误,
  另 3 字段(`calls` / `lastCtx` / `lastArgs`)让测试断言调用参数。
  编译期断言 `var _ agent.Tool = (*fakeTool)(nil)`。
- **10 个测试**(全 PASS),覆盖:
  - `TestNewRegistry_Empty` — 空 registry 的 `Names()` / `Specs()` 都是空 slice
  - `TestRegister_Adds` — Register 单 tool
  - `TestRegister_Overwrites` — 同名后写覆盖(`Specs()` 也确认 `Description` 跟第二个)
  - `TestSpecs_MatchesToolFields` — `Name` / `Description` / `Parameters` 三个字段一对一
  - `TestNames_Sorted` — `Names()` 排序(注册顺序 `zebra / alpha / mango` → 输出 `alpha / mango / zebra`)
  - `TestInvoke_DispatchesAndReturnsResult` — happy path,计数验证
  - `TestInvoke_PropagatesToolError` — tool 错误透传(`errors.Is` 验证)
  - `TestInvoke_NotFound_ReturnsErrToolNotFound` — 未命中时 `errors.Is(err, agent.ErrToolNotFound)` 且 error 字符串含 name
  - `TestInvoke_RespectsContextCancellation` — `ctx.Done()` 取消时返回 `context.Canceled`(channel 同步,无 sleep 不 flaky)
  - `TestInvoke_PassesRawArgsUnchanged` — `args` 透传不解析
- **每个测试独立构造 fakeTool**,无共享 state,无 `t.Parallel()`(MVP 简单优先)。
- **未用第三方断言库**,全 stdlib `if/else` + `t.Errorf`。
- 自验:`go test -race -count=1 -v ./internal/agent/...` 10/10 PASS;
  `make test` 23/23 PASS(10 agent + 11 provider + 2 openai);
  `make lint` clean。

**Follow-up**:`fakeTool` 当前就地,Phase 04 写 agent loop 集成测时如果还要同款
fake,提到 `internal/agent/testdata/fake_tool.go`(跨测试文件共享)。
当前 MVP 只 agent 自身用,先就地。
