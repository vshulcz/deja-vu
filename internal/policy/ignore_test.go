package policy

import "testing"

// The default is the one directory nobody works in: the temp tree a background
// agent's runtime makes for itself. Measured on a real store, 207 of the 300
// most recent sessions sat there (#2050).
func TestTheDefaultIgnoresAnAgentsScratchTree(t *testing.T) {
	var p Policy
	for _, c := range []struct {
		name, path, project string
		want                bool
	}{
		{"a job's temp tree", "/Users/x/.claude/jobs/9f0aa059/tmp/ab2/neutral", "neutral", true},
		{"opencode, which carries the directory in the project",
			"/Users/x/.local/share/opencode/db", "/Users/x/.claude/jobs/abc/tmp/run", true},
		{"a real project", "/Users/x/code/deja-vu/s.jsonl", "deja-vu", false},
		{"a system temp directory, where a person may genuinely work",
			"/var/folders/jn/T/spike/s.jsonl", "spike", false},
		{"a repository with jobs in the name", "/Users/x/code/jobs-api/s.jsonl", "jobs-api", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := p.Ignored(c.path, c.project); got != c.want {
				t.Errorf("Ignored(%q, %q) = %v, want %v", c.path, c.project, got, c.want)
			}
		})
	}
}

// Someone who writes the key has an opinion about what deja should skip.
// Keeping the built-in rule alongside theirs would mean a config file that does
// not say what it does.
func TestAWrittenListReplacesTheDefault(t *testing.T) {
	p := Policy{Ignore: []string{"*/ci-checkout/*"}}
	if !p.Ignored("/build/ci-checkout/repo/s.jsonl", "repo") {
		t.Error("the pattern from the file did not match")
	}
	if p.Ignored("/Users/x/.claude/jobs/abc/tmp/run", "run") {
		t.Error("the default is still in force after the file named its own rules")
	}
}

// A directory rule is written as a fragment, and a session path is longer than
// the rule that describes it.
func TestAFragmentMatchesAnywhereInThePath(t *testing.T) {
	p := Policy{Ignore: []string{"*/eval-harness/*"}}
	for _, s := range []string{
		"/home/runner/work/eval-harness/case-3/s.jsonl",
		"/Users/x/eval-harness/s.jsonl",
	} {
		if !p.Ignored(s, "") {
			t.Errorf("%q was not matched", s)
		}
	}
	if p.Ignored("/Users/x/code/harness/s.jsonl", "") {
		t.Error("a shorter name matched a longer rule")
	}
}

// What is in force has to be printable, or a rule that silently drops history
// is indistinguishable from history that was never there.
func TestWhatIsInForceCanBeShown(t *testing.T) {
	var p Policy
	if got := p.IgnorePatterns(); len(got) != 1 || got[0] != defaultIgnore[0] {
		t.Errorf("the default is not reportable: %v", got)
	}
	p.Ignore = []string{"*/x/*"}
	if got := p.IgnorePatterns(); len(got) != 1 || got[0] != "*/x/*" {
		t.Errorf("the file's own rules are not reportable: %v", got)
	}
}
