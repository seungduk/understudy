# Faults and usage limits (measured)

What each harness does with each fault and usage limit understudy can play, measured on the real CLIs.

## Faults

What each harness did with each fault (measured 2026-11-18 with the versions in [harnesses.md](harnesses.md#harness-versions-tested)):

| Script | Codex | opencode | pi | Gemini CLI | Antigravity |
|---|---|---|---|---|---|
| `http_error: {error: api_error}` | retried | retried | retried | retried | **not retried**, exit 3 |
| `http_error: {error: overloaded}` | retried | retried | retried | retried | retried |
| `http_error: {error: rate_limit, retry_after_seconds: 1}` | retried | retried | retried | retried | retried |
| `stream_error: {error: overloaded}` | **not retried**: `Selected model is at capacity`, exit 1 | retried | retried | retried | kept the partial text as a turn and asked again |
| `cut: {after_events: 3, how: reset}` | retried (`Reconnecting... 1/5`) | retried | retried | retried | retried |
| `http_error: {error: invalid_request, retry: never}` | not retried, exit 1 | not retried, exit 1 | not retried, exit 0 | not retried | not retried, exit 3 |
| `limit: {window: per_minute}` | retried | retried after retry-after | retried | retried after RetryInfo | retried |
| `limit: {window: seven_day}` | `You’ve hit your usage limit`, exit 1 | waits out retry-after | 4 requests, then `429 The usage limit has been reached` | `You exceeded your current quota`, no retry | `RESOURCE_EXHAUSTED`, exit 3 |

Codex with a 500 that never ends sends 30 requests (6 stream attempts of 5 HTTP tries each)
before `We’re currently experiencing high demand`.

| Script | Wire | Claude Code 2.1.325 did |
|---|---|---|
| `http_error: {error: overloaded}` | 529 `overloaded_error` | retried (exit 0 when a later attempt answers) |
| `http_error: {error: api_error}` | 500 `api_error` | retried |
| `stream_error: {error: overloaded}` | 200, `message_start`, `ping`, `event: error` | retried the whole request |
| `cut: {after_events: 4, how: close}` | stream closed mid-way | retried |
| `cut: {after_events: 0, how: reset}` / `after_events: 3` | TCP RST before / during the stream | retried |
| `stall: {gate: g}` | the reply waits until `POST /_understudy/gates/g/release` | waited, then went on |
| `limit: {window: seven_day, ...}` | 429 with the unified headers (below) | `You've hit your weekly limit · resets <time>`, exit 1, no retry (gateway token) |
| strict divergence | 400 `invalid_request_error`, `x-should-retry: false` | retried 5 times, then `API Error: 400 understudy: the request does not fit the script: ...`, exit 1 |

## Account-limit simulation

### Codex, Gemini and the others

The same `limit` reply is each protocol's own limit:

- **OpenAI protocols** (Codex, opencode, pi): `429` with
  `{"error":{"type":"usage_limit_reached","resets_at":<unix>,"limit_window_minutes":N,...}}`,
  `x-codex-primary-used-percent: 100`, `x-codex-primary-window-minutes`, `x-codex-primary-reset-at`,
  `retry-after` and `x-should-retry: false`. Codex 0.170.0 parses it (in `codex-api`) as a usage
  limit and prints `You’ve hit your usage limit. ... try again at <time>.`, exit 1, no retry.
  Without `x-should-retry: false` the OpenAI SDK in pi sleeps for the whole `retry-after`.
  `per_minute` is a `429 rate_limit_exceeded` (`Please try again in Ns.`) that every harness retries.
- **Gemini** (Gemini CLI, Antigravity, pi): `429 RESOURCE_EXHAUSTED` with
  `google.rpc.QuotaFailure` (`quotaId` `GenerateRequestsPerDayPerProjectPerModel` for `daily`,
  `...PerMinute...` for `per_minute`) and `google.rpc.RetryInfo`. Gemini CLI 0.68.0 reads a
  per-day quota as terminal (`You have exhausted your daily quota on this model.`), a delay over
  300 s as terminal (`Suggested retry after 7200s`), and retries a shorter one after `retryDelay`.
- **Anthropic** to harnesses other than Claude Code (opencode, pi): the unified headers below;
  they show the body's message.

### Claude Code

Measured on 2026-11-18 with **Claude Code 2.1.325** (macOS arm64), against a probe server and then
the e2e suite:

- A 429 carrying these headers:

  ```
  x-should-retry: false
  retry-after: <seconds>
  anthropic-ratelimit-unified-status: rejected
  anthropic-ratelimit-unified-representative-claim: seven_day     (five_hour, seven_day_opus, seven_day_sonnet)
  anthropic-ratelimit-unified-reset: <unix seconds>
  anthropic-ratelimit-unified-7d-status: rejected                 (5h-* for five_hour)
  anthropic-ratelimit-unified-7d-reset: <unix seconds>
  anthropic-ratelimit-unified-7d-utilization: 1.0
  ```

  makes Claude Code print `You've hit your weekly limit · resets 10:37pm (Asia/Seoul)` (or
  `5-hour limit`, `Opus limit`), set `api_error: "usage_limit_reached"` on its result line, exit 1,
  and send no retry, **when it authenticates the way subscription and gateway accounts do**. It
  reads the unified headers only for claude.ai subscribers, or when `ANTHROPIC_BASE_URL` points
  somewhere other than `api.anthropic.com` and `ANTHROPIC_AUTH_TOKEN` is a JWT whose audience is
  `claude-gateway`. understudy's `--auth gateway` (`clienv.Gateway`) sends such a token: an
  unsigned JWT (`alg: none`) that nothing can verify, so it is not a credential.
- With a dummy API key, the same 429 is a plain rate limit:
  `API Error: Request rejected (429) · <message>`.
- Without `x-should-retry: false`, Claude Code retries the 429 (up to 10 times) in either mode.

Other measured facts (2.1.325): `claude -p` sends `HEAD /api/hello` at start even with
`ANTHROPIC_BASE_URL` set; each turn is one `POST /v1/messages?beta=true` with `stream: true`; the
conversation carries a `role: "system"` message holding `Primary working directory: <cwd>` (which
`{{cwd}}` uses); a failed Bash call comes back as `is_error: true` with `Exit code N` first; an Edit
without permission comes back as an error, so scripted runs pass `--dangerously-skip-permissions`;
`ANTHROPIC_CUSTOM_HEADERS` reaches every request; `--append-system-prompt` reaches the `system`
field.
