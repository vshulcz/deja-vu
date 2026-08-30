package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// halvedStore indexes sessions in two projects, one of which the reader's
// ignore rule keeps out of recall.
func halvedStore(t *testing.T) string {
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
	write := func(project, id, day string) {
		p := filepath.Join(root, "-w-"+project, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"2026-07-` + day + `T10:00:00Z","message":{"role":"user","content":"the widget pipeline"}}` + "\n" +
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"2026-07-` + day + `T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/x/` + id + `.go","old_string":"a","new_string":"b"}}]}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		write("keep", fmt.Sprintf("k%d", i), "01")
	}
	for i := 0; i < 6; i++ {
		write("scratch", fmt.Sprintf("s%d", i), "02")
	}
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Overview reports what the index holds, which is what `deja doctor` asks for.
// OverviewServable reports what recall can serve, which is what the brief and
// the "older than that window" line describe — and counting the first on those
// screens put three times the sessions on the brief that its own rows showed,
// dated to a day none of them happened on (#2650).
func TestOverviewServableLeavesOutTheIgnoredTree(t *testing.T) {
	dir := halvedStore(t)

	whole, err := Overview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if whole.Sessions != 9 {
		t.Fatalf("the whole store holds 9 sessions, Overview says %d", whole.Sessions)
	}

	servable, err := OverviewServable(dir)
	if err != nil {
		t.Fatal(err)
	}
	if servable.Sessions != 3 {
		t.Fatalf("recall can serve 3 sessions, OverviewServable says %d", servable.Sessions)
	}
	// The range too: the ignored six are the only work on the second of July.
	if got := servable.Newest.UTC().Format("2006-01-02"); got != "2026-07-01" {
		t.Fatalf("the servable range ends %s, on a day only ignored sessions were worked on", got)
	}
	if !whole.Newest.After(servable.Newest) {
		t.Fatalf("the two ranges should differ on this store: whole %s, servable %s", whole.Newest, servable.Newest)
	}
}

// The span count is what `restore` would hand back, and restore refuses a tree
// the ignore rule covers.
func TestSpanInventoryCountsOnlyWhatRestoreWouldGiveBack(t *testing.T) {
	dir := halvedStore(t)
	spans, files, err := SpanInventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if spans != 3 {
		t.Fatalf("3 sessions are servable and each replaced one span, got %d", spans)
	}
	if files != 3 {
		t.Fatalf("those three spans are in three files, got %d", files)
	}
}
