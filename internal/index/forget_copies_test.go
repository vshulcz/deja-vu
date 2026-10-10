package index

import (
	"path/filepath"
	"reflect"
	"testing"
)

// A Claude Code fork opens with its source's turns rewritten under its own
// id. Forgetting the source leaves that copy searchable under the fork, so
// forget names the fork rather than saying nothing.
func TestForgetNamesTheForkThatHoldsACopy(t *testing.T) {
	h := newTwoWayEnv(t)
	opening := hPrompt("app", "s1", 2, 0, "rotate the staging signing key") +
		hSay("app", "s1", 2, 1, "Rotated it and updated the verifier.")
	h.put("app", "s1", opening)
	// The same lines with the fork's id, then a turn of its own.
	fork := hPrompt("app", "s9", 2, 0, "rotate the staging signing key") +
		hSay("app", "s9", 2, 1, "Rotated it and updated the verifier.") +
		hPrompt("app", "s9", 2, 5, "now the production one")
	h.put("app", "s9", fork)
	h.put("svc", "s5", hPrompt("svc", "s5", 3, 0, "unrelated cache work"))
	dir := filepath.Join(h.tmp, "a.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	res, err := Forget(dir, ForgetOptions{Session: "s1", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"claude:s9"}; !reflect.DeepEqual(res.Copies, want) {
		t.Fatalf("copies = %v, want %v", res.Copies, want)
	}
	// And the other way: forgetting the fork names the source it copied.
	res, err = Forget(dir, ForgetOptions{Session: "s9", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"claude:s1"}; !reflect.DeepEqual(res.Copies, want) {
		t.Fatalf("copies of the fork = %v, want %v", res.Copies, want)
	}
}
