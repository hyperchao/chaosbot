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
| 02-1  | provider 接口 + 类型定义 | ✅ | 06-01 | 06-01 | 92 | 0/0 | 9 类型 + 4 Role 常量,无外部依赖 |
| 02-2  | provider 接口契约单测(手写 fake) | ✅ | 06-01 | 06-01 | 112 | 5/5 | 5 表驱动 + fakeProvider 复用 |
| 02-3  | OpenAI 协议 provider 实现 | ✅ | 06-01 | 06-01 | 188 | 0/0 | 1 直接依赖 go-openai v1.41.2,二进制 2.2MB |
| 02-4  | 统一 `provider.Config` + `openai.New` 签名重构 | ✅ | 06-03 | 06-03 | ~55 | 2/2 | 见 phase-02-provider.md 实现笔记 02-4;无 factory,dispatch 走 DI alias(07-2) |
| 02-5  | `Request.System` 文档化 + 校验 | ✅ | 06-03 | 06-03 | ~30 | 6/6 | 见 phase-02-provider.md 实现笔记 02-5 |
| 03-1  | agent.Tool 接口 + Registry | ✅ | 06-03 | 06-03 | 63 | 0/0 | 0 外部依赖,03-3 补测;Makefile 修已合本批 |
| 03-2  | Registry → OpenAI tool JSON 转换 | ✅ | 06-03 | 06-03 | 27 | 0/0 | `Specs()` + `Names()` 落在 `internal/agent/tool.go`,03-3 补测 |
| 03-3  | Registry + 转换单测 | ✅ | 06-03 | 06-03 | 187 | 10/10 | 1 编译期断言 + 10 表驱动;fakeTool 复用;外部 test 包 `agent_test` |
| 04-1  | agent.Message 类型 | ✅ | 06-03 | 06-03 | 27 | 3/3 | 复用 `provider.Message`,3 构造函数;见 phase-04 实现笔记 |
| 04-2  | agent loop 单步逻辑 | ✅ | 06-03 | 06-03 | 68 | 6/6 | `step` 改 `step(ctx, history)`,无 userMsg;bug 抓 tool error 必须嵌 message;fake 抽到 `provider/fake/` |
| 04-3  | Agent struct + 终止条件 + 集成测 | ✅ | 06-03 | 06-03 | ~80 | 4/4 | `Run` + `MaxSteps` + `ErrMaxSteps`;fake 扩 Script/AllReqs 队列;测合并到 4 个 |
| 04-4  | context window 滑动窗口 + token 估算 | ✅ | 06-16 | 06-16 | ~260 | 8/8 | `applyWindow`/`dropOldestTurns`;`contextBudget` 防御性 clamp;`estimateHistoryTokens` zero-alloc unsafe;`EstimateTokensDefault` zero-alloc CJK;Config +2 字段;ADR-0002 更新 |
| 04-4b/c | safety net + LLM summarization | ✅ | 06-21 | 06-21 | ~160 | 10/11 | `SummaryEnabled` cfg;`summaryMsg`/`summaryCursor`;`serializeHistoryFragment`;`summarizeHistory`;proactive applyWindow;reactive ErrContextLength retry;Reset/Resume clear summary |
| 05-1  | tools/fs.read_file | ✅ | 06-14 | 06-14 | ~340 | 6/6 | `internal/tools/fs/read_file.go` 158 + test 180;`bufio.Scanner` token 1 MiB;`cat -n` 输出;`*os.PathError` 透传;binary sniff 512 B;见 phase-05 实现笔记 05-1 |
| 05-2  | tools/fs.write_file | ✅ | 06-15 | 06-15 | ~290 | 9/9 | `writeFileAtomic` tmp+fsync+rename 原子写;父目录 `MkdirAll`;tmp 0600;`%w` wrap `*os.PathError` 透传;failure path 留原文件不动 |
| 05-3  | tools/fs.edit_file | ✅ | 06-15 | 06-15 | ~300 | 8/8 | strict unique-anchor(0 或 N>1 都返错,error 带前 5 个 offsets);`writeFileAtomic` 复用;empty `old_text` 拒绝(防 infinite match);empty `new_text` 删 anchor |
| 05-4  | tools/shell.exec | ✅ | 06-15 | 06-15 | ~340 | 10/10 | `exec.CommandContext` + SIGKILL on ctx cancel;`cappedWriter` 100 KB + marker;merged stdout+stderr;exit code 嵌入 reply(非零 exit 不返 Go err,只 LLM 可见);timeout `interrupted: <snippet>`;`/bin/sh -c` |
| 05-5  | tools/web.fetch | ✅ | 06-15 | 06-15 | ~415 | 9/9 | `golang.org/x/net/html` tokenizer 抽 visible text;`io.LimitReader(1MB)` 输入 cap;output 50 KB cap;`http.Client` 复用;scheme 白名单 http/https;4xx/5xx 透传;`go.mod` 多 1 dep(4 direct,8 预算内) |
| 05-6  | 默认工具注册 | ✅ | 06-15 | 06-15 | n/a | n/a | 不额外写代码:每个 tool commit 都 incremental 加进 `wire.go` 闭包;`make test` + REPL smoke 验证 5 个 tool 都能被 LLM 看到 |
| 06-1  | session.Store 持久化 | ✅ | 06-16 | 06-16 | 200 | 0/0 | Store 接口 + FileStore(JSONL,O_APPEND,bufio.Writer+fsync);200 LOC 拆成 impl 1 commit |
| 06-2  | session 单测 | ✅ | 06-16 | 06-16 | 220 | 11/11 | 11 个测:roundtrip / 增量 / LoadNotExist / empty no-op / List / Delete / corrupt / large output / NewID |
| 06-3  | Agent 接入 session | ✅ | 06-17 | 06-17 | ~500 | 8/8 | `Store` DI 注入;`sessionID`/`sessionOffset` internal 状态;`Resume`/`SessionID` 加 `Agent` 接口;`saveOnSuccess` 自动 Append 增量;`Reset` 删 session;`Agent` own session 内部状态;`sessionOffset` 与 windowing 解耦;见 phase-06 实现笔记 06-3 |
| 07-1  | config 加载(YAML + env) | ✅ | 06-03 | 06-03 | 140 | 8/8 | yaml.v3 入 go.mod;`CHAOSBOT_*` env 覆盖 YAML;api_key_env 间接解析;无 XDG/cwd 发现 |
| 07-2  | cobra 子命令 stub | ✅ | 06-03 | 06-03 | ~120 | 7/7 | **DI 版**:`main.go` 走 `di.New()` 装配;`cli` 拿 `agent.Agent` 接口 + 手写 `fakeAgent` mock;`needsConfig` 让 `version` 跳过 API key 校验;`openai.New` 用闭包转成无参 |
| 07-3  | ui/cli 单次输出渲染 | ✅ skipped | | | | | fmt.Fprintln 当前够用,无独立 renderer |
| 07-4  | REPL(/reset /exit /help,bufio.Scanner) | ✅ | 06-11 | 06-11 | ~115 | 11/11 | **stdlib `flag` + bufio.Scanner**;agent.Agent 加 `Chat(ctx, msgs) (msg, err)`,`Run` 改包装;REPL 持 `[]provider.Message` history,每轮 `[history..., userMsg, reply]` 喂 Chat,2 turn 测验证 LLM 看到 q1+a1+q2;slash commands 在 cli.replCmd;os.Stdin DI alias `"in"`;6 个新 cli test + 1 个 agent 层 Chat test |
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

## 已知问题（deferred）

未解决的设计问题，详情见 `docs/issues/`：

- [001](issues/001-session-save-duplicates.md) — Session save 失败导致偶发重复 message
  (Append 部分写入 + in-memory offset 未更新 → 下次 retry 重复)。MVP 接受。
- [002](issues/002-repl-readline.md) — REPL 缺 readline (arrow keys, history, line editing)。
  当前用 stdlib bufio.Scanner; 真实使用率上来再决定。
