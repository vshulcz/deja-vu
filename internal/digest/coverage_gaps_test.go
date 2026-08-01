package digest

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Digests are cut to a byte budget. Cutting mid-rune puts a replacement
// character into whatever the agent reads next, so the cut has to land on a
// boundary — every harness downstream trusts this.
func TestUTF8SafeCutLandsOnRuneBoundaries(t *testing.T) {
	s := "привет мир"
	for n := 0; n <= len(s)+2; n++ {
		got := UTF8SafeCut(s, n)
		if !utf8.ValidString(got) {
			t.Fatalf("cut at %d produced invalid utf8: %q", n, got)
		}
		if len(got) > n && n <= len(s) {
			t.Fatalf("cut at %d returned %d bytes", n, len(got))
		}
	}
	if UTF8SafeCut(s, 0) != "" {
		t.Fatal("zero budget returned text")
	}
	if UTF8SafeCut(s, -5) != "" {
		t.Fatal("negative budget returned text")
	}
	if UTF8SafeCut(s, len(s)+10) != s {
		t.Fatal("a budget past the end truncated")
	}
	// ASCII is unaffected, or every English digest pays for this.
	if got := UTF8SafeCut("hello world", 5); got != "hello" {
		t.Fatalf("ascii cut = %q", got)
	}
}

func TestShortTrimsIdentifiers(t *testing.T) {
	// Both ends survive: a UUID cut to its head names no session, and the
	// message that prints it is telling the reader which one was refused
	// (#741).
	if got := Short("2fc1d1ef-59f0-4044-b986-ba349e11c53c"); got != "2fc1d1ef-…349e11c53c" {
		t.Fatalf("Short() = %q", got)
	}
	if got := Short("abc"); got != "abc" {
		t.Fatalf("a short id was trimmed: %q", got)
	}
	if got := Short(""); got != "" {
		t.Fatalf("empty id became %q", got)
	}
}

// Handoff packages a session for another agent to continue. It is read by a
// model with no other context, so the framing has to say where the work came
// from and that it should not be re-derived.
func TestHandoffFramesThePackagedContext(t *testing.T) {
	s := model.Session{
		ID: "s1", Harness: "claude", Project: "api", Updated: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		Messages: []model.Message{
			{Role: "user", Text: "the connection pool keeps running out"},
			{Role: "assistant", Text: "raised max_conns and added jitter to the retries"},
		},
	}
	got := Handoff(s, 4096)
	for _, want := range []string{"handed off", "claude", "api", "2026-05-01"} {
		if !strings.Contains(got, want) {
			t.Fatalf("handoff missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "connection pool") {
		t.Fatalf("handoff dropped the problem statement:\n%s", got)
	}
	if len(got) > 4096 {
		t.Fatalf("handoff is %d bytes, over budget", len(got))
	}
	// A session with no timestamp still hands off; saying "unknown" beats
	// printing a zero date that reads as 1 January year one.
	s.Updated = time.Time{}
	if got := Handoff(s, 4096); !strings.Contains(got, "unknown") {
		t.Fatalf("undated session lost its date framing:\n%s", got)
	}
}

// Transcripts are full of plumbing recorded under a user role. Indexing it
// makes recall answer with tool echoes instead of what the user said.
func TestIsAgentArtifactSpotsPlumbing(t *testing.T) {
	for text, want := range map[string]bool{
		"<environment_context>cwd: /tmp</environment_context>": true,
		"total 24\ndrwxr-xr-x  3 user staff":                   true,
		"File created successfully at: /tmp/x.go":              true,
		"diff --git a/main.go b/main.go":                       true,
		"$ go test ./...":                                      true,
		"the connection pool keeps running out":                false,
		"why did we replace the etag reuse?":                   false,
		"":                                                     false,
	} {
		if got := IsAgentArtifact(text); got != want {
			t.Fatalf("IsAgentArtifact(%.40q) = %v, want %v", text, got, want)
		}
	}
}

// Terminal output lands in transcripts with escape codes in it. Left in, they
// are indexed as tokens and shown back to the user as garbage.
func TestStripANSIRemovesEscapesOnly(t *testing.T) {
	in := "\x1b[31mred\x1b[0m and \x1b[1;32mgreen\x1b[0m"
	got := stripANSI(in)
	if strings.Contains(got, "\x1b") {
		t.Fatalf("escape survived: %q", got)
	}
	if !strings.Contains(got, "red") || !strings.Contains(got, "green") {
		t.Fatalf("stripping took the text with it: %q", got)
	}
	// Text with no escapes must come back untouched, byte for byte.
	plain := "plain text with [brackets] and 0m digits"
	if stripANSI(plain) != plain {
		t.Fatalf("plain text was altered: %q", stripANSI(plain))
	}
}
