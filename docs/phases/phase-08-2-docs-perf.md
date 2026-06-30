# Phase 08-2 — README + 性能基线 + bench 子命令

## Frontmatter

| Field | Value |
|---|---|
| Phase | `08-2` |
| Sub-units | `08-2` |
| Status | `✅ complete` (1 sub-unit done; bench intentionally skipped, see below) |
| Owner | chaosbot authors |
| Pre-requisites | all prior phases |
| Estimated total LOC | ~40 Go LOC (no new code) |
| Performance impact | 无新增代码 |

## Goal

完善项目文档（README），回填性能基线数据，对原计划的 bench 子命令做出有据 skip 决策。

## 为什么跳过 bench 子命令

原 Phase 08-2 计划实现 `chaosbot bench` 子命令，通过 `runtime.MemStats` 精确测量 F1（冷启动 RSS）、F2（单次 Agent.Run 峰值）、F3（工具级 RSS 归因）。

**跳过理由：**

1. **性价比低** — 当前 `measure.sh` + `ps` 轮询测得的二进制大小 6.85 MB、冷启动 2.69 MB 远低于限制（25 MB / 30 MB），没有压线风险。精确测量不值得投入。
2. **F1 对短命令无意义** — `chaosbot version` 50ms 内退出，peak 与 idle 差异极小，精确数值对决策无帮助。
3. **F2/F3 需要 mock 驱动** — 真正的 Agent.Run 峰值依赖 LLM 调用（网络 I/O 占主导），mock provider 下的测量不和实际场景对应。
4. **属于调试工具而非核心功能** — bench 子命令是开发者工具而非用户功能。未来需要时可重新评估。

## 实际实施

### measure.sh 修复

- 修复 `go build -o bin/chaosbot ./...` → `./cmd/chaosbot`（原命令因多 package 无法构建）
- 更新 REPL steady-state 测量为 pipe EOF 方式
- bench 测量标注为"intentionally skipped"

### README

完善为正式的使用文档：安装、配置、快速开始、子命令说明、文档索引。

### 性能基线回填

基于 `make perf` 实测更新 `docs/performance.md` §5 当前基线表。

## 实现笔记

### 08-2 — 文档 + 基线 + skip bench

**实际实施：**
- `scripts/measure.sh`：修复 build target + 更新 skip 信息
- `README.md`：重写为完整使用文档
- `docs/performance.md`：新增 §5 当前基线表

**偏差：**
- bench 子命令有意跳过（见上文「为什么跳过 bench 子命令」）
- 性能文档 F1-F3 follow-up 标注为 `🚫 intentionally skipped`

**提交指针：** 待提交
