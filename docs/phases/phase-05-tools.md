# Phase 05 — Built-in tools (6 sub-units)

> The agent's hands. `internal/tools/<area>/<tool>.go` for
> each tool; `cmd/chaosbot/wire.go` registers them in the
> agent's Registry. The `Tool` interface and `Registry` are
> already in place from Phase 03.

## Frontmatter

| Field | Value |
|---|---|
| Phase | `05` |
| Sub-units | `05-1` … `05-6` |
| Status | `🟡 in progress` (0/6 sub-units done) |
| Owner | chaosbot authors |
| Pre-requisites | Phase 03 (Tool interface + Registry), Phase 04 (agent loop dispatches to Registry) |
| Estimated total LOC | ~600 Go (5 tools + 1 shared package, with tests) |
| Performance impact | `golang.org/x/net/html` (sub-dep of std `net/http`) adds ~50 KB to binary; direct-dep count 3 → 4, still within 8 budget |

## Goal

Ship five built-in tools that turn the LLM into a useful
agent: read / write / edit files, run shell, fetch web. Each
tool lives in its own subpackage `internal/tools/<area>/`
and implements the `agent.Tool` interface (4 methods:
`Name`, `Description`, `Parameters`, `Invoke` — per Phase 03).

## Scope decisions (recorded)

- **5 tools, not 6.** `get_time` from SPEC §8 is dropped:
  `shell` + `date` is equivalent and one fewer tool is
  simpler. If a real use case emerges later, easy to add.
- **No `pattern` / `grep` on `read_file`.** File I/O is file
  I/O; querying is `shell` + `rg` / `grep`. LLM picks.
- **`edit_file` requires unique `old_text`.** LLM that can't
  make its anchor unique should grow it. Strict check
  prevents silent multi-replace bugs. Aider-style
  `// ... existing code ...` markers can come later if
  unique proves too strict in practice.
- **`write_file` always overwrites** (no `mode` flag). LLMs
  are expected to read before overwriting; users review.
- **`read_file` rejects binary** by NUL-byte sniff on the
  first 512 bytes (ripgrep default). Returns an error
  telling the LLM to use `shell` + `xxd` / `hexdump`.
  **Image / PDF / Office files are caught by this** (PNG
  chunks contain NUL, JPEG headers are mostly ASCII but
  Huffman tables usually hit NUL within the window).
  Vision / multimodal is a Phase N+2 concern: it needs a
  new `Tool.Invoke` result type carrying an image payload
  and matching provider wire-format fields, both breaking
  changes. MVP relies on `shell base64 / xxd / file` for
  binary inspection.
- **`web_fetch` uses `golang.org/x/net/html` for tokenizing.**
  Hand-written regex strip is unreliable (entity handling,
  nested tags, `<script>`/`<style>` blocks). The `x/net`
  sub-package is a one-line `require`; 0 transitive deps;
  ~50 KB to binary. Documented as the 4th direct dep.
- **No workspace jail.** `cfg.Workspace` is informational
  only; file tools accept any path. If a user later wants
  chroot, it's a tool-config field, not a re-architecture.

## Public API additions

```go
// internal/tools/time/      — DROPPED (use shell + date)
// internal/tools/fs/read.go
// internal/tools/fs/write.go
// internal/tools/fs/edit.go
// internal/tools/shell/shell.go
// internal/tools/web/web.go

type ReadFileTool struct{}      // struct so tests can inject workspace later
func (t *ReadFileTool) Name() string                                 // "read_file"
func (t *ReadFileTool) Description() string
func (t *ReadFileTool) Parameters() json.RawMessage                  // JSON Schema
func (t *ReadFileTool) Invoke(ctx, args) (string, error)

// same shape for WriteFileTool, EditFileTool, ShellTool, WebFetchTool
```

Each tool has:
- A package-local `defaultTimeout` const
- A package-local `maxOutputBytes` const (matches
  `docs/performance.md` caps)
- A constructor that returns the struct (or `agent.Tool`
  interface); no config knobs in MVP

## Sub-units

- `05-1`  `internal/tools/fs/read_file.go` — read a text
  file with optional `start_line` / `end_line` (1-indexed,
  inclusive). Pure Go, no exec, no network. NUL-byte sniff
  on the first 512 bytes rejects binary. Output capped at
  2000 lines × 256 KB; truncation marker included.
  ~50 Go LOC.
- `05-2`  `internal/tools/fs/write_file.go` — atomic write
  via `os.CreateTemp` + `os.Rename`. Creates parent
  directories as needed. ~50 Go LOC.
- `05-3`  `internal/tools/fs/edit_file.go` — replace one
  occurrence of `old_text` with `new_text`. Strict
  unique-anchor check: 0 hits → not-found error, ≥ 2 hits
  → non-unique error. ~50 Go LOC.
- `05-4`  `internal/tools/shell/shell.go` — `os/exec` with
  `context.WithTimeout`, output capture (stdout+stderr
  merged, ≤ 100 KB cap), exit code surfaced to LLM as part
  of the tool result. ~80 Go LOC.
- `05-5`  `internal/tools/web/web.go` — `net/http` GET with
  `Content-Length` check + `io.LimitReader(1MB)`, then
  `html.NewTokenizer` to extract visible text (skip
  `<script>`/`<style>` blocks), truncate to 50 KB. ~100 Go
  LOC + 1 new direct dep.
- `05-6`  wire all 5 into `Registry` from `cmd/chaosbot/wire.go`;
  smoke-test each with a real LLM through the REPL. The
  `/tools` slash command is **deferred** to when REPL hits
  5+ slash commands (per the table-driven refactor note in
  `phase-07-cli-repl.md`); for MVP, `Registry.Names()` is
  inspectable via the `version`/`bench` debug paths.

## Test points

| Test | Sub-unit | Type |
|---|---|---|
| `TestReadFile_Whole` | 05-1 | unit (tmpfile) |
| `TestReadFile_LineRange` | 05-1 | unit |
| `TestReadFile_Binary_Rejects` | 05-1 | unit (write NUL bytes) |
| `TestReadFile_NotFound_Propagates` | 05-1 | unit |
| `TestReadFile_LineCap` | 05-1 | unit (2000-line file) |
| `TestReadFile_ByteCap` | 05-1 | unit (256 KB file) |
| `TestWriteFile_Atomic` | 05-2 | unit (overwrite existing) |
| `TestWriteFile_Permissions` | 05-2 | unit (0600 on Linux) |
| `TestEditFile_Unique_Replaces` | 05-3 | unit |
| `TestEditFile_NonUnique_Errors` | 05-3 | unit |
| `TestEditFile_NotFound_Anchor_Errors` | 05-3 | unit |
| `TestShell_Runs` | 05-4 | unit (echo hello) |
| `TestShell_Timeout` | 05-4 | unit (`sleep 5`, ctx 100ms) |
| `TestShell_Truncates` | 05-4 | unit (`yes`, 200 KB) |
| `TestShell_NonZeroExit_Surfaces` | 05-4 | unit (`false`) |
| `TestShell_StderrMerged` | 05-4 | unit |
| `TestWeb_Fetches` | 05-5 | unit (httptest) |
| `TestWeb_StripsHTML` | 05-5 | unit (assert no `<tag>` in output) |
| `TestWeb_StripsScript` | 05-5 | unit |
| `TestWeb_LimitsTo1MB` | 05-5 | unit (serve 2 MB) |
| `TestWeb_TruncatesTo50KB` | 05-5 | unit (serve 200 KB body) |
| `TestWeb_4xx5xx_Errors` | 05-5 | unit (httptest 404) |
| `TestRegistry_All5_Registered` | 05-6 | integration (build real container) |

## Tool specs (the JSON sent to the LLM)

```yaml
# read_file
name: read_file
description: |
  Read a text file. Returns the file content as a string.
  Binary files (containing NUL bytes) are rejected; use
  shell + xxd for binary inspection. Capped at 2000 lines
  and 256 KB.
parameters:
  type: object
  required: [path]
  properties:
    path:      {type: string, description: "absolute or relative file path"}
    start_line:{type: integer, minimum: 1, description: "1-indexed start line (inclusive, default 1)"}
    end_line:  {type: integer, minimum: 1, description: "1-indexed end line (inclusive, default start+1999 or EOF)"}

# write_file
name: write_file
description: |
  Write content to a file, replacing any existing content.
  Atomic via tmp+rename: partial writes never appear.
  Creates parent directories as needed.
parameters:
  type: object
  required: [path, content]
  properties:
    path:    {type: string}
    content: {type: string}

# edit_file
name: edit_file
description: |
  Replace one occurrence of old_text with new_text in a
  file. The old_text anchor must appear exactly once in
  the file — use read_file first to verify, or grow the
  anchor to disambiguate.
parameters:
  type: object
  required: [path, old_text, new_text]
  properties:
    path:     {type: string}
    old_text: {type: string}
    new_text: {type: string}

# shell
name: shell
description: |
  Run a shell command. stdout and stderr are merged and
  returned. Capped at 100 KB; longer output is truncated
  with a marker. Default timeout 30 s.
parameters:
  type: object
  required: [command]
  properties:
    command:     {type: string, description: "shell command line (runs via /bin/sh -c)"}
    timeout_sec: {type: integer, minimum: 1, maximum: 600, default: 30}

# web_fetch
name: web_fetch
description: |
  Fetch a URL and return its visible text content.
  HTML is stripped (script/style blocks removed, tags
  dropped, entities decoded). Capped at 1 MB input and
  50 KB output. HTTP only (no https-to-http downgrade).
parameters:
  type: object
  required: [url]
  properties:
    url: {type: string, description: "http or https URL"}
```

## Risks

- **`edit_file` unique-anchor strictness** may frustrate
  LLMs in tight cases. Mitigated by LLM growing the
  anchor; revisit in Phase 09 if real workflows fail.
- **`shell` is RCE.** No sandboxing in MVP. `cfg.Workspace`
  is advisory. Document clearly; user is the only auth
  boundary.
- **`web_fetch` HTML extraction quality** depends on
  `golang.org/x/net/html` correctness. Edge cases:
  malformed HTML, weird encodings, very large inline JS.
  Test the basics; don't promise perfection.
- **`read_file` binary sniff** may mis-flag UTF-16/UTF-32
  text as binary. Acceptable: those are rare on Linux/Mac;
  LLM falls back to `shell iconv`. Also catches all image
  formats (PNG / JPEG / GIF / WebP / PDF), which is the
  desired behavior for MVP — vision support is deferred.
- **Output truncation** must include a clear marker
  (`"... [truncated, total 150 KB] ..."`) so the LLM
  knows to refetch with a tighter range / different
  command. Without the marker, the LLM might not realize
  output was cut.
- **5 tools at once is a lot to test.** Splitting into
  sub-units (05-1, 05-2, 05-3, 05-4, 05-5, 05-6) keeps each
  commit reviewable.
- **New direct dep** (`golang.org/x/net/html`) lands in
  05-5. ADR-0001 needs an update line.

## Performance impact

- `golang.org/x/net/html`: ~50 KB to binary, 0 transitive
  sub-deps (it's part of Go's official extended stdlib).
- Total direct dep count: 3 → 4 (`go-openai` + `hyperchao/di`
  + `yaml.v3` + `golang.org/x/net/html`). Within 8 budget.
- Steady-state RSS: no impact (tools are stateless /
  per-call).
- Per-call peak: `web_fetch` allocates up to 1 MB for the
  raw HTML then drops it; safe within 80 MB per-`Run` cap.
- `shell` output cap (100 KB) bounds memory.

## Layering

- `internal/tools/*` implement `agent.Tool`. They MAY
  import `internal/agent` and `internal/provider` (for
  `provider.ToolSpec`'s JSON Schema type if they need to
  build it dynamically — currently not, they use a static
  `json.RawMessage`).
- `internal/tools/*` MUST NOT import `cmd/chaosbot/*` (no
  reverse dep).
- Tools are wired in `cmd/chaosbot/wire.go` only.

## 实现笔记

> Filled as each sub-unit lands.
