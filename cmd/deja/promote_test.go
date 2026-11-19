package main

import (
	"bytes"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A note is a distillation, so it is as old as the session it came from. When
// promote stamped the note with the moment it ran, promoting a January session
// minted the freshest thing in the store: the note ranked first and `deja ctx`
// handed the agent the stale value while the August session that corrected it
// sat below, unread.
func TestPromotedNoteRanksByEvidenceAgeNotFilingTime(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-app", "old.jsonl"), "oldsess1", []string{
		`{"type":"user","sessionId":"oldsess1","timestamp":"2026-01-10T10:00:00Z","message":{"role":"user","content":"what is the payment gateway retry timeout"}}`,
		`{"type":"assistant","sessionId":"oldsess1","timestamp":"2026-01-10T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"the payment gateway retry timeout is 30 seconds"}]}}`,
	})
	fresh := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(root, "-tmp-app", "new.jsonl"), "freshsess", []string{
		`{"type":"user","sessionId":"freshsess","timestamp":"` + fresh + `","message":{"role":"user","content":"we changed the payment gateway retry timeout, what is it now"}}`,
		`{"type":"assistant","sessionId":"freshsess","timestamp":"` + fresh + `","message":{"role":"assistant","content":[{"type":"text","text":"the payment gateway retry timeout is now 5 seconds"}]}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := runPromote(dir, []string{"oldsess1", "--note", "payment gateway retry timeout is 30 seconds"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	hits := searchHits(t, dir, "payment gateway retry timeout")
	if len(hits) < 3 {
		t.Fatalf("want note + both transcripts, got %d hits", len(hits))
	}
	if got := hits[0].Session.ID; got != "freshsess" {
		t.Fatalf("the newer session must outrank a note distilled from a January one, got %s:%s at rank 1",
			hits[0].Session.Harness, got)
	}
	// And the note still sits in front of the transcript it came from: the
	// evidence date must not cost it that place.
	note, src := -1, -1
	for i, h := range hits {
		switch h.Session.ID {
		case "deja-note-claude-oldsess1":
			note = i
		case "oldsess1":
			src = i
		}
	}
	if note < 0 || src < 0 || note > src {
		t.Fatalf("note must rank above its own source transcript: note=%d source=%d", note, src)
	}
}

// share refuses a session whose project the exclude list covers, because the
// pattern only runs at ingest and the index may still hold it. promote wrote
// the note anyway and told the reader it now outranks the transcript in recall
// — while every reading surface filters that note out again (#2278).
func TestPromoteRefusesAnExcludedProject(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-secretproj")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"sec1","timestamp":%q,"cwd":"/work/secretproj",`+
		`"message":{"role":"user","content":"the payroll export for acme-prod drops rows"}}`, at)
	if err := os.WriteFile(filepath.Join(claude, "s.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The premise: without a pattern the promotion works.
	if _, err := captureRun(t, "promote", "sec1", "--state", "accepted"); err != nil {
		t.Fatalf("promote without an exclude pattern: %v", err)
	}

	t.Setenv("DEJA_EXCLUDE_PROJECTS", "secretproj")
	before, _ := os.ReadFile(notes)
	out, err := captureRun(t, "promote", "sec1", "--state", "rejected")
	if err == nil {
		t.Errorf("promote of an excluded project succeeded, printing %q", out)
	} else if !strings.Contains(err.Error(), "exclude") {
		t.Errorf("promote said %v — it should name the exclude list, as share does", err)
	}
	after, _ := os.ReadFile(notes)
	if len(after) != len(before) {
		t.Error("the refusal still wrote a note")
	}

	// Correcting the note written before the pattern existed is still allowed:
	// it copies nothing new out of the excluded project, and taking a mark back
	// on work you have since excluded is the cleanup someone would want.
	if _, err := captureRun(t, "promote", "deja-note-claude-sec1", "--state", "rejected"); err != nil {
		t.Errorf("correcting an existing note: %v", err)
	}
}

// The heading is separated from what came before by the leading newline of the
// section, which assumes the file ends with one. A hand-written file whose last
// line has no newline — a list, most often — got the heading glued to it (#871).
func TestExportAppendsAfterAFileWithNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	glued := filepath.Join(dir, "nonl.md")
	if err := os.WriteFile(glued, []byte("# Notes\n\n- a line without a newline"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := exportPromoted(glued, "pool exhausted", "raised it", "claude:s1", "accepted", time.Time{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(glued)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "a line without a newline\n\n## pool exhausted") {
		t.Errorf("heading is not separated from the file's last line:\n%q", string(got))
	}

	// A file that does end with a newline keeps exactly one blank line.
	normal := filepath.Join(dir, "norm.md")
	if err = os.WriteFile(normal, []byte("# Notes\n\ntext\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = exportPromoted(normal, "pool exhausted", "raised it", "claude:s1", "accepted", time.Time{}); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(normal)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "text\n\n## pool exhausted") || strings.Contains(string(got), "text\n\n\n") {
		t.Errorf("normal append changed shape:\n%q", string(got))
	}
}

// A failed export said "open …: no such file or directory" and nothing else,
// so a reader whose note had already been written was told their decision was
// lost (#871).
func TestExportFailureSaysTheNoteIsKept(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission behaviour differs here")
	}
	dir := t.TempDir()
	for _, tc := range []struct{ path, reason string }{
		{filepath.Join(dir, "nope", "out.md"), "its directory does not exist"},
		{dir, "that path is a directory"},
	} {
		_, err := exportPromoted(tc.path, "t", "b", "claude:s1", "accepted", time.Time{})
		if err == nil {
			t.Fatalf("%s: export succeeded", tc.path)
		}
		if got := exportFailureReason(err); got != tc.reason {
			t.Errorf("%s: reason = %q, want %q", tc.path, got, tc.reason)
		}
	}
	ro := filepath.Join(dir, "ro.md")
	if err := os.WriteFile(ro, []byte("x\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	_, err := exportPromoted(ro, "t", "b", "claude:s1", "accepted", time.Time{})
	if err == nil {
		t.Fatal("export into a read-only file succeeded")
	}
	if got := exportFailureReason(err); got != "permission denied" {
		t.Errorf("reason = %q", got)
	}
}

// Taking a decision back is the same moment forget is: the machines that
// already have this session keep the note as it was. forget has said so since
// #788; promote said nothing (#898).
func TestPromoteSaysWhenTheSessionAlreadyLeft(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the winch brake pads squeal"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"w1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "w1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")

	// Nothing has left yet: a retraction says nothing about elsewhere.
	out, err := captureRun(t, "promote", "w1", "--state", "rejected", "--note", "did not hold")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "already sent") {
		t.Errorf("a session that never left was called sent:\n%s", out)
	}

	if _, err = index.ExportFull(dir, filepath.Join(tmp, "export")); err != nil {
		t.Fatal(err)
	}

	out, err = captureRun(t, "promote", "w1", "--state", "superseded", "--note", "replaced")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already sent") || !strings.Contains(out, "still reads as it did") {
		t.Errorf("a retraction after an export said nothing about the copies:\n%s", out)
	}

	// Accepting is not a retraction: no line.
	out, err = captureRun(t, "promote", "w1", "--state", "accepted", "--note", "back on")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "already sent") {
		t.Errorf("accepting was treated as a retraction:\n%s", out)
	}
}

// The note outlives the session it came from — that is what forget promises
// when it keeps it. But the id promote hands out for corrections then resolves
// to the note, both routes refused, and the advice named a session that is gone
// for good: the note froze at `accepted` and recall kept serving a withdrawn
// decision (#979).
func TestCorrectionReachesANoteWhoseSourceIsForgotten(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"should we raise the pool cap"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"srcx","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "srcx", "--state", "accepted", "--note", "pool cap goes to 200"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "forget", "--session", "srcx"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "promote", "srcx", "--state", "rejected", "--note", "rolled back, the cap stays at 20")
	if err != nil {
		t.Fatalf("the correction was refused: %v", err)
	}
	if !strings.Contains(out, "as rejected") {
		t.Errorf("the correction did not land:\n%s", out)
	}
	// It belongs to the note the decision lives in, not to a second one, and
	// it leads — the withdrawal is the answer that holds.
	shown, err := captureRun(t, "show", "deja-note-claude-srcx")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "rolled back") || !strings.Contains(shown, "pool cap goes to 200") {
		t.Errorf("the note lost one of the two marks:\n%s", shown)
	}
	if strings.Index(shown, "rolled back") > strings.Index(shown, "pool cap goes to 200") {
		t.Errorf("the correction sits behind the mark it takes back:\n%s", shown)
	}
	// The title of a note whose borrowed one forget cleared must not be the
	// display fallback echoed back at the reader.
	if strings.Contains(out, "promoted from claude:srcx") {
		t.Errorf("the correction took the note's display line as its title:\n%s", out)
	}

	// The backside: a note someone wrote with `remember` is not a promotion,
	// and turning it into one would invent a source session.
	if _, err = captureRun(t, "remember", "plain note about the cert"); err != nil {
		t.Fatal(err)
	}
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	day := ""
	for _, m := range metas {
		if strings.HasPrefix(m.ID, "deja-20") {
			day = m.ID
		}
	}
	if day == "" {
		t.Fatal("no day note to try")
	}
	if _, err := captureRun(t, "promote", day, "--state", "rejected"); err == nil {
		t.Errorf("a note written by hand was promoted as if it had a source session")
	} else if !strings.Contains(err.Error(), "not a session") {
		t.Errorf("the refusal does not say what the id is: %v", err)
	}
}

// #979 made the correction reach the note; without --note it then distilled
// the note's own rows, so `[accepted] asked: … · outcome: …` came back as the
// text of the correction, one wrapper deeper each time (#1033).
func TestCorrectionWithoutANoteCarriesTheNoteText(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"how should we shard the write queue"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"shard1","cwd":"/proj"}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"decided to shard by tenant id"}]},"timestamp":"2026-07-11T10:05:00Z","sessionId":"shard1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "shard1"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "forget", "--session", "claude:shard1"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "promote", "deja-note-claude-shard1", "--state", "superseded")
	if err != nil {
		t.Fatalf("the correction was refused: %v", err)
	}
	if strings.Contains(out, "[accepted]") {
		t.Errorf("the correction wrapped the note's own rendered line:\n%s", out)
	}
	if strings.Count(out, "asked:") > 1 {
		t.Errorf("the correction repeats the note inside itself:\n%s", out)
	}
	if !strings.Contains(out, "shard by tenant") {
		t.Errorf("the correction lost the decision it carries:\n%s", out)
	}
}

// promote prints how to correct the mark it just made. It echoed the selector
// the reader typed, so a prefix that stood for several sessions sent the
// correction to whichever is newest — a different session from the one just
// marked (#884).
func TestPromoteHintNamesTheSessionItMarked(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, when string }{
		{"deja-2026-08-01-hyper-service", "2026-08-01T10:00:00Z"},
		{"deja-2026-08-01-super-service", "2026-07-30T10:00:00Z"},
	} {
		line := `{"type":"user","message":{"role":"user","content":"pool exhausted in ` + tc.id + `"},"timestamp":"` + tc.when + `","sessionId":"` + tc.id + `","cwd":"/proj"}`
		if err := os.WriteFile(filepath.Join(store, tc.id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "promote", "deja-2026…er-service", "--state", "rejected", "--note", "did not hold")
	if err != nil {
		t.Fatal(err)
	}
	marked := "deja-2026-08-01-hyper-service"
	if !strings.Contains(out, "promoted claude:"+marked) {
		t.Fatalf("promoted a different session:\n%s", out)
	}
	if !strings.Contains(out, "deja promote "+marked+" --state") {
		t.Errorf("the correction hint does not name the session that was marked:\n%s", out)
	}
	if strings.Contains(out, "deja promote deja-2026…er-service --state") {
		t.Errorf("the hint hands back the ambiguous selector:\n%s", out)
	}
}

// promote says the note outranks the transcript in recall. It has to be in the
// index for that to be true, and it was not: the hook never builds, so a
// decision recorded here was invisible to the next session until some other
// command happened to run (#910).
func TestPromoteLeavesTheNoteInTheIndex(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the winch brake pads squeal"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"p1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "p1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if n := index.HarnessSessionCounts(dir)["deja"]; n != 0 {
		t.Fatalf("the store already had %d note sessions", n)
	}

	if _, err := captureRun(t, "promote", "p1", "--note", "a decision I just made"); err != nil {
		t.Fatal(err)
	}
	if n := index.HarnessSessionCounts(dir)["deja"]; n != 1 {
		t.Errorf("the note is not in the index: %d note sessions", n)
	}
	// And it can be recalled without anything else running first.
	ss, err := indexSearch(dir, query.Options{Query: "a decision I just made"})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range ss {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "a decision I just made") {
				found = true
			}
		}
	}
	if !found {
		t.Error("the note is in the manifest but not searchable")
	}
}

// promote is where a decision the user wants to keep goes; on a locked notes
// file it named a syscall and nothing to do about it, while index and forget
// both say what to change (#806).
func TestPromoteExplainsALockedNotesFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	hermeticEnv(t)
	notesDir := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(notesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(notesDir, "notes.jsonl"))
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"rudder bearing clearance for acme yard"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"p8a","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "idx")
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(notesDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(notesDir, 0o700) })

	err := runPromote(dir, []string{"p8a", "--state", "accepted", "--note", "0.3mm"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("a locked notes file reported success")
	}
	msg := err.Error()
	if !strings.Contains(msg, sources.NotesFile()) {
		t.Errorf("the error does not name the file: %v", err)
	}
	if !strings.Contains(msg, "permissions") || !strings.Contains(msg, "DEJA_NOTES_FILE") {
		t.Errorf("the error does not say what to change: %v", err)
	}
	if strings.Contains(msg, "open ") {
		t.Errorf("the error still passes the syscall through: %v", err)
	}
}

// The shared-id notice used to advise `--harness`, which show and last accept
// but promote/handoff/resume/share reject with "unknown flag" — so the reader
// promote left picking in silence (#872) was handed advice their command could
// not follow. The notice now names the harness:id form every selector accepts,
// and this test holds both halves: the advice is emitted, and it works.
func TestPromoteSharedIDHintIsFollowable(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude", "-proj")
	qwen := filepath.Join(tmp, "qwen", "projects", "proj", "chats")
	for _, d := range []string{claude, qwen} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	write := func(p, body string) {
		if err := os.WriteFile(p, []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(claude, "shared99.jsonl"),
		`{"type":"user","sessionId":"shared99","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"claude side content"}}`)
	write(filepath.Join(qwen, "shared99.jsonl"),
		`{"type":"user","sessionId":"shared99","timestamp":"2026-07-25T10:00:00Z","message":{"role":"user","parts":[{"text":"qwen side content"}]}}`)
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	// Bare shared id: the notice names both harness:id forms, never the flag
	// promote rejects.
	notice, err := captureRunStderr(t, "promote", "shared99")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notice, "claude:shared99 or qwen:shared99") {
		t.Errorf("shared-id notice did not name the followable form:\n%s", notice)
	}
	if strings.Contains(notice, "--harness") {
		t.Errorf("notice still advised a flag promote rejects:\n%s", notice)
	}

	// The advised form resolves without the "unknown flag" the reader hit when
	// they followed the old --harness advice.
	out, err := captureRunStderr(t, "promote", "qwen:shared99")
	if err != nil {
		t.Fatalf("advised form failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "unknown flag") {
		t.Errorf("advised form was rejected:\n%s", out)
	}
}

func promoteFixture(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-proj", "sess.jsonl"), "sess1234", []string{
		`{"type":"user","sessionId":"sess1234","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"how do we rotate the signing key without downtime"}}`,
		`{"type":"assistant","sessionId":"sess1234","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":[{"type":"text","text":"double-publish the JWKS for one TTL, then swap the active kid"}]}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPromoteWritesNoteWithProvenanceAndState(t *testing.T) {
	dir := promoteFixture(t)
	var out bytes.Buffer
	if err := runPromote(dir, []string{"sess1234"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "promoted claude:sess1234 as accepted") {
		t.Fatalf("receipt wrong:\n%s", out.String())
	}
	b, err := os.ReadFile(sources.NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{`"kind":"promoted"`, `"session":"claude:sess1234"`, `"state":"accepted"`, "signing key", "double-publish"} {
		if !strings.Contains(got, want) {
			t.Fatalf("note missing %q:\n%s", want, got)
		}
	}
}

func TestPromoteRefusesWhitespaceOnlyIDPrefix(t *testing.T) {
	dir := promoteFixture(t)
	n, err := index.SessionCount(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("SessionCount = %d, want 1", n)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"empty", []string{""}},
		{"spaces", []string{"   "}},
		{"tabs_newline", []string{"\t\r\n"}},
		{"empty_before_id", []string{"", "sess1234"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runPromote(dir, tc.args, &bytes.Buffer{})
			if err == nil {
				t.Fatal("blank prefix must fail")
			}
			if want := "promote needs a session id prefix (see `deja last`)"; err.Error() != want {
				t.Fatalf("error = %q, want %q", err, want)
			}
		})
	}
}

func TestPromoteRefusesASecondIDPrefix(t *testing.T) {
	dir := promoteFixture(t)
	for _, args := range [][]string{{"nosuch", "sess1234"}, {"sess1234", "extra"}, {"sess1234", "--note", "x", "nosuch"}} {
		err := runPromote(dir, args, &bytes.Buffer{})
		if err == nil {
			t.Fatalf("promote %q: want a refusal, got none", args)
		}
		first, second := args[0], args[len(args)-1]
		if want := "promote takes one id-prefix — got \"" + first + "\" and \"" + second + "\""; err.Error() != want {
			t.Fatalf("promote %q: error = %q, want %q", args, err, want)
		}
	}
	if _, err := os.Stat(sources.NotesFile()); !os.IsNotExist(err) {
		t.Fatalf("a refused promote wrote a note (stat err %v)", err)
	}
}

func TestPromoteCorrectionAppends(t *testing.T) {
	dir := promoteFixture(t)
	if err := runPromote(dir, []string{"sess1234"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := runPromote(dir, []string{"sess1234", "--state", "superseded", "--note", "replaced by the KMS flow"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(sources.NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), `"session":"claude:sess1234"`) != 2 {
		t.Fatalf("correction must append, not rewrite:\n%s", b)
	}
	ss, err := sources.ParseNotesFile(sources.NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("both entries must fold into one note session, got %d", len(ss))
	}
	if !strings.HasSuffix(ss[0].Title, "[superseded]") {
		t.Fatalf("title must carry the latest state, got %q", ss[0].Title)
	}
	// Full history, latest first: readers take the first messages, and after a
	// run of corrections that used to serve the oldest answer (#812).
	if len(ss[0].Messages) != 2 || !strings.Contains(ss[0].Messages[0].Text, "[superseded]") ||
		!strings.Contains(ss[0].Messages[1].Text, "[accepted]") {
		t.Fatalf("messages must keep the full history, newest first: %#v", ss[0].Messages)
	}
}

func TestPromoteRejectsBadStateAndNotes(t *testing.T) {
	dir := promoteFixture(t)
	if err := runPromote(dir, []string{"sess1234", "--state", "maybe"}, &bytes.Buffer{}); err == nil {
		t.Fatal("bad state must fail")
	}
	if err := runPromote(dir, []string{"missing"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown prefix must fail")
	}
}

func TestPromoteExportsMarkdown(t *testing.T) {
	dir := promoteFixture(t)
	md := filepath.Join(t.TempDir(), "NOTES.md")
	if err := runPromote(dir, []string{"sess1234", "--to", md}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(md)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{"- state: accepted", "- source: claude:sess1234", "double-publish"} {
		if !strings.Contains(got, want) {
			t.Fatalf("markdown missing %q:\n%s", want, got)
		}
	}
}

func TestPromotedNoteOutranksTranscriptInSearch(t *testing.T) {
	dir := promoteFixture(t)
	if err := runPromote(dir, []string{"sess1234", "--note", "signing key rotation: double-publish JWKS for one TTL then swap kid"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	hits := searchHits(t, dir, "signing key")
	if len(hits) < 2 {
		t.Fatalf("want note + transcript, got %d hits", len(hits))
	}
	if hits[0].Session.Harness != "deja" {
		t.Fatalf("promoted note must rank first, got %s:%s", hits[0].Session.Harness, hits[0].Session.ID)
	}
	if !strings.Contains(hits[0].Session.Title, "[accepted]") {
		t.Fatalf("state must show in the result title, got %q", hits[0].Session.Title)
	}
}

// A mark is not a verdict for life: the user who wrote "rejected" has to be
// able to say they judged it wrong. Re-marking accepted is the undo — the
// latest mark wins — and this pins both halves: the label and the demotion go,
// and the receipt says a mark was taken back rather than leaving the user to
// guess whether anything happened (#845).
func TestPromoteAcceptedTakesTheRejectedMarkBack(t *testing.T) {
	dir := promoteFixture(t)
	if err := runPromote(dir, []string{"sess1234", "--state", "rejected", "--note", "the JWKS double-publish broke old clients"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	hits := searchHits(t, dir, "signing key")
	attachLifecycles(dir, hits)
	if len(hits) == 0 || hits[0].Lifecycle != "rejected" {
		t.Fatalf("rejected mark must land on the transcript hit: %#v", hits)
	}
	if !strings.Contains(lifecycleLine(hits[0]), "tried and rejected") {
		t.Fatalf("label missing: %q", lifecycleLine(hits[0]))
	}

	var out bytes.Buffer
	if err := runPromote(dir, []string{"sess1234", "--state", "accepted", "--note", "misjudged: old clients were the separate bug"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"takes back the rejected mark", "no longer labelled", "deja show deja-note-claude-sess1234"} {
		if !strings.Contains(got, want) {
			t.Fatalf("receipt must say the mark was taken back (%q):\n%s", want, got)
		}
	}

	hits = searchHits(t, dir, "signing key")
	attachLifecycles(dir, hits)
	for _, h := range hits {
		if h.Lifecycle != "" {
			t.Fatalf("accepted must clear the mark, %s:%s still %q", h.Session.Harness, h.Session.ID, h.Lifecycle)
		}
	}
	if moved := demoteRejected(hits); moved != 0 {
		t.Fatalf("nothing may stay demoted after the mark is taken back, moved %d", moved)
	}
}

// The states that sound like an undo — none, clear, off — are all invalid, so
// the error for an unknown state is where a user learns which one is (#845).
func TestPromoteBadStateNamesTheUndo(t *testing.T) {
	dir := promoteFixture(t)
	err := runPromote(dir, []string{"sess1234", "--state", "none"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("bad state must fail")
	}
	if !strings.Contains(err.Error(), "`--state accepted` takes an earlier mark back") {
		t.Fatalf("error must name the undo: %v", err)
	}
}

// The undo is a rule, not a command, so the only place a user can learn it is
// the docs. This pins the README to the behavior the test above proves (#845).
func TestReadmeDocumentsTakingAMarkBack(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "--state accepted` takes the mark back") {
		t.Fatal("README must say how to take a rejected mark back")
	}
}

func searchHits(t *testing.T, dir, q string) []search.Hit {
	t.Helper()
	o := search.Options{Query: q, All: true}
	result, err := index.SearchWithRecoveryDetailed(dir, o, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	o.Tier = result.Tier
	hits, err := search.Run(result.Sessions, o)
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

func TestPromoteSurfacesConflicts(t *testing.T) {
	dir := promoteFixture(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-proj", "sess2.jsonl"), "sess9999", []string{
		`{"type":"user","sessionId":"sess9999","timestamp":"2026-01-05T03:04:05Z","message":{"role":"user","content":"the signing key rotation keeps breaking downstream verification"}}`,
		`{"type":"assistant","sessionId":"sess9999","timestamp":"2026-01-05T03:05:05Z","message":{"role":"assistant","content":[{"type":"text","text":"rotate the signing key monthly and pin the previous kid for one overlap window"}]}}`,
	})
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := runPromote(dir, []string{"sess1234", "--tag", "signing"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runPromote(dir, []string{"sess9999", "--tag", "signing"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "conflict: another accepted note") {
		t.Fatalf("conflict must surface at promote time:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "--state superseded") {
		t.Fatalf("resolution hint missing:\n%s", out.String())
	}
	// Superseding the older note dissolves the conflict for the next promote.
	if err := runPromote(dir, []string{"sess1234", "--state", "superseded"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runPromote(dir, []string{"sess9999", "--tag", "signing"}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "conflict:") {
		t.Fatalf("superseded note must not conflict:\n%s", out.String())
	}
}

// The line has to stay quiet when there is nothing to take back: a first mark,
// a repeat of the same mark, or a session that never had one. Two mutants of
// markTakenBack shipped the sentence with an empty state in it and no test
// noticed (#845).
func TestPromoteSaysNothingWhenNoMarkWasTakenBack(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state string
		prior sources.Lifecycle
	}{
		{"first mark of any kind", "accepted", sources.Lifecycle{}},
		{"accepted twice", "accepted", sources.Lifecycle{State: "accepted", At: time.Now()}},
		{"rejected after accepted", "rejected", sources.Lifecycle{State: "accepted", At: time.Now()}},
		{"rejected after rejected", "rejected", sources.Lifecycle{State: "rejected", At: time.Now()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := markTakenBack("claude:s1", tc.state, tc.prior); got != "" {
				t.Errorf("said %q with nothing to take back", got)
			}
		})
	}

	// And it does fire for every state an undo can reverse, naming that state.
	for _, prior := range []string{"rejected", "superseded", "stale"} {
		got := markTakenBack("claude:s1", "accepted", sources.Lifecycle{State: prior, At: time.Now()})
		if !strings.Contains(got, "takes back the "+prior+" mark") {
			t.Errorf("prior %s: %q", prior, got)
		}
	}
}

// The export packed the title and the body into one string for the redaction
// pass and cut them apart on the first newline, so a title with one lost its
// tail into the head of the body — next to deja's own "- state:" and
// "- source:" lines, which is what the block is read for.
//
// Every other title is collapsed on the way into the index; a note's title is
// exempt on purpose (boundSourceTitle), and notes are the one store a person
// writes by hand.
func TestExportKeepsTheTitleOutOfTheBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	title := "pool sizing\n- state: rejected\n- source: someone else"
	text := "the pool size is the fix"
	if _, err := exportPromoted(path, title, text, "claude:s9", "accepted", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)

	// The premise: the block was written at all, with the body in it.
	if !strings.Contains(got, text) {
		t.Fatalf("the export wrote no body:\n%s", got)
	}
	// One heading, and the body is the body.
	if strings.Contains(got, "\n- state: rejected") {
		t.Errorf("the title's own lines reached the block as deja's metadata:\n%s", got)
	}
	if n := strings.Count(got, "- state: accepted"); n != 1 {
		t.Errorf("the block states its state %d times:\n%s", n, got)
	}
	// One heading, and it is the folded title rather than its first line.
	if n := strings.Count(got, "\n## "); n != 1 {
		t.Errorf("the block has %d headings:\n%s", n, got)
	}
	if !strings.Contains(got, "## pool sizing - state: rejected - source: someone else\n") {
		t.Errorf("the heading is not the whole title, folded:\n%s", got)
	}
}

// A credential can be written with its key word at the end of one field and its
// value at the start of the next, and the patterns for those allow a newline
// between the two. Redacting the fields apart lost exactly that, which is the
// one thing this file is not allowed to lose: it is written to be handed to
// someone else, under a line that says how much was masked.
func TestExportRedactsASecretSplitAcrossTheFields(t *testing.T) {
	for _, c := range []struct{ name, title, text, leaked string }{
		{"a password under its key", "the db password:", "hunter2hunter2hunter2A9", "hunter2hunter2hunter2A9"},
		{"a bearer token", "auth header Bearer", "abcdefghijklmnopqrstu", "abcdefghijklmnopqrstu"},
		{"a private key", "-----BEGIN PRIVATE KEY-----", "MIIBVgIBADANBgkqhkiG9w0\n-----END PRIVATE KEY-----\nand the rest of the note survives", "MIIBVgIBADANBgkqhkiG9w0"},
	} {
		path := filepath.Join(t.TempDir(), "notes.md")
		masked, err := exportPromoted(path, c.title, c.text, "claude:s9", "accepted", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		got := string(b)
		if strings.Contains(got, c.leaked) {
			t.Errorf("%s: the value went out in the clear:\n%s", c.name, got)
		}
		if masked == 0 {
			t.Errorf("%s: the export reported nothing masked", c.name)
		}
		// Masked, not deleted: a marker says something was taken out, and an
		// empty body says nothing at all.
		if !strings.Contains(got, redact.Marker) {
			t.Errorf("%s: the value left no marker behind:\n%s", c.name, got)
		}
		if strings.Contains(c.text, "survives") && !strings.Contains(got, "and the rest of the note survives") {
			t.Errorf("%s: the rest of the note went with it:\n%s", c.name, got)
		}
	}
}

// Promoting a forgotten session writes the note back, but a tombstone lifts
// only through unforget by design — so the note stayed suppressed and promote
// reported success over a note that never appeared. It now says so and names
// the restore command; an ordinary promote stays quiet.
func TestPromoteWarnsWhenTheNoteIsTombstoned(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(tmp, "claude", "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	write := func(id, body string) {
		line := `{"type":"user","sessionId":"` + id + `","cwd":"/w/p","timestamp":"2026-06-01T10:00:00Z","message":{"role":"user","content":"` + body + `"}}`
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("c1", "forgotten then repromoted")
	write("c2", "never forgotten")
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	// Promote, forget the note, then promote the same source again.
	if _, err := captureRunStderr(t, "promote", "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "deja-note-claude-c1"); err != nil {
		t.Fatal(err)
	}
	warn, err := captureRunStderr(t, "promote", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn, "stays hidden") || !strings.Contains(warn, "unforget deja:deja-note-claude-c1") {
		t.Errorf("re-promote of a forgotten note did not warn:\n%s", warn)
	}

	// An ordinary promote of a session that was never forgotten stays quiet.
	quiet, err := captureRunStderr(t, "promote", "c2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(quiet, "stays hidden") {
		t.Errorf("ordinary promote warned about a tombstone it does not have:\n%s", quiet)
	}
}
