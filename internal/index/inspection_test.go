package index

import "testing"

// InspectionCommand decides whether "you have run this before" is worth saying.
// Two shapes were reaching it as changes: git's own options sit before the
// subcommand, so `git -C dir status` was read as the subcommand `-C`, and a
// line that starts with `cd` was read as `cd`.
func TestInspectionCommandReadsPastGitOptionsAndCd(t *testing.T) {
	for _, cmd := range []string{
		"git status --short",
		"git -C /tmp/wt status --short",
		"git --no-pager -C /tmp/wt log -1",
		"git -c user.name=x -C /tmp/wt show HEAD",
		"cd /tmp && git -C /tmp/wt grep -n foo",
		"cd /tmp; ls -la",
	} {
		if !InspectionCommand(cmd) {
			t.Errorf("looking at state read as changing it: %q", cmd)
		}
	}
	for _, cmd := range []string{
		"git checkout -- cmd/deja/mcp.go",
		"git -C /tmp/wt checkout HEAD -- x.go",
		"cd /tmp && rm -rf build",
		"brew install coreutils",
		"ls -la && rm -rf build",
	} {
		if InspectionCommand(cmd) {
			t.Errorf("changing state read as looking at it: %q", cmd)
		}
	}
}

// The miner asks a wider question than the hook: could this have been the
// remedy at all? Running the suite is how a fix is checked, and a grep for the
// symbol the compiler named is what an agent does before fixing it — the rule
// that keeps a pair is satisfied by construction there.
func TestInvestigationCommandCoversTheStepsBeforeAFix(t *testing.T) {
	for _, cmd := range []string{
		"go test ./internal/index/ -count=1",
		"go vet ./...",
		"grep -rn 'undefined' internal/",
		"sed -n '10,20p' internal/index/ingest.go",
		"rg -n reduceToTerms",
		"gofmt -l internal",
	} {
		if !investigationCommand(cmd) {
			t.Errorf("a step taken before the fix read as the fix: %q", cmd)
		}
	}
	for _, cmd := range []string{
		"sed -i '' 's/a/b/' f.go",
		"go mod tidy",
		"brew install gnu-sed",
		"rm internal/index/zz_probe_test.go",
	} {
		if investigationCommand(cmd) {
			t.Errorf("a change read as investigation: %q", cmd)
		}
	}
	// The hook's question is narrower and must stay that way: an agent runs
	// `go test` in every session, and that is worth saying nothing about
	// while still being an ordinary thing to do rather than an inspection.
	if InspectionCommand("go test ./... -count=1") {
		t.Error("the miner's rule leaked into the hook's")
	}
}
