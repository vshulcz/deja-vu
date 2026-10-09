package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The hint exists for the guess that fell through to search (#674). A guess
// with an exact answer behind it was sent somewhere else: `hook context` is the
// hyphen-free spelling of a command that exists, and the hint proposed `deja
// how`, because the candidate list drops the plumbing and `how` is what
// survives nearest to `hook` (#2115).
func TestTheHintNamesTheCommandThatWasGuessedAt(t *testing.T) {
	for _, c := range []struct {
		query string
		want  string
	}{
		{"hook context", "deja hook-context"},
		{"hook prompt --plain", "deja hook-prompt"},
		// No second word to join, so the stem is all deja has: say the
		// commands exist rather than pointing at an unrelated one.
		{"hook", "hook-"},
		// The word deja's own MCP tool is called, and the docs use it
		// throughout, so it is a fair guess at the shell form.
		{"recall pgbouncer", "deja \"pgbouncer\""},
	} {
		got := commandHint(c.query)
		if !strings.Contains(got, c.want) {
			t.Errorf("%q -> %q, want it to name %q", c.query, got, c.want)
		}
		if strings.Contains(got, "deja how") {
			t.Errorf("%q was sent to an unrelated command: %q", c.query, got)
		}
	}
}

// And the hints that already worked keep working.
func TestTheHintStillAnswersTheGuessesItAlreadyKnew(t *testing.T) {
	for _, c := range []struct {
		query, want string
	}{
		{"serch pool", "deja search"},
		{"unpromote", "promote <id> --state rejected"},
		{"unforget", "forget --unforget"},
		{"upgrade", "deja update"},
	} {
		if got := commandHint(c.query); !strings.Contains(got, c.want) {
			t.Errorf("%q -> %q, want it to name %q", c.query, got, c.want)
		}
	}
	// A word that is a word, not a guess: no hint at all.
	for _, q := range []string{
		"pgbouncer pool timeout", "brief", "search pool",
		// Prose that starts with a stem or with a tool's name is a search, not
		// a guess at a command.
		"hook up the pool", "recall the decision we made about the pool",
	} {
		if got := commandHint(q); got != "" {
			t.Errorf("%q got a command hint it did not need: %q", q, got)
		}
	}
}

// A first word one edit from a command gets the hint before the search, and
// the search still runs (#4628): lost, fixes and logs are also words people
// look for, and refusing them hid the answer.
func TestOneEditCommandTypoHintsThenSearches(t *testing.T) {
	const content = "serch pool statsx statz lost connection fixes for redis logs rotation"
	for _, indexed := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			args []string
			want string
		}{
			{"deletion", []string{"serch", "pool"}, "search"},
			{"insertion", []string{"statsx"}, "stats"},
			{"substitution", []string{"statz"}, "stats"},
			{"case insensitive", []string{"SERCH", "pool"}, "search"},
			{"real word lost", []string{"lost", "connection"}, "last"},
			{"real word fixes", []string{"fixes", "for", "redis"}, "files"},
			{"real word logs", []string{"logs", "rotation"}, "log"},
		} {
			name := tc.name + "/fresh"
			if indexed {
				name = tc.name + "/indexed"
			}
			t.Run(name, func(t *testing.T) {
				hermeticEnv(t)
				t.Setenv("DEJA_STORES", "claude")
				writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "app", "one.jsonl"), "s1", []string{
					`{"type":"user","sessionId":"s1","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"` + content + `"}}`,
				})
				if indexed {
					if _, err := captureRunStderr(t, "index"); err != nil {
						t.Fatal(err)
					}
				}
				var out string
				var err error
				said := captureStderr(t, func() { out, err = captureRun(t, tc.args...) })
				if err != nil {
					t.Errorf("a query with results failed: %v", err)
				}
				if !strings.Contains(out, "serch pool") {
					t.Errorf("the search did not run: stdout %q, stderr %q", out, said)
				}
				hint := "did you mean `deja " + tc.want + "`?"
				if n := strings.Count(said, hint); n != 1 {
					t.Errorf("want the suggestion once, got %d: %q", n, said)
				}
				if i := strings.Index(said, "indexing"); i >= 0 && i < strings.Index(said, hint) {
					t.Errorf("the suggestion came after the index build: %q", said)
				}
			})
		}
	}
}

// A typo with nothing behind it still says the command once and exits 1, as
// the empty-result hint always has.
func TestOneEditCommandTypoWithNoResultsSaysItOnce(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_STORES", "claude")
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "app", "one.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"pool timeout"}}`,
	})
	var err error
	said := captureStderr(t, func() { _, err = captureRun(t, "serch", "kafka") })
	if !errors.Is(err, errAlreadySaid) {
		t.Errorf("err = %v, want the mistyped-command exit", err)
	}
	if n := strings.Count(said, "did you mean `deja search`?"); n != 1 {
		t.Errorf("want the suggestion once, got %d: %q", n, said)
	}
}

// --json is read by a script: no hint before the results or after them.
func TestJSONSearchGetsNoCommandHint(t *testing.T) {
	for _, args := range [][]string{
		{"serch", "--json", "pool"},
		{"--json", "serch", "pool"},
		{"serch", "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			hermeticEnv(t)
			t.Setenv("DEJA_STORES", "claude")
			writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "app", "one.jsonl"), "s1", []string{
				`{"type":"user","sessionId":"s1","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"serch pool"}}`,
			})
			var out string
			var err error
			said := captureStderr(t, func() { out, err = captureRun(t, args...) })
			if err != nil || !strings.Contains(out, "serch pool") {
				t.Fatalf("query did not search: stdout %q, err %v", out, err)
			}
			if strings.Contains(said, "did you mean") {
				t.Errorf("--json got a command hint: %q", said)
			}
		})
	}
}

func TestExplicitAndQuotedQueriesStillSearch(t *testing.T) {
	for _, args := range [][]string{
		{"search", "serch", "pool"},
		{"serch pool"},
		{"--json", "serch", "pool"},
		{"pool"},
		{"a", "pool"},
		{"-stats", "pool"},
		{"STATS"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			hermeticEnv(t)
			t.Setenv("DEJA_STORES", "claude")
			writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "app", "one.jsonl"), "s1", []string{
				`{"type":"user","sessionId":"s1","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"a serch pool stats"}}`,
			})
			out, err := captureRun(t, args...)
			if err != nil || !strings.Contains(out, "serch pool") {
				t.Fatalf("query did not search: stdout %q, err %v", out, err)
			}
		})
	}
}
