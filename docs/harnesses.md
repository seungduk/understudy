# Harnesses

Which agent harnesses understudy serves, how to point each one at it, and the versions tested.

## Supported harnesses

| Harness | Protocol(s) understudy serves it | Tested version | Session key | e2e (2026-11-18, macOS arm64) |
|---|---|---|---|---|
| Claude Code | Anthropic Messages | 2.1.325 (2.1.326 on Linux) | header | 11 tests pass |
| OpenAI Codex CLI | OpenAI Responses (Codex dropped Chat Completions) | codex-cli 0.170.0 | header | 7 of 7 |
| opencode | Chat Completions, Anthropic Messages, DeepSeek provider | 1.19.6 | header | 7 of 7 per protocol |
| pi coding agent | Chat Completions, Responses, Anthropic Messages, Gemini | 0.73.1 | header | 7 of 7 per protocol |
| Gemini CLI | Gemini | 0.68.0 | header (`GEMINI_CLI_CUSTOM_HEADERS`) | 7 of 7 |
| Google Antigravity CLI (`agy`) | Gemini (`modelProvider: gemini`) | 1.5.11 | prompt marker | 7 of 7 |
| DeepSeek API users | Chat Completions at `/chat/completions`, Anthropic at `/anthropic/v1/messages` | (no official DeepSeek harness exists) | as the harness | opencode's `deepseek` provider: 7 of 7; Claude Code at `/anthropic`: covered |

The seven e2e tests per harness: a text answer, a tool loop that edits a real file, a branch on
a failing test, a retry after a server error, the usage-limit message, a stall held until a gate
is released, and record-then-replay through the recording proxy.

DeepSeek has no official coding harness (its own documentation points Claude Code at
`https://api.deepseek.com/anthropic` and lists opencode); understudy serves both DeepSeek
surfaces: Chat Completions without `/v1` (DeepSeek's base URL), thinking as DeepSeek's
`reasoning_content`, and the Anthropic API under `/anthropic`.

## Claude Code

`understudy env` prints `export`/`unset` lines: it points `ANTHROPIC_BASE_URL` at the server,
sets a dummy `ANTHROPIC_API_KEY`, turns off telemetry and non-essential traffic, and removes any
variable that could carry a real credential. Run the CLI with its own empty `CLAUDE_CONFIG_DIR`
so it never reads your login. understudy never needs or forwards a real credential, and the
request log never keeps `Authorization`, `x-api-key` or any header that looks like a secret.

## Other harnesses

`understudy setup` writes a harness's config files into an empty home folder and prints the
command that runs it against the server, in an empty environment with dummy keys only:

```sh
understudy setup --harness codex --home /tmp/codex-home --session t1/writer --prompt "fix the bug"
# env -i PATH=... HOME=/tmp/codex-home CODEX_HOME=/tmp/codex-home/.codex UNDERSTUDY_DUMMY_KEY=understudy-dummy-key \
#   codex exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox -C ... 'fix the bug' </dev/null
understudy harnesses          # every profile: protocols, how each action maps to its tools
```

What it sets up, per harness (never your own config: the home folder is new):

| Harness | Points it at understudy with | Runs |
|---|---|---|
| Codex | `config.toml`: `model_providers.understudy` with `base_url = "URL/v1"`, `wire_api = "responses"`, `env_key` naming a dummy key, `http_headers` with the session | `codex exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox` with stdin empty (it reads more prompt from a pipe) |
| opencode | `OPENCODE_CONFIG` file: an `@ai-sdk/openai-compatible` provider with `baseURL = URL/v1`, the built-in `anthropic` and `deepseek` providers with `baseURL`, headers with the session, permissions allowed | `opencode run --format json --model understudy/fake-chat` (or `anthropic/fake-claude`, `deepseek/deepseek-chat`) |
| pi | `PI_CODING_AGENT_DIR/models.json`: one provider per API (`openai-completions`, `openai-responses`, `anthropic-messages`, `google-generative-ai`) with `baseUrl` and headers | `pi -p --mode json --model us-chat/fake-chat` (or `us-responses/...`, `us-anthropic/...`, `us-gemini/...`) |
| Gemini CLI | `GOOGLE_GEMINI_BASE_URL=URL`, `GEMINI_API_KEY` dummy, `settings.json` with `selectedType: gemini-api-key` and folder trust off, `GEMINI_CLI_CUSTOM_HEADERS` with the session | `gemini -p ... --output-format stream-json --yolo --model gemini-3.1-pro-preview` |
| Antigravity | `settings.json` with `modelProvider: gemini` (without it `GEMINI_API_KEY` is ignored), `GOOGLE_GEMINI_BASE_URL=URL`, `AGY_CLI_DISABLE_AUTO_UPDATE` | `agy -p ... --output-format stream-json --dangerously-skip-permissions`; the session marker goes into the prompt |

Side requests are answered without the script and logged with `aux`: opencode's title request,
Gemini CLI's routing request (sent when no `--model` is given).

## Harness versions tested

| Harness | Version | Installed from | Result | Date |
|---|---|---|---|---|
| Codex CLI | 0.170.0 | npm `@openai/codex` | 7/7 | 2026-11-18 |
| opencode | 1.19.6 | npm `opencode-ai` | 7/7 over Chat Completions, Anthropic and its DeepSeek provider | 2026-11-18 |
| pi | 0.73.1 | npm `@mariozechner/pi-coding-agent` | 7/7 over Chat Completions, Responses, Anthropic and Gemini | 2026-11-18 |
| Gemini CLI | 0.68.0 | npm `@google/gemini-cli` | 7/7 | 2026-11-18 |
| Antigravity CLI | 1.5.11 | Google's release tarball (the Homebrew cask's URL and checksum) | 7/7 | 2026-11-18 |

All were installed into a private prefix, run with fresh home folders and dummy keys, and never
signed in.

## Claude Code versions tested

| Version | Platform | Result | Date |
|---|---|---|---|
| 2.1.325 | macOS 27 arm64 | e2e suite passes (11 tests) | 2026-11-18 |
| 2.1.325 | macOS 27 arm64 | e2e suite passes (12 tests, with the MCP loop and the web UI's log fields) | 2026-11-18 |
| 2.1.326 | Linux x86_64 | e2e suite passes (11 tests) | 2026-11-18 |
| 2.1.325 | Linux arm64 microVMs on macOS | a full agent-factory practice run, 19 requests | 2026-11-18 |
