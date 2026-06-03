# Phase 04 — Agent loop (3 sub-units)

> The ReAct loop. `agent.Run` orchestrates `provider.Provider` and
> `agent.Registry` to produce a final answer.

## Frontmatter

| Field | Value |
|---|---|
| Phase | `04` |
| Sub-units | `04-1` … `04-3` |
| Status | `✅ complete` (all 3 sub-units done; see 实现笔记) |
| Owner | chaosbot authors |
| Pre-requisites | Phase 03 (Tool/Registry), Phase 02-5 (Request.Validate) |
| Estimated total LOC | ~370 Go (60 + 160 + 150) |
| Performance impact | none new; allocations bounded by `len(history)` per Chat round |

## Goal

Ship the ReAct loop: take a user input, call the LLM with the current
history, dispatch any tool calls the model requested, feed results
back, repeat until the model produces a final answer (or hit a
termination condition).

`internal/agent` is the **only** place that orchestrates this. CLI
(`cmd/chaosbot`) and session persistence (`internal/session`) just
hand the Agent a user string and a session id.

## Key design decision: reuse `provider.Message`

**Decision**: do **not** introduce a new `agent.Message` type. The
agent loop's history IS `[]provider.Message`. Constructor helpers
(`agent.NewUserMessage` / `NewAssistantMessage` / `NewToolMessage`)
live in the `agent` package and return `provider.Message` values,
covering the readability concern without duplicating the type.

| Option | Pros | Cons |
|---|---|---|
| **A. Reuse `provider.Message`** ✅ | Zero conversion; one source of truth; session serializes the same struct the LLM sees | Agent state IS the wire format (YAGNI separation) |
| B. New `agent.Message` + conversion | Independent evolution; future agent-only fields (timestamps, IDs) possible | Translation noise at every boundary; **agent-only fields never used in MVP** |
| C. `type Message = provider.Message` alias | Same as A | No value over importing `provider.Message` directly |

Future separation: if agent-only fields are needed, add a wrapper
(`agent.HistoryEntry{ Msg provider.Message; At time.Time; ID string }`).
**Not now**.

## Public API (target shape, filled across 04-1..04-3)

```go
package agent

import "chaosbot/internal/provider"

// 04-1: constructor helpers
func NewUserMessage(content string) provider.Message
func NewAssistantMessage(content string, toolCalls []provider.ToolCall) provider.Message
func NewToolMessage(toolCallID, name, content string) provider.Message

// 04-3: Agent
type Agent struct {
    Provider    provider.Provider
    Registry    *Registry
    System      string
    Model       string
    Temperature float64
    MaxTokens   int
    MaxSteps    int  // 04-3
}

func (a *Agent) Run(ctx context.Context, userInput string) (string, error)
```

The `step` function (04-2) is **unexported**: `func (a *Agent) step(...)`.
It's exercised directly by 04-2 tests; 04-3 tests `Run` end-to-end.

## Data flow (one iteration of the loop)

```
Agent.Run(ctx, userInput):
  history = []provider.Message{}
  for step in 0..MaxSteps:
    if ctx.Err() != nil: return "", ctx.Err()

    newHistory, final, err := a.step(ctx, history, NewUserMessage(userInput))
    if err != nil: return "", err        // provider / build error is fatal
    history = newHistory
    if final != "": return final, nil   // assistant didn't request tools

    userInput = ""  // only used on the first iteration
  return "", ErrMaxSteps
```

`step` body (04-2):

```
step(ctx, history, userMsg):
  msgs = append(history, userMsg)        // 1 alloc
  req = provider.Request{
    System: a.System,
    Messages: msgs,
    Tools: a.Registry.Specs(),
    Model: a.Model,
    Temperature: a.Temperature,
    MaxTokens: a.MaxTokens,
  }
  if err := req.Validate(); err != nil: return nil, "", err  // boundary check
  resp, err := a.Provider.Chat(ctx, req)
  if err != nil: return nil, "", fmt.Errorf("agent: chat: %w", err)

  history = append(history, NewAssistantMessage(resp.Content, resp.ToolCalls))
  if len(resp.ToolCalls) == 0:
      return history, resp.Content, nil   // terminal

  for _, call := range resp.ToolCalls:
    result, _ := a.Registry.Invoke(ctx, call.Name, call.Arguments)
    // tool errors are NOT Go-level errors — they go in the tool message
    // so the LLM can decide how to react (per architecture.md §4)
    history = append(history, NewToolMessage(call.ID, call.Name, result))
  return history, "", nil
```

Termination conditions (04-3):

| Condition | Behavior |
|---|---|
| Assistant final content (no tool calls) | Return content, nil |
| `MaxSteps` reached | Return `ErrMaxSteps` (sentinel) |
| `ctx.Done()` | Return `ctx.Err()` |
| `provider.Chat` returns Go error | Wrap & return |
| `Request.Validate()` returns error | Wrap & return |
| Tool returns Go error | Embedded in tool message, **loop continues** |

`ErrMaxSteps` sentinel is new; lives in `agent.go` (04-3).

## Sub-units

- `04-1`  `NewUserMessage` / `NewAssistantMessage` / `NewToolMessage` + tests
  (`internal/agent/message.go` + `message_test.go`,~60 Go LOC)
- `04-2`  `(a *Agent) step` 方法 + 单元测
  (`internal/agent/step.go` + `step_test.go`,~160 Go LOC)
  - **注意**:`step` 是 `*Agent` 的方法,所以 04-2 需要先在 `agent.go` 放
    `Agent` struct 的字段(`Provider` / `Registry` / `System` / `Model` /
    `Temperature` / `MaxTokens`),`MaxSteps` 留到 04-3 补。04-2 还没 `Run`。
- `04-3`  `MaxSteps` 字段 + `ErrMaxSteps` sentinel + `(a *Agent) Run`
  方法 + 集成测
  (`internal/agent/agent.go` + `agent_test.go`,~150 Go LOC)

## Test points

| Test | Sub-unit | Type |
|---|---|---|
| `TestNewUserMessage` / `TestNewAssistantMessage` / `TestNewToolMessage` (3-5 each, table) | 04-1 | unit |
| `TestStep_FinalAnswerNoTools` | 04-2 | unit, fakeProvider + fakeTool |
| `TestStep_OneToolCall_AppendsToolMessage` | 04-2 | unit |
| `TestStep_MultipleToolCalls` | 04-2 | unit |
| `TestStep_ToolError_EmbeddedInToolMessage` | 04-2 | unit (error is in content, not Go error) |
| `TestStep_ProviderError_BubblesUp` | 04-2 | unit |
| `TestStep_ValidateFails_BubblesUp` | 04-2 | unit (passes `Request.System` + leading system msg) |
| `TestRun_FinalAnswerFirstStep` | 04-3 | integration, fakeProvider |
| `TestRun_TwoStepReActLoop` | 04-3 | integration (LLM asks for tool, gets result, answers) |
| `TestRun_MaxStepsReached` | 04-3 | integration, 3-step budget, model never finalizes |
| `TestRun_ContextCanceled` | 04-3 | integration, cancel mid-loop |
| `TestRun_PassesSystemPrompt` | 04-3 | integration, asserts captured `Request.System` |

## Risks

- **`step` mutates `history` vs returns new slice**. Returning new
  keeps the call site explicit (`history = newHistory`); mutating is
  faster. Decision: **return new**; the per-step cost of one
  slice growth is noise vs the LLM round-trip.

- **Tool error → tool message vs Go error**. Per `architecture.md` §4
  and the spec, tool errors are surfaced to the LLM as tool
  messages. The loop continues. The user gets a chance to retry,
  switch tools, or give up. 04-2 tests this explicitly.

- **`MaxSteps = 0` or negative**. Treated as "use default" (10) in
  04-3? Or panic? Decision: **treat ≤ 0 as 10**; matches "do
  something reasonable" UX. Documented in godoc.

- **Concurrency**. `Agent.Run` is sequential by design. `Agent` itself
  has no mutex; safe to call `Run` from one goroutine at a time. Same
  contract as `Registry` / `Provider`.

## Performance impact

- `step`: 1 slice growth (history) + 1 Chat call + 0..N tool
  invocations + 0..N slice appends. Per `MaxSteps`, this is bounded.
- `Specs()`: same as 03-2 (1 alloc per step, ~560 B).
- History growth: amortized O(1) (Go's slice doubling).
- No new direct dependencies. Stays within the 8-depend budget.

## 实现笔记

> Filled as each sub-unit lands.

### 04-1 — `provider.Message` 构造函数

- `internal/agent/message.go` **27 行**,3 个构造函数,无外部 import(`provider`)。
- **不引入 `agent.Message` 类型** — 决策落在 spec "Key design decision" 一节:
  复用 `provider.Message`,构造函数返回的就是 `provider.Message` 值。
- 3 个 helper:
  - `NewUserMessage(content string) provider.Message` — `Role: RoleUser`,其他字段零值。
  - `NewAssistantMessage(content string, toolCalls []provider.ToolCall) provider.Message` —
    `Role: RoleAssistant`,`toolCalls` 透传(nil / 空 slice / 多元素 都行,wire 层根据
    `len > 0` 决定是否发送)。
  - `NewToolMessage(toolCallID, name, content string) provider.Message` —
    `Role: RoleTool`,3 字段全部填(LLM 用 `ToolCallID` 匹配 assistant 轮调用的 `ID`,
    用 `Name` 知道是哪个 tool 出的结果)。
- **未提供 `NewSystemMessage`**:system prompt 走 `provider.Request.System` 顶层字段
  (per Phase 02-5 互斥契约),message list 不放 system message。如果将来 session 加载
  真的需要把 system 重建到 Messages 里,再加。
- **零外部依赖**。仅 import `chaosbot/internal/provider`(已知)。
- 自验:`make build` 1.5 MB / `make test` 23/23 PASS(agent 包 13:10+3,
  2/3 测 + 1/3 sub-cases);`make lint` clean。

### 04-2 — `(a *Agent) step` 方法 + Agent struct 字段

**新增文件**:
- `internal/agent/agent.go` **68 行**:`Agent` struct(`Provider` / `Registry` /
  `System` / `Model` / `Temperature` / `MaxTokens`,**无 `MaxSteps`**——04-3 补)
  + `(a *Agent) step(ctx, history) (newHistory, finalContent, err)` 方法。
- `internal/agent/agent_test.go` **215 行**:6 个 step 单元测(internal test 包
  `package agent`,因为 `step` 是 unexported,外部包调不到)。

**附带 refactor(fakeProvider + fakeTool 共享化)**:
- `internal/provider/fake/fake.go` **49 行**:`Provider` 导出版本(原 `fakeProvider`
  unexported 留在 `provider_test.go`,无法跨包 import)。AGENTS.md 写明
  "do not duplicate";之前的 canonical 位置不对,这次提到 subpackage 共享。
- `internal/provider/provider_test.go` 改用 `fake.Provider`(净 0 LOC,字段从
  小写 `name`/`nextResp` 改成大写 `NameStr`/`NextResp`)。
- `internal/agent/fake/fake.go` **~62 行**:`Tool` 导出版本(同 `provider/fake` 模式)。
  **不**直接 import `chaosbot/internal/agent`,**没有**编译期 `var _ agent.Tool = ...`
  断言。原因:`agent/agent_test.go` 是 internal test 包(`package agent`),已经 import
  这个 subpackage,会形成 `agent → agent/fake → agent` cycle(provider 那边
  `provider_test.go` 是 external test 包,import 图独立算,所以 provider/fake 能正常
  断言)。契约保证:agent 包测试 `Register(&fake.Tool{...})` 调用点编译器自动强校,
  fake 签名漂移编译失败。
- `internal/agent/tool_test.go` 和 `internal/agent/agent_test.go` 都改用 `fake.Tool`,
  原先两处本地 `fakeTool` 全删,**零重复**。

**step 实现关键点**:
- `step(ctx, history)` 只接 `history`,**不接 `userMessage`** — 跟 spec 原稿不同。
  原稿是 `step(ctx, history, userMsg)`,但实现发现:这样 caller 要在每轮手动加
  user_msg,会重复。改为 Run(04-3)在循环**前**追加一次 user_msg,step 只负责
  assistant + tools 增量。Spec 偏差记在这里。
- `req.Validate()` 在 dispatch 之前调(per Phase 02-5 契约),错误 wrap `agent: invalid request: %w`。
- provider 错误 wrap `agent: chat: %w`(Go error,中断循环)。
- **tool 错误嵌进 tool message**:`if err != nil { result = err.Error() }`,
  **不**作为 Go error 冒泡(per `architecture.md` §4,LLM 决定怎么反应)。
- 一次 Chat 调,1 个 `make([]provider.Message, len(history), len(history)+1+N)` 预分配。
- step 接收 `history` 是值语义,返回新 slice;用 `append(history, NewAssistantMessage(...))`,
  不预分配 `make` + `copy`。理由:cap 够时是 O(1) 摊销,不重新分配;cap 不够时 Go runtime
  增长策略跟手算最优也差不多;caller(Run / 04-3)只重新赋值不 mutate 元素,共享底层数组
  无害。

**测试覆盖**(6 个,全 PASS):
- `TestStep_FinalAnswerNoTools` — happy path,`final` 非空,history 增 1 条 assistant
- `TestStep_ToolCallsAppendToolMessages` — 多 tool call 顺序追加,`ToolCallID` / `Name` 透传
- `TestStep_ToolErrorEmbeddedInMessage` — tool 返回 error,`Content = err.Error()`,
  **不**冒泡 Go error(本轮 bug 抓到这个)
- `TestStep_ProviderErrorBubblesUp` — `errors.Is(err, wantErr)`
- `TestStep_ValidateFailsBubblesUp` — System + leading system msg → `ErrSystemConflict`
- `TestStep_PassesSystemAndToolsToProvider` — 验证 `LastReq.System` / `Model` / `Tools` / `Messages`

**已知 duplication**(已修):internal test 里就地写了一份 `fakeTool`(~25 行),因为
`tool_test.go` 里的 `fakeTool` 在外部包 `agent_test`,内部包看不到。
**已在 04-2 收尾时抽到 `internal/agent/fake/fake.go`**,跟 provider/fake 同模式,
`tool_test.go` 和 `agent_test.go` 都改用 `fake.Tool`,零重复。

**Layering**:`agent.go` 仅 import `context` / `fmt` / `provider`。
`grep "internal/provider/openai" internal/agent/*.go` → 空。符合 `architecture.md` §1。

**自验**:`make test` 18/18 PASS(agent 包新增 6 step,原有 13 tool/registry/message
未动;provider 6 不变;openai 2 不变);`make build` 1.5 MB;`make lint` clean。

### 04-3 — `Run` 循环 + `MaxSteps` + `ErrMaxSteps`

**新增**:
- `Agent` struct 补 `MaxSteps int` 字段(`<= 0` 走 `defaultMaxSteps = 10`)。
- `ErrMaxSteps` sentinel,`Run` 用 `fmt.Errorf("agent: %d steps exhausted: %w", max, ErrMaxSteps)`
  包装,`errors.Is` 可识别。
- `(a *Agent) Run(ctx, userInput string) (string, error)`:循环 `step`,终止条件:
  ① step 返回 `final != ""`(LLM 给终答案);② `ctx.Err() != nil`(循环顶部检查,
  **包括 Run 入口前已 cancel 的情况**);③ step 返回 Go error(provider / Validate);
  ④ MaxSteps 用完 → `ErrMaxSteps`。
- 内部默认 `defaultMaxSteps = 10` 是 package const,`MaxSteps <= 0` 走它(godoc 注明)。

**附带 provider/fake 扩展**:
- `internal/provider/fake/fake.go` 加 `Call` 类型(`Resp` + `Err`),
  `Provider` 加 `Script []Call` 字段(响应队列,每轮 Chat 弹一个)和 `AllReqs []Request`
  字段(全量请求记录)。`NextResp`/`NextErr` 保留作单次 shot 路径,`Script` 非空时优先用。
  之前 6 个 provider 测 + 2 个 openai 测继续走 `NextResp` 路径,不受影响。

**新增文件**:
- `internal/agent/run_test.go` **149 行**,4 个集成测(外部 `package agent_test`,
  `Run` 公开):

| Test | 验证 |
|---|---|
| `TestRun_FinalAnswerFirstStep` | 1 轮 Chat 返终答案;同时验证 `AllReqs[0].System` / `Model` 透传(原 spec 的 `TestRun_PassesSystemPrompt` 合并进来) |
| `TestRun_TwoStepReActLoop` | LLM 第 1 轮要 tool,echo 工具返 "echoed",LLM 第 2 轮给终答案;验证 2 次 Chat,2nd request messages 长度 3(user + assistant + tool)且 role 顺序对 |
| `TestRun_MaxStepsReached` | 3 步预算,LLM 永远只返 tool call → `errors.Is(err, agent.ErrMaxSteps)`;Calls=3 |
| `TestRun_ContextCanceled` | `cancel()` 在 Run 之前,期望 `errors.Is(err, context.Canceled)`,`fp.Calls == 0`(没真发起 Chat) |

**自验**:`make test` 31/31 PASS(agent 包 23:10 tool + 3 message + 6 step + 4 run;
provider 6 不变;openai 2 不变);`make build` 1.5 MB;`make lint` clean;`gofmt -l .` clean。

**Phase 04 收尾**:所有 3 个 sub-unit ✅。`internal/agent` 包完整:
- `Tool` interface + `Registry`(03-1/2/3)
- `NewUserMessage` / `NewAssistantMessage` / `NewToolMessage` 构造器(04-1)
- `Agent` struct + `step` 方法(04-2)+ `Run` + `MaxSteps` + `ErrMaxSteps`(04-3)
- 23 个测全过
