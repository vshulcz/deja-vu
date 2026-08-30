package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// "no matches in 1 indexed session — try fewer words" is advice about wording
// for a session the rule never let search open: the count named the whole
// store rather than the part this path may read (#986).
func TestNoMatchesCountsWhatSearchMayRead(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer", Project: "work/api", Role: "user", Text: "peer work on the vault rotation"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	// Without a rule the miss is a wording problem and reads like one.
	var out bytes.Buffer
	printNoMatches(&out, dir, "ticker window", false)
	if !strings.Contains(out.String(), "no matches in 1 indexed session") {
		t.Errorf("an ordinary miss lost its count:\n%s", out.String())
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out.Reset()
	printNoMatches(&out, dir, "ticker window", false)
	got := out.String()
	if strings.Contains(got, "try fewer words") {
		t.Errorf("the reader was sent after their wording:\n%s", got)
	}
	if !strings.Contains(got, "trust policy") {
		t.Errorf("nothing named the rule that emptied the search path:\n%s", got)
	}
	if strings.Contains(got, "in 1 indexed session") {
		t.Errorf("the count still names sessions search cannot read:\n%s", got)
	}
}

// The ignore rule withholds sessions the same way and was not counted at all:
// every other surface said three while the empty answer said six, and told the
// reader to reword a query against history search never opened (#2707).
func TestNoMatchesCountsPastTheIgnoreRuleToo(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	write := func(project, id, day string) {
		t.Helper()
		store := filepath.Join(root, "-"+project)
		if err := os.MkdirAll(store, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","message":{"role":"user","content":"the zonkoshard retry budget in ` + project + `"},` +
			`"timestamp":"2026-08-0` + day + `T10:00:00Z","sessionId":"` + id + `","cwd":"/` + project + `"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A project name no test name can contain: Ignored substring-matches the
	// whole session path, and a t.TempDir() under a test called "hide" would
	// hide every session in the fixture.
	write("keep", "k1", "1")
	write("keep", "k2", "2")
	write("zonkohide", "h1", "3")
	write("zonkohide", "h2", "4")
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	printNoMatches(&out, dir, "quingleflarp", false)
	if !strings.Contains(out.String(), "no matches in 4 indexed sessions") {
		t.Fatalf("the fixture does not hold four sessions to begin with:\n%s", out.String())
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*zonkohide*"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out.Reset()
	printNoMatches(&out, dir, "quingleflarp", false)
	got := out.String()
	if !strings.Contains(got, "no matches in 2 indexed sessions") {
		t.Errorf("the count names sessions the ignore rule keeps out of every answer:\n%s", got)
	}

	// And when the rule hides everything, the reader is told which rule did
	// it: counting the ignore rule into the same zero would otherwise put this
	// store under a sentence about the trust policy, quoting a trust rule that
	// allows everything.
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	printNoMatches(&out, dir, "quingleflarp", false)
	got = out.String()
	if !strings.Contains(got, "the ignore rule withholds every indexed session") {
		t.Errorf("a store emptied by the ignore rule was not told so:\n%s", got)
	}
	if strings.Contains(got, "trust policy") {
		t.Errorf("the wrong rule was named:\n%s", got)
	}
	if strings.Contains(got, "try fewer words") {
		t.Errorf("the reader was sent after their wording:\n%s", got)
	}
}
