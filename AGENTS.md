# AGENTS.md

Instructions for AI coding agents (and humans) working on chaosbot. **Read this
before making changes.** All rules here are mandatory unless explicitly waived
by the user in a specific message.

## Project

- **Name**: chaosbot
- **Language**: Go 1.24
- **Type**: Tool-using AI agent (CLI), ReAct loop
- **Module path**: `chaosbot`
- **Entry point**: `cmd/chaosbot/main.go`
- **License**: MIT
- **DI framework**: [`github.com/hyperchao/di`](https://github.com/hyperchao/di)

## SDD workflow (mandatory)

For every non-trivial change:

1. **Spec first** — write or update the relevant `docs/phases/phase-NN-*.md`.
   A spec must include: goal, public API/interface, data structures, test
   points, risks, performance impact.
2. **Human review** — stop, surface the spec, wait for the user to ack.
3. **Implement** — generate ≤ 200 lines of new/changed code in this turn
   (`git diff --stat` per unit), so the user can review in one pass.
   File size itself is unconstrained.
4. **Self-verify** — run `make test` and `make lint`; paste results in chat.
5. **Update spec** — append an "实现笔记" section to the phase doc with any
   deviations, follow-ups, and a commit pointer.
6. **Update progress** — flip the matching row in `docs/progress.md` to ✅ and
   fill the LOC/test columns.

If a unit would generate more than 200 lines in one turn, split it into
sub-units and add new rows to the master table in `docs/progress.md` under
the `## 拆分` section.

## Repository layout

Target shape (only `cmd/chaosbot/` and `internal/provider/` exist as of
Phase 02; rest arrive per the `Master Table` in `docs/progress.md`):

```
cmd/chaosbot/                 CLI entry (cobra)
internal/agent/                Agent core: loop, tool, message
internal/provider/             LLM abstraction; subdirs per provider
internal/provider/<name>/      Concrete provider impl (e.g. openai/)
internal/tools/{time,fs,shell,web}/   Built-in tools
internal/config/               Config loading (YAML + env)
internal/session/              JSON session persistence
internal/ui/                   CLI/REPL rendering
docs/                          Specs (SPEC.md, architecture.md,
                               performance.md, progress.md), ADRs,
                               and one phase-NN-*.md per phase
scripts/                       Measurement scripts (measure.sh)
```

## Dependency injection

- Use `github.com/hyperchao/di` for all wiring.
- **Define interfaces in the consumer package.** `agent` depends on
  `provider.Provider`, not on the OpenAI struct. Concrete types live in
  `internal/provider/<name>/`.
- Register concrete implementations at the composition root (`main.go` or a
  `wire` function) using `di.RegisterDI` (default) or `di.RegisterAliasDI`
  (named variants / test fakes).
- For tests, build a fresh `di.New()` and register hand-written fakes. **No
  mock frameworks** (no gomock, testify/mock, counterfeiter, mockery, etc.).

### Injection pattern

```go
type Agent struct {
    Provider provider.Provider `di:"type"`        // default impl
    Tools    *agent.Registry   `di:"type"`
}
```

For multiple instances of the same interface, use aliases:

```go
type Toolset struct {
    Default Provider `di:"alias:openai"`
    Backup  Provider `di:"alias:anthropic"`
}
```

## Code style

- Go 1.24, idiomatic Go, `gofmt` + `go vet` clean.
- Exported types/functions/methods get godoc comments starting with the
  symbol name. Unexported helpers do not require comments.
- Errors: wrap with `fmt.Errorf("...: %w", err)`. Sentinel errors live in
  the package that produces them, named `Err<Thing>`.
- Logging: `log/slog` only. No third-party logger.
- **No global state** except via DI. The package-level `di` instance in the
  di module is forbidden in chaosbot code — always pass `*di.DI` explicitly
  or take dependencies as struct fields.
- File size is **not** capped — let files grow to whatever size is natural.
  The 200-line rule applies only to **how much you generate per turn** so
  the user can review in a single pass. Plan larger work as multiple
  todo items and ship them one at a time.
- Do not add inline comments unless asked. (godoc on exported symbols
  counts as "asked" and is required.)

## Testing

- Standard library `testing` only. No third-party assertion libraries.
- Table-driven tests preferred.
- Use external test package (`package provider_test`, not `package
  provider`) for black-box tests; that way the fakes double as a
  contract check on the public surface.
- Compile-time interface assertion at the top of the fake file, e.g.
  `var _ provider.Provider = (*fakeProvider)(nil)`.
- Hand-written fakes implementing the relevant interface live in
  `*_test.go` files inside the package under test (or in `testdata/` if
  shared across packages). The `fakeProvider` in
  `internal/provider/provider_test.go` is the canonical example and is
  meant to be **reused** by every later package that depends on
  `provider.Provider` — do not duplicate it.
- Each new behavior gets at least one positive and one negative test.
- Coverage targets: ≥ 60% for `internal/agent`, `internal/provider`,
  `internal/session`. No global coverage gate yet.

## Performance budget (hard limits)

- Binary size: ≤ 25 MB
- Cold-start RSS: ≤ 30 MB
- Steady-state RSS (empty REPL): ≤ 40 MB
- Peak RSS per `Agent.Run`: ≤ 80 MB
- Direct dependencies: ≤ 8

Full numbers and measurement procedure live in `docs/performance.md`.
All I/O must accept `context.Context` and respect cancellation. Reuse a
single `*http.Client` with configured timeouts. Tool outputs are
size-capped (file reads, shell output, web fetch — see performance doc).

### `make perf` caveats

- macOS `/usr/bin/time -v` is BSD and rejects `-v`; `measure.sh` auto-falls
  back to `ps` polling. Cross-platform results are racy for sub-50ms
  commands (the real Go-runtime peak is only seen after exit).
- `chaosbot repl` and `chaosbot bench` are skipped with notes until
  Phase 07-4 and Phase 08-2 respectively.

## Adding a dependency (forbidden by default)

1. Document in the relevant phase spec why `stdlib` is insufficient.
2. Confirm the new direct-dep count stays ≤ 8 (or update ADR-0001).
3. Add a `// why: <reason>` comment near the import or note in the phase doc.

## Build, test, lint

```
make help            # list all targets
make build           # go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/chaosbot ./...
make test            # go test -race -count=1 ./...
make lint            # make fmt + make vet (gofmt -l . ; go vet ./...)
make run ARGS="..."  # builds, then runs bin/chaosbot <args>  (NOT go run)
make perf            # scripts/measure.sh
```

The binary lands at `bin/chaosbot`. `make test` always runs with `-race
-count=1`; do not run plain `go test ./...` and think you're covered.

### Run a focused test

```bash
# single package
go test -race -count=1 ./internal/provider/...

# single test
go test -race -count=1 -run TestFakeProvider_ReturnsProgrammedError ./internal/provider/...

# whole module
go test -race -count=1 ./...
```

`make build` injects `main.version` from `git describe --tags --dirty
--always` via `-ldflags -X`. Don't edit the `const version = "dev"` in
`cmd/chaosbot/main.go`; that's only the fallback when no git tags exist.

## Common pitfalls

- Registering the same `(type, alias)` pair twice → `di` panics.
- Forgetting the `di:"..."` tag → field is silently ignored.
- `Tool.Invoke` signatures must accept `ctx context.Context` so Ctrl-C
  cancels the tool.
- Returning a `*os.File` or other `io.Closer` from a tool without a
  documented close contract → resource leak.
- Pulling in a library that drags in CGO → banned, non-portable.
- Streaming JSON encoders for huge strings — use bounded `io.LimitReader`
  instead of `io.ReadAll`.
- `provider.Message` is a **tagged union**: one struct carries all four
  roles, with role-specific fields valid only for that role. Constructor
  helpers (`NewUserMessage`, `NewToolMessage`, ...) are added in Phase
  04-1; until then, build messages by struct literal and document which
  fields you set.

## When in doubt

1. Re-read `docs/SPEC.md`.
2. Skim the latest `docs/adr/*.md`.
3. Check `docs/progress.md` for the current phase.
4. Ask the user.
