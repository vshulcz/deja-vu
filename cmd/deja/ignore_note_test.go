package main

import (
	"strings"
	"testing"
)

// The miss path prints the line so it lands before "try fewer words", where
// rewording is the wrong advice; the block after the cap note printed it again,
// so every search that missed said the same sentence twice (#2632).
func TestSearchSaysWhatTheIgnoreRuleKeptOutOnce(t *testing.T) {
	ignoredTreeStore(t)
	out, err := captureRunStderr(t, "widget pipeline")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "the ignore rule keeps"); n != 1 {
		t.Fatalf("the line is printed %d times, want once:\n%s", n, out)
	}
}

// files and blame apply the rule and said nothing, directly under the policy
// note that exists for the same misread: the topic did match, a rule withheld
// it (#686, #680).
func TestFilesAndBlameSayWhatTheIgnoreRuleKeptOut(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"files", []string{"files", "widget"}},
		{"blame", []string{"blame", "/w/scratch/widget/pipeline.go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ignoredTreeStore(t)
			out, err := captureRun(t, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			errOut, err := captureRunStderr(t, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			all := out + errOut
			if !strings.Contains(all, "the ignore rule keeps 3 sessions") {
				t.Fatalf("nothing names the rule that emptied the answer:\n%s", all)
			}
		})
	}
}

// A miss under an active filter takes its own branch and never reaches
// printNoMatches, so it says the line itself rather than losing it with the
// guard that stopped the doubling (#2632).
func TestSearchUnderAFilterStillNamesTheIgnoreRule(t *testing.T) {
	ignoredTreeStore(t)
	out, err := captureRunStderr(t, "--project", "keep", "widget pipeline")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "the ignore rule keeps"); n != 1 {
		t.Fatalf("the line is printed %d times, want once:\n%s", n, out)
	}
}
