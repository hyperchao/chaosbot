# Phase 01 — Skeleton (template + worked example)

> **This file is the canonical template** for per-phase specifications.
> When a new phase doc is created, copy the structure below and fill in
> the concrete content. Do not invent new sections — if something is
> missing here, update this template in a separate PR first.

## Frontmatter

| Field | Value |
|---|---|
| Phase | `01` |
| Sub-units | `01-1a` … `01-3` |
| Status | `🟡 in progress` |
| Owner | chaosbot authors |
| Pre-requisites | none (first phase) |
| Estimated total LOC | <200 Go LOC across all sub-units (no Go code in this phase) |
| Performance impact | none yet; baselines captured in Phase 01-3 |

## Goal

Establish the repository skeleton that every later phase builds on: the
`docs/` tree, the build harness, and a runnable `chaosbot version` binary.

## Public API / interface

None — this phase ships no Go code. The "API" is the set of `make`
targets declared in `Makefile`:

```
make build   # go build ./...
make test    # go test ./...
make lint    # go vet ./... + gofmt -l
make run     # go run ./cmd/chaosbot ...
make perf    # scripts/measure.sh
```

## Data structures

None.

## Test points

| Test | Type | Where |
|---|---|---|
| `chaosbot version` prints the version string | manual smoke | shell |
| `go vet ./...` exits 0 | CI gate | `make lint` |
| `gofmt -l .` prints nothing | CI gate | `make lint` |
| Binary starts in < 200 ms | manual, `make perf` | shell |

## Risks

- Choosing the wrong toolchain version blocks contributors on older
  distros. Mitigated by `go.mod` `toolchain go1.24.x`.
- README attracting users before features exist. Mitigated by labeling
  the project pre-alpha in the README header.

## Performance impact

None in this phase. The first real baseline is captured in Phase 01-3
(`scripts/measure.sh`) and Phase 08-2 (post-feature baseline).

## Sub-units

- `01-1a` 仓库门面: LICENSE + README + docs/progress.md ✅
- `01-1b` 决策记录(ADR-0001) + 主规格(SPEC.md) ✅
- `01-1c` 性能预算文档(docs/performance.md) ✅
- `01-1d` 架构文档(docs/architecture.md) ✅
- `01-1e` 阶段规格模板(本文件) ✅
- `01-2`  Go 模块 + Makefile + main(version) ⬜
- `01-3`  性能基线脚本 ⬜

## 实现笔记

> _Filled at the end of the phase. Records deviations, follow-ups, and
> a commit pointer._

### 01-1a — 仓库门面
- 生成 LICENSE(MIT)/ README(桩) / `docs/progress.md`(33 行主进度表)。
- 实际新增 91 行(无 Go 代码)。

### 01-1b — ADR-0001 + SPEC
- 固化了 Go 1.24、hyperchao/di、≤8 直接依赖、CGO 禁止四条硬规则。
- SPEC.md 144 行,7 个非目标、4 个用户故事、5 边界包。

### 01-1c — performance.md
- 7 条硬性数字 + 5 条工具 cap + 1 条 HTTP client 守门。
- 测量方法由 Phase 01-3 脚本驱动。

### 01-1d — architecture.md
- 7 节:包映射 / DI 根 / one-shot 数据流 / ReAct 循环 / 扩展点 / 并发 / 错误日志。
- 关键边界:agent 禁导入 provider/openai,provider 禁导入 agent。

### 01-1e — 模板(本文件)
- 统一后续 32 个阶段 doc 的章节顺序。
- 自指:后续每阶段用此结构 + 在「实现笔记」追加。

## Follow-ups

- Phase 01-2 是第一个 Go 代码阶段,需在 spec 里复述"go.mod toolchain 字段"和"Makefile 目标必须可被 `make -n` 干跑"。
- `make perf` 目标目前是 stub,Phase 01-3 落地测量脚本。
