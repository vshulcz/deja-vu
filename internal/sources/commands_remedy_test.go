package sources

import "testing"

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
