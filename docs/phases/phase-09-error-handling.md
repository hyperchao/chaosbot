# Phase 09 — Provider error handling & retry

> Classify provider errors, retry transient failures, surface actionable
> messages to the user. Today all provider errors bubble up as opaque
> `error: agent: chat: openai: ...` strings.

## Frontmatter

| Field | Value |
|---|---|
| Phase | `09` |
| Sub-units | `09-1` … `09-3` |
| Status | `✅ complete` (all 3 sub-units landed; see 实现笔记 — 09-1 via `fce21fd`+`7d36201`+`154afe7`; 09-2 via `7d36201`; 09-3 via `ebec145`; progress.md updated retroactively) |
| Owner | chaosbot authors |
| Pre-requisites | Phase 02 (Provider), Phase 04 (Agent loop), Phase 07 (REPL) |
| Estimated total LOC | ~150 Go + ~80 test |
| Performance impact | retry adds latency on transient failures; bounded by MaxRetries |

## Goal

Make provider failures survivable. Today:
- 429 rate limit → REPL crashes with raw `error: agent: chat: openai: ...`
- 401 auth failure → same opaque error
- 5xx / network → same, no retry
- Context-length error → known (04-4b deferred) but not user-friendly

After this phase:
- 429 / 5xx / network → automatic exponential backoff retry, REPL keeps going
- 401 / 403 → clear message, REPL keeps going
- 400 bad request → clear message, REPL keeps going
- All errors carry actionable advice (what to do next)

## Design

### 1. Provider error classification

Add to `internal/provider` package:

```go
var (
    ErrRateLimited  = errors.New("provider: rate limited")
    ErrAuthFailed   = errors.New("provider: authentication failed")
    ErrServerError  = errors.New("provider: server error")
    ErrBadRequest   = errors.New("provider: bad request")
    ErrNetwork      = errors.New("provider: network error")
)
```

Provider implementations (OpenAI) classify HTTP responses:

| HTTP status | Sentinel |
|---|---|
| 429 | ErrRateLimited |
| 401, 403 | ErrAuthFailed |
| 400 | ErrBadRequest |
| 5xx | ErrServerError |
| timeout, connection refused, DNS | ErrNetwork |
| other | ErrBadRequest (default) |

Classification is in the OpenAI provider's response handling, not in the agent. Agent just `errors.Is` against sentinels.

### 2. Retry policy

Config:
```go
type Config struct {
    ...
    MaxRetries    int           // default 3
    RetryBaseDelay time.Duration // default 1s
}
```

Retry behavior:
- **Retryable**: ErrRateLimited, ErrServerError, ErrNetwork
- **Non-retryable**: ErrAuthFailed, ErrBadRequest, ErrContextLength
- **Strategy**: exponential backoff with jitter
  - delay = RetryBaseDelay × 2^attempt + random(0, RetryBaseDelay)
  - attempt 0: ~1s, attempt 1: ~2s, attempt 2: ~4s
- **MaxRetries = 0**: no retry (current behavior)
- Retry respects `ctx.Done()` (Ctrl-C cancels)

Where retry lives:
- In the **provider** (e.g., `openai.Provider.Chat`), not the agent
- Agent stays simple: just call `Chat`, get back either success or a final error
- This way any caller of the provider (agent, future tools) gets retry for free

### 3. User-facing error messages

Agent's `Run` checks for known sentinels and produces a clear message:

| Error | User message |
|---|---|
| ErrContextLength | "context too long; type /reset to start a new session" |
| ErrRateLimited | "rate limited, retried N times; please wait and try again" |
| ErrAuthFailed | "authentication failed; check your API key (CHAOSBOT_API_KEY or provider.api_key)" |
| ErrServerError | "provider server error after N retries; try again later" |
| ErrBadRequest | "bad request: <details> (this may be a bug; please report)" |
| ErrNetwork | "network error: <details>" |
| other | "<wrapped error>" (current behavior) |

REPL catches the error, prints the message, shows the next `>` prompt.
Currently the REPL aborts on any error — we need to make non-fatal errors recoverable.

### 4. REPL non-fatal error handling

Current REPL behavior (after agent.Run returns an error):
- print the error
- show next `>` prompt? or exit?

Need to verify current behavior. The fix:
- Print error message
- Continue the loop, show `>` prompt
- The conversation history is unchanged (per 04-3 contract: failed Run doesn't append)

## Sub-units

### 09-1 — Provider error sentinels + classification

Files: `internal/provider/errors.go`, `internal/provider/openai/openai.go`

- Define 5 sentinels (ErrRateLimited, ErrAuthFailed, ErrServerError, ErrBadRequest, ErrNetwork)
- In OpenAI provider: classify HTTP response status, wrap with appropriate sentinel
- Add tests for each status code

### 09-2 — Retry with exponential backoff

Files: `internal/provider/openai/openai.go`, `internal/config/config.go`

- Add MaxRetries, RetryBaseDelay to provider.Config
- Implement retry loop in OpenAI Chat:
  ```go
  for attempt := 0; attempt <= maxRetries; attempt++ {
      resp, err := p.doChat(ctx, req)
      if err == nil {
          return resp, nil
      }
      if !isRetryable(err) {
          return nil, err
      }
      if attempt == maxRetries {
          return nil, err
      }
      delay := backoff(attempt, baseDelay)
      select {
      case <-ctx.Done():
          return nil, ctx.Err()
      case <-time.After(delay):
      }
  }
  ```
- Add jitter to backoff (random 0-100% of baseDelay)
- Tests: succeed on 2nd try, fail after MaxRetries, ctx cancel during backoff

### 09-3 — Agent error translation + REPL non-fatal

Files: `internal/agent/agent.go`, `cmd/chaosbot/cli/cli.go`

- Agent's `step` returns error wrapped with context
- New helper `humanError(err) string` translates sentinels to actionable messages
- Agent.Run checks for sentinels:
  - ErrContextLength → clear message + clear history
  - ErrRateLimited / ErrServerError / ErrNetwork → clear message (retry already happened)
  - ErrAuthFailed / ErrBadRequest → clear message
  - other → pass through
- REPL: after `agent.Run` returns error, print message, continue loop

## Test points

### 09-1 tests
1. **classify_429** → ErrRateLimited
2. **classify_401** → ErrAuthFailed
3. **classify_403** → ErrAuthFailed
4. **classify_400** → ErrBadRequest
5. **classify_500** → ErrServerError
6. **classify_502** → ErrServerError
7. **classify_timeout** → ErrNetwork
8. **classify_unknown** → ErrBadRequest

### 09-2 tests
1. **retry_succeeds_on_2nd** — first call 429, second call 200 → returns response
2. **retry_exhausted** — all 3 calls 429 → returns final 429 error
3. **retry_no_retry_on_auth** — 401 returns immediately, no retry
4. **retry_no_retry_on_bad_request** — 400 returns immediately
5. **retry_backoff_respects_ctx** — ctx cancel during backoff → returns ctx.Err
6. **retry_disabled_when_maxRetries_0** — single call, no retry
7. **retry_jitter** — verify backoff is not exactly deterministic

### 09-3 tests
1. **humanError_rateLimited** — returns actionable message
2. **humanError_authFailed** — returns "check API key" message
3. **REPL_continues_on_rate_limit** — error printed, REPL still accepts next input
4. **REPL_continues_on_auth_error** — same
5. **ErrContextLength_clear_message** — "type /reset" suggestion

## Risks

- **Retry storms**: if all clients retry at the same time, can worsen rate limiting
  - Mitigation: jitter in backoff spreads retries
- **Long waits on bad networks**: 3 retries × 4s = up to 12s on top of LLM latency
  - Mitigation: ctx cancel works, MaxRetries configurable
- **Error message leaks**: showing raw error details could leak API keys or internal info
  - Mitigation: humanError() picks known fields, not raw error

## Not in scope

- Circuit breaker (stop calling after N consecutive failures)
- Adaptive retry based on Retry-After header (just sleep fixed)
- Per-tool retry (tools don't have transient errors usually)
- Retry metrics / observability

---

### 实现笔记 09-1 — Provider error sentinels + 分类

**Files**: `internal/provider/provider.go` (sentinels), `internal/provider/openai/openai.go` (classification)
**Commits**: `fce21fd` (ErrContextLength), `7d36201` (5 sentinels + classify + retry loop, split later), `9fd5712` (review fixes), `154afe7` (context_length_exceeded → ErrContextLength)

**Sentinels** in `internal/provider/provider.go`:
- `ErrContextLength` (precursor from `fce21fd`, used by summarization reactive path)
- `ErrRateLimited`, `ErrAuthFailed`, `ErrServerError`, `ErrBadRequest`, `ErrNetwork`

**Classification path** in openai package:
- `classifyOpenAIError(err)` dispatches on type:
  - `*openaipkg.APIError` → check `apiErr.Code == "context_length_exceeded"` first (route to `ErrContextLength`), else `classifyByStatus(apiErr.HTTPStatusCode)`
  - `*openaipkg.RequestError` → `classifyByStatus(reqErr.HTTPStatusCode)` (HTML/error bodies)
  - other → `ErrNetwork`
- `classifyByStatus(status, msg)`:
  - 429 → `ErrRateLimited`
  - 401 / 403 → `ErrAuthFailed`
  - 5xx → `ErrServerError`
  - 400 → `ErrBadRequest` (the `context_length_exceeded` branch was lifted to `classifyOpenAIError` by `154afe7` so it can inspect `apiErr.Code`, which `RequestError` lacks)
  - other → `ErrBadRequest` with status echo

### 实现笔记 09-2 — Retry with exponential backoff

**Files**: `internal/provider/openai/openai.go`, `internal/config/config.go`, `internal/provider/provider.go`
**Commits**: `7d36201` (initial), `9fd5712` (review fixes: defaults via helper funcs, jitter, ctx-aware delay)

**Config** in `provider.Config`:
- `MaxRetries int` (default 3 via `retryOrDefault`)
- `RetryBaseDelay time.Duration` (default 1s via `retryBaseOrDefault`)

**Retry loop** in `openai.Provider.Chat`:
- `for attempt := 0; attempt <= p.maxRetries; attempt++`
- Non-retryable (`isRetryable(err)` false: `ErrAuthFailed`, `ErrBadRequest`, `ErrContextLength`) → return immediately, no retry
- Retryable (`ErrRateLimited`, `ErrServerError`, `ErrNetwork`) → backoff + jitter, respect ctx cancel
- After final attempt → return last classified error
- `backoffWithJitter(attempt, base)` = `base << attempt + uniform random [0, base)`, cap at 60s
- Per-attempt `slog.Debug` log added later by commit `e326707`

### 实现笔记 09-3 — HumanError + REPL non-fatal

**Files**: `internal/agent/human_error.go` (new), `internal/agent/agent.go` (forces compress on ErrContextLength)
**Commits**: `ebec145`, `9fd5712`

**`agent.HumanError(err) string`** maps each sentinel to actionable user-facing copy:
- `ErrContextLength` → `"context too long; type /reset to start a new session"`
- `ErrRateLimited` → `"rate limited; please wait a moment and try again"`
- `ErrAuthFailed` → `"authentication failed; check CHAOSBOT_API_KEY or provider.api_key"`
- `ErrServerError` → `"provider server error; please try again later"`
- `ErrBadRequest` → `"bad request (this may be a bug; please report): <err.Error()>"`
- `ErrNetwork` → `"network error: <err.Error()>"`
- default → pass through `err.Error()`

**agent.go**: `step` already detects `errors.Is(stepErr, provider.ErrContextLength)` and passes `forceCompress=true` on the next step (reactive summarization). The `ErrContextLength` sentinel itself was added in `fce21fd`; this phase just gives the user a "type /reset" hint via `HumanError`.

**REPL non-fatal**: the actual loop continues after `agent.Run` returns the error; nothing in this phase changed the loop because it was already non-fatal (errors print + next prompt). The improvement is the message itself going through `HumanError`.

### 偏差 from spec

- 4 vs 5 sentinels: spec listed `ErrContextLength` implicitly via the "context-length error" goal but the 5 explicit sentinels table omits it; implementation actually has 6 sentinels (`ErrContextLength` + the 5 from spec).
- `backoffWithJitter` overflow protection: `base << attempt` overflows `time.Duration` if `attempt` is large; mitigated by clamping `exp > 60s` to 60s.
- `isRetryable` does not include `ErrContextLength` (spec says non-retryable) — confirmed; reactive path is via `forceCompress`, not retry.

### Follow-ups (deferred)

- No `slog.Info` when retry kicks in (only Debug); users with default log level won't see "retrying in Ns".
- `HumanError("network error: ...")` includes raw err text which may leak internals on some errors.
- No metric for retry attempt count; debugging requires log inspection.

**progress.md retroactive update**: this phase was implemented but progress.md was never updated until now. The four 2026-07-01 commits (`51cdaaf`/`e326707`/`154afe7`) and 2026-07-02 (`47d5e27`) landed without spec frontmatter flips; Phase 09 sub-units landed in commits `fce21fd`/`7d36201`/`ebec145`/`9fd5712` earlier in the 06-xx window without progress.md rows.
