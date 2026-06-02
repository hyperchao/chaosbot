# Architecture

Companion to `docs/SPEC.md` §5. This document is a **map**, not a
re-statement of intent. If the two disagree, `SPEC.md` wins and this
file gets a PR.

## 1. Package map

| Package | Responsibility | Imports | Imported by |
|---|---|---|---|
| `cmd/chaosbot` | CLI entry, cobra wiring, DI composition root | everything | (entry) |
| `internal/agent` | ReAct loop, message types, tool registry | `provider` | `cmd`, `ui` |
| `internal/provider` | `Provider` interface + request/response types | stdlib only | `agent`, `cmd` |
| `internal/provider/openai` | OpenAI-compatible HTTP impl | `provider`, sdk | `cmd` (registration) |
| `internal/tools/<x>` | One tool each; all implement `agent.Tool` | `agent` (type only) | `cmd` (registration) |
| `internal/config` | YAML+env loader | stdlib, yaml | `cmd` |
| `internal/session` | JSON store on disk | stdlib | `agent`, `cmd` |
| `internal/ui` | CLI + REPL rendering | `agent`, `config` | `cmd` |

**Boundary rules**

- `internal/agent` must not import `internal/provider/openai` or any
  concrete provider.
- `internal/provider` must not import `internal/agent`.
- `internal/tools/<x>` must not import any other `internal/tools/<y>`.
  Cross-tool sharing goes through helpers in `internal/tools/internal/`
  if needed (not yet created).
- `internal/session` is leaf: it imports nothing from chaosbot.
- `cmd/chaosbot` is the **only** place that imports more than one
  internal package's concrete types.

## 2. DI composition root

`cmd/chaosbot/main.go` builds a single `*di.DI` instance and registers
the default set of constructors. The exact wiring (Phase 07-2) follows
this shape:

```
di.RegisterDI(config.Load)            // returns *config.Config
di.RegisterDI(openai.New)              // returns provider.Provider
di.RegisterDI(time.New, fs.New, ...)   // returns []agent.Tool
di.RegisterDI(agent.NewRegistry)       // takes []agent.Tool
di.RegisterDI(session.NewFileStore)    // takes *config.Config
di.RegisterDI(agent.New)               // takes Provider, Registry, Store, *Config
di.RegisterDI(ui.New)                  // takes *agent.Agent, *config.Config
```

Tests skip the composition root entirely and build their own
`di.New()` with hand-written fakes.

## 3. Data flow — one-shot `run`

```
user → cobra.run
  → ctx, cancel                // propagated everywhere
  → config.Load()              // YAML + env
  → di.Get[*Agent]             // builds tree once
  → agent.Run(ctx, userInput)  // see §4
  → ui.Render(answer)
  → di.Clean()                 // close http clients, etc.
  → exit
```

## 4. Data flow — ReAct loop (one step)

`agent.Run` is a single function; the loop is internal.

```
loop (max = config.MaxSteps):
  msgs = system + history + [user]
  resp = provider.Chat(ctx, {msgs, tools=Registry.Specs()})
  append resp.assistant_message to history

  if resp.ToolCalls empty:
      return resp.content

  for each call in resp.ToolCalls:
      result = registry.Invoke(ctx, call.Name, call.Arguments)
      append {role=tool, tool_call_id, name, content=result}
                                                       to history
  continue loop
```

Errors from a tool are returned to the LLM as a tool message with the
error string in `content`, **not** as a Go-level error that aborts the
loop. The LLM gets to decide how to react (retry, give up, etc.).
A Go-level error from `provider.Chat` is fatal and bubbles up.

## 5. Extension points

### Add a new LLM provider

1. Create `internal/provider/<name>/<name>.go`.
2. Define an unexported struct that satisfies `provider.Provider`.
3. Export a constructor `func New(...) provider.Provider`.
4. Add one line in `cmd/chaosbot/main.go`:
   `di.RegisterDI(<name>.New)`.
5. (Optional) Add `di.RegisterAliasDI[provider.Provider]("<name>", <name>.New)`
   for users who want to swap providers via config.

### Add a new tool

1. Create `internal/tools/<area>/<tool>.go` with an unexported struct
   that satisfies `agent.Tool`.
2. Export `func New(...) agent.Tool`.
3. Add to the default-registration list in `cmd/chaosbot/main.go`.

### Add a new session backend

1. Implement `session.Store` in a new package.
2. Register a constructor in `main.go` that returns that concrete type
   bound to the `session.Store` interface.
3. `agent` keeps importing the interface only.

## 6. Concurrency model

- One goroutine per `chaosbot` process. No fan-out workers in MVP.
- `ctx` carries cancellation through every I/O call.
- The agent loop is single-threaded by design; tool calls within a
  step are **sequential**. Parallel tool calls are a follow-up.
- `*http.Client` is safe for concurrent use, but we don't exploit that
  yet.

## 7. Error and log surface

- `log/slog` with a `slog.NewTextHandler(os.Stderr, nil)` default.
- All non-fatal warnings go through `slog.Warn`; fatal ones use
  `slog.Error` and return an error.
- User-facing error strings are plain English; structured logging stays
  on stderr.
- No log to stdout — stdout is reserved for the agent's final answer.
