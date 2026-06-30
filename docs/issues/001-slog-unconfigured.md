# Open Issues

## [slog] ✅ resolved — 日志输出已配置

**Status**: ✅ closed  
**Resolved**: 2026-06-30 (commit `0fb665e`)

`main.go` 已添加 `slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))`，Warn 级别日志以结构化格式输出到 stderr。`Agent.Run` 等内部 slog 调用已被捕获。Agent 内部 `saveOnSuccess` 等 slog 调用均可被捕获。

---

## [待认领]

