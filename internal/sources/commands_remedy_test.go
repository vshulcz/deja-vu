package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The allowlist enumerated `go test|build|vet|run` and stopped, so every `go`
// subcommand that changes the build's inputs was dropped — and those are the
// remedies. `deja fix` could see the failure and never the thing that repaired
// it (#1635).
func TestWorthIndexingKeepsTheCommandsThatFixABuild(t *testing.T) {
	for _, c := range []string{
		"go mod tidy",
		"go mod vendor",
		"go mod download",
		"go get ./...",
		"go get github.com/x/y@latest",
		"go install ./cmd/deja",
		"go generate ./...",
		"go work sync",
		"go clean -modcache",
		// brew belongs here for the same reason, and by the same measurement:
		// `brew install jq` was in the eleven-command fixture and was dropped
		// with the rest. "the tool is missing" is an error people paste, and
		// the remedy is an install.
		"brew install jq",
		"brew upgrade",
		"cd repo && go mod tidy",
	} {
		if !worthIndexing(c) {
			t.Errorf("dropped the remedy: %q", c)
		}
	}
	// The controls: navigation stays dropped, and a meaningful name inside a
	// navigation command's argument is still not a command.
	for _, c := range []string{
		"cat go.mod | grep module",
		"ls go.sum",
		"echo 'go mod tidy'",
		"grep 'brew install' notes.md",
	} {
		if worthIndexing(c) {
			t.Errorf("kept navigation: %q", c)
		}
	}
}

// A repository's own scripts are its checks and its fixes, and none of them
// were indexed, so the failure hint never had a pair to show for them.
func TestWorthIndexingKeepsProjectScripts(t *testing.T) {
	for _, c := range []string{
		"./scripts/check.sh",
		"./scripts/render.sh --theme dark",
		"bash scripts/check.sh",
		"sh ./hack/verify.sh",
		"python3 tools/gen_feed.py",
		"cd repo && ./bin/test",
		"./configure",
	} {
		if !worthIndexing(c) {
			t.Errorf("dropped the project script: %q", c)
		}
	}
	// And through the reader: a Claude transcript that ran the script keeps it.
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	dir := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s1.jsonl")
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-09-02T09:59:00Z","cwd":"/work/app","message":{"role":"user","content":"run the checks"}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-09-02T10:00:00Z","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"Bash","input":{"command":"./scripts/check.sh"}}]}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err = %v", len(ss), err)
	}
	kept := false
	for _, m := range ss[0].Messages {
		kept = kept || (m.Role == RoleCommand && strings.Contains(m.Text, "./scripts/check.sh"))
	}
	if !kept {
		t.Error("the transcript's ./scripts/check.sh run was not kept as a command")
	}
	for _, c := range []string{
		"bash /tmp/probe.sh",
		"cat ./scripts/check.sh",
		"grep -n foo ./scripts/check.sh",
		"echo ./scripts/check.sh",
		"ls scripts/",
		"./scripts/check.sh <<EOF\nx\nEOF",
	} {
		if worthIndexing(c) {
			t.Errorf("kept: %q", c)
		}
	}
}

// An install pointed at a mirror is the remedy to an index that refused the
// package, and `pip3`, `pip config`, conda and apt were dropped (#5599).
func TestWorthIndexingKeepsTheMirrorInstalls(t *testing.T) {
	for _, c := range []string{
		"pip3 install --index-url https://mirror.example.internal/simple vllm",
		"pip config set global.index-url https://mirror.example.internal/simple",
		"pip download torch -d wheels",
		"conda install -c https://mirror.example.internal/conda pytorch",
		"conda config --add channels https://mirror.example.internal/conda",
		"mamba install numpy",
		"sudo apt-get install -y libssl-dev",
		"apt install build-essential",
	} {
		if !worthIndexing(c) {
			t.Errorf("dropped the remedy: %q", c)
		}
	}
	for _, c := range []string{
		"grep 'pip3 install' README.md",
		"echo 'apt install foo'",
	} {
		if worthIndexing(c) {
			t.Errorf("kept navigation: %q", c)
		}
	}
}
