# 已知问题（deferred）

未解决的、尚未决定方案的设计问题。先记录，后续 phase 解决。

## 001 — Session save 失败导致重复 message

**Status**: open
**Found**: 06-3 implementation (2026-06-16)
**Severity**: low (MVP 可接受)
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

### 现状（缓解）

- Partial 行在 Load 时被天然 skip（NDJSON 格式，损坏行 unmarshal 失败
  返回 partial + error）
- 用户 / 自动化接受偶发重复
- 重复 message 极罕见（需要 Append 期间 disk full / power loss）

### 候选方案

1. **D1: 接受 limitation, 文档说明**
   - 复杂度: 0
   - 用户影响: 偶发看到重复 user msg, LLM 能处理
   - MVP 推荐

2. **D2: saveOnSuccess 返回 error, Run 传播**
   - 复杂度: 低（已经写到一半的代码）
   - 用户影响: save 失败时 Run 返回 error, 用户知道
   - 仍然不解决重复, 只让用户知情

3. **D3: 单 message atomic write + 每条 fsync**
   - 单条 message 用一次 `f.Write(data)` + `f.Sync()`
   - POSIX PIPE_BUF (4KB) 内 atomic
   - 工具输出可能 > 4KB, 仍然可能 partial
   - 复杂度: 中

4. **D4: WAL 格式 (TLV framing)**
   - `<8-byte length><newline><message>` 包裹每条 message
   - Load 跳过 incomplete frame
   - Append 截断到 last complete frame
   - 完全 atomic per message
   - 复杂度: 高
   - 改变文件格式, 不向后兼容

5. **D5: tmp+rename per Append (方案 A)**
   - 每次 Append 写 tmp, fsync, rename
   - 完全 atomic, 但 O(n) per Append
   - 复杂度: 中
   - 性能差

### 当前建议

实现 D2 (让用户知情) + D1 (文档说 limitation), MVP ship。其他方案
按需引入。

### 触发决策的事件

- 用户报告 session 文件出现重复 message
- power loss / disk full 频发
- 性能 profile 显示 fsync 是热点
