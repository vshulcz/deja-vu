package sources

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNotesPathAndAppendParse(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_NOTES_FILE", "")
	t.Setenv("XDG_DATA_HOME", xdg)
	if got, want := NotesFile(), filepath.Join(xdg, "deja", "notes.jsonl"); got != want {
		t.Fatalf("NotesFile=%q want %q", got, want)
	}
	path := filepath.Join(t.TempDir(), "private", "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	// decision two is the same instant, so the two share a local day in every
	// zone. An hour apart they straddled midnight at UTC-3 (#5044).
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	if err := AppendNote("app", "decision one", when); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, appendMustRead(t, path, `{"ts":"2026-01-02T02:04:05Z","project":"app","text":"decision two"}`+"\n"+`{"ts":"bad","project":"app","text":"ignored"}`+"\n"+`{"ts":"2026-01-03T00:00:00Z","project":"other","text":"other"}`+"\n"+`{"ts":"2026-01-03T00:00:00Z","project":"app","text":"torn`), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseNotesFile(path)
	if err != nil || len(ss) != 2 {
		t.Fatalf("notes=%#v err=%v", ss, err)
	}
	if ss[0].Project != "app" || len(ss[0].Messages) != 2 || ss[1].Project != "other" {
		t.Fatalf("grouped notes=%#v", ss)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	offset := bytes.IndexByte(data, '\n') + 1
	if got, err := ParseNotesFileFromOffset(path, int64(offset)); err != nil || len(got) != 2 {
		t.Fatalf("offset notes=%#v err=%v", got, err)
	}
	if err := AppendNote("", "x", when); err == nil {
		t.Fatal("empty project accepted")
	}
	if err := AppendNote("app", "", when); err == nil {
		t.Fatal("empty text accepted")
	}
	if err := AppendNote("app", "  preserved  ", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{filepath.Dir(path), path} {
			info, statErr := os.Stat(p)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if info.Mode().Perm() != map[bool]os.FileMode{true: 0o600, false: 0o700}[p == path] {
				t.Fatalf("%s mode=%o", p, info.Mode().Perm())
			}
		}
	}
}

func TestNotesSymlinkAndMissingFile(t *testing.T) {
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(t.TempDir(), "missing", "notes.jsonl"))
	if got := LoadNotes(); got != nil {
		t.Fatalf("missing notes=%#v", got)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	t.Setenv("DEJA_NOTES_FILE", link)
	if err := AppendNote("p", "x", time.Now()); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink append err=%v", err)
	}
}

func appendMustRead(t *testing.T, path, suffix string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return append(b, suffix...)
}

func TestConflictingNotes(t *testing.T) {
	a := PromotedNote{Project: "app", Session: "claude:a", State: "accepted", Title: "retry policy", Text: "use exponential backoff with jitter for outbound retries", Tags: []string{"retries"}}
	b := PromotedNote{Project: "app", Session: "claude:b", State: "accepted", Title: "retry policy v2", Text: "fixed-interval retries are simpler and sufficient here", Tags: []string{"retries"}}
	c := PromotedNote{Project: "app", Session: "claude:c", State: "superseded", Title: "old retries", Text: "exponential backoff jitter outbound retries", Tags: []string{"retries"}}
	d := PromotedNote{Project: "other", Session: "claude:d", State: "accepted", Title: "retry policy", Text: "exponential backoff with jitter for outbound retries", Tags: []string{"retries"}}
	all := []PromotedNote{a, b, c, d}
	got := ConflictingNotes(a, all)
	if len(got) != 1 || got[0].Session != "claude:b" {
		t.Fatalf("conflicts = %+v", got)
	}
	// No tags: 3+ shared informative words still connect them ("retry",
	// "policy", "retries" — a genuine topical clash).
	a2, b2 := a, b
	a2.Tags, b2.Tags = nil, nil
	if got := ConflictingNotes(a2, []PromotedNote{a2, b2}); len(got) != 1 {
		t.Fatalf("topical word overlap must conflict, got %+v", got)
	}
	// A note sharing fewer than 3 informative words stays unrelated.
	far := PromotedNote{Project: "app", Session: "claude:e", State: "accepted", Title: "logging", Text: "ship structured logs with retries counter"}
	if got := ConflictingNotes(a2, []PromotedNote{a2, far}); len(got) != 0 {
		t.Fatalf("weak overlap conflicted: %+v", got)
	}
}

// `promote` copies the source session's first line into the note as its title.
// The note is a separate record, so `forget --session` removed the session and
// left that line visible in `deja last` — the sentence most likely to carry a
// customer name or a ticket id (#666).
func TestForgetPromotedTitles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	lines := []string{
		`{"ts":"2026-07-21T10:00:00Z","project":"p","text":"cap retries at 3","kind":"promoted","session":"claude:ps1","state":"accepted","title":"our customer ACME-4471 hit the retry storm"}`,
		`{"ts":"2026-07-21T10:00:00Z","project":"p","text":"other decision","kind":"promoted","session":"claude:other","state":"accepted","title":"an unrelated session title"}`,
		`{"ts":"2026-07-21T10:00:00Z","project":"p","text":"a plain note","kind":"note"}`,
		// A note of another kind that happens to name the same session: only
		// `promote` borrows a title, so only promoted notes lose one.
		`{"ts":"2026-07-21T10:00:00Z","project":"p","text":"a linked note","kind":"note","session":"claude:ps1","title":"a title this command must not touch"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := ForgetPromotedTitles(func(src string) bool { return strings.Contains(src, "ps1") })
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("cleared %d titles, want 1", n)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Contains(got, "ACME-4471") {
		t.Errorf("the forgotten session's first line survived:\n%s", got)
	}
	// The decision it was promoted for is often why the raw session was safe
	// to forget — the note itself must stay.
	if !strings.Contains(got, "cap retries at 3") {
		t.Errorf("the promoted decision was destroyed:\n%s", got)
	}
	// Unrelated notes are untouched, including plain ones.
	if !strings.Contains(got, "an unrelated session title") || !strings.Contains(got, "a plain note") ||
		!strings.Contains(got, "a title this command must not touch") {
		t.Errorf("touched a note it should not have:\n%s", got)
	}
	// Idempotent: a second run has nothing left to clear.
	if again, err := ForgetPromotedTitles(func(src string) bool { return strings.Contains(src, "ps1") }); err != nil || again != 0 {
		t.Fatalf("second run cleared %d, err %v", again, err)
	}
}

// Nothing matched must leave the file alone. Rewriting it in place for a
// no-op risks the notes of every other session on a crash between write and
// rename, for no change at all.
func TestForgetPromotedTitlesLeavesTheFileAloneWhenNothingMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	line := `{"ts":"2026-07-21T10:00:00Z","project":"p","text":"d","kind":"promoted","session":"claude:other","title":"t"}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if n, forgetErr := ForgetPromotedTitles(func(string) bool { return false }); forgetErr != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, forgetErr)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("rewrote the notes file for a no-op: %v -> %v", before.ModTime(), after.ModTime())
	}
}

func TestForgetPromotedTitlesWithNoNotesFile(t *testing.T) {
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(t.TempDir(), "missing.jsonl"))
	if n, err := ForgetPromotedTitles(func(string) bool { return true }); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v — a missing notes file is not an error", n, err)
	}
}

func TestRepoScopedNotesSanitizeMergeAndProvenance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	personalNotes := filepath.Join(home, "personal-notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", personalNotes)

	repoDir := filepath.Join(home, "work", "payments-api")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(repoDir, "internal", "handler")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(subDir)

	if got := NotesRepoRoot(""); got != repoDir {
		t.Fatalf("NotesRepoRoot() = %q, want %q", got, repoDir)
	}
	wantRepoNotes := filepath.Join(repoDir, ".deja", "notes.jsonl")
	if got := RepoNotesFile(""); got != wantRepoNotes {
		t.Fatalf("RepoNotesFile() = %q, want %q", got, wantRepoNotes)
	}

	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if err := AppendNoteTagged("payments-api", "personal note stays in home", []string{"local"}, now); err != nil {
		t.Fatal(err)
	}

	secret := "sk-ant-api03-" + strings.Repeat("a", 40)
	rawNote := "check " + filepath.Join(repoDir, "internal", "handler", "charge.go") + " and " + filepath.Join(home, ".config", "app.env") + " token " + secret
	masked, err := AppendRepoNoteTagged(repoDir, "", rawNote, []string{"team"}, now)
	if err != nil {
		t.Fatalf("AppendRepoNoteTagged: %v", err)
	}
	if masked == 0 {
		t.Fatal("expected secret in repo note to be redacted and counted")
	}
	if _, dupErr := AppendRepoNoteTagged(repoDir, "", rawNote, []string{"team"}, now); !errors.Is(dupErr, ErrNoteExists) {
		t.Fatalf("duplicate repo note err = %v, want ErrNoteExists", dupErr)
	}

	rawBytes, err := os.ReadFile(wantRepoNotes)
	if err != nil {
		t.Fatal(err)
	}
	diskText := string(rawBytes)
	if strings.Contains(diskText, secret) {
		t.Fatalf("secret leaked into .deja/notes.jsonl: %s", diskText)
	}
	if strings.Contains(diskText, repoDir) {
		t.Fatalf("absolute repo path leaked into .deja/notes.jsonl: %s", diskText)
	}
	if !strings.Contains(diskText, "internal/handler/charge.go") {
		t.Fatalf("expected repo-relative path in .deja/notes.jsonl: %s", diskText)
	}

	pMasked, err := AppendRepoPromotedSourced(
		repoDir,
		"payments-api",
		"fix retry budget in "+filepath.Join(repoDir, "retry.go"),
		"keep retry budget at 3 ("+secret+")",
		"claude:sess123",
		"accepted",
		[]string{"retry"},
		now.Add(-time.Hour),
		now,
	)
	if err != nil {
		t.Fatalf("AppendRepoPromotedSourced: %v", err)
	}
	if pMasked == 0 {
		t.Fatal("expected secret in promoted repo note to be redacted")
	}
	// Idempotent re-promote with identical content does not duplicate.
	if _, err := AppendRepoPromotedSourced(
		repoDir,
		"payments-api",
		"fix retry budget in "+filepath.Join(repoDir, "retry.go"),
		"keep retry budget at 3 ("+secret+")",
		"claude:sess123",
		"accepted",
		[]string{"retry"},
		now.Add(-time.Hour),
		now,
	); err != nil {
		t.Fatalf("idempotent AppendRepoPromotedSourced: %v", err)
	}

	files := NotesFiles()
	if len(files) != 2 || files[0] != personalNotes || files[1] != wantRepoNotes {
		t.Fatalf("NotesFiles() = %v, want [%q, %q]", files, personalNotes, wantRepoNotes)
	}
	if !IsNotesFile(personalNotes) || !IsNotesFile(wantRepoNotes) {
		t.Fatal("IsNotesFile returned false for personal or repo notes file")
	}

	sessions := LoadNotes()
	if len(sessions) != 3 {
		t.Fatalf("LoadNotes() returned %d sessions, want 3 (1 personal day note, 1 repo day note, 1 repo promoted note): %#v", len(sessions), sessions)
	}
	var sawPersonal, sawRepoDay, sawRepoPromoted bool
	for _, s := range sessions {
		switch {
		case s.From == "" && strings.HasPrefix(s.ID, "deja-"):
			sawPersonal = true
		case s.From == "repo" && strings.HasPrefix(s.ID, "deja-repo-note-"):
			sawRepoPromoted = true
		case s.From == "repo" && strings.HasPrefix(s.ID, "deja-repo-"):
			sawRepoDay = true
		}
	}
	if !sawPersonal || !sawRepoDay || !sawRepoPromoted {
		t.Fatalf("sessions missing expected provenance: personal=%v repoDay=%v repoPromoted=%v (%#v)", sawPersonal, sawRepoDay, sawRepoPromoted, sessions)
	}

	promoted := LoadPromotedNotes()
	if len(promoted) != 1 || promoted[0].Session != "claude:sess123" || promoted[0].State != "accepted" {
		t.Fatalf("LoadPromotedNotes() = %#v", promoted)
	}
	lc := PromotedLifecycles()
	if lc["claude:sess123"].State != "accepted" {
		t.Fatalf("PromotedLifecycles() = %#v", lc)
	}
}
