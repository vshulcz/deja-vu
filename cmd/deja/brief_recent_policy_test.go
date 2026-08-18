package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The brief's recent block names a project and the first line of a session.
// Search, `last`, blame and the status line all refuse a session a trust rule
// withholds; this screen named three of them.
func TestBriefRecentHonoursThePolicy(t *testing.T) {
	dir := importedStore(t, 3)
	writePolicy(t, `{"activations":{"search":{"local":true,"imported":false},"auto":{"local":true,"imported":false}}}`)

	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); strings.Contains(out, "projx") || strings.Contains(out, "kafka") {
		t.Errorf("the brief names a session the policy withholds:\n%s", out)
	}
}

// With no rule in the way the block still fills: a fix that simply dropped it
// would pass the test above.
func TestBriefRecentStillNamesWhatIsAllowed(t *testing.T) {
	dir := importedStore(t, 3)

	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "recent") || !strings.Contains(out, "projx") {
		t.Errorf("the brief stopped naming recent sessions:\n%s", out)
	}
	if n := strings.Count(out, "projx"); n != 3 {
		t.Errorf("the brief names %d recent sessions, want 3:\n%s", n, out)
	}
}

// A rule that withholds only some sessions leaves the block full rather than
// short: the brief asks for more than it shows for exactly this reason.
func TestBriefRecentFillsAroundWithheldSessions(t *testing.T) {
	dir := importedStore(t, 3)
	// A local session alongside the imported ones, newer than none of them:
	// the block should fill with it rather than come back short.
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the local scheduler question"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"solo","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "solo.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// The withheld one is the NEWEST, so a brief that asks for only as many as
	// it shows comes back short.
	writePolicy(t, `{"activations":{"search":{"local":false,"imported":true}}}`)

	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "scheduler") {
		t.Errorf("the brief names a withheld session:\n%s", out)
	}
	if n := strings.Count(out, "projx"); n != 3 {
		t.Errorf("the brief names %d recent sessions, want the block filled with the 3 allowed:\n%s", n, out)
	}
}
