package sources

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchSpansTakesTheRemovedLines(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Update File: /w/app.go\n" +
		"@@\n" +
		"-func old() error {\n" +
		"-\treturn nil\n" +
		"+func nw() error {\n" +
		"+\treturn errors.New(\"x\")\n" +
		"*** End Patch"
	got := patchSpans(patch)
	if len(got) != 1 {
		t.Fatalf("got %d spans, want one", len(got))
	}
	path, body, _ := strings.Cut(got[0], "\n")
	if path != "/w/app.go" {
		t.Fatalf("path = %q", path)
	}
	if body != "func old() error {\n\treturn nil" {
		t.Fatalf("body = %q — only the removed lines belong in a span", body)
	}
}

func TestPatchSpansHandlesSeveralFilesAndNoRemovals(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Update File: /w/a.go\n@@\n-one\n+two\n" +
		"*** Add File: /w/b.go\n+brand new\n" +
		"*** Update File: /w/c.go\n@@\n-three\n" +
		"*** End Patch"
	got := patchSpans(patch)
	if len(got) != 2 {
		t.Fatalf("got %v — a file with nothing removed has no span to keep", got)
	}
	if !strings.HasPrefix(got[0], "/w/a.go\n") || !strings.HasPrefix(got[1], "/w/c.go\n") {
		t.Fatalf("got %v", got)
	}
	if len(patchSpans("")) != 0 || len(patchSpans("nonsense")) != 0 {
		t.Fatal("junk in, nothing out")
	}
}

func TestWorthIndexingCoversMoreThanOneEcosystem(t *testing.T) {
	for _, cmd := range []string{
		"go test ./internal/index/ -run Posting",
		"git status --short",
		"latexmk -pdf thesis.tex",
		"uv run pytest -q",
		"gradle build",
		"psql -c 'select 1'",
	} {
		if !worthIndexing(cmd) {
			t.Errorf("%q is work worth recording", cmd)
		}
	}
	for _, cmd := range []string{
		"ls -la",
		"cd /tmp",
		"cat file.txt",
		"python3 - <<'PY'\nprint(1)\nPY",
	} {
		if worthIndexing(cmd) {
			t.Errorf("%q is not", cmd)
		}
	}
}

// TestOpencodeParsesCommandsAndPatches drives the row branches added for bash
// and apply_patch through the real SQL path, since a unit test of patchSpans
// alone would leave the query and the row loop unmeasured.
func TestOpencodeParsesCommandsAndPatches(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	patch := `*** Begin Patch\n*** Update File: /w/app.go\n@@\n-func old() error {\n+func nw() error {\n*** End Patch`
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"assistant"}');
insert into part values('p1','m1','{"type":"tool","tool":"bash","state":{"input":{"command":"go test ./pkg/ -run Retry"}},"time":{"start":"2026-01-02T03:00:00Z"}}');
insert into part values('p2','m1','{"type":"tool","tool":"bash","state":{"input":{"command":"ls -la"}},"time":{"start":"2026-01-02T03:00:01Z"}}');
insert into part values('p3','m1','{"type":"tool","tool":"apply_patch","state":{"input":{"patchText":"` + patch + `"}},"time":{"start":"2026-01-02T03:00:02Z"}}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	var cmds, edits []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleEdit:
			edits = append(edits, m.Text)
		}
	}
	if len(cmds) != 1 || cmds[0] != "$ go test ./pkg/ -run Retry" {
		t.Fatalf("commands = %v — `ls` is not work worth recording", cmds)
	}
	if len(edits) != 1 || !strings.HasPrefix(edits[0], "/w/app.go\n") ||
		!strings.Contains(edits[0], "func old() error {") {
		t.Fatalf("edits = %v", edits)
	}
}

func TestOpencodeIndexesCommandOutputAndExitStatus(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"assistant"}');
insert into part values('p1','m1','{"type":"tool","tool":"bash","state":{"input":{"command":"go test ./pkg/"},"output":"--- FAIL: TestRetry\nFAIL\tpkg 0.1s","metadata":{"exit":1}},"time":{"start":"2026-01-02T03:00:00Z"}}');
insert into part values('p2','m1','{"type":"tool","tool":"bash","state":{"input":{"command":"go build ./..."},"output":"","metadata":{"exit":0}},"time":{"start":"2026-01-02T03:00:01Z"}}');
insert into part values('p3','m1','{"type":"tool","tool":"read","state":{"input":{"filePath":"/w/big.go"},"output":"package main\n// thousands of lines of file content"},"time":{"start":"2026-01-02T03:00:02Z"}}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	var cmds, outs []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleToolOutput:
			outs = append(outs, m.Text)
		}
	}
	// A failure carries its status; a success does not — saying "exit 0" on
	// every passing run is noise, and zero is the default reading.
	if len(cmds) != 2 {
		t.Fatalf("commands = %v", cmds)
	}
	if !strings.Contains(cmds[0], "→ exit 1") {
		t.Errorf("a non-zero exit belongs on the command: %q", cmds[0])
	}
	if strings.Contains(cmds[1], "exit") {
		t.Errorf("a successful run needs no annotation: %q", cmds[1])
	}
	// The output of a command is indexed; the body of a file read is not —
	// file contents are 119 MB against 49 MB of command output on a real store.
	if len(outs) != 1 || !strings.Contains(outs[0], "--- FAIL") {
		t.Fatalf("tool output = %v", outs)
	}
	for _, o := range outs {
		if strings.Contains(o, "thousands of lines") {
			t.Error("read bodies must not be indexed as tool output")
		}
	}
}

func TestExitCodeReadsEveryShapeSQLiteEmits(t *testing.T) {
	for in, want := range map[any]int{
		float64(1): 1, float64(0): 0, "127": 127, "": 0, nil: 0, "junk": 0,
	} {
		if got := exitCode(in); got != want {
			t.Errorf("exitCode(%#v) = %d, want %d", in, got, want)
		}
	}
}
