package sources

import "testing"

// The allowlist carried make and gradle and not just, task or mise, so a
// repository driven by a justfile recorded none of its own commands. That costs
// more than the command table: the point-of-action hook asks whether a session
// ran the command before it speaks, so on those repositories it could never
// speak about a deploy or a migration at all.
func TestWorthIndexingKeepsTheTaskRunners(t *testing.T) {
	for _, c := range []string{
		"just build",
		"just deploy staging",
		"task test",
		"task db:migrate",
		"mise run lint",
		"cd repo && just release",
	} {
		if !worthIndexing(c) {
			t.Errorf("dropped a task runner: %q", c)
		}
	}
	// Each of these names is an ordinary English word, so they only count at
	// the start of a segment — otherwise every sentence about a task became a
	// command this machine had run.
	for _, c := range []string{
		"echo 'just do it'",
		"ls tasks/",
		"cat notes.md | grep task",
	} {
		if worthIndexing(c) {
			t.Errorf("kept a word that names no runner: %q", c)
		}
	}
	// A runner named inside another command's argument is that command's
	// business, not a run of its own — the rule the allowlist already applies
	// to `grep "go test" log`. These are the cases the anchor decides: the
	// segment is neither trivial nor otherwise meaningful, so without it the
	// word alone would carry them in.
	for _, c := range []string{
		"ssh mini task deploy",
		"xargs just build",
		"time mise run lint",
		// The one this rule kept when it was judged per pipe: a quoted
		// alternation is cut on its own `\|` and the fragment reads like a
		// run. Found on a real store, one line in 119,024.
		`grep -n "worktree names\|task signal" cmd/deja/doctor.go`,
	} {
		if worthIndexing(c) {
			t.Errorf("kept a runner named as another command's argument: %q", c)
		}
	}
}
