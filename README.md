# chaosbot

Tool-using AI agent CLI written in Go. Single static binary, pluggable LLM
provider, built-in tools (file ops, shell, web fetch), ReAct loop.

## Install

```bash
git clone https://github.com/anomalyco/chaosbot.git
cd chaosbot
make build
```

Binary lands at `bin/chaosbot`.

## Quickstart

```bash
# Set API key
export CHAOSBOT_API_KEY=sk-xxxxx

# One-shot
chaosbot run "summarize README.md"

# Interactive REPL
chaosbot

# Show config
chaosbot config
```

## Configuration

Loaded in this order (later wins):

1. Built-in defaults
2. `~/.config/chaosbot/config.yaml`
3. `./config.yaml` (cwd)
4. `--config <path>` flag
5. `CHAOSBOT_*` env vars

```yaml
# chaosbot.yaml
provider:
  name: deepseek           # provider name (openai-compatible protocol)
  api_key: sk-xxxxx        # or use api_key_env / CHAOSBOT_API_KEY
  base_url: https://api.deepseek.com/v1
  model: deepseek-chat
  timeout: 60s
  max_retries: 3

system: "You are a helpful coding assistant."
max_steps: 30
max_context_tokens: 128000
workspace: .
```

See `config.example.yaml` for all options.

## Commands

| Command | Description |
|---|---|
| `chaosbot` | Start interactive REPL |
| `chaosbot run <prompt>` | One-shot query |
| `chaosbot run --session <id> <prompt>` | Resume a saved session |
| `chaosbot config` | Print effective config |
| `chaosbot version` | Version info |

REPL slash commands: `/help`, `/reset`, `/exit`, `/quit`, `/tools`.

REPL features: arrow key history, Ctrl-A/E/B/F, Tab completion for `/` commands.

## Tools

| Tool | Description |
|---|---|
| `read_file` | Read file with line range |
| `write_file` | Atomic file write |
| `edit_file` | Find-and-replace in file |
| `shell` | Execute shell command |
| `web_fetch` | Fetch and extract text from URL |

## Build & Test

```bash
make help      # list all targets
make build     # build bin/chaosbot
make test      # go test -race ./...
make lint      # go vet + gofmt
make perf      # measure binary size and RSS
```

## Performance

| Metric | Current | Limit |
|---|---|---|
| Binary size | 6.85 MB | ≤ 25 MB |
| Cold-start RSS | 2.69 MB | ≤ 30 MB |
| Direct deps | 6 | ≤ 8 |
| CGO | none | forbidden |

## Docs

- [SPEC.md](docs/SPEC.md) — master specification
- [architecture.md](docs/architecture.md) — architecture overview
- [performance.md](docs/performance.md) — performance budget
- [progress.md](docs/progress.md) — build progress
- [adr/](docs/adr/) — architecture decision records
- [phases/](docs/phases/) — per-phase specifications

## License

MIT, see [LICENSE](LICENSE).
