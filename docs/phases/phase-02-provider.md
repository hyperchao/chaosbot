# Phase 02 — Provider abstraction (5 sub-units)

> The LLM boundary. `internal/agent` may depend on `provider.Provider`
> only — never on a concrete implementation.

## Frontmatter

| Field | Value |
|---|---|
| Phase | `02` |
| Sub-units | `02-1` … `02-5` |
| Status | `✅ complete` (all 5 sub-units done; see 实现笔记) |
| Owner | chaosbot authors |
| Pre-requisites | Phase 01 (Go module, Makefile, lint) |
| Estimated total LOC | Each sub-unit ≤ 200 Go LOC (SDD gate); see 实现笔记 for actual totals |
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
- `02-4`  统一 `provider.Config` + `openai.New` 签名重构(2026-06-03)
- `02-5`  `Request.System` 文档化 + 校验(follow-up,2026-06-03)

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

### 02-3 — OpenAI 协议 provider
- `internal/provider/openai/openai.go` 195 行,包名 `openai`。
- 引入 `github.com/sashabaranov/go-openai v1.41.2`(**直接依赖 1,远低于 8 上限**)。
- 单一 `*http.Client` 复用,`Timeout` 默认 60s(可被 Config 覆盖)。
- 四个 helper:`toOpenAIRequest` / `toOpenAIMessage` /
  `fromOpenAIResponse` / `timeoutOrDefault`。每个都是纯函数,
  单元测试可在 08-1 阶段补(计划里 02-3 不分配测试子单元)。
- `Config{APIKey, BaseURL, OrgID, Timeout, Name}` 一次性读,`Provider` 不可变。
- **`Name` 字段**:上游厂商标签(`"deepseek"` / `"ollama"`),用于日志和
  配置输出;空时回退 `"openai"`。**协议名始终是 OpenAI 协议**,
  与 Name 解耦(同一 SDK 可服务 OpenAI / DeepSeek / GLM / vLLM / Ollama)。
- 二进制体积:1.5 MB → **2.2 MB**(增加 0.7 MB,SDK + 间接),远低于 25 MB 预算。
- `go build` / `gofmt -l` / `go vet` / `make test` 全绿。

### 02-4 — 统一 `provider.Config` + `openai.New` 签名重构

2026-06-03。**起因**:`openai.Config` 重复持有 `provider` 层需要的连接字段;
后续 anthropic/google 进来时还要再来一份,字段全靠手工映射。讨论后决定把
Config 提到 `provider` 包,所有 provider 实现统一签名 `New(cfg provider.Config) provider.Provider`。

**实现**:
- 新增 `provider.Config`(`Name/APIKey/BaseURL/OrgID/Timeout`)。`OrgID` 等
  部分 vendor 不用的字段由 provider 自己忽略;`Timeout=0` 走各自默认。
- 删除 `openai.Config`。`openai.New` 签名改为 `func New(cfg provider.Config) provider.Provider`,
  函数体不变(`defaultTimeout` / `defaultName` 常量保留)。
- 新建 `openai_test.go`(原 openai 包无单测),2 个 smoke case:Name 透传(保留大小写)、
  空 Name 回退 "openai"。Wire-format 单测仍按计划留 08-1 补。
- **没有 factory 函数**。原本想写 `provider.Build` 做 name dispatch,撞 import cycle
  (`provider ⇄ openai` 互引),放弃。改为:`dispatch 走 DI alias` —— main.go(Phase 07-2)
  用闭包把 Config 注入 `openai.New`,通过 `di.RegisterAliasDI[provider.Provider]("openai", ...)`
  注册。`hyperchao/di` 的构造器是 `func() T`(无参),Config 走闭包捕获,既不污染库
  代码,也满足 AGENTS.md "no package-level di" 规则。
- `internal/provider/factory.go` 那一轮**没有**提交。

**LOC**:`provider.go` +16 / `openai.go` 净 -10 / `openai_test.go` 新增 ~20 / docs ~30,
合计 ~55 行(净 +26 Go 代码)。**测试**:2/2 PASS(全 module `go test -race -count=1 ./...` 全绿)。

**Layering 校验**:`grep -l "internal/provider/openai" internal/provider/*.go` —— 除 factory.go(已删)
外,**`internal/provider/` 不依赖 `provider/openai`**。`architecture.md §1` 的"stdlib only"描述
对 `internal/provider` 仍然成立(factory 不在本包,自然不需要 openai import)。

**契约**:Phase 07-2 wiring 模板 ——

```go
// cmd/chaosbot/main.go (Phase 07-2 时落地)
di.RegisterAliasDI[provider.Provider]("openai", func() provider.Provider {
    return openai.New(loadProviderConfig())  // loadProviderConfig 从 YAML 读
})
```

### 已知限制(留 follow-up)
- **未做 round-trip / wire-format 单测**(本单元按计划不分配测试子单元,
  留 08-1 统一补)。
- 错误未做细分(401/429/5xx 都返回同一种 wrapped error);08-1 加。
- `Name` 字段未做单测(空回退 / 自定义值);08-1 补。

### 02-5 — `Request.System` 文档化 + 校验

Follow-up,2026-06-03。**起因**:`Request.System` 字段与
`Message{Role:RoleSystem}` 历史上存在两条互不冲突也不互斥的路径,
无校验、无文档说明,会导致同一次 `Chat` 产生 **两条** `role:"system"`
的 wire 消息。`Progress 02-3` 完成后回看代码发现。

**实现**:
- `provider.Request.Validate()`:检查 `System` 与 `Messages[0].Role == RoleSystem`
  互斥,返回新 sentinel `provider.ErrSystemConflict`。
- `Request.System` 字段 godoc 补 "mutually exclusive" 说明与设计动机
  (session-scoped config vs turn-scoped Messages)。
- `Provider` 接口 godoc 补 "assumes validated input" 契约,**明确校验
  责任在 agent 层(Phase 04)**,provider 实现侧不重复调用。
- `openai.Chat` **不**调用 `Validate()`(契约而非实现,避免每个 provider
  重复;agent loop 是唯一的 caller)。
- `provider_test.go` 新增 `TestRequest_Validate` 表驱动 6 case。
- `docs/progress.md` Master Table 新增 02-5 行。

**LOC**:~30 行(代码 + godoc)。**测试**:6/6 PASS。

**契约落地**:`agent.Run`(Phase 04)在 dispatch 之前调 `req.Validate()`,
失败时返回 `errors.Is(err, provider.ErrSystemConflict)` 给 ui 层做用户
提示(类似 "config 错误:system prompt 同时出现在两个地方")。

## Follow-ups

- Anthropic provider: interface is ready, concrete impl deferred to a
  follow-up ADR after MVP ships.
- Streaming (`ChatStream`): interface stays a single `Chat` for MVP;
  a future `Provider2` with `ChatStream(ctx, req, sink)` is additive.
