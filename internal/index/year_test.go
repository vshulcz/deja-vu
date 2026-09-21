package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ScanYearWork is the one pass behind `deja stats --year`. What it has to get
// right is the arithmetic the screen prints: records inside the window only,
// files and commands counted as distinct things rather than as records, and
// nothing from a tree the ignore rule covers — a year report that counts the
// scratch directory is a figure nobody can show anyone.
func yearStore(t *testing.T) (dir string, from time.Time) {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	policyFile := filepath.Join(home, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	recent := now.AddDate(0, -1, 0).Format("2006-01-02T15:04:05Z")
	old := now.AddDate(-2, 0, 0).Format("2006-01-02T15:04:05Z")

	write := func(project, id, stamp string, tools string) {
		p := filepath.Join(root, "-w-"+project, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"` + stamp + `","message":{"role":"user","content":"the widget pipeline keeps dropping the last attempt"}}` + "\n" +
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"` + stamp + `","message":{"role":"assistant","content":[` + tools + `]}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Two sessions in the window edit the same file and run the same command,
	// so records and distinct things differ on purpose.
	edit := func(path string) string {
		return `{"type":"tool_use","name":"Edit","input":{"file_path":"` + path + `","old_string":"for i := 0; i < n; i++ {","new_string":"for i := 0; i <= n; i++ {"}}`
	}
	bash := func(cmd string) string {
		return `{"type":"tool_use","name":"Bash","input":{"command":"` + cmd + `"}}`
	}
	write("keep", "k1", recent, edit("/w/keep/loop.go")+","+bash("go test ./..."))
	write("keep", "k2", recent, edit("/w/keep/loop.go")+","+bash("go test ./..."))
	write("keep", "k3", recent, edit("/w/keep/backoff.go")+","+bash("go build ./cmd/x"))
	// Older than the window, and a tree the ignore rule covers: neither is in
	// any figure.
	write("keep", "k4", old, edit("/w/keep/ancient.go")+","+bash("make release"))
	for i := 0; i < 2; i++ {
		write("scratch", fmt.Sprintf("s%d", i), recent,
			edit("/w/scratch/tmp.go")+","+bash("rm -rf /w/scratch/build"))
	}

	dir = filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir, time.Now().AddDate(-1, 0, 0)
}

func TestScanYearWorkCountsDistinctThingsInsideTheWindow(t *testing.T) {
	dir, from := yearStore(t)
	work, err := ScanYearWork(dir, from)
	if err != nil {
		t.Fatal(err)
	}
	// Three sessions in the window, each naming one file and running one
	// command: three records of each kind, two distinct files, two distinct
	// commands.
	if got := work.Records[roleFiles]; got != 3 {
		t.Errorf("files records = %d, want 3 (one per session in the window)", got)
	}
	if work.Files != 2 {
		t.Errorf("distinct files = %d, want 2 — loop.go twice is one file", work.Files)
	}
	if got := work.Records[roleCommand]; got != 3 {
		t.Errorf("command records = %d, want 3", got)
	}
	if work.Commands != 2 {
		t.Errorf("distinct commands = %d, want 2 — the same `go test` twice is one command", work.Commands)
	}
	if work.Spans != 3 || work.SpanFiles != 2 {
		t.Errorf("spans = %d in %d files, want 3 in 2", work.Spans, work.SpanFiles)
	}
}

func TestScanYearWorkLeavesOutTheIgnoredTreeAndTheOlderYears(t *testing.T) {
	dir, from := yearStore(t)
	work, err := ScanYearWork(dir, from)
	if err != nil {
		t.Fatal(err)
	}
	// The scratch sessions carry a file and a command of their own, and the
	// session from two years ago carries a third: counting either would show
	// 5 or 6 files where the window holds 2.
	if work.Files != 2 {
		t.Fatalf("distinct files = %d — the ignored tree or the older year is being counted", work.Files)
	}
	// And the whole store really does hold them, or this test passes for the
	// wrong reason.
	whole, err := ScanYearWork(dir, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if whole.Files <= work.Files {
		t.Fatalf("the store holds %d files over all time and %d in the window — the fixture proves nothing",
			whole.Files, work.Files)
	}
	if whole.Files != 3 {
		t.Errorf("over all time the readable files = %d, want 3 — the ignored tree is still out", whole.Files)
	}
}
