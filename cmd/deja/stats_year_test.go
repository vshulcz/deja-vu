package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The two rules #578 puts on this screen, and they are the whole point of it:
// every number says the arithmetic behind it, and nothing leaves for a screen
// meant to be shown to other people without the outbound redaction pass.
func TestYearScreenLabelsItsArithmeticAndRedactsWhatItQuotes(t *testing.T) {
	dir := yearFixture(t)
	var out bytes.Buffer
	if err := runStatsYear(&out, dir, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()

	for _, want := range []string{
		"deduplicated by path",
		"deduplicated by the command line",
		"asked in more than one session",
		"the exact bytes an edit overwrote",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the screen prints a number without the arithmetic behind it — no %q:\n%s", want, got)
		}
	}
	// The friction line the fixture plants carries a home path and an internal
	// hostname, which is exactly what a screen written to be pasted must not
	// carry.
	if strings.Contains(got, "/Users/dana") || strings.Contains(got, "build.internal") {
		t.Errorf("the year screen quotes a home path or an internal host:\n%s", got)
	}
	if !strings.Contains(got, "masked for outbound use") {
		t.Errorf("the screen masked something and did not say so:\n%s", got)
	}
	// And it says what it quoted at all, or the redaction check above passes
	// on an empty screen.
	if !strings.Contains(got, "sessions hit") {
		t.Errorf("no recurring error reached the screen, so the redaction is unproven:\n%s", got)
	}
}

func TestYearJSONCarriesTheSameNumbers(t *testing.T) {
	dir := yearFixture(t)
	var out bytes.Buffer
	if err := runStatsYear(&out, dir, true); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Kind      string `json:"kind"`
		Sessions  int    `json:"sessions"`
		Questions struct {
			Distinct int `json:"distinct"`
			Repeated int `json:"repeated"`
		} `json:"questions"`
		Work struct {
			Files    int `json:"files"`
			Commands int `json:"commands"`
			Spans    int `json:"spans"`
		} `json:"work"`
		Friction []struct {
			Line     string `json:"line"`
			Sessions int    `json:"sessions"`
		} `json:"friction"`
		Masked map[string]int `json:"masked"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("the JSON does not parse: %v\n%s", err, out.String())
	}
	if got.Kind != yearJSONKind {
		t.Errorf("kind = %q, want %q", got.Kind, yearJSONKind)
	}
	if got.Sessions == 0 || got.Work.Files == 0 || got.Work.Spans == 0 {
		t.Errorf("the JSON is empty where the screen is not: %+v", got)
	}
	if got.Questions.Repeated < 1 || got.Questions.Distinct <= got.Questions.Repeated {
		t.Errorf("questions = %d repeated of %d distinct, want a repeat inside a larger population",
			got.Questions.Repeated, got.Questions.Distinct)
	}
	if len(got.Friction) == 0 {
		t.Fatalf("no friction in the JSON: %s", out.String())
	}
	for _, f := range got.Friction {
		if strings.Contains(f.Line, "/Users/dana") || strings.Contains(f.Line, "build.internal") {
			t.Errorf("the JSON carries what the screen masked: %q", f.Line)
		}
	}
	if len(got.Masked) == 0 {
		t.Errorf("nothing reported as masked, though the fixture plants two shapes: %s", out.String())
	}
}

// A filter would narrow the sessions and not the records, so the screen would
// stop adding up. It refuses rather than printing a report half-filtered.
func TestYearRefusesFiltersAndTheOtherReports(t *testing.T) {
	dir := yearFixture(t)
	t.Setenv("DEJA_INDEX_DIR", dir)
	for _, args := range [][]string{
		{"--year", "--harness", "claude"},
		{"--year", "--since", "30d"},
		{"--year", "--project", "app"},
		{"--year", "--role", "command"},
		{"--year", "--impact"},
		{"--year", "--card"},
	} {
		err := runStats(dir, args)
		if err == nil {
			t.Errorf("deja stats %v was accepted", args)
			continue
		}
		if !strings.Contains(err.Error(), "--year takes no filters") && !strings.Contains(err.Error(), "two reports") &&
			!strings.Contains(err.Error(), "one output") {
			t.Errorf("deja stats %v refused with %q, which does not say why", args, err)
		}
	}
}

// yearFixture is a year's worth of three sessions: the same file edited twice,
// the same command run in each, one question asked in all three, and an error line
// carrying a home path and an internal hostname.
func yearFixture(t *testing.T) string {
	t.Helper()
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	fail := `make: *** [build] Error 1 in /Users/dana/src/app, reported by build.internal`
	for i, id := range []string{"y1", "y2", "y3"} {
		// Each session asks at its own time: the same question at the same
		// moment in two sessions is one conversation copied, not a repeat.
		ts := time.Now().Add(-48*time.Hour + time.Duration(i)*time.Hour).UTC().Format(time.RFC3339)
		lines := []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts +
				`","message":{"role":"user","content":"why does the payout retry fire twice in a row"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts +
				`","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"make build"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts +
				`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"` + fail + `"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts +
				`","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/retry.go","old_string":"for i := 0; i < attempts; i++ {","new_string":"for i := 0; i <= attempts; i++ {"}}]}}`,
		}
		// A second question, asked once, so the repeat has a population to sit
		// in: "1 of 1 asked twice" is not a figure about anything.
		if id == "y3" {
			lines = append(lines, `{"type":"user","sessionId":"y3","cwd":"/work/app","timestamp":"`+ts+
				`","message":{"role":"user","content":"where does the scheduler read its failover timeout from"}}`)
		}
		writeClaudeFixture(t, filepath.Join(claudeRoot, "-work-app", id+".jsonl"), id, lines)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}
