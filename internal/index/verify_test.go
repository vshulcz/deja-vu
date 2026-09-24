package index

import "testing"

// "Ran here and passed" is true of most of what a session runs, and on a real
// store the top candidate for two files in this repository was a cleanup chain
// — `kill %1; pkill -f 'codex exec'; echo …` — run in two sessions, exit 0, and
// worth none of the bytes it costs at every action.
func TestOnlyACommandThatChecksTheWorkCountsAsVerifying(t *testing.T) {
	for _, cmd := range []string{
		"SVC_FIXTURES=$PWD/fixtures GOFLAGS_EXTRA=\"-tags golden\" make test",
		"go test ./internal/store -run TestGolden",
		"cd /w/app && npm run test",
		"golangci-lint run ./...",
		"python3 -m pytest tests/",
		"go build ./... && go vet ./...",
	} {
		if !VerifyCommand(cmd) {
			t.Errorf("a command that checks the work reads as one that does not: %q", cmd)
		}
	}
	for _, cmd := range []string{
		"kill %1 2>/dev/null; pkill -f 'codex exec' 2>/dev/null; echo \"codex probe done\"",
		"git commit -am wip",
		"cp -R /tmp/a /tmp/b",
		"curl -s https://example.com",
		"npm install",
		"git status --short",
		"go run ./cmd/deja index --rebuild",
		"make test && rm -rf /tmp/scratch",
	} {
		if VerifyCommand(cmd) {
			t.Errorf("a command that checks nothing is offered as the way to check an edit: %q", cmd)
		}
	}
}
