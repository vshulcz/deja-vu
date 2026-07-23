# Contributing

## Build and Test

deja requires Go 1.25 and has no runtime Go dependencies. Before opening a pull
request, run:

```sh
go test ./... -race -count=1
go vet ./...
go build ./cmd/deja
```

Run `gofmt -s` on changed Go files. Every package touched by a change must keep
at least 95% statement coverage. Measure each package directly so aggregate
coverage does not hide a regression:

```sh
go test ./internal/sources/ -coverprofile=coverage.out
go tool cover -func=coverage.out
```

Replace `internal/sources` with each package changed. CI prints only the
aggregate number, so the two commands above are how you check the figure review
actually looks at — run them before opening the PR and there will be no
surprises. Do not add low-value tests only to move the number; explain a
genuinely unreachable branch in the pull request instead.

## Hermetic Tests

Tests must not read or modify a contributor's real agent configuration or
history.

- Use `t.TempDir()` for files and directories.
- Set both `HOME` and `USERPROFILE` with `t.Setenv`.
- Also set every location variable the code path reads: relevant `DEJA_*`
  variables and, when applicable, `CODEX_HOME`, `GEMINI_CLI_HOME`,
  `CURSOR_CONFIG_DIR`, `CLAUDE_CONFIG_DIR`, or
  `AIDER_CHAT_HISTORY_FILE`.
- Keep paths valid on Windows. Guard Unix-only behavior with `runtime.GOOS` and
  skip it on unsupported platforms.
- Check cleanup errors where practical. In particular, do not leave a bare
  error-returning call in a `defer`.

Use table-driven tests from the standard library. The project does not use
testify or other test frameworks.

## Adding or Updating a Harness Parser

The [source parser registry](docs/ARCHITECTURE.md#source-parsers) maps harnesses
to their implementation under `internal/sources`. Follow the integration steps
in [Add a new harness](docs/ARCHITECTURE.md#add-a-new-harness); a parser alone is
not enough because discovery, incremental indexing, install support, and user
documentation are separate paths.

Fixtures must be synthetic and minimal. Put reusable cross-package samples
under `fixtures/synthetic`; keep parser-specific cases near their tests. Use
stable IDs and timestamps, retain only fields needed to exercise the format,
and replace credentials and identifying paths. SQLite fixtures should be built
from a checked-in script such as `fixtures/synthetic/make_opencode_db.sh`, not
copied from a local agent database.

A parser change should cover discovery, normal parsing, malformed or truncated
input, and incremental behavior where the format is append-only. Add an index
integration test and install/doctor coverage when those commands recognize the
harness.

If an upstream format changed, use the
[format drift report](https://github.com/vshulcz/deja-vu/issues/new?template=format-drift.yml).
For a new harness, use the
[parser request](https://github.com/vshulcz/deja-vu/issues/new?template=parser-request.yml)
before contributing real session data.

## Pull Requests and Review

- Keep changes small and explain the user-visible behavior.
- Add or update tests and documentation with the implementation.
- Do not commit private history, generated local indexes, or machine-specific logs.
- No CLA.

A pull request is ready to merge when CI is green, affected packages meet the
coverage bar, tests are hermetic on supported platforms, documentation matches
the behavior, and review comments are resolved. Maintainers may ask for a
smaller change when unrelated refactoring makes behavior difficult to review.
