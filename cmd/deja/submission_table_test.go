package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docs/benchmarks/SUBMISSION.md invites somebody to beat our row and prints
// that row: two sets, five metrics each. Nothing checked it. The headline test
// next door keys on "N.N% hit@1", which a table cell is not, so these ten
// figures could drift from the committed runs without a test noticing — and
// this is the page whose whole argument is that the numbers are checkable.
func TestTheSubmissionTableMatchesTheCommittedRuns(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "docs", "benchmarks", "SUBMISSION.md"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)

	read := func(name string) benchArtifact {
		b, err := os.ReadFile(filepath.Join(root, "docs", "benchmarks", name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var a benchArtifact
		if err := json.Unmarshal(b, &a); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return a
	}
	for _, c := range []struct {
		artifact string
		label    string
	}{
		{"longmemeval-s-cleaned.json", "470 questions"},
		{"longmemeval-s-full.json", "all 500"},
	} {
		a := read(c.artifact)
		row := submissionRow(t, page, c.label)
		for _, cell := range []struct {
			what string
			want float64
		}{
			{"hit@1", a.Total.Hit1},
			{"hit@5", a.Total.Hit5},
			{"hit@10", derefOr(a.Total.Hit10)},
			{"hit@20", derefOr(a.Total.Hit20)},
		} {
			want := fmt.Sprintf("%.1f%%", cell.want)
			if !strings.Contains(row, want) {
				t.Errorf("the %q row does not carry %s %s from %s:\n%s",
					c.label, cell.what, want, c.artifact, row)
			}
		}
		if want := fmt.Sprintf("%.3f", a.Total.MRR); !strings.Contains(row, want) {
			t.Errorf("the %q row does not carry MRR %s from %s:\n%s", c.label, want, c.artifact, row)
		}
		// The set size is the other half of the claim: 470 and 500 give
		// different numbers for the same system, which the page says itself.
		if !strings.Contains(row, fmt.Sprint(a.Total.N)) {
			t.Errorf("the %q row does not say which set it counted (%d questions)", c.label, a.Total.N)
		}
	}
}

// submissionRow is the table line whose first cell names that set.
func submissionRow(t *testing.T, page, label string) string {
	t.Helper()
	for _, line := range strings.Split(page, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "|") && strings.Contains(line, label) {
			return line
		}
	}
	t.Fatalf("SUBMISSION.md has no table row for %q — it is the row this page asks people to beat", label)
	return ""
}

func derefOr(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
