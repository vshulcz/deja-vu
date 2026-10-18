package sources

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// opencode's apply_patch resolves a relative header against the session's
// directory; gpt models write "*** Add File: notes.txt" often enough that a
// sub-agent's whole change was filed under a bare name blame never matched.
func TestOpencodeRelativePatchPathsResolveAgainstTheSessionDirectory(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	patch := `*** Begin Patch\n*** Add File: notes.txt\n+the sub agent wrote this particular line of text.\n` +
		`*** Update File: ./sub/app.go\n@@\n-func oldName() error { return nil }\n+func newName() error { return errors.New(\"x\") }\n` +
		`*** Update File: /elsewhere/abs.go\n@@\n-an absolute path stays exactly as it was\n+and so does its replacement line here\n*** End Patch`
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w/app2','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"assistant"}');
insert into part values('p1','m1','{"type":"tool","tool":"apply_patch","state":{"status":"completed","input":{"patchText":"` + patch + `"}},"time":{"start":"2026-01-02T03:00:02Z"}}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	var wrote, edits []string
	for _, m := range ss[0].Messages {
		head, _, _ := strings.Cut(m.Text, "\n")
		switch m.Role {
		case RoleWrote:
			wrote = append(wrote, head)
		case RoleEdit:
			edits = append(edits, head)
		}
	}
	if want := []string{"/w/app2/notes.txt", "/w/app2/sub/app.go", "/elsewhere/abs.go"}; strings.Join(wrote, ",") != strings.Join(want, ",") {
		t.Errorf("wrote records name %v, want %v", wrote, want)
	}
	if want := []string{"/w/app2/sub/app.go", "/elsewhere/abs.go"}; strings.Join(edits, ",") != strings.Join(want, ",") {
		t.Errorf("edit spans name %v, want %v", edits, want)
	}
}
