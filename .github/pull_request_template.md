## What this changes

<!-- What the change does and why. Link the issue it fixes, if any. -->

## Checks

- [ ] `gofmt -l .` prints nothing, and `go vet ./...` and `go test -race ./...` pass
- [ ] `golangci-lint run` passes
- [ ] New or changed behaviour has tests on the package's public interface
- [ ] A change to the wire, the server or the test helpers ran the e2e suite (`go test -tags e2e ./e2e`); the agent versions are in docs/harnesses.md
- [ ] A change to `internal/ui/web` rebuilt and committed `internal/ui/dist`
- [ ] User-visible changes have a line under `## [Unreleased]` in CHANGELOG.md
- [ ] Docs (README, SCRIPTS.md, docs/) say what changed
