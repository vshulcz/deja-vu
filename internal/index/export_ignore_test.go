package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exportStore holds one session recall may serve and one the ignore rule keeps
// out, each saying something only it says.
func exportStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	// DEJA_POLICY_FILE, not a file under the home: this package shares one
	// XDG_CONFIG_HOME across its tests, so a policy written through
	// policy.Path() outlives the test that wrote it and every later test reads
	// it — which is how a rule of `*scratch*` silenced the default rule three
	// tests were pinning.
	policyFile := filepath.Join(home, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	write := func(project, id, text string) {
		p := filepath.Join(root, "-w-"+project, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("keep", "k1", "the widget pipeline stalls on shard three")
	write("scratch", "s1", "scratch work nobody should see off this machine")
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func exported(t *testing.T, outDir string) string {
	t.Helper()
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(outDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		all.Write(b)
	}
	return all.String()
}

// The export applies the exclude list, and its comment says why: the export is
// where data leaves, and a privacy control set after the index was built has to
// hold at that boundary. The ignore rule is the same kind of control and was
// not applied, so the one tree deja never recalls from was shipped to another
// machine, where it is served as imported memory (#2654).
func TestExportLeavesTheIgnoredTreeBehind(t *testing.T) {
	dir := exportStore(t)
	out := t.TempDir()
	n, err := Export(dir, out)
	if err != nil {
		t.Fatal(err)
	}
	body := exported(t, out)
	if strings.Contains(body, "nobody should see off this machine") {
		t.Fatalf("the ignored tree left the machine:\n%s", body)
	}
	if !strings.Contains(body, "widget pipeline stalls") {
		t.Fatalf("the session recall may serve did not travel:\n%s", body)
	}
	if n < 1 {
		t.Fatalf("nothing was exported at all")
	}
}

// Held, not settled: the exclude path keeps a record's watermark unmoved so a
// rule lifted later still sends it. The same has to hold here, or a rule set
// once would settle that work for good.
func TestExportCanStillSendWhatTheRuleStopsCovering(t *testing.T) {
	dir := exportStore(t)
	if _, err := Export(dir, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// The reader changes their mind.
	if err := os.WriteFile(os.Getenv("DEJA_POLICY_FILE"), []byte(`{"ignore":["*nothing-here*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if _, err := Export(dir, out); err != nil {
		t.Fatal(err)
	}
	if body := exported(t, out); !strings.Contains(body, "nobody should see off this machine") {
		t.Fatalf("work held back by a rule that no longer covers it never travels:\n%s", body)
	}
}
