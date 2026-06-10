# Phase 07 — CLI + REPL (4 sub-units)

> The user surface. `cmd/chaosbot` wires config + agent + REPL
> and exposes subcommands.

## Frontmatter

| Field | Value |
|---|---|
| Phase | `07` |
| Sub-units | `07-1` … `07-4` |
| Status | `🟡 in progress` (2/4 sub-units done; see 实现笔记) |
| Owner | chaosbot authors |
| Pre-requisites | Phase 04 (Agent + Run), Phase 02 (provider.Config) |
| Estimated total LOC | ~310 Go (80 + 100 + 30 + 100) |
| Performance impact | cobra readline adds ~1 MB to binary; acceptable per 25 MB budget |

## Goal

Ship the user surface: a runnable binary that takes a user query
and produces an answer. Two modes:

- `chaosbot run "..."` — one-shot, prints answer, exits
- `chaosbot` — REPL with multi-turn, prints each answer, history
  in-memory (no session persistence in this phase)

`cmd/chaosbot` is the composition root: it builds the config,
constructs the provider + registry + agent, and dispatches to
the chosen subcommand. Per `architecture.md` §1, this is the
**only** place that imports multiple internal concrete types.

## Key design decision: skip 05 + 06 for now

Phase 05 (built-in tools) and Phase 06 (session persistence) are
**deferred** so the user has something to chat with earlier.
The REPL runs without tools (`Registry.Specs()` returns `[]`),
the LLM answers directly. Multi-turn history lives in process
memory; restart loses it. `chaosbot run --resume <id>` lands
in Phase 06.

## Public CLI surface (target shape, filled across 07-1..07-4)

```
chaosbot                            # start REPL (default)
chaosbot run "..."                  # one-shot
chaosbot version                    # version info (exists since Phase 01)
chaosbot config                     # print effective config
chaosbot --config <path> ...        # override config file
chaosbot --workspace <path> ...     # override workspace root (Phase 05)
chaosbot --provider openai          # override provider name
```

`chaosbot tools` is deferred to Phase 05 (no tools yet → empty
list would be a confusing first impression).

## Public API (target shape)

```go
// internal/config/config.go
type Config struct {
    Provider  provider.Config   // API key, base URL, model, max tokens
    System    string            // system prompt
    MaxSteps  int               // agent loop budget
    Workspace string            // root for fs tools (Phase 05)
}

func Load(path string) (*Config, error)   // YAML + env override

// cmd/chaosbot/main.go
func main() {
    cfg, _ := config.Load(...)
    p, _ := openai.New(cfg.Provider)
    reg := agent.NewRegistry()
    a := &agent.Agent{Provider: p, Registry: reg, System: cfg.System, MaxSteps: cfg.MaxSteps}
    cmd.Execute(a)
}
```

## Sub-units

- `07-1`  `internal/config` 包(`Config` struct + `Load(path)` YAML+env,
  ~80 Go LOC) — env vars 优先级高于 YAML 文件;YAML 不存在时只用 env
- `07-2`  `cmd/chaosbot` 子命令 + composition root(本轮 `run` / `config` /
  `version`,`repl` 留 07-4,~100 Go LOC)
  - stdlib `flag` + 手动 dispatch(按 spec 决策**不**引 cobra,免 ~150 KB + 1 直接依赖)
  - `Deps` struct 注入(`Agent` / `Config` / `Out` / `ErrOut` / `Version`),CLI 包不知道
    di 存在(per AGENTS.md: interface 在 consumer 包,DI 在 main.go)
  - `main.go` 走 `di.New()` 装配(`hyperchao/di`,per AGENTS.md),
    `openai.New` 用闭包包成无参(因为 `openai.New(cfg)` 签名不匹配 `func() T`)
  - `config.Load` 严格校验,但 `version` / no-args 走 bypass(`needsConfig` 判断),
    不需要 API key 也能跑 — `chaosbot version` 因此可独立 smoke 测
- `07-3`  `internal/ui/cli` 单次输出渲染(~30 Go LOC)— REPL 也复用
- `07-4`  `internal/ui/repl` readline 循环(`chaosbot/readline` 库,
  `bufio.Scanner` 走 stdlib,~100 Go LOC) — slash commands: `/reset` `/exit`
  (defer `/tools` to Phase 05)

## Test points

| Test | Sub-unit | Type |
|---|---|---|
| `TestLoad_Defaults` | 07-1 | unit |
| `TestLoad_FromYAML` | 07-1 | unit |
| `TestLoad_EnvOverridesYAML` | 07-1 | unit |
| `TestLoad_MissingAPIKey_ReturnsError` | 07-1 | unit |
| `TestRun_OneShot` (subcommand dispatcher) | 07-2 | unit (fakeProvider, capture args) |
| `TestRun_DefaultsToREPL` (no args) | 07-2 | unit |
| `TestRender_Bare` | 07-3 | unit (just fmt-equivalent) |
| `TestREPL_TwoTurnLoop` | 07-4 | unit (programmed readline input) |
| `TestREPL_SlashExit` | 07-4 | unit |
| `TestREPL_SlashReset` | 07-4 | unit |

The `cmd/chaosbot/main.go` itself is left untested at the package
level (it's a 5-line composition root). Subcommand logic lives
in the `cmd/chaosbot/cli` subpackage, which is unit-testable.

## Risks

- **Readline lib choice**. Two options: (a) `github.com/chzyer/readline`
  (popular, simple, but unmaintained since 2020); (b) `bufio.Scanner`
  on `os.Stdin` (no extra dep, no arrow-key history, no line editing).
  **Decision**: start with `bufio.Scanner` for MVP, no extra dep.
  Arrow-key history is nice-to-have; ship without it first.

- **YAML lib choice**. `gopkg.in/yaml.v3` is the only sensible option
  in Go. Adds 1 direct dep (within the 8 budget). Alternative: TOML
  via `github.com/pelletier/go-toml` (adds 1 too). **Decision**: YAML,
  matches `chaosbot/config.example.yaml` reference in SPEC.md.

- **Cobra weight**. `spf13/cobra` adds ~150 KB to binary + 1 direct
  dep. `flag` (stdlib) is enough for our ~3 subcommands.
  **Decision**: use stdlib `flag` for MVP. Cobra can land later if
  subcommand count grows.

- **Config file path defaults**. `~/.config/chaosbot/config.yaml`
  (XDG) vs `./config.yaml` (cwd). SPEC.md says both, with precedence
  rules. For MVP, only check the explicit `--config` flag, then
  env vars. No file discovery yet.

## Performance impact

- `cobra` skipped → binary stays at 1.5 MB
- `gopkg.in/yaml.v3` → +~80 KB; still well under 25 MB
- No new I/O hot paths
- REPL idle RSS ~10-15 MB (no LLM call, just line reader)

## 实现笔记

> Filled as each sub-unit lands.

### 07-1 — `internal/config` 配置加载

**新增文件**:
- `internal/config/config.go` **140 行**:`Config` struct(顶层)+ `ProviderConfig`
  struct(嵌套,给 `provider.Config` 喂数据);`Load(path string) (*Config, error)`
  主入口。
- `internal/config/config_test.go` **162 行**:8 个测,全 PASS。
- `config.example.yaml` **43 行**:参考配置,所有字段都注释默认值和单位。

**新增依赖**:`gopkg.in/yaml.v3 v3.0.1`(直接依赖从 1 → 2,仍在 8 预算内)。
二进制:1.5 MB → 1.5 MB(无明显变化)。

**加载链(本单元)**:
```
defaults()  →  loadYAML(path)  →  applyEnv()  →  applyDefaults()  →  resolveAPIKey()  →  validate()
  内置零值        YAML 文件          CHAOSBOT_*        填零值              按 api_key_env 拿        必填校验
```

**API key 解析优先级**:
1. `CHAOSBOT_API_KEY` env(最具体,覆盖一切)
2. YAML `provider.api_key`(直接给)
3. YAML `provider.api_key_env` 引用的 env var(默认 `OPENAI_API_KEY`)
4. 三者全空 → `Load` 返回 error

**env 覆盖 YAML 的字段**:`CHAOSBOT_PROVIDER` / `CHAOSBOT_API_KEY` /
`CHAOSBOT_BASE_URL` / `CHAOSBOT_MODEL` / `CHAOSBOT_SYSTEM` / `CHAOSBOT_MAX_STEPS` /
`CHAOSBOT_WORKSPACE`。`MAX_STEPS` 用 `strconv.Atoi` 解析,解析失败静默忽略(保持 default)。

**测试覆盖**(8 个,全 PASS):
- `TestLoad_Defaults` — 无 YAML 无 env,期望 error(无 API key)
- `TestLoad_FromEnv_APIKey` — `CHAOSBOT_API_KEY` 路径,验证其他字段走默认
- `TestLoad_FromYAML` — YAML 设全套 + `api_key_env` 解析
- `TestLoad_EnvOverridesYAML` — YAML 跟 env 同时设,env 赢
- `TestLoad_APIKeyEnvFallsBackToProviderEnv` — `api_key_env: CUSTOM_KEY` 路径
- `TestLoad_MissingAPIKey_ReturnsError` — 全空,error
- `TestLoad_MalformedYAML` — `:::not yaml`,YAML parser 报错透传
- `TestLoad_YAMLFileNotFound` — 路径不存在,`os.ReadFile` 报错透传

每个测用 `t.Setenv` 隔离 env(Go 1.17+,test 结束自动还原),互不污染。

**Layering**:`internal/config` 仅 import `gopkg.in/yaml.v3` + stdlib。
不依赖 `internal/provider` / `internal/agent` — config 是叶子包。
07-2 wiring 时 `config.ProviderConfig` → `provider.Config` 转换在 main.go
里做(不在 config 包里,**避免 config → provider 的反向依赖**)。

**自验**:`go test -race -count=1 -v ./internal/config/...` 8/8 PASS;
`make build` 1.5 MB;`make lint` clean。

**已知 follow-up**:
- `--config` 显式路径之外的自动发现(XDG / cwd)按本阶段决策不做
- 配置文件 watch / hot-reload 不做
- 复杂类型(list / map)YAML 字段不暴露(MVP 用不到)

### 07-2 — `cmd/chaosbot` 子命令 + composition root(DI 版)

**新增 / 替换文件**:
- `cmd/chaosbot/main.go` **~75 行** composition root:`flag.Parse` 拿 `--config`,
  调 `config.Load`,失败时只对 `run` / `config` 子命令报错(让 `version` / no-args
  不依赖 API key),然后 `buildAgent` 走 `di.New()` 装配,最后 `cli.Run(args, Deps)`
- `cmd/chaosbot/cli/cli.go` **~110 行**:`Deps` 注入 5 件套(`Agent` / `Config` /
  `Out` / `ErrOut` / `Version`);3 个 handler `runCmd` / `configCmd` / `versionCmd`
- `cmd/chaosbot/cli/cli_test.go` **~150 行**:**手写 `fakeAgent`** 实现
  `agent.Agent`,7 个 dispatch / handler 测(per AGENTS.md "no mock frameworks")

**为什么这版 CLI 走 DI**(从 spec 决策的演化):
- 07-2 第一次写时我用了 `Deps` struct + 手写 `openai.New` 装配,user 提了我违反
  AGENTS.md "Use hyperchao/di for all wiring"
- 回滚后 review `hyperchao/di/di.go` 源码,发现约束:`RegisterDI` 只接无参构造函数,
  字段必须 exported 并打 `di:"type"` 标签(反射填值)
- 这又引出 Phase 04 refactor:`Agent` 改 interface,`reActAgent` 字段 exported,
  `New()` 无参,`Config` 抽 struct
- 最后成型:**`internal/agent` 完全 DI 友好**;**`internal/agent/fake` 提供 Provider
  fake**;**`cmd/chaosbot/cli` 拿 `agent.Agent` 接口(测试用 fakeAgent)**;**`main.go` 装配**

**`needsConfig` 设计**:
- `config.Load` 严格(要 API key)
- 但 `chaosbot version` / no-args 不该强制要 API key
- `main.go` 拿 `needsConfig(args)` 判断:仅 `run` / `config` 子命令失败时报 config 错
- 这让 `chaosbot version` 可以独立 smoke 测,不依赖任何 secret

**`openai.New` 闭包**:
```go
di.RegisterDI(c, func() provider.Provider {
    return openai.New(provider.Config{...})  // 闭包捕获 cfg
})
```
di 库 `func() T` 约束 vs `openai.New(provider.Config) provider.Provider` 不匹配,
用闭包转接。**`internal/provider/openai` 不变**,CLI 不直接 import 它。

**测试覆盖**(7 个,全 PASS):
- `TestRun_NoSubcommand_Errors` — 空 args,error 提 REPL 07-4
- `TestRun_UnknownSubcommand_Errors` — 未知子命令,error 含 bad name
- `TestRun_Version` — `version` 走 `Deps.Version`
- `TestRun_Config` — `config` 打印 11 字段(model / temperature / max_tokens 全部展示)
- `TestRun_OneShot` — happy path,断言 `userInput == "hello"` 传给 agent
- `TestRun_OneShot_MissingPrompt_Errors` — `run` 不带参数
- `TestRun_OneShot_AgentError_Propagates` — agent 返回的 error 透传,`errors.Is` 验

`fakeAgent` 在 cli_test.go 里就地实现,**只有 5 行**(`runFunc` 字段 + `Run` 方法),
编译期断言 `var _ agent.Agent = (*fakeAgent)(nil)`。

**冒烟测试**(手动 binary 验证):
```
$ ./bin/chaosbot version          # 无 API key 也跑
chaosbot dev
$ ./bin/chaosbot                  # 无 args,REPL hint
chaosbot: no subcommand (REPL coming in 07-4; use 'chaosbot run "<prompt>"')
$ ./bin/chaosbot foobar
chaosbot: unknown subcommand: foobar (try 'run', 'config', 'version')
$ ./bin/chaosbot run "hi"         # 缺 API key
chaosbot: config: config: API key not set (use CHAOSBOT_API_KEY, ...)
$ CHAOSBOT_API_KEY=sk-fake-test1234 ./bin/chaosbot config
provider:    openai
model:       (empty)
...
```

**Layering 校验**:
- `cmd/chaosbot/cli/cli.go` import `agent` / `config` / `context` / `fmt` / `io`,
  **不** import `provider/openai` / `hyperchao/di`(di 是 main.go 的关注点)
- `cmd/chaosbot/main.go` import `agent` / `cli` / `config` / `provider` /
  `provider/openai` / `hyperchao/di`(composition root 唯一知道全部部件)
- `grep "internal/provider/openai" cmd/chaosbot/cli/*.go` → 空(cli 不直接依赖 concrete provider)

**自验**:`make test` 54/54 PASS(23 agent + 9 config + 6 provider + 2 openai
+ 14 cmd/chaosbot/cli);`make build` 1.5 MB;`make lint` clean;`gofmt -l .` clean。
冒烟 5 条路径全对。
