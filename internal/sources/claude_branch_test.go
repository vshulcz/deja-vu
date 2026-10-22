package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// A Claude Code /branch copies its source's turns into a new transcript and
// stamps each copied record with forkedFrom.sessionId (2.1.302). That is the
// branch's source, said by the harness; read as an unrelated session, show
// named no source and blame credited the branch with the source's edits.
func TestClaudeBranchNamesItsSource(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	src := "6f40857d-40c1-4644-a7c1-c7d6e086cb03"
	branch := "84f06bb6-48da-4a49-a9ab-109c2fa67f2b"
	dir := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	copied := func(role, text, uuid string) string {
		return `{"type":"` + role + `","sessionId":"` + branch + `","uuid":"` + uuid + `","isSidechain":false,"forkedFrom":{"sessionId":"` + src + `","messageUuid":"` + uuid + `"},"timestamp":"2026-10-18T04:38:45.469Z","cwd":"/work/app","message":{"role":"` + role + `","content":"` + text + `"}}` + "\n"
	}
	own := `{"type":"user","sessionId":"` + branch + `","uuid":"u3","isSidechain":false,"timestamp":"2026-10-18T04:42:00.000Z","cwd":"/work/app","message":{"role":"user","content":"now in the branch"}}` + "\n"
	path := filepath.Join(dir, branch+".jsonl")
	if err := os.WriteFile(path, []byte(copied("user", "edit main.go", "u1")+copied("assistant", "done", "u2")+own), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != branch {
		t.Fatalf("sessions = %#v", ss)
	}
	if ss[0].Kind != "fork" || ss[0].Parent != src {
		t.Errorf("kind = %q parent = %q, want fork of %s", ss[0].Kind, ss[0].Parent, src)
	}
	ref, err := parseClaudeGenericFromOffset(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ref) != 1 || ref[0].Kind != ss[0].Kind || ref[0].Parent != ss[0].Parent {
		t.Errorf("reference parser disagrees: %#v", ref)
	}

	// Control: the source itself carries no marker and stays a plain session.
	plain := filepath.Join(dir, src+".jsonl")
	if err = os.WriteFile(plain, []byte(`{"type":"user","sessionId":"`+src+`","uuid":"u1","timestamp":"2026-10-18T04:38:45.469Z","cwd":"/work/app","message":{"role":"user","content":"edit main.go"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, err := ParseClaudeFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Kind != "" || ps[0].Parent != "" {
		t.Errorf("source read as %#v", ps)
	}
}
