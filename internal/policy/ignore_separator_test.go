package policy

import "testing"

// An ignore rule is written with forward slashes — it is a directory rule, and
// that is how a person writes one — while a session path on Windows carries
// backslashes. Compared as they are, the rule matched nothing there and the
// tree it names was recalled on every surface (#2808).
func TestAnIgnoreRuleReadsAPathWithEitherSeparator(t *testing.T) {
	var p Policy
	for _, path := range []string{
		`/home/me/.claude/jobs/abc/projects/-w-app/scratch.jsonl`,
		`C:\Users\me\.claude\jobs\abc\projects\-w-app\scratch.jsonl`,
		`C:/Users/me/.claude/jobs/abc/projects/-w-app/scratch.jsonl`,
	} {
		if !p.Ignored(path, "") {
			t.Errorf("the default rule does not reach %q", path)
		}
	}
	if p.Ignored(`C:\Users\me\.claude\projects\-w-app\real.jsonl`, "") {
		t.Error("an ordinary session was ignored")
	}
	// A rule someone writes in the Windows spelling reaches the same tree.
	win := Policy{Ignore: []string{`*\.claude\jobs\*`}}
	if !win.Ignored(`C:\Users\me\.claude\jobs\abc\s.jsonl`, "") {
		t.Error("a rule written with backslashes does not match its own tree")
	}
}
