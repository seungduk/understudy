# Contributing

Thanks for helping. understudy stays small and exact; changes are welcome when they keep it so.

## Rules of the code

- **Layers.** Packages depend only downwards: `internal/wire`, `internal/clock`, `internal/gate`,
  `internal/reqlog`, `clienv` at the bottom; then `script`, `internal/conv`, `internal/deliver`;
  then `server`, `record`; then `explain`, `understudytest`; `cmd/understudy` on top.
  `internal/layers` checks this in `go test ./...`; a new package needs a line in its table.
- **Small interfaces.** Closed enums instead of strings, named enums instead of booleans, bounded
  numbers, validated types. A new script field needs: the Go type, the loader's field table
  (`script/load.go`), a check with a fix-it message (`script/compile.go`), the JSON Schema
  (`script/understudy-v1.schema.json`, kept in step by `script/schema_test.go`), and SCRIPTS.md.
- **Tests bottom-up.** Each package is tested on its own public interface, every case its inputs
  allow, failure cases included. No network beyond loopback in unit tests. `go test -race ./...`
  must pass.
- **Wire facts are measured.** Anything about what Claude Code sends or does is measured against
  a named CLI version and recorded in the README, and the e2e suite proves it.

## Before you send a change

```sh
gofmt -l .            # prints nothing
go vet ./...
go test -race ./...
go test -tags e2e -v ./e2e   # when you touched the wire, the server or the helper, with claude installed
```

Add the Claude Code version you ran the e2e suite with to the README table.

## Licence

By contributing you agree that your contribution is licensed under Apache-2.0.
