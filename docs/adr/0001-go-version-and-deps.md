# ADR-0001: Go version, dependency framework, and target dependency set

- **Status**: Accepted
- **Date**: 2026-06-01
- **Deciders**: chaosbot authors
- **Supersedes**: —

## Context

We are starting a fresh Go project. We need to commit to:

1. A Go toolchain version.
2. A dependency injection strategy (the project will have many pluggable
   parts: LLM provider, tool set, session store, UI).
3. The initial set of direct dependencies, with a strict budget for
   portability and low-spec-server friendliness.

The project will be packaged as a single static binary, so anything that
forces CGO or pulls in a heavy runtime is banned.

## Decisions

### Go 1.24

- `range over int` and the latest `slices`/`maps` standard library are
  available and reduce boilerplate.
- Generics are first-class and needed for the DI framework we chose.
- Minimum supported version is 1.24; toolchain directive in `go.mod` pins it.

### Dependency injection: `github.com/hyperchao/di`

- Lightweight, generic-based, struct-tag driven; no codegen, no reflection
  beyond what the standard library already does.
- Aligns with the "depend on interfaces" rule: consumers define interfaces,
  register concrete implementations in `main.go` via `di.RegisterDI` (default)
  or `di.RegisterAliasDI` (named variants / test fakes).
- Test isolation is trivial: `di.New()` + register hand-written fakes.
- **Forbidden**: the package-level `di` global from the di module is never
  imported by chaosbot code. Always pass `*di.DI` explicitly.

### Direct dependencies (initial budget: ≤ 8)

| Module | Purpose | Why stdlib is not enough |
|---|---|---|
| `github.com/hyperchao/di` | DI wiring | Stdlib has no DI; hand-rolled wiring doesn't compose |
| `github.com/spf13/cobra` | CLI subcommands | Stdlib `flag` lacks subcommand trees and `--help` ergonomics |
| `github.com/sashabaranov/go-openai` | OpenAI-compatible HTTP | Implementing the protocol by hand is brittle and bloats the binary less than rolling our own |
| `gopkg.in/yaml.v3` | Config parsing | Stdlib has no YAML |
| `github.com/chzyer/readline` | REPL line editing | Stdlib `bufio.Scanner` cannot handle arrow keys, history, or Ctrl-D cleanly |
| `github.com/fatih/color` *(planned)* | Terminal coloring | Stdlib has no ANSI helper |
| `github.com/JohannesKaufmann/dom/...` *(rejected)* | HTML→markdown | Adds a large dependency; we will write a tiny HTML stripper in `tools/web` instead |

CGO is **banned**. No dependency that requires it.

### Code style guardrails

- `gofmt` + `go vet` clean on every commit.
- Standard library `testing` only; no assertion libraries.
- No global state except via DI.
- Errors wrapped with `%w`; sentinel errors named `Err<Thing>`.

## Consequences

- New contributors can read the full DI surface in one file (`di.go` is
  ~110 lines).
- The dependency count is small enough to audit by hand on every PR.
- Adding a new provider (Anthropic, Google, local) is a matter of writing
  one struct that implements `provider.Provider` and registering it in
  `main.go`.
- If `hyperchao/di` becomes unmaintained, the abstraction is small enough
  to replace in a single PR.

## Alternatives considered

- **Wire (`github.com/google/wire`)**: codegen, less explicit, adds a build
  step. Rejected for the same reason `viper` is rejected: too much magic.
- **Uber `fx`**: too heavy, brings in `dig` and a lifecycle model we don't
  need.
- **Hand-rolled constructors in `main.go`**: works for ~5 components, but
  the agent will have 10+; hand-wiring gets unmaintainable.
- **`viper` for config**: brings cobra's config cousin and conflicts with
  the dependency budget. Rejected.
