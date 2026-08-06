package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The card is an SVG document. Writing it into card.png produced a file
// GitHub serves as a PNG and browsers refuse, while the command printed the
// markdown to embed it — a broken image for anyone who followed the hint.

func TestCardFileNameKeepsTheFormatHonest(t *testing.T) {
	// An extension deja cannot honour is replaced, not appended: card.png used
	// to become card.png.svg, an SVG named after a format it is not (#1056).
	for in, want := range map[string]string{
		"deja-stats.svg": "deja-stats.svg",
		"CARD.SVG":       "CARD.SVG",
		"card.png":       "card.svg",
		"card.txt":       "card.svg",
		"card":           "card.svg",
		"dir/card":       "dir/card.svg",
		"dir/card.jpeg":  "dir/card.svg",
	} {
		got, note := cardFileName(in)
		if got != want {
			t.Fatalf("cardFileName(%q) = %q, want %q", in, got, want)
		}
		// The replacement is stated once; a name deja could keep says nothing.
		if wantNote := filepath.Ext(in) != "" && !strings.EqualFold(filepath.Ext(in), ".svg"); wantNote != (note != "") {
			t.Errorf("cardFileName(%q) note = %q, want a note: %v", in, note, wantNote)
		}
	}
}

// `deja restore` matters at one panicked moment, and nobody reads a command
// list then. The number is stated where someone reads calmly, and it is their
// own data (#577).
func TestStatsNamesWhatRestoreCanRecover(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-s")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"sp1","cwd":"/w","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"fix the pool"}}` + "\n" +
		`{"type":"assistant","sessionId":"sp1","cwd":"/w","timestamp":"2026-07-20T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/pool.go","old_string":"the bytes that stopped existing","new_string":"new"}}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "sp1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Recover") || !strings.Contains(out, "1 span your agents replaced") {
		t.Fatalf("stats does not say what can be recovered:\n%s", out)
	}
	if !strings.Contains(out, "deja restore") {
		t.Fatalf("the command is not named:\n%s", out)
	}
	// Singular reads right: "1 spans across 1 files" is the kind of thing that
	// makes a page look unfinished.
	if strings.Contains(out, "1 spans") || strings.Contains(out, "1 files") {
		t.Fatalf("plural disagreement:\n%s", out)
	}
}

// A store with nothing to recover says nothing: a zero here is an invitation
// to a command that would answer nothing.
func TestStatsSilentWithNoSpans(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-n")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"n1","cwd":"/w","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"just talking"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "n1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Recover") || strings.Contains(out, "deja restore") {
		t.Fatalf("offered recovery on a store with nothing to recover:\n%s", out)
	}
}
