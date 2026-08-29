package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Two ways a secret reached the stored title. The derived title was cut to 60
// runes and only then redacted, so a secret straddling the cut lost the tail
// its pattern needs and the plaintext head stayed; and the incremental
// fallback, which fills a title that was empty on the first pass, redacted
// nothing at all. Both land in sessions.gob and on every page that prints a
// title.
func TestDerivedTitleRedactsBeforeItCuts(t *testing.T) {
	at := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	// 29 characters of lead, so the 60-rune cut falls between the password and
	// the "@" that the url-credentials pattern needs.
	const straddling = "rotate the staging secret ok https://deploy:hunter2swordfish@internal.example.com/x"
	cases := []struct {
		name   string
		msgs   []model.Message
		absent string
	}{
		{"secret across the cut", []model.Message{
			{Role: "user", Text: straddling, Time: at},
		}, "hunter2swordfish"},
		{"secret before the cut", []model.Message{
			{Role: "user", Text: "ship it with AKIAIOSFODNN7EXAMPLE now", Time: at},
		}, "AKIAIOSFODNN7EXAMPLE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := metaForSession(model.Session{ID: "s1", Harness: "claude", Messages: c.msgs}).Title
			if strings.Contains(got, c.absent) {
				t.Fatalf("the stored title carries the secret in the clear: %q", got)
			}
			if !strings.Contains(got, "[redacted:") {
				t.Fatalf("title %q lost the redaction marker", got)
			}
			if n := len([]rune(got)); n > 61 { // 60 plus the ellipsis
				t.Fatalf("title is %d runes, want the 60-rune cut: %q", n, got)
			}
		})
	}
}

// The second pass fills a title the first pass could not derive: the session
// opened with harness plumbing, so it was indexed titleless, and the real
// first turn arrived on a later append carrying a key.
func TestIncrementalTitleFallbackRedacts(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	file := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s.jsonl")
	write(t, file, claudeLine("s1", "2026-08-07T10:00:00Z", "<command-name>/init</command-name>"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The first pass has nothing but the slash command to go on, so the row
	// carries the stand-in #2548 gave it rather than a bare id — and the real
	// first turn, when it arrives, still replaces it.
	if got := m.Sessions["claude:s1"].Title; !titlePlaceholder(got) {
		t.Fatalf("precondition: the first pass already titled the session %q", got)
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(claudeLine("s1", "2026-08-07T10:01:00Z", "ship it with AKIAIOSFODNN7EXAMPLE now")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	title := m.Sessions["claude:s1"].Title
	if title == "" {
		t.Fatalf("the append did not fill the title at all: %#v", m.Sessions["claude:s1"])
	}
	if strings.Contains(title, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("the incremental fallback stored an unredacted key: %q", title)
	}
	if titlePlaceholder(title) {
		t.Fatalf("the stand-in outlived the turn that should have replaced it: %q", title)
	}
}
