# Performance Budget

This document pins down the hard limits chaosbot must respect to remain
viable on a 1 vCPU / 512 MB server. Numbers are upper bounds, not targets
to flirt with. Every PR that touches the binary, networking, or
allocation patterns should be measured against this doc.

## 1. Hard limits

| Metric | Limit | Why |
|---|---|---|
| Binary size | ≤ 25 MB | Single-binary distribution, cheap to fetch |
| Cold-start RSS | ≤ 30 MB | 512 MB box must have headroom for other tenants |
| Steady-state RSS (empty REPL) | ≤ 40 MB | REPL idles between turns; baseline must stay low |
| Peak RSS per `Agent.Run` | ≤ 80 MB | Tool outputs are bounded; large jobs must stream |
| Cold-start wall time | ≤ 200 ms | `time chaosbot version` should feel instant |
| Direct dependencies | ≤ 8 | See ADR-0001 |
| CGO usage | forbidden | Cross-compile, musl/Alpine, ARM portability |

## 2. Per-tool output caps

These constants live in each tool package and are unit-tested.

| Tool | Default cap | Configurable |
|---|---|---|
| `read_file` | 2000 lines / 256 KB | yes, via args |
| `write_file` | none (full file) | n/a |
| `edit_file` | 256 KB file scan | yes, via args |
| `shell` | 100 KB combined stdout+stderr | no (hard cap to bound memory) |
| `web_fetch` | 1 MB HTML → 50 KB text | no |

If a cap is hit, the tool returns a clear error string explaining the
truncation; the agent loop surfaces it to the LLM so it can retry with a
narrower request.

## 3. Provider layer caps

- LLM request body: ≤ 256 KB. Enforced by the OpenAI provider; large
  histories are trimmed from the front, with a log line.
- LLM response body: ≤ 1 MB. Beyond that the provider returns a parse
  error rather than buffering unbounded JSON.
- HTTP client: a **single** `*http.Client` is constructed at provider
  init with `Timeout = 60 s` and a tuned `Transport` (idle conn pool
  10, idle timeout 90 s, response header timeout 10 s).
- No per-request `http.Client` construction.

## 4. Measurement procedure

`scripts/measure.sh` is the canonical harness (added in Phase 01-3). It
runs three checks and prints a table:

1. **Binary size**: `go build -ldflags="-s -w" -trimpath` then
   `ls -l bin/chaosbot | awk '{print $5}'`.
2. **Cold-start RSS**: `/usr/bin/time -v ./bin/chaosbot version` — read
   `Maximum resident set size (kbytes)`.
3. **Steady-state RSS**: same trick with `./bin/chaosbot repl` running
   idle, sampled after 2 s.
4. **Peak RSS per Run**: a synthetic agent run (mock provider returning
   a fixed 10-step tool loop) under `runtime.ReadMemStats` snapshots.

Each phase that touches binary, memory, or I/O must paste the table
output at the bottom of the corresponding `docs/phases/phase-NN-*.md`
under a "性能基线" heading.

## 5. When to re-measure

- After any change to `go.mod` (dependency change).
- After any change to the `init()` path of any package.
- After any change to the `http.Client` configuration.
- After adding or removing a built-in tool.
- Before tagging a release.

## 6. Regression handling

If a PR pushes any metric past its limit, the PR **must** either:

- Be split into smaller changes that each fit the budget, **or**
- Include a justification in the relevant phase spec and a follow-up
  ADR that explicitly raises the limit.

No silent regressions. The progress table should reflect the new
measured number in the same commit that changes the binary.

## 7. Profiling escape hatches

- `make perf-cpu` runs the synthetic agent loop under `pprof` and writes
  `cpu.prof` for later inspection.
- `make perf-heap` does the same for heap allocations.
- `CHAOSBOT_PPROF=:6060` (planned, not in MVP) starts an HTTP pprof
  endpoint when set; default off to keep the binary lean.
