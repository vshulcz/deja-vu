package index

import "testing"

func TestAnIrreversibleCommandIsNeverARemedy(t *testing.T) {
	for cmd, want := range map[string]bool{
		"gh pr merge 41 --squash --delete-branch":                  true,
		"git push origin --delete feat/old":                        true,
		"git push -f origin main":                                  true,
		"git branch -D scratch":                                    true,
		"git reset --hard origin/main":                             true,
		"git stash drop stash@{0} 2>&1 | tail -1":                  true,
		"cd repo && git clean -fdx":                                true,
		"rm -rf build && make":                                     true,
		"kubectl -n shop-prod exec db-0 -- env":                    true,
		"kubectl delete pod web-1":                                 true,
		"GOFLAGS=-count=1 gh api -X DELETE repos/o/r/git/refs/x":   true,
		"$ git push -q -u origin fix/x  → exit 0":                  false,
		"git checkout -- internal/store/store.go":                  false,
		"brew install coreutils":                                   false,
		"kubectl -n staging port-forward svc/api 8080:80":          false,
		"rm internal/zz_probe_test.go && go test ./internal/index": false,
		"gh pr checks 41 | grep -v pass":                           false,
		"go test ./... -run TestPushDelete":                        false,
	} {
		if got := remedyIsIrreversible(cmd); got != want {
			t.Errorf("remedyIsIrreversible(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// What the session did next was merge its PR; the agent that hits the same
// error is handed the merge. The sighting behind it still answers.
func TestFixesForSkipsAnIrreversibleRemedy(t *testing.T) {
	dir := t.TempDir()
	sig := frictionHash(mustFriction(t, "zsh:1: command not found: timeout"))
	writeFixesForTest(t, dir, []FixPair{
		{Sig: sig, Command: "gh pr merge 41 --squash --delete-branch", Project: "p"},
		{Sig: sig, Command: "brew install coreutils", Candidate: true, Project: "p"},
	})
	got := FixesFor(dir, "zsh:1: command not found: timeout", 4, nil)
	if len(got) != 1 || got[0].Command != "brew install coreutils" {
		t.Errorf("want only the install, got %+v", got)
	}
}

// When every remedy on file is one FixesFor withholds, the store still holds
// the pair: the empty answer must not say nothing ran after the error.
func TestAWithheldRemedyIsNotReportedAsNothingRan(t *testing.T) {
	dir := t.TempDir()
	sig := frictionHash(mustFriction(t, "zsh:1: command not found: timeout"))
	writeFixesForTest(t, dir, []FixPair{
		{Sig: sig, Command: "gh pr merge 41 --squash --delete-branch", Project: "p"},
		{Sig: sig, Command: "git push -f origin main", Candidate: true, Project: "p"},
	})
	const e = "zsh:1: command not found: timeout"
	if got := FixesFor(dir, e, 4, nil); len(got) != 0 {
		t.Fatalf("want nothing served, got %+v", got)
	}
	if !FixWithheldAsIrreversible(dir, e, nil) {
		t.Error("the withheld merge is not reported")
	}
	if FixCandidateSeen(dir, e, nil) {
		t.Error("an irreversible sighting is reported as waiting for confirmation")
	}
	if FixWithheldAsIrreversible(dir, e, func(string) bool { return false }) {
		t.Error("a pair the policy hides is still reported")
	}
	if FixWithheldAsIrreversible(dir, "some other error nobody hit", nil) {
		t.Error("an unseen error reports a withheld remedy")
	}
}
