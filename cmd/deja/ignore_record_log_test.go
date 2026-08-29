package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/policy"
)

// ignoredTreeStore builds a store where one project is covered by the reader's
// ignore rule and holds everything worth finding: the command, the replaced
// span, the recurring error.
func ignoredTreeStore(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	if err := os.MkdirAll(filepath.Dir(policy.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policy.Path(), []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-w-keep", "k1.jsonl"), "k1", []string{
		`{"type":"user","sessionId":"k1","cwd":"/w/keep","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"morning notes"}}`,
	})
	for _, id := range []string{"s1", "s2", "s3"} {
		writeClaudeFixture(t, filepath.Join(root, "-w-scratch", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the widget pipeline keeps stalling"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"make widget-pipeline"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:01:30Z","message":{"role":"user","content":[{"type":"tool_result","content":"make: *** [widget-pipeline] Error 2"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/scratch/widget/pipeline.go","old_string":"before-line","new_string":"after-line"}}]}}`,
		})
	}
}

// The ignore rule is applied inside retrieval, and the surfaces that walk the
// record log instead of ranking did not apply it at all: `how`, `restore` and
// `friction` answered out of the tree the reader asked deja to stay out of, and
// with the default rule that tree is the one a background agent makes for
// itself (#2630).
func TestTheRecordLogSurfacesObeyTheIgnoreRule(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		leaks string
	}{
		{"how", []string{"how", "make widget"}, "make widget-pipeline"},
		{"restore", []string{"restore", "/w/scratch/widget/pipeline.go"}, "B replaced"},
		{"friction", []string{"friction"}, "widget-pipeline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ignoredTreeStore(t)
			out, err := captureRun(t, tc.args...)
			if err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			if strings.Contains(out, tc.leaks) {
				t.Fatalf("the ignored tree answered:\n%s", out)
			}
		})
	}
}

// And say so, rather than reading as a store that holds nothing: the same line
// the listing and search already print.
func TestTheRecordLogSurfacesSayWhatTheIgnoreRuleKeptOut(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"how", []string{"how", "make widget"}},
		{"friction", []string{"friction"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ignoredTreeStore(t)
			out, err := captureRunStderr(t, tc.args...)
			if err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			if !strings.Contains(out, "the ignore rule keeps 3 sessions") {
				t.Fatalf("nothing says the rule took the answer out:\n%s", out)
			}
		})
	}
}

// And the sentence on stdout, which is what a redirect keeps: saying the
// machine never recorded tool output is a claim about the store when the truth
// is a claim about the rule (#637, #1044, and now #2630).
func TestFrictionNamesTheRuleRatherThanTheStore(t *testing.T) {
	ignoredTreeStore(t)
	out, err := captureRun(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "the ignore rule keeps the 3 sessions that recorded tool output out of recall") {
		t.Fatalf("stdout blames the store for what the rule did:\n%s", out)
	}
}
