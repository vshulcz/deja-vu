package index

import "testing"

func TestAFailureIsLaidOnlyAtTheProgramThatCouldHaveProducedIt(t *testing.T) {
	for cmd, want := range map[string]bool{
		"go test ./...": true,
		"$ go test ./... 2>&1 | tail -5  → exit 1":             true,
		`go test ./cmd/deja 2>&1 | grep -E "^--- FAIL|^ok"`:    true,
		"make check; echo exit=$?":                             true,
		"gh pr checks 41; go test ./internal/index":            false,
		"git fetch -q origin && [ $a == $b ] && echo same":     false,
		`grep -rn "Title" docs | python3 -c 'import json'`:     false,
		"mkdir -p out && ls out/*":                             false,
		"gofmt -l cmd internal; go vet ./... && go test ./...": false,
		"python3 scripts/check.py || true":                     true,
		`cd repo && git status --short`:                        false,
	} {
		if got := failureIsTheHeads(cmd); got != want {
			t.Errorf("failureIsTheHeads(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// zsh names the glob it refused; the warning belongs to the part that holds it.
func TestAGlobErrorIsOnFileAgainstThePartThatHeldTheGlob(t *testing.T) {
	acc := newCommandFailAcc()
	for _, key := range []string{"a", "b"} {
		acc.command(key, "go test ./... 2>&1 | grep -rn --include=*.go Flock")
		acc.output(key, "p", "zsh:1: no matches found: --include=*.go")
		acc.command(key, "ls build/*.tar")
		acc.output(key, "p", "zsh:1: no matches found: build/*.tar")
	}
	got := acc.table()
	if len(got) != 1 || got[0].Head != "ls build/*.tar" {
		t.Errorf("want only the ls glob on file, got %+v", got)
	}
}

// The run the warning is built from: a chain's error is not its head's.
func TestAChainsErrorIsNotOnFileAgainstItsFirstCommand(t *testing.T) {
	acc := newCommandFailAcc()
	for _, key := range []string{"a", "b"} {
		acc.command(key, "gh pr checks 41; go test ./internal/index")
		acc.output(key, "p", "--- FAIL: TestLedgerRollsBack\nFAIL\n")
		acc.command(key, "go test ./internal/store 2>&1 | tail -3")
		acc.output(key, "p", "--- FAIL: TestLedgerRollsBack\nFAIL\n")
	}
	got := acc.table()
	if len(got) != 1 || got[0].Head != "go test ./internal/store" {
		t.Errorf("want only the go test failure on file, got %+v", got)
	}
}
