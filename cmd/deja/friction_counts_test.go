package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// friction's own note says how many sessions a rule keeps out; the sentence
// beside it counted the whole store, so one screen said both "none of the 4
// indexed sessions recorded tool output" and "the ignore rule keeps 2 sessions
// out of this listing". Every other surface counts what the answer could have
// held (#2707); this is the one that did not (#2709).
func TestFrictionCountsWhatItMayRead(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	write := func(project, id, day string) {
		t.Helper()
		store := filepath.Join(root, "-"+project)
		if err := os.MkdirAll(store, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","message":{"role":"user","content":"deploy the staging cluster"},` +
			`"timestamp":"2026-08-0` + day + `T10:00:00Z","sessionId":"` + id + `","cwd":"/` + project + `"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Nothing recorded tool output anywhere, and half the store is hidden: the
	// sentence is about how much friction had to read, which is two.
	write("keep", "k1", "1")
	write("keep", "k2", "2")
	for i := 1; i <= 2; i++ {
		write("zonkohide", "h"+strconv.Itoa(i), strconv.Itoa(i+2))
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*zonkohide*"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out, err := captureRun(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "none of the 2 indexed sessions recorded tool output") {
		t.Errorf("friction did not count what it may read:\n%s", out)
	}
}

// And with nothing hidden the sentence still names the store, because there
// the two are the same number.
func TestFrictionStillNamesTheStoreWhenNothingIsHidden(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(root, "-keep")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		id := "k" + strconv.Itoa(i)
		body := `{"type":"user","message":{"role":"user","content":"deploy the staging cluster"},` +
			`"timestamp":"2026-08-0` + strconv.Itoa(i) + `T10:00:00Z","sessionId":"` + id + `","cwd":"/keep"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "none of the 3 indexed sessions recorded tool output") {
		t.Errorf("a store with nothing hidden lost its count:\n%s", out)
	}
}

// And a machine whose history is entirely behind a rule is not a machine
// without history. Counting only what friction may read sent that store into
// the arm written for an empty index — "run `deja index`" over a note saying
// two rules are hiding things, which is the claim #1020 and #1044 closed
// elsewhere.
func TestFrictionDoesNotCallAHiddenStoreAnEmptyMachine(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(root, "-zonkohide")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		id := "h" + strconv.Itoa(i)
		body := `{"type":"user","message":{"role":"user","content":"deploy the staging cluster"},` +
			`"timestamp":"2026-08-0` + strconv.Itoa(i) + `T10:00:00Z","sessionId":"` + id + `","cwd":"/zonkohide"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*zonkohide*"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out, err := captureRun(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "deja index") || strings.Contains(out, "no agent history was found") {
		t.Errorf("a store hidden by a rule was reported as a machine with no history:\n%s", out)
	}
	if !strings.Contains(out, "the ignore rule") {
		t.Errorf("nothing named the rule that leaves friction nothing to read:\n%s", out)
	}
	if !strings.Contains(out, "2 indexed session") {
		t.Errorf("the sessions on disk went uncounted:\n%s", out)
	}
}
