# chaosbot — Progress Log

This document is the **single source of truth** for build status. Every phase
must update the table below when it starts, finishes, or changes scope.

## Conventions

- 状态: `⬜` 未开始 · `🟡` 进行中 · `✅` 已完成 · `⚠️` 有遗留问题 · `🚫` 已取消
- LOC: 本阶段新增/修改的代码行数(不含文档与生成代码)
- 测试: `通过用例 / 总用例`(本阶段新增的)
- 备注: 链接到该阶段的 `docs/phases/phase-NN-*.md` 末尾"实现笔记"

## Master Table

| 阶段 | 标题 | 状态 | 开始 | 结束 | LOC | 测试 | 备注 |
|---|---|---|---|---|---|---|---|
| 00-0  | AGENTS.md 协作规范 | ✅ | 06-01 | 06-01 | 0 | 0/0 | 文档/无代码 |
| 01-1a | 仓库门面(LICENSE/README/progress) | ✅ | 06-01 | 06-01 | 0 | 0/0 | 文档/无代码 |
| 01-1b | 决策记录(ADR-0001) + 主规格(SPEC) | ✅ | 06-01 | 06-01 | 0 | 0/0 | 文档/无代码 |
| 01-1c | 性能预算文档(performance.md) | ✅ | 06-01 | 06-01 | 0 | 0/0 | 文档/无代码 |
| 01-1d | 架构文档(architecture.md) | ✅ | 06-01 | 06-01 | 0 | 0/0 | 文档/无代码 |
| 01-1e | 阶段规格模板(phase-01-skeleton.md) | ✅ | 06-01 | 06-01 | 0 | 0/0 | 文档/无代码 |
| 01-2  | Go 模块 + Makefile + main(version) | ✅ | 06-01 | 06-01 | 78 | 0/0 | 5 go.mod + 41 Makefile + 32 main.go |
| 01-3  | 性能基线脚本(measure.sh) | ✅ | 06-01 | 06-01 | 70 | 0/0 | 见 phase doc 偏差说明 |
| 02-1  | provider 接口 + 类型 | ⬜ | | | | | |
| 02-2  | provider 接口契约单测 | ⬜ | | | | | |
| 02-3  | OpenAI 协议 provider 实现 | ⬜ | | | | | |
| 02-4  | provider factory | ⬜ | | | | | |
| 03-1  | agent.Tool 接口 + Registry | ⬜ | | | | | |
| 03-2  | Registry → OpenAI tool JSON 转换 | ⬜ | | | | | |
| 03-3  | Registry + 转换单测 | ⬜ | | | | | |
| 04-1  | agent.Message 类型 | ⬜ | | | | | |
| 04-2  | agent loop 单步逻辑 | ⬜ | | | | | |
| 04-3  | Agent struct + 终止条件 + 集成测 | ⬜ | | | | | |
| 05-1  | tools/time.get_time | ⬜ | | | | | |
| 05-2  | tools/fs.read_file | ⬜ | | | | | |
| 05-3  | tools/fs.write_file | ⬜ | | | | | |
| 05-4  | tools/fs.edit_file | ⬜ | | | | | |
| 05-5  | tools/shell.exec | ⬜ | | | | | |
| 05-6  | tools/web.fetch | ⬜ | | | | | |
| 05-7  | 默认工具注册 | ⬜ | | | | | |
| 06-1  | session.Store 持久化 | ⬜ | | | | | |
| 06-2  | session 单测 | ⬜ | | | | | |
| 06-3  | Agent 接入 session | ⬜ | | | | | |
| 07-1  | config 加载(YAML + env) | ⬜ | | | | | |
| 07-2  | cobra 子命令 stub | ⬜ | | | | | |
| 07-3  | ui/cli 单次输出渲染 | ⬜ | | | | | |
| 07-4  | ui/repl readline REPL | ⬜ | | | | | |
| 08-1  | 测试补全(边界 + 错误路径) | ⬜ | | | | | |
| 08-2  | README/config 完善 + 性能基线回填 + Go bench 子命令(performance.md F1-F3) | ⬜ | | | | | |

## 200-LOC 守门(每轮 review 预算)

每完成一阶段,运行:

```bash
git diff --stat <prev-tag-or-commit>..HEAD -- ':!docs' ':!*.md' ':!*.yaml' ':!*.yml' ':!LICENSE'
```

本阶段新增/修改的 Go 代码行数应 ≤ 200,目的是让用户一轮 review 完。
文件自身行数没有上限,只有"每轮生成量"受限。
超出则在 `## 拆分` 节追加 sub-step。

## 拆分

(此处记录超出 200 行的子阶段)
