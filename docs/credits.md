# Learned from

understudy takes ideas, not code, from these projects (all MIT or Apache-2.0); see [NOTICE](../NOTICE).

- [AkaraChen/fakellm](https://github.com/AkaraChen/fakellm): the event sequence and where `ping`
  goes, measured Claude Code retry behaviour (`docs/wire.md`), reading the conversation position
  from the request history so retries and concurrency are safe, close versus reset cuts, the
  request-log record.
- [majorcontext/harness](https://github.com/majorcontext/harness) (`harnesstest`): named steps and
  block/release gates for deterministic stalls.
- [acus9345/mock-llm-server](https://github.com/acus9345/mock-llm-server): the small reply model
  (text, reasoning, tool calls, error, stream error).
- [piyook/llm-mock](https://github.com/piyook/llm-mock), [theblixguy/llm-mock-server](https://github.com/theblixguy/llm-mock-server),
  [kagent-dev/mockllm](https://github.com/kagent-dev/mockllm): fault and matching vocabularies.
- [sloprail/harness-mocks](https://github.com/sloprail/harness-mocks): a corpus of recorded real
  Claude Code runs (a fake CLI, the opposite approach: it replaces the CLI, understudy keeps it).

What understudy adds: an explicit session key, closed reply kinds and tests instead of free-form
regex rules and embedded scripting, event-gated stalls, the measured subscription-limit
simulation, and record/replay with a divergence policy.
