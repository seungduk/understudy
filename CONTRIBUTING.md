# Contributing

Thanks for helping. understudy stays small and exact; changes are welcome when they keep it so.

## Rules of the code

- **Layers.** Packages depend only downwards: `internal/wire`, `internal/clock`, `internal/gate`,
  `internal/reqlog`, `internal/notify`, `clienv`, `ui` and the module root (the embedded
  documents) at the bottom; then `script`, `internal/conv`, `internal/deliver`; then `server`,
  `record`; then `explain`, `control`, `understudytest`; then `mcpserver`; `cmd/understudy` on top.
  `internal/layers` checks this in `go test ./...`; a new package needs a line in its table.
- **Small interfaces.** Closed enums instead of strings, named enums instead of booleans, bounded
  numbers, validated types. A new script field needs: the Go type, the loader's field table
  (`script/load.go`), a check with a fix-it message (`script/compile.go`), the JSON Schema
  (`script/understudy-v1.schema.json`, kept in step by `script/schema_test.go`), and SCRIPTS.md.
- **Tests bottom-up.** Each package is tested on its own public interface, every case its inputs
  allow, failure cases included. No network beyond loopback in unit tests. `go test -race ./...`
  must pass.
- **Wire facts are measured.** Anything about what Claude Code sends or does is measured against
  a named CLI version and recorded in docs/ (docs/limits.md, docs/harnesses.md), and the e2e suite proves it.

- **The web UI** is TypeScript in `ui/web`, built by esbuild into `ui/dist`, which is committed
  and embedded (`go install` needs no Node). After changing `ui/web`, run
  `cd ui && npm ci && npm run check && npm run build` and commit `ui/dist`; CI fails when it
  differs from a fresh build. It reads only the control API and loads nothing from the network.
- **MCP tools** answer JSON; a failure is `{ok: false, error, fix}`. A new tool needs a
  description an agent can act on, a test in `mcpserver`, and a line in the MCP table in docs/mcp.md.

## Before you send a change

```sh
gofmt -l .            # prints nothing
go vet ./...
go test -race ./...
go test -tags e2e -v ./e2e   # when you touched the wire, the server or the helper, with claude installed
```

Add the Claude Code version you ran the e2e suite with to the table in docs/harnesses.md.

## License

By contributing you agree that your contribution is licensed under Apache-2.0.
