# Phase 02 — Provider abstraction (4 sub-units)

> The LLM boundary. `internal/agent` may depend on `provider.Provider`
> only — never on a concrete implementation.

## Frontmatter

| Field | Value |
|---|---|
| Phase | `02` |
| Sub-units | `02-1` … `02-4` |
| Status | `🟡 in progress` |
| Owner | chaosbot authors |
| Pre-requisites | Phase 01 (Go module, Makefile, lint) |
| Estimated total LOC | ≤ 200 Go LOC across all four sub-units |
| Performance impact | interface adds zero runtime cost; OpenAI impl measures in Phase 08-2 |

## Goal

Define the LLM boundary behind a single `provider.Provider` interface
and ship a working OpenAI-compatible implementation. The agent loop
must be able to consume any `Provider` interchangeably, including
hand-written fakes in tests.

## Public API / interface (target shape, filled by 02-1)

```go
type Provider interface {
    Chat(ctx context.Context, req Request) (*Response, error)
    Name() string
}

type Request struct {
    System      string
    Messages    []Message
    Tools       []ToolSpec
    Model       string
    Temperature float64
    MaxTokens   int
}

type Response struct {
    Content   string
    ToolCalls []ToolCall
    Usage     Usage
}

type Message struct {
    Role       Role
    Content    string
    ToolCalls  []ToolCall
    ToolCallID string
    Name       string
}

type ToolSpec struct {
    Name        string
    Description string
    Parameters  json.RawMessage
}

type ToolCall struct {
    ID        string
    Name      string
    Arguments json.RawMessage
}

type Usage struct {
    PromptTokens, CompletionTokens, TotalTokens int
}

type Role string
const (
    RoleSystem Role = "system"
    RoleUser   Role = "user"
    RoleAssistant Role = "assistant"
    RoleTool   Role = "tool"
)
```

## Data structures

- `Message` is the union of all four roles. Fields are valid per role:
  - `system`/`user`: only `Content` set.
  - `assistant`: `Content` and/or `ToolCalls` set.
  - `tool`: `Content` (the tool's string output) + `ToolCallID` + `Name`
    (the tool's registered name).
- `Request.System` is the developer prompt; serialized as a system
  message at the head of the conversation by the provider.
- `ToolSpec.Parameters` is a JSON Schema object. Providers may pass it
  through (OpenAI, Anthropic) or transform it (vendors with custom DSLs).
- `ToolCall.Arguments` is **raw JSON object**, not a Go struct. The agent
  loop hands it to `Tool.Invoke(ctx, args)` unchanged.

## Test points

| Test | Where |
|---|---|
| `TestMessage_RoleString_Constants` (table) | provider_test.go (02-2) |
| `TestProvider_FakeImplementsInterface` (compile-time) | provider_test.go (02-2) |
| `TestOpenAI_BodyShape_*` (HTTP recording) | openai_test.go (02-3) |
| `TestFactory_PicksConfiguredProvider` | factory_test.go (02-4) |

## Risks

- A `Message` struct that has 5 fields, 4 of which are role-specific,
  is a classic "tagged union" smell. Mitigation: keep one struct but
  provide constructor helpers (`NewUserMessage`, `NewToolMessage`, ...)
  in 04-1 to make call sites read cleanly.
- Wire-format drift between `ToolSpec` and what each provider accepts.
  Mitigation: provider owns the serialization, agent never sees it.

## Performance impact

Interface method calls in Go are devirtualized aggressively; no
measurable overhead. The OpenAI implementation in 02-3 must reuse a
single `*http.Client` and respect the body caps in
`docs/performance.md` §3.

## Sub-units

- `02-1`  Provider 接口 + 类型定义
- `02-2`  Provider 接口契约单测(手写 fake)
- `02-3`  OpenAI 协议 provider 实现
- `02-4`  Provider factory

## 实现笔记

> Filled as each sub-unit lands.

### 02-1 — 接口 + 类型
- `internal/provider/provider.go` 92 行:1 个 interface + 4 个 Role 常量
  + 5 个数据 struct(`Message` / `ToolSpec` / `ToolCall` / `Usage` /
  `Request` / `Response`)。
- 全部 godoc 注释齐全,无外部依赖(只 import `context` 和 `encoding/json`)。
- 编译 `go build` / `gofmt -l` / `go vet` 三连全绿。
- 关键设计:`Message` 是 tagged union 风格(一个 struct 装四角色),
  02-1 不提供构造函数,留给 04-1 给出 `NewUserMessage` 等 helper 缓解
  call site 可读性。

### 02-2 — 接口契约单测 + 手写 fake
- `internal/provider/provider_test.go` 112 行,外部 test package
  (`provider_test`),5 个表驱动测试。
- `fakeProvider` 手写结构:4 字段(`name` / `nextResp` / `nextErr` / `lastReq`),
  满足 `provider.Provider` 接口(编译期断言 `var _ Provider = ...`)。
- 测试覆盖:Role 常量 / 接口实现 / 程序化响应 / 请求捕获 / 错误返回。
- `go test -race` 全绿(5/5 PASS)。fakeProvider 将被 02-3/04-x 复用。

## Follow-ups

- Anthropic provider: interface is ready, concrete impl deferred to a
  follow-up ADR after MVP ships.
- Streaming (`ChatStream`): interface stays a single `Chat` for MVP;
  a future `Provider2` with `ChatStream(ctx, req, sink)` is additive.
