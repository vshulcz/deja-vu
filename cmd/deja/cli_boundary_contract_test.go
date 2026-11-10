package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

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
	// typos like `--harnses=claude`, reject cross-command flags like
	// `--before=30d`, and reject trailing `--bogus=1`.
	o, err := parseSearch([]string{"--", "--limit=5"})
	if err != nil || o.Query != "--limit=5" {
		t.Fatalf("parseSearch(-- --limit=5) = (%#v, %v), want Query='--limit=5'", o, err)
	}
	for _, bad := range [][]string{
		{"--harnses=claude", "retry"},
		{"--before=30d", "retry"},
		{"--settle=3f9a01c2", "retry"},
		{"goroutines", "--bogus=1"},
	} {
		if got, perr := parseSearch(bad); perr == nil {
			t.Fatalf("parseSearch(%q) succeeded with Query=%q; want flag error", bad, got.Query)
		}
	}

	// 2. Unit-testable flag parsers must produce identical options for
	// `--flag=value` and `--flag value`.
	s1, err := parseShow([]string{"--harness=claude", "--offset=2", "--limit=10", "019fa282"})
	if err != nil || s1.harness != "claude" || s1.offset != 2 || s1.limit != 10 || s1.id != "019fa282" {
		t.Fatalf("parseShow(--flag=value) = (%#v, %v)", s1, err)
	}
	n1, l1, sr1, err := parseLast([]string{"--harness=claude", "--project=app", "--since=7d", "--role=user", "--from=local", "5"})
	if err != nil || n1 != 5 || l1.Harness != "claude" || l1.Project != "app" || sr1 != "7d" || l1.Role != "user" || l1.From != "local" {
		t.Fatalf("parseLast(--flag=value) = (%d, %#v, %q, %v)", n1, l1, sr1, err)
	}
	bp, bo, _, err := parseBlame([]string{"--harness=claude", "--project=app", "--since=7d", "main.go"})
	if err != nil || bp != "main.go" || bo.Harness != "claude" || bo.Project != "app" || bo.Since == 0 {
		t.Fatalf("parseBlame(--flag=value) = (%q, %#v, %v)", bp, bo, err)
	}
	fo, err := parseFilesArgs([]string{"--project=app", "--limit=4", "auth"})
	if err != nil || fo.project != "app" || fo.limit != 4 || len(fo.terms) != 1 || fo.terms[0] != "auth" {
		t.Fatalf("parseFilesArgs(--flag=value) = (%#v, %v)", fo, err)
	}
	st, err := parseStatsArgs([]string{"--harness=claude", "--project=app", "--since=7d", "--role=user"})
	if err != nil || st.options.Harness != "claude" || st.options.Project != "app" || st.sinceRaw != "7d" || st.options.Role != "user" {
		t.Fatalf("parseStatsArgs(--flag=value) = (%#v, %v)", st, err)
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
	if !embed.Off() || !embedOff() {
		t.Fatal("DEJA_EMBED=' OFF ' must switch off both embed.Off() and embedOff()")
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
