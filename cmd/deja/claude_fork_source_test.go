package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const forkSourceID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

// stubAncestors stands in for the processes above the hook.
func stubAncestors(t *testing.T, argv ...[]string) {
	t.Helper()
	old := ancestorArgs
	ancestorArgs = func(int) [][]string { return argv }
	t.Cleanup(func() { ancestorArgs = old })
}

// forkFirstPrompt is the fork's first prompt hook, its transcript not written
// yet, after a SessionStart when started is set.
func forkFirstPrompt(t *testing.T, started bool) string {
	t.Helper()
	dir, fork := forkStore(t, true)
	transcript := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p", fork+".jsonl")
	// Claude Code 2.1.302 writes the fork's copy after its first tool result.
	if err := os.Remove(transcript); err != nil {
		t.Fatal(err)
	}
	stubAncestors(t,
		[]string{"/bin/sh", "-c", "deja hook-context"},
		[]string{"/usr/local/bin/claude", "-p", "--resume", forkSourceID, "--fork-session", forkQuestion})
	if started {
		start := mustMarshal(t, map[string]string{"session_id": fork, "source": "fork", "transcript_path": transcript, "cwd": "/w/p", "hook_event_name": "SessionStart"})
		withHookStdin(t, string(start))
		if out := captureStdout(t, func() {
			if err := runHookContext(dir, false); err != nil {
				t.Error(err)
			}
		}); strings.TrimSpace(out) != "" {
			t.Errorf("a fork got a session-start digest:\n%s", out)
		}
	}
	payload := mustMarshal(t, map[string]string{"session_id": fork, "transcript_path": transcript, "cwd": "/w/p", "prompt": forkQuestion, "hook_event_name": "UserPromptSubmit"})
	var out strings.Builder
	if err := runHookPrompt(dir, strings.NewReader(string(payload)), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// `claude -p --resume <K> --fork-session` runs its first prompt hook before the
// fork's transcript exists, and SessionStart names no source: the recall led
// with K (#5128). The claude argv names K, and SessionStart keeps it.
func TestAClaudeForkSessionsFirstPromptLeavesOutItsSource(t *testing.T) {
	if out := forkFirstPrompt(t, false); !strings.Contains(out, "aaaaaaaa") {
		t.Fatalf("without SessionStart the fork was not handed its source, so this proves nothing:\n%s", out)
	}
	if out := forkFirstPrompt(t, true); strings.Contains(out, "aaaaaaaa") {
		t.Errorf("the fork's first prompt was handed its own source as history:\n%s", out)
	}
}

// The first tool hook sees the fork's transcript with three bookkeeping
// records and no copied turn; the lineage it leaves out has to hold the source
// from the mark alone (#5128).
func TestAClaudeForkSessionsFirstToolCountsItsSourceAsItself(t *testing.T) {
	dir, fork := forkStore(t, true)
	transcript := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p", fork+".jsonl")
	head := `{"type":"mode","sessionId":"` + fork + `"}` + "\n" +
		`{"type":"permission-mode","permissionMode":"default","sessionId":"` + fork + `"}` + "\n" +
		`{"type":"atis-latch","atis":"","sessionId":"` + fork + `"}` + "\n"
	if err := os.WriteFile(transcript, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	if askerLineage(dir, fork, transcript)[forkSourceID] {
		t.Fatal("the source counted as the fork before any mark, so this proves nothing")
	}
	stubAncestors(t, []string{"claude", "--resume", forkSourceID, "--fork-session"})
	rememberClaudeForkSource(dir, fork, transcript)
	if !askerLineage(dir, fork, transcript)[forkSourceID] {
		t.Errorf("the fork's first tool does not count its source as itself")
	}
	// Another session in the project is not the fork's.
	if askerLineage(dir, "someone-else", "")[forkSourceID] {
		t.Errorf("the mark leaked to another session")
	}
}

// A /branch writes the copy with forkedFrom before SessionStart, and the
// transcript is what names the source then: argv of a claude that was itself
// started as a fork names a different session.
func TestABranchKeepsNoMarkFromArgv(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "idx")
	transcript := filepath.Join(tmp, "f.jsonl")
	const branch = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	rec := `{"type":"user","uuid":"u-1","sessionId":"` + branch + `","cwd":"/w/p","timestamp":"2026-10-18T10:00:00Z","forkedFrom":{"sessionId":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","messageUuid":"u-1"},"message":{"role":"user","content":"hi"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	stubAncestors(t, []string{"claude", "--resume", forkSourceID, "--fork-session"})
	rememberClaudeForkSource(dir, branch, transcript)
	if got := claudeForkMark(dir, branch); got != "" {
		t.Errorf("a /branch was marked from argv as a fork of %s", got)
	}
}

func TestClaudeForkSourceFromArgv(t *testing.T) {
	tmp := t.TempDir()
	const self = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	older, newer := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	for i, id := range []string{older, newer, self} {
		p := filepath.Join(tmp, id+".jsonl")
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(time.Duration(i-3) * time.Minute)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	transcript := filepath.Join(tmp, self+".jsonl")
	sh := []string{"/bin/sh", "-c", "deja hook-prompt"}
	for _, c := range []struct {
		name string
		argv [][]string
		want string
	}{
		{"resume id", [][]string{sh, {"claude", "-p", "--resume", forkSourceID, "--fork-session", "q"}}, forkSourceID},
		{"short flag", [][]string{{"claude", "-r", forkSourceID, "--fork-session"}}, forkSourceID},
		{"equals", [][]string{{"claude", "--fork-session", "--resume=" + forkSourceID}}, forkSourceID},
		{"npm", [][]string{{"node", "/usr/lib/node_modules/@anthropic-ai/claude-code/cli.js", "-r", forkSourceID, "--fork-session"}}, forkSourceID},
		{"continue", [][]string{{"claude", "-c", "--fork-session"}}, newer},
		{"no fork", [][]string{{"claude", "--resume", forkSourceID}}, ""},
		{"picker", [][]string{{"claude", "--resume", "--fork-session"}}, ""},
		{"by name", [][]string{{"claude", "--resume", "billing work", "--fork-session"}}, ""},
		{"own id", [][]string{{"claude", "--resume", self, "--fork-session"}}, ""},
		{"outer claude", [][]string{sh, {"claude"}, {"zsh"}, {"claude", "--resume", forkSourceID, "--fork-session"}}, ""},
		{"unreadable", nil, ""},
	} {
		if got := claudeForkSourceIn(c.argv, self, transcript); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Expired marks go once the file grows, and a later mark for the same
// session is the one read.
func TestClaudeForkMarksStaySmall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "idx")
	old := "dddddddd-dddd-4ddd-8ddd-dddddddddddd " + forkSourceID + " " + strconv.FormatInt(time.Now().Add(-3*forkMarkTTL).Unix(), 10) + "\n"
	if err := os.WriteFile(forkMarksPath(dir), []byte(strings.Repeat(old, forkMarksPruneAt/len(old)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	const fork = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	const other = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	stubAncestors(t, []string{"claude", "--resume", forkSourceID, "--fork-session"})
	rememberClaudeForkSource(dir, fork, "")
	stubAncestors(t, []string{"claude", "--resume", other, "--fork-session"})
	rememberClaudeForkSource(dir, fork, "")
	b, _ := os.ReadFile(forkMarksPath(dir))
	if strings.Contains(string(b), "dddddddd") || strings.Count(string(b), "\n") != 2 {
		t.Errorf("expired marks were kept:\n%s", b)
	}
	if got := claudeForkMark(dir, fork); got != other {
		t.Errorf("mark = %q, want the later %q", got, other)
	}
}
