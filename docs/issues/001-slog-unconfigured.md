# Open Issues

## [slog] 日志输出未分离

**描述**: `slog` 默认 handler 无配置，直接写 stderr。CLI 工具运行时日志和 UI 输出混在一起，难以区分。

**现状**: 代码中没有任何 `slog.SetHandler` / `slog.New` 调用。

**期望**:
- 上线阶段初始化 JSON handler 写文件
- 开发阶段 Text handler 写 stderr，带 log level
- `Agent.Run` 等内部 slog 调用均可被捕获

**相关**: `internal/agent/agent.go:277`

---

## [待认领]

