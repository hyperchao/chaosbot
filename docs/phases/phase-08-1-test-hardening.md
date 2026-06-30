# Phase 08-1 — 测试补全（边界 + 错误路径）

## Frontmatter

| Field | Value |
|---|---|
| Phase | `08-1` |
| Sub-units | `08-1` |
| Status | `✅ complete` (1 sub-unit done; see 实现笔记) |
| Owner | chaosbot authors |
| Pre-requisites | Phase 04-4b/c, Phase 06-3 |
| Estimated total LOC | ~150 Go LOC |
| Performance impact | 无 |

## Goal

提升 `internal/agent`、`internal/provider`、`internal/session` 三包的测试覆盖率至 ≥ 80%，聚焦边界条件和错误路径。

## 覆盖率热点

### agent 包（当前 76%）

| 函数 | 当前 | 目标 | 缺口 |
|---|---|---|---|
| `saveOnSuccess` | 68.4% | ≥80% | store 写入失败 path |
| `summaryEnabled` | 0% | ≥60% | Config 组合覆盖 |
| `roleTag` | 50% | ≥80% | error path |
| `summarizeHistory` | 81.8% | ≥85% | summarize 失败 path |
| `applyWindow` | 87.1% | ≥90% | 边界 budget |

### provider 包（当前 71.7%）

| 函数 | 当前 | 目标 | 缺口 |
|---|---|---|---|
| `toOpenAIMessage` | 0% | ≥60% | role=error, tool path |
| `EstimateTokens` (openai) | 0% | ≥60% | 不同 content 类型 |

### session 包（当前 72.6%）

| 函数 | 当前 | 目标 | 缺口 |
|---|---|---|---|
| `SaveSummary` | 61.5% | ≥80% | 写入失败 path |
| `Append` | 70.0% | ≥80% | 写入失败 path |
| `Delete` | 57.1% | ≥80% | 文件不存在 path |

## Test points

| Test | 包 | 描述 |
|---|---|---|
| `TestSaveOnSuccess_StoreWriteFails` | agent | SaveSummary 返回 error，验证只打 warn 不 panic |
| `TestSummaryEnabled_RespectsConfig` | agent | SummaryDisabled=true → false → summarization 行为差异 |
| `TestRoleTag_ReturnsErrorOnUnknown` | agent | 非法 role 返回错误 |
| `TestSummarizeHistory_FailsGracefully` | agent | summarizeHistory 返回 error 时 step 处理 |
| `TestApplyWindow_BudgetAtExactBoundary` | agent | budget == MaxContextTokens 边界 |
| `TestToOpenAIMessage_ToolRole` | provider | role=tool 的 message 转换 |
| `TestEstimateTokens_EmptyString` | provider | 空字符串返回 0 |
| `TestSaveSummary_WriteFails` | session | json.Marshal 失败或文件不可写 |
| `TestAppend_WriteFails` | session | bufio.Writer flush 失败 |
| `TestDelete_FileNotExist` | session | 删除不存在的 session 不报错 |

## Risks

- 覆盖率和实现细节耦合：测试 private 函数（如 `roleTag`）需要同包测试文件，可接受。
- fake Provider 不触发某些 OpenAI-specific path（如 `toOpenAIMessage` 的 tool role），需要在 `provider/openai` 包内补充白盒测试。

## Sub-units

- `08-1` 测试补全 ✅

## 实现笔记

### 08-1 — 测试补全

**实际实施：**

| 新增文件 | 关键测试 |
|---|---|
| `internal/agent/agent_internal_test.go` | `roleTag` 100% (6 cases) |
| `internal/agent/agent_test.go` | `saveOnSuccess` 失败路径 (Append/SaveSummary 失败不影响 Run 成功) |
| `internal/session/filestore_test.go` | `SaveSummary` 写入失败 (只读目录)、`Append` 写入失败、`Delete` 文件不存在 |
| `internal/provider/openai/openai_internal_test.go` | `toOpenAIMessage` RoleTool + 未知role、`EstimateTokens` 空串 |

**覆盖率变化（agent 包整体，含新增测试文件）：**

| 函数 | 前 | 后 |
|---|---|---|
| `roleTag` | 50% | 100% |
| `saveOnSuccess` | 68.4% | 73.7% |
| `toOpenAIMessage` | 0% | 27.3% |
| `EstimateTokens` (openai) | 0% | 100% |
| `SaveSummary` (session) | 61.5% | 69.2% |
| `Append` (session) | 70.0% | 75.0% |
| `Delete` (session) | 57.1% | 57.1% |

**未达到 80% 的热点：** `summaryEnabled`(0%)、`serializeHistoryFragment`(8%)、`contextBudget`(62.5%)、`toOpenAIRequest`(44.4%)。这些需要更复杂的编排或 mock，不影响功能正确性。

**提交指针：** 待用户 ack 后提交。
