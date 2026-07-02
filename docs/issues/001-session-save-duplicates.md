# 已知问题（deferred）

未解决的、尚未决定方案的设计问题。先记录，后续 phase 解决。

## 001 — Session save 失败导致重复 message

**Status**: ✅ resolved in Phase 10 (2026-07-02)
**Found**: 06-3 implementation (2026-06-16)
**Affects**: `internal/session/filestore.go`, `internal/agent/agent.go`

### 问题

`FileStore.Append` 是 `O_APPEND | bufio.Writer` + `f.Sync()`。如果 Append 在
sync 之前 crash 或 sync 失败：

- 磁盘可能已部分写入（bufio flush 出去的数据）
- `saveOnSuccess` 的实现把 `a.sessionOffset` 更新放在 Append 之后
  ```go
  if err := a.Store.Append(ctx, ...); err != nil {
      return  // error path, offset NOT updated
  }
  a.sessionOffset = len(history)
  ```
- 下次 Run 调 `saveOnSuccess` 仍然从旧 offset append 同一段 messages
- 结果：磁盘文件出现 `... m4 (partial) m4 (完整) m5 ...`
- Load 时 partial m4 unmarshal 失败被跳过，但完整 m4 重复

### 解决方案: line_id 去重（D6）

每个 message 写入时带上 `line_id`（JSON 字段 `"l"`），Load 时去重：

**机制**:
1. 格式: 每条行是 `{"role": "user", "content": "...", "l": <line_id>}`
   - 使用 `storedLine` 结构体（`provider.Message` embedded + `LineID int \`json:"l"\``）
2. `FileStore.Append` 为每条 message 赋予连续 line_id（从 caller 传入的
   `offset` 开始）, 单次 fsync
3. `loadFromOffset` 用 line_id 做 cursor（`LineID < offset` 跳过）
4. 重复 line_id 去重（`seen[LineID]` map, 后者为准）
5. JSON 解析失败的行（partial write）静默跳过
6. 污 cursor 检测: `maxLineID+1 < offset` → `ErrStaleCursor`

**具体改动** (`internal/session/store.go`, `filestore.go`, `agent/agent.go`):

- `Store.Append` 签名从 `(ctx, id, msgs) error` 改为
  `(ctx, id, offset, msgs) error` — offset 显式传入
- Agent 的 `saveOnSuccess` 计算 `offset = trimmedTotal + sessionOffset`
- 所有 Append 实现、NoopStore、test fake 更新签名

**优点**:
- 部分写入: JSON 解析失败 → skip（天然恢复）
- 重复: 相同 line_id → 后者覆盖前者
- 性能: N writes + 1 fsync（无 stat recovery, 无 per-message fsync）
- cursor: line_id == 绝对位置, Resume 语义不变

### 实现笔记

- `storedLine` embedding `provider.Message` + `json:"l"` 保证扁平 JSON
- 向后兼容: unmarshal 旧格式得到 LineID == 0, 正常加载
- `commitTrim`/`pruneHistory` 不变, 仍用 `committedPrefix`/`trimmedTotal`
- `info.Cursor` 语义不变: 绝对 line_id（与之前的绝对值 cursor 同一值）
- 单次 Append 失败时 caller 不推进 offset, 重试得到相同 line_id,
  读到端产生重复 frame → Load 时去重

### 候选方案（未采用）

1. **D1: 接受 limitation, 文档说明** 不采用 — line_id 去重复杂度
   足够低
2. **D2: saveOnSuccess 返回 error, Run 传播** 部分采用 — Append
   返回 error 的逻辑保留（先写实现 154afe7, 后合并到 D6）
3. **D3: 单 message atomic write + 每条 fsync** — 性能差，不采用
4. **D4: WAL 格式 (TLV framing)** — 复杂度高，未采用
5. **D5: tmp+rename per Append** — 性能差，未采用
6. **D6: line_id 去重** ✅ 采用

### 触发决策的事件

- 用户报告 session 文件出现重复 message
- power loss / disk full 频发
- 性能 profile 显示 fsync 是热点
