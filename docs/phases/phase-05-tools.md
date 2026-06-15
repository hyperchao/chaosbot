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
| Status | `✅ complete` (6/6 sub-units done; see 实现笔记) |
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

### 05-1 — `tools/fs/read_file.go`

**新增文件**:
- `internal/tools/fs/read_file.go` **158 行**:`ReadFileTool`
  struct(无状态,`struct{}`)+ 4 个 `agent.Tool` 方法 + 内部
  helpers (`sniffBinary` / `renderWindow`)+ `readFileArgs`
  反序列化结构。
- `internal/tools/fs/read_file_test.go` **180 行**:
  6 个表驱动测 + 编译期断言
  `var _ agent.Tool = (*fstools.ReadFileTool)(nil)` +
  `mustArgs` / `min` helpers。`package fs_test`(外部 test 包);
  `chaosbot/internal/tools/fs` 用 alias `fstools` 导入避免
  与 `io/fs` 重名。

**关键实现选择**:
- **`bufio.Scanner` max token = `readFileMaxBytes` (256 KB)**:
  `scanner.Buffer(make([]byte, 64*1024), readFileMaxBytes)`。
  默认 64 KB token 限制在长 JSON / 大 YAML 单行会爆;提到
  跟 output cap 同值 256 KB。**对称设计**:单行 > 256 KB
  直接 `bufio.ErrTooLong` 失败(LLM 改用 `shell + head` /
  `shell + sed -n`),而不是写到 output cap 截断后留下
  `"1\t<256 KB 前缀>\n... [truncated] ..."` 这种"line 编号
  在但内容被截"的无用 payload。
- **byte cap 触发时整段重写**:`out.Bytes()[:256*1024]` 然后
  `out.Reset()` + `Write(truncated)` + 追加 marker。比
  `Truncate(256*1024)` 更明确意图;后者会留底层数组没释放
  一次。
- **line cap 计数 `remaining`**:写满 2000 行触发 cap 时,
  内层 for 继续 `scanner.Scan()` 数剩下的行数,marker 里
  写 "file has N more lines"。一次 O(N) 扫描,N ≤ ∞ 但
  调用方拿到 marker 知道去哪取 → MVP OK;真要追求极致
  可换成 `io.LimitReader` + 两遍,不值。
- **错误前缀 `read_file:`**:所有 Go 错误统一 wrap 前缀
  (validation + binary sniff);`*os.PathError` 例外,
  按 spec 透传,LLM 看到 "no such file or directory"
  这样的标准错误更易处理。
- **`f.Seek(0, io.SeekStart)`**:`sniffBinary` 读完后回到
  文件头,`renderWindow` 从头扫。比关掉重开省 1 个 fd
  (在 Linux 上还是同 fd,只是 offset 重置)。

**测试覆盖**(6 个,全 PASS):
- `TestReadFile_Whole` — 3 行文件,默认窗口,验证
  `"1\thello\n2\tworld\n3\tfoo\n"`。
- `TestReadFile_LineRange` — 5 行文件,`start_line=2,
  end_line=3`,验证只返 2-3 行且 line 编号对。
- `TestReadFile_Binary_Rejects` — 写 `"abc\x00def"`
  (NUL 在 512 字节 sniff 窗口内),验证 Go error 包含
  "binary"。
- `TestReadFile_NotFound_Propagates` — 路径不存在,
  `errors.Is(err, os.ErrNotExist)`。**用 `os.ErrNotExist`
  而非 `io/fs.ErrNotExist`**,两者是 Go 1.16+ 的 alias,
  但 `os` 是本项目已用包,避免新增 import。
- `TestReadFile_LineCap` — 2500 行文件,显式传 `end_line=999999`,
  验证输出 2000 + 1 marker 行(共 2001),marker 包含
  "2000-line cap reached" 和 "500 more"。
- `TestReadFile_LongLine_FailsFast` — 单行 300 KB(无 newline),
  验证 Go error 且 `errors.Is(err, bufio.ErrTooLong)`。**取代
  原始 spec 的 `TestReadFile_ByteCap`** — symmetry 设计下
  scanner max = output cap,超长单行 fail-fast 不再走"output
  截断"路径(那条路径现在只对"多行总和超 cap"有意义,日常
  2000 行 × 平均 200 字节 = 400 KB 才会触发,典型代码文件
  遇不到)。

**Spec 偏差**:
- 删除了"## 05-1 spec"独立节(impl 阶段草稿):review 时
  指出 phase-05-tools.md 现有的 Sub-units 段 + Test points
  表 + Tool specs 段 + Risks 段 + Performance impact 段已经
  覆盖了 SDD 要求的 "goal, public API/interface, data
  structures, test points, risks, performance impact"
  全部 6 要素,只是粒度 phase-level 而非 sub-unit-level。
  05-1 sub-unit 的细节放在本实现笔记里。
- `start_line` / `end_line` validation 反复改:初版按 spec
  JSON Schema `minimum: 1` 严拒 ≤0;第二版放宽 `== 0` 走
  default 跟 JSON Schema 矛盾;最终版用 `*int` 区分
  "省略"(走 default)和"传了 ≤0"(strict error),
  spec JSON Schema 不动。
- `bufio.Scanner` max token 跟 `readFileMaxBytes` 同步 256 KB
  (spec 没说,impl 时 review 指出原始 1 MiB × 256 KB 不对称
  会留下"line 编号在但内容被截"的无用 payload,改为
  fail-fast)。

**Layering**:
- `internal/tools/fs/read_file.go` import `bufio` / `bytes`
  / `context` / `encoding/json` / `errors` / `fmt` / `io`
  / `os`,全部 stdlib。
- 不 import `internal/agent`(避免 tools→agent 反向依赖;
  编译期断言在 test 文件做)。
- 不 import `cmd/chaosbot/*`(符合 phase-05 "Layering" 段)。

**自验**:`make test` 8/8 包 PASS(新增 `internal/tools/fs`
8 测,既有 6 包 31 测不变);`make build` 通过;`make lint`
clean;`gofmt -l .` clean。

### 05-2 — `tools/fs/write_file.go`

Atomic via `tmp+fsync+rename`. Parent directory
`MkdirAll(dir, 0o755)` on demand. Tmp file is
`path + ".tmp"` (fixed name, no random suffix) with
`0o600`; on any failure between Create and Rename the
tmp file is removed. *os.PathError propagates verbatim so
the LLM sees "no such file or directory" / "permission
denied" in the tool-message stream.

Eight tests cover: create new file under a non-existent
parent (parent auto-created), overwrite existing file,
no `.tmp` leftover on success, original file intact when
the parent is a file (MkdirAll fails before any write),
0600 permissions on Linux (skipped on Windows), missing
'path' arg, malformed JSON args, pre-canceled context.

### 05-3 — `tools/fs/edit_file.go`

Strict unique-anchor check: zero or many matches both
fail without modifying the file. Error message includes
the match count and the first five byte offsets so the
LLM can grow the anchor to disambiguate without
re-reading the whole file. Empty `old_text` is rejected
(empty anchor matches every position, equivalent to
infinite matches). Empty `new_text` is allowed and
deletes the anchor. Reuses `writeFileAtomic` so a
mid-edit crash leaves the original file intact.

Eight tests cover: unique single-line replace, multi-line
anchor, missing anchor (file unchanged), non-unique
anchor with offset reporting, empty old_text rejection,
empty new_text deletion, missing path arg, pre-canceled
context.

### 05-4 — `tools/shell/shell.go`

`/bin/sh -c` invocation, stdout and stderr merged into a
single `cappedWriter` (100 KB cap with truncation marker).
Default timeout 30 s, max 600 s. ctx cancellation
propagates to the child via `exec.CommandContext`, which
SIGKILLs the process when the deadline fires or the
parent ctx is cancelled. Exit codes are surfaced as part
of the tool result (not returned as Go errors) so the
LLM can see "exit 1" or "exit 127" and react; non-zero
exits are not errors from the tool's perspective — only
child-kill events are. Reply format:
`"<output><truncation_marker>\n--- exit <N>"`.

Ten tests cover: echo success, non-zero exit code,
merged stderr, timeout kills child (asserts elapsed < 3 s
for `sleep 5` with 1 s timeout), truncation at 100 KB
with marker (uses `head -c 200000` so the child exits
cleanly and the truncation path is exercised, not the
timeout path), invalid/negative timeout, empty command,
pre-canceled context, command-not-found (exit 127).

### 05-5 — `tools/web/web.go`

HTTP GET (http/https only) with body capped at 1 MB via
`io.LimitReader`. Body is run through the
`golang.org/x/net/html` tokenizer to extract visible
text: `<script>` and `<style>` blocks (plus `<noscript>`,
`<head>`, `<template>`) are skipped via depth tracking,
tags are dropped, and HTML entities are decoded by the
tokenizer. Block-level elements (p, div, h1..h6, li, tr,
section, article, etc.) emit newlines so the LLM gets a
readable layout. Output is capped at 50 KB; overflow
appends a truncation marker. The stdlib `*http.Client`
is reused (a `WebFetchTool` is constructed once per
agent) so a single agent session does not exhaust
ephemeral ports on Linux.

Nine tests cover: basic HTML fetch + tag stripping,
script/style block exclusion, scheme allow-list
(file/ftp/javascript/data all rejected), 4xx/5xx error
surfacing, 1 MB body cap (server sends 2 MB), 50 KB
output cap with truncation marker (5k paragraphs × 40
bytes), empty url, malformed JSON args, pre-canceled
context (httptest server hangs).

**go.mod**: added `golang.org/x/net v0.43.0` as a direct
dep (4th, within 8 budget). v0.43.0 is the newest
release that still compiles under `go 1.24`; newer
versions require 1.25 and would force a toolchain bump.
The `html` and `html/atom` sub-packages are stable, no
API churn in years. 0 transitive sub-deps beyond x/net
itself.

### 05-6 — Default tool registration

No additional code: each tool commit (05-1 through 05-5)
incrementally added its `r.Register(...)` call to the
`Registry` factory closure in `cmd/chaosbot/wire.go`.
The final closure registers all five:
`ReadFileTool`, `WriteFileTool`, `EditFileTool`,
`ShellTool`, `NewWebFetchTool()`. A REPL smoke test
asks the LLM to list its tools and gets back all five
names in alphabetical order (edit_file, read_file,
shell, web_fetch, write_file), confirming the wire
format carries the full tool list to the provider.

## Phase 05 summary

Five built-in tools shipped in 6 sub-units. Total Go
LOC: 1,316 (207 tool impl + 297 tests for fs, 154+177
for shell, 208+203 for web, plus wire.go and go.mod
changes). Direct dep count 3 → 4 (golang.org/x/net
v0.43.0). All `make test`, `make lint`, `gofmt -l .`
clean; REPL smoke confirms all five tools visible to
the LLM and invokable end-to-end.

LLM-driven code review (after a multi-tool session
reading agent.go + write_file.go) flagged several
real issues deferred to Phase 06+:
- **write_file path sandbox**: no allow-list, LLM can
  be tricked into writing `~/.ssh/authorized_keys` or
  `/etc/cron.d/*`. Should add a workspace root
  (already a config field, currently advisory) and
  enforce it in the tool.
- **write_file size cap**: no `MaxBytes`; runaway LLM
  could fill the disk.
- **agent.Run partial history on failure**: the user
  message is appended before the loop, so a ctx
  cancel mid-loop leaves an orphan user message in
  history. Should either roll back the append on
  error or document "Reset after failure".
- **`Registry.Specs()` called per step**: minor perf
  concern if Specs() ever becomes non-trivial.
- **Concurrent Run calls**: `History` is not mutex-
  protected; currently safe only because the agent
  loop is single-threaded (spec 04), but this should
  be either guarded or explicitly documented.
