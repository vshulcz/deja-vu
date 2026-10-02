package sources

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Kimchi writes each `Agent` sub-agent run as a transcript of its own beside
// its parent, and deja indexed it as a session, where the sub-agent's
// restatement of the task competed with the parent for recall. The run is
// marked by a `kimchi:subagent-session` entry after the header; a fork carries
// `parentSession` too and is a session of its own (#4401).
func TestKimchiSubagentRunsAreSkipped(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_KIMCHI_ROOT", root)
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	dir := filepath.Join(root, "--private-tmp-proj--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	msg := `{"type":"message","id":"m1","timestamp":"2026-10-01T21:57:05.000Z","message":{"role":"user","content":[{"type":"text","text":"summarise retry.cfg in one line"}]}}` + "\n"
	write := func(name, head string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(head+msg), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	parent := write("2026-10-01T21-57-03-925Z_01a0f978-9334.jsonl",
		`{"type":"session","version":3,"id":"01a0f978-9334","timestamp":"2026-10-01T21:57:03.925Z","cwd":"/private/tmp/proj"}`+"\n")
	sub := write("2026-10-01T21-57-04-562Z_01a0f978-95b2.jsonl",
		`{"type":"session","version":3,"id":"01a0f978-95b2","timestamp":"2026-10-01T21:57:04.562Z","cwd":"/private/tmp/proj","parentSession":"`+parent+`"}`+"\n"+
			`{"type":"custom","id":"c1","parentId":null,"timestamp":"2026-10-01T21:57:04.562Z","customType":"kimchi:subagent-session","data":{"type":"General-Purpose"}}`+"\n")
	fork := write("2026-10-01T22-00-00-000Z_01a0f990-0000.jsonl",
		`{"type":"session","version":3,"id":"01a0f990-0000","timestamp":"2026-10-01T22:00:00.000Z","cwd":"/private/tmp/proj","parentSession":"`+parent+`"}`+"\n")

	files := KimchiSessionFiles()
	if !slices.Contains(files, parent) || !slices.Contains(files, fork) {
		t.Errorf("files = %v, want the parent and the fork", files)
	}
	if slices.Contains(files, sub) {
		t.Errorf("the sub-agent run is listed as a session: %v", files)
	}
	if !KimchiSubagentFile(sub) || KimchiSubagentFile(fork) || KimchiSubagentFile(parent) {
		t.Errorf("KimchiSubagentFile: sub %v, fork %v, parent %v", KimchiSubagentFile(sub), KimchiSubagentFile(fork), KimchiSubagentFile(parent))
	}
	if kindFor := KindForPath(sub); kindFor == "kimchi" {
		t.Errorf("the incremental pass still claims the sub-agent run")
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if !slices.Contains(KimchiSessionFiles(), sub) {
		t.Error("DEJA_INCLUDE_SUBAGENTS=1 does not take the sub-agent run")
	}
}
