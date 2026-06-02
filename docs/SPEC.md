# chaosbot — Master Specification

> The single source of truth for what chaosbot is and isn't. Anything
> missing here is either in flight in `docs/phases/` or not yet decided.
> When in doubt, defer to the latest `docs/adr/`.

## 1. Overview

chaosbot is a **tool-using AI agent** packaged as a single Go binary CLI.
It connects to a pluggable LLM provider, exposes a small set of built-in
tools (file ops, shell, web fetch, time), runs a ReAct-style loop until
the model returns a final answer, and renders the result to the terminal.

It is designed to run on a 1 vCPU / 512 MB server, ship as a ≤ 25 MB
static binary, and start in under 100 ms cold.

## 2. Goals

- **G1** — Single static binary; no daemon, no service mesh, no DB required
  for MVP.
- **G2** — Pluggable LLM provider behind a single `provider.Provider`
  interface, with the OpenAI-compatible protocol as the reference
  implementation (covers OpenAI, DeepSeek, GLM, vLLM, Ollama, etc.).
- **G3** — ReAct loop: think → optional tool call → observe → repeat.
- **G4** — Local JSON session persistence in the user's config dir.
- **G5** — Two interaction modes: one-shot `run` and interactive `repl`.
- **G6** — Hand-written fakes for tests; no mock framework.
- **G7** — ≥ 60% unit-test coverage in `internal/agent`, `internal/provider`,
  and `internal/session`.

## 3. Non-goals (MVP)

- Streaming token output (deferred to a follow-up ADR).
- Anthropic Claude native protocol (interface ready; concrete impl
  deferred).
- MCP (Model Context Protocol) client.
- Multi-agent orchestration or sub-agents.
- Parallel tool execution within a single model turn.
- Vector long-term memory / RAG.
- IM platform integrations (Feishu, Slack, etc.).
- Shell command sandboxing beyond a timeout.

## 4. User stories

- **U1 — One-shot**: `chaosbot run "summarize the last 3 commits"` → bot
  reads git, calls LLM, prints answer, exits.
- **U2 — REPL**: `chaosbot` → user types multi-turn questions, the
  session is persisted, `Ctrl-D` exits.
- **U3 — Tools**: agent can read/write files, run shell, fetch URLs,
  and ask the current time, all behind a workspace root.
- **U4 — Resume**: `chaosbot run --resume <id> "continue"` reloads a
  saved session and appends the new turn.

## 5. Core abstractions

```
┌─────────────────────────────────────────────────────────────┐
│                          cmd/chaosbot                       │
│  (cobra subcommands, di container, ctx propagation)         │
└──────────┬──────────────────────────┬───────────────────────┘
           │                          │
           ▼                          ▼
   ┌──────────────┐         ┌─────────────────────┐
   │  agent.Agent │────┐    │ provider.Provider   │
   │   (loop)     │    │    │   (interface)       │
   └──────┬───────┘    │    └─────────┬───────────┘
          │            │              │ impl
          ▼            │              ▼
   ┌──────────────┐    │    ┌─────────────────────┐
   │  agent.Tool  │    │    │ provider/openai     │
   │  (Registry)  │    │    │  (concrete impl)    │
   └──────┬───────┘    │    └─────────────────────┘
          │            │
          ▼            │
   ┌──────────────┐    │
   │ tools/{time, │
   │  fs,shell,   │    │
   │  web}        │    │
   └──────────────┘    │
                       │
   ┌──────────────┐    │
   │ session.Store│◀───┘
   │  (JSON)      │
   └──────────────┘
```

- `provider.Provider` is the **only** LLM boundary; agent never imports
  `provider/openai`.
- `agent.Tool` is the only tool boundary; tools never import each other.
- `session.Store` is the only persistence boundary.

## 6. Public CLI surface

```
chaosbot                          # start REPL (default)
chaosbot run "..."                # one-shot
chaosbot run --resume <id> "..."  # resume a saved session
chaosbot config                   # print effective config
chaosbot tools                    # list registered tools
chaosbot version                  # version info
chaosbot --config <path> ...      # override config file
chaosbot --workspace <path> ...   # override workspace root
```

REPL slash commands: `/reset` (clear session), `/tools` (list), `/exit`.

## 7. Configuration model

Loaded in this order (later wins):

1. Built-in defaults.
2. `~/.config/chaosbot/config.yaml`.
3. `./config.yaml` (cwd).
4. `--config <path>` flag.
5. Environment variables (`CHAOSBOT_<KEY>` for top-level, plus the
   provider-specific `*_API_KEY` envs referenced by `api_key_env`).

A full example lives at `config.example.yaml` (added in Phase 07-1).

## 8. Default tool set (MVP)

| Tool | Args | Notes |
|---|---|---|
| `get_time` | (none) | RFC3339 UTC + local |
| `read_file` | `path`, `start_line?`, `end_line?` | ≤ 2000 lines / 256 KB |
| `write_file` | `path`, `content` | Atomic write via tmp+rename |
| `edit_file` | `path`, `old_text`, `new_text` | `old_text` must be unique |
| `shell` | `command`, `timeout_sec?` | Default 30 s, output ≤ 100 KB |
| `web_fetch` | `url` | 1 MB HTML → ≤ 50 KB text |

## 9. Performance budget

See `docs/performance.md` for full numbers and measurement procedure.
Headline limits: binary ≤ 25 MB, cold-start RSS ≤ 30 MB, steady-state
RSS ≤ 40 MB, single `Run` peak RSS ≤ 80 MB, ≤ 8 direct dependencies.

## 10. Out of scope (this spec)

- Token usage / cost reporting.
- Authentication / multi-tenant isolation.
- Plugin marketplace.
- Cross-platform GUI.

These may be added in later ADRs.
