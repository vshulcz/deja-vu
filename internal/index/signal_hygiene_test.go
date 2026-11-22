package index

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestInspectionCommandSplitsPipesGroupsAndEnvPrefixes(t *testing.T) {
	narrowInspect := []string{
		"   ",
		"$ git status",
		"(cd /work/app && git status)",
		`(echo "a)b" && echo \) && git status)`,
		`echo a\;b && cat "x\"y.go" || ls`,
		`cat 'a\b.go' || head -n 5`,
		"FOO=1 BAR=baz cat a.go | head -n 5",
		"{ git diff; cat foo.go; }",
		"cd /work/app; { git status; ls; }",
		"git stash list",
		"(cat a.go) | (head -n 5)",
		"echo {",
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
		"go env GOPATH",
	}
	for _, cmd := range investigations {
		if !investigationCommand(cmd) {
			t.Errorf("investigationCommand(%q) = false, want true", cmd)
		}
	}

	nonInspect := []string{
		"go test ./...",
		"git stash push -m wip",
		"FOO=1",
		"FOO=1 then",
	}
	for _, cmd := range nonInspect {
		if InspectionCommand(cmd) {
			t.Errorf("InspectionCommand(%q) = true, want false", cmd)
		}
	}

	mutating := []string{
		"FOO=1",
		"FOO=1 then",
		"gh api -X POST repos/acme/app/issues",
		"gh api --method POST repos/acme/app/issues",
		"gh api repos/acme/app/issues -f title=hi",
		"gh api repos/acme/app/issues --input body.json",
		"gh api repos/acme/app/issues --raw-field body=x",
		"gh pr merge 1",
		"rm -rf build",
		`echo "{a,b}" && rm -f tmp.out`,
		`echo foo{ bar } && rm -f tmp.out`,
		"touch {a,b}.txt && rm -f a.txt",
		"ls (foo && rm -rf build",
		"(ls}",
		"sed --in-place 's/a/b/' a.go",
		"sed --in-place=.bak 's/a/b/' a.go",
	}
	for _, cmd := range mutating {
		if InspectionCommand(cmd) {
			t.Errorf("InspectionCommand(%q) = true, want false", cmd)
		}
		if investigationCommand(cmd) {
			t.Errorf("investigationCommand(%q) = true, want false", cmd)
		}
	}

	if !shellBraceStartsGroup("ls && { git status; }", 0, 6) {
		t.Error("shellBraceStartsGroup after && = false, want true")
	}
	for _, bad := range []string{"{", "{a,b}", "(ls}", "(cat a.go) | (head -n 5)", `(echo "unclosed)`} {
		if got, ok := unwrapShellGroup(bad); ok {
			t.Errorf("unwrapShellGroup(%q) = (%q, true), want false", bad, got)
		}
	}
}

func TestViewerCommandDistinguishesViewersFromTestRunners(t *testing.T) {
	viewers := []string{
		"   ",
		"cd /work/app && git log -n 1",
		`rg "connection refused" internal/`,
		"grep -C 3 panic handler.go",
		"cat internal/app/main.go",
		"git status",
		"git blame internal/app/main.go",
		"git diff HEAD~1",
		"git show HEAD:cmd/app/main.go",
		"sed -n '1,20p' internal/app/main.go",
		"FOO=1 rg err internal/ | head -n 20",
		"gh search issues bug",
		"gh api repos/acme/app/issues",
		"gh api -XGET repos/acme/app/issues",
		"gh pr view 1",
		"gh pr diff 1",
		"gh run list",
		"gh issue status",
	}
	for _, cmd := range viewers {
		if !ViewerCommand(cmd) {
			t.Errorf("ViewerCommand(%q) = false, want true", cmd)
		}
	}

	nonViewers := []string{
		"FOO=1",
		"git checkout -b feat",
		"go test ./...",
		"go vet ./...",
		"pytest tests/",
		"sed -i '' 's/a/b/' internal/app/main.go",
		"sed --in-place 's/a/b/' internal/app/main.go",
		"gh pr create --title test",
		"gh api -XPOST repos/acme/app/issues",
		"gh api repos/acme/app/issues --input body.json",
		"gh api repos/acme/app/issues --raw-field body=x",
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
		"2026-09-10T10:00:00Z 42- // panic: connection refused",
		"internal/very/long/subpackage/handler.go-42- fatal: repository not found",
		"cmd/my-service/handler.go-42- fatal: repository not found",
		"internal/very/long/subpackage/handler.go:42: // panic: connection refused",
		`internal/very/long/subpackage/handler.go:42:10: case "connection refused":`,
		`42: case "connection refused":`,
		"42: // panic: connection refused",
		`{Role: "tool-output", Text: "psql: connection refused on port 5432"},`,
		`{Role: "a\"b", Text: "psql: connection refused on port 5432"},`,
		`{Text: "connection \"refused\" on port 5432"},`,
		`Text: "psql: connection refused on port 5432",`,
	}
	for _, line := range dropped {
		if got, ok := FrictionLine(line); ok {
			t.Errorf("FrictionLine(%q) = (%q, true), want false", line, got)
		}
	}
	if !looksLikeSource("42- // panic: connection refused") {
		t.Error("looksLikeSource(grep context line) = false, want true")
	}
	if !looksLikeSource("42: // panic: connection refused") {
		t.Error("looksLikeSource(bare lineno source line) = false, want true")
	}
	if !looksLikeSource(`{Role: "a\"b", Text: "psql: connection refused on port 5432"},`) {
		t.Error("looksLikeSource(keyed escaped Role+Text line) = false, want true")
	}
	if looksLikeGrepContextLine(`"foo"-42- fatal: repository not found`) {
		t.Error("looksLikeGrepContextLine(quoted token) = true, want false")
	}
	for _, nonLiteral := range []string{`: "bad",`, `foo.Text: "bad",`, `Text: "unclosed`} {
		if keyedStringLiteralValue(nonLiteral) {
			t.Errorf("keyedStringLiteralValue(%q) = true, want false", nonLiteral)
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
		Sig:       viewerSig,
		Failed:    "$ rg -n fatal internal/",
		Error:     `fatal: repository "https://example.invalid/repo.git" not found`,
		Command:   "$ go test ./...",
		Project:   "app",
		Candidate: true,
	}
	storedGrepErr := FixPair{
		Failed:  "$ make check",
		Error:   "internal/app/foo.go-42- fatal: repository not found",
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
	if !IsViewerFixPair(storedGrepErr) {
		t.Fatalf("IsViewerFixPair(storedGrepErr) = false, want true")
	}
	if IsViewerFixPair(storedTest) {
		t.Fatalf("IsViewerFixPair(storedTest) = true, want false")
	}
	writeFixesForTest(t, dir, []FixPair{storedViewer, storedTest})

	if got := FixesForCommand(dir, "go test ./...", `fatal: repository "https://example.invalid/repo.git" not found`, 3, nil); len(got) != 0 {
		t.Fatalf("FixesForCommand returned legacy viewer pair: %+v", got)
	}
	if FixCandidateSeen(dir, `fatal: repository "https://example.invalid/repo.git" not found`, nil) {
		t.Fatal("FixCandidateSeen = true for stored viewer candidate, want false")
	}
	if got := FixesForCommand(dir, "go test ./internal/checkout/...", "--- FAIL: TestCheckout (0.02s)", 3, nil); len(got) != 1 || got[0].Sig != testSig {
		t.Fatalf("FixesForCommand(test) = %+v, want storedTest", got)
	}
}

func TestGiveUpLineIgnoresEnclosingQuotesAndBackticks(t *testing.T) {
	quoted := []string{
		"the marker `* reverted: ...` is documented in notes",
		"header\nthe doc mentions `reverted` token\nfooter",
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
		"«кэш» откатили после деплоя на стейджинге",
		"«мы откатили миграцию после сбоя базы",
	}
	for _, line := range genuine {
		if _, ok := GiveUpLine(line); !ok {
			t.Errorf("GiveUpLine(%q) = false, want true", line)
		}
	}

	nested := "«а откатили «б»"
	if phraseQuotedOrCode(nested, strings.Index(nested, "откатили"), len("откатили")) {
		t.Error("phraseQuotedOrCode(nested guillemet) = true, want false")
	}
	if phraseQuotedOrCode("reverted", 0, len("reverted")) {
		t.Error("phraseQuotedOrCode(phraseAt=0) = true, want false")
	}
}
