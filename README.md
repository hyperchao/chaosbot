# chaosbot

Tool-using AI agent written in Go. Single binary, pluggable LLM provider, minimal
default tool set, designed to run on low-spec servers.

## Status

Pre-alpha. See [docs/progress.md](docs/progress.md) for the live build status.

## Quickstart (placeholder, will be filled in Phase 08-2)

```bash
export OPENAI_API_KEY=...
go run ./cmd/chaosbot --help
```

## Documentation

- [docs/SPEC.md](docs/SPEC.md) — main specification
- [docs/architecture.md](docs/architecture.md) — architecture overview
- [docs/performance.md](docs/performance.md) — performance budget and baselines
- [docs/progress.md](docs/progress.md) — phase-by-phase progress log
- [docs/adr/](docs/adr/) — architecture decision records
- [docs/phases/](docs/phases/) — per-phase specifications

## License

MIT, see [LICENSE](LICENSE).
