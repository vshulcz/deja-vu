package sources

import (
	"encoding/json"
	"testing"
)

// Three quarters of command text on a real corpus is navigation that answers
// nothing and matches by accident: `cat internal/index/index.go` looks relevant
// to a question about the index. The selection is the feature.
func TestWorthIndexingKeepsWhatHappened(t *testing.T) {
	for _, c := range []string{
		"go test ./internal/index/ -count=1",
		"golangci-lint run",
		"gh pr checks 558",
		"git commit -m 'fix: x'",
		"make build && deja index --rebuild",
		"docker compose up -d",
		// Build/test toolchains beyond the Go/JS/Python core (#1098 sweep):
		// dropping these lost the command history of whole ecosystems.
		"bazel test //services:api_test",
		"deno test spec.ts",
		"jest --watch",
		"vitest run",
		"tox -e py311",
		"sbt compile",
		"ninja -C build",
		"meson compile",
		"cabal test",
		"stack build",
		"zig build test",
		"nix build .#pkg",
		"ctest --output-on-failure",
		"tsc --noEmit",
		"rake spec",
		"phpunit tests/",
		"composer install",
		"clang -O2 main.c",
		"gcc -o app app.c",
		"rustc main.rs",
		// Compound commands: a leading cd/export/source must not sink the real
		// work chained after it — the `cd repo && build` shape is everywhere.
		"cd services/api && go test ./...",
		"cd frontend && npm run build",
		"export CGO_ENABLED=0 && go build ./...",
		"source .venv/bin/activate && pytest -q",
		"cd a && cd b && bazel test //x:y",
	} {
		if !worthIndexing(c) {
			t.Errorf("dropped a command worth keeping: %q", c)
		}
	}
	for _, c := range []string{
		"cat internal/index/index.go",
		"ls -la",
		"grep -rn foo internal/",
		"cd /repo && pwd",
		"sed -n 1,40p internal/index/index.go",
		"echo hi",
		// A meaningful tool name inside a navigation command stays dropped —
		// the trivial prefix wins.
		"cat stack.yaml",
		"which gcc",
		"cd /nix/store",
		// A meaningful command name inside a nav command's ARGUMENT is not a
		// chained segment, so it stays dropped.
		"grep 'go test' logs.txt",
		"cat go.mod | grep module",
		"echo 'run npm build'",
		"ls && cd /tmp",
	} {
		if worthIndexing(c) {
			t.Errorf("kept navigation: %q", c)
		}
	}
}

func TestClaudeCommandsExtractsAndFilters(t *testing.T) {
	raw := json.RawMessage(`[
	  {"type":"text","text":"running it"},
	  {"type":"tool_use","name":"Bash","input":{"command":"go test ./internal/index/"}},
	  {"type":"tool_use","name":"Bash","input":{"command":"ls -la"}},
	  {"type":"tool_use","name":"Read","input":{"file_path":"/x.go"}},
	  {"type":"tool_result","content":"ok"}
	]`)
	got := claudeCommands(raw)
	if len(got) != 1 || got[0] != "$ go test ./internal/index/" {
		t.Fatalf("commands = %#v", got)
	}
}

// The reference parser must agree, or the differential test in #502 is empty.
func TestBothParsersAgreeOnCommands(t *testing.T) {
	body := `[{"type":"tool_use","name":"Bash","input":{"command":"go build ./..."}}]`
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	a, b := commandsFromContent(v), claudeCommands(json.RawMessage(body))
	if len(a) != len(b) || len(a) != 1 || a[0] != b[0] {
		t.Fatalf("generic=%#v typed=%#v", a, b)
	}
}
