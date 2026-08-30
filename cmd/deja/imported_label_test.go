package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// labelStore indexes one local session and one that arrived by sync.
func labelStore(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-w-beta", "b1.jsonl"), "b1", []string{
		`{"type":"user","sessionId":"b1","cwd":"/w/beta","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the beta rollout"}}`,
	})
	writeClaudeFixture(t, filepath.Join(root, "-w-alpha", "a1.jsonl"), "a1", []string{
		`{"type":"user","sessionId":"a1","cwd":"/w/alpha","timestamp":"2026-07-02T10:00:00Z","message":{"role":"user","content":"the alpha pipeline"}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// The label `deja last` prints for an imported session — the sending machine in
// place of the "imported:" prefix — was accepted by no filter, so the one
// string a reader can copy off that screen worked nowhere (#2644).
func TestTheProjectFilterTakesTheLabelTheListingPrints(t *testing.T) {
	for _, tc := range []struct {
		name, project, from, want string
		match                     bool
	}{
		{"the label a listing prints", "imported:alpha", "mini", "mini:alpha", true},
		{"the stored form", "imported:alpha", "mini", "imported:alpha", true},
		{"the bare project", "imported:alpha", "mini", "alpha", true},
		// The machine alone stays --from's question: the project filter would
		// otherwise select everything that machine sent.
		{"the machine alone", "imported:alpha", "mini", "mini", false},
		{"another machine's label", "imported:alpha", "mini", "laptop:alpha", false},
		{"another project on this machine", "imported:beta", "mini", "mini:alpha", false},
		{"a local session is untouched", "alpha", "", "mini:alpha", false},
		{"a local session by its own name", "alpha", "", "alpha", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := projectFilterMatches(tc.project, tc.from, tc.want); got != tc.match {
				t.Fatalf("--project %q against %q from %q = %v, want %v", tc.want, tc.project, tc.from, got, tc.match)
			}
		})
	}
}

// And the label on the screen is built by the same function the filter reads,
// so the two cannot drift apart.
func TestTheListingLabelAndTheFilterAgree(t *testing.T) {
	s := model.Session{Project: "imported:alpha", From: "mini"}
	shown := displayProject(s)
	if shown != "mini:alpha" {
		t.Fatalf("the listing label changed: %q", shown)
	}
	if !projectFilterMatches(s.Project, s.From, shown) {
		t.Fatalf("the label the listing prints does not select the session it names")
	}
}

// End to end, on a store: the filter still selects what it always did.
func TestProjectFilterStillSelectsALocalProject(t *testing.T) {
	labelStore(t)
	out, err := captureRun(t, "last", "--project", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Fatalf("the project filter no longer selects a plain project:\n%s", out)
	}
}
