# Phase 04 — Agent loop (3 sub-units)

> The ReAct loop. `agent.Run` orchestrates `provider.Provider` and
> `agent.Registry` to produce a final answer.

## Frontmatter

| Field | Value |
|---|---|
| Phase | `04` |
| Sub-units | `04-1` … `04-3` |
| Status | `🟡 in progress` (1/3 sub-units done; see 实现笔记) |
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
