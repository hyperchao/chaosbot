# ADR-0002: Context window handling for long agent sessions

- **Status**: Accepted (deferred implementation)
- **Date**: 2026-06-15
- **Deciders**: chaosbot authors
- **Supersedes**: —
- **Related**: phase-04-agent-loop, phase-05-tools (LLM-driven
  code review, 06-15), phase-06-session-persistence (future)

## Context

The agent loop sends the entire `History` (every
`provider.Message` accumulated so far) on every Chat call
to the LLM provider. LLM APIs have a fixed context window
per model (8K / 32K / 128K depending on vendor and model),
and exceeding it returns `400 InvalidRequestError` from
the provider — surfacing to the user as a generic
"agent: chat: 400" error that kills the REPL.

Real workloads that risk hitting the window:
- A REPL session that runs `MaxSteps=30` iterations with
  large tool outputs (e.g. `read_file` on a 256 KB source
  file ≈ 64K tokens by itself; `shell` on a 100 KB log).
- Multi-turn sessions where each turn's tool calls leave
  several KB of tool-result messages in history.
- Future vision / multimodal features (Phase N+2) that
  carry per-message payloads much larger than text.

We need a strategy that:
1. Prevents the context-length error from killing a
   session.
2. Preserves as much recent context as practical (the
   LLM's last few turns are usually the load-bearing
   ones; early turns are mostly warmup).
3. Stays within the project's no-new-dep discipline (no
   tiktoken, no LLM-side summarization SDK).
4. Interacts cleanly with Phase 06 session persistence:
   the on-disk format and the in-memory representation
   must agree about what counts as "the history".

## Decisions

### Persistence stores the full history; windowing is
### purely a runtime in-memory view

The session store writes the complete, un-windowed
history to disk. Windowing happens only at Chat-call
time: the agent passes a windowed view of the full
history to the LLM, but `a.History` itself keeps
everything.

- **Rationale**: the user-visible "session" is the
  complete conversation. If the user inspects
  `~/.chaosbot/sessions/<id>.json` they should see the
  full transcript, not an LLM-shaped summary of the
  recent half. The windowed view is a presentation
  concern, not a data-integrity concern.
- **Trade-off**: a session file can grow large (10s of
  MB for very long REPL sessions). The store path is
  Phase 06's problem; we can compress on disk later if
  it becomes an issue.
- **Restoration**: when loading a saved session, the
  agent receives the full history. The first `step()`
  applies windowing before sending to the LLM, so the
  user immediately gets a windowed REPL even though the
  disk has more.

### Sliding window driven by token estimation

The agent owns a `MaxContextTokens` config (default
120_000, set to roughly 80% of a 128K context window
so we leave headroom for the model's own per-response
overhead and the safety buffer below). Before each
`step()` the agent estimates the token count of the
current history; if the estimate exceeds
`MaxContextTokens - Buffer` (Buffer = 10% of
MaxContextTokens), windowing kicks in.

Token estimation is provided by a helper on the
`provider.Provider` interface (new method
`EstimateTokens(content string) int`):

- **Default implementation** (in `internal/provider`,
  all providers inherit) uses a character-count
  heuristic: `len(content) / 3` for Latin / English
  text, `len(content) / 1.5` for CJK. The heuristic
  detects the script by checking for runes >= 0x3000
  in a sample of the first 200 bytes. ±20% accuracy
  vs real tokenizers, which is good enough for
  windowing with a 10% buffer.
- **Providers can override** for higher accuracy:
  OpenAI-compatible providers that ship alongside
  tiktoken can use exact token counts. The default
  heuristic is the floor of acceptable behavior.

The buffer (10% of MaxContextTokens) absorbs the
heuristic's ±20% error and keeps the request comfortably
under the model's hard limit.

### Summarization before windowing drops anything

When windowing fires, the agent doesn't just truncate
the head of the history. It tries to preserve the
information in two stages:

1. **Summarize the early half**: call the LLM once
   with a "summarize the following conversation
   fragment" prompt. The result is a single
   `provider.Message{Role: RoleUser, Content:
   "<summary>"}` (a user-role message because most
   providers refuse non-trailing system messages; we
   can re-promote to system when a provider supports
   it).
2. **Replace early history with the summary**: the
   new history starts with `[summary_msg] + recent`.
3. **Re-check**: if even after summarizing the
   estimate still exceeds the budget, the summary
   itself is dropped and a warning is logged; the
   safety net below takes over.

The summarize call is an extra Chat round-trip and
incurs token cost (input = the early turns, output =
the summary). It only fires when windowing would
otherwise lose non-trivial context; for a one-shot
`chaosbot run` invocation with MaxSteps=30 it fires
at most once.

### Failure-mode safety net: detect and reset

If a Chat call still fails with a context-length error
after windowing + summarization, the agent treats it
as a soft failure:
- `a.History = a.History[:0]` (drop everything from
  the in-memory view, but the persistent store
  retains the full record).
- The error returned to the LLM is wrapped with
  "context too long; in-memory history cleared;
  please repeat your last request" so the LLM knows
  the prior turn is gone and can re-prompt.
- `ErrContextLength` sentinel is exposed from
  `internal/provider` so tests and call sites can
  match.

Detection: substring match on the error string for
`context_length`, `maximum context length`, `tokens`,
`too long` (case-insensitive). Crude but covers OpenAI
and the openai-compatible providers we ship. Each
provider can later add a typed error if needed.

### No new dependencies

- No tiktoken (different tokenizers per model, ~5 MB
  binary, marginal accuracy gain over the heuristic
  for our use case).
- Summarization uses the existing provider; no new
  LLM client.
- The 10% safety buffer absorbs the heuristic's
  inaccuracy.

## Consequences

- `provider.Provider` interface gains
  `EstimateTokens(content string) int`. All existing
  implementations inherit the heuristic default; a
  provider can override for accuracy.
- `agent.Agent` config gets `MaxContextTokens` (default
  120_000) and `ContextBufferFraction` (default 0.10).
  These land in the same `agent.Config` struct that
  already holds `System` / `Model` / `Temperature` /
  `MaxTokens` / `MaxSteps`.
- `internal/provider` exposes `ErrContextLength` as a
  new sentinel that wraps the LLM's
  context-length-exceeded error so call sites can
  branch on it.
- The summarize step is an extra Chat call; tests
  assert it fires only when windowing would otherwise
  lose content, and the resulting summary message
  lives at the head of the new history.
- Phase 06 session save/load round-trips the **full**
  history; the in-memory view at any moment is the
  windowed projection. A test asserts the on-disk JSON
  contains everything that was ever added to
  `a.History` and the LLM only sees a windowed slice.
- A future `/window <tokens>` slash command can adjust
  the budget at runtime. Deferred — see phase-07
  follow-ups; not in MVP.

## Alternatives considered

- **Turn-counting window instead of token-counting**.
  Simpler (no estimation needed) but a "turn" is
  ill-defined: a turn can be 1 user message + 0
  assistant / tool messages, or 1 + 5, depending on how
  many tool calls the LLM made. Token-counting is
  precise about what actually matters (model context
  size) and naturally accounts for variable-size
  tool outputs.

- **Drop the early half without summarizing**. Cheaper
  (no extra LLM call) but throws away information that
  the LLM might still find useful (file paths,
  decisions made, code snippets). The cost of a
  summarize call is bounded (one round-trip per window
  trigger) and the benefit is "the LLM still knows
  what we did 20 turns ago".

- **Use tiktoken for accurate token counting**. Adds
  a dependency for marginal accuracy over the
  heuristic. The 10% safety buffer absorbs the
  heuristic's ±20% error; precise counting only
  matters when the budget is near full, and at that
  point the summarize step will fire anyway.

- **Hard-fail on context length with a clear error
  message and a hint to /reset**. Simple, no code, but
  terrible UX during long sessions. The windowing +
  summarize pipeline makes this unnecessary.

- **Summarize the whole history on every step** (not
  only on windowing). The agent always sends a
  compressed view. Loses the verbatim recent turns that
  are usually the load-bearing ones.

## Implementation plan

Deferred to **Phase 04-4** (a new sub-unit of
phase-04-agent-loop), to be implemented alongside
**Phase 06** session persistence so the two interact
cleanly:

- `provider`: `EstimateTokens` interface method +
  heuristic default implementation +
  `ErrContextLength` sentinel.
- `agent`: windowing in `Run` / `step` driven by
  estimated token count; summarize call before
  dropping turns; safety-net Reset on
  `ErrContextLength`.
- tests: windowing at exactly the threshold, windowing
  with summarization, safety-net reset, provider
  interface contract for the new method.
- Phase 06: persist the full history; the in-memory
  view remains windowed.

Estimated LOC: ~250 Go + 80 test, fitting in 3-4 commits
under the 200-LOC per-turn budget.
