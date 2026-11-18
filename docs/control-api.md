# Control API

On the same address, under `/_understudy/`:

| Call | Does |
|---|---|
| `GET health` | `{ok, version, script_loaded}` |
| `PUT script` (body: the YAML) | load a script; `400 {problems: [...]}` when it does not check (or names a harness without a profile) |
| `GET script` | the loaded script, as it was written |
| `GET script/info` | `{loaded, divergence, provenance, sessions: [{key, line, steps: [{name, line}]}]}` |
| `POST validate` (body: the YAML) | `{ok, sessions, problems}` without loading it |
| `GET sessions` | every session: `{key, state, turn, step, branch, requests, divergences, gate, ...}` |
| `GET requests[?session=KEY]` | the request log: per request also the `prompt`, the `answer` sent, `held` while stalled, and `expected`/`actual` for a divergence |
| `GET events` | Server-Sent Events: `change` with `{"version": N}` after every change of the log, gates or script |
| `POST reset` | forget the log, attempt counts and gates (waiting requests are released) |
| `GET gates`, `POST gates/NAME/release` | gates and who waits on them |
| `GET clock`, `POST clock/advance {"ms": N}` | the virtual clock (`serve --clock virtual`) |
