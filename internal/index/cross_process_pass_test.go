package index

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// What makes the pass's package state safe is the directory lock, and every
// test of it has run goroutines in one process. Two `deja index` processes at
// once is the shape a cron and a session-start hook make, and nothing measured
// it: a flock that did not hold across processes would leave the store to
// whichever pass wrote last.
//
// The child half of this test is `TestCrossProcessIndexChild`, which the parent
// runs by re-executing the test binary.
func TestTwoIndexProcessesLeaveOneGoodStore(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(claude, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	const sessions = 120
	for i := 0; i < sessions; i++ {
		line := fmt.Sprintf(`{"type":"user","sessionId":"s%03d","timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":"turn %d: the pool timed out during the migration"}}`, i, stamp, i)
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("s%03d.jsonl", i)), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")

	// The child inherits this environment and puts it back after the package's
	// TestMain has pointed every store at a temp root of its own.
	env := append(os.Environ(),
		"DEJA_PASS_INDEX="+dir,
		"DEJA_PASS_CLAUDE="+claude,
		"DEJA_PASS_CODEX="+filepath.Join(tmp, "codex"),
		"DEJA_PASS_OPENCODE="+filepath.Join(tmp, "none.db"),
		"DEJA_PASS_NOTES="+filepath.Join(tmp, "notes.jsonl"),
		// The child's own TestMain re-points these at a temp root of its own,
		// so they travel under names it will not overwrite and are put back on
		// the other side. Without them the two children would keep their
		// tombstones and exclude lists somewhere the parent never wrote.
		"DEJA_PASS_HOME="+tmp,
		"DEJA_PASS_XDG_CONFIG="+filepath.Join(tmp, "config"),
		"DEJA_PASS_APPDATA="+filepath.Join(tmp, "AppData", "Roaming"),
	)
	start := make(chan struct{})
	var wg sync.WaitGroup
	type run struct {
		out        string
		err        error
		from, upto time.Time
	}
	runs := make([]run, 2)
	for i := range runs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// A copy, so the two children never share a backing array, and a
			// deadline, so a pass that never gets the lock fails this test
			// rather than hanging the package.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCrossProcessIndexChild")
			cmd.Env = append(append([]string{}, env...), fmt.Sprintf("DEJA_PASS_FULL=%d", boolToInt(i == 0)))
			<-start
			out, err := cmd.CombinedOutput()
			runs[i].out, runs[i].err = string(out), err
			runs[i].from, runs[i].upto = childWindow(string(out))
		}(i)
	}
	close(start)
	wg.Wait()

	for i, r := range runs {
		if r.err != nil {
			t.Fatalf("process %d failed: %v\n%s", i, r.err, r.out)
		}
		if !strings.Contains(r.out, "pass-ok") {
			t.Fatalf("process %d did not report a finished pass:\n%s", i, r.out)
		}
	}
	// Whether the lock was actually contended, from the lock itself rather
	// than from wall-clock windows: a child that waited says so. This is
	// reported and not asserted, because a machine that runs the first pass to
	// completion before the second process has finished starting is slow or
	// lucky, not broken — the assertions that matter are below.
	waited := 0
	for _, r := range runs {
		if strings.Contains(r.out, "waited-for-lock") {
			waited++
		}
	}
	if waited == 0 {
		t.Logf("neither process waited for the lock: the passes did not overlap this run (%s..%s and %s..%s)",
			runs[0].from, runs[0].upto, runs[1].from, runs[1].upto)
	}

	// One good store: every session once, and a manifest that reads back.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatalf("the store two processes left cannot be read: %v", err)
	}
	if len(m.Sessions) != sessions {
		t.Errorf("the store holds %d sessions, want %d", len(m.Sessions), sessions)
	}
	counted := 0
	for _, meta := range m.Sessions {
		counted += meta.Counted
	}
	if counted != sessions {
		t.Errorf("the sessions count %d messages between them, want %d", counted, sessions)
	}
	if !recordsReadable(dir, m) {
		t.Error("records.bin does not match the manifest the two processes left")
	}
}

// childWindow reads the stamps the child printed around its pass.
func childWindow(out string) (from, upto time.Time) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "pass-ok ") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 3 {
			continue
		}
		a, err1 := strconv.ParseInt(parts[1], 10, 64)
		b, err2 := strconv.ParseInt(parts[2], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		return time.Unix(0, a), time.Unix(0, b)
	}
	return time.Time{}, time.Time{}
}

// TestCrossProcessIndexChild is one index pass, run as its own process by the
// test above. It is a no-op anywhere else.
func TestCrossProcessIndexChild(t *testing.T) {
	dir := os.Getenv("DEJA_PASS_INDEX")
	if dir == "" {
		t.Skip("the parent runs this one")
	}
	// After TestMain, which points every store at a temp root of its own.
	for k, v := range map[string]string{
		"DEJA_CLAUDE_ROOT": os.Getenv("DEJA_PASS_CLAUDE"),
		"DEJA_CODEX_ROOT":  os.Getenv("DEJA_PASS_CODEX"),
		"DEJA_OPENCODE_DB": os.Getenv("DEJA_PASS_OPENCODE"),
		"DEJA_NOTES_FILE":  os.Getenv("DEJA_PASS_NOTES"),
		"HOME":             os.Getenv("DEJA_PASS_HOME"),
		"USERPROFILE":      os.Getenv("DEJA_PASS_HOME"),
		"XDG_CONFIG_HOME":  os.Getenv("DEJA_PASS_XDG_CONFIG"),
		"APPDATA":          os.Getenv("DEJA_PASS_APPDATA"),
	} {
		if err := os.Setenv(k, v); err != nil {
			t.Fatal(err)
		}
	}
	// The lock's own signal, which is what says the two passes met.
	LockWaitNotice = func() { fmt.Fprintln(os.Stdout, "waited-for-lock") }
	from := time.Now()
	if err := Ensure(dir, "", os.Getenv("DEJA_PASS_FULL") == "1", nil); err != nil {
		t.Fatalf("pass failed: %v", err)
	}
	fmt.Fprintf(os.Stdout, "pass-ok %d %d\n", from.UnixNano(), time.Now().UnixNano())
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
