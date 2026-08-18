package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// pairStore writes one session per call into the same project and re-indexes,
// so sessions arrive the way they really do: one at a time.
func pairStore(t *testing.T) (dir, proj string) {
	t.Helper()
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	proj = filepath.Join(claude, "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(tmp, "index.db"), proj
}

// writePairSession puts an error and the command run after it into one session.
func writePairSession(t *testing.T, proj, id, errText, cmd string) {
	t.Helper()
	line := func(role, text string) string {
		switch role {
		case "tool":
			return `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":[{"type":"tool_result","content":"` + text + `"}]}}` + "\n"
		default:
			return `{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + text + `"}}]}}` + "\n"
		}
	}
	body := line("tool", errText) + line("cmd", cmd)
	if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A pair whose only evidence is that another session did the same thing after
// the same error was lost when the sessions arrived one at a time: the first
// candidate was rejected and forgotten, so the second had nothing to count
// against (#1301).
func TestACrossSessionPairSurvivesOneAtATimeArrival(t *testing.T) {
	dir, proj := pairStore(t)
	const errText = "undefined: renderInvoice in vendor/billing/api.go"
	const cmd = "go mod vendor \\u0026\\u0026 go build ./..."

	writePairSession(t, proj, "one", errText, cmd)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := FixesFor(dir, errText, 3, nil); len(got) != 0 {
		t.Fatalf("one session is not evidence yet, got %d pairs", len(got))
	}

	writePairSession(t, proj, "two", errText, cmd)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	got := FixesFor(dir, errText, 3, nil)
	if len(got) == 0 {
		t.Fatalf("the second session confirmed the remedy and the pair is still missing")
	}
}

// The whole-corpus build and the one-at-a-time build have to agree, which is
// the measurement the issue reported failing.
func TestPairsAgreeWhicheverWayTheIndexGotThere(t *testing.T) {
	const errText = "undefined: renderInvoice in vendor/billing/api.go"
	const cmd = "go mod vendor \\u0026\\u0026 go build ./..."

	grownDir, grownProj := pairStore(t)
	writePairSession(t, grownProj, "one", errText, cmd)
	if err := Ensure(grownDir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	writePairSession(t, grownProj, "two", errText, cmd)
	if err := Ensure(grownDir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	wholeDir, wholeProj := pairStore(t)
	writePairSession(t, wholeProj, "one", errText, cmd)
	writePairSession(t, wholeProj, "two", errText, cmd)
	if err := Ensure(wholeDir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	whole := len(FixesFor(wholeDir, errText, 3, nil))
	grown := len(FixesFor(grownDir, errText, 3, nil))
	if whole != grown {
		t.Errorf("built whole gives %d pairs, grown one session at a time gives %d", whole, grown)
	}
}

// A candidate is not an answer. Until a second session confirms it, nothing
// serves it — one session doing something after an error is half the evidence.
func TestACandidateIsNeverServed(t *testing.T) {
	dir, proj := pairStore(t)
	const errText = "undefined: renderInvoice in vendor/billing/api.go"
	writePairSession(t, proj, "one", errText, "go mod vendor \\u0026\\u0026 go build ./...")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := FixesFor(dir, errText, 3, nil); len(got) != 0 {
		t.Errorf("a single sighting was served as a pair: %+v", got)
	}
	// It is on file, though — that is the point.
	held := 0
	for _, p := range ReadFixes(dir) {
		if p.Candidate {
			held++
		}
	}
	if held == 0 {
		t.Error("the sighting was thrown away, so a later session has nothing to confirm")
	}
}

// The incremental path has its own merge, and it is the one a live machine
// actually runs: sessions arrive one at a time, and the carried table is where
// the earlier sighting has to be waiting.
func TestTheIncrementalMergePromotesACarriedCandidate(t *testing.T) {
	dir := t.TempDir()
	const errText = "psql: connection refused on port 5432"
	const cmd = "brew services start postgresql"
	carried := []FixPair{{
		Sig: frictionHash(errText), Error: errText, Command: cmd,
		Key: "claude:one", When: time.Now().Add(-time.Hour), Candidate: true,
	}}
	if err := writeGob(fixesPath(dir), carried); err != nil {
		t.Fatal(err)
	}
	// A second session does the same thing after the same error.
	second := model.Session{Harness: "claude", ID: "two", Messages: []model.Message{
		{Role: roleToolOutput, Text: errText, Time: time.Now()},
		{Role: roleCommand, Text: cmd, Time: time.Now()},
	}}
	mergeFixes(dir, dir, []model.Session{second}, map[string]bool{})
	served := 0
	for _, p := range ReadFixes(dir) {
		if !p.Candidate {
			served++
		}
	}
	if served == 0 {
		t.Errorf("a carried sighting plus a fresh one did not make a pair: %+v", ReadFixes(dir))
	}
	// And the candidate copy of the same thing is gone, so the table does not
	// carry both readings of one fact.
	for _, p := range ReadFixes(dir) {
		if p.Candidate && p.Command == cmd {
			t.Errorf("the promoted pair is still marked a candidate: %+v", p)
		}
	}
}

// And the merge keeps a sighting it sees for the first time, so the session
// that arrives tomorrow has something to confirm. Two updates, one session
// each, which is what a live machine does all day.
func TestTheIncrementalMergeKeepsAFirstSighting(t *testing.T) {
	dir := t.TempDir()
	const errText = "psql: connection refused on port 5432"
	const cmd = "brew services start postgresql"
	session := func(id string, when time.Time) model.Session {
		return model.Session{Harness: "claude", ID: id, Messages: []model.Message{
			{Role: roleToolOutput, Text: errText, Time: when},
			{Role: roleCommand, Text: cmd, Time: when.Add(time.Second)},
		}}
	}
	mergeFixes(dir, dir, []model.Session{session("one", time.Now().Add(-time.Hour))}, map[string]bool{})
	if got := FixesFor(dir, errText, 3, nil); len(got) != 0 {
		t.Fatalf("one sighting was served as a pair: %+v", got)
	}
	mergeFixes(dir, dir, []model.Session{session("two", time.Now())}, map[string]bool{})
	if got := FixesFor(dir, errText, 3, nil); len(got) == 0 {
		t.Errorf("the second update confirmed the remedy and nothing was served: %+v", ReadFixes(dir))
	}
}
