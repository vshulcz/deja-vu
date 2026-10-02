package index

import (
	"os"
	"path/filepath"
	"testing"
)

func cherryChunk(text string) string {
	return `{"type":"assistant","uuid":"u-` + text[len(text)-3:] + `","requestId":"req-9","sessionId":"cs-9","timestamp":"2026-09-30T11:00:01.000Z","message":{"id":"msg-9","role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
}

// Cherry Studio appends a snapshot line per stream chunk. An index that ran
// between two chunks stored the half reply, and the next pass appended the
// full one beside it: show kept "the loop has no" next to the finished
// answer, the duplicate #3644 removed for a full read (#4346).
func TestCherryStudioIndexMidStreamKeepsOneReply(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "CherryStudio", "Data", "Agents", ".claude", "projects")
	proj := filepath.Join(root, "-tmp-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj, "cs-9.jsonl")
	head := `{"type":"user","sessionId":"cs-9","timestamp":"2026-09-30T11:00:00.000Z","cwd":"/tmp/proj","message":{"role":"user","content":"why does the retry loop spin"}}` + "\n" +
		cherryChunk("the loop") + cherryChunk("the loop has no")
	if err := os.WriteFile(path, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CHERRYSTUDIO_ROOTS", root)
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "cherrystudio", false, nil); err != nil {
		t.Fatal(err)
	}

	full := "the loop has no attempt cap, so it spins until the server answers"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(cherryChunk("the loop has no attempt cap") + cherryChunk(full))
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "cherrystudio", false, nil); err != nil {
		t.Fatal(err)
	}

	recs, err := ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	var replies []string
	for _, r := range recs {
		if r.Record.SourcePath == path && r.Record.Role == "assistant" {
			replies = append(replies, r.Record.Text)
		}
	}
	if len(replies) != 1 || replies[0] != full {
		t.Errorf("assistant records = %q, want only the finished reply", replies)
	}
}
