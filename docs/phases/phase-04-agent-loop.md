# Phase 04 — Agent loop (3 sub-units)

> The ReAct loop. `agent.Run` orchestrates `provider.Provider` and
> `agent.Registry` to produce a final answer.

## Frontmatter

| Field | Value |
|---|---|
| Phase | `04` |
| Sub-units | `04-1` … `04-4` |
| Status | `✅ complete` (all 4 sub-units done; see 实现笔记) |
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

## Public API (target shape, filled across 04-1..04-3 + 2 refactors)

```go
package agent

import "chaosbot/internal/provider"

// 04-1: constructor helpers
func NewUserMessage(content string) provider.Message
func NewAssistantMessage(content string, toolCalls []provider.ToolCall) provider.Message
func NewToolMessage(toolCallID, name, content string) provider.Message

// 04-3 + 2 refactors: Agent is an INTERFACE; the concrete
// type is unexported. CLI / session / tests depend on the
// interface; only the agent package's own tests touch the
// concrete type (for the unexported step method).
type Agent interface {
    Run(ctx context.Context, userInput string) (string, error)
}

// Config is the agent's non-DI runtime config. Populated from
// chaosbot/internal/config (System / MaxSteps / Temperature /
// MaxTokens) + the provider's Model.
type Config struct {
    System      string
    Model       string
    Temperature float64
    MaxTokens   int
    MaxSteps    int
}

// reActAgent is the concrete ReAct implementation. Fields are
// EXPORTED so the di library can fill them via reflection
// (unexported fields can't be reflect.Set). TYPE is still
// unexported, so external packages only see the Agent
// interface.
type reActAgent struct {
    Provider provider.Provider `di:"type"`
    Registry *Registry         `di:"type"`
    Cfg      Config            `di:"type"`
}

// New is the no-arg constructor used by the di library. Fields
// are populated via reflection by walking the di tags.
func New() Agent

// NewFromFields is for tests / manual wiring. Production
// should go through New + DI.
func NewFromFields(p provider.Provider, reg *Registry, cfg Config) Agent
```

The `step` function (04-2) is **unexported** on the concrete
type: `func (a *reActAgent) step(...)`. Reachable only from
tests in `package agent` (internal test).

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
- `04-refactor`  **`Agent` 从 struct → interface**(post-04 review,2026-06-03):
  - 新 `Options` struct + `New(opts Options) Agent` 构造器(返回 interface)
  - 具体实现改名 `reActAgent`(unexported),所有字段 lowercase
  - `step` / `Run` 方法移到 `*reActAgent`
  - `agent_test.go`(internal 测)直接构造 `&reActAgent{...}`,**6 个 step 测无改动**
  - `run_test.go`(external 测)改用 `agent.New(agent.Options{...})` 走公共 API
  - **不是** spec 规划的独立 sub-unit,是 07-2 review 决定的 refactor
  (per AGENTS.md "Define interfaces in the consumer package",让 CLI / session /
  测试拿接口而不是具体类型)

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

- **`MaxSteps = 0` or negative**. Treated as "use default" (30)
  in 04-3? Or panic? Decision: **treat ≤ 0 as 30**; matches "do
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
- `Agent` struct 补 `MaxSteps int` 字段(`<= 0` 走 `defaultMaxSteps = 30`)。
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
- `Agent` interface + `step` / `Run` 方法(04-2/3)
- 23 个测全过

### 04-refactor — `Agent` 改成 interface

Post-04 review 补做(2026-06-03)。**起因**:07-2 启动时 user 指出 `Agent` 是 struct 让 CLI
没法 mock,违反 AGENTS.md "Define interfaces in the consumer package"。
虽然只有一个实现,接口能:(a) 让 CLI / session / 测试拿接口做 mock;
(b) 跟 `hyperchao/di` 配合做 wiring; (c) 强迫 caller 走 `New(opts)` 而不是 struct literal,
字段封装到位。

**改动**:
- `type Agent struct { ... }` → `type Agent interface { Run(...) }`
- 新增 `type Options struct` + `func New(opts Options) Agent`(返回 interface)
- 具体实现重命名 `reActAgent`(unexported),所有字段 lowercase
- `step` / `Run` 方法挂到 `*reActAgent` 上
- `agent_internal_test.go`(internal 测):`newTestAgent` 返回 `*reActAgent`;6 个 step 测**全
  无改动**(都是构造 concrete + 调 `step`,internal 访问不受影响)
- `agent_test.go`(external 测):4 个 test 改用 `agent.New(agent.Options{...})` 走公共 API

**契约变化**:
- 之前:`a := &agent.Agent{Provider: ..., MaxSteps: 5}`(直接构造)
- 现在:`a := agent.New(agent.Options{...})`(走构造函数)
- 唯一变化:测试代码从 struct literal 改成 `New(...)` 调用,**行为完全一致**,
  所有 23 个测全过(0 个测试 fail / 0 个 skip)

### 04-refactor 2 — `Options` 拆 Config + DI 字段 + 无参 New

Post-04-refactor 跟进(2026-06-03),为 07-2 真接 DI 做准备。**起因**:user
看 `New(opts Options)` 设计,指出 (a) Provider/Registry 本来就能走 DI
`di:"type"` 注入,塞在 `Options` 里是冗余;(b) 其他字段(System/Model/etc.)本质
上从 config 来,`Options` 跟 `config.Config` 字段重叠。

**di 库的关键约束**(看 `hyperchao/di/di.go` 源码):
```go
func buildStruct(d *DI, v reflect.Value) {
    for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
        if v.IsNil() { return }
        v = v.Elem()
    }
    if v.Kind() != reflect.Struct { return }
    for i := 0; i < t.NumField(); i++ {
        if !field.IsExported() { continue }  // ← unexported 反射写不进
        if tag := field.Tag.Get("di"); tag == "" { continue }
        v.Field(i).Set(build(d, alias{t: field.Type, name: ...}))
    }
}
```
→ `di.RegisterDI(F)` 的 `F` **必须无参**;字段**必须 exported**(unexported 反射写不进);
DI 通过反射遍历 `di:"type"` 标签字段自动填值。

**改动**:
- `Options` 删,改 `Config struct`(5 字段,跟 chaosbot config 子集对齐)
- `reActAgent` **类型 unexported**,**字段全部 exported** + 加 `di:"type"` 标签:
  ```go
  type reActAgent struct {
      Provider provider.Provider `di:"type"`
      Registry *Registry         `di:"type"`
      Cfg      Config            `di:"type"`
  }
  ```
- `New(opts Options) Agent` → `New() Agent` 无参(由 di 库反射调用)
- **没有** `NewFromFields` —— 外部 test 按 AGENTS.md "For tests, build a fresh
  `di.New()` and register hand-written fakes" 走 DI 路径:
  ```go
  c := di.New()
  di.RegisterDI(c, func() provider.Provider { return fp })
  di.RegisterDI(c, func() *agent.Registry { return reg })
  di.RegisterDI(c, func() agent.Config { return cfg })
  di.RegisterDI(c, agent.New)
  return di.GetDI[agent.Agent](c)
  ```
  内部 test(`package agent`)可以走 `&reActAgent{...}` 字面量构造(internal 访问)。
- `chaosbot/internal/config/config.go` 加 `Temperature` + `MaxTokens` 字段,
  `Temperature == 0` 默认 0.7(让 LLM 不那么随机)

**DI wiring(07-2 真接时)**:
```go
di := di.New()
di.RegisterDI(openai.New)                    // provider.Provider
di.RegisterDI(agent.NewRegistry)             // *Registry
di.Register(func() agent.Config { ... })      // 值类型 Config
di.RegisterDI(agent.New)                     // Agent(reActAgent),字段自动填
a := di.Get[agent.Agent](di)
```

**封装性 trade-off**:
- ❌ `reActAgent` 字段 exported(反射要求,unavoidable)
- ✅ `reActAgent` **类型**仍然 unexported → 外部包拿不到这个类型,只能拿
  `Agent` interface → 字段 exported 在外部**不可见**(拿不到类型就读不到字段)。
  封装层级靠"类型 unexported"维持,不是靠"字段 lowercase"。
- ✅ 没有 `NewFromFields` 这类"测试专用 constructor"漏到 public API

**测试改动**:
- `agent_internal_test.go`:6 个 step 测**全无改动**,继续用 `&reActAgent{Provider: ...,
  Registry: ..., Cfg: Config{...}}` 构造(internal test 有权限访问 unexported 类型)
- `agent_test.go`:4 个 Run 测改用 DI 路径(`buildAgent` helper + `di.New()`),
  替代直接 `agent.New(opts)` 跟 `agent.NewFromFields`。这是 AGENTS.md 写明的
  "For tests, build a fresh di.New() and register hand-written fakes" 范式
- **新增直接依赖** `github.com/hyperchao/di v0.0.5`(原 0 直接依赖,现 3:
  go-openai + di + yaml.v3,仍在 8 预算内)

**Layering 校验**:
- `internal/agent/agent.go` 仍只 import `context` / `errors` / `fmt` / `provider`
- 不 import `internal/provider/openai` / `internal/config`
- `internal/agent/agent_test.go`(external test)import `hyperchao/di` +
  `agent` + `agent/fake` + `provider` + `provider/fake`,符合 external test 黑盒测
  public API 的约定(走 DI 是公共路径)
- 符合 `architecture.md` 第 22 行规则

**自验**:`go test -race -count=1 ./...` 47/47 PASS(23 agent + 9 config
+ 6 provider + 2 openai + 7 cli 已撤回;`Temperature` 默认值 0.7 是新测;
DI 路径 `buildAgent` helper 在 4 个 Run 测里正常工作);
`make build` 1.5 MB;`make lint` clean;`gofmt -l .` clean。

**未改 progress.md**:refactor 跟之前一样,sub-unit 04-3 范围不变,这是后置
改进不是新 sub-unit。

---

### 实现笔记 04-4 — context window 滑动窗口

**目标**: 在每次 `step()` 前估算 history token 数,超出 budget 时丢弃最旧的
完整 turn(ADR-0002 § "Sliding window")。

**新增函数**:
- `contextBudget() int` — 返回 `MaxContextTokens × (1 - SafetyMarginFraction)`;
  防御性 clamp: 负数 max → default,frac ∉ [0,1) → 最近合法边,budget ≥ 0
- `estimateHistoryTokens([]Message) int` — 遍历每条 message 的 Content 和
  ToolCalls.Arguments; args 通过 `unsafe.String(unsafe.SliceData(...))` zero-alloc
  转 string 后调 `Provider.EstimateTokens`,确保 provider 自定义 tokenizer 被尊重
- `applyWindow(ctx, history) ([]Message, error)` — 估算 → 超 budget 则调
  `dropOldestTurns`
- `dropOldestTurns(history, budget, estimate)` — 每次剥掉最旧一整个 turn
  (user msg 到下一个 user msg 之间),直到 estimate ≤ budget
- `turnEnd(history) int` — 返回第二个 user msg 的 index,或 -1

**Config 新增**:
- `MaxContextTokens int` — 0 → default 128K,> 0 → verbatim
- `SafetyMarginFraction float64` — 0 → default 0.10,< 0 → clamp 0,≥ 1 → default

**provider.go 改动**:
- `EstimateTokensDefault` CJK 检测改用 `unsafe.Slice(unsafe.StringData(...))`
  避免 `[]rune(content)` 分配;内部用 `utf8.DecodeRune` 逐字节迭代

**自验**: `make test` 80+ PASS;`make lint` clean。

**偏差**:
- `EstimateTokensFromBytes` 曾短暂存在于 provider 包,后发现是错误抽象
  (绕过 provider 自定义 tokenizer),改为 agent 内 inline unsafe 转换
- `contextBudget` 最初返回 `(max, buffer int)`,后发现 caller 只用 budget,
  简化为返回单个 `int`
