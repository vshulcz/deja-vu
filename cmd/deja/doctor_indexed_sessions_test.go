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

// doctor counted files and stopped there, so a store whose transcripts share
// ids read exactly like one where every file is its own session: two files,
// one session, no difference on screen (#861).
func TestDoctorRowCountsIndexedSessions(t *testing.T) {
	tmp := hermeticEnv(t)
	chats := filepath.Join(tmp, "qwen", "projects", "proj", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(sid, text string) string {
		return `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","parts":[{"text":"` + text + `"}]}}` + "\n"
	}
	for _, f := range []string{"a.jsonl", "b.jsonl"} {
		if err := os.WriteFile(filepath.Join(chats, f), []byte(line("same-id", "pool exhausted")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))

	dir := filepath.Join(tmp, "idx")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	doctorHarnesses(&buf, dir)
	row := harnessRow(t, buf.String(), "qwen")
	if !strings.Contains(row, "2 files") || !strings.Contains(row, "1 indexed session") {
		t.Errorf("row does not show both counts: %q", row)
	}

	// A harness whose store is not on this machine but whose sessions are in
	// the index — everything arrived by `sync import` — is named too, and said
	// to have come from elsewhere (#892).
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "gone"))
	buf.Reset()
	doctorHarnesses(&buf, dir)
	// The store is not where deja looks any more, but the session in the index
	// is still this machine's own: the count is named without claiming it came
	// from another machine (#892, narrowed by #894).
	if row := harnessRow(t, buf.String(), "qwen"); !strings.Contains(row, "1 indexed session") {
		t.Errorf("a store that is only in the index says nothing: %q", row)
	} else if strings.Contains(row, "from elsewhere") {
		t.Errorf("a local session was called imported: %q", row)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))

	// A store that has both: the two are counted apart, because "2 files, 4
	// indexed sessions" reads as a miscount (#894).
	exp := filepath.Join(tmp, "export")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch strings.Builder
	for _, id := range []string{"r1", "r2"} {
		b, err := json.Marshal(index.SyncRecord{Harness: "qwen", SessionID: id, Project: "remote", Role: "user", Text: "a remote session about the seal"})
		if err != nil {
			t.Fatal(err)
		}
		batch.WriteString(string(b) + "\n")
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), []byte(batch.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	doctorHarnesses(&buf, dir)
	if row := harnessRow(t, buf.String(), "qwen"); !strings.Contains(row, "1 indexed session, 2 more from elsewhere") {
		t.Errorf("mixed store does not separate the two: %q", row)
	}

	// A harness with nothing in the index says nothing rather than "0": the
	// row is about what deja found, and a zero there reads as a failure.
	buf.Reset()
	doctorHarnesses(&buf, filepath.Join(tmp, "empty"))
	if row := harnessRow(t, buf.String(), "qwen"); strings.Contains(row, "indexed session") {
		t.Errorf("unbuilt index still claims sessions: %q", row)
	}
}

func harnessRow(t *testing.T, out, name string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), name+" ") {
			return line
		}
	}
	t.Fatalf("no %s row in:\n%s", name, out)
	return ""
}
