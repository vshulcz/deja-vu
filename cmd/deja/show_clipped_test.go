package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A pasted log is one message, and the index stores the first 64 KB of it.
// `deja show` printed that much and said nothing, so a reader looking for the
// line that explains a failure searched a log they believed was whole — the
// "showed part, read as whole" family, on the door where the whole point is to
// read a session as it was (#2467).
func TestShowSaysWhenAMessageWasClipped(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	// Longer than what the index keeps for one message.
	huge := strings.Repeat("2026-08-28T10:00:00Z INFO orders.pipeline step finished with pool=8 retries=0\n", 1800)
	line := `{"type":"user","sessionId":"huge","timestamp":"` + at + `","cwd":"/work/app",` +
		`"message":{"role":"user","content":` + jsonString(huge) + `}}`
	if err := os.WriteFile(filepath.Join(store, "huge.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, errOut := captureBoth(t, "show", "huge")
	whole := out + errOut
	if len(out) >= len(huge) {
		t.Fatalf("the fixture was not clipped at all: %d bytes shown", len(out))
	}
	if !strings.Contains(whole, "stored short of what the transcript holds") {
		t.Errorf("show printed part of a message and said nothing:\n%.400s", tail(whole))
	}
}

// tail is the end of the output, where a note about the message would sit.
func tail(s string) string {
	if len(s) <= 400 {
		return s
	}
	return s[len(s)-400:]
}

// Zed keeps every thread in one threads.db, and the clip count is recorded per
// file. Looked up by path, one pasted log anywhere in the store made every
// thread in it carry the note — a two-line session told the reader it was
// incomplete (#4340).
func TestShowClippedNoteStaysOnTheSessionThatHoldsIt(t *testing.T) {
	tmp := hermeticEnv(t)
	if !sources.SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	huge := strings.Repeat("2026-08-28T10:00:00Z INFO orders.pipeline step finished with pool=8 retries=0\n", 1800)
	row := func(id, user, reply string) string {
		doc := `{"version":"0.2.0","summary":"t","updated_at":"2026-09-30T10:00:02Z","messages":[` +
			`{"id":1,"role":"user","segments":[{"type":"text","text":` + jsonString(user) + `}]},` +
			`{"id":2,"role":"assistant","segments":[{"type":"text","text":` + jsonString(reply) + `}]}]}`
		return "insert into threads (id, summary, updated_at, data_type, data, folder_paths) values ('" + id +
			"', 't', '2026-09-30T10:00:02+00:00', 'json', '" + strings.ReplaceAll(doc, "'", "''") + "', '/work/proj');\n"
	}
	sql := "create table threads (id text primary key, summary text not null, updated_at text not null, " +
		"data_type text not null, data blob not null, parent_id text, folder_paths text, folder_paths_order text, created_at text);\n" +
		row("33333333-0000-4000-8000-000000000003", huge, "that log ends in a timeout") +
		row("44444444-0000-4000-8000-000000000004", "fix the retry loop", "the loop now stops after three tries") +
		row("55555555-0000-4000-8000-000000000005", strings.Repeat("a", 64*1024), "a paste exactly at the cap, kept whole")
	db := filepath.Join(tmp, "threads.db")
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sqlite fixture: %v: %s", err, out)
	}
	t.Setenv("DEJA_ZED_DB", db)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	_, errOut := captureBoth(t, "show", "3333")
	if !strings.Contains(errOut, "one message was stored short") {
		t.Errorf("the thread with the pasted log lost its note:\n%s", errOut)
	}
	_, errOut = captureBoth(t, "show", "4444")
	if strings.Contains(errOut, "stored short") {
		t.Errorf("a whole two-message thread was told it was stored short:\n%s", errOut)
	}
	// A message exactly at the cap was stored whole; its length alone cannot
	// tell it from one the cap cut.
	_, errOut = captureBoth(t, "show", "5555")
	if strings.Contains(errOut, "stored short") {
		t.Errorf("a thread whose message fits the cap exactly was told it was stored short:\n%s", errOut)
	}
}
