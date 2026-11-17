package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
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

// TestEveryCLICommandRejectsBlankInputsBeforeIndexing enforces the CLI
// boundary contract: blank, whitespace-only, empty-selector, or unexpected
// positional arguments must be rejected at parse time before opening or
// building the session index.
func TestEveryCLICommandRejectsBlankInputsBeforeIndexing(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "untouched-index")
	t.Setenv("DEJA_INDEX_DIR", idxDir)

	cases := [][]string{
		{"origin", ""},
		{"origin", "   "},
		{"origin", "--", ""},
		{"origin", "--", "   "},
		{"origin", "", "HEAD"},
		{"review", ""},
		{"review", "   "},
		{"review", "--", ""},
		{"review", "--", "   "},
		{"review", "", "HEAD"},
		{"repo", "off", "extra"},
		{"repo", "on", "extra"},
		{"repo", "disable", "--bogus"},
		{"repo", "enable", "--bogus"},
		{"repo", "add", ""},
		{"repo", "add", "   "},
		{"repo", "add", "--kind", "", "note"},
		{"repo", "add", "--kind=", "note"},
		{"repo", "add", "--kind=   ", "note"},
		{"repo", "add", "--kind=bogus", "note"},
		{"repo", "rm", ""},
		{"repo", "rm", "   "},
		{"sync", "forget", ""},
		{"sync", "forget", "   "},
		{"sync", "forget", "host1", "host2"},
		{"sync", "export", ""},
		{"sync", "export", "   "},
		{"sync", "export", "--peer", "", "out"},
		{"sync", "export", "--peer", "   ", "out"},
		{"sync", "export", "dir1", "dir2"},
		{"sync", "import", ""},
		{"sync", "import", "   "},
		{"sync", "ssh", ""},
		{"sync", "ssh", "   "},
		{"sync", "ssh", "", "host2"},
		{"share", ""},
		{"share", "   "},
		{"share", `""`},
		{"share", "''"},
		{"promote", ""},
		{"promote", "   "},
		{"promote", `""`},
		{"promote", "''"},
		{"handoff", ""},
		{"handoff", "   "},
		{"handoff", `""`},
		{"handoff", "''"},
		{"remember", ""},
		{"remember", "   "},
		{"remember", "", "second"},
		{"shell", ""},
		{"shell", "   "},
		{"shell", "-c", ""},
		{"shell", "-c", "   "},
		{"warmup-status", "extra"},
		{"warmup-status", "--bogus"},
		{"show", ""},
		{"show", "   "},
		{"show", `""`},
		{"show", "''"},
		{"ctx", ""},
		{"ctx", "   "},
		{"ctx", `""`},
		{"ctx", "''"},
		{"blame", ""},
		{"blame", "   "},
		{"recall", ""},
		{"recall", "   "},
		{"fix", ""},
		{"fix", "   "},
		{"how", ""},
		{"how", "   "},
		{"files", ""},
		{"files", "   "},
		{"restore", ""},
		{"restore", "   "},
		{"resume", ""},
		{"resume", "   "},
		{"resume", `""`},
		{"resume", "''"},
		{"delete", ""},
		{"delete", "   "},
		{"delete", `""`},
		{"delete", "''"},
	}

	for _, args := range cases {
		if err := run(args); err == nil {
			t.Errorf("run(%q) succeeded; want parse error before indexing", args)
		}
		if _, err := os.Stat(idxDir); !os.IsNotExist(err) {
			t.Fatalf("run(%q) touched index dir %s (stat err = %v)", args, idxDir, err)
		}
	}
}

// TestEveryCLICommandAcceptsFlagEqualsValueAndRejectsEmptyEquals verifies that
// every CLI command accepting value flags supports `--flag=value` (matching
// `--flag value`), rejects `--flag=` with empty value before indexing, and
// never silently swallows `--flag=value` typos or cross-command flags into
// search queries.
func TestEveryCLICommandAcceptsFlagEqualsValueAndRejectsEmptyEquals(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "untouched-equals-index")
	t.Setenv("DEJA_INDEX_DIR", idxDir)

	// 1. parseSearch must preserve literal `--flag=value` after `--`, reject
	// typos like `--harnses=codex`, reject cross-command flags like
	// `--before=30d`, and reject trailing `--bogus=1`.
	o, err := parseSearch([]string{"--", "--limit=5"})
	if err != nil || o.Query != "--limit=5" {
		t.Fatalf("parseSearch(-- --limit=5) = (%#v, %v), want Query='--limit=5'", o, err)
	}
	for _, bad := range [][]string{
		{"--harnses=codex", "retry"},
		{"--harnses=gemini", "retry"},
		{"--before=30d", "retry"},
		{"--settle=3f9a01c2", "retry"},
		{"goroutines", "--bogus=1"},
	} {
		if got, perr := parseSearch(bad); perr == nil {
			t.Fatalf("parseSearch(%q) succeeded with Query=%q; want flag error", bad, got.Query)
		}
	}

	// 2. Unit-testable flag parsers must produce identical options for
	// `--flag=value` and `--flag value` across multiple harnesses.
	for _, h := range []string{"codex", "gemini", "qwen", "opencode", "claude"} {
		s1, perr := parseShow([]string{"--harness=" + h, "--offset=2", "--limit=10", "019fa282"})
		if perr != nil || s1.harness != h || s1.offset != 2 || s1.limit != 10 || s1.id != "019fa282" {
			t.Fatalf("parseShow(--harness=%s) = (%#v, %v)", h, s1, perr)
		}
		n1, l1, sr1, perr := parseLast([]string{"--harness=" + h, "--project=app", "--since=7d", "--role=user", "--from=local", "5"})
		if perr != nil || n1 != 5 || l1.Harness != h || l1.Project != "app" || sr1 != "7d" || l1.Role != "user" || l1.From != "local" {
			t.Fatalf("parseLast(--harness=%s) = (%d, %#v, %q, %v)", h, n1, l1, sr1, perr)
		}
		bp, bo, _, perr := parseBlame([]string{"--harness=" + h, "--project=app", "--since=7d", "main.go"})
		if perr != nil || bp != "main.go" || bo.Harness != h || bo.Project != "app" || bo.Since == 0 {
			t.Fatalf("parseBlame(--harness=%s) = (%q, %#v, %v)", h, bp, bo, perr)
		}
		st, perr := parseStatsArgs([]string{"--harness=" + h, "--project=app", "--since=7d", "--role=user"})
		if perr != nil || st.options.Harness != h || st.options.Project != "app" || st.sinceRaw != "7d" || st.options.Role != "user" {
			t.Fatalf("parseStatsArgs(--harness=%s) = (%#v, %v)", h, st, perr)
		}
	}
	fo, err := parseFilesArgs([]string{"--project=app", "--limit=4", "auth"})
	if err != nil || fo.project != "app" || fo.limit != 4 || len(fo.terms) != 1 || fo.terms[0] != "auth" {
		t.Fatalf("parseFilesArgs(--flag=value) = (%#v, %v)", fo, err)
	}
	fg, err := parseForgetArgs([]string{"--session='019fa282'", "--project=app"})
	if err != nil || fg.o.Session != "019fa282" || fg.o.Project != "app" {
		t.Fatalf("parseForgetArgs(--flag=value) = (%#v, %v)", fg, err)
	}

	// 3. Every CLI command must reject empty `--flag=` before touching the index.
	emptyFlagCases := [][]string{
		{"search", "--limit=", "retry"},
		{"search", "--project=", "retry"},
		{"last", "--project="},
		{"last", "--harness="},
		{"show", "--harness=", "019fa282"},
		{"show", "--limit=", "019fa282"},
		{"blame", "--project=", "main.go"},
		{"how", "--limit=", "test"},
		{"how", "--project=", "test"},
		{"fix", "--limit=", "panic"},
		{"files", "--limit=", "auth"},
		{"files", "--project=", "auth"},
		{"restore", "--span=", "main.go"},
		{"restore", "-o=", "main.go"},
		{"restore", "--out=", "main.go"},
		{"friction", "--limit="},
		{"friction", "--settle="},
		{"promote", "--state=", "019fa282"},
		{"promote", "--note=", "019fa282"},
		{"promote", "--to=", "019fa282"},
		{"promote", "--tag=", "019fa282"},
		{"handoff", "--to="},
		{"remember", "--project=", "note"},
		{"remember", "--tag=", "note"},
		{"stats", "--project="},
		{"forget", "--session="},
		{"forget", "--project="},
		{"view", "--out="},
		{"rules", "candidates", "--limit="},
		{"rules", "candidates", "--project="},
		{"rules", "candidates", "--since="},
		{"repo", "candidates", "--limit="},
		{"repo", "candidates", "--since="},
		{"repo", "init", "--limit="},
		{"repo", "sync", "--limit="},
		{"sync", "export", "--peer=", "out"},
		{"recap", "--limit="},
		{"recap", "--since="},
		{"tests", "--limit="},
		{"secrets", "--limit="},
	}
	for _, args := range emptyFlagCases {
		rerr := run(args)
		if rerr == nil {
			t.Fatalf("run(%q) succeeded; want missing-value error", args)
		}
		if strings.Contains(rerr.Error(), "unknown flag") {
			t.Fatalf("run(%q) returned unknown flag error %q; want missing-value error", args, rerr.Error())
		}
		if _, statErr := os.Stat(idxDir); !os.IsNotExist(statErr) {
			t.Fatalf("run(%q) touched index dir %s", args, idxDir)
		}
	}
}

// TestEverySessionSelectorCommandUnwrapsPastedQuotes verifies that every
// session-selector CLI command (`show`, `ctx`, `share`, `promote`, `handoff`)
// unwraps single quotes, double quotes, and backticks around session IDs.
func TestEverySessionSelectorCommandUnwrapsPastedQuotes(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-work-app", "019fa282-quoted-test.jsonl"), "019fa282-quoted-test", []string{
		`{"type":"user","sessionId":"019fa282-quoted-test","cwd":"/work/app","timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"investigate selector quoting"}}`,
		`{"type":"assistant","sessionId":"019fa282-quoted-test","cwd":"/work/app","timestamp":"2026-03-01T10:01:00Z","message":{"role":"assistant","content":"unwrapped selector quotes cleanly"}}`,
	})
	idxDir := index.DefaultDir()
	if err := index.Ensure(idxDir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	tmp := t.TempDir()
	for _, wrapped := range []string{`'019fa282'`, `"019fa282"`, "`019fa282`", `'claude:019fa282'`} {
		var buf bytes.Buffer
		if err := runShare(idxDir, []string{wrapped}, &buf); err != nil {
			t.Fatalf("runShare(%s) error: %v", wrapped, err)
		}
		buf.Reset()
		if err := runHandoff(idxDir, []string{"--to=claude", wrapped}, &buf); err != nil {
			t.Fatalf("runHandoff(%s) error: %v", wrapped, err)
		}
		outPath := filepath.Join(tmp, "promoted.md")
		buf.Reset()
		if err := runPromote(idxDir, []string{"--to=" + outPath, wrapped}, &buf); err != nil {
			t.Fatalf("runPromote(%s) error: %v", wrapped, err)
		}
	}
}

// TestTestsJSONAndTextRespectLimitParity verifies that `deja tests --json --limit N`
// applies the same `--limit` cap to `repeats` as text output.
func TestTestsJSONAndTextRespectLimitParity(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-work-app", "s1.jsonl"), "s1",
		testRunLines("s1", "2026-03-01", "c1", "go test ./...", "--- FAIL: TestAlpha (0.01s)\n--- FAIL: TestBeta (0.01s)\nFAIL\tapp\t0.02s\nFAIL"))
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-work-app", "s2.jsonl"), "s2",
		testRunLines("s2", "2026-03-02", "c2", "go test ./...", "--- FAIL: TestAlpha (0.01s)\n--- FAIL: TestBeta (0.01s)\nFAIL\tapp\t0.02s\nFAIL"))
	idxDir := index.DefaultDir()
	if err := index.Ensure(idxDir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runTests(idxDir, []string{"--json", "--limit=1"}, &buf); err != nil {
		t.Fatalf("runTests(--json --limit=1) error: %v", err)
	}
	var parsed testsJSON
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal testsJSON: %v", err)
	}
	if len(parsed.Repeats) != 1 {
		t.Fatalf("len(parsed.Repeats) = %d with --limit=1, want 1", len(parsed.Repeats))
	}
}

// TestDoctorBoundedStoreErrorPreservesValidUTF8 verifies that boundedStoreError
// never slices mid-rune on multi-byte UTF-8 input.
func TestDoctorBoundedStoreErrorPreservesValidUTF8(t *testing.T) {
	for pad := storeErrorMax - 5; pad <= storeErrorMax+5; pad++ {
		msg := strings.Repeat("a", pad) + "ошибка базы данных sqlite3"
		got := boundedStoreError(msg)
		if !utf8.ValidString(got) {
			t.Fatalf("boundedStoreError produced invalid UTF-8 at pad=%d: %q", pad, got)
		}
	}
}

// TestDoctorAndRuntimeShareEnvResolvers verifies that doctor and runtime
// commands consult the same environment helpers for DEJA_EMBED / DEJA_EMBED_OFF
// and DEJA_AIDER_ROOTS.
func TestDoctorAndRuntimeShareEnvResolvers(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "idx")
	t.Setenv("DEJA_INDEX_DIR", idxDir)

	t.Setenv("DEJA_EMBED_URL", "")
	t.Setenv("DEJA_EMBED_OFF", "")
	t.Setenv("DEJA_EMBED", " OFF ")
	if !embed.Disabled() || !embed.Off() || !embedOff() {
		t.Fatal("DEJA_EMBED=' OFF ' must switch off embed.Disabled(), embed.Off(), and embedOff()")
	}
	if rep := collectDoctorEmbed(idxDir); rep != nil {
		t.Fatalf("collectDoctorEmbed = %#v with DEJA_EMBED=' OFF ', want nil", rep)
	}

	r1 := filepath.Join(tmp, "aider-one")
	r2 := filepath.Join(tmp, "aider-two")
	sep := string(os.PathListSeparator)
	t.Setenv("DEJA_AIDER_ROOTS", sep+" "+r1+" "+sep+sep+r2+sep)

	gotRoots := sources.AiderRoots()
	if len(gotRoots) != 2 || gotRoots[0] != r1 || gotRoots[1] != r2 {
		t.Fatalf("sources.AiderRoots() = %#v, want [%q, %q]", gotRoots, r1, r2)
	}
	loc := doctorAiderLocation()
	wantSuffix := sep + r1 + sep + r2
	if !strings.HasSuffix(loc, wantSuffix) {
		t.Fatalf("doctorAiderLocation() = %q, want suffix %q", loc, wantSuffix)
	}
}

func TestHookPromptToolAfterSessionEndCompletion(t *testing.T) {
	scripts := map[string]string{}
	for _, shell := range completionTestShells {
		scripts[shell] = emittedCompletion(t, shell)
	}
	for _, tc := range []struct {
		cmd   string
		flags []string
	}{
		{"hook-prompt", []string{"--plain", "--copilot", "--strict", "--mcode"}},
		{"hook-tool-after", []string{"--plain", "--copilot", "--mcode"}},
		{"hook-session-end", []string{"--json"}},
	} {
		for _, shell := range completionTestShells {
			block := shellCommandBlock(shell, scripts[shell], tc.cmd)
			for _, flag := range tc.flags {
				if !strings.Contains(block, flag) {
					t.Errorf("%s completion for %q does not offer %s:\n%s", shell, tc.cmd, flag, block)
				}
			}
			// None of these commands should fall through to search's --re flag.
			if strings.Contains(block, "--re") {
				t.Errorf("%s completion for %q fell through to search flags:\n%s", shell, tc.cmd, block)
			}
		}
	}
}

func TestSearchAndStatsRoleAndHarnessCompletion(t *testing.T) {
	roles := strings.Join(knownRoles, " ")
	bash := emittedCompletion(t, "bash")
	for _, cmd := range []string{"stats", "search"} {
		block := shellCommandBlock("bash", bash, cmd)
		if !strings.Contains(block, `"$prev" == "--role"`) || !strings.Contains(block, roles) {
			t.Errorf("bash completion for %q does not complete --role values:\n%s", cmd, block)
		}
	}
	ps := emittedCompletion(t, "powershell")
	psSearch := shellCommandBlock("powershell", ps, "search")
	for _, want := range []string{`$previous -eq '--harness'`, `$harnesses`, `$previous -eq '--role'`, `$roles`} {
		if !strings.Contains(psSearch, want) {
			t.Errorf("powershell completion for search missing %s:\n%s", want, psSearch)
		}
	}
}

func TestEmptyFlagValuesRefused(t *testing.T) {
	tmp := t.TempDir()
	for _, tc := range []struct {
		name string
		run  func() error
		want string
	}{
		{"remember --tag empty", func() error { return runRemember(tmp, []string{"--tag", "", "some note"}) }, "remember: --tag needs value"},
		{"remember --tag spaces", func() error { return runRemember(tmp, []string{"--tag", "   ", "some note"}) }, "remember: --tag needs value"},
		{"promote --tag empty", func() error { return runPromote(tmp, []string{"--tag", "", "abc"}, &bytes.Buffer{}) }, "promote: --tag needs a value"},
		{"promote --tag spaces", func() error { return runPromote(tmp, []string{"--tag", "  ", "abc"}, &bytes.Buffer{}) }, "promote: --tag needs a value"},
		{"promote --note empty", func() error { return runPromote(tmp, []string{"--note", "", "abc"}, &bytes.Buffer{}) }, "promote: --note needs text"},
		{"promote --note spaces", func() error { return runPromote(tmp, []string{"--note", "  ", "abc"}, &bytes.Buffer{}) }, "promote: --note needs text"},
		{"promote --to empty", func() error { return runPromote(tmp, []string{"--to", "", "abc"}, &bytes.Buffer{}) }, "promote: --to needs a path"},
		{"promote --to spaces", func() error { return runPromote(tmp, []string{"--to", "  ", "abc"}, &bytes.Buffer{}) }, "promote: --to needs a path"},
		{"view --out empty", func() error { return runView(tmp, []string{"--out", ""}) }, "view: --out needs a path"},
		{"view --out spaces", func() error { return runView(tmp, []string{"--out", "  "}) }, "view: --out needs a path"},
		{"restore -o empty", func() error { return runRestore(tmp, []string{"-o", "", "file.go"}, &bytes.Buffer{}) }, "restore: -o needs value"},
		{"restore --out spaces", func() error { return runRestore(tmp, []string{"--out", "  ", "file.go"}, &bytes.Buffer{}) }, "restore: -o needs value"},
	} {
		err := tc.run()
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestDashDashTreatsRemainingWordsAsPositional(t *testing.T) {
	tmp := t.TempDir()
	for _, args := range [][]string{
		{"--", "-first", "--bogus"},
		{"--", "first", "--repo"},
	} {
		err := runRemember(tmp, args)
		if err == nil || !strings.Contains(err.Error(), "expected one text argument") {
			t.Errorf("runRemember(%v): got %v, want 'expected one text argument'", args, err)
		}
	}
	for _, args := range [][]string{
		{"--", "file.go", "--bogus"},
		{"--", "file.go", "--json"},
	} {
		_, _, _, err := parseBlame(args)
		if err == nil || err.Error() != "blame accepts one path" {
			t.Errorf("parseBlame(%v): got %v, want 'blame accepts one path'", args, err)
		}
	}
}

func TestNegativeLimitErrorMessageNamesZeroFloor(t *testing.T) {
	tmp := t.TempDir()
	for _, tc := range []struct {
		name string
		run  func() error
		want string
	}{
		{"recap", func() error { return runRecap(tmp, []string{"--limit", "-1"}, &bytes.Buffer{}) }, `recap: --limit wants a number from 0 up, got "-1"`},
		{"tests", func() error { return runTests(tmp, []string{"--limit", "-1"}, &bytes.Buffer{}) }, `tests: --limit wants a number from 0 up, got "-1"`},
		{"secrets", func() error { return runSecrets(tmp, []string{"--limit", "-1"}, &bytes.Buffer{}) }, `secrets: --limit wants a number from 0 up, got "-1"`},
	} {
		err := tc.run()
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestRecapJSONHonorsLimit(t *testing.T) {
	tmp := hermeticEnv(t)
	indexDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", indexDir)

	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-recap-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, id := range []string{"r1", "r2", "r3"} {
		ts := now.Add(-time.Duration(i+1) * time.Hour).Format(time.RFC3339)
		lines := []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/recap-proj","timestamp":"` + ts + `","message":{"role":"user","content":"investigate recap session ` + id + `"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/recap-proj","timestamp":"` + ts + `","message":{"role":"assistant","content":"Decision: keep recap session ` + id + ` deterministic."}}`,
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(indexDir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runRecap(indexDir, []string{"--since", "7d", "--limit", "1", "--json"}, &buf); err != nil {
		t.Fatalf("runRecap: %v", err)
	}
	var out recapJSON
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal recapJSON: %v", err)
	}
	if out.Considered != 3 {
		t.Errorf("sessions_in_window = %d, want 3", out.Considered)
	}
	if len(out.Sessions) != 1 {
		t.Errorf("len(sessions) = %d, want 1", len(out.Sessions))
	}
}

// On Windows without Git Bash CodeBuddy runs the plugin's shell scripts
// through PowerShell, which runs none of them; doctor has to say so and name
// the install that works (#4753).
func TestDoctorSaysTheCodeBuddyPluginNeedsGitBashOnWindows(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CODEBUDDY_CONFIG_DIR", cfg)
	t.Setenv("CODEBUDDY_CODE_GIT_BASH_PATH", "")
	writeSettings := func(s string) {
		if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitDir := t.TempDir()
	lookPath := map[string]string{}
	oldGOOS, oldLook := codeBuddyGOOS, codeBuddyLookPath
	t.Cleanup(func() { codeBuddyGOOS, codeBuddyLookPath = oldGOOS, oldLook })
	codeBuddyLookPath = func(name string) (string, error) {
		if p, ok := lookPath[name]; ok {
			return p, nil
		}
		return "", errors.New("not found")
	}

	codeBuddyGOOS = "windows"
	writeSettings(`{"enabledPlugins":{"deja-vu@some-market":true}}`)
	if note := codeBuddyPluginNote(); !strings.Contains(note, "deja install codebuddy-auto") {
		t.Fatalf("plugin on, no Git Bash: note %q", note)
	}

	// Controls: each one condition away from the broken machine says nothing.
	codeBuddyGOOS = "darwin"
	if note := codeBuddyPluginNote(); note != "" {
		t.Errorf("not Windows: note %q", note)
	}
	codeBuddyGOOS = "windows"
	writeSettings(`{"enabledPlugins":{"deja-vu@some-market":false,"other@x":true}}`)
	if note := codeBuddyPluginNote(); note != "" {
		t.Errorf("plugin off: note %q", note)
	}
	writeSettings(`{"enabledPlugins":{"deja-vu@some-market":true}}`)
	// Git for Windows: cmd\git.exe with bin\bash.exe beside it.
	if err := os.MkdirAll(filepath.Join(gitDir, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(gitDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "bin", "bash.exe"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	lookPath["git"] = filepath.Join(gitDir, "cmd", "git.exe")
	if note := codeBuddyPluginNote(); note != "" {
		t.Errorf("Git Bash beside git: note %q", note)
	}
	delete(lookPath, "git")
	// WSL's bash on PATH is not Git Bash.
	lookPath["bash"] = `C:\Windows\System32\bash.exe`
	if note := codeBuddyPluginNote(); note == "" {
		t.Errorf("only WSL bash: no note")
	}
	t.Setenv("CODEBUDDY_CODE_GIT_BASH_PATH", filepath.Join(gitDir, "bin", "bash.exe"))
	if note := codeBuddyPluginNote(); note != "" {
		t.Errorf("override set: note %q", note)
	}
}

func codebuddyHookCommand(t *testing.T, entry any) any {
	t.Helper()
	group, _ := entry.(map[string]any)
	hooks, _ := group["hooks"].([]any)
	if len(hooks) == 0 {
		t.Fatalf("no hooks in %v", entry)
	}
	h, ok := hooks[0].(map[string]any)
	if !ok {
		t.Fatalf("hook is not an object: %v", hooks[0])
	}
	return h["command"]
}

// hookEchoEnv turns the test binary into a stand-in for deja: it prints its
// arguments on one line and then whatever came in on stdin, and exits 0, or
// with the code in hookEchoExitEnv.
const (
	hookEchoEnv     = "DEJA_TEST_HOOK_ECHO"
	hookEchoExitEnv = "DEJA_TEST_HOOK_EXIT"
)

func hookEcho() {
	in, _ := io.ReadAll(os.Stdin)
	_, _ = os.Stdout.WriteString(strings.Join(os.Args[1:], " ") + "\n")
	_, _ = os.Stdout.Write(in)
	code, _ := strconv.Atoi(os.Getenv(hookEchoExitEnv))
	os.Exit(code)
}

// cbParseCommandLine is CodeBuddy's parseCommandLine (2.161.3): split on
// blanks, double quotes group, and `\"` or "`\"" inside them is a quote.
func cbParseCommandLine(s string) (string, []string, error) {
	var out []string
	var cur strings.Builder
	inQ, have := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQ {
			if (c == '`' || c == '\\') && i+1 < len(s) && s[i+1] == '"' {
				cur.WriteByte('"')
				i++
				continue
			}
			if c == '"' {
				inQ = false
				continue
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case '"':
			inQ, have = true, true
		case ' ', '\t':
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteByte(c)
			have = true
		}
	}
	if inQ {
		return "", nil, errors.New("unterminated quote")
	}
	if have {
		out = append(out, cur.String())
	}
	if len(out) == 0 {
		return "", nil, errors.New("empty")
	}
	return out[0], out[1:], nil
}

// cbIsPowerShell is CodeBuddy's isPowerShellExecutable.
func cbIsPowerShell(exe string) bool {
	if i := strings.LastIndexAny(exe, `/\`); i >= 0 {
		exe = exe[i+1:]
	}
	switch strings.ToLower(exe) {
	case "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return true
	}
	return false
}

// The line deja writes for CodeBuddy on Windows when the path has a space, and
// how CodeBuddy's hook executor takes it apart: powershell as the program, and
// the inner command as one argument. Elsewhere, and for a plain path, the line
// is the one every other harness gets (#4728).
func TestCodeBuddyWindowsHookLineForAPathWithASpace(t *testing.T) {
	exe := `C:\Users\First Last\AppData\Local\deja\deja.exe`
	line := codeBuddyHookRun("windows", exe, "hook-context")
	want := `powershell -NoProfile -Command "& 'C:/Users/First Last/AppData/Local/deja/deja.exe' hook-context; exit (Get-Variable LASTEXITCODE -ValueOnly)"`
	if line != want {
		t.Fatalf("line\n got %s\nwant %s", line, want)
	}
	prog, args, err := cbParseCommandLine(line)
	if err != nil || !cbIsPowerShell(prog) {
		t.Fatalf("CodeBuddy would not spawn this directly: %q %q %v", prog, args, err)
	}
	if got := args[len(args)-1]; got != `& 'C:/Users/First Last/AppData/Local/deja/deja.exe' hook-context; exit (Get-Variable LASTEXITCODE -ValueOnly)` {
		t.Errorf("the command PowerShell gets: %q", got)
	}

	if got := codeBuddyHookRun("windows", `C:\Users\O'Brien Smith\deja.exe`, "hook-prompt"); !strings.Contains(got, `'C:/Users/O''Brien Smith/deja.exe'`) {
		t.Errorf("a quote in the path is not doubled for PowerShell: %s", got)
	}
	for exe, want := range map[string]string{
		`C:\Users\me\deja.exe`:     `C:/Users/me/deja.exe hook-prompt`,
		`C:\Users\a $b\deja.exe`:   `"C:/Users/a $b/deja.exe" hook-prompt`,
		`/Users/A B/bin/deja`:      hookCommandQuoteFor("darwin", `/Users/A B/bin/deja`) + " hook-prompt",
		`/usr/local/bin/deja-hook`: `/usr/local/bin/deja-hook hook-prompt`,
	} {
		goos := "windows"
		if strings.HasPrefix(exe, "/") {
			goos = "darwin"
		}
		if got := codeBuddyHookRun(goos, exe, "hook-prompt"); got != want {
			t.Errorf("%s on %s: got %s, want %s", exe, goos, got, want)
		}
	}
	if runtime.GOOS != "windows" {
		if got, want := codeBuddyHookRun(runtime.GOOS, "/Users/A B/bin/deja", "hook-prompt"), hookRun("/Users/A B/bin/deja", "hook-prompt"); got != want {
			t.Errorf("the %s line changed: %s, was %s", runtime.GOOS, got, want)
		}
	}
}

// An entry written before as `"C:/…/deja.exe" hook-context` is deja's own: an
// install takes it over with the new line instead of adding a second hook
// beside it, and an uninstall takes the new line out.
func TestCodeBuddyWindowsHookLineReplacesTheQuotedOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	exe := `C:\Users\First Last\AppData\Local\deja\deja.exe`
	old := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"\"C:/Users/First Last/AppData/Local/deja/deja.exe\" hook-context","timeout":60}]}]}}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	line := codeBuddyHookRun("windows", exe, "hook-context")
	if _, err := installSettingsHookCmd(path, "SessionStart", "", 60, line, false); err != nil {
		t.Fatal(err)
	}
	cfg := readJSONConfig(path)
	entries, _ := cfg["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(entries) != 1 {
		t.Fatalf("%d SessionStart entries, want 1: %v", len(entries), entries)
	}
	if got := codebuddyHookCommand(t, entries[0]); got != line {
		t.Fatalf("command %q, want %q", got, line)
	}
	// From another install path: still ours, still one entry.
	moved := codeBuddyHookRun("windows", `D:\Tools Dir\deja.exe`, "hook-context")
	if _, err := installSettingsHookCmd(path, "SessionStart", "", 60, moved, false); err != nil {
		t.Fatal(err)
	}
	entries, _ = readJSONConfig(path)["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(entries) != 1 || codebuddyHookCommand(t, entries[0]) != moved {
		t.Fatalf("after a move: %v", entries)
	}
	if _, err := installSettingsHookCmd(path, "SessionStart", "", 60, moved, true); err != nil {
		t.Fatal(err)
	}
	if hooks, _ := readJSONConfig(path)["hooks"].(map[string]any); len(hooks) != 0 {
		t.Fatalf("uninstall left %v", hooks)
	}
	// Somebody's own line that runs deja's hook among other things is still
	// theirs.
	wrapper := `powershell -NoProfile -Command "& 'C:/x y/deja.exe' hook-context; Write-Host done"`
	if kind := hookCommandKindOf(wrapper, line); kind != hookWrapsDejas {
		t.Errorf("a wrapper read as %v", kind)
	}
}

// What CodeBuddy actually does with the line on Windows, run for real against
// a stand-in deja whose path has a space: spawned directly when its first word
// is powershell (2.161), handed to PowerShell -Command when not and there is
// no Git Bash, and to Git Bash when there is one. The payload carries non-ASCII
// text both ways, as a prompt does. Before #4728 the first two failed with
// PowerShell's "Unexpected token 'hook-context'".
//
// And a failing deja: CodeBuddy acts on a hook's exit code, and PowerShell
// turns any failed native command into 1. Where CodeBuddy spawns the line, or
// Git Bash runs it, the code deja exited with is the one it sees. An older
// CodeBuddy that wraps the line in a second PowerShell flattens it again, out
// of deja's reach; there it only has to stay a failure.
func TestCodeBuddyHookLineRunsInEveryWindowsShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("CodeBuddy's Windows hook executor")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "First Last")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "deja.exe")
	copyFile(t, self, exe)

	sub := "hook-context"
	line := codeBuddyHookRun(runtime.GOOS, exe, sub)
	payload := `{"prompt":"修复 the build — café","hook_event_name":"SessionStart"}`
	want := sub + "\n" + payload

	type run struct {
		name  string
		argv  []string
		exact bool // the exit code passes through unchanged
	}
	var runs []run
	// CodeBuddy 2.161: tryBuildDirectPowerShellHookCommand, else the shell.
	if prog, args, err := cbParseCommandLine(line); err == nil && cbIsPowerShell(prog) {
		args = append(append([]string{}, args[:len(args)-2]...), append([]string{"-NonInteractive", "-WindowStyle", "Hidden"}, args[len(args)-2:]...)...)
		runs = append(runs, run{"CodeBuddy, spawned directly", append([]string{prog}, args...), true})
	} else {
		runs = append(runs, run{"CodeBuddy, no Git Bash", []string{"powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", line}, true})
	}
	// An older CodeBuddy without the direct spawn, and no Git Bash.
	runs = append(runs, run{"PowerShell -Command", []string{"powershell", "-NoProfile", "-NonInteractive", "-Command", line}, false})
	if _, err := exec.LookPath("pwsh"); err == nil {
		runs = append(runs, run{"pwsh -Command", []string{"pwsh", "-NoProfile", "-NonInteractive", "-Command", line}, false})
	}
	if bash := gitBashPath(); bash != "" {
		runs = append(runs, run{"Git Bash", []string{bash, "-c", line}, true})
	} else {
		t.Log("no Git Bash on this machine; the bash leg is not run")
	}
	for _, r := range runs {
		c := exec.CommandContext(t.Context(), r.argv[0], r.argv[1:]...)
		c.Env = append(os.Environ(), hookEchoEnv+"=1")
		c.Stdin = strings.NewReader(payload)
		var stderr strings.Builder
		c.Stderr = &stderr
		out, err := c.Output()
		got := strings.ReplaceAll(string(out), "\r\n", "\n")
		if err != nil || got != want {
			t.Errorf("%s ran %q:\n err %v\n out %q\nwant %q\n stderr %s", r.name, line, err, got, want, stderr.String())
		}

		c = exec.CommandContext(t.Context(), r.argv[0], r.argv[1:]...)
		c.Env = append(os.Environ(), hookEchoEnv+"=1", hookEchoExitEnv+"=3")
		c.Stdin = strings.NewReader(payload)
		err = c.Run()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Errorf("%s: %v", r.name, err)
			continue
		}
		if r.exact && code != 3 {
			t.Errorf("%s: deja exited 3, CodeBuddy sees %d", r.name, code)
		} else if code == 0 {
			t.Errorf("%s: deja exited 3, CodeBuddy sees success", r.name)
		}
	}
}

// gitBashPath is Git for Windows' bash, which is what CodeBuddy looks for; the
// bash.exe in System32 is WSL's and is not it.
func gitBashPath() string {
	for _, p := range []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe"),
		filepath.Join(os.Getenv("ProgramW6432"), "Git", "bin", "bash.exe"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o755); err != nil {
		t.Fatal(err)
	}
}

// Codex pins each approved hook in config.toml by its place in hooks.json. An
// uninstall left the pins for deja's hooks behind, so the file did not come
// back and a reinstall started trusted without a review (#4183). The reader's
// own hook moves up when deja's goes, and its pin has to move with it.
func TestUninstallTakesCodexTrustPinsWithTheHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("DEJA_CODEX_ROOT", "")
	codex := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codex, 0o755); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(codex, "hooks.json")
	cfgPath := filepath.Join(codex, "config.toml")
	cfg := "# mine\nmodel = \"luna\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	// The reader adds a SessionStart hook of their own after deja's, and
	// codex pins all of them once the user approves.
	b, _ := os.ReadFile(hooksPath)
	mine := `{"hooks":[{"type":"command","command":"~/bin/mine.sh"}]}`
	withMine := strings.Replace(string(b), `"SessionStart": [`, `"SessionStart": [`+"\n      "+mine+",", 1)
	if withMine == string(b) {
		t.Fatalf("could not place the reader's hook:\n%s", b)
	}
	// Theirs first, deja's second.
	if err := os.WriteFile(hooksPath, []byte(withMine), 0o600); err != nil {
		t.Fatal(err)
	}
	pin := func(event string, g int) string {
		return "\n[hooks.state." + strconv.Quote(hooksPath+":"+event+":"+strconv.Itoa(g)+":0") + "]\ntrusted_hash = \"sha256:" + event + strconv.Itoa(g) + "\"\n"
	}
	approved := cfg + "\n[hooks.state]\n" + pin("session_start", 0) + pin("session_start", 1) + pin("user_prompt_submit", 0) + pin("pre_tool_use", 0) + pin("post_tool_use", 0) + pin("pre_compact", 0)
	if err := os.WriteFile(cfgPath, []byte(approved), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installCodexHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cfgPath)
	want := cfg + "\n[hooks.state]\n" + pin("session_start", 0)
	if string(got) != want {
		t.Errorf("after uninstall config.toml is\n%s\nwant\n%s", got, want)
	}

	// A table codex writes after the pins keeps the blank line in front of it.
	if err := os.WriteFile(hooksPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	tail := "\n[tui.model_availability_nux]\n\"gpt-5.6-sol\" = 1\n"
	approved = cfg + "\n[projects.\"/w\"]\ntrust_level = \"trusted\"\n\n[hooks.state]\n" + pin("session_start", 0) + pin("pre_compact", 0) + tail
	if err := os.WriteFile(cfgPath, []byte(approved), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if got, want := readString(t, cfgPath), cfg+"\n[projects.\"/w\"]\ntrust_level = \"trusted\"\n"+tail; got != want {
		t.Errorf("after uninstall config.toml is\n%q\nwant\n%q", got, want)
	}

	// And with nothing of the reader's left, the file comes back as it was.
	if err := os.WriteFile(hooksPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	approved = cfg + "\n[hooks.state]\n" + pin("session_start", 0) + pin("pre_compact", 0)
	if err := os.WriteFile(cfgPath, []byte(approved), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(cfgPath); string(got) != cfg {
		t.Errorf("after uninstall config.toml is\n%q\nwant\n%q", got, cfg)
	}
}

func readString(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The shapes a review found broken in the first cut of #4183.
func TestRewriteCodexHookTrustKeepsWhatIsNotAPin(t *testing.T) {
	file := "/h/.codex/hooks.json"
	paths := map[string]bool{file: true}
	pin := func(pos string, extra string) string {
		return "[hooks.state." + strconv.Quote(file+":"+pos) + "]" + extra + "\ntrusted_hash = \"" + pos + "\"\n"
	}
	gone := map[codexHookPos]*codexHookPos{{"session_start", 0, 0}: nil}
	up := &codexHookPos{"session_start", 0, 0}

	// A comment above the next table stays with it.
	in := "model = \"x\"\n\n[hooks.state]\n\n" + pin("session_start:0:0", "") + "\n# my work profile\n[profiles.work]\nmodel = \"y\"\n"
	if got, want := rewriteCodexHookTrust(in, paths, gone), "model = \"x\"\n\n# my work profile\n[profiles.work]\nmodel = \"y\"\n"; got != want {
		t.Errorf("comment above the next table:\ngot  %q\nwant %q", got, want)
	}

	// A move onto a key a stale pin already holds does not write it twice.
	moves := map[codexHookPos]*codexHookPos{{"session_start", 0, 0}: nil, {"session_start", 1, 0}: up}
	in = "[hooks.state]\n\n" + pin("session_start:0:0", "") + "\n" + pin("session_start:1:0", "") + "\n" + pin("session_start:0:0", "")
	got := rewriteCodexHookTrust(in, paths, moves)
	if n := strings.Count(got, strconv.Quote(file+":session_start:0:0")); n != 1 {
		t.Errorf("key written %d times:\n%s", n, got)
	}
	if !strings.Contains(got, `trusted_hash = "session_start:1:0"`) {
		t.Errorf("the moved pin was lost:\n%s", got)
	}

	// A literal-string key — codex's form for a path with a backslash — is
	// read, and a renamed one is written back the same way with its comment.
	win := `C:\Users\x\.codex\hooks.json`
	lit := "[hooks.state.'" + win + ":session_start:1:0'] # mine\r\ntrusted_hash = \"b\"\r\n"
	got = rewriteCodexHookTrust(lit, map[string]bool{win: true}, map[codexHookPos]*codexHookPos{{"session_start", 1, 0}: up})
	if want := "[hooks.state.'" + win + ":session_start:0:0'] # mine\r\ntrusted_hash = \"b\"\r\n"; got != want {
		t.Errorf("literal key:\ngot  %q\nwant %q", got, want)
	}
}

// Matching positions stays linear in the hooks: an insertion sort inside the
// loop made 1,500 hooks take seconds.
func TestCodexTrustMovesIsFastOnALongHooksFile(t *testing.T) {
	groups := make([]any, 0, 3000)
	for i := range 3000 {
		groups = append(groups, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "c" + strconv.Itoa(i)}}})
	}
	before := map[string]any{"hooks": map[string]any{"SessionStart": groups}}
	after := map[string]any{"hooks": map[string]any{"SessionStart": groups[1:]}}
	start := time.Now()
	moves := codexTrustMoves(before, after)
	if took := time.Since(start); took > time.Second {
		t.Errorf("3000 hooks took %v", took)
	}
	if to := moves[codexHookPos{"session_start", 2999, 0}]; to == nil || to.group != 2998 {
		t.Errorf("last hook moved to %v, want group 2998", to)
	}
}

// Text inside a multi-line string is not a header, and a comment at the end
// of the file is not part of the last pin.
func TestRewriteCodexHookTrustReadsTOMLNotLines(t *testing.T) {
	file := "/h/hooks.json"
	paths := map[string]bool{file: true}
	moves := map[codexHookPos]*codexHookPos{{"session_start", 0, 0}: nil, {"session_start", 1, 0}: {"session_start", 0, 0}}
	in := "[profile]\ninstr = \"\"\"\n[hooks.state.\"/h/hooks.json:session_start:0:0\"]\n\"\"\"\n"
	if got := rewriteCodexHookTrust(in, paths, moves); got != in {
		t.Errorf("a line inside a multi-line string was read as a pin:\n%q", got)
	}

	in = "model = \"x\"\n\n[hooks.state.\"/h/hooks.json:session_start:0:0\"]\ntrusted_hash = \"x\"\n# keep me at eof\n"
	if got, want := rewriteCodexHookTrust(in, paths, moves), "model = \"x\"\n# keep me at eof\n"; got != want {
		t.Errorf("comment at the end of the file:\ngot  %q\nwant %q", got, want)
	}

	if got := quoteTOMLKey("a\x7fb\"c\\d", false); got != `"a\u007Fb\"c\\d"` {
		t.Errorf("TOML quoting: %s", got)
	}
}

// An install that drops a second copy of deja's entry moves the reader's hook
// after it up a place, and the pin at that place is the dropped copy's. The
// pins have to follow on install as they do on uninstall (#4227).
func TestInstallDroppingADuplicateMovesTheReadersPin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("DEJA_CODEX_ROOT", "")
	codex := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codex, 0o755); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(codex, "hooks.json")
	cfgPath := filepath.Join(codex, "config.toml")
	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(readString(t, hooksPath)), &root); err != nil {
		t.Fatal(err)
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("no hooks in %s", hooksPath)
	}
	ups, ok := hooks["UserPromptSubmit"].([]any)
	if !ok || len(ups) == 0 {
		t.Fatalf("no UserPromptSubmit hooks in %s", hooksPath)
	}
	mine := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "~/bin/mine.sh"}}}
	hooks["UserPromptSubmit"] = []any{ups[0], ups[0], mine}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(hooksPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	pin := func(g int, hash string) string {
		return "\n[hooks.state." + strconv.Quote(hooksPath+":user_prompt_submit:"+strconv.Itoa(g)+":0") + "]\ntrusted_hash = \"sha256:" + hash + "\"\n"
	}
	cfg := "model = \"luna\"\n\n[hooks.state]\n"
	if err := os.WriteFile(cfgPath, []byte(cfg+pin(0, "deja")+pin(1, "copy")+pin(2, "mine")), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got, want := readString(t, cfgPath), cfg+pin(0, "deja")+pin(1, "mine"); got != want {
		t.Errorf("after install config.toml is\n%s\nwant\n%s", got, want)
	}
}

// The Codex marketplace rejects the whole file on an unknown enum, so a typo
// here means `codex plugin marketplace add` fails for everyone.
func TestCodexMarketplaceManifest(t *testing.T) {
	var market struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source struct {
				Source string `json:"source"`
				Path   string `json:"path"`
			} `json:"source"`
			Policy struct {
				Installation   string `json:"installation"`
				Authentication string `json:"authentication"`
			} `json:"policy"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(repoFile(t, ".agents/plugins/marketplace.json"), &market); err != nil {
		t.Fatalf("marketplace.json: %v", err)
	}
	if len(market.Plugins) != 1 {
		t.Fatalf("want exactly one plugin, got %d", len(market.Plugins))
	}
	p := market.Plugins[0]
	// Paths resolve against the repository root, not the manifest directory.
	if p.Source.Path != "./codex-plugin" {
		t.Fatalf("source path = %q", p.Source.Path)
	}
	// Codex only accepts ON_INSTALL or ON_USE here; NONE is rejected outright.
	if p.Policy.Authentication != "ON_USE" && p.Policy.Authentication != "ON_INSTALL" {
		t.Fatalf("authentication policy %q is not one Codex accepts", p.Policy.Authentication)
	}
}

func TestCodexPluginManifest(t *testing.T) {
	var plugin struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		Skills     string `json:"skills"`
		MCPServers string `json:"mcpServers"`
		Interface  struct {
			DisplayName   string   `json:"displayName"`
			DefaultPrompt []string `json:"defaultPrompt"`
		} `json:"interface"`
	}
	if err := json.Unmarshal(repoFile(t, "codex-plugin/.codex-plugin/plugin.json"), &plugin); err != nil {
		t.Fatalf("plugin.json: %v", err)
	}
	if plugin.Version == "" || plugin.Name == "" {
		t.Fatalf("plugin manifest is missing name/version: %+v", plugin)
	}
	// The pointers must resolve, or the plugin installs as an empty shell.
	for _, rel := range []string{plugin.Skills, plugin.MCPServers} {
		if rel == "" {
			t.Fatal("plugin declares no skills or MCP servers")
		}
		if _, err := os.Stat(filepath.Join("..", "..", "codex-plugin", strings.TrimPrefix(rel, "./"))); err != nil {
			t.Fatalf("plugin points at %s which does not exist: %v", rel, err)
		}
	}
	// The composer surfaces these; an empty list wastes the slot.
	if len(plugin.Interface.DefaultPrompt) == 0 || plugin.Interface.DisplayName == "" {
		t.Fatalf("interface metadata is incomplete: %+v", plugin.Interface)
	}
	var mcp struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(repoFile(t, "codex-plugin/.mcp.json"), &mcp); err != nil {
		t.Fatalf(".mcp.json: %v", err)
	}
	s, ok := mcp.Servers["deja"]
	if !ok || s.Command != "deja" || len(s.Args) != 1 || s.Args[0] != "mcp" {
		t.Fatalf("mcp server entry = %+v", mcp.Servers)
	}
}

// Every bundled skill must be the same one `deja install` writes. Both plugin
// directories ship one, and a harness that reads the bundle gets whatever is in
// it — so a stale copy would teach an agent something the installed skill no
// longer says.
func TestBundledSkillsMatchInstaller(t *testing.T) {
	for _, p := range []string{
		"codex-plugin/skills/deja-history/SKILL.md",
		"claude-plugin/skills/deja-history/SKILL.md",
		"codebuddy-plugin/skills/deja-history/SKILL.md",
		"devin-plugin/skills/deja-history/SKILL.md",
		"cursor-plugin/skills/deja-history/SKILL.md",
		"antigravity-plugin/skills/deja-history/SKILL.md",
		"extensions/kimi/skills/deja-history/SKILL.md",
		"extensions/grok/skills/deja-history/SKILL.md",
		"extensions/codewhale/skills/deja-history/SKILL.md",
	} {
		got := string(repoFile(t, p))
		if want := guidanceText("claude"); got != want {
			t.Fatalf("%s has drifted from guidanceText:\n--- file ---\n%s\n--- installer ---\n%s", p, got, want)
		}
	}
}

// The plugin's own hooks are what a marketplace install gets instead of
// `deja install codex-auto`. Codex discovers them at hooks/hooks.json and
// expands ${PLUGIN_ROOT}; a path it cannot resolve is a hook that never runs.
func TestCodexPluginHooks(t *testing.T) {
	var file struct {
		Hooks map[string][]struct {
			Matcher *string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(repoFile(t, "codex-plugin/hooks/hooks.json"), &file); err != nil {
		t.Fatalf("hooks.json: %v", err)
	}
	want := map[string]struct {
		sub, matcher string
		omitMatcher  bool
		timeout      int
	}{
		// Codex fires SessionStart with source "compact" after compaction. The
		// documented every-source form has no matcher, matching codex-auto.
		"SessionStart":     {sub: "hook-context", omitMatcher: true},
		"UserPromptSubmit": {sub: "hook-prompt", omitMatcher: true},
		"PreToolUse":       {sub: "hook-tool", matcher: "Bash|apply_patch"},
		"PostToolUse":      {sub: "hook-tool-after", matcher: "Bash"},
		"PreCompact":       {sub: "hook-precompact", matcher: "manual|auto"},
		// The session just quit is back in the next one's MCP recall (#4545).
		"SessionEnd": {sub: "hook-session-end", omitMatcher: true, timeout: 3},
	}
	checkFile := func(rel, cmdPrefix string) {
		var f struct {
			Hooks map[string][]struct {
				Matcher *string `json:"matcher"`
				Hooks   []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
					Timeout int    `json:"timeout"`
				} `json:"hooks"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(repoFile(t, rel), &f); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		for event, want := range want {
			groups, ok := f.Hooks[event]
			if !ok || len(groups) == 0 || len(groups[0].Hooks) == 0 {
				t.Fatalf("%s: no %s hook in the plugin", rel, event)
			}
			cmd := groups[0].Hooks[0].Command
			if !strings.HasPrefix(cmd, cmdPrefix) || !strings.HasSuffix(cmd, want.sub) {
				t.Fatalf("%s: %s runs %q, not the plugin's own script with %s", rel, event, cmd, want.sub)
			}
			if groups[0].Hooks[0].Type != "command" || groups[0].Hooks[0].Timeout <= 0 {
				t.Fatalf("%s: %s entry is not a command with a timeout: %+v", rel, event, groups[0].Hooks[0])
			}
			if want.timeout > 0 && groups[0].Hooks[0].Timeout != want.timeout {
				t.Fatalf("%s: %s timeout = %d, want %d", rel, event, groups[0].Hooks[0].Timeout, want.timeout)
			}
			if want.omitMatcher {
				if groups[0].Matcher != nil {
					t.Fatalf("%s: %s matcher = %q, want omitted", rel, event, *groups[0].Matcher)
				}
			} else if groups[0].Matcher == nil || *groups[0].Matcher != want.matcher {
				got := ""
				if groups[0].Matcher != nil {
					got = *groups[0].Matcher
				}
				t.Fatalf("%s: %s matcher = %q, want %q", rel, event, got, want.matcher)
			}
		}
	}
	checkFile("codex-plugin/hooks/hooks.json", "${PLUGIN_ROOT}/hooks/deja.sh ")
	checkFile(".codex-plugin/hooks.json", "${PLUGIN_ROOT}/codex-plugin/hooks/deja.sh ")
	_ = file

	script := filepath.Join("..", "..", "codex-plugin", "hooks", "deja.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("the script the hooks point at is missing: %v", err)
	}
	// What ships is the mode git records, not the one this checkout happens to
	// have: a Windows checkout reports -rw-rw-rw- for every file, so reading it
	// off the disk failed the whole matrix (#1671).
	out, err := exec.CommandContext(t.Context(), "git", "ls-files", "-s", "--", script).Output()
	if err != nil {
		t.Skipf("git is not available to read the recorded mode: %v", err)
	}
	if mode, _, ok := strings.Cut(strings.TrimSpace(string(out)), " "); !ok || mode != "100755" {
		t.Fatalf("codex-plugin/hooks/deja.sh is recorded as %q, not 100755 — Codex runs it directly", mode)
	}

	// The spec documents `hooks` as one path; Codex's loader also takes a
	// list, but the catalogue validators read the documented form.
	var manifest struct {
		Hooks string `json:"hooks"`
	}
	if err := json.Unmarshal(repoFile(t, "codex-plugin/.codex-plugin/plugin.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Hooks != "./hooks/hooks.json" {
		t.Fatalf("the manifest does not declare the hooks file as a path: %q", manifest.Hooks)
	}
}

// The plugin stands down when `deja install codex-auto` has already written
// the same hook into Codex's own hooks.json. If the installer's command stops
// matching what the script greps for, both fire and the digest arrives twice.
func TestCodexPluginStandsDownForTheInstaller(t *testing.T) {
	script := string(repoFile(t, "codex-plugin/hooks/deja.sh"))
	if !strings.Contains(script, "CODEX_HOME") {
		t.Fatal("the script does not honour CODEX_HOME, so a relocated Codex home hides the installer's wiring")
	}
	pattern := regexp.MustCompile(`deja[^"]*hook-(context|prompt|precompact|tool)`)
	written, err := json.Marshal(map[string]any{"command": "/opt/homebrew/bin/deja hook-context"})
	if err != nil {
		t.Fatal(err)
	}
	if !pattern.Match(written) {
		t.Fatalf("the script's own pattern does not match what the installer writes: %s", written)
	}
}

// The plugin directory asks for these before it will list anything, and a
// missing logo or policy URL is a submission bounced weeks later rather than a
// build that fails now.
func TestCodexPluginListingMetadata(t *testing.T) {
	var manifest struct {
		Interface struct {
			DisplayName      string   `json:"displayName"`
			ShortDescription string   `json:"shortDescription"`
			LongDescription  string   `json:"longDescription"`
			DeveloperName    string   `json:"developerName"`
			Category         string   `json:"category"`
			WebsiteURL       string   `json:"websiteURL"`
			PrivacyPolicyURL string   `json:"privacyPolicyUrl"`
			TermsURL         string   `json:"termsOfServiceUrl"`
			Logo             string   `json:"logo"`
			LogoDark         string   `json:"logoDark"`
			DefaultPrompt    []string `json:"defaultPrompt"`
		} `json:"interface"`
	}
	if err := json.Unmarshal(repoFile(t, "codex-plugin/.codex-plugin/plugin.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	i := manifest.Interface
	for name, value := range map[string]string{
		"displayName":       i.DisplayName,
		"shortDescription":  i.ShortDescription,
		"longDescription":   i.LongDescription,
		"developerName":     i.DeveloperName,
		"category":          i.Category,
		"websiteURL":        i.WebsiteURL,
		"privacyPolicyUrl":  i.PrivacyPolicyURL,
		"termsOfServiceUrl": i.TermsURL,
	} {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("interface.%s is empty", name)
		}
	}
	if len(i.DefaultPrompt) == 0 {
		t.Fatal("no starter prompts: the listing shows them, and the review asks for realistic ones")
	}
	// The logos ship inside the plugin, so a marketplace copy carries them.
	for _, rel := range []string{i.Logo, i.LogoDark} {
		if !strings.HasPrefix(rel, "./") {
			t.Fatalf("logo %q must be a ./ path inside the plugin", rel)
		}
		if _, err := os.Stat(filepath.Join("..", "..", "codex-plugin", strings.TrimPrefix(rel, "./"))); err != nil {
			t.Fatalf("logo %s is not in the plugin: %v", rel, err)
		}
	}
}

func TestRootCodexPluginManifest(t *testing.T) {
	var manifest struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		Skills     string `json:"skills"`
		MCPServers string `json:"mcpServers"`
		Hooks      string `json:"hooks"`
		Interface  struct {
			ComposerIcon string `json:"composerIcon"`
			Logo         string `json:"logo"`
			LogoDark     string `json:"logoDark"`
		} `json:"interface"`
	}
	if err := json.Unmarshal(repoFile(t, ".codex-plugin/plugin.json"), &manifest); err != nil {
		t.Fatalf(".codex-plugin/plugin.json: %v", err)
	}
	if manifest.Name == "" || manifest.Version == "" {
		t.Fatalf(".codex-plugin/plugin.json missing name/version: %+v", manifest)
	}
	for _, rel := range []string{
		manifest.Skills,
		manifest.MCPServers,
		manifest.Hooks,
		manifest.Interface.ComposerIcon,
		manifest.Interface.Logo,
		manifest.Interface.LogoDark,
	} {
		if !strings.HasPrefix(rel, "./") {
			t.Fatalf(".codex-plugin/plugin.json path %q must be repo-relative with ./ prefix", rel)
		}
		if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(strings.TrimPrefix(rel, "./")))); err != nil {
			t.Fatalf(".codex-plugin/plugin.json points at %s which does not exist: %v", rel, err)
		}
	}

	var hooksFile struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(repoFile(t, ".codex-plugin/hooks.json"), &hooksFile); err != nil {
		t.Fatalf(".codex-plugin/hooks.json: %v", err)
	}
	if len(hooksFile.Hooks) != 6 {
		t.Fatalf(".codex-plugin/hooks.json declares %d hook events, want 6", len(hooksFile.Hooks))
	}
	for event, entries := range hooksFile.Hooks {
		for _, entry := range entries {
			for _, h := range entry.Hooks {
				rel, ok := strings.CutPrefix(h.Command, "${PLUGIN_ROOT}/")
				if !ok {
					t.Fatalf("%s hook command %q must start with ${PLUGIN_ROOT}/", event, h.Command)
				}
				scriptRel, _, _ := strings.Cut(rel, " ")
				if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(scriptRel))); err != nil {
					t.Fatalf("%s hook script %q does not resolve from repo root: %v", event, scriptRel, err)
				}
			}
		}
	}
}

// Codex keeps the context a hook added in the thread, and resuming it replays
// that into the next request: every `codex resume` put the same start digest in
// front of the model once more, two after one resume and three after a fork
// (#4550). A Claude transcript that holds no digest still gets one on resume
// (#5122 covers the one that does).
func TestCodexResumeGetsNoSecondDigest(t *testing.T) {
	dir, fork := forkStore(t, false)
	codex := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "10", "02")
	source := filepath.Join(codex, "rollout-2026-10-02T07-00-00-source-x.jsonl")
	forked := filepath.Join(codex, "rollout-2026-10-02T07-20-00-fork-x.jsonl")
	prior := filepath.Join(codex, "rollout-2026-08-04T09-00-00-prior-x.jsonl")
	run := func(sid, src, transcript, cwd string) string {
		// Marshalled, not spliced: a Windows transcript path's backslashes
		// spliced in raw are not JSON, and the hook read no payload at all.
		payload, err := json.Marshal(map[string]string{"session_id": sid, "source": src, "transcript_path": transcript, "cwd": cwd})
		if err != nil {
			t.Fatal(err)
		}
		withHookStdin(t, string(payload))
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}
	if out := run("source-x", "startup", source, "/w/q"); !strings.Contains(out, "deja-recall") {
		t.Fatalf("the codex session got no digest at startup, so this proves nothing:\n%s", out)
	}
	if out := run("source-x", "resume", source, "/w/q"); strings.TrimSpace(out) != "" {
		t.Errorf("a resumed codex session got its start digest again:\n%s", out)
	}
	// A fork carries the thread it came from, that digest included.
	if out := run("fork-x", "startup", forked, "/w/q"); strings.TrimSpace(out) != "" {
		t.Errorf("a codex fork got a second start digest:\n%s", out)
	}
	// A thread that never had one gets one on resume.
	if out := run("prior-x", "resume", prior, "/w/q"); !strings.Contains(out, "deja-recall") {
		t.Errorf("a resumed codex session that never had a digest got none:\n%s", out)
	}
	// A Claude transcript holding no digest is given one.
	claude := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p", fork+".jsonl")
	if out := run(fork, "startup", claude, "/w/p"); !strings.Contains(out, "deja-recall") {
		t.Fatalf("the claude session got no digest at startup:\n%s", out)
	}
	if out := run(fork, "resume", claude, "/w/p"); !strings.Contains(out, "deja-recall") {
		t.Errorf("a resumed Claude Code session lost its digest:\n%s", out)
	}
}

// codexDejaTimeouts reads the hook timeout under each event of a hooks.json
// that holds only deja's entries.
func codexDejaTimeouts(t *testing.T, path string) map[string]float64 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Hooks []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for event, groups := range root.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				out[event], _ = h["timeout"].(float64)
			}
		}
	}
	return out
}

// codex-cli 0.149.0 caps SessionEnd hooks at 3 s and prints "clamping
// SessionEnd hook timeout to 3s" on every start when hooks.json asks for more;
// deja wrote 10 there like every other event (#5126).
func TestCodexSessionEndTimeoutFitsCodexCap(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CodexHome(), "hooks.json")
	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	got := codexDejaTimeouts(t, path)
	if got["SessionEnd"] != 3 {
		t.Errorf("SessionEnd timeout = %v, want 3 (codex clamps anything above)", got["SessionEnd"])
	}
	for _, h := range codexHookWiring {
		if h.Event != "SessionEnd" && got[h.Event] != 10 {
			t.Errorf("%s timeout = %v, want 10 as before", h.Event, got[h.Event])
		}
	}
}

// A hooks.json written before the fix carries 10; a reinstall rewrites it,
// and a lower value the reader chose stays.
func TestCodexReinstallLowersOldSessionEndTimeout(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CodexHome(), "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		seed, want float64
	}{{10, 3}, {2, 2}} {
		cmd := hookRun("/usr/local/bin/deja", "hook-session-end")
		seed := mustMarshal(t, map[string]any{"hooks": map[string]any{
			"SessionEnd": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd, "timeout": c.seed}}}},
		}})
		if err := os.WriteFile(path, seed, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
			t.Fatal(err)
		}
		if got := codexDejaTimeouts(t, path)["SessionEnd"]; got != c.want {
			t.Errorf("seeded %v: SessionEnd timeout after reinstall = %v, want %v", c.seed, got, c.want)
		}
		b, _ := os.ReadFile(path)
		if r, err := installCodexHooks("/usr/local/bin/deja", false); err != nil || r.Action != "unchanged" {
			t.Errorf("seeded %v: a second install = %q, %v; want unchanged", c.seed, r.Action, err)
		}
		if n := dejaHookCount(settingsHooks(t, b), "SessionEnd", "hook-session-end"); n != 1 {
			t.Errorf("seeded %v: SessionEnd runs deja %d times, want once:\n%s", c.seed, n, b)
		}
	}
}

// The other shape the count is written in: "thirty-four coding agents", or
// "thirty-three other coding agents" in a file that ships inside one of them.
// The "named + N more" tests cannot see it, and it is in twelve files —
// including the two plugin READMEs, which had said twenty-five since the
// registry held twenty-five.
func TestEveryCodingAgentCountMatchesTheRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)
	phrase := regexp.MustCompile(`(?i)((?:twenty|thirty|forty|fifty)(?:-[a-z]+)?) (other )?coding agents`)

	files := []string{
		"claude-plugin/README.md",
		"codex-plugin/README.md",
		"llms-install.md",
		"docs/ARCHITECTURE.md",
		"docs/guide/harnesses.html",
		"extensions/dsh/README.md",
		"extensions/dsh/package.json",
		"extensions/openclaw/README.md",
		"extensions/opencode/README.md",
		"extensions/opencode/package.json",
		"extensions/pi/README.md",
		// The generator's own doc comment, which said thirty-three while it
		// was rendering thirty-four pages.
		"scripts/genregistry/main.go",
	}
	checked := 0
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		found := phrase.FindAllStringSubmatch(string(b), -1)
		if len(found) == 0 {
			t.Errorf("%s no longer counts coding agents — the phrasing changed and this stopped checking it", f)
			continue
		}
		for _, m := range found {
			// "other" means the file ships inside one of them, so it counts
			// the rest.
			want := countWord(t, n)
			if m[2] != "" {
				want = countWord(t, n-1)
			}
			if got := strings.ToLower(m[1]); got != want {
				t.Errorf("%s says %q%scoding agents; the registry has %d, so it is %q",
					f, got, " "+m[2], n, want)
			}
			checked++
		}
	}
	if checked < len(files) {
		t.Errorf("only %d counts checked across %d files", checked, len(files))
	}
}

// The four rules #2575 to #2596 established for configs and skills — recognise
// what is deja's, restore what install replaced, drop deja's own backup, remove
// what deja created — were applied one file at a time. The command files were
// never given them: install overwrites a `deja.md` the reader wrote, keeps the
// backup it promised, and uninstall deletes its own copy without putting theirs
// back. Eight files are lost that way in a full install/uninstall round (#2600).
func TestUninstallPutsBackACommandFileItReplaced(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".cursor", "commands", "deja.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "# my own deja command\n\nThis file is mine.\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
		t.Fatal(err)
	}
	// The premise: deja replaced it and kept a copy.
	if b, err := os.ReadFile(path); err != nil || string(b) == mine {
		t.Fatalf("install did not replace the reader's command file: %v", err)
	}
	if b, err := os.ReadFile(path + ".bak"); err != nil || string(b) != mine {
		t.Fatalf("install kept no copy of it: %v", err)
	}

	if _, err := captureRun(t, "uninstall", "cursor"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the reader's own command file is gone: %v", err)
	}
	if string(b) != mine {
		t.Errorf("what is there now is not theirs:\n%s", b)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("the copy was put back and the backup left behind: %v", err)
	}
}

// A command file deja wrote itself still goes, with no leftovers.
func TestUninstallRemovesACommandFileItWrote(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".cursor", "commands", "deja.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("install wrote no command file: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "cursor"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("deja's own command file survived: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("a backup survived: %v", err)
	}
}

// The CLI fallback these command files hand the model was the bare-query form,
// `deja "$ARGUMENTS"`. deja dispatches a first word that happens to be a
// command, so a one-word question ran that command instead of searching for the
// word — `deja version` prints a version number — and a query naming one of
// deja's own flags died in flag parsing.
func TestWrittenCommandFilesNameTheSubcommand(t *testing.T) {
	for _, tc := range []struct {
		name, body, token string
	}{
		{"markdown", markdownCommand("/bin/deja"), "$ARGUMENTS"},
		{"body", commandBody("/bin/deja", "{{args}}"), "{{args}}"},
	} {
		call := "/bin/deja --framed search -- \"" + tc.token + "\""
		if !strings.Contains(tc.body, call) {
			t.Errorf("%s does not run a search:\n%s", tc.name, tc.body)
		}
		if strings.Contains(tc.body, "/bin/deja \""+tc.token+"\"") {
			t.Errorf("%s still hands the query over as deja's first word", tc.name)
		}
	}
}

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

// captureBoth runs one command and returns its stdout and stderr separately:
// this test is about which of the two the hint lands on.
func captureBoth(t *testing.T, args ...string) (string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = wOut, wErr
	outCh, errCh := make(chan string, 1), make(chan string, 1)
	go func() { b, _ := io.ReadAll(rOut); outCh <- string(b) }()
	go func() { b, _ := io.ReadAll(rErr); errCh <- string(b) }()
	_ = run(args)
	_ = wOut.Close()
	_ = wErr.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	return <-outCh, <-errCh
}

// A wrong guess at a command falls through to search, and the hint that says so
// used to run only when the search came back empty (#674). A typo whose word is
// in the history never got there: the reader saw a conversation they did not
// ask for and nothing about the command they meant (#2197).
func TestAMistypedCommandIsNamedEvenWhenTheSearchMatches(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claude, "alpha", "one.jsonl"), "c1", []string{
		`{"type":"user","sessionId":"c1","timestamp":"` + at +
			`","message":{"role":"user","content":"we ran deja stats and doctro on the box"}}`,
	})
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	// The premise: this word is in the history, so the search does find it and
	// the hint is not standing in for an empty result.
	out, said := captureBoth(t, "doctro")
	if !strings.Contains(out, "doctro on the box") {
		t.Fatalf("the fallback search found nothing, so this measures nothing:\n%s\n%s", out, said)
	}
	if !strings.Contains(said, "did you mean `deja doctor`?") {
		t.Errorf("a mistyped command that matched something says nothing about the command:\n%s", said)
	}
	// On stderr, so a script reading the results is not fed the hint.
	if strings.Contains(out, "did you mean") {
		t.Errorf("the hint is on stdout, where the results are:\n%s", out)
	}

	// A bare search of more than one word is a search, however close its first
	// word sits to a command name.
	if _, saidPhrase := captureBoth(t, "doctro", "box"); strings.Contains(saidPhrase, "did you mean") {
		t.Errorf("a multi-word bare search was read as a guess at a command:\n%s", saidPhrase)
	}
	// The explicit form is someone searching for the word, and stays quiet.
	_, saidExplicit := captureBoth(t, "search", "doctro")
	if strings.Contains(saidExplicit, "did you mean") {
		t.Errorf("`deja search doctro` is a search, not a guess at a command:\n%s", saidExplicit)
	}
}

// The command file tells the reader — a model or a person — what to run when
// the MCP tools are not there. They copy it into a shell verbatim, so a home
// like "/Users/John Smith" must not be pasted bare: the shell would run
// "/Users/John" and pass the rest as arguments.
func TestCommandSnippetQuotesAPathWithSpaces(t *testing.T) {
	body := commandBody("/Users/John Smith/bin/deja", "the user's request")
	if strings.Contains(body, "\n/Users/John Smith/bin/deja ") {
		t.Fatalf("the path is pasted bare into a shell snippet:\n%s", body)
	}
	if !strings.Contains(body, `'/Users/John Smith/bin/deja'`) {
		t.Fatalf("the path is not quoted:\n%s", body)
	}
}

// An ordinary path stays plain: quoting it would suggest that escaping matters
// here when it does not.
func TestCommandSnippetLeavesAPlainPathAlone(t *testing.T) {
	body := commandBody("/usr/local/bin/deja", "the user's request")
	if strings.Contains(body, `'/usr/local/bin/deja'`) {
		t.Fatalf("an ordinary path was quoted:\n%s", body)
	}
	// The snippet names the subcommand now; what this test is about is that the
	// path itself is left unquoted.
	if !strings.Contains(body, "/usr/local/bin/deja --framed search -- \"the user's request\"") {
		t.Fatalf("the snippet lost its shape:\n%s", body)
	}
}

func TestShellQuoteIfNeeded(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/usr/local/bin/deja", "/usr/local/bin/deja"},
		{"/Users/John Smith/deja", `'/Users/John Smith/deja'`},
		{"/opt/it's/deja", `'/opt/it'"'"'s/deja'`},
		{"/opt/$HOME/deja", `'/opt/$HOME/deja'`},
	} {
		if got := shellQuoteIfNeeded(tc.in); got != tc.want {
			t.Errorf("shellQuoteIfNeeded(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Command text is not prose. The clause cut that lets a question lead past an
// instruction (#4751) would lead "cd repo; go test" with the test and drop the
// directory, and cut a quoted commit message at its full stop. The tool hook
// reads a command exactly as it did before that change.
func TestToolHookReadsACommandWithoutCuttingClauses(t *testing.T) {
	cases := []struct{ cmd, want string }{
		{
			`cd quokkabloom-service; go test ./retry/... -run TestDialTimeout -count=1 -v`,
			"quokkabloom-service test retry testdialtimeout",
		},
		{
			`go test ./internal/zebraquux/fetcher -run TestFetchTimeout -count=1 -race; echo done`,
			"test internal/zebraquux/fetcher testfetchtimeout echo done",
		},
		{
			`git commit -m "fix: zebraquux fetcher retries. quokkabloom dial timeout raised" internal/zebraquux/fetch.go`,
			"git commit zebraquux fetcher retries quokkabloom",
		},
	}
	for _, c := range cases {
		if got := strings.Join(toolCommandTerms(c.cmd), " "); got != c.want {
			t.Errorf("toolCommandTerms(%q) = %q, want %q", c.cmd, got, c.want)
		}
	}
}

// The CLI reference page is subtitled "every command". It missed five —
// index, mcp, share, handoff and check — and nothing noticed, because the page
// is only checked for its title and its counts (#3356).
//
// Two commands are named differently on purpose: the page describes the bare
// `deja` screen rather than `deja brief`, and `deja "query"` rather than the
// `deja search` alias that exists so a query may start with a dash. Hidden
// hooks are not commands a reader types.
func TestTheCommandsPageNamesEveryCommand(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", "commands.html"))
	if err != nil {
		t.Fatal(err)
	}
	named := map[string]bool{}
	for _, m := range regexp.MustCompile(`<code>deja ([a-z-]+)`).FindAllStringSubmatch(string(page), -1) {
		named[m[1]] = true
	}
	if len(named) < 20 {
		t.Fatalf("the page names %d commands, so this checks nothing", len(named))
	}

	spelledOtherwise := map[string]bool{"brief": true, "search": true}
	var missing []string
	for line := range strings.SplitSeq(usageText(), "\n") {
		// A name, not a flag: the usage lists `deja --harness name` under the
		// bare query as well.
		m := regexp.MustCompile(`^\s+deja ([a-z][a-z-]*)`).FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		if strings.HasPrefix(name, "hook-") || spelledOtherwise[name] || named[name] {
			continue
		}
		missing = append(missing, name)
	}
	if len(missing) > 0 {
		t.Errorf("docs/guide/commands.html says \"every command\" and has no row for %v", missing)
	}
}

// The compaction block is built from a session's own records, and a record can
// be a path or a command the parser lifted out of a tool call — payloads the
// serving-path guard never covered, because its dirt is always in the message
// text (#2000). This block is injected into a context window that was just
// emptied, and a command row is the string a person is most likely to copy.
func TestCompactEvidenceStripsControlBytes(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	dir := filepath.Join(claude, "-tmp-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_INDEX_TOOL_PATHS", "1")
	t.Setenv("DEJA_INDEX_COMMANDS", "1")

	esc := "\x1b[31m"
	rec := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// The escape is written as a \u001b escape in the JSON, the way a harness
	// writes it. A raw control byte inside a JSON string is invalid JSON and
	// the line would be dropped before any of this (#1993).
	user := rec(map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": "2026-01-02T03:04:05Z",
		"message":   map[string]any{"role": "user", "content": "why does pgbouncer time out"},
	})
	asst := rec(map[string]any{
		"type": "assistant", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": "2026-01-02T03:04:06Z",
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "name": "Read",
				"input": map[string]any{"file_path": "/tmp/app/pool" + esc + ".go"}},
			map[string]any{"type": "tool_use", "name": "Bash",
				"input": map[string]any{"command": "psql -c 'show pool" + esc + "'"}},
			// The same path twice, once with a stray byte on the end. Cleaning
			// the entry without deduping on the cleaned spelling printed it as
			// two identical rows out of a block budgeted at eight. A bare
			// control byte, not an escape sequence: SafeText replaces the ESC
			// and leaves the "[31m" behind, which is a different path and
			// should stay a different row.
			map[string]any{"type": "tool_use", "name": "Read",
				"input": map[string]any{"file_path": "/tmp/app/retry.go\u0007"}},
			map[string]any{"type": "tool_use", "name": "Read",
				"input": map[string]any{"file_path": "/tmp/app/retry.go"}},
		}},
	})
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(user+"\n"+asst+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "index"); err != nil {
		t.Fatalf("index: %v %s", err, out)
	}

	got := compactEvidence(os.Getenv("DEJA_INDEX_DIR"), "s1", "/tmp/app")
	// The premise: the block has to hold both kinds of row, or a clean answer
	// here means the records never arrived rather than that they were stripped.
	if !strings.Contains(got, "files it touched:") || !strings.Contains(got, "psql") {
		t.Fatalf("the block is missing the rows this is about:\n%q", got)
	}
	if n := strings.Count(got, "retry.go"); n != 1 {
		t.Errorf("the same path with and without a stray byte is one row, printed %d times:\n%q", n, got)
	}
	for _, r := range got {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			t.Errorf("the compaction block carries %q, which reaches the model's context and the terminal that renders it:\n%q", r, got)
			break
		}
	}
}

// The block deja serves after a compaction named the files and commands a
// summary drops (#543) and not what the session was doing. Measured on real
// transcripts, a session after a compaction spends a median of 28 actions before
// its next edit against 10 from a cold start, over 86 compactions — working out
// the two things that were in the transcript all along: the task, in 94% of
// them, and a decision it had reached, in 94%.
func TestTheCompactBlockSaysWhatTheSessionWasDoing(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "app", "compacting.jsonl"), "compacting", []string{
		`{"type":"user","sessionId":"compacting","cwd":"/work/app","timestamp":"` + ts +
			`","message":{"role":"user","content":"cap the quokkabloom retries and warm the cold path first"}}`,
		`{"type":"assistant","sessionId":"compacting","cwd":"/work/app","timestamp":"` + ts +
			`","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit",` +
			`"input":{"file_path":"/work/app/internal/fetch/quokkabloom.go","old_string":"a","new_string":"b"}}]}}`,
		`{"type":"user","sessionId":"compacting","cwd":"/work/app","timestamp":"` + ts +
			`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"e1","content":"ok"}]}}`,
		`{"type":"assistant","sessionId":"compacting","cwd":"/work/app","timestamp":"` + ts +
			`","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"Bash",` +
			`"input":{"command":"go test ./internal/fetch -count=1"}}]}}`,
		`{"type":"user","sessionId":"compacting","cwd":"/work/app","timestamp":"` + ts +
			`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"ok  internal/fetch  1.2s"}]}}`,
		`{"type":"assistant","sessionId":"compacting","cwd":"/work/app","timestamp":"` + ts +
			`","message":{"role":"assistant","content":"Decision: the quokkabloom retry budget stays at four."}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	got := compactEvidence(index.DefaultDir(), "compacting", "/work/app")
	if got == "" {
		t.Fatal("the compaction block is empty, so this measures nothing")
	}
	if !strings.Contains(got, "was working on: cap the quokkabloom retries") {
		t.Errorf("the block does not say what the session was doing:\n%s", got)
	}
	if !strings.Contains(got, "had settled: Decision: the quokkabloom retry budget stays at four") {
		t.Errorf("the block does not say what the session settled:\n%s", got)
	}
	// And it still carries the specifics a summary drops.
	if !strings.Contains(got, "quokkabloom.go") || !strings.Contains(got, "go test ./internal/fetch") {
		t.Errorf("the block lost the files or the commands:\n%s", got)
	}
}

// The compaction block is a session's own evidence, and "its own" is the whole
// point: the hook payload names no harness, so a shared id used to resolve to
// whichever copy the index had seen most recently — which can be a different
// project's session that happens to have been touched later (#1999).
func TestCompactEvidenceStaysInTheCallersProject(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	codex := filepath.Join(tmp, "codex", "sessions", "2026", "01", "01")
	if err := os.MkdirAll(filepath.Join(claude, "-w-other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(codex, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_INDEX_TOOL_PATHS", "1")
	t.Setenv("DEJA_INDEX_COMMANDS", "1")

	rec := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// The other project's copy, and the newer of the two in the index.
	other := rec(map[string]any{
		"type": "user", "sessionId": "shared", "cwd": "/w/other",
		"timestamp": "2026-06-01T10:00:00Z",
		"message":   map[string]any{"role": "user", "content": "the other project's question"},
	}) + "\n" + rec(map[string]any{
		"type": "assistant", "sessionId": "shared", "cwd": "/w/other",
		"timestamp": "2026-06-01T10:02:00Z",
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{"file_path": "/w/other/claude_only.go"}},
			map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "go test ./pkg/ -run ClaudeOnly"}},
		}},
	}) + "\n"
	if err := os.WriteFile(filepath.Join(claude, "-w-other", "shared.jsonl"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	// The session that is compacting: same id, this project, older.
	mine := `{"type":"session_meta","timestamp":"2026-01-01T12:00:00Z","payload":{"session_id":"shared","cwd":"/w/app"}}` + "\n" +
		`{"timestamp":"2026-01-01T12:00:01Z","payload":{"role":"user","content":"the app's question about pgbouncer"}}` + "\n" +
		`{"timestamp":"2026-01-01T12:00:02Z","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"go test ./app/ -run Pool\"}"}}` + "\n"
	if err := os.WriteFile(filepath.Join(codex, "rollout-2026-01-01T12-00-00-shared.jsonl"), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "index"); err != nil {
		t.Fatalf("index: %v %s", err, out)
	}

	dir := os.Getenv("DEJA_INDEX_DIR")
	// The premise: without a project to go on, the newer copy is what answers.
	// If that stops being true the case below passes for the wrong reason.
	if blind := compactEvidence(dir, "shared", ""); !strings.Contains(blind, "claude_only.go") {
		t.Fatalf("the fixture does not reproduce the mix-up it is about:\n%s", blind)
	}

	got := compactEvidence(dir, "shared", "/w/app")
	if got == "" {
		t.Fatal("the compacting session got no block at all")
	}
	if strings.Contains(got, "claude_only.go") || strings.Contains(got, "ClaudeOnly") {
		t.Errorf("the block names another project's work:\n%s", got)
	}
	if !strings.Contains(got, "run Pool") {
		t.Errorf("the block does not name what this session actually ran:\n%s", got)
	}
}

func TestLastDistinctTakesTheNewestAndDeduplicates(t *testing.T) {
	ms := []model.Message{
		{Role: "files", Text: "/w/a.go"},
		{Role: "user", Text: "/w/ignored.go"},
		{Role: "files", Text: "/w/b.go\n/w/a.go"},
		{Role: "files", Text: "/w/c.go"},
	}
	got := lastDistinct(ms, "files", 3,
		func(s string) []string { return strings.Split(s, "\n") },
		func(s string) string { return s })
	// Newest first, each path once, and nothing from another role.
	want := []string{"/w/c.go", "/w/b.go", "/w/a.go"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if len(lastDistinct(ms, "files", 1, func(s string) []string { return strings.Split(s, "\n") },
		func(s string) string { return s })) != 1 {
		t.Fatal("the limit should bound the block")
	}
}

func TestLastDistinctDropsAgentScratch(t *testing.T) {
	ms := []model.Message{{Role: "files", Text: "/Users/x/.claude/notes.md\n/w/real.go"}}
	got := lastDistinct(ms, "files", 5,
		func(s string) []string { return strings.Split(s, "\n") },
		func(s string) string { return s })
	if len(got) != 1 || got[0] != "/w/real.go" {
		t.Fatalf("got %v, want the repository file only", got)
	}
}

func writeCompactFixture(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	line := func(s string) { b.WriteString(s + "\n") }
	line(`{"type":"user","sessionId":"claude-compact","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"make the retry loop stop dropping the last attempt"}}`)
	line(`{"type":"assistant","sessionId":"claude-compact","cwd":"/w","timestamp":"2026-01-02T03:05:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"/w/pkg/retry.go"}}]}}`)
	line(`{"type":"assistant","sessionId":"claude-compact","cwd":"/w","timestamp":"2026-01-02T03:06:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./pkg/ -run Retry -count=3"}}]}}`)
	// A pasted script: whole it swallows the block, truncated it says nothing.
	line(`{"type":"assistant","sessionId":"claude-compact","cwd":"/w","timestamp":"2026-01-02T03:07:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"python3 - <<'PY'\nimport os\nprint(os.getcwd())\nPY"}}]}}`)
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", false, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCompactEvidenceNamesWhatTheSummaryDrops(t *testing.T) {
	writeCompactFixture(t)
	got := compactEvidence(index.DefaultDir(), "claude-compact", "")
	if got == "" {
		t.Fatal("a session with files and commands should produce a block")
	}
	if !strings.Contains(got, "retry.go") {
		t.Errorf("the files it touched should be named:\n%s", got)
	}
	if !strings.Contains(got, "go test ./pkg/ -run Retry -count=3") {
		t.Errorf("the command it ran should be named:\n%s", got)
	}
	if strings.Contains(got, "<<'PY'") {
		t.Errorf("a pasted script is not evidence worth handing back:\n%s", got)
	}
}

func TestCompactEvidenceSaysNothingWhenItCannot(t *testing.T) {
	writeCompactFixture(t)
	dir := index.DefaultDir()
	if got := compactEvidence(dir, "", ""); got != "" {
		t.Errorf("no session id, no block: %q", got)
	}
	if got := compactEvidence(dir, "claude-not-indexed-yet", ""); got != "" {
		t.Errorf("an unknown session says nothing: %q", got)
	}
}

func TestFindByIDFindsWithoutTheHarness(t *testing.T) {
	writeCompactFixture(t)
	dir := index.DefaultDir()
	s, ok, err := index.FindByID(dir, "claude-compact")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if s.Harness != "claude" || len(s.Messages) == 0 {
		t.Fatalf("got %+v", s.Harness)
	}
	if _, ok, _ := index.FindByID(dir, "nope"); ok {
		t.Fatal("an unknown id should not resolve")
	}
	if _, ok, _ := index.FindByID(dir, ""); ok {
		t.Fatal("an empty id should not resolve")
	}
}

// The packet read an exit marker out of the command text, which codex and
// opencode append and Claude does not — so on a Claude transcript where the suite
// failed and was then fixed, both runs arrived as "[recorded]" and a resuming
// agent could not tell whether the work was green when it was interrupted.
func TestThePacketSaysWhetherTheCommandPassed(t *testing.T) {
	now := time.Now().UTC()
	at := func(i int) time.Time { return now.Add(time.Duration(i) * time.Minute) }
	session := model.Session{
		Harness: "claude", ID: "s1", Project: "app",
		Messages: []model.Message{
			{Role: sources.RoleCommand, Text: "go test ./internal/exporter/ -run TestBackoff -count=1", Time: at(1)},
			{Role: sources.RoleToolOutput, Text: "--- FAIL: TestBackoff (0.01s)\n    backoff_test.go:41: attempt 4 went out after 0s\nFAIL", Time: at(2)},
			{Role: sources.RoleCommand, Text: "go test ./internal/exporter/ -count=1", Time: at(3)},
			{Role: sources.RoleToolOutput, Text: "ok  \tinternal/exporter\t0.9s", Time: at(4)},
		},
	}
	data := model.CompactionContext{Tests: []model.ContextTest{
		{Command: "$ go test ./internal/exporter/ -run TestBackoff -count=1", Outcome: "recorded"},
		{Command: "$ go test ./internal/exporter/ -count=1", Outcome: "recorded"},
	}}
	withCommandOutcomes(&data, session)
	if data.Tests[0].Outcome != "failed" {
		t.Errorf("the run that printed FAIL came back as %q", data.Tests[0].Outcome)
	}
	if data.Tests[1].Outcome != "passed" {
		t.Errorf("the run that printed ok came back as %q", data.Tests[1].Outcome)
	}
}

// A command whose output nobody recorded stays what it was: the packet does not
// claim a run passed because nothing said otherwise.
func TestAnUnrecordedCommandKeepsItsOutcome(t *testing.T) {
	now := time.Now().UTC()
	session := model.Session{
		Harness: "claude", ID: "s1", Project: "app",
		Messages: []model.Message{
			{Role: sources.RoleCommand, Text: "make deploy", Time: now},
		},
	}
	data := model.CompactionContext{Tests: []model.ContextTest{{Command: "$ make deploy", Outcome: "recorded"}}}
	withCommandOutcomes(&data, session)
	if data.Tests[0].Outcome != "recorded" {
		t.Errorf("an unrecorded run was called %q", data.Tests[0].Outcome)
	}
}

// An exit marker the harness wrote wins: it is the harness's own word on the run,
// and the packet already trusted it.
func TestARecordedExitStatusIsNotOverwritten(t *testing.T) {
	now := time.Now().UTC()
	session := model.Session{
		Harness: "codex", ID: "s1", Project: "app",
		Messages: []model.Message{
			{Role: sources.RoleCommand, Text: "go test ./... → exit 1", Time: now},
			{Role: sources.RoleToolOutput, Text: "ok  \tinternal/exporter\t0.9s", Time: now.Add(time.Minute)},
		},
	}
	data := model.CompactionContext{Tests: []model.ContextTest{{Command: "$ go test ./... → exit 1", Outcome: "failed"}}}
	withCommandOutcomes(&data, session)
	if data.Tests[0].Outcome != "failed" {
		t.Errorf("the harness's own exit status was overwritten with %q", data.Tests[0].Outcome)
	}
}

// The completions are hand-maintained lists, so a new command reaches users
// without one unless something checks. `files` and `restore` shipped in 0.16.4
// and 0.16.5 and were in none of the three shells.
func TestCompletionsListEveryUserFacingCommand(t *testing.T) {
	// Hooks are invoked by agents, not typed; warmup-status is internal.
	internal := map[string]bool{
		"hook-context": true, "hook-prompt": true, "hook-precompact": true,
		"hook-antigravity": true, "hook-goose": true, "hook-goose-prompt": true,
		"hook-refresh": true, "hook-session-end": true, "hook-mcp-call": true,
		"hook-stop": true, "hook-plan": true, "hook-tool": true, "hook-tool-after": true, "hook-codewhale": true,
		"warmup-status": true, "mcp": true, "reasonix-ext": true,
	}
	// The emitted script, not the template: the command list is substituted at
	// run time now, the way the harnesses and the roles already were.
	shells := map[string]string{
		"bash":       emittedCompletion(t, "bash"),
		"zsh":        emittedCompletion(t, "zsh"),
		"fish":       emittedCompletion(t, "fish"),
		"powershell": emittedCompletion(t, "powershell"),
	}
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	// The switch in run() handles these before the map is consulted, so the
	// map alone misses them — which is how `search` stayed out of all three
	// shells (#749). `aider` and `goose` are deliberately absent: bare
	// `deja goose` is a search for the word, not a command.
	names = append(names, "search", "show", "last")
	for _, name := range names {
		if internal[name] || strings.HasPrefix(name, "-") {
			continue
		}
		for shell, script := range shells {
			if !strings.Contains(script, name) {
				t.Errorf("%s completion does not offer %q", shell, name)
			}
		}
	}
	// And the reverse: a completion offering something that no longer exists
	// teaches a wrong command.
	for _, name := range []string{"files", "restore", "view"} {
		if _, ok := commands[name]; !ok {
			t.Errorf("%q is completed but not dispatched", name)
		}
	}
}

type completionDispatchCase struct{ words, want, absent []string }

// shellScriptCommand runs body from a file rather than through -c. Git Bash on
// Windows cut a -c argument near 8 KB, so once the bash script grew past that
// the last line arrived as `co: command not found` and only the Windows leg on
// main went red (#5030).
// pwsh -File wants a .ps1 name, the others take any.
func shellScriptCommand(ctx context.Context, t *testing.T, shell string, flags []string, body string) *exec.Cmd {
	t.Helper()
	file := filepath.Join(t.TempDir(), "completion.ps1")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return exec.CommandContext(ctx, shell, append(flags, file)...)
}

func checkCompletionDispatch(t *testing.T, cases []completionDispatchCase) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	for _, shell := range completionTestShells {
		t.Run(shell, func(t *testing.T) {
			binary := shell
			if shell == "powershell" {
				binary = "pwsh"
			}
			path, err := exec.LookPath(binary)
			if err != nil {
				t.Skipf("%s not on PATH", binary)
			}
			script := emittedCompletion(t, shell)
			for _, tc := range cases {
				t.Run(strings.Join(tc.words, " "), func(t *testing.T) {
					quoted := make([]string, len(tc.words))
					for i, word := range tc.words {
						quoted[i] = fmt.Sprintf("%q", word)
					}
					input := strings.Join(tc.words, " ")
					var flags []string
					var body string
					switch shell {
					case "bash":
						drive := fmt.Sprintf("\nCOMP_WORDS=(%s)\nCOMP_CWORD=%d\n_deja_completion\nprintf '%%s\\n' \"${COMPREPLY[@]}\"\n", strings.Join(quoted, " "), len(tc.words)-1)
						flags, body = []string{"--noprofile", "--norc"}, script+drive
					case "zsh":
						// Capture the dispatched specs without depending on an interactive ZLE.
						stub := "compdef() { :; }; _arguments() { print -rl -- \"$@\"; }; _values() { shift; print -rl -- \"$@\"; };\n"
						drive := fmt.Sprintf("\nwords=(%s)\nCURRENT=%d\n_deja\n", strings.Join(quoted, " "), len(tc.words))
						flags, body = []string{"-f"}, stub+script+drive
					case "fish":
						flags, body = []string{"--no-config"}, script+"\ncomplete -C '"+input+"'\n"
					case "powershell":
						drive := fmt.Sprintf("\n[System.Management.Automation.CommandCompletion]::CompleteInput('%s', %d, $null).CompletionMatches | ForEach-Object { $_.CompletionText }\n", input, len(input))
						flags, body = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-File"}, "$ErrorActionPreference = 'Stop';\n"+script+drive
					}
					cmd := shellScriptCommand(context.Background(), t, path, flags, body)
					cmd.Dir = home
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("%s completion: %v\n%s", shell, err, out)
					}
					got := map[string]bool{}
					for line := range strings.SplitSeq(string(out), "\n") {
						line, _, _ = strings.Cut(line, "\t")
						line, _, _ = strings.Cut(line, "[")
						// zsh marks a repeatable flag with a leading `*`; the user still types --tag.
						line = strings.TrimPrefix(strings.TrimSpace(line), "*")
						got[strings.TrimSuffix(line, "=")] = true
					}
					for _, want := range tc.want {
						if !got[want] {
							t.Errorf("missing %q in %s", want, out)
						}
					}
					for _, absent := range tc.absent {
						if got[absent] {
							t.Errorf("unexpected %q in %s", absent, out)
						}
					}
				})
			}
		})
	}
}

func TestBenchCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "bench", ""}, []string{"recall", "context", "prompt", "block", "ingest", "read", "live"}, []string{}},
		{[]string{"deja", "bench", "context", "--"}, []string{"--json", "--seed"}, []string{}},
		{[]string{"deja", "bench", "prompt", "--"}, []string{"--json", "--seed"}, []string{}},
		{[]string{"deja", "bench", "block", "--"}, []string{"--json", "--seed"}, []string{}},
		{[]string{"deja", "bench", "ingest", "--"}, []string{"--json", "--seed"}, []string{}},
		{[]string{"deja", "bench", "read", "--"}, []string{"--json", "--seed"}, []string{}},
		{[]string{"deja", "bench", "recall", "--"}, []string{"--json", "--seed"}, []string{"--delivery"}},
		{[]string{"deja", "bench", "live", "--"}, []string{"--delivery", "--json"}, []string{"--seed"}},
	})
}

func TestBlameCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "blame", "--"}, []string{"--attribution", "--git-note", "--json"}, []string{}},
		{[]string{"deja", "last", "--"}, []string{"--json"}, []string{"--attribution", "--git-note"}},
	})
}

// files and how take the same four flags, but neither had a case, so Tab
// offered the search flags: two by accident, the rest refused, and never
// --all-projects, the one each help page points at (#5067).
func TestFilesHowCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "files", "x", "--"}, []string{"--all-projects", "--limit", "--project", "--json"}, []string{"--harness", "--re"}},
		{[]string{"deja", "how", "x", "--"}, []string{"--all-projects", "--limit", "--project", "--json"}, []string{"--harness", "--re"}},
	})
}

// --quiet belongs to index. bash and zsh offered it to search, which refuses
// it, and fish and PowerShell left it off index, where it works (#5073).
func TestIndexQuietCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "search", "foo", "--"}, []string{"--limit"}, []string{"--quiet", "-quiet"}},
		{[]string{"deja", "index", "--"}, []string{"--rebuild", "--quiet"}, []string{"--json", "--limit"}},
	})
}

// The dispatch test above cannot reach bash's first-word list, which offered
// --quiet to `deja --<Tab>` and turned it into the query "--quiet ...". So each
// script names the flag exactly once, on its index line.
func TestQuietIsOfferedOnlyByIndex(t *testing.T) {
	for _, tc := range []struct{ shell, flag, line string }{
		{"bash", "--quiet", `compgen -W "--rebuild -rebuild --quiet -quiet"`},
		{"zsh", "'--quiet[", "'--rebuild[force a full rebuild]' '-rebuild[force a full rebuild]' '--quiet["},
		{"fish", "-l quiet", "__fish_seen_subcommand_from index' -l quiet"},
		{"powershell", "'--quiet'", "'index' { @('--rebuild', '-rebuild', '--quiet', '-quiet') }"},
	} {
		script := emittedCompletion(t, tc.shell)
		if n := strings.Count(script, tc.flag); n != 1 {
			t.Errorf("%s completion offers %s in %d places, want only index", tc.shell, tc.flag, n)
		}
		if !strings.Contains(script, tc.line) {
			t.Errorf("%s completion does not offer %s to index", tc.shell, tc.flag)
		}
	}
}

func TestInstallCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "install", "--"}, []string{"--no-index", "--force", "--no-guidance"}, []string{}},
		{[]string{"deja", "uninstall", "--"}, []string{"--no-guidance"}, []string{"--force", "--no-index"}},
	})
}

func TestLimitJSONCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "fix", "--"}, []string{"--limit", "--json"}, []string{"--since", "--re", "--settle", "--unsettle"}},
		{[]string{"deja", "friction", "--"}, []string{"--limit", "--json", "--settle", "--unsettle"}, []string{"--since", "--re"}},
		{[]string{"deja", "tests", "--"}, []string{"--limit", "--json"}, []string{"--since", "--re", "--settle", "--unsettle"}},
	})
}

// log and view had no case in any script, so Tab offered the search flags,
// which both refuse, and never their own (#5070).
func TestLogViewCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "log", "--"}, []string{"--last", "--json"}, []string{"--re", "--harness"}},
		{[]string{"deja", "view", "--"}, []string{"--no-open", "--out"}, []string{"--re", "--json"}},
	})
}

// promote had no case in any script, so bash, zsh and PowerShell offered the
// search flags, every one refused, and fish offered nothing — not even the four
// states --state accepts (#5066).
func TestPromoteCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "promote", "01a0", "--"}, []string{"--state", "--note", "--tag", "--to"}, []string{"--re", "--json"}},
		{[]string{"deja", "promote", "01a0", "--state", ""}, []string{"accepted", "rejected", "superseded", "stale"}, []string{"--re", "--note"}},
	})
}

// The states are substituted from the map promote checks against, like the
// roles, so a fifth state cannot leave the scripts behind.
func TestCompletionOffersEveryNoteState(t *testing.T) {
	states := make([]string, 0, len(sources.NoteStates))
	for s := range sources.NoteStates {
		states = append(states, s)
	}
	sort.Strings(states)
	want := strings.Join(states, " ")
	for _, shell := range completionTestShells {
		if script := emittedCompletion(t, shell); !strings.Contains(script, want) {
			t.Errorf("%s completion does not offer every promote state", shell)
		}
	}
}

func TestRecallCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "recall", "--"}, []string{"--project", "--harness", "--limit"}, []string{"--re", "--role", "--json"}},
		{[]string{"deja", "search", "--"}, []string{"--re", "--role"}, nil},
	})
}

// recap had no case in any script, so Tab offered the search flags: --since
// only because search has it too, and --project, which recap refuses (#5069).
func TestRecapCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "recap", "--"}, []string{"--since", "--limit", "--json"}, []string{"--project", "--re", "--harness"}},
	})
}

func TestRestoreCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "restore", "src/a.go", "--"}, []string{"--span", "--force", "--all"}, []string{"--re", "--json"}},
		{[]string{"deja", "last", "--"}, []string{"--json"}, []string{"--span", "--force"}},
	})
}

func TestRulesCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "rules", ""}, []string{"sync", "status", "candidates"}, []string{"--re", "--no-embed"}},
		{[]string{"deja", "rules", "candidates", "--"}, []string{"--json", "--limit", "--since"}, []string{"--re"}},
		{[]string{"deja", "rules", "sync", ""}, []string{}, []string{"--json", "--limit", "export", "import", "ssh"}},
		{[]string{"deja", "rules", "status", ""}, []string{}, []string{"--json", "--limit"}},
	})
}

func TestSecretsCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "secrets", "--"}, []string{"--limit", "--json", "--scrub", "--dry-run"}, []string{"--re", "--harness", "--role"}},
		{[]string{"deja", "last", "--"}, []string{"--json"}, []string{"--scrub", "--dry-run"}},
	})
}

func TestSyncCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "sync", ""}, []string{"export", "import", "ssh", "forget"}, []string{}},
		{[]string{"deja", "sync", "export", "--"}, []string{"--full", "--include-imported", "--peer"}, []string{"--both"}},
		{[]string{"deja", "sync", "ssh", "--"}, []string{"--pull", "--full", "--both"}, []string{"--include-imported", "--peer"}},
		{[]string{"deja", "sync", "forget", "--"}, []string{}, []string{"--list", "--pull", "--full", "--both", "--peer"}},
		{[]string{"deja", "sync", "import", "--"}, []string{}, []string{"--include-imported", "--peer", "--both"}},
	})
}

// update sat in the no-argument list in every script, so Tab offered nothing
// for the one flag it takes (#5072).
func TestUpdateCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "update", "--"}, []string{"--force"}, []string{"--re", "--json"}},
	})
}

// wip takes --json alone and brief takes nothing, but both fell through to the
// search flags in every script but fish, and refused each one (#5071).
func TestWipBriefCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "wip", "--"}, []string{"--json"}, []string{"--re", "--harness", "--limit"}},
		{[]string{"deja", "brief", "--"}, nil, []string{"--json", "--re", "--harness", "--limit"}},
	})
}

func TestLastAndStatsCompletionValueDispatch(t *testing.T) {
	withTempStores(t)
	t.Setenv("BASH_ENV", "")
	cases := []struct {
		line string
		want []string
	}{
		// An empty value exposes the bug: every flag matches the empty prefix.
		{"deja last --project <Tab>", nil},
		{"deja last --since <Tab>", nil},
		{"deja last --from <Tab>", nil},
		{"deja stats --project <Tab>", nil},
		{"deja stats --since <Tab>", nil},

		// Once the value is complete, the next word may be another flag.
		{"deja last --project sample-project <Tab>", lastFlags},
		{"deja last --since 7d <Tab>", lastFlags},
		{"deja last --from local <Tab>", lastFlags},
		{"deja stats --project sample-project <Tab>", statsFlags},
		{"deja stats --since 7d <Tab>", statsFlags},

		// Known value lists and ordinary flag suggestions must still work.
		{"deja last --harness <Tab>", sources.HarnessNames()},
		{"deja last --role <Tab>", knownRoles},
		{"deja stats --harness <Tab>", sources.HarnessNames()},
		{"deja stats --role <Tab>", knownRoles},
		{"deja last <Tab>", lastFlags},
		{"deja stats <Tab>", statsFlags},
	}
	for _, shell := range []string{"bash", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.line, func(t *testing.T) {
					got := lastStatsCompletions(t, shell, tc.line)
					want := slices.Clone(tc.want)
					slices.Sort(got)
					slices.Sort(want)
					if !slices.Equal(got, want) {
						t.Errorf("offers %q, want %q", got, want)
					}
				})
			}
		})
	}
}

// Run the generated completion script as if Tab were pressed at the marked
// cursor. An empty working directory keeps filesystem fallback out of results.
func lastStatsCompletions(t *testing.T, shell, line string) []string {
	t.Helper()
	binary := shell
	if shell == "powershell" {
		binary = "pwsh"
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		t.Skipf("%s not on PATH", binary)
	}
	input := strings.TrimSuffix(line, "<Tab>")
	var flags []string
	var drive string
	if shell == "bash" {
		// Bash reads the words and cursor index, then writes matches to COMPREPLY.
		// Split preserves the final empty word after an option's trailing space.
		words := strings.Split(input, " ")
		for i, word := range words {
			words[i] = fmt.Sprintf("%q", word)
		}
		drive = fmt.Sprintf(`
COMP_WORDS=(%s)
COMP_CWORD=%d
_deja_completion
printf '%%s\n' "${COMPREPLY[@]}"
`, strings.Join(words, " "), len(words)-1)
		flags = []string{"--noprofile", "--norc"}
	} else {
		// PowerShell parses the command line and asks the registered completer.
		drive = fmt.Sprintf(`
[System.Management.Automation.CommandCompletion]::CompleteInput('%s', %d, $null).CompletionMatches |
    ForEach-Object { $_.CompletionText }
`, input, len(input))
		flags = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-File"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := shellScriptCommand(ctx, t, path, flags, emittedCompletion(t, shell)+drive)
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("completion: %v\n%s", err, out)
	}
	return strings.Fields(string(out))
}

func TestCompletionSearchLimit(t *testing.T) {
	withTempStores(t)
	// Check the search declarations, not merely the presence of --limit:
	// show already has its own, separate limit option.
	for _, tc := range []struct {
		shell, start, end, want string
	}{
		{"bash", "        *)", "    esac", "--limit"},
		{"zsh", "    *)", "  esac", "--limit=[max sessions to return (1-100)]:count:"},
		{"fish", "complete -c deja -n '__deja_needs_command' -l limit", "\n", "-r"},
		{"fish", "complete -c deja -n '__fish_seen_subcommand_from search' -l limit", "\n", "-r"},
		{"powershell", "$defaultOptions = @(", ")", "'--limit'"},
	} {
		t.Run(tc.shell+"/"+tc.start, func(t *testing.T) {
			_, section, ok := strings.Cut(emittedCompletion(t, tc.shell), tc.start)
			if !ok {
				t.Fatalf("missing search declaration %q", tc.start)
			}
			section, _, _ = strings.Cut(section, tc.end)
			if !strings.Contains(section, tc.want) {
				t.Errorf("search declaration %q missing %q", section, tc.want)
			}
		})
	}
}

func TestBashCompletionLimitScope(t *testing.T) {
	withTempStores(t)
	t.Setenv("BASH_ENV", "")
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	for _, tc := range []struct {
		words string
		want  bool
	}{
		{"deja --li", true},
		{"deja search --li", true},
		{"deja search query --li", true},
		{"deja query --li", true},
		{"deja show session --li", true},
		{"deja last --li", false},
		{"deja doctor --li", false},
		{"deja search --harness --li", false},
	} {
		t.Run(tc.words, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// words contains only the fixed test cases above, never user input.
			drive := "\nCOMP_WORDS=(" + tc.words + ")\nCOMP_CWORD=$((${#COMP_WORDS[@]}-1))\n_deja_completion\nprintf '%s\\n' \"${COMPREPLY[@]}\"\n"
			out, err := shellScriptCommand(ctx, t, bash, []string{"--noprofile", "--norc"}, emittedCompletion(t, "bash")+drive).CombinedOutput()
			if err != nil {
				t.Fatalf("bash completion failed: %v\n%s", err, out)
			}
			got := false
			for candidate := range strings.FieldsSeq(string(out)) {
				got = got || candidate == "--limit"
			}
			if got != tc.want {
				t.Errorf("offers --limit = %v, want %v; candidates: %s", got, tc.want, out)
			}
		})
	}
}

var completionTestShells = []string{"bash", "zsh", "fish", "powershell"}

func emittedCompletion(t *testing.T, shell string) string {
	t.Helper()
	out, err := captureRun(t, "completion", shell)
	if err != nil {
		t.Fatalf("completion %s: %v", shell, err)
	}
	return out
}

// Tab completion offered eleven harness names while deja read eighteen, in six
// hand-maintained copies of one list — hermes, goose, kimi, cline, roo,
// openclaw and zed were all missing. The lists are substituted from the
// registry now, and this is what keeps the next harness from repeating it.
func TestCompletionOffersEveryHarness(t *testing.T) {
	names := sources.HarnessNames()
	if len(names) < 10 {
		t.Fatalf("registry returned %d names, so this test proves nothing", len(names))
	}
	// The whole list, in registry order, not each name somewhere in the script:
	// every harness name also appears among the install targets, so looking for
	// them one at a time passes on a completion that offers none of them.
	want := strings.Join(names, " ")
	for _, shell := range completionTestShells {
		if script := emittedCompletion(t, shell); !strings.Contains(script, want) {
			t.Errorf("%s completion does not offer the registry's harnesses", shell)
		}
	}
}

// The same for the agents `handoff --to` accepts: that list is a function, and
// the shells held a copy of it from when it was shorter.
func TestCompletionOffersEveryHandoffTarget(t *testing.T) {
	targets := handoffTargets()
	if len(targets) < 5 {
		t.Fatalf("handoffTargets returned %d, so this test proves nothing", len(targets))
	}
	want := strings.Join(targets, " ")
	for _, shell := range completionTestShells {
		if script := emittedCompletion(t, shell); !strings.Contains(script, want) {
			t.Errorf("%s completion does not offer every handoff target", shell)
		}
	}
}

// And nothing is left holding a placeholder: a substitution that stops matching
// would otherwise ship the marker to the reader's shell.
func TestCompletionSubstitutesEveryPlaceholder(t *testing.T) {
	for _, shell := range completionTestShells {
		if script := emittedCompletion(t, shell); strings.Contains(script, "%HARNESSES%") ||
			strings.Contains(script, "%HANDOFF_TARGETS%") || strings.Contains(script, "%INSTALL_TARGETS%") ||
			strings.Contains(script, "%NOTE_STATES%") {
			t.Errorf("%s completion still carries a placeholder", shell)
		}
	}
}

// The roles list was the one hand-maintained copy left: seven of them across
// the three shells, all reading "user assistant tool" while `--role` accepted
// four more — tool-output, files, command and edit, the channels that record
// which files a session touched, which commands it ran and which edits it made
// (#1658). Substituted from knownRoles now, like the harnesses above.
// PowerShell kept two copies of the old list after that, because this test
// never looked at it. (#5017)
func TestCompletionOffersEveryRole(t *testing.T) {
	if len(knownRoles) < 5 {
		t.Fatalf("knownRoles holds %d entries, so this test proves nothing", len(knownRoles))
	}
	want := strings.Join(knownRoles, " ")
	for _, shell := range completionTestShells {
		script := emittedCompletion(t, shell)
		if !strings.Contains(script, want) {
			t.Errorf("%s completion does not offer every role deja accepts", shell)
		}
		// And no copy of the old short list may survive alongside it.
		if strings.Contains(script, "user assistant tool)") ||
			strings.Contains(script, "'user assistant tool'") ||
			strings.Contains(script, "'user', 'assistant', 'tool'") {
			t.Errorf("%s completion still holds a hand-written role list", shell)
		}
	}
}

func TestCompletionOffersHookFlags(t *testing.T) {
	want := map[string][]string{
		"bash": {
			`hook-prompt)` + "\n            COMPREPLY=( $(compgen -W \"--plain --copilot --strict --mcode\"",
			`hook-tool-after)` + "\n            COMPREPLY=( $(compgen -W \"--plain --copilot --mcode\"",
			`hook-session-end)` + "\n            COMPREPLY=( $(compgen -W \"--json\"",
		},
		"zsh": {
			`hook-prompt)` + "\n      _arguments '--plain[omit formatting]' '--copilot[answer in the Copilot CLI hook shape]' '--strict[skip low-confidence sessions]' '--mcode[answer in the MCode hook shape]'",
			`hook-tool-after)` + "\n      _arguments '--plain[omit formatting]' '--copilot[answer in the Copilot CLI hook shape]' '--mcode[answer in the MCode hook shape]'",
			`hook-session-end)` + "\n      _arguments '--json[print JSON]'",
		},
		"fish": {
			`__fish_seen_subcommand_from hook-prompt' -l plain`, `__fish_seen_subcommand_from hook-prompt' -l copilot`, `__fish_seen_subcommand_from hook-prompt' -l strict`, `__fish_seen_subcommand_from hook-prompt' -l mcode`,
			`__fish_seen_subcommand_from hook-tool-after' -l plain`, `__fish_seen_subcommand_from hook-tool-after' -l copilot`, `__fish_seen_subcommand_from hook-tool-after' -l mcode`,
			`__fish_seen_subcommand_from hook-session-end' -l json`,
		},
		"powershell": {
			`'hook-prompt' { @('--plain', '--copilot', '--strict', '--mcode') }`,
			`'hook-tool-after' { @('--plain', '--copilot', '--mcode') }`,
			`'hook-session-end' { @('--json') }`,
		},
	}
	for _, shell := range completionTestShells {
		script := emittedCompletion(t, shell)
		for _, commandEntry := range want[shell] {
			if !strings.Contains(script, commandEntry) {
				t.Errorf("%s completion does not dispatch hook flags through %s", shell, commandEntry)
			}
		}
	}
}

// The command list was the copy that outlived every other: `deja recap` and
// `deja tests` shipped in 0.21.0 and neither bash nor fish had ever heard of
// them, while the bash and fish scripts each carried the same list twice over
// and the PowerShell array carried it three times — merges landing beside each
// other. A command nobody can tab-complete is a command most people never find.
func TestEveryCommandIsOfferedByEveryShell(t *testing.T) {
	offered := map[string]bool{}
	for _, name := range completionCommands() {
		offered[name] = true
	}
	for name := range commands {
		// Flags the table answers to, hooks a harness calls, and the internals
		// nobody types: none of them is a command to complete.
		if strings.HasPrefix(name, "-") || strings.HasPrefix(name, "hook-") || completionHiddenCommands[name] {
			continue
		}
		if !offered[name] {
			t.Errorf("`deja %s` is in the command table and in no completion script", name)
		}
	}
	// The reverse, minus the four run() answers before the map is consulted.
	handledEarly := map[string]bool{"search": true, "show": true, "last": true, "help": true}
	for name := range offered {
		if _, ok := commands[name]; !ok && !handledEarly[name] {
			t.Errorf("completion offers %q, which is not a command", name)
		}
	}
	want := strings.Join(completionCommands(), " ")
	for _, shell := range []string{"bash", "fish"} {
		if script := emittedCompletion(t, shell); !strings.Contains(script, want) {
			t.Errorf("%s completion does not offer the command list", shell)
		}
	}
}

// One list per shell, not three. The duplicates were harmless to the shell and
// fatal to the list: each copy drifted on its own.
func TestNoShellCarriesTheCommandListTwice(t *testing.T) {
	first := completionCommands()[0]
	for _, shell := range completionTestShells {
		script := emittedCompletion(t, shell)
		if n := strings.Count(script, first+" "); n > 2 {
			t.Errorf("%s completion mentions the head of the command list %d times", shell, n)
		}
	}
	ps := emittedCompletion(t, "powershell")
	if n := strings.Count(ps, "$commands = @("); n != 1 {
		t.Errorf("the powershell script declares $commands %d times", n)
	}
	// Counted in the array alone: recap has a case of its own now, which names
	// it once more further down (#5069).
	_, decl, _ := strings.Cut(ps, "$commands = @(")
	decl, _, _ = strings.Cut(decl, "\n")
	if n := strings.Count(decl, "'recap'"); n != 1 {
		t.Errorf("'recap' appears %d times in the powershell command list", n)
	}
}

// No shell had a case for restore, so bash, zsh and PowerShell fell through to
// the search flags — the first of which, --re, restore refuses — and fish
// offered nothing at all.
func TestCompletionHasARestoreBlock(t *testing.T) {
	for _, tc := range []struct{ shell, start, end string }{
		{"bash", "\n        restore)\n", "\n            ;;\n"},
		{"zsh", "\n    restore)\n", "\n      ;;\n"},
		{"fish", "__fish_seen_subcommand_from restore'", "\ncomplete -c deja -n '__fish_seen_subcommand_from secrets'"},
		{"powershell", "\n            'restore' {\n", "\n            }\n"},
	} {
		script := emittedCompletion(t, tc.shell)
		_, block, ok := strings.Cut(script, tc.start)
		if !ok {
			t.Errorf("%s completion has no restore block", tc.shell)
			continue
		}
		block, _, _ = strings.Cut(block, tc.end)
		// fish names a long flag "-l span" where the others spell "--span".
		block = strings.ReplaceAll(block, "-l ", "--")
		for _, flag := range []string{"--span", "--force", "--all", "out"} {
			if !strings.Contains(block, flag) {
				t.Errorf("%s restore block does not offer %s:\n%s", tc.shell, flag, block)
			}
		}
	}
}

// zsh keeps its command list by hand, with a description beside each name, so
// the joined-list check above cannot see it — and `deja recall` was missing
// from it while bash and fish offered it.
func TestZshOffersEveryCommand(t *testing.T) {
	script := emittedCompletion(t, "zsh")
	_, block, ok := strings.Cut(script, "\n  commands=(\n")
	if !ok {
		t.Fatal("zsh completion has no commands=( block")
	}
	block, _, _ = strings.Cut(block, "\n  )\n")
	for _, name := range completionCommands() {
		if !strings.Contains(block, "'"+name+":") {
			t.Errorf("zsh completion does not offer `deja %s`", name)
		}
	}
}

// recall fell through to the search flags in bash, zsh and PowerShell, most of
// which it refuses, and fish offered nothing for it.
func TestCompletionHasARecallBlock(t *testing.T) {
	for _, tc := range []struct{ shell, start, end, valueGuard string }{
		{"bash", "\n        recall)\n", "\n            ;;\n", "elif [[ \"$prev\" == \"--project\" || \"$prev\" == \"--limit\" || \"$prev\" == \"--offset\" ]]; then\n                COMPREPLY=()"},
		{"zsh", "\n    recall)\n", "\n      ;;\n", ""},
		{"fish", "__fish_seen_subcommand_from recall;", "\ncomplete -c deja -n '__fish_seen_subcommand_from remember'", ""},
		{"powershell", "\n            'recall' {\n", "\n            }\n", "elseif ($previous -in @('--project', '--limit', '--offset')) { @() }"},
	} {
		script := emittedCompletion(t, tc.shell)
		_, block, ok := strings.Cut(script, tc.start)
		if !ok {
			t.Errorf("%s completion has no recall block", tc.shell)
			continue
		}
		block, _, _ = strings.Cut(block, tc.end)
		if tc.valueGuard != "" {
			if !strings.Contains(block, tc.valueGuard) {
				t.Errorf("%s recall block offers flags while completing a project, limit, or offset:\n%s", tc.shell, block)
			}
			block = strings.Replace(block, tc.valueGuard, "", 1)
		}
		block = strings.ReplaceAll(block, "-l ", "--")
		for _, flag := range recallFlags {
			if !strings.Contains(block, flag) {
				t.Errorf("%s recall block does not offer %s:\n%s", tc.shell, flag, block)
			}
		}
		for _, flag := range []string{"--re", "--role", "--json", "--since"} {
			if strings.Contains(block, flag+" ") || strings.Contains(block, flag+"'") || strings.Contains(block, flag+"=") || strings.Contains(block, flag+"[") {
				t.Errorf("%s recall block offers %s, which recall refuses:\n%s", tc.shell, flag, block)
			}
		}
		if !strings.Contains(block, sources.HarnessNames()[0]) && !strings.Contains(block, "harnesses") {
			t.Errorf("%s recall block does not complete --harness:\n%s", tc.shell, block)
		}
	}
}

// show in bash and PowerShell only guarded --harness, so the count after
// --offset or --limit was completed with show's own flags (#5940).
func TestCompletionShowBlockLeavesCountsAlone(t *testing.T) {
	for _, tc := range []struct{ shell, start, end, valueGuard string }{
		{"bash", "\n        show)\n", "\n            ;;\n", "elif [[ \"$prev\" == \"--offset\" || \"$prev\" == \"--limit\" ]]; then\n                COMPREPLY=()"},
		{"powershell", "\n            'show' {\n", "\n            }\n", "elseif ($previous -in @('--offset', '--limit')) { @() }"},
	} {
		_, block, ok := strings.Cut(emittedCompletion(t, tc.shell), tc.start)
		if !ok {
			t.Errorf("%s completion has no show block", tc.shell)
			continue
		}
		block, _, _ = strings.Cut(block, tc.end)
		if !strings.Contains(block, tc.valueGuard) {
			t.Errorf("%s show block offers flags while completing an offset or limit:\n%s", tc.shell, block)
		}
		block = strings.Replace(block, tc.valueGuard, "", 1)
		for _, flag := range showFlags {
			if !strings.Contains(block, flag) {
				t.Errorf("%s show block does not offer %s:\n%s", tc.shell, flag, block)
			}
		}
	}
}

// The value guards must also be present when a contributor lacks either shell.
func TestCompletionLastAndStatsLeaveValuesAlone(t *testing.T) {
	for _, tc := range []struct{ shell, command, valueGuard string }{
		{"bash", "last", `elif [[ "$prev" == "--project" || "$prev" == "--since" || "$prev" == "--from" ]]; then
                COMPREPLY=()`},
		{"bash", "stats", `elif [[ "$prev" == "--project" || "$prev" == "--since" ]]; then
                COMPREPLY=()`},
		{"powershell", "last", `elseif ($previous -in @('--project', '--since', '--from')) { @() }`},
		{"powershell", "stats", `elseif ($previous -in @('--project', '--since')) { @() }`},
	} {
		t.Run(tc.shell+"/"+tc.command, func(t *testing.T) {
			block := shellCommandBlock(tc.shell, emittedCompletion(t, tc.shell), tc.command)
			if !strings.Contains(block, tc.valueGuard) {
				t.Errorf("%s %s completion offers flags where a value is expected:\n%s", tc.shell, tc.command, block)
			}
		})
	}
}

// friction shared one case with fix and tests in every shell, so Tab never
// offered --settle or --unsettle, which `deja help friction` documents. Its
// case is its own now; fix keeps the two flags it takes, and the wall id after
// --settle or --unsettle is not completed (#5640).
func TestCompletionHasAFrictionBlock(t *testing.T) {
	for _, tc := range []struct {
		shell, friction, fix, end string
		// takesValue is how the shell says the flag's value is not completed.
		takesValue []string
	}{
		{"bash", "\n        friction)\n", "\n        fix|tests)\n", "\n            ;;\n",
			[]string{`"$prev" == "--settle" || "$prev" == "--unsettle" ]]; then` + "\n                COMPREPLY=()"}},
		{"zsh", "\n    friction)\n", "\n    fix|tests)\n", "\n      ;;\n",
			[]string{"'--settle=[mark a wall fixed by the id its row prints]:wall id:'", "'--unsettle=[take a --settle back]:wall id:'"}},
		{"fish", "", "", "",
			[]string{"-l settle -x", "-l unsettle -x"}},
		{"powershell", "\n            'friction' {\n", "\n            { $_ -in @('fix', 'tests') } {\n", "\n            }\n",
			[]string{"if ($previous -in @('--limit', '--settle', '--unsettle')) { @() }"}},
	} {
		script := emittedCompletion(t, tc.shell)
		block := func(start, name string) string {
			if tc.shell == "fish" {
				return fishLinesFor(script, name)
			}
			_, b, ok := strings.Cut(script, start)
			if !ok {
				t.Errorf("%s completion has no %s block", tc.shell, name)
				return ""
			}
			b, _, _ = strings.Cut(b, tc.end)
			return b
		}
		friction, fix := block(tc.friction, "friction"), block(tc.fix, "fix")
		// fish names a long flag "-l settle" where the others spell "--settle".
		flags := func(b string) string { return strings.ReplaceAll(b, "-l ", "--") }
		for _, flag := range []string{"--limit", "--json", "--settle", "--unsettle"} {
			if !strings.Contains(flags(friction), flag) {
				t.Errorf("%s friction block does not offer %s:\n%s", tc.shell, flag, friction)
			}
		}
		for _, flag := range []string{"--limit", "--json"} {
			if !strings.Contains(flags(fix), flag) {
				t.Errorf("%s fix block no longer offers %s:\n%s", tc.shell, flag, fix)
			}
		}
		for _, flag := range []string{"--settle", "--unsettle"} {
			if strings.Contains(flags(fix), flag) {
				t.Errorf("%s fix block offers %s, which only friction takes:\n%s", tc.shell, flag, fix)
			}
		}
		for _, want := range tc.takesValue {
			if !strings.Contains(friction, want) {
				t.Errorf("%s friction block completes the wall id after --settle; want %q in:\n%s", tc.shell, want, friction)
			}
		}
	}
}

// fishLinesFor is the fish script's lines that apply to one command: fish has
// no case block, each flag is a complete line whose first condition lists the
// commands it is for.
func fishLinesFor(script, name string) string {
	const prefix = "complete -c deja -n '__fish_seen_subcommand_from "
	var lines []string
	for line := range strings.SplitSeq(script, "\n") {
		rest, ok := strings.CutPrefix(line, prefix)
		if !ok {
			continue
		}
		cmds, _, _ := strings.Cut(rest, "'")
		cmds, _, _ = strings.Cut(cmds, ";")
		for c := range strings.FieldsSeq(cmds) {
			if c == name {
				lines = append(lines, line)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func shellCommandBlock(shell, script, cmd string) string {
	switch shell {
	case "fish":
		return strings.ReplaceAll(fishLinesFor(script, cmd), "-l ", "--")
	case "bash":
		_, body, ok := strings.Cut(script, `    case "$command" in`+"\n")
		if !ok {
			return ""
		}
		body, _, _ = strings.Cut(body, "\n    esac\n")
		return caseBranchFor(body, "        ", "            ;;", cmd, false)
	case "zsh":
		_, body, ok := strings.Cut(script, `  case "$words[2]" in`+"\n")
		if !ok {
			return ""
		}
		body, _, _ = strings.Cut(body, "\n  esac\n")
		return caseBranchFor(body, "    ", "      ;;", cmd, false)
	case "powershell":
		_, body, ok := strings.Cut(script, "        $candidates = switch ($command) {\n")
		if !ok {
			return ""
		}
		body, _, _ = strings.Cut(body, "\n        }\n")
		b := powershellBranchFor(body, cmd)
		if strings.Contains(b, "$defaultOptions") {
			if _, def, ok := strings.Cut(script, "$defaultOptions = @("); ok {
				def, _, _ = strings.Cut(def, ")")
				b += " " + def
			}
		}
		return b
	}
	return ""
}

func caseBranchFor(body, indent, endMarker, cmd string, _ bool) string {
	lines := strings.Split(body, "\n")
	var defaultBlock string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if !strings.HasPrefix(line, indent) || strings.HasPrefix(line, indent+" ") {
			continue
		}
		head, ok := strings.CutSuffix(strings.TrimPrefix(line, indent), ")")
		if !ok {
			continue
		}
		var blockLines []string
		for j := i + 1; j < len(lines); j++ {
			if lines[j] == endMarker {
				i = j
				break
			}
			blockLines = append(blockLines, lines[j])
		}
		block := strings.Join(blockLines, "\n")
		if head == "*" {
			defaultBlock = block
			continue
		}
		for c := range strings.SplitSeq(head, "|") {
			if c == cmd {
				return block
			}
		}
	}
	return defaultBlock
}

func powershellBranchFor(body, cmd string) string {
	var defaultBlock string
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "default {") {
			defaultBlock = line
			continue
		}
		if strings.HasPrefix(trimmed, "'"+cmd+"' {") ||
			(strings.HasPrefix(trimmed, "{ $_ -in @(") && strings.Contains(trimmed, "'"+cmd+"'")) {
			var blockLines []string
			_, rest, _ := strings.Cut(body, line)
			blockLines = append(blockLines, line)
			if !strings.HasSuffix(trimmed, "}") {
				for l := range strings.SplitSeq(rest, "\n") {
					if l == "" {
						continue
					}
					blockLines = append(blockLines, l)
					if l == "            }" {
						break
					}
				}
			}
			return strings.Join(blockLines, "\n")
		}
	}
	return defaultBlock
}

func TestEveryHelpEntryFlagIsOfferedByCompletion(t *testing.T) {
	scripts := map[string]string{}
	for _, shell := range completionTestShells {
		scripts[shell] = emittedCompletion(t, shell)
	}
	for _, entry := range helpEntries {
		if entry.toolHelp ||
			(strings.HasPrefix(entry.name, "hook-") && entry.name != "hook-context") {
			continue
		}
		usageText := strings.Join(entry.usage, " ")
		var flags []string
		for tok := range strings.FieldsSeq(usageText) {
			tok = strings.Trim(tok, "[]|")
			for part := range strings.SplitSeq(tok, "|") {
				if strings.HasPrefix(part, "--") && len(part) > 2 {
					flags = append(flags, part)
				}
			}
		}
		if len(flags) == 0 {
			continue
		}
		for _, shell := range completionTestShells {
			block := shellCommandBlock(shell, scripts[shell], entry.name)
			// install_targets / $installTargets holds --all and --auto for install and uninstall.
			if entry.name == "install" || entry.name == "uninstall" {
				block += " --all --auto"
			}
			for _, flag := range flags {
				if !strings.Contains(block, flag) {
					t.Errorf("%s completion for %q does not offer %s (documented in %q):\n%s",
						shell, entry.name, flag, usageText, block)
				}
			}
		}
	}
}

func TestEveryValueFlagIsGuardedInEveryShell(t *testing.T) {
	scripts := map[string]string{}
	for _, shell := range completionTestShells {
		scripts[shell] = emittedCompletion(t, shell)
	}

	valueFlagsByCmd := []struct {
		cmd   string
		flags []string
	}{
		{"blame", []string{"--harness", "--project", "--since"}},
		{"origin", []string{"--harness", "--project", "--limit"}},
		{"review", []string{"--harness", "--project", "--limit"}},
		{"bench", []string{"--seed"}},
		{"shell", []string{"-c"}},
		{"forget", []string{"--session", "--project", "--before", "--unforget"}},
		{"handoff", []string{"--to", "--project", "--harness"}},
		{"status", []string{"--harness"}},
		{"trail", []string{"--project", "--harness", "--limit"}},
		{"last", []string{"--from", "--harness", "--project", "--since", "--role"}},
		{"recall", []string{"--project", "--harness", "--limit", "--offset"}},
		{"remember", []string{"--project", "--tag"}},
		{"repo", []string{"--limit", "--since", "--kind"}},
		{"rules", []string{"--limit", "--since", "--project"}},
		{"restore", []string{"--span", "-o", "--out"}},
		{"secrets", []string{"--limit"}},
		{"stats", []string{"--html", "--card", "--harness", "--project", "--since", "--role"}},
		{"sync", []string{"--peer"}},
		{"show", []string{"--harness", "--offset", "--limit"}},
		{"view", []string{"--out"}},
		{"recap", []string{"--since", "--limit"}},
		{"files", []string{"--project", "--limit"}},
		{"how", []string{"--project", "--limit"}},
		{"promote", []string{"--state", "--note", "--tag", "--to"}},
		{"friction", []string{"--limit", "--settle", "--unsettle"}},
		{"fix", []string{"--limit"}},
		{"tests", []string{"--limit"}},
		{"init", []string{"--auto-lock"}},
		{"mcp", []string{"--http"}},
		{"search", []string{"--harness", "--project", "--since", "--role", "--session", "--limit", "--sort"}},
	}

	for _, tc := range valueFlagsByCmd {
		for _, shell := range completionTestShells {
			block := shellCommandBlock(shell, scripts[shell], tc.cmd)
			for _, flag := range tc.flags {
				switch shell {
				case "bash":
					// Value-taking flags must either be guarded in a `$prev` branch or,
					// for stats --html/--card, remain guarded by _arguments in zsh/fish.
					if tc.cmd == "stats" && (flag == "--html" || flag == "--card") {
						continue
					}
					if !strings.Contains(block, `"`+flag+`"`) {
						t.Errorf("bash %s block does not guard value flag %s in $prev check:\n%s", tc.cmd, flag, block)
					}
				case "powershell":
					if tc.cmd == "stats" && (flag == "--html" || flag == "--card") {
						continue
					}
					if !strings.Contains(block, "$previous") || !strings.Contains(block, "'"+flag+"'") {
						t.Errorf("powershell %s block does not guard value flag %s in $previous check:\n%s", tc.cmd, flag, block)
					}
				case "zsh":
					want := flag + "="
					if flag == "-c" {
						want = "-c["
					}
					if !strings.Contains(block, want) {
						t.Errorf("zsh %s block does not declare value spec %s:\n%s", tc.cmd, want, block)
					}
				case "fish":
					raw := fishLinesFor(scripts["fish"], tc.cmd)
					fishFlag := "-l " + strings.TrimPrefix(flag, "--")
					switch flag {
					case "-c":
						fishFlag = "-s c"
					case "-o":
						fishFlag = "-s o"
					}
					var matched bool
					for line := range strings.SplitSeq(raw, "\n") {
						if strings.Contains(line, fishFlag) && (strings.Contains(line, " -r") || strings.Contains(line, " -x")) {
							matched = true
							break
						}
					}
					if !matched {
						t.Errorf("fish %s block does not mark %s with -r or -x:\n%s", tc.cmd, flag, raw)
					}
				}
			}
		}
	}

	for _, shell := range completionTestShells {
		block := shellCommandBlock(shell, scripts[shell], "repo")
		for _, sub := range []string{"candidates", "add", "rm", "init", "sync", "on", "off"} {
			if !strings.Contains(block, sub) {
				t.Errorf("%s repo block does not offer subcommand %q:\n%s", shell, sub, block)
			}
		}
	}
}

// A shell with `set -u` is a shell people actually run — it is standard advice
// and it follows them into interactive use. The function guarded `prev` against
// an unbound index and read COMP_WORDS[1] and [2] bare, so the first Tab after
// `deja ` printed "COMP_WORDS[2]: unbound variable" and no candidates (#1656).
func TestBashCompletionSurvivesNounset(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	script := bashCompletion
	// The state right after `deja <Tab>`: two words, the cursor on the second.
	const drive = `
set -u
COMP_WORDS=(deja '')
COMP_CWORD=1
_deja_completion
printf '%s\n' "${#COMPREPLY[@]}"
`
	out, err := shellScriptCommand(context.Background(), t, bash, nil, script+drive).CombinedOutput()
	got := string(out)
	if err != nil {
		t.Fatalf("completion failed under set -u: %v\n%s", err, got)
	}
	if strings.Contains(got, "unbound variable") {
		t.Errorf("unbound variable under set -u:\n%s", got)
	}
	if strings.TrimSpace(got) == "0" {
		t.Errorf("no candidates offered:\n%s", got)
	}
}

// Runs the emitted bash completion: after `show --offset` or `show --limit`
// the next word is a count, so no flag may be offered there (#5940).
func TestBashCompletionShowCounts(t *testing.T) {
	withTempStores(t)
	t.Setenv("BASH_ENV", "")
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	for _, tc := range []struct {
		words     string
		wantFlags bool
	}{
		{"deja show --offset ''", false},
		{"deja show --limit ''", false},
		{"deja show abc --offset ''", false},
		{"deja show --offset 5 ''", true},
		{"deja show ''", true},
	} {
		t.Run(tc.words, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// words contains only the fixed test cases above, never user input.
			drive := "\nCOMP_WORDS=(" + tc.words + ")\nCOMP_CWORD=$((${#COMP_WORDS[@]}-1))\n_deja_completion\nprintf '%s\\n' \"${COMPREPLY[@]}\"\n"
			out, err := shellScriptCommand(ctx, t, bash, []string{"--noprofile", "--norc"}, emittedCompletion(t, "bash")+drive).CombinedOutput()
			if err != nil {
				t.Fatalf("bash completion failed: %v\n%s", err, out)
			}
			got := strings.Fields(string(out))
			if tc.wantFlags && len(got) != len(showFlags) {
				t.Errorf("offers %q, want show's flags %q", got, showFlags)
			}
			if !tc.wantFlags && len(got) != 0 {
				t.Errorf("offers %q where a count is expected", got)
			}
		})
	}
}

// PowerShell 5.1 writes UTF-8 with a byte order mark by default, and editors
// on Windows do too, so a config that has been through one refused every JSON
// target deja has — with a remedy naming a character nobody can see (#3696).
// The mark belongs with the line endings and the indent: read past it, and put
// it back on what is written.
func TestAConfigWithAByteOrderMarkIsStillInstallable(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	seed := func(rel, body string) string {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, append(append([]byte(nil), utf8BOM...), body...), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	files := map[string]string{
		".claude.json":            `{"editorMode":"vim"}`,
		".claude/settings.json":   `{"model":"opus"}`,
		".cursor/mcp.json":        `{"mcpServers":{}}`,
		".gemini/settings.json":   `{"theme":"dark"}`,
		".qwen/settings.json":     `{"theme":"dark"}`,
		".zcode/cli/setting.json": `{}`,
	}
	paths := map[string]string{}
	for rel, body := range files {
		paths[rel] = seed(rel, body)
	}
	for _, target := range []string{"claude-code", "claude-auto", "cursor", "gemini", "gemini-auto", "qwen", "qwen-auto", "zcode", "zcode-auto"} {
		out, err := captureRun(t, "install", target, "--no-index")
		if err != nil {
			t.Errorf("install %s refused a config with a byte order mark: %v", target, err)
			continue
		}
		if strings.Contains(out, "refused") {
			t.Errorf("install %s refused a config with a byte order mark:\n%s", target, out)
		}
	}
	for rel, keptKey := range map[string]string{
		".claude.json":          "editorMode",
		".claude/settings.json": "model",
		".gemini/settings.json": "theme",
		".qwen/settings.json":   "theme",
	} {
		b, err := os.ReadFile(paths[rel])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(b, utf8BOM) {
			t.Errorf("%s came back without the mark it had", rel)
		}
		var root map[string]any
		if err := json.Unmarshal(bytes.TrimPrefix(b, utf8BOM), &root); err != nil {
			t.Fatalf("%s is not readable after an install: %v", rel, err)
		}
		if _, ok := root[keptKey]; !ok {
			t.Errorf("%s lost the reader's own %q", rel, keptKey)
		}
		if !mentionsDeja(b) {
			t.Errorf("%s carries no wiring of deja's after an install: %s", rel, b)
		}
	}
}

// And a mark is not added to a file that had none: the file keeps the shape its
// owner's tools give it, in both directions.
func TestNoByteOrderMarkIsAddedToAConfigWithoutOne(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(p, []byte("{\n  \"editorMode\": \"vim\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(b, utf8BOM) {
		t.Errorf("install put a byte order mark on a file that had none")
	}
}

// The MCP context reply marked its own trim but sent the agent to `deja ctx
// <id>` "for the whole one", which cuts at the same 8 KB, and to
// recall_context, a tool the one-tool server does not list (#5392).
func TestTheContextReplyPointsAtAReadThatIsWhole(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "kkkk5392-4444-4000-8000-c3d4e5f6a7b8"
	line := func(role, text string) string {
		return `{"type":"` + role + `","message":{"role":"` + role + `","content":` + quoteJSON(text) +
			`},"timestamp":"2026-07-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
	}
	body := line("user", "here is the build log "+strings.Repeat("filler ", 6000)) +
		line("assistant", "the root cause is the stale lockfile")
	if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	text, err := callMCPTool(dir, "deja", json.RawMessage(`{"mode":"context","q":"`+id+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "recall_context") || strings.Contains(text, "for the whole one") {
		t.Errorf("the reply names a removed tool or calls a cut read whole:\n%s", text[max(0, len(text)-400):])
	}
	if !strings.Contains(text, "`deja show "+id+"`") {
		t.Errorf("the reply does not say where the rest is:\n%s", text[max(0, len(text)-400):])
	}
	if !strings.Contains(text, "the last turn not among them") && !strings.Contains(text, "stale lockfile") {
		t.Errorf("the final turn is neither shown nor reported missing:\n%s", text[max(0, len(text)-400):])
	}
}

// The changes block stopped at its room and said nothing about the edits it
// left out, so it read as everything the session changed (#5392).
func TestTheChangesBlockCountsWhatDidNotFit(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "cccc5392-1111-4000-8000-d6e7f8a9b0c1"
	var b strings.Builder
	n := 0
	line := func(typ string, content any) {
		n++
		raw := mustMarshal(t, map[string]any{
			"type": typ, "sessionId": id, "cwd": "/proj",
			"timestamp": fmt.Sprintf("2026-07-01T10:%02d:%02dZ", n/60, n%60),
			"message":   map[string]any{"role": typ, "content": content},
		})
		b.Write(raw)
		b.WriteByte('\n')
	}
	line("user", "rename the handler everywhere")
	const files = 12
	for i := range files {
		line("assistant", []any{map[string]any{"type": "tool_use", "id": fmt.Sprintf("t%d", i), "name": "Edit",
			"input": map[string]string{"file_path": fmt.Sprintf("/proj/pkg/f%02d.go", i),
				"old_string": "oldHandler()", "new_string": "newHandler()\n" + strings.Repeat("// moved\n", 120)}}})
	}
	if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	s, ok, err := index.FindByPrefix(dir, id)
	if err != nil || !ok {
		t.Fatalf("session not found: %v", err)
	}
	blk := changesBlock(s, changesBlockMax)
	shown := strings.Count(blk, "pkg/f")
	if shown == 0 || shown >= files {
		t.Fatalf("the fixture should overflow the block: %d of %d shown", shown, files)
	}
	if want := fmt.Sprintf("(%d more changes not shown)", files-shown); !strings.Contains(blk, want) {
		t.Errorf("the block does not count the %d changes it left out:\n%s", files-shown, blk[max(0, len(blk)-300):])
	}
	if len(blk) > changesBlockMax+1 {
		t.Errorf("the count pushed the block to %d bytes, over its %d", len(blk), changesBlockMax)
	}
}

// When a query's subject is a word no session holds, the stemmed tier answers
// on what is left. The counted page says which words it threw away — "ignored:
// no session matches it with the rest" — and recall_context, which returns a
// whole session, printed only `[stemmed]`. So the tool that hands an agent the
// most text was the one that did not say the question's subject had been
// dropped, which is the failure #2074 is about (#2827).
func TestTheContextDigestNamesTheWordsItIgnored(t *testing.T) {
	dir := manySessionStore(t, 40)

	// "quibblesnatch" is in the store, "wombatron" is in no session, so the
	// tier answers on the rest and drops the invented word.
	text, err := callMCPTool(dir, "recall_context", []byte(`{"query":"the wombatron quibblesnatch parser rejects frames"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "# deja context:") {
		t.Fatalf("nothing was served, so there is nothing to caveat:\n%s", firstLines(text, 4))
	}
	if !strings.Contains(text, "wombatron") {
		t.Errorf("the word the search dropped is not named:\n%s", firstLines(text, 4))
	}
	if !strings.Contains(text, "ignored") {
		t.Errorf("the digest does not say the word was ignored:\n%s", firstLines(text, 4))
	}
}

// And a query every word of which the store holds says nothing of the sort.
func TestTheContextDigestIsQuietWhenNothingWasIgnored(t *testing.T) {
	dir := manySessionStore(t, 40)
	if _, err := index.AllMeta(dir); err != nil {
		t.Fatal(err)
	}

	text, err := callMCPTool(dir, "recall_context", []byte(`{"query":"quibblesnatch parser"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "ignored") {
		t.Errorf("nothing was dropped and the digest said otherwise:\n%s", firstLines(text, 4))
	}
}

// "No session is about this" was printed whenever the relevance tier answered,
// which on any store with competition is most questions — including the ones
// whose answer is served at rank 1. In real-world stores, 20 of 20
// questions lifted verbatim out of indexed sessions were disowned that way, and
// an agent told the right answer is about nothing learns to ignore the line —
// which costs what #2074 bought with it (#2827).
func TestARelevanceAnswerThatNamesTheAskedIsNotDisowned(t *testing.T) {
	dir := manySessionStore(t, 40)

	// Reaches the relevance tier — no session holds every word — and the
	// session it serves is the one that says "quibblesnatch".
	text, err := callMCPTool(dir, "recall_context", []byte(`{"query":"what did the quibblesnatch parser do with the stalled pipeline frames"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "so this one was ranked") {
		t.Fatalf("the fixture no longer reaches the relevance tier, so this guards nothing:\n%s", firstLines(text, 4))
	}
	if strings.Contains(text, "No session is about this") {
		t.Errorf("a session that names what was asked was disowned:\n%s", firstLines(text, 4))
	}
	if !strings.Contains(text, "rare") {
		t.Errorf("the session holding the asked-about word was not the one served:\n%s", firstLines(text, 6))
	}
}

// And the case the line exists for keeps it: a subject this store has never
// held, served its nearest neighbour, under the caveat.
//
// The fixture is built for that and not borrowed: the question's ordinary words
// have to be in the store — otherwise the tier refuses and serves nothing,
// which is the other right answer and not this case — and no session may hold
// all of them, or an earlier tier answers and this code is never reached. So
// they go one per session.
func TestARelevanceAnswerAboutSomethingAbsentKeepsTheCaveat(t *testing.T) {
	dir := spreadWordsStore(t)

	text, err := callMCPTool(dir, "recall_context", []byte(`{"query":"which crystal did we pick for the antenna array tuning"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "# deja context:") {
		t.Fatalf("nothing was served, so the caveat this test is about was never reached:\n%s", firstLines(text, 4))
	}
	if !strings.Contains(text, "No session is about this") {
		t.Errorf("a subject the store never held was reported as named:\n%s", firstLines(text, 4))
	}
}

// recall_context serves one session and said nothing about the rest, so a
// digest chosen from forty candidates read exactly like the only thing deja
// held. That is the misread #1308 fixed for the counted page — "(5 match(es))"
// reads as five exist — and blame already names what it left out.
//
// The tool returns one digest on purpose; what was missing is the number the
// agent needs to decide whether one is enough.
func TestTheContextDigestSaysHowManyItChoseFrom(t *testing.T) {
	dir := manySessionStore(t, 40)

	text, err := callMCPTool(dir, "recall_context", json.RawMessage(`{"query":"pipeline stalled retry"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "# deja context:") {
		t.Fatalf("nothing was served, so there is nothing to count:\n%s", firstLines(text, 4))
	}
	if !strings.Contains(text, "other sessions matched") {
		t.Errorf("the digest does not say it was chosen from several:\n%s", firstLines(text, 5))
	}
	if !strings.Contains(text, "recall") {
		t.Errorf("nothing points at the tool that lists the others:\n%s", firstLines(text, 5))
	}
}

// The number itself, on a store where it is known: three sessions say
// "pipeline … stalled on retry", so the one served leaves two behind. Asserting
// only the sentence let an off-by-one through — the count is the whole point of
// the line.
func TestTheContextDigestCountsTheOthersCorrectly(t *testing.T) {
	dir := manySessionStore(t, 3)

	text, err := callMCPTool(dir, "recall_context", json.RawMessage(`{"query":"pipeline stalled retry"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "# deja context:") {
		t.Fatalf("nothing was served, so there is nothing to count:\n%s", firstLines(text, 4))
	}
	if !strings.Contains(text, "2 other sessions matched") {
		t.Errorf("the count is wrong — three sessions match and one was served:\n%s", firstLines(text, 6))
	}
}

// One match is one match: a sentence about others would be a lie, and an agent
// told to go looking wastes a call.
func TestTheContextDigestIsQuietWhenItWasTheOnlyMatch(t *testing.T) {
	dir := manySessionStore(t, 1)

	text, err := callMCPTool(dir, "recall_context", json.RawMessage(`{"query":"quibblesnatch"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "# deja context:") {
		t.Fatalf("nothing was served, so this guards nothing:\n%s", firstLines(text, 4))
	}
	if strings.Contains(text, "other sessions matched") {
		t.Errorf("the only match was reported as one of several:\n%s", firstLines(text, 5))
	}
}

// A probe on the two string helpers, so a failure says which one moved the
// text rather than only that the file came out wrong.
func TestContinueYAMLHelpers(t *testing.T) {
	doc := "name: mine\nmcpServers:\n  - name: sqlite\n    command: uvx\nrules:\n  - be brief\n"
	got, _ := appendYAMLListItem(doc, "mcpServers", "  - name: deja\n    command: /bin/deja\n")
	want := "name: mine\nmcpServers:\n  - name: sqlite\n    command: uvx\n  - name: deja\n    command: /bin/deja\nrules:\n  - be brief\n"
	if got != want {
		t.Fatalf("append put the item in the wrong place:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
	back := removeContinueItem(got, "mcpServers", "deja")
	if back != doc {
		t.Fatalf("remove did not put the file back:\n--- got ---\n%q\n--- want ---\n%q", back, doc)
	}
	// A file with no such key gets one.
	fresh, _ := appendYAMLListItem("name: mine\n", "prompts", "  - name: deja\n")
	if fresh != "name: mine\nprompts:\n  - name: deja\n" {
		t.Fatalf("append did not write the missing key:\n%q", fresh)
	}
	// And the key at the end of the file, with no line after it.
	tail, _ := appendYAMLListItem("name: mine\nprompts:\n  - name: other\n", "prompts", "  - name: deja\n")
	if !strings.HasSuffix(tail, "  - name: other\n  - name: deja\n") {
		t.Fatalf("append at the end of the file:\n%q", tail)
	}
}

// Copilot CLI 1.0.91 sends postToolUse for a command that exited non-zero,
// in camelCase, with the output under toolResult.textResultForLlm, and reads
// only a flat {"additionalContext": ...} back. Measured on a stand: that text
// is appended to the tool result the model reads in the same turn.
func TestFixPairAnswersCopilotPostToolUse(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	payload := mustMarshal(t, map[string]any{
		"sessionId": "copilot-1",
		"cwd":       "/work/app",
		"toolName":  "bash",
		"toolArgs":  `{"command": "make"}`,
		"toolResult": map[string]any{
			"resultType":       "success",
			"textResultForLlm": "panic: sql: database is closed\n<shellId: 0 completed with exit code 2>",
		},
	})
	copilotHookOutput = true
	t.Cleanup(func() { copilotHookOutput = false })
	var strike1, out bytes.Buffer
	if err := runHookToolAfterMode(os.Getenv("DEJA_INDEX_DIR"), bytes.NewReader(payload), &strike1, false); err != nil {
		t.Fatal(err)
	}
	if err := runHookToolAfterMode(os.Getenv("DEJA_INDEX_DIR"), bytes.NewReader(payload), &out, false); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not the flat shape Copilot reads: %q: %v", out.String(), err)
	}
	if len(got) != 1 || !strings.Contains(got["additionalContext"], "make clean && make CGO_ENABLED=0") {
		t.Fatalf("Copilot got no fix line, or more than additionalContext: %v", got)
	}
}

func TestCopilotInstallWiresTheFailureLine(t *testing.T) {
	for _, h := range copilotHooks {
		if h.event == "postToolUse" && strings.Join(h.args, " ") == "hook-tool-after --copilot" {
			return
		}
	}
	t.Fatal("copilot-auto does not wire postToolUse to hook-tool-after --copilot")
}

// A question that begins with a dash reads as a flag, and the three verbs that
// refuse it said so without saying what to do instead. `--` has worked all
// along — the Amp plugin has used it since it was written — so the error names
// it now (#3395).
func TestADashLeadingQueryIsToldAboutTheSeparator(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	for _, c := range []struct {
		name string
		run  func() error
	}{
		{"ctx", func() error { return cmdCtx(dir, []string{"--auto looks for the six harnesses"}) }},
		{"how", func() error { return runHow(dir, []string{"--auto looks for the six harnesses"}, io.Discard) }},
	} {
		err := c.run()
		if err == nil {
			t.Errorf("%s: a dash-leading query was accepted; the test no longer covers the message", c.name)
			continue
		}
		if !strings.Contains(err.Error(), "goes after `--`") {
			t.Errorf("%s: %v — the message does not say a dash-leading query goes after `--`", c.name, err)
		}
	}
}
