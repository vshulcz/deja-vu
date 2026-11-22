package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestInspectionCommandSplitsPipesGroupsAndEnvPrefixes(t *testing.T) {
	narrowInspect := []string{
		"(cd /work/app && git status)",
		"FOO=1 BAR=baz cat a.go | head -n 5",
		"{ git diff; cat foo.go; }",
	}
	for _, cmd := range narrowInspect {
		if !InspectionCommand(cmd) {
			t.Errorf("InspectionCommand(%q) = false, want true", cmd)
		}
	}

	investigations := []string{
		"(cd /work/app && git status)",
		"FOO=1 BAR=baz cat a.go | head -n 5",
		"{ git diff; cat foo.go; }",
		"FOO=1 BAR=baz rg foo | head -n 5",
		"{ git diff; rg bar; }",
		"gh search prs bug",
		`gh search code "ViewerCommand" --repo acme/app`,
		"gh api repos/acme/app/pulls/1/comments",
		"gh api -X GET /repos/acme/app/issues",
		"gh api --method=get /repos/acme/app/issues",
		"gh run watch 42",
		"gh pr checks 42 --watch",
	}
	for _, cmd := range investigations {
		if !investigationCommand(cmd) {
			t.Errorf("investigationCommand(%q) = false, want true", cmd)
		}
	}

	nonInspect := []string{
		"go test ./...",
	}
	for _, cmd := range nonInspect {
		if InspectionCommand(cmd) {
			t.Errorf("InspectionCommand(%q) = true, want false", cmd)
		}
	}

	mutating := []string{
		"gh api -X POST repos/acme/app/issues",
		"gh api --method POST repos/acme/app/issues",
		"gh api repos/acme/app/issues -f title=hi",
		"gh pr merge 1",
		"rm -rf build",
		`echo "{a,b}" && rm -f tmp.out`,
		"ls (foo && rm -rf build",
	}
	for _, cmd := range mutating {
		if InspectionCommand(cmd) {
			t.Errorf("InspectionCommand(%q) = true, want false", cmd)
		}
		if investigationCommand(cmd) {
			t.Errorf("investigationCommand(%q) = true, want false", cmd)
		}
	}
}

func TestViewerCommandDistinguishesViewersFromTestRunners(t *testing.T) {
	viewers := []string{
		`rg "connection refused" internal/`,
		"grep -C 3 panic handler.go",
		"cat internal/app/main.go",
		"git status",
		"git diff HEAD~1",
		"git show HEAD:cmd/app/main.go",
		"sed -n '1,20p' internal/app/main.go",
		"FOO=1 rg err internal/ | head -n 20",
	}
	for _, cmd := range viewers {
		if !ViewerCommand(cmd) {
			t.Errorf("ViewerCommand(%q) = false, want true", cmd)
		}
	}

	nonViewers := []string{
		"go test ./...",
		"go vet ./...",
		"pytest tests/",
		"sed -i '' 's/a/b/' internal/app/main.go",
		"make build",
	}
	for _, cmd := range nonViewers {
		if ViewerCommand(cmd) {
			t.Errorf("ViewerCommand(%q) = true, want false", cmd)
		}
	}
}

func TestFrictionLineDropsGrepContextAndLongPathSource(t *testing.T) {
	dropped := []string{
		"42- // panic: connection refused",
		"internal/very/long/subpackage/handler.go-42- fatal: repository not found",
		"internal/very/long/subpackage/handler.go:42: // panic: connection refused",
		`internal/very/long/subpackage/handler.go:42:10: case "connection refused":`,
		`{Role: "tool-output", Text: "psql: connection refused on port 5432"},`,
		`Text: "psql: connection refused on port 5432",`,
	}
	for _, line := range dropped {
		if got, ok := FrictionLine(line); ok {
			t.Errorf("FrictionLine(%q) = (%q, true), want false", line, got)
		}
	}

	kept := []string{
		"--- FAIL: TestCheckout (0.02s)",
		`psql: error: connection to server at "localhost", port 5432 failed: Connection refused`,
	}
	for _, line := range kept {
		if _, ok := FrictionLine(line); !ok {
			t.Errorf("FrictionLine(%q) = false, want true", line)
		}
	}
}

func TestFixPairsSkipsViewerStdoutKeepsEnvFailureAndFiltersStoredViewerPairs(t *testing.T) {
	at := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	// Viewer stdout containing error-looking text is skipped when !env.
	viewerStdout := []model.Message{
		{Role: roleCommand, Text: `$ rg -n "connection refused" internal/  → exit 0`, Time: at},
		{Role: roleToolOutput, Text: "internal/app/handler.go:12: connection refused on port 5432", Time: at.Add(time.Second)},
		{Role: roleCommand, Text: "$ go test ./internal/app/...  → exit 0", Time: at.Add(2 * time.Second)},
		{Role: roleToolOutput, Text: "ok", Time: at.Add(3 * time.Second)},
	}
	if pairs := fixPairsIn(viewerStdout, "claude:v1", "app"); len(pairs) != 0 {
		t.Fatalf("fixPairsIn(viewer stdout) = %+v, want empty", pairs)
	}

	// Shell/env invocation failure of a viewer stays mined (`env == true`).
	viewerEnvFail := []model.Message{
		{Role: roleCommand, Text: "$ rg -n panic *.go internal/  → exit 1", Time: at},
		{Role: roleToolOutput, Text: "zsh:1: no matches found: *.go", Time: at.Add(time.Second)},
		{Role: roleCommand, Text: "$ rg -n panic '*.go' internal/  → exit 0", Time: at.Add(2 * time.Second)},
		{Role: roleToolOutput, Text: "cmd/app/main.go:1", Time: at.Add(3 * time.Second)},
	}
	envPairs := fixPairsIn(viewerEnvFail, "claude:v2", "app")
	if len(envPairs) == 0 {
		t.Fatal("fixPairsIn(viewer env failure) = empty, want mined pair")
	}

	// Query-time filter drops legacy stored viewer pairs while keeping test failures.
	dir := t.TempDir()
	viewerSig := frictionHash(mustFriction(t, `fatal: repository "https://example.invalid/repo.git" not found`))
	testSig := frictionHash(mustFriction(t, "--- FAIL: TestCheckout (0.02s)"))
	storedViewer := FixPair{
		Sig:     viewerSig,
		Failed:  "$ rg -n fatal internal/",
		Error:   `fatal: repository "https://example.invalid/repo.git" not found`,
		Command: "$ go test ./...",
		Project: "app",
	}
	storedTest := FixPair{
		Sig:     testSig,
		Failed:  "$ go test ./internal/checkout/...",
		Error:   "--- FAIL: TestCheckout (0.02s)",
		Command: "$ go test -run TestCheckout ./internal/checkout/...",
		Project: "app",
	}
	if !IsViewerFixPair(storedViewer) {
		t.Fatalf("IsViewerFixPair(storedViewer) = false, want true")
	}
	if IsViewerFixPair(storedTest) {
		t.Fatalf("IsViewerFixPair(storedTest) = true, want false")
	}
	writeFixesForTest(t, dir, []FixPair{storedViewer, storedTest})

	if got := FixesForCommand(dir, "go test ./...", `fatal: repository "https://example.invalid/repo.git" not found`, 3, nil); len(got) != 0 {
		t.Fatalf("FixesForCommand returned legacy viewer pair: %+v", got)
	}
	if got := FixesForCommand(dir, "go test ./internal/checkout/...", "--- FAIL: TestCheckout (0.02s)", 3, nil); len(got) != 1 || got[0].Sig != testSig {
		t.Fatalf("FixesForCommand(test) = %+v, want storedTest", got)
	}
}

func TestGiveUpLineIgnoresEnclosingQuotesAndBackticks(t *testing.T) {
	quoted := []string{
		"the marker `* reverted: ...` is documented in notes",
		"в заметке было написано «откатили обратно вчера»",
		`he quoted “reverted the cache” from another doc`,
		`she wrote "rolled back the migration" inside the issue`,
		`the token 'reverted' was highlighted in red`,
	}
	for _, line := range quoted {
		if got, ok := GiveUpLine(line); ok {
			t.Errorf("GiveUpLine(%q) = (%q, true), want false", line, got)
		}
	}

	genuine := []string{
		"we reverted the migration after staging broke",
		`"fast path" was broken so we reverted the patch`,
	}
	for _, line := range genuine {
		if _, ok := GiveUpLine(line); !ok {
			t.Errorf("GiveUpLine(%q) = false, want true", line)
		}
	}
}
