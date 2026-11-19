package index

import (
	"bytes"
	"context"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// An incremental append grows records.bin before it rewrites the manifest, so a
// concurrent lock-free reader sees the new size against the old stamp. That must
// not read as corruption (a needless full rebuild / a hard error), because the
// extra bytes are an uncommitted tail no bucket references.
func TestSearchToleratesAnInFlightAppend(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{{
		Harness: "claude", ID: "a", Project: "p",
		Messages: []model.Message{{Role: "user", Text: "jwt refresh rotation reconciler"}},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	// Simulate the append window: records.bin is longer than the manifest's
	// committed size (an uncommitted tail).
	rp := filepath.Join(dir, "records.bin")
	f, err := os.OpenFile(rp, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("uncommitted appended tail bytes not in any bucket"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	// The read path must still answer, not fail as corrupt.
	res, err := SearchDetailed(dir, query.Options{Query: "reconciler"})
	if err != nil {
		t.Fatalf("a longer records.bin surfaced as an error: %v", err)
	}
	if len(res.Sessions) == 0 {
		t.Fatal("the committed session was not found through the in-flight window")
	}

	// A SHORTER records.bin is still real truncation.
	if err = os.Truncate(rp, 10); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if recordsReadable(dir, m) {
		t.Error("a truncated records.bin was accepted as readable")
	}
}

// The keyless/regex query full-scans records.bin to EOF, so it actually reads
// the uncommitted tail. A garbage tail (a valid session's record appended but a
// second, torn or unknown-session record after it) must not corrupt the answer:
// the committed session is still found, and the tail contributes nothing.
func TestRegexScanToleratesAnInFlightTail(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{{
		Harness: "claude", ID: "a", Project: "p",
		Messages: []model.Message{{Role: "user", Text: "REGEXMARK committed content"}},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	// Append a plausible-looking but uncommitted tail (a short length header +
	// bytes), the shape an in-flight append leaves before the manifest updates.
	rp := filepath.Join(dir, "records.bin")
	f, err := os.OpenFile(rp, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// A 4-byte little-endian length header claiming a small record, then a
	// truncated payload — decodes to a short read, treated as a clean EOF.
	if _, err = f.Write([]byte{20, 0, 0, 0, 'u', 'n', 'c', 'o', 'm', 'm'}); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	// A regex query takes the full-scan path (no postings keys).
	res, err := SearchDetailed(dir, query.Options{Query: "REGEXMARK", Regex: true})
	if err != nil {
		t.Fatalf("the full-scan path errored on an in-flight tail: %v", err)
	}
	if len(res.Sessions) == 0 {
		t.Fatal("the committed session was lost on the full-scan path")
	}
}

// The audit issue #46 asks for proof that no path from parser to disk skips
// redaction: message text, tool-result payloads and metadata like titles.
// Secrets are planted per harness (including a grok generated_title and a
// claude tool_result block), then every byte of the built index is scanned.
func TestNoPlaintextSecretReachesIndex(t *testing.T) {
	root, dir := allHarnessEnv(t)
	secrets := map[string]string{ //nolint:gosec // a fake secret the redaction test plants
		"claude-text":  "AKIAIOSFODNN7EXAMPL1",
		"claude-tool":  "ghp_toolresultsecret0123456789abcdef",
		"grok-title":   "sk-ant-api03-titlesecret0123456789",
		"aider-text":   "xoxb-1234567890-aidersecret",
		"codex-text":   "glpat-codexsecret0123456789",
		"gemini-text":  "hf_geminisecret0123456789abcd",
		"claude-title": "sk-proj-firsttitle0123456789abcdef",
	}
	write(t, filepath.Join(root, "claude", "-p-app", "s1.jsonl"),
		`{"type":"user","sessionId":"s1","timestamp":"2026-02-01T10:00:00Z","message":{"role":"user","content":"leaked key `+secrets["claude-text"]+` in text"}}`+"\n"+
			`{"type":"user","sessionId":"s1","timestamp":"2026-02-01T10:00:01Z","message":{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"tool output with `+secrets["claude-tool"]+`"}]}]}}`+"\n")
	write(t, filepath.Join(root, "claude", "-p-app", "s2.jsonl"),
		`{"type":"user","sessionId":"s2","timestamp":"2026-02-01T11:00:00Z","message":{"role":"user","content":"please use `+secrets["claude-title"]+` to auth"}}`+"\n")
	write(t, filepath.Join(root, "codex", "sessions", "2026", "02", "01", "rollout-2026-02-01T10-00-00-x1.jsonl"),
		`{"type":"session_meta","timestamp":"2026-02-01T10:00:00Z","payload":{"session_id":"x1","cwd":"/p/app"}}`+"\n"+
			`{"timestamp":"2026-02-01T10:00:01Z","payload":{"role":"user","content":"token `+secrets["codex-text"]+`"}}`+"\n")
	write(t, filepath.Join(root, "gemini", "tmp", "s", "chats", "session-g1.json"),
		`{"sessionId":"g1","startTime":"2026-02-01T10:00:00.000Z","lastUpdated":"2026-02-01T10:00:00.000Z","messages":[{"id":"m1","timestamp":"2026-02-01T10:00:01.000Z","type":"user","content":"secret `+secrets["gemini-text"]+`"}]}`)
	write(t, filepath.Join(root, "aiderroot", ".aider.chat.history.md"),
		"# aider chat started at 2026-02-01 10:00:00\n\n#### paste "+secrets["aider-text"]+" here\n\nok\n")
	grokDir := filepath.Join(root, "grok", "sessions", "%2Fp%2Fapp")
	write(t, filepath.Join(grokDir, "gr1", "summary.json"),
		`{"info":{"id":"gr1","cwd":"/p/app"},"generated_title":"session about `+secrets["grok-title"]+`","created_at":"2026-02-01T10:00:00Z","updated_at":"2026-02-01T10:05:00Z"}`)
	write(t, filepath.Join(grokDir, "gr1", "updates.jsonl"),
		`{"timestamp":1769940000000,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hello grok"},"_meta":{"promptIndex":0}},"_meta":{"promptId":"p0"}}}`+"\n")

	if err := EnsureForSearch(dir, search.Options{Query: "x", All: true}, true, nil); err != nil {
		t.Fatal(err)
	}

	var files []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 3 {
		t.Fatalf("index looks empty: %v", files)
	}
	for _, p := range files {
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for name, secret := range secrets {
			if strings.Contains(string(b), secret) {
				t.Errorf("plaintext %s secret survived in %s", name, filepath.Base(p))
			}
		}
	}

	// Titles specifically: the grok generated_title and the first-message
	// title must come back scrubbed, not dropped.
	ss, err := Search(dir, search.Options{Query: "hello grok", All: true})
	if err != nil || len(ss) == 0 {
		t.Fatalf("grok session lost: %v %v", ss, err)
	}
	if !strings.Contains(ss[0].Title, "[redacted:") || strings.Contains(ss[0].Title, secrets["grok-title"]) {
		t.Fatalf("grok title not redacted: %q", ss[0].Title)
	}
}

// The carry-forward drops the old counts of a file the pass is re-reading, so
// the fresh read is the whole story. It decided that by file kind while the key
// became the store in #2239, so for five stores nothing matched and every
// incremental pass added the file's secrets on top of the ones already counted
// (#2240).
func TestAReReadFileDoesNotCountItsRedactionsTwice(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	cline := filepath.Join(tmp, "cline")
	if err := os.MkdirAll(cline, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLINE_ROOT", cline)

	secret := "ghp_" + strings.Repeat("a", 36)
	stamp := time.Now().Add(-time.Hour).UnixMilli()
	path := filepath.Join(cline, "one.messages.json")
	write := func(turns int) {
		t.Helper()
		var b strings.Builder
		b.WriteString(`{"agent":"lead","messages":[`)
		for i := range turns {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"ts":%d,"role":"user","content":"the token is %s"}`, stamp+int64(i), secret)
		}
		b.WriteString("]}")
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	counted := func() int {
		t.Helper()
		stats, err := Redactions(filepath.Join(tmp, "index.db"))
		if err != nil {
			t.Fatal(err)
		}
		return stats.Rules["cline"]["github-token"]
	}

	dir := filepath.Join(tmp, "index.db")
	write(1)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The premise: one secret in the file, one on the counter.
	if got := counted(); got != 1 {
		t.Fatalf("the first build counted %d of one secret, so this measures nothing", got)
	}

	write(2)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := counted(); got != 2 {
		t.Errorf("two secrets in the file, %d counted after an incremental pass", got)
	}

	// And again, to catch a carry that only shows on the second repeat.
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := counted(); got != 2 {
		t.Errorf("a pass with nothing new to read moved the count to %d", got)
	}
}

// And the same file, in an index whose rules were written before the fold: the
// stale kind key has to go when its file is re-read, or the report folds it
// into the fresh count and reports both (#2240).
func TestAStaleKindKeyIsDroppedWhenItsFileIsReRead(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	cline := filepath.Join(tmp, "cline")
	if err := os.MkdirAll(cline, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLINE_ROOT", cline)

	secret := "ghp_" + strings.Repeat("a", 36)
	stamp := time.Now().Add(-time.Hour).UnixMilli()
	path := filepath.Join(cline, "one.messages.json")
	turn := func(i int) string {
		return fmt.Sprintf(`{"ts":%d,"role":"user","content":"the token is %s"}`, stamp+int64(i), secret)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := os.WriteFile(path, []byte(`{"agent":"lead","messages":[`+turn(0)+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The manifest as a deja from before the fold left it.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.RedactionRules = map[string]int{"cline-sdk:github-token": 1}
	if err = writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}

	if err = os.WriteFile(path, []byte(`{"agent":"lead","messages":[`+turn(0)+`,`+turn(1)+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	stats, err := Redactions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := stats.Rules["cline"]["github-token"]; got != 2 {
		t.Errorf("two secrets in the file, the report says %d", got)
	}
}

// `deja stats` prints redaction counts grouped by these keys, so a cline user
// read a heading — "cline-sdk" — that no other screen uses. #2234 fixed the
// same shape for ingest_health and left this counter alone (#2238).
func TestRedactionCountsAreFiledUnderTheStore(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	cline := filepath.Join(tmp, "cline")
	t.Setenv("DEJA_CLINE_ROOT", cline)
	if err := os.MkdirAll(cline, 0o755); err != nil {
		t.Fatal(err)
	}

	secret := "ghp_" + strings.Repeat("a", 36)
	stamp := time.Now().Add(-time.Hour).UnixMilli()
	body := fmt.Sprintf(`{"agent":"lead","messages":[{"ts":%d,"role":"user","content":"the token is %s"}]}`, stamp, secret)
	if err := os.WriteFile(filepath.Join(cline, "one.messages.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"s1","timestamp":%q,"cwd":"/work/app",`+
		`"message":{"role":"user","content":"the token is %s"}}`, at, secret)
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	stats, err := Redactions(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: both stores had something redacted, or the names prove
	// nothing.
	if len(stats.Rules) < 2 {
		t.Fatalf("only %d store reported a redaction: %v", len(stats.Rules), stats.Rules)
	}
	for name := range stats.Rules {
		if sources.HarnessForKind(name) != name {
			t.Errorf("redactions are filed under %q, which is a file kind; every screen says %q",
				name, sources.HarnessForKind(name))
		}
	}
	if _, ok := stats.Rules["cline"]; !ok {
		t.Errorf("nothing is filed under the name the rest of deja prints: %v", stats.Rules)
	}
}

// An index written before the fix holds keys by file kind, and a pass since
// then writes them by store — one manifest, both shapes. The reader folds, or
// the screen shows two stores where there is one (#2238).
func TestOldKindKeysFoldIntoTheStoreOnRead(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{
		Version: version,
		RedactionRules: map[string]int{
			"cline-sdk:github-token":    2,
			"cline:github-token":        1,
			"cline-vscode:aws-key":      3,
			"claude:github-token":       4,
			"nothing-like-a-store:rule": 5,
		},
	}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	stats, err := Redactions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := stats.Rules["cline"]["github-token"]; got != 3 {
		t.Errorf("the two spellings of one store report %d github tokens, want 2+1", got)
	}
	if got := stats.Rules["cline"]["aws-key"]; got != 3 {
		t.Errorf("the other kind of the same store reports %d aws keys, want 3", got)
	}
	if _, ok := stats.Rules["cline-sdk"]; ok {
		t.Errorf("a file kind is still a heading: %v", stats.Rules)
	}
	if got := stats.Rules["claude"]["github-token"]; got != 4 {
		t.Errorf("a store whose kind carries its own name reports %d, want 4", got)
	}
	// A name deja does not know is left as it is rather than dropped: it came
	// from somewhere, and losing the count would be worse than an odd heading.
	if got := stats.Rules["nothing-like-a-store"]["rule"]; got != 5 {
		t.Errorf("an unknown name reports %d, want 5", got)
	}
}

// ProjectRelevant returns the sessions and, beside them, how many informative
// and strong terms each one holds. The per-prompt hook reads them positionally
// — `for i, s := range ranked { … matched[i] … }` — so the two have to be the
// same length and in the same order.
//
// They are not. The ranking is cut to n as metas, and the sessions are loaded
// afterwards through sessionsServable, which drops what the trust policy
// ignores (#2541) and what a query is not allowed to be served. Every session
// after a dropped one is then judged by its neighbour's counts (#2546).
func TestRelevantCountsStayBesideTheirSessions(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	root := filepath.Join(home, "claude")
	// Both leaves carry the project slug, so the two sessions land in the same
	// project and only the path tells them apart.
	jobs := filepath.Join(root, ".claude", "jobs", "abc", "-w-app")
	realPath := filepath.Join(root, "-w-app")
	for _, d := range []string{jobs, realPath} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(dir, sid, day, text string) {
		line := func(role, content string) string {
			return `{"type":"` + role + `","sessionId":"` + sid + `","cwd":"/w/app","timestamp":"2026-01-` +
				day + `T03:04:05Z","message":{"role":"` + role + `","content":"` + content + `"}}`
		}
		body := strings.Join([]string{
			line("user", text),
			line("assistant", "Decision: "+text),
		}, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The ignored session holds both rare words, so it ranks first; the two
	// real ones hold one each.
	write(jobs, "scratch", "05", "the quokkactl run hit snorblefunc under load")
	write(realPath, "keeper", "04", "the quokkactl run needed a second pass")
	write(realPath, "other", "03", "snorblefunc came back from the vendor tree")
	// Filler, so the two words are rare in this corpus: on a store of three
	// sessions nothing clears the idf floor and the ranking answers nothing.
	for i, topic := range []string{"the sidebar layout", "the invoice renderer", "the webpack config",
		"the login throttle", "the avatar uploader", "the cron scheduler", "the email templates",
		"the feature flags"} {
		write(realPath, fmt.Sprintf("f%02d", i), "02", "worked on "+topic+" today")
	}

	setHome(t, home)
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Whole-index scope: the claude reader names a session's project after the
	// first directory under its root, so the ignored copy is not in "w/app" —
	// the alignment this measures is the same either way.
	ss, matched, strong, _, err := ProjectRelevant(dir, nil,
		[]string{"quokkactl", "snorblefunc"}, 8)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the ranking answered with the sessions that are not ignored.
	if len(ss) == 0 {
		t.Fatal("nothing was ranked, so this measures nothing")
	}
	for _, s := range ss {
		if s.ID == "scratch" {
			t.Fatalf("an ignored session was ranked: %s", s.Path)
		}
	}
	if len(matched) != len(ss) || len(strong) != len(ss) {
		t.Fatalf("%d sessions against %d matched and %d strong counts", len(ss), len(matched), len(strong))
	}
	// And the counts are the ones those sessions earned: each real session
	// holds exactly one of the two words.
	for i, s := range ss {
		if matched[i] > 1 {
			t.Errorf("%s was given %d informative terms; it holds one: %s",
				s.ID, matched[i], fmt.Sprint(matched))
		}
	}
}

// The README offers `deja "what did we do in may"` as a feature, and the
// parser knew only relative phrases — "may" was searched as an ordinary word.
func TestMonthNamesResolveToTheMostRecentOccurrence(t *testing.T) {
	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	for query, want := range map[string]string{
		"what did we do in may": "2026-05",
		"что делали в мае":      "2026-05",
		"in june":               "2026-06",
		"back in january":       "2026-01",
		// Asking about a month later than the current one means last year's:
		// nobody is asking what they did next December.
		"in december": "2025-12",
	} {
		got := relativeTimeTerms(query, now)
		found := false
		for _, tok := range got {
			if tok == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%q -> %v, want %s among them", query, got, want)
		}
	}
	// A query with no time in it must stay empty, or every search picks up
	// month hints it never asked for.
	if got := relativeTimeTerms("fix the parser", now); len(got) != 0 {
		t.Fatalf("time hints invented for a plain query: %v", got)
	}
	// "March" as a verb, and "may" as a modal, are the cost of this: both
	// produce a hint. The tier only ever OR-scores them, so a wrong hint
	// costs ranking weight and never filters anything out.
	if got := relativeTimeTerms("we may need to fix this", now); len(got) == 0 {
		t.Log("modal 'may' produced no hint; fine either way")
	}
}

// On the 31st of a 31-day month after a 30-day or 28-day month, AddDate(0,-1,0)
// overflows into the current month (Oct 31 -> Oct 1, Mar 31 -> Mar 3).
func TestLastMonthOnTheThirtyFirstDoesNotOverflow(t *testing.T) {
	oct31 := time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC)
	if got := relativeTimeTerms("what did we do last month", oct31); len(got) == 0 || got[0] != "2026-09" {
		t.Fatalf("last month on Oct 31 = %v, want 2026-09", got)
	}
	if day, ok := pointInTime("a month ago", oct31); !ok || day.Month() != time.September || day.Day() != 30 {
		t.Fatalf("a month ago on Oct 31 = %v (%v), want 2026-09-30", day, ok)
	}
	mar31 := time.Date(2026, time.March, 31, 12, 0, 0, 0, time.UTC)
	if got := relativeTimeTerms("two months ago", mar31); len(got) == 0 || got[0] != "2026-01" {
		t.Fatalf("two months ago on Mar 31 = %v, want 2026-01", got)
	}
	if got := relativeTimeTerms("last month", mar31); len(got) == 0 || got[0] != "2026-02" {
		t.Fatalf("last month on Mar 31 = %v, want 2026-02", got)
	}
}

// A rename arrives as one removed path and one added path in the same pass, so
// the path the manifest holds is the one that went away. Comparing them called
// it a collision with a file that no longer exists: the row kept the dead path
// until a full rebuild, and `forget` warned that dropping the session would
// take a second conversation with it (#1086).
func TestARenamedTranscriptIsNotASecondCopyOfItself(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")

	// b sorts after a, which is the direction that used to keep the dead path:
	// the tie-break takes the lexicographically smaller one.
	from := filepath.Join(claude, "project", "a.jsonl")
	to := filepath.Join(claude, "project", "b.jsonl")
	writeLines(t, from, claudeLine("sess-a", "2026-01-01T00:01:00Z", "widget calibration drifted"))
	if err := Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}

	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := m.Sessions["claude:sess-a"]
	if !ok {
		t.Fatalf("the renamed session left the index: %v", m.Sessions)
	}
	if meta.Path != to {
		t.Errorf("path = %q, want the file that exists, %q", meta.Path, to)
	}
	if meta.Shared {
		t.Error("a renamed transcript was marked as sharing its id with another")
	}
}

// The tie-break still has to fire when two transcripts really do claim one id,
// even when some unrelated file went away in the same pass.
func TestTwoLiveTranscriptsSharingAnIdStillCollide(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")

	one := filepath.Join(claude, "p1", "x.jsonl")
	gone := filepath.Join(claude, "p1", "unrelated.jsonl")
	writeLines(t, one, claudeLine("dup-1", "2026-01-01T00:01:00Z", "widget calibration drifted"))
	writeLines(t, gone, claudeLine("other", "2026-01-01T00:01:00Z", "nothing to do with it"))
	if err := Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	// A second live transcript claiming the same id, and a removal beside it.
	two := filepath.Join(claude, "p2", "y.jsonl")
	writeLines(t, two, claudeLine("dup-1", "2026-01-01T00:02:00Z", "widget calibration drifted again"))
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}

	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := m.Sessions["claude:dup-1"]
	if !ok {
		t.Fatalf("the shared session left the index: %v", m.Sessions)
	}
	if !meta.Shared {
		t.Error("two live transcripts claiming one id were not reported as sharing it")
	}
}

func seedOneTranscript(t *testing.T, name, text string) (dir, path string) {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-me-deja-vu")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(proj, name)
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-06-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir = filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func recordsSize(t *testing.T, dir string) int64 {
	t.Helper()
	fi, err := os.Stat(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

// A transcript under a new name was read from the first byte and written again:
// twenty renames of one 4 KB log left twenty-one copies in records.bin and
// twenty rows pointing at paths that were gone, with search answering once and
// no screen saying the store was twenty times its content (#3546).
func TestARenamedTranscriptIsNotIndexedAgain(t *testing.T) {
	dir, path := seedOneTranscript(t, "s1.jsonl", "the zorblax pool deadlocked")
	before := recordsSize(t, dir)
	renamed := filepath.Join(filepath.Dir(path), "s1-v3.jsonl")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if after := recordsSize(t, dir); after != before {
		t.Errorf("records.bin went from %d to %d bytes for a file that only changed its name", before, after)
	}
	if !strings.Contains(log.String(), "renamed") {
		t.Errorf("the pass said nothing about the rename:\n%s", log.String())
	}
	if strings.Contains(log.String(), "no longer on disk") {
		t.Errorf("the old name was kept as a transcript deja still holds:\n%s", log.String())
	}
	ss, err := Search(dir, search.Options{Query: "zorblax"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("after the rename the session answers %d times, want 1", len(ss))
	}
	if ss[0].Path != renamed {
		t.Errorf("the session still points at %q, want %q", ss[0].Path, renamed)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Files[path]; ok {
		t.Errorf("the old path kept a file row: %v", sortedKeys(m.Files))
	}
	if _, ok := m.Files[renamed]; !ok {
		t.Errorf("the new path has no file row: %v", sortedKeys(m.Files))
	}
	// And the pass after it has nothing to do.
	log.Reset()
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if after := recordsSize(t, dir); after != before {
		t.Errorf("the following pass rewrote records.bin: %d against %d", after, before)
	}
}

// The pairing is on content. A file of the same size holding a different
// conversation is a new transcript, and the one that went away is kept, not
// renamed away.
func TestADifferentFileIsNotTakenForARename(t *testing.T) {
	dir, path := seedOneTranscript(t, "s1.jsonl", "the zorblax pool deadlocked")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// Same length, different text and a different session id.
	other := filepath.Join(filepath.Dir(path), "s2.jsonl")
	line := `{"type":"user","sessionId":"s2","timestamp":"2026-06-02T03:04:05Z","message":{"role":"user","content":"the quuxbar pool deadlocked"}}` + "\n"
	if err := os.WriteFile(other, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "renamed") {
		t.Errorf("a different conversation was taken for a rename:\n%s", log.String())
	}
	if ss, _ := Search(dir, search.Options{Query: "zorblax"}); len(ss) != 1 {
		t.Errorf("the deleted transcript stopped being searchable: %d hits", len(ss))
	}
	if ss, _ := Search(dir, search.Options{Query: "quuxbar"}); len(ss) != 1 {
		t.Errorf("the new transcript was not indexed: %d hits", len(ss))
	}
}

// The conditions themselves, without the filesystem: a row with no fingerprint
// is left alone, and two harnesses are never paired.
func TestRenameDetectionIsNarrow(t *testing.T) {
	old := map[string]FileState{
		"/gone/a.jsonl":       {Path: "/gone/a.jsonl", Size: 100, SafeSize: 100, PrefixSample: 7},
		"/gone/nohash.jsonl":  {Path: "/gone/nohash.jsonl", Size: 200, SafeSize: 200},
		"/gone/other.jsonl":   {Path: "/gone/other.jsonl", Size: 300, SafeSize: 300, PrefixSample: 9},
		"/still/here.jsonl":   {Path: "/still/here.jsonl", Size: 400, SafeSize: 400, PrefixSample: 11},
		"deja-sync-import":    {Path: "deja-sync-import", Size: 100, SafeSize: 100, PrefixSample: 7},
		"/gone/sizeonly.json": {Path: "/gone/sizeonly.json", Size: 500, SafeSize: 500, PrefixSample: 13},
	}
	files := map[string]FileState{
		"/new/a.jsonl":      {Path: "/new/a.jsonl", Size: 100, SafeSize: 100, PrefixSample: 7},
		"/new/nohash.jsonl": {Path: "/new/nohash.jsonl", Size: 200, SafeSize: 200},
		"/new/other.jsonl":  {Path: "/new/other.jsonl", Size: 300, SafeSize: 300, PrefixSample: 8},
		"/still/here.jsonl": {Path: "/still/here.jsonl", Size: 400, SafeSize: 400, PrefixSample: 11},
	}
	got := detectRenamedFiles(old, files)
	if len(got) != 1 {
		t.Fatalf("paired %d files, want 1: %v", len(got), got)
	}
	if got["/new/a.jsonl"] != "/gone/a.jsonl" {
		t.Errorf("pairing = %v, want /new/a.jsonl from /gone/a.jsonl", got)
	}
}

// A resumed session arrives under a new name with a turn appended. The
// fingerprint covers the bytes deja had already read, so the pairing still
// holds and only the tail is parsed.
func TestARenamedTranscriptThatGrewReadsOnlyItsTail(t *testing.T) {
	dir, path := seedOneTranscript(t, "s1.jsonl", "the zorblax pool deadlocked")
	before := recordsSize(t, dir)
	renamed := filepath.Join(filepath.Dir(path), "s1-resumed.jsonl")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(renamed, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T03:05:05Z","message":{"role":"assistant","content":"quuxbar was the fix"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	var log bytes.Buffer
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "renamed") {
		t.Errorf("a renamed file that grew was not recognised:\n%s", log.String())
	}
	if ss, _ := Search(dir, search.Options{Query: "zorblax"}); len(ss) != 1 {
		t.Errorf("the first turn answers %d times, want 1", len(ss))
	}
	if ss, _ := Search(dir, search.Options{Query: "quuxbar"}); len(ss) != 1 {
		t.Errorf("the appended turn answers %d times, want 1", len(ss))
	}
	// The tail, not the whole file: the growth is one short turn.
	if grew := recordsSize(t, dir) - before; grew > before {
		t.Errorf("records.bin grew by %d bytes for a one-turn append to a %d byte store", grew, before)
	}
}

// goose hands back every turn of a session it touched, so each pass re-offers
// messages the index already holds — and the incremental writer appends what it
// is given without asking whether it has seen it. What keeps a re-offered turn
// from becoming a second record is the whole-store rewrite
// (`wholeStoresThisPass`/`rereadsWholeSessions`), which is a property of the
// pass rather than of the writer, and nothing held it: the tests beside this
// one pin the ingest counts and the survival of earlier turns, neither of which
// moves when a message is stored twice.
//
// A duplicate is not a cosmetic matter here: the same sentence comes back as
// two hits in one session, and the count beside a search result is what a
// reader uses to judge whether a session is the one.
func TestAReofferedTurnDoesNotBecomeASecondRecord(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "goose.db")
	t.Setenv("DEJA_GOOSE_DB", db)

	const first = "the pgbouncer pool timed out during the migration"
	seedGooseTurn(t, db, "g1", "user", first, 1767322800)
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	records := func() int {
		t.Helper()
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		if err := eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
			if strings.Contains(r.Text, "pgbouncer pool timed out") {
				n++
			}
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := records(); got != 1 {
		t.Fatalf("the first pass stored the turn %d times, so this measures nothing", got)
	}
	// Two more passes, each of which hands the first turn back with the new one.
	for i, ts := range []int64{1767326400, 1767330000} {
		seedGooseTurn(t, db, "g1", "assistant", "a short answer", ts)
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		if got := records(); got != 1 {
			t.Errorf("after pass %d the store holds the same turn %d times", i+2, got)
		}
	}
	// And the reader sees one of it: a session whose every line is doubled
	// reads as a conversation that repeated itself.
	s, ok, err := FindByIdentity(dir, "goose", "g1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the session is gone from the index")
	}
	seen := 0
	for _, msg := range s.Messages {
		if strings.Contains(msg.Text, "pgbouncer pool timed out") {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("the session carries the same turn %d times", seen)
	}
	res, err := SearchDetailed(dir, search.Options{Query: "pgbouncer pool", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 {
		t.Fatalf("search returned %d sessions, want the one", len(res.Sessions))
	}
	if n := len(res.Sessions[0].Messages); n != 1 {
		t.Errorf("search shows the turn %d times in one session", n)
	}
}

// The remedy that is the failing command corrected stands on its own. Before
// this it did not: the corrected line names nothing the error named — zsh says
// `no matches found` and the fix is a pair of quotes — so it was filed as a
// candidate and never served until some other session happened to run the same
// line after the same error.
func TestTheCorrectedCommandIsServedAtOnce(t *testing.T) {
	now := time.Now().UTC()
	s := model.Session{
		Harness: "claude", ID: "s1",
		Messages: []model.Message{
			{Role: "command", Text: `grep -rn "quokkabloom" --include=*.go internal`, Time: now},
			{Role: "tool-output", Text: "zsh:1: no matches found: --include=*.go", Time: now.Add(time.Second)},
			{Role: "command", Text: `grep -rn "quokkabloom" --include="*.go" internal`, Time: now.Add(2 * time.Second)},
		},
	}
	dir := t.TempDir()
	buildFixes(dir, []model.Session{s}, func(m model.Session) string { return m.Harness + ":" + m.ID })

	served := FixesFor(dir, "zsh:1: no matches found: --include=*.go", 3, nil)
	if len(served) != 1 {
		t.Fatalf("the corrected command was not served: %+v", ReadFixes(dir))
	}
	if !served[0].Repaired {
		t.Errorf("served without being recognised as a repair: %+v", served[0])
	}
	if served[0].Command != `grep -rn "quokkabloom" --include="*.go" internal` {
		t.Errorf("served the wrong command: %q", served[0].Command)
	}
}

// A session that hits an error and goes on to unrelated work still yields
// nothing on its own.
func TestMovingOnStillWaitsForASecondSession(t *testing.T) {
	now := time.Now().UTC()
	s := model.Session{
		Harness: "claude", ID: "s1",
		Messages: []model.Message{
			{Role: "command", Text: `psql -c "select 1"`, Time: now},
			{Role: "tool-output", Text: "psql: connection refused on port 5432", Time: now.Add(time.Second)},
			{Role: "command", Text: "git status --short", Time: now.Add(2 * time.Second)},
		},
	}
	dir := t.TempDir()
	buildFixes(dir, []model.Session{s}, func(m model.Session) string { return m.Harness + ":" + m.ID })
	if got := FixesFor(dir, "psql: connection refused on port 5432", 3, nil); len(got) != 0 {
		t.Errorf("unrelated work was served as a remedy: %+v", got)
	}
}

// The shapes a repair actually takes, counted over 2,953 failed commands: a
// glob that needed quoting, a flag that had to go, a path that was wrong. Each
// is the failing command with a small correction, which is why it is evidence
// on its own — unlike "the command that ran next", which named nothing in 87%
// of cases.
func TestARepairIsTheFailingCommandCorrected(t *testing.T) {
	repairs := [][2]string{
		// zsh will not expand the glob; quoting it is the whole fix.
		{`grep -rn "hookDigest" --include=*.go internal`, `grep -rn "hookDigest" --include="*.go" internal`},
		// The flag the tool does not take.
		{`go test ./internal/index -run TestFix -count=1 -race -json`, `go test ./internal/index -run TestFix -count=1 -race`},
		// The path that was wrong.
		{`python3 scripts/report.py --out /var/reports`, `python3 scripts/report.py --out ./reports`},
		// A wrapper in front does not change what is being run.
		{`sudo systemctl restart deja-sync.service --now`, `systemctl restart deja-sync.service --now`},
	}
	for _, r := range repairs {
		if !repairedVariant(r[0], r[1]) {
			t.Errorf("not read as a repair:\n  was: %s\n  now: %s", r[0], r[1])
		}
	}
}

// What the bound is for. The most common shape below it was one program run
// twice on unrelated arguments, which says nothing about the error.
func TestMovingOnIsNotARepair(t *testing.T) {
	notRepairs := [][2]string{
		// Going somewhere else carries no work, whether or not it was the fix.
		// `cd` is 705 of the 1,892 same-program successes, the largest single
		// source of noise in the shape.
		{`cd /work/api && ls`, `cd /work/web && ls`},
		// A different program entirely.
		{`go build ./...`, `npm run build`},
		// The identical command again is a retry, not a repair.
		{`go test ./internal/index -count=1`, `go test ./internal/index -count=1`},
		// One word is not enough to compare.
		{`make`, `ninja`},
	}
	for _, r := range notRepairs {
		if repairedVariant(r[0], r[1]) {
			t.Errorf("read as a repair when it is not:\n  was: %s\n  now: %s", r[0], r[1])
		}
	}
}

// A pass that writes the log again used to read all of it back three more
// times: for the offset table, to check every friction entry, and to walk the
// command failures (#5402). It knows which sessions it changed, and the rest
// are copied record for record, so each of the three reads only those.
func TestReplacementPassSidecarsReadOnlyTheChangedSessions(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	// The command table's last run comes back in the local zone from the
	// records and in UTC from a full build: the same instant, printed apart.
	defer func(l *time.Location) { time.Local = l }(time.Local)
	time.Local = time.UTC
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	const n = 12
	session := func(i int, ask string) {
		id := fmt.Sprintf("s%02d", i)
		var body string
		var bodySb27 strings.Builder
		for turn := range 4 {
			ts := fmt.Sprintf("%02d:%02d", i*4+turn, 0)[:3]
			tid := fmt.Sprintf("t%d", turn)
			bodySb27.WriteString(parityClaudeLine(id, ts+"00Z", "user", fmt.Sprintf("%q", ask)) +
				parityClaudeLine(id, ts+"01Z", "assistant", `[{"type":"tool_use","id":"`+tid+`","name":"Bash","input":{"command":"make migrate"}}]`) +
				parityClaudeLine(id, ts+"02Z", "user", `[{"type":"tool_result","tool_use_id":"`+tid+`","content":"dial tcp 127.0.0.1:5432: connect: connection refused\nmake: *** [migrate] Error 1"}]`))
		}
		body += bodySb27.String()
		parityWrite(t, filepath.Join(proj, id+".jsonl"), body, false)
		later := time.Now().Add(time.Duration(i+len(ask)) * time.Second)
		_ = os.Chtimes(filepath.Join(proj, id+".jsonl"), later, later)
	}
	for i := range n {
		session(i, "why does the migration hang")
	}
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	// One rewrite first, so the measured pass starts from what a store that
	// was updated before holds: a command-fail state and friction texts.
	session(3, "why does the migration hang again")
	parityPass(t, inc, false)

	scanned, outputs, seen := atomic.LoadInt64(&recordOffsetsScanned), atomic.LoadInt64(&frictionOutputsRead), atomic.LoadInt64(&commandFailRecordsSeen)
	session(7, "why does the migration still hang")
	parityPass(t, inc, false)
	scanned, outputs, seen = atomic.LoadInt64(&recordOffsetsScanned)-scanned, atomic.LoadInt64(&frictionOutputsRead)-outputs, atomic.LoadInt64(&commandFailRecordsSeen)-seen

	// One session of twelve changed: 12 records of it, 4 of them outputs.
	if scanned != 0 {
		t.Errorf("the offset table walked %d records, want none: the pass knows where it wrote each", scanned)
	}
	if outputs > 4 {
		t.Errorf("friction texts read %d tool outputs back, want the changed session's 4", outputs)
	}
	if seen > 12 {
		t.Errorf("the command-fail walk looked at %d records, want the changed session's 12", seen)
	}
	m, err := readManifest(inc)
	if err != nil {
		t.Fatal(err)
	}
	if len(frictionWalls(m)) == 0 || len(ReadCommandFails(inc)) == 0 {
		t.Fatal("no wall or no command failure on file, so this measures nothing")
	}
	checkFrictionTexts(t, inc, m)
	offsetTableMatchesAWalk(t, inc, true)
	sameAsRebuild(t, inc)
}

// offsetTableMatchesAWalk holds the offset table in dir to a walk of its log.
// Unless whole is set, a table that covers only part of the log, or none, is
// left alone: readers walk the rest.
func offsetTableMatchesAWalk(t *testing.T, dir string, whole bool) {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	ro, err := readRecordOffsetsFor(dir, f, nil)
	if !whole && (err != nil || ro.covered != fi.Size()) {
		return
	}
	if err != nil {
		t.Fatalf("no offset table that fits the log: %v", err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	walked := map[string][]int64{}
	n, err := scanRecordOffsets(f, tablesFromManifest(m), 0, fi.Size(), walked)
	if err != nil {
		t.Fatal(err)
	}
	if ro.covered != fi.Size() || ro.records != n || fmt.Sprint(ro.byKey) != fmt.Sprint(walked) {
		t.Errorf("offset table covers %d bytes and %d records, the log %d and %d; same offsets: %v",
			ro.covered, ro.records, fi.Size(), n, fmt.Sprint(ro.byKey) == fmt.Sprint(walked))
	}
}

// What `deja index` does on a pass that replaces one session: ask whether the
// index is fresh, run the pass, then the lines it prints after it. Each of
// those decoded the manifest again, 49 MB of sessions on a 40,000-session
// store, and the check and the pass each walked every store (#5471).
func indexCommandPass(t *testing.T, dir string, handOver bool) (decodes, walks int64) {
	t.Helper()
	invalidateManifestCache(dir) // as a new process finds it
	d, w := manifestDecodes.Load(), storeWalks.Load()
	fresh, _, check := CheckUpToDate(dir, "")
	if fresh || check == nil {
		t.Fatal("the index is up to date after a session was rewritten")
	}
	if handOver {
		if err := EnsureChecked(check, false, io.Discard); err != nil {
			t.Fatal(err)
		}
	} else {
		parityPass(t, dir, false)
	}
	_ = IngestHealth(dir)
	_ = ExclusionsChanged(dir)
	return manifestDecodes.Load() - d, storeWalks.Load() - w
}

func TestAReplacingPassDecodesTheManifestOnce(t *testing.T) {
	proj, inc := replaceStore(t, 40)
	rewriteSession(t, proj, "s007")
	// Written a while ago: a directory that changed just before the walk is
	// taken for one that changed after it (#5503).
	ago := time.Now().Add(-time.Minute)
	for _, d := range []string{proj, filepath.Dir(proj)} {
		if err := os.Chtimes(d, ago, ago); err != nil {
			t.Fatal(err)
		}
	}
	decodes, walks := indexCommandPass(t, inc, true)
	// The freshness check's, which the pass then changes.
	if decodes != 1 {
		t.Errorf("a replacing pass and the checks after it decoded the manifest %d times, want 1", decodes)
	}
	if walks != 1 {
		t.Errorf("the freshness check and the pass walked the stores %d times, want 1", walks)
	}
	sameAsRebuild(t, inc)

	// The control: a pass that is not handed the check decodes and walks
	// again for itself.
	rewriteSession(t, proj, "s011")
	if decodes, walks := indexCommandPass(t, inc, false); decodes < 2 || walks < 2 {
		t.Errorf("a pass on its own decoded the manifest %d times and walked %d, so the counts miss its own", decodes, walks)
	}
	sameAsRebuild(t, inc)
}

// The pass starts from the freshness check's walk only while the index is the
// one that walk was held against: one another process wrote in between holds
// files as they were after it.
func TestTheCheckedWalkIsNotReusedAcrossAnotherWrite(t *testing.T) {
	proj, inc := replaceStore(t, 8)
	rewriteSession(t, proj, "s003")
	fresh, _, check := CheckUpToDate(inc, "")
	if fresh || check == nil {
		t.Fatal("the index is up to date after a session was rewritten")
	}
	// Another process: the same change and one more, indexed under the lock.
	parityWrite(t, filepath.Join(proj, "s005.jsonl"), replaceBody("s005", "wombat "), false)
	later := time.Now().Add(3 * time.Second)
	_ = os.Chtimes(filepath.Join(proj, "s005.jsonl"), later, later)
	parityPass(t, inc, false)
	w := storeWalks.Load()
	if err := EnsureChecked(check, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if storeWalks.Load() == w {
		t.Error("the pass took a walk from before another process wrote the index")
	}
	if fresh, _ := UpToDate(inc, ""); !fresh {
		t.Error("the index is not up to date after the pass")
	}
	sameAsRebuild(t, inc)
}

// Repo memory is named from the reader's own checkout, so it scopes by the
// local rule once the mark is off: a bare basename does not reach it.
func TestRepoMemoryScopesLikeLocalWork(t *testing.T) {
	for _, c := range []struct {
		project, want string
		in            bool
	}{
		{"repo:goprojects/api", "goprojects/api", true},
		{"repo:goprojects/api", "api", false},
		{"imported:goprojects/api", "api", true},
		{"repo:api", "api", true},
	} {
		if got := ProjectInScope(c.project, c.want); got != c.in {
			t.Errorf("ProjectInScope(%q, %q) = %v, want %v", c.project, c.want, got, c.in)
		}
	}
	if !ProjectInScopeStrict("repo:goprojects/api", "goprojects/api") {
		t.Error("strict scope does not take repo memory of its own project")
	}
}

// The ranker scores sessions on many cores now, because lowercasing and
// tokenising every turn of every ranked session was 1.6 s of a 2.4 s recall
// answer on a real store. Spreading work changes nothing about the answer only
// if the order it comes back in is the same, so both paths are run.
func TestRankingIsTheSameOnOneCoreAndMany(t *testing.T) {
	ss := make([]model.Session, 0, 24)
	for i := range 24 {
		msgs := []model.Message{
			{Role: "user", Text: fmt.Sprintf("session %d asks about the retry budget", i)},
			{Role: "assistant", Text: fmt.Sprintf("we capped it at %d attempts", i%5)},
		}
		if i%3 == 0 {
			msgs = append(msgs, model.Message{Role: "assistant", Text: "the scheduler stayed single-writer"})
		}
		ss = append(ss, model.Session{Harness: "claude", ID: fmt.Sprintf("s%02d", i), Messages: msgs})
	}
	terms := []string{"retry", "budget", "scheduler"}
	idf := map[string]float64{"retry": 1.5, "budget": 2, "scheduler": 3}

	rerankWorkers = func() int { return 1 }
	one := rerankByBestMessage(cloneSessions(ss), terms, idf)
	rerankWorkers = func() int { return 8 }
	many := rerankByBestMessage(cloneSessions(ss), terms, idf)
	t.Cleanup(func() { rerankWorkers = defaultRerankWorkers })

	if len(one) != len(many) {
		t.Fatalf("one core ranked %d sessions, eight ranked %d", len(one), len(many))
	}
	for i := range one {
		if one[i].ID != many[i].ID {
			t.Fatalf("position %d: one core says %s, eight say %s", i, one[i].ID, many[i].ID)
		}
	}
}

func cloneSessions(ss []model.Session) []model.Session {
	out := make([]model.Session, len(ss))
	copy(out, ss)
	return out
}

func kiroPrompt(id string, ts int64, text string) string {
	return fmt.Sprintf(`{"version":"v1","kind":"Prompt","data":{"message_id":%q,"content":[{"kind":"text","data":%q}],"meta":{"timestamp":%d}}}`, id, text, ts) + "\n"
}

// kiro-cli 2.22 stamps only the Prompt.
func kiroAnswer(id, text string) string {
	return fmt.Sprintf(`{"version":"v1","kind":"AssistantMessage","data":{"message_id":%q,"content":[{"kind":"text","data":%q}]}}`, id, text) + "\n"
}

func kiroCLISession(t *testing.T) (dir, transcript string) {
	t.Helper()
	tmp := t.TempDir()
	isolateStores(t, tmp)
	root := filepath.Join(tmp, "kiro")
	t.Setenv("DEJA_KIRO_ROOT", root)
	t.Setenv("DEJA_KIRO_DB", filepath.Join(tmp, "absent.sqlite3"))
	transcript = filepath.Join(root, "cli", "s-retry.jsonl")
	writeAt(t, filepath.Join(root, "cli", "s-retry.json"), `{"session_id":"s-retry","cwd":"/tmp/proj"}`, time.Now())
	return filepath.Join(tmp, "index.db"), transcript
}

// A reply or tool call takes the time of the Prompt before it. On a read
// resumed after a pass that Prompt was behind the offset, and what was
// appended landed at 0001-01-01 (#4444).
func TestKiroCLIRecordAppendedAfterAPassKeepsThePromptsTime(t *testing.T) {
	dir, transcript := kiroCLISession(t)
	writeAt(t, transcript, kiroPrompt("p1", 1785600100, "fix the retry loop"), time.Now())
	indexPass(t, dir)

	appendTo(t, transcript, `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m4","content":[{"kind":"text","data":"vetting it"},{"kind":"toolUse","data":{"toolUseId":"t3","name":"shell","input":{"command":"go vet ./retry"}}}]}}`+"\n")
	indexPass(t, dir)
	matchesRebuild(t, dir, "kiro", "s-retry")
	s, _, _ := FindByIdentity(dir, "kiro", "s-retry")
	for _, m := range s.Messages {
		if m.Time.IsZero() {
			t.Errorf("%s %q has no time", m.Role, m.Text)
		}
	}
}

// A reply streams in as records sharing a message id. A pass between two of
// them stored the halves as two replies; a rebuild joins them (#4445).
func TestKiroCLIReplyStreamedAcrossAPassIsOneMessage(t *testing.T) {
	dir, transcript := kiroCLISession(t)
	writeAt(t, transcript, kiroPrompt("p3", 1785600200, "and the backoff?")+kiroAnswer("a3", "The backoff "), time.Now())
	indexPass(t, dir)
	appendTo(t, transcript, kiroAnswer("a3", "doubles each attempt."))
	indexPass(t, dir)
	matchesRebuild(t, dir, "kiro", "s-retry")
}

// The next reply has its own id and is appended as before.
func TestKiroCLINextReplyAfterAPassIsItsOwn(t *testing.T) {
	dir, transcript := kiroCLISession(t)
	writeAt(t, transcript, kiroPrompt("p3", 1785600200, "and the backoff?")+kiroAnswer("a3", "The backoff doubles."), time.Now())
	indexPass(t, dir)
	appendTo(t, transcript, kiroPrompt("p4", 1785600300, "and the cap?")+kiroAnswer("a4", "Three attempts."))
	indexPass(t, dir)
	matchesRebuild(t, dir, "kiro", "s-retry")
	if s, _, _ := FindByIdentity(dir, "kiro", "s-retry"); len(s.Messages) != 4 {
		t.Errorf("%d messages, want 4", len(s.Messages))
	}
}

func grokChunk(ts int, update, meta string) string {
	return fmt.Sprintf(`{"timestamp":%d,"method":"session/update","params":{"update":%s,"_meta":%s}}`, 1784300000+ts, update, meta) + "\n"
}

func grokUser(ts, idx int, text string) string {
	return grokChunk(ts, fmt.Sprintf(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":%q},"_meta":{"promptIndex":%d}}`, text, idx), "{}")
}

func grokAgent(ts int, prompt, text string) string {
	return grokChunk(ts, fmt.Sprintf(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":%q}}`, text), fmt.Sprintf(`{"promptId":%q}`, prompt))
}

// Grok joins agent chunks that share a promptId, across the tool records
// between them. A pass mid-reply stored two replies (#4445).
func TestGrokReplyStreamedAcrossAPassIsOneMessage(t *testing.T) {
	tmp := t.TempDir()
	isolateStores(t, tmp)
	root := filepath.Join(tmp, "grok")
	t.Setenv("DEJA_GROK_ROOT", root)
	t.Setenv("DEJA_GROK_DB", filepath.Join(tmp, "absent.db"))
	updates := filepath.Join(root, "sessions", "%2Ftmp%2Fproj", "s-retry", "updates.jsonl")
	writeAt(t, updates, grokUser(30, 2, "and the backoff?")+grokAgent(31, "p2", "the backoff "), time.Now())
	dir := filepath.Join(tmp, "index.db")
	indexPass(t, dir)
	appendTo(t, updates, grokChunk(32, `{"sessionUpdate":"tool_call","toolCallId":"call-3","title":"read_file","rawInput":{"target_file":"/tmp/proj/retry.go"},"_meta":{"x.ai/tool":{"name":"read_file"}}}`, `{"promptId":"p2"}`)+
		grokAgent(33, "p2", "doubles each attempt"))
	indexPass(t, dir)
	matchesRebuild(t, dir, "grok", "s-retry")

	// The next prompt's reply is its own.
	appendTo(t, updates, grokUser(40, 3, "and the cap?")+grokAgent(41, "p3", "three attempts"))
	indexPass(t, dir)
	matchesRebuild(t, dir, "grok", "s-retry")
}

// Kimi streams a reply as content.part events and joins them at step.end. A
// pass before the step ended flushed what had arrived, and the rest became a
// second reply (#4445).
func TestKimiReplyStreamedAcrossAPassIsOneMessage(t *testing.T) {
	tmp := t.TempDir()
	isolateStores(t, tmp)
	root := filepath.Join(tmp, "kimi")
	t.Setenv("DEJA_KIMI_ROOT", root)
	session := filepath.Join(root, "sessions", "wd_proj_0123456789ab", "session_retry01")
	wire := filepath.Join(session, "agents", "main", "wire.jsonl")
	at := func(sec int) int64 { return 1788253200000 + int64(sec)*1000 }
	ev := func(sec int, e string) string {
		return fmt.Sprintf(`{"type":"context.append_loop_event","event":%s,"time":%d}`, e, at(sec)) + "\n"
	}
	part := func(sec int, text string) string {
		return ev(sec, fmt.Sprintf(`{"type":"content.part","part":{"type":"text","text":%q}}`, text))
	}
	writeAt(t, filepath.Join(session, "state.json"), `{"title":"why does the retry loop spin","workDir":"/tmp/proj"}`, time.Now())
	writeAt(t, wire, `{"type":"metadata","protocol_version":"1.4"}`+"\n"+
		fmt.Sprintf(`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"why does the retry loop spin"}],"toolCalls":[]},"time":%d}`, at(0))+"\n"+
		ev(1, `{"type":"step.begin"}`)+part(2, "The loop never sleeps; "), time.Now())
	dir := filepath.Join(tmp, "index.db")
	indexPass(t, dir)
	appendTo(t, wire, part(3, "add a backoff before the next attempt.")+ev(4, `{"type":"step.end"}`))
	indexPass(t, dir)
	matchesRebuild(t, dir, "kimi", "session_retry01")

	// A step that begins after a pass is its own reply.
	appendTo(t, wire, ev(5, `{"type":"step.begin"}`)+part(6, "Done."))
	indexPass(t, dir)
	matchesRebuild(t, dir, "kimi", "session_retry01")

	// A user turn that lands mid-stream goes ahead of the unfinished reply.
	appendTo(t, wire, fmt.Sprintf(`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"and the cap?"}],"toolCalls":[]},"time":%d}`, at(7))+"\n")
	indexPass(t, dir)
	matchesRebuild(t, dir, "kimi", "session_retry01")
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Agents rewind a session by truncating the transcript and writing it again.
// Once it grows past its old length that is indistinguishable from an append
// by size alone, and appending onto it leaves the rewritten prefix in the
// index with its old text while the new text is never read.
func TestRewrittenPrefixIsNotTreatedAsAppend(t *testing.T) {
	dir := t.TempDir()
	// The append fast path only applies to recognised harness files, so the
	// fixture has to live where the claude parser looks.
	t.Setenv("DEJA_CLAUDE_ROOT", dir)
	path := filepath.Join(dir, "project", "session.jsonl")
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "ORIGINAL text"), claudeLine("s1", "2026-01-01T00:02:00Z", "second"))
	before := currentFileStateFor(t, path)

	// The rewind: shorter, then longer again with a different first line.
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "REWRITTEN text"), claudeLine("s1", "2026-01-01T00:02:00Z", "second"), claudeLine("s1", "2026-01-01T00:03:00Z", "third"))
	after := currentFileStateFor(t, path)

	if after.Size <= before.Size {
		t.Fatalf("test does not exercise the case: sizes %d -> %d", before.Size, after.Size)
	}
	if canAppendIncremental(map[string]FileState{path: after}, map[string]FileState{path: before}) {
		t.Fatal("a rewritten prefix was accepted as an append; the old text would stay indexed")
	}

	// A genuine append must still take the fast path, or every session write
	// becomes a full reparse.
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "REWRITTEN text"), claudeLine("s1", "2026-01-01T00:02:00Z", "second"), claudeLine("s1", "2026-01-01T00:03:00Z", "third"), claudeLine("s1", "2026-01-01T00:04:00Z", "fourth"))
	grown := currentFileStateFor(t, path)
	if !canAppendIncremental(map[string]FileState{path: grown}, map[string]FileState{path: after}) {
		t.Fatal("a plain append no longer takes the append path")
	}
}

func currentFileStateFor(t *testing.T, path string) FileState {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	fs := FileState{Path: path, Size: fi.Size(), MTime: fi.ModTime().UnixNano()}
	fs.SafeSize = lastCompleteLineOffset(path, fi.Size())
	fs.PrefixHash = filePrefixHash(path, fs.SafeSize)
	return fs
}

// postingShape is what a bucket says about a token, minus the offsets and
// ordinals two builds are free to assign differently.
func postingShape(t *testing.T, dir string) map[string][2]int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "buckets", "*.bin"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][2]int{}
	for _, f := range files {
		data, err := readBucket(f)
		if err != nil {
			t.Fatal(err)
		}
		for tok, ps := range data {
			if ps = liveOnly(ps, m.DeadRecords); len(ps) == 0 {
				continue
			}
			var c [2]int
			for _, p := range ps {
				if p.Tool {
					c[1]++
				} else {
					c[0]++
				}
			}
			out[tok] = c
		}
	}
	return out
}

// An incremental pass that rewrites the record log carries every record it
// keeps into a new one. Those records get the postings a full build gives
// them: the written side's hashes and a replaced span's body are not tokens,
// and a tool record keeps its tool bit.
func TestARewritingPassPostsWhatARebuildPosts(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-me-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s1 := `{"type":"user","sessionId":"s1","timestamp":"2026-06-02T03:04:05Z","message":{"role":"user","content":"write the pool config"}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Write","input":{"file_path":"/Users/me/app/pool.go","content":"func retireConnectionsBeforeTheProxyDoes() {\n\tpool.MaxConnLifetime = 4 * time.Minute\n}\n"}}]}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T03:04:07Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu2","name":"Bash","input":{"command":"go test ./pool"}}]}}` + "\n" +
		`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T03:04:08Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu2","content":"ok  example.com/app/pool 0.4s"}]}}` + "\n"
	write("s1.jsonl", s1)
	write("s2.jsonl", `{"type":"user","sessionId":"s2","timestamp":"2026-06-03T03:04:05Z","message":{"role":"user","content":"first draft"}}`+"\n")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	live := filepath.Join(tmp, "live.db")
	if err := Ensure(live, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	// Rewritten rather than appended to, so the pass carries s1 into a new log.
	write("s2.jsonl", `{"type":"user","sessionId":"s2","timestamp":"2026-06-03T03:04:05Z","message":{"role":"user","content":"second draft, rewritten"}}`+"\n")
	if err := Ensure(live, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(tmp, "fresh.db")
	if err := Ensure(fresh, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	got, want := postingShape(t, live), postingShape(t, fresh)
	var diff []string
	for tok, w := range want {
		if got[tok] != w {
			diff = append(diff, tok)
		}
	}
	for tok := range got {
		if _, ok := want[tok]; !ok {
			diff = append(diff, tok)
		}
	}
	sort.Strings(diff)
	if len(diff) > 0 {
		for _, tok := range diff[:min(10, len(diff))] {
			t.Errorf("%q: incremental %v, rebuild %v (speech, tool)", tok, got[tok], want[tok])
		}
	}
}

// A record's role sits outside its compressed body, so a scan for one kind of
// record reads a prefix and skips the rest instead of inflating all of them:
// `deja how` asks for every command in the store and was decompressing all
// 280,000 records to read 20,000, which was 53% of its time. What it hands back
// has to be exactly what filtering after a full decode gives.
func TestARoleScanReturnsWhatAFullScanWouldFilter(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "role-index")
	now := time.Now().UTC().Truncate(time.Second)
	ss := make([]model.Session, 0, 12)
	for i := range 12 {
		ss = append(ss, model.Session{
			Harness: "claude", ID: fmt.Sprintf("s%02d", i), Project: "app", Updated: now,
			Messages: []model.Message{
				{Role: "user", Text: fmt.Sprintf("session %d asks about the retry budget", i), Time: now},
				{Role: "command", Text: fmt.Sprintf("go test ./internal/pool -run TestDrain%d", i), Time: now.Add(time.Second)},
				{Role: "tool-output", Text: strings.Repeat("output line that compresses well\n", 8), Time: now.Add(2 * time.Second)},
				{Role: "edit", Text: fmt.Sprintf("internal/pool/pool%d.go\n-\tone\n+\ttwo", i), Time: now.Add(3 * time.Second)},
				{Role: "assistant", Text: "capped the retry budget at three", Time: now.Add(4 * time.Second)},
			},
		})
	}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	tables := tablesFromManifest(m)
	recs := filepath.Join(dir, "records.bin")

	for _, role := range []string{"command", "edit", "user", "assistant", "tool-output"} {
		var want, got []Record
		if err := eachRecord(recs, tables, func(r Record) {
			if r.Role == role {
				want = append(want, r)
			}
		}); err != nil {
			t.Fatal(err)
		}
		if err := eachRecordInRoles(recs, tables, map[string]bool{role: true}, func(r Record) {
			got = append(got, r)
		}); err != nil {
			t.Fatal(err)
		}
		if len(want) == 0 {
			t.Fatalf("no %s records were written, so the comparison proves nothing", role)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: the role scan returned %d records, the full scan %d", role, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s record %d differs:\n scan: %+v\n full: %+v", role, i, got[i], want[i])
			}
		}
	}

	// Two roles at once keep the order they were written in, which is what a
	// caller pairing a command with its output depends on.
	var pairs []string
	if err := eachRecordInRoles(recs, tables, map[string]bool{"command": true, "tool-output": true}, func(r Record) {
		pairs = append(pairs, r.Role)
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i] != "command" || pairs[i+1] != "tool-output" {
			t.Fatalf("the two roles came back out of order: %v", pairs[:min(8, len(pairs))])
		}
	}
}

// One file name in five on a real store pooled several real files: 439 of 2169,
// worst `00-scroll-00.png` in 289 places, `service.go` in 7 packages,
// `kustomization.yaml` in 9 environments. The line before an edit reported that
// mixed history as the file's own.
func TestTwoFilesOfOneNameAreTwoFiles(t *testing.T) {
	apart := [][2]string{
		{"/work/app/cmd/dfbot/main.go", "/work/app/cmd/dfinvoice/main.go"},
		{"internal/handlers/service.go", "internal/billing/service.go"},
		{"deploy/prod/kustomization.yaml", "deploy/staging/kustomization.yaml"},
	}
	for _, c := range apart {
		if SameFile(c[0], c[1]) {
			t.Errorf("two different files read as one:\n  %s\n  %s", c[0], c[1])
		}
	}
}

// And every spelling of one file is still that file: the store holds what each
// harness recorded — absolute here, relative there, Windows separators from a
// synced machine.
func TestOneFileIsOneFileHoweverItWasSpelled(t *testing.T) {
	same := [][2]string{
		{"/work/app/internal/handlers/service.go", "internal/handlers/service.go"},
		{`C:\work\app\internal\handlers\service.go`, "internal/handlers/service.go"},
		{"./internal/handlers/service.go", "/work/app/internal/handlers/service.go"},
		// A harness that recorded the name and nothing else has only the name
		// to match on.
		{"service.go", "/work/app/internal/handlers/service.go"},
		{"/work/app/internal/handlers/service.go", "service.go"},
	}
	for _, c := range same {
		if !SameFile(c[0], c[1]) {
			t.Errorf("one file read as two:\n  %s\n  %s", c[0], c[1])
		}
	}
	if SameFile("internal/handlers/service.go", "internal/handlers/client.go") {
		t.Error("different names in one directory read as one file")
	}
}

// A session is re-projected onto the repository root once it has touched enough
// files under it (projectFromPaths), so the name recorded for a session started
// in a subdirectory is two segments the caller only reaches by walking up. A
// match against the cwd's own last segments alone missed exactly the sessions
// people have (#1999).
func TestSameProjectWalksUpToTheRepositoryRoot(t *testing.T) {
	for _, c := range []struct {
		recorded, cwd string
		want          bool
	}{
		{"w/app", "/w/app", true},
		{"app", "/w/app", true},
		{"w/app", "/w/app/", true}, // a trailing slash is still the same place
		{"home2/repo", "/home2/repo/cmd/deja", true},
		{"goprojects/deja-vu", "/Users/x/goprojects/deja-vu/internal/index", true},
		{"x/my app", "/Users/x/my app", true},
		{"x/проект", "/Users/x/проект", true},
		{"app/app", "/w/app/", false},          // and not a project of that name
		{"cmd", "/home2/repo/cmd/deja", false}, // a bare name matches where the caller stands
		{"other/app", "/w/app", false},
		{"", "/w/app", false},
		{"w/app", "", false},
		{"w/app", "/", false},
	} {
		if got := sameProject(c.recorded, c.cwd); got != c.want {
			t.Errorf("sameProject(%q, %q) = %v, want %v", c.recorded, c.cwd, got, c.want)
		}
	}
}

// How far up the match was found decides between two sessions that both match:
// the project's own session beats one recorded against a directory above it,
// however fresh that one is. People do run an agent from a home directory, which
// is the case projectFromPaths was written for.
func TestProjectDistanceRanksTheNearerMatch(t *testing.T) {
	cwd := "/Users/me/work/app"
	for _, c := range []struct {
		recorded string
		want     int
	}{
		{"app", 0},
		{"work/app", 0},
		{"me/work", 1},
		{"Users/me", 2},
		{"work", -1}, // a bare name matches only where the caller stands
		{"other/app", -1},
	} {
		if got := projectDistance(c.recorded, cwd); got != c.want {
			t.Errorf("projectDistance(%q, %q) = %d, want %d", c.recorded, cwd, got, c.want)
		}
	}
	if !nearerProject(0, 2) || !nearerProject(3, -1) {
		t.Error("a nearer match, and any match at all, has to win")
	}
	if nearerProject(2, 0) || nearerProject(-1, 3) || nearerProject(-1, -1) || nearerProject(1, 1) {
		t.Error("a farther match, or none, must not displace what is held")
	}
}

// The sessions PoolScope adds are read only for the project's answer. Read
// with the pool, they decided the machine search's own tier: a quoted phrase
// whose pool held no session saying it came back exact, built from the
// project's sessions alone, where without them the search went on down the
// ladder (#5486).
func TestPoolScopeDoesNotDecideTheWiderAnswer(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	now := time.Now()
	n := search.PoolSize(5) + 300
	ss := make([]model.Session, 0, n)
	for i := range n {
		at := now.Add(-time.Duration(i) * time.Minute)
		text, project := fmt.Sprintf("alpha went first and beta went after, run %d", i), "other"
		if i >= n-12 {
			// Older, so the pool leaves them out; they say the phrase.
			text, project = fmt.Sprintf("alpha beta settled it, run %d", i), "shop"
		}
		ss = append(ss, model.Session{Harness: "claude", ID: fmt.Sprintf("s-%05d", i), Project: project, Started: at, Updated: at,
			Messages: []model.Message{{Role: "user", Text: text, Time: at}}})
	}
	writeTestStore(t, dir, ss...)

	o := query.Options{Query: `"alpha beta"`, Window: 5}
	plain, err := SearchDetailed(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	o.PoolScope = []string{"shop"}
	scoped, err := SearchDetailed(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if scoped.Tier != plain.Tier {
		t.Errorf("with the project's sessions read alongside, the tier is %q, without them %q", scoped.Tier, plain.Tier)
	}
	own := 0
	for _, s := range scoped.Sessions {
		if scoped.Unread == nil || !scoped.Unread.Scoped[s.Harness+":"+s.ID] {
			own++
		}
	}
	if own != len(plain.Sessions) {
		t.Errorf("the wider search ranks %d sessions of its own, without the scope %d", own, len(plain.Sessions))
	}

	// A scope no session belongs to adds nothing to read. It handed the scan
	// no offsets, which reads the whole record log: 20 s and 3 GB on a 40k
	// store.
	o.Query, o.PoolScope = "alpha", []string{"nowhere"}
	scanned := 0
	wholeLogScanned = func() { scanned++ }
	defer func() { wholeLogScanned = nil }()
	r, err := SearchDetailed(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if scanned > 0 {
		t.Errorf("an empty scope scanned %d records of the whole log", scanned)
	}
	if r.Unread == nil || !r.Unread.ScopeWhole || len(r.Unread.Scoped) > 0 {
		t.Errorf("an empty scope: unread %+v", r.Unread)
	}
}

// A search-only build writes the records and the postings and none of the
// derived tables, an append keeps it that way, and turning the setting off
// mines them on the next pass (#5395).
func TestASearchOnlyBuildSkipsTheDerivedTables(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(tmp, "policy.json"))
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, sid := range []string{"s-one", "s-two"} {
		write(t, filepath.Join(claude, "p", sid+".jsonl"),
			claudeLine(sid, "2026-01-02T03:04:05Z", "the exporter retries without a pause")+
				claudeBashLine(sid, "2026-01-02T03:05:00Z", "go test ./internal/exporter/"))
	}
	derived := []string{cooccurFile, fixRawFile, commandsFile, commandFailStateFile, sessionFactsFile}
	present := func(dir string) []string {
		var got []string
		for _, name := range derived {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				got = append(got, name)
			}
		}
		return got
	}

	// The control: the same store mined writes the tables this test looks for.
	full := filepath.Join(tmp, "full")
	if err := Ensure(full, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if len(present(full)) == 0 {
		t.Fatal("an ordinary build wrote none of the derived tables, so this proves nothing")
	}

	t.Setenv(SearchOnlyEnv, "1")
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := present(dir); len(got) > 0 {
		t.Errorf("a search-only build wrote %v", got)
	}
	if !BuiltSearchOnly(dir) {
		t.Error("the index does not record that it was built search-only")
	}
	if n, err := SessionCount(dir); err != nil || n != 2 {
		t.Errorf("a search-only index holds %d sessions (%v), want 2", n, err)
	}

	// A new transcript, and a turn added to one already read: the second is
	// the append path, which mines the tables on its own.
	write(t, filepath.Join(claude, "p", "s-three.jsonl"),
		claudeLine("s-three", "2026-01-02T05:00:00Z", "run the suite")+
			claudeBashLine("s-three", "2026-01-02T05:01:00Z", "go test ./internal/exporter/"))
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(claude, "p", "s-one.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(claudeBashLine("s-one", "2026-01-02T06:01:00Z", "go vet ./internal/exporter/"))
	_ = f.Close()
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := present(dir); len(got) > 0 {
		t.Errorf("an update of a search-only index wrote %v", got)
	}

	// A pass nobody asked for keeps the setting the index was built under:
	// a hook started without the user's environment does not rebuild the
	// store over it, and the shell does not rebuild it back.
	t.Setenv(SearchOnlyEnv, "")
	if err = Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := present(dir); len(got) > 0 || !BuiltSearchOnly(dir) {
		t.Errorf("an automatic pass under another setting changed the index: %v, search-only %v", got, BuiltSearchOnly(dir))
	}

	// `deja index` run by hand applies it.
	undo := ApplySearchOnly()
	err = Ensure(dir, "", false, nil)
	undo()
	if err != nil {
		t.Fatal(err)
	}
	if len(present(dir)) == 0 || BuiltSearchOnly(dir) {
		t.Errorf("turning search-only off left the tables unmined: %v", present(dir))
	}

	// And the policy file turns it on the same way.
	if err = os.WriteFile(filepath.Join(tmp, "policy.json"), []byte(`{"search_only": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	undo = ApplySearchOnly()
	err = Ensure(dir, "", false, nil)
	undo()
	if err != nil {
		t.Fatal(err)
	}
	if got := present(dir); len(got) > 0 || !BuiltSearchOnly(dir) {
		t.Errorf(`"search_only": true left %v behind`, got)
	}
}

// The rule name inside a marker is read back out of text, and text can hold
// something that only looks like one: deja's own documentation of the format, a
// test fixture, an answer quoting an earlier answer. Taking those at their word
// invented rules called `<kind>` and `…` with 17 and 3 hits on a real store
// (#536).
func TestMarkerKindsReadsOnlyRulesTheRedactorCanWrite(t *testing.T) {
	text := "the format is " + redact.Marker + "<kind>] and here is a real one: " +
		redact.Marker + "github-token] plus " + redact.Marker + "jwt] and " +
		redact.Marker + "…]"
	got := markerKinds(text)
	want := []string{"github-token", "jwt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("markerKinds read %v, want %v", got, want)
	}
	if !SecretKindNamed("github-token") {
		t.Error("a provider key is not named")
	}
	if SecretKindNamed("entropy") {
		t.Error("the entropy rule is listed as a credential")
	}
}

// One session pasting the same key into three turns is one finding with a
// count, not three lines.
func TestScanSecretsGroupsASessionsTurnsIntoOneFinding(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	when := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	msg := func(text string) model.Message {
		return model.Message{Role: "user", Text: text, Time: when}
	}
	writeStore(t, dir, []model.Session{{
		ID: "s1", Harness: "claude", Project: "work/app", Path: "/tmp/s1.jsonl",
		Updated: when,
		Messages: []model.Message{
			msg("export GITHUB_TOKEN=" + redact.Marker + "github-token]"),
			msg("again with " + redact.Marker + "github-token]"),
			msg("and a digest " + redact.Marker + "entropy]"),
		},
	}})
	scan, err := ScanSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Findings) != 1 {
		t.Fatalf("want one finding, got %d: %+v", len(scan.Findings), scan.Findings)
	}
	f := scan.Findings[0]
	if f.Kind != "github-token" || f.Count != 2 || f.Harness != "claude" {
		t.Errorf("wrong finding: %+v", f)
	}
	if f.Path != "/tmp/s1.jsonl" {
		t.Errorf("the transcript holding it was not named: %q", f.Path)
	}
	if scan.Sessions != 1 {
		t.Errorf("want one session, got %d", scan.Sessions)
	}
	if scan.Counted["entropy"] != 1 {
		t.Errorf("the entropy tier was not counted: %+v", scan.Counted)
	}
}

// The file is the actionable half of this report, and the rule that decides
// whether to print one used to be "the extension is .jsonl". Three harnesses
// write one file per session under another extension — Cline and half of VS
// Code Copilot Chat write .json, the DeepSeek Harness writes .zstd — and their
// rows arrived with no file at all. Counted on this machine's store: 69
// sessions sent to look in a store deja would not name.
func TestAPerSessionFileIsNamedWhateverItsExtension(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	when := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	msg := model.Message{Role: "user", Text: "export GITHUB_TOKEN=" + redact.Marker + "github-token]", Time: when}
	writeStore(t, dir, []model.Session{
		{ID: "a", Harness: "cline", Path: "/tmp/tasks/a/ui_messages.json", Updated: when, Messages: []model.Message{msg}},
		{ID: "b", Harness: "deepseek", Path: "/tmp/.dsh/b.jsonl.zstd", Updated: when, Messages: []model.Message{msg}},
		{ID: "c", Harness: "zed", Path: "/tmp/zed/threads.db", Updated: when, Messages: []model.Message{msg}},
		{ID: "d", Harness: "goose", Path: "/tmp/goose/sessions.sqlite", Updated: when, Messages: []model.Message{msg}},
		{ID: "e", Harness: "opencode", Path: t.TempDir(), Updated: when, Messages: []model.Message{msg}},
	})

	scan, err := ScanSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range scan.Findings {
		got[f.ID] = f.Path
	}
	if len(got) != 5 {
		t.Fatalf("want a finding per session, got %d: %+v", len(got), scan.Findings)
	}
	for _, id := range []string{"a", "b"} {
		if got[id] == "" {
			t.Errorf("session %s keeps its own file and the report named none", id)
		}
	}
	// A database holds every other session too, so naming it sends someone to
	// a file they must not edit.
	for _, id := range []string{"c", "d", "e"} {
		if got[id] != "" {
			t.Errorf("session %s is a database store and the report named %q", id, got[id])
		}
	}
}

// A session that arrived by sync is keyed here by the id the import gave it,
// so the id the reader knows — the one the machine that recorded it prints,
// the one a note promoted from it carries — matched nothing, and the only way
// to forget it was an id nobody chose (#2843).
func TestASelectorFindsAnImportedSessionByTheIdItCameWith(t *testing.T) {
	meta := SessionMeta{Harness: "claude", ID: ImportedSessionID("claude", "longs"), OrigID: "longs"}
	for _, sel := range []string{"longs", "claude:longs", meta.ID, "claude:" + meta.ID} {
		if !selectorMatches(meta, sel) {
			t.Errorf("%q does not name the session", sel)
		}
	}
	// And it stays a name rather than a substring: a session that merely
	// starts the same way is not the one the reader asked for.
	if selectorMatches(SessionMeta{Harness: "claude", ID: "other", OrigID: "longshoreman"}, "longs") {
		t.Error("a prefix of another session's original id matched")
	}
	if selectorMatches(meta, "codex:longs") {
		t.Error("the harness is not being checked")
	}
	if selectorMatches(SessionMeta{Harness: "claude", ID: "other", OrigID: "longshoreman"}, "claude:longs") {
		t.Error("a prefix of another session's original id matched through the harness form")
	}
}

// And the round trip: what forget takes, unforget gives back by the same
// string. Taking a session by an id whose undo needs a different one is worse
// than not taking it (#2843).
func TestATombstoneAnswersToTheIdItWasForgottenBy(t *testing.T) {
	key := "claude:" + ImportedSessionID("claude", "longs")
	for _, sel := range []string{"longs", key, ImportedSessionID("claude", "longs")} {
		if !tombstoneMatches(key, sel) {
			t.Errorf("%q does not lift the tombstone forget wrote for it", sel)
		}
	}
	if tombstoneMatches(key, "someone-elses-session") {
		t.Error("an unrelated id lifted the tombstone")
	}
}

// Asking deja about an error must not teach deja that the error happened
// again. `deja fix` prints the error with the date beside it and the command
// underneath; both land in the next session's tool output, where the header
// read as a fresh sighting and the command below it became a remedy for the
// error it was quoting. In real-world stores, as the pair
// `command not found: python · 2026-05-18`.
func TestDejasOwnFixReportIsNotFriction(t *testing.T) {
	for _, l := range []string{
		"command not found: python · 2026-05-18",
		"zsh:1: command not found: timeout · 2026-01-02",
		"  ran next: brew install coreutils",
	} {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("deja's own report read as friction: %q", l)
		}
	}
	// The same errors, as a tool actually prints them, still are.
	for _, l := range []string{
		"zsh:1: command not found: timeout",
		"ModuleNotFoundError: No module named 'yaml'",
	} {
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("a real error stopped being friction: %q", l)
		}
	}
	// A date at the end is deja's separator, not a general rule: a line that
	// happens to end in a date without it keeps its meaning.
	if _, ok := FrictionLine("fatal: could not read repository state at 2026-05-18"); !ok {
		t.Error("a line ending in a date lost its meaning")
	}
}

// Cursor writes `<timestamp>…</timestamp>` above `<user_query>…</user_query>`,
// and removing the one and unwrapping the other each left the newline that
// separated them, so every Cursor turn on this machine's store was indexed
// with a blank line ahead of the question (#3357).
func TestStripSelfRecallLeavesNoBlankLineWhereABlockWas(t *testing.T) {
	in := "<timestamp>Sunday, Jul 26, 2026, 1:06 PM (UTC+3)</timestamp>\n<user_query>\nwhy does the retry loop drop the last attempt\n</user_query>"
	if got := stripSelfRecall(in); got != "why does the retry loop drop the last attempt" {
		t.Errorf("stripSelfRecall = %q, want the question with nothing around it", got)
	}
	// A block in the middle keeps the paragraph break around it, and the words
	// on both sides stay whole.
	mid := "first line\n\n<timestamp>Sunday, Jul 26, 2026, 1:06 PM</timestamp>\n\nsecond line"
	if got := stripSelfRecall(mid); got != "first line\n\n<timestamp>Sunday, Jul 26, 2026, 1:06 PM</timestamp>\n\nsecond line" {
		t.Errorf("stripSelfRecall = %q — a stamp that is not the head of the message is prose", got)
	}
}

// And nothing is trimmed from a message the cleaning did not touch: a code
// block that opens and closes with a blank line keeps both (review of #3357).
func TestStripSelfRecallLeavesAnUntouchedMessageAlone(t *testing.T) {
	in := "\n\n```\n\nfunc foo() {}\n```\n\n"
	if got := stripSelfRecall(in); got != in {
		t.Errorf("stripSelfRecall = %q, want the message unchanged — nothing was stripped from it", got)
	}
}

// The trim belongs where the removal happened. Trimming the whole message
// instead ate what an author wrote at the far end — a blank line after their
// pasted diff — and made a message's edges depend on whether a marker fired
// somewhere else entirely (second review of #3357).
func TestStripSelfRecallTrimsOnlyWhereItCut(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			"<timestamp>Sunday, Jul 26, 2026, 1:06 PM</timestamp>\n\nhere is my diff:\n\n```\n+foo\n```\n\n",
			"here is my diff:\n\n```\n+foo\n```\n\n",
		},
		{
			"\n\nfirst paragraph\n\n<system-reminder>noise</system-reminder>\n\nsecond paragraph\n\n",
			"\n\nfirst paragraph\n\n\n\nsecond paragraph\n\n",
		},
	}
	for _, c := range cases {
		if got := stripSelfRecall(c.in); got != c.want {
			t.Errorf("stripSelfRecall(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Most harnesses prepend the recall block to a real user turn. Dropping the
// whole message would therefore lose the question that started the session —
// the block goes, the question stays.
func TestStripSelfRecallKeepsWhatTheUserWrote(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"no block": {"just a question", "just a question"},
		"block then question": {
			"<deja-recall>\nRecalled history…\n- Session: **x** `1`\n</deja-recall>\nwhy is the pool exhausted?",
			"why is the pool exhausted?",
		},
		"question then block": {
			"why is the pool exhausted?\n<deja-recall>\nstuff\n</deja-recall>",
			"why is the pool exhausted?",
		},
		"block in the middle": {
			"before\n\n<deja-recall>\nstuff\n</deja-recall>\n\nafter",
			"before\n\nafter",
		},
		"two blocks": {
			"<deja-recall>a</deja-recall>keep<deja-recall>b</deja-recall>",
			"keep",
		},
		// gemini stores the markers HTML-escaped inside its hook_context
		// wrapper. A filter that only knew the raw form left every gemini
		// session polluted while appearing to work everywhere else.
		"html-escaped block": {
			"&lt;deja-recall&gt;\nRecalled history…\n&lt;/deja-recall&gt;\nthe real question",
			"the real question",
		},
		"nothing but the block": {
			"<deja-recall>\nRecalled history…\n</deja-recall>",
			"",
		},
		// A truncated block means a context cap or a crash cut it off.
		// Everything from the marker onward is ours either way.
		"unclosed block": {
			"real question\n<deja-recall>\nrecalled and then cut off",
			"real question",
		},
	} {
		if got := stripSelfRecall(tc.in); got != tc.want {
			t.Fatalf("%s:\n got %q\nwant %q", name, got, tc.want)
		}
	}
}

// The whole point: what deja injects must not come back as what deja knows.
func TestIndexDoesNotSwallowItsOwnRecall(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)

	// A transcript exactly as a harness writes it: deja's block, then the real
	// user turn.
	// The newlines are escaped because the fixture interpolates straight into
	// a JSON line.
	writeLines(t, filepath.Join(claude, "project", "s.jsonl"),
		claudeLine("s1", "2026-02-01T00:01:00Z",
			`<deja-recall>\nRecalled history from prior sessions.\n- Session: **old** `+"`abc`"+` INJECTEDTOKEN\n</deja-recall>\nGENUINEQUESTION about the pool`))

	dir := filepath.Join(home, "idx")
	if err := Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}

	// The user's own words are still findable.
	got, err := Search(dir, query.Options{Query: "GENUINEQUESTION", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("stripping the recall block took the user's question with it")
	}

	// deja's injection is not.
	for _, term := range []string{"INJECTEDTOKEN", "deja-recall"} {
		hits, searchErr := Search(dir, query.Options{Query: term, All: true})
		if searchErr != nil {
			t.Fatal(searchErr)
		}
		if len(hits) > 0 {
			t.Fatalf("%q was indexed from deja's own recall block: %d hits", term, len(hits))
		}
	}

	// And the stored text carries no trace, so `deja show` and share do not
	// leak it either.
	s, ok, err := FindByPrefix(dir, "s1")
	if err != nil || !ok {
		t.Fatalf("session missing: ok=%v err=%v", ok, err)
	}
	for _, m := range s.Messages {
		if strings.Contains(m.Text, "deja-recall") || strings.Contains(m.Text, "INJECTEDTOKEN") {
			t.Fatalf("recall block survived into the stored message: %q", m.Text)
		}
	}
}

// Codex injects its preamble as a user turn, so it survives any role filter.
// It is stripped only at the head of a message: the tags are ordinary enough
// that people quote them, and nine Claude sessions on the store this was
// measured against do exactly that (#636).
func TestStripsCodexPreambleOnlyAtTheHead(t *testing.T) {
	preamble := "<environment_context>\n  <cwd>/w</cwd>\n</environment_context>\n" +
		"<recommended_plugins>a long list</recommended_plugins>\n" +
		"# AGENTS.md instructions\n\n<INSTRUCTIONS>project rules</INSTRUCTIONS>\n"
	got := stripSelfRecall(preamble + "the actual question")
	if strings.TrimSpace(got) != "the actual question" {
		t.Fatalf("got %q, want only the question", got)
	}
	if got := strings.TrimSpace(stripSelfRecall(preamble)); got != "" {
		t.Fatalf("a message that is nothing but preamble should strip to empty, got %q", got)
	}
	// A person discussing the tag keeps their sentence.
	for _, genuine := range []string{
		"why does <environment_context> outrank everything?",
		"the parser should drop <user_instructions> blocks",
		"see the note about # AGENTS.md instructions in the issue",
		// A complete pair quoted mid-sentence — the shape that a
		// strip-anywhere rule eats and a strip-at-the-head rule keeps. Real
		// messages on this store look exactly like this.
		"the injected block is <environment_context><cwd>/w</cwd></environment_context> and it wins on match count",
		"codex sends <recommended_plugins>a list</recommended_plugins> before the first turn",
	} {
		if got := stripSelfRecall(genuine); got != genuine {
			t.Errorf("ate a real message:\n  in  %q\n  out %q", genuine, got)
		}
	}
}

// The AGENTS.md heading only introduces the injected block when the block
// follows it immediately. As a plain open/close pair it spanned to the next
// </INSTRUCTIONS> anywhere in the message, so a person asking how to stop
// Codex injecting these lost their entire question — the exact message someone
// writes when reporting this.
func TestAgentsHeadingNeedsItsBlockToFollow(t *testing.T) {
	genuine := []string{
		"# AGENTS.md instructions\n\nHow do I stop Codex injecting these? Mine is:\n<INSTRUCTIONS>be terse</INSTRUCTIONS>\n",
		"# AGENTS.md instructions are confusing.\n\nWhy does the block end with </INSTRUCTIONS>?",
		"# AGENTS.md instructions\n\nno block at all here",
	}
	for _, in := range genuine {
		if got := stripSelfRecall(in); got != in {
			t.Errorf("ate a real message:\n  in  %q\n  out %q", in, got)
		}
	}
	injected := "# AGENTS.md instructions\n\n<INSTRUCTIONS>project rules</INSTRUCTIONS>\nthe real question"
	if got := strings.TrimSpace(stripSelfRecall(injected)); got != "the real question" {
		t.Fatalf("got %q", got)
	}
}

// Nested copies of the same tag must be counted, or the outer closer is left
// behind in the index.
func TestPrefixStripCountsNesting(t *testing.T) {
	got := stripSelfRecall("<environment_context>a<environment_context>b</environment_context>c</environment_context>tail")
	if got != "tail" {
		t.Fatalf("got %q, want the whole nested block gone", got)
	}
}

// A truncated rollout, or a Codex release that renames a closing tag, leaves
// an unclosed block. Stripping to the end of the message would delete real
// text that follows, so the block stays — deliberately, and stated here so a
// future change to that behaviour is a decision rather than a slip.
func TestUnclosedPrefixBlockIsLeftAlone(t *testing.T) {
	in := "<environment_context>\n<cwd>/w</cwd>\nthe file was cut here"
	if got := stripSelfRecall(in); got != in {
		t.Fatalf("got %q", got)
	}
}

// The preamble class is not Codex-specific. Looking only where a contributor
// pointed left Cursor and Gemini titling every session with plumbing: 13 of 13
// and 9 of 9 on this machine.
func TestStripsCursorAndGeminiWrappers(t *testing.T) {
	// Cursor wraps the real question; the tags go, the question stays.
	cursor := "<timestamp>Friday, Jul 31, 2026, 2:07 PM (UTC+3)</timestamp>\n<user_query>\nCreate notes.txt containing alpha\n</user_query>"
	if got := strings.TrimSpace(stripSelfRecall(cursor)); got != "Create notes.txt containing alpha" {
		t.Fatalf("cursor: got %q", got)
	}
	// Gemini opens with its own context block.
	gemini := "<session_context>\nThis is the Gemini CLI. We are setting up the context for our chat.\nMy operating system is: darwin\n</session_context>\nwhy is the pool sized like this"
	if got := strings.TrimSpace(stripSelfRecall(gemini)); got != "why is the pool sized like this" {
		t.Fatalf("gemini: got %q", got)
	}
	// A Gemini sub-agent's first turn opens with the project and extension
	// memory, deja's own MCP instructions among it, as a part of its own
	// ahead of the task; it titled every child session (#5214).
	child := "\n<loaded_context>\n<project_context>\nThe following are instructions provided by the tool server 'deja':\n---[start of server instructions]---\ndeja indexes this user's past sessions\n---[end of server instructions]---\n</project_context>\n</loaded_context>\nCreate the child file."
	if got := strings.TrimSpace(stripSelfRecall(child)); got != "Create the child file." {
		t.Fatalf("gemini sub-agent: got %q", got)
	}
	// A person quoting the tag keeps the sentence.
	quoted := "why does every child start with <loaded_context>? </loaded_context> is the end"
	if got := stripSelfRecall(quoted); got != quoted {
		t.Fatalf("a quoted <loaded_context> was stripped: %q", got)
	}
	// The compaction preamble titled real sessions.
	compacted := "This session is being continued from a previous conversation that ran out of context.\nfix the retry loop"
	if got := strings.TrimSpace(stripSelfRecall(compacted)); got != "fix the retry loop" {
		t.Fatalf("compaction preamble: got %q", got)
	}
}

// deja's own injected recall came back through the front door: Gemini stores
// it wrapped in <hook_context> and HTML-escaped, so the marker filter never
// saw it and deja indexed its own output as if a person had written it.
func TestUnwrapsHookContextAroundOwnRecall(t *testing.T) {
	in := "hi <hook_context>&lt;deja-recall&gt;\nRecalled history from prior sessions.\n1. [claude] proj · abc\n&lt;/deja-recall&gt;</hook_context>"
	got := strings.TrimSpace(stripSelfRecall(in))
	if got != "hi" {
		t.Fatalf("got %q, want only what the person typed", got)
	}
	for _, leak := range []string{"deja-recall", "Recalled history", "hook_context"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q survived into the index", leak)
		}
	}
}

// Unwrapping keeps text; stripping removes it. A wrapper whose contents are
// the person's words must never be dropped whole.
func TestUnwrapBlockKeepsContents(t *testing.T) {
	if got := unwrapBlock("a <user_query>the question</user_query> b", "<user_query>", "</user_query>"); got != "a the question b" {
		t.Fatalf("got %q", got)
	}
	// Unclosed: leave it alone rather than eating the rest of the message.
	in := "a <user_query>the question"
	if got := unwrapBlock(in, "<user_query>", "</user_query>"); got != in {
		t.Fatalf("got %q", got)
	}
	// Repeated wrappers all unwrap.
	got := unwrapBlock("<user_query>one</user_query> and <user_query>two</user_query>", "<user_query>", "</user_query>")
	if got != "one and two" {
		t.Fatalf("got %q", got)
	}
}

// A tool result that is nothing but deja's own recall block strips to empty,
// and a rebuild writes no record for it (#551). The pass that re-reads a
// changed transcript wrote it anyway, an empty tool-output between the turns
// (#5555). Each case rewrites its file in place, so the pass is the
// replacement one, not an append.
func TestAReReadDropsAToolResultThatIsOnlyRecall(t *testing.T) {
	const sid = "11111111-2222-4333-8444-555555555555"
	recall := "<deja-recall>\nRecalled history from prior sessions\n- example hit\n</deja-recall>"
	at := time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC)
	cases := []struct {
		harness, id string
		path        func() string
		body, extra string
	}{
		{
			harness: "claude", id: sid,
			path: func() string { return filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "demo", sid+".jsonl") },
			body: hjson(map[string]any{"type": "user", "sessionId": sid, "uuid": "u1", "timestamp": "2026-10-15T01:00:00.000Z", "cwd": "/tmp/demo",
				"message": map[string]any{"role": "user", "content": "what did we decide about the cache?"}}) +
				hjson(map[string]any{"type": "assistant", "sessionId": sid, "uuid": "a1", "parentUuid": "u1", "timestamp": "2026-10-15T01:00:01.000Z", "cwd": "/tmp/demo",
					"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "toolu_1", "name": "mcp__deja__deja", "input": map[string]any{"mode": "recall", "q": "cache"}}}}}) +
				hjson(map[string]any{"type": "user", "sessionId": sid, "uuid": "u2", "parentUuid": "a1", "timestamp": "2026-10-15T01:00:02.000Z", "cwd": "/tmp/demo",
					"message": map[string]any{"role": "user", "content": []any{map[string]any{"tool_use_id": "toolu_1", "type": "tool_result", "content": []any{map[string]any{"type": "text", "text": recall}}}}}}) +
				hjson(map[string]any{"type": "assistant", "sessionId": sid, "uuid": "a2", "parentUuid": "u2", "timestamp": "2026-10-15T01:00:03.000Z", "cwd": "/tmp/demo",
					"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "We decided to keep the cache."}}}}),
			extra: hjson(map[string]any{"type": "user", "sessionId": sid, "uuid": "u3", "parentUuid": "a2", "timestamp": "2026-10-15T01:00:04.000Z", "cwd": "/tmp/demo",
				"message": map[string]any{"role": "user", "content": "more"}}),
		},
		{
			harness: "codex", id: sid,
			path: func() string {
				return filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "10", "15", "rollout-2026-10-15T01-00-00-"+sid+".jsonl")
			},
			body: codexLine(at, "session_meta", map[string]any{"id": sid, "cwd": "/tmp/demo"}) +
				codexLine(at, "response_item", map[string]any{"type": "message", "role": "user",
					"content": []any{map[string]any{"type": "input_text", "text": "what did we decide about the cache?"}}}) +
				codexLine(at.Add(time.Second), "response_item", map[string]any{"type": "function_call", "name": "mcp__deja__deja", "call_id": "c1", "arguments": `{"mode":"recall","q":"cache"}`}) +
				codexLine(at.Add(2*time.Second), "response_item", map[string]any{"type": "function_call_output", "call_id": "c1", "output": recall}) +
				codexLine(at.Add(3*time.Second), "response_item", map[string]any{"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "We decided to keep the cache."}}}),
			extra: codexLine(at.Add(4*time.Second), "response_item", map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "more"}}}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.harness, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			dir := filepath.Join(tmp, "idx")
			p := tc.path()
			write(t, p, tc.body)
			if err := Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			appendTo(t, p, tc.extra)
			indexPass(t, dir)
			write(t, p, tc.body)
			bumpMTime(t, p)
			var progress strings.Builder
			if err := Ensure(dir, "", false, &progress); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(progress.String(), replacementPassMarker) {
				t.Fatalf("the restore did not take the re-read pass, so this measures nothing: %q", progress.String())
			}
			s, ok, err := FindByIdentity(dir, tc.harness, tc.id)
			if err != nil || !ok {
				t.Fatalf("session gone after the re-read (%v)", err)
			}
			for _, m := range s.Messages {
				if strings.TrimSpace(m.Text) == "" {
					t.Errorf("the re-read kept an empty %s record", m.Role)
				}
			}
			sessionMatchesRebuild(t, dir, tc.harness, tc.id)
		})
	}
}

// A transcript with nothing to index has no row after a rebuild (#868). The
// append pass and the re-read pass gave it one with no records under it, an
// untitled line in `deja last` (#5563).
func TestAPassGivesNoRowToATranscriptWithNothingToIndex(t *testing.T) {
	recallOnly := func(sid string) string {
		return hjson(map[string]any{"type": "user", "sessionId": sid, "uuid": "u2", "timestamp": "2026-10-15T01:00:01.000Z", "cwd": "/tmp/demo",
			"message": map[string]any{"role": "user", "content": []any{map[string]any{"tool_use_id": "toolu_1", "type": "tool_result",
				"content": []any{map[string]any{"type": "text", "text": "<deja-recall>\nRecalled history from prior sessions\n- example hit\n</deja-recall>"}}}}}})
	}
	hasRow := func(t *testing.T, dir, id string) bool {
		t.Helper()
		metas, err := AllMeta(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range metas {
			if m.ID == id {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		name, id, marker string
		change           func(t *testing.T, root string)
	}{
		{"a new file, appended", "s-c", "updated 1 file", func(t *testing.T, root string) {
			t.Helper()
			write(t, filepath.Join(root, "p", "c.jsonl"), recallOnly("s-c"))
		}},
		{"a file that held text, read again", "s-b", replacementPassMarker, func(t *testing.T, root string) {
			t.Helper()
			p := filepath.Join(root, "p", "b.jsonl")
			write(t, p, recallOnly("s-b"))
			bumpMTime(t, p)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			dir := filepath.Join(tmp, "idx")
			root := os.Getenv("DEJA_CLAUDE_ROOT")
			write(t, filepath.Join(root, "p", "a.jsonl"), claudeLine("s-a", "2026-10-15T01:00:00Z", "alpha one"))
			write(t, filepath.Join(root, "p", "b.jsonl"), claudeLine("s-b", "2026-10-15T01:00:00Z", "beta one"))
			if err := Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			tc.change(t, root)
			var progress strings.Builder
			if err := Ensure(dir, "", false, &progress); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(progress.String(), tc.marker) {
				t.Fatalf("the pass was not the one this case is about: %q", progress.String())
			}
			if hasRow(t, dir, tc.id) {
				t.Errorf("%s has a row with nothing under it", tc.id)
			}
			// The control: the session that holds text keeps its row.
			if !hasRow(t, dir, "s-a") {
				t.Error("the pass dropped s-a, which holds text")
			}
			sessionMatchesRebuild(t, dir, "claude", tc.id)
			sessionMatchesRebuild(t, dir, "claude", "s-a")
		})
	}
}

// spanOf is a session's Started and Updated as the index holds them.
func spanOf(t *testing.T, dir, key string) (started, updated time.Time) {
	t.Helper()
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.Harness+":"+m.ID == key {
			return m.Started.UTC(), m.Updated.UTC()
		}
	}
	t.Fatalf("%s is not in the index", key)
	return
}

// sameSpanAsRebuild fails where the row's Started or Updated differs from a
// fresh build of the same stores.
func sameSpanAsRebuild(t *testing.T, inc, key string) {
	t.Helper()
	fresh := inc + "-span"
	parityPass(t, fresh, true)
	s1, u1 := spanOf(t, inc, key)
	s2, u2 := spanOf(t, fresh, key)
	if !s1.Equal(s2) || !u1.Equal(u2) {
		t.Errorf("%s: incremental span %s..%s, rebuild %s..%s", key, s1, u1, s2, u2)
	}
}

// Codex appends records with a time and no message after a conversation's
// last turn, thread_settings_applied among them. A rebuild moved Updated to
// them; the append read no message, returned no session and left Updated
// where it was, so one file gave two rows (#4166).
func TestCodexTailWithNoMessageMovesUpdatedAsARebuildDoes(t *testing.T) {
	root := parityEnv(t, map[string]string{"DEJA_CODEX_ROOT": "codex"})
	const id = "01900000-0000-7000-8000-0000000000d1"
	p := filepath.Join(root, "codex", "sessions", "x", "rollout-2026-06-01T01-00-00-"+id+".jsonl")
	parityWrite(t, p, `{"timestamp":"2026-06-01T01:00:00.000Z","type":"session_meta","payload":{"id":"`+id+`","timestamp":"2026-06-01T01:00:00.000Z","cwd":"/tmp/x","originator":"codex_exec","cli_version":"0.1"}}
{"timestamp":"2026-06-01T01:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first question"}]}}
{"timestamp":"2026-06-01T01:00:09.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first answer"}]}}
`, false)
	dir := filepath.Join(root, "idx")
	parityPass(t, dir, true)
	parityWrite(t, p, `{"timestamp":"2026-06-01T05:00:00.000Z","type":"event_msg","payload":{"type":"thread_settings_applied"}}`+"\n", true)
	parityPass(t, dir, false)
	sameSpanAsRebuild(t, dir, "codex:"+id)
	sameAsRebuild(t, dir)
}

// Two files under one id, a Gemini resume stub beside its transcript. The
// full build took the other file's span into the row only when the row's
// owner was read second, so Updated came from the file names' order, and an
// update, which takes every file's span, matched only one of the two (#4253).
func TestASharedIDsSpanDoesNotDependOnWhichFileSortsFirst(t *testing.T) {
	var spans []string
	for _, tc := range []struct{ name, real, stub string }{
		{"stub after", "session-b-008140a7.jsonl", "session-z-008140a7.jsonl"},
		{"stub first", "session-b-008140a7.jsonl", "session-a-008140a7.jsonl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			write(t, filepath.Join(geminiChats(), tc.real), geminiOriginal())
			write(t, filepath.Join(geminiChats(), tc.stub), geminiStub())
			dir := filepath.Join(tmp, "idx")
			if err := Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			s, u := spanOf(t, dir, "gemini:"+resumeID)
			spans = append(spans, s.Format(time.RFC3339Nano)+".."+u.Format(time.RFC3339Nano))

			// The same files reached a pass at a time.
			inc := filepath.Join(tmp, "inc")
			if err := os.Rename(filepath.Join(geminiChats(), tc.stub), filepath.Join(tmp, "stub")); err != nil {
				t.Fatal(err)
			}
			if err := Ensure(inc, "", true, nil); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(tmp, "stub"), filepath.Join(geminiChats(), tc.stub)); err != nil {
				t.Fatal(err)
			}
			if err := Ensure(inc, "", false, nil); err != nil {
				t.Fatal(err)
			}
			if is, iu := spanOf(t, inc, "gemini:"+resumeID); !is.Equal(s) || !iu.Equal(u) {
				t.Errorf("an update holds %s..%s, the full build %s..%s", is, iu, s, u)
			}
		})
	}
	if len(spans) == 2 && spans[0] != spans[1] {
		t.Errorf("the full build's span follows the file names: %s with the stub sorting after, %s with it first", spans[0], spans[1])
	}
}

// When only the owner of a shared id is read again, its own span replaced the
// row's and the later Updated the stub gave was gone until a rebuild (#4574).
func TestRereadingASharedIDsOwnerKeepsTheOtherFilesSpan(t *testing.T) {
	for _, stub := range []string{"session-z-008140a7.jsonl", "session-a-008140a7.jsonl"} {
		t.Run(stub, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			realPath := filepath.Join(geminiChats(), "session-b-008140a7.jsonl")
			write(t, realPath, geminiOriginal())
			write(t, filepath.Join(geminiChats(), stub), geminiStub())
			dir := filepath.Join(tmp, "idx")
			if err := Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(realPath, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(`{"$set":{"summary":"add an email column"}}` + "\n"); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			sameSpanAsRebuild(t, dir, "gemini:"+resumeID)
		})
	}
}

func TestSessionStatsSurviveFullBuildAndIncrementalAppend(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(tmp, "index.db")
	transcript := filepath.Join(proj, "s1.jsonl")

	head := strings.Join([]string{
		`{"type":"user","sessionId":"s1","isSidechain":true,"agentId":"child1","attributionAgent":"general-purpose","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"check build"}}`,
		`{"type":"assistant","sessionId":"s1","isSidechain":true,"agentId":"child1","attributionAgent":"general-purpose","requestId":"r1","timestamp":"2026-01-02T03:04:06Z","message":{"id":"m1","model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":100,"output_tokens":30,"cache_read_input_tokens":400,"output_tokens_details":{"thinking_tokens":10}},"content":[{"type":"tool_use","id":"u1","name":"Bash","input":{"command":"go test ./..."}}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcript, []byte(head), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	tail := strings.Join([]string{
		`{"type":"user","sessionId":"s1","isSidechain":true,"agentId":"child1","attributionAgent":"general-purpose","timestamp":"2026-01-02T03:04:07Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"u1","is_error":true,"content":"Exit code 1\nFAIL"}]}}`,
		`{"type":"assistant","sessionId":"s1","isSidechain":true,"agentId":"child1","attributionAgent":"general-purpose","requestId":"r2","timestamp":"2026-01-02T03:04:08Z","message":{"id":"m2","model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":60,"output_tokens":20,"cache_read_input_tokens":200,"output_tokens_details":{"thinking_tokens":5}},"content":[{"type":"tool_use","id":"u2","name":"Edit","input":{"file_path":"/w/app/main.go","old_string":"a","new_string":"b"}},{"type":"text","text":"fixed main.go"}]}}`,
	}, "\n") + "\n"
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(tail); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	ss, err := Recent(dir, 10)
	if err != nil || len(ss) != 1 {
		t.Fatalf("Recent = %+v, %v", ss, err)
	}
	got := ss[0]
	if got.ID != "child1" || got.Kind != "sidechain" || got.Parent != "s1" || got.Agent != "general-purpose" {
		t.Errorf("lineage = (%q, %q, %q, %q)", got.ID, got.Kind, got.Parent, got.Agent)
	}
	wantModel := &model.Model{Provider: "anthropic", ID: "claude-opus-4-6"}
	if !reflect.DeepEqual(got.Model, wantModel) {
		t.Errorf("got.Model = %+v, want %+v", got.Model, wantModel)
	}
	wantTokens := &model.Tokens{Input: 160, Output: 50, Reasoning: 15, CacheRead: 600}
	if !reflect.DeepEqual(got.Tokens, wantTokens) {
		t.Errorf("got.Tokens = %+v, want %+v", got.Tokens, wantTokens)
	}
	wantTools := map[string]int{"Bash": 1, "Edit": 1}
	if !reflect.DeepEqual(got.ToolCalls, wantTools) {
		t.Errorf("got.ToolCalls = %+v, want %+v", got.ToolCalls, wantTools)
	}
	if got.ToolErrors != 1 {
		t.Errorf("got.ToolErrors = %d, want 1", got.ToolErrors)
	}

	// Re-delivering already-counted messages (empty newMessages tail) must be
	// idempotent and never double Tokens, ToolCalls, or ToolErrors; and a full
	// rebuild (`Ensure(..., true)`) must match the incremental SessionMeta.
	meta := textOf(got).meta
	extendOwnedRow(&meta, got, transcript, true, false)
	if !reflect.DeepEqual(meta.Tokens, wantTokens) {
		t.Fatalf("duplicate delivery doubled Tokens: got %+v, want %+v", meta.Tokens, wantTokens)
	}
	if !reflect.DeepEqual(meta.ToolCalls, wantTools) {
		t.Fatalf("duplicate delivery doubled ToolCalls: got %+v, want %+v", meta.ToolCalls, wantTools)
	}
	if meta.ToolErrors != 1 {
		t.Fatalf("duplicate delivery doubled ToolErrors: got %d, want 1", meta.ToolErrors)
	}

	if err = Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := Recent(dir, 10)
	if err != nil || len(rebuilt) != 1 {
		t.Fatalf("Recent after full rebuild = %+v, %v", rebuilt, err)
	}
	if !reflect.DeepEqual(rebuilt[0].Tokens, got.Tokens) ||
		!reflect.DeepEqual(rebuilt[0].ToolCalls, got.ToolCalls) ||
		rebuilt[0].ToolErrors != got.ToolErrors ||
		!reflect.DeepEqual(rebuilt[0].Model, got.Model) {
		t.Fatalf("incremental vs full rebuild drift:\nincremental: %+v\nrebuilt:     %+v", got, rebuilt[0])
	}
}

// The build derives each row's text fields ahead of the write loop, in
// parallel. The row has to be the one metaForSession gives for the same
// session, including when the loop hands the session an earlier row's title.
func TestSessionTextsRowIsMetaForSession(t *testing.T) {
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	openings := []string{
		"export GITHUB_TOKEN=ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8 then push",
		"  --password [redacted:credential] and postgres://app:S3cretPass@db.internal:5432/prod  ",
		"пароль: БазаПароль2026годаДлинный",
		"hi",
		"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdefghijk",
		strings.Repeat("x", maxIndexedText-10) + " password: hunter2hunter2",
		"",
	}
	ss := make([]model.Session, 0, len(openings))
	for i, o := range openings {
		s := model.Session{Harness: "codex", ID: "s" + string(rune('a'+i)), Path: "/tmp/s.jsonl", Project: "p", Started: at, Updated: at,
			Messages: []model.Message{
				{Role: "user", Text: o, Time: at},
				{Role: "assistant", Text: "ran go test ./... and it failed: undefined: foo", Time: at.Add(time.Second)},
				{Role: roleCommand, Text: "go test ./...", Time: at.Add(2 * time.Second)},
				{Role: "user", Text: "now fix the failing build in internal/index/ingest.go", Time: at.Add(3 * time.Second)},
			}}
		if i%2 == 1 {
			s.Title = o
		}
		ss = append(ss, s)
	}
	preRedactSessions(nil, ss)
	texts := sessionTexts(ss)
	for i, s := range ss {
		if got, want := texts[i].row(s), metaForSession(s); !reflect.DeepEqual(got, want) {
			t.Errorf("session %d: row\n got %+v\nwant %+v", i, got, want)
		}
		// What the loop changes before it writes the row.
		s.Title, s.Project, s.Started = "token sk-ant-api03-"+strings.Repeat("A", 90), "history", at.Add(-time.Hour)
		if got, want := texts[i].row(s), metaForSession(s); !reflect.DeepEqual(got, want) {
			t.Errorf("session %d with a borrowed title: row\n got %+v\nwant %+v", i, got, want)
		}
	}
}

func factSession(id string, msgs ...model.Message) model.Session {
	return model.Session{Harness: "claude", ID: id, Messages: msgs}
}

func at(minute int) time.Time {
	return time.Date(2026, 9, 1, 10, minute, 0, 0, time.UTC)
}

// What the reader runs after a hit is in the session, and the entry keeps the
// commands nearest the end — a long session's opening moves are usually the
// ones it abandoned.
func TestASessionFactKeepsTheCommandsTheSessionEndedOn(t *testing.T) {
	dir := t.TempDir()
	buildSessionFacts(dir, []model.Session{factSession("s1",
		model.Message{Role: roleCommand, Text: "make test  → exit 2", Time: at(2)},
		model.Message{Role: roleCommand, Text: "SVC_FIXTURES=./fixtures make test\nok  \texample.com/svc/internal/store\t0.6s  → exit 0", Time: at(4)},
	)})
	f, ok := SessionFactOf(dir, "claude", "s1")
	if !ok {
		t.Fatal("the table has no entry for a session that ran commands")
	}
	if len(f.Commands) != 2 {
		t.Fatalf("commands are %v", f.Commands)
	}
	if got := f.Commands[0]; got.Text != "SVC_FIXTURES=./fixtures make test" || !got.Passed() {
		t.Errorf("the command that passed is %#v; want it first and known-good", got)
	}
	if f.Commands[1].Passed() {
		t.Errorf("the failing command reads as passed: %#v", f.Commands[1])
	}
}

// An entry is evidence, so a command with no recorded status must not read as
// one that passed: most of the value is the difference between "was run here"
// and "worked here".
func TestACommandWithNoRecordedStatusDoesNotClaimToHavePassed(t *testing.T) {
	dir := t.TempDir()
	buildSessionFacts(dir, []model.Session{factSession("s2",
		model.Message{Role: roleCommand, Text: "go build ./...", Time: at(1)},
	)})
	f, _ := SessionFactOf(dir, "claude", "s2")
	if len(f.Commands) != 1 {
		t.Fatalf("commands are %v", f.Commands)
	}
	if f.Commands[0].Known || f.Commands[0].Passed() {
		t.Errorf("a command with no exit status reads as %#v", f.Commands[0])
	}
}

// Caps and duplicates: the same command run forty times is one fact.
func TestASessionFactIsCappedAndDeduped(t *testing.T) {
	dir := t.TempDir()
	msgs := make([]model.Message, 0, 30)
	for i := range 30 {
		msgs = append(msgs, model.Message{Role: roleCommand, Text: "make test  → exit 0", Time: at(i)})
	}
	buildSessionFacts(dir, []model.Session{factSession("s3", msgs...)})
	f, _ := SessionFactOf(dir, "claude", "s3")
	if len(f.Commands) != 1 {
		t.Errorf("the same command appears %d times", len(f.Commands))
	}
}

// A session that ran nothing earns no entry, and an index built
// before the table existed has none at all — both callers read as "nothing to
// add" rather than failing.
func TestNoEntryRatherThanAnEmptyOne(t *testing.T) {
	dir := t.TempDir()
	buildSessionFacts(dir, []model.Session{factSession("s4",
		model.Message{Role: "user", Text: "what did we decide about retries", Time: at(1)},
	)})
	if err := readGob(filepath.Join(dir, sessionFactsFile), new(map[string]SessionFact)); err == nil {
		t.Error("a session with nothing to record still wrote a table")
	}
	if facts := ReadSessionFacts(t.TempDir()); facts != nil {
		t.Errorf("an index with no table read as %v", facts)
	}
	if _, ok := SessionFactOf(t.TempDir(), "claude", "nobody"); ok {
		t.Error("a missing table answered for a session")
	}
}

// A session ends on a commit, a `gh pr checks` and some cleanup, and the
// command that proved the change is further up — so a table of the last few
// commands answers "how is this checked here" almost never. One slot past the
// cap is kept for it.
func TestTheCommandThatCheckedTheWorkSurvivesTheCap(t *testing.T) {
	dir := t.TempDir()
	msgs := []model.Message{ //nolint:prealloc // a fixture literal the loop below grows
		{Role: roleCommand, Text: "SVC_FIXTURES=./fixtures make test  → exit 0", Time: at(1)},
	}
	for i, c := range []string{"git add -A", "git commit -m wip", "gh pr create", "gh pr checks 1", "pkill -f codex"} {
		msgs = append(msgs, model.Message{Role: roleCommand, Text: c + "  → exit 0", Time: at(2 + i)})
	}
	buildSessionFacts(dir, []model.Session{factSession("s5", msgs...)})
	f, ok := SessionFactOf(dir, "claude", "s5")
	if !ok {
		t.Fatal("no entry for a session that ran commands")
	}
	var found bool
	for _, c := range f.Commands {
		found = found || strings.Contains(c.Text, "make test")
	}
	if !found {
		t.Errorf("the command that checked the work was dropped: %v", f.Commands)
	}
	if len(f.Commands) > sessionFactsCommands+1 {
		t.Errorf("the entry grew past one slot over the cap: %v", f.Commands)
	}
}

// An incremental update replaces the whole index directory, and the path that
// builds it never mines the sidecars — it deliberately never holds the whole
// corpus. So the swap used to delete them: one new session anywhere, and `fix`
// had nothing to answer with, ranking lost its query expansion, and neither
// came back until the next full rebuild. Ordinary use, silently degraded.
//
// This asserts the files survive, not what is in them. What is in them is
// allowed to be one update stale; being absent is not.
func TestIncrementalUpdateKeepsTheMinedSidecars(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)

	// An error followed by a command, twice, which is the evidence a fix pair
	// needs before deja will keep it.
	session := func(name, ts string) {
		body := `{"type":"user","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"user","content":"build broken"}}
{"type":"user","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"undefined: renderInvoice in vendor/billing/api.go"}]}]}}
{"type":"assistant","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go mod vendor && go build ./..."}}]}}
{"type":"assistant","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"assistant","content":"vendoring fixed it"}}
`
		if err := os.WriteFile(filepath.Join(proj, name+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	session("a", "2026-01-02T03:04:05Z")
	session("b", "2026-01-03T03:04:05Z")

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// Whatever the full build mined is what the incremental one has to keep.
	// cooccur needs more sessions than this fixture has and is carried by the
	// same code; asserting on it here would only pin the fixture's size.
	var want []string
	for _, name := range []string{fixesFile, commandsFile, cooccurFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			want = append(want, name)
		}
	}
	if len(want) < 2 {
		t.Fatalf("full build mined only %v — the fixture no longer clears the bars this test needs", want)
	}
	if len(FixesFor(dir, "undefined: renderInvoice in vendor/billing/api.go", 3, nil)) == 0 {
		t.Fatal("full build mined no fix pair; the fixture is not exercising what this test guards")
	}

	// One more session is all it takes: this is the incremental path.
	session("c", "2026-01-04T03:04:05Z")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range want {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("an incremental update dropped %s", name)
		}
	}
	if len(FixesFor(dir, "undefined: renderInvoice in vendor/billing/api.go", 3, nil)) == 0 {
		t.Error("fix went silent after an incremental update")
	}

	// Keeping the old pairs is half of it. A new session is a new file, and a
	// new file never takes the append path, so sessions arriving after the
	// first build are the only source of new pairs and all of them come
	// through here. Carrying alone left fix frozen at whatever the full build
	// had seen — which this assertion, and not the one above, is what catches.
	settled := func(name, ts string) {
		body := `{"type":"user","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"user","content":"migration broke"}}
{"type":"user","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"connection refused: flimbardb:5432"}]}]}}
{"type":"assistant","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"docker compose restart flimbardb"}}]}}
{"type":"assistant","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"assistant","content":"restarting it cleared the migration"}}
`
		if err := os.WriteFile(filepath.Join(proj, name+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	settled("d", "2026-02-01T03:04:05Z")
	settled("e", "2026-02-02T03:04:05Z")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if len(FixesFor(dir, "connection refused: flimbardb:5432", 3, nil)) == 0 {
		t.Error("a pair from a session that arrived after the full build was never mined")
	}
	// And the old one is still there: learning must not cost what was carried.
	if len(FixesFor(dir, "undefined: renderInvoice in vendor/billing/api.go", 3, nil)) == 0 {
		t.Error("mining the new sessions dropped the carried pairs")
	}

	// The command table has the same shape of problem and needed a different
	// answer. Its counters are aggregates — distinct sessions, per project —
	// and a subset cannot be merged into one, so it is recomputed from the
	// records the index already holds. hook-tool is its only reader, and it
	// stayed silent about every command that became a habit after the last full
	// build, which is every habit: the sessions that make one arrive through
	// this path. Both new sessions above ran the same command.
	use, ok := CommandHistory(dir, "docker compose restart flimbardb")
	if !ok {
		t.Error("a command that became a habit after the full build is missing from the table")
	} else if use.Sessions != 2 {
		t.Errorf("command counted in %d sessions, want 2", use.Sessions)
	}
}

// Both full-build paths scrub the sessions in place before mining anything from
// them, because the record loop redacts into a local and leaves the sessions
// holding the raw text. The incremental path had no such call, so a secret in
// an error line or a command reached fixes.gob whole — while rebuilding the
// same corpus from scratch produced a clean one. Which build path a machine
// happened to take decided whether its credentials were written to disk.
//
// The assertion is on the bytes of the index, not on what a command prints: the
// leak is the file, and every reader of it inherits the problem.
func TestIncrementalUpdateRedactsWhatItMines(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)

	const secret = "sk-live-ZZTOPSECRET1234567890abcd" //nolint:gosec // a fake secret the redaction test plants
	plain := func(name, ts string) {
		body := `{"type":"user","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"user","content":"ordinary work"}}
{"type":"assistant","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"assistant","content":"nothing special"}}
`
		if err := os.WriteFile(filepath.Join(proj, name+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// An error carrying a credential, settled by a command carrying the same
	// one — which is what makes it a pair worth mining.
	leaky := func(name, ts string) {
		body := `{"type":"user","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"user","content":"deploy is broken"}}
{"type":"user","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"connection refused: db.internal:5432 for ` + secret + `"}]}]}}
{"type":"assistant","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"kubectl create secret generic api --from-literal=token=` + secret + `"}}]}}
{"type":"assistant","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"assistant","content":"recreated it and the deploy came back"}}
`
		if err := os.WriteFile(filepath.Join(proj, name+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	dir := filepath.Join(tmp, "index.db")
	plain("a", "2026-02-01T03:04:05Z")
	plain("b", "2026-02-02T03:04:05Z")
	plain("c", "2026-02-03T03:04:05Z")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if found := filesHolding(t, dir, secret); len(found) > 0 {
		t.Fatalf("the full build already leaks into %v — this test can no longer tell the paths apart", found)
	}

	// The sessions holding it arrive afterwards: the incremental path.
	leaky("d", "2026-03-01T03:04:05Z")
	leaky("e", "2026-03-02T03:04:05Z")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// Absence of the secret proves nothing if nothing was mined. Pin that the
	// pair exists first, so this cannot pass by the fixture quietly stopping to
	// qualify — the evidence rules in fixPairsIn are strict and have moved
	// before.
	if len(ReadFixes(dir)) == 0 {
		t.Fatal("no pair was mined, so the assertion below would hold for the wrong reason")
	}
	if found := filesHolding(t, dir, secret); len(found) > 0 {
		t.Errorf("an incremental update wrote the raw credential into %v", found)
	}

	// A machine that already ran the leaky build carries its poisoned file
	// forward, and carrying is all that ever happens to a pair whose session is
	// not re-read. Simulate that: put the credential back into the pair on disk
	// and let one ordinary session trigger an update.
	pairs := ReadFixes(dir)
	pairs[0].Error = "connection refused: db.internal:5432 for " + secret
	if err := writeGob(filepath.Join(dir, fixesFile), pairs); err != nil {
		t.Fatal(err)
	}
	plain("f", "2026-04-01T03:04:05Z")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if found := filesHolding(t, dir, secret); len(found) > 0 {
		t.Errorf("a credential carried in from an older index survived the update, in %v", found)
	}
}

// filesHolding names the files under dir whose bytes contain want.
func filesHolding(t *testing.T, dir, want string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil //nolint:nilerr // best-effort walk
		}
		b, err := os.ReadFile(p) //nolint:gosec // walks the test's own temp dir
		if err != nil {
			return nil //nolint:nilerr // best-effort walk
		}
		if strings.Contains(string(b), want) {
			out = append(out, filepath.Base(p))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// onlySession names the one session the index holds.
func onlySession(t *testing.T, dir string) (harness, id string) {
	t.Helper()
	metas, err := AllMeta(dir)
	if err != nil || len(metas) != 1 {
		t.Fatalf("want one session in the index, have %d (%v)", len(metas), err)
	}
	return metas[0].Harness, metas[0].ID
}

// Five readers take a session's title, workspace or start from a file beside
// the transcript, which the agent writes on its own: late, after the
// transcript, and again on a rename. A change to that file alone never
// reached the incremental index, only a rebuild (#4446, after #4319 for
// cline-sdk).
func TestASidecarChangeAloneIsReread(t *testing.T) {
	rooTask := `[{"role":"user","content":[{"type":"text","text":"<task>\nfix the retry loop\n</task>"}],"ts":1790762400000},{"role":"assistant","content":[{"type":"text","text":"looking at the retry loop"}],"ts":1790762410000}]`
	rooItem := func(id string) func(string) string {
		return func(title string) string {
			return fmt.Sprintf(`{"id":%q,"number":1,"ts":1790762460000,"task":%q,"workspace":"/tmp/proj"}`, id, title)
		}
	}
	cases := []struct {
		name       string
		env        map[string]string // values are under the store root
		transcript string            // relative to the store root
		body       string
		sidecar    string // relative to the store root
		meta       func(title string) string
	}{{
		name:       "kilocode-task",
		env:        map[string]string{"DEJA_KILO_ROOTS": "kilo", "DEJA_KILO_DB": "absent-kilo.db"},
		transcript: "kilo/tasks/0b7c1f9e-2d3a-4e5f-8a9b-1c2d3e4f5a6b/api_conversation_history.json",
		body:       rooTask,
		sidecar:    "kilo/tasks/0b7c1f9e-2d3a-4e5f-8a9b-1c2d3e4f5a6b/history_item.json",
		meta:       rooItem("0b7c1f9e-2d3a-4e5f-8a9b-1c2d3e4f5a6b"),
	}, {
		name:       "roo",
		env:        map[string]string{"DEJA_ROO_ROOTS": "roo", "DEJA_ROO_CLI_ROOT": "roocli"},
		transcript: "roo/tasks/1790762400000/api_conversation_history.json",
		body:       rooTask,
		sidecar:    "roo/tasks/1790762400000/history_item.json",
		meta:       rooItem("1790762400000"),
	}, {
		name:       "cline-vscode",
		env:        map[string]string{"DEJA_CLINE_ROOT": "none", "DEJA_CLINE_ROOTS": "cline-ext"},
		transcript: "cline-ext/tasks/1767300000000/api_conversation_history.json",
		body:       `[{"role":"user","content":[{"type":"text","text":"<task>\nfix the retry loop\n</task>"}]},{"role":"assistant","content":[{"type":"text","text":"looking at the retry loop"}]}]`,
		sidecar:    "cline-ext/state/taskHistory.json",
		meta: func(title string) string {
			return fmt.Sprintf(`[{"id":"1767999999999","ts":1767999999999,"task":"another task"},{"id":"1767300000000","ts":1767300000000,"task":%q,"cwdOnTaskInitialization":"/tmp/proj","tokensIn":12}]`, title)
		},
	}, {
		name:       "reasonix",
		env:        map[string]string{"DEJA_REASONIX_ROOT": "rx"},
		transcript: "rx/projects/-tmp-proj/sessions/20260930-100000.000000000-deepseek-chat.jsonl",
		body: `{"role":"user","content":"fix the retry loop","createdAt":1790762410000}` + "\n" +
			`{"role":"assistant","content":"looking at the retry loop","createdAt":1790762420000}` + "\n",
		sidecar: "rx/projects/-tmp-proj/sessions/20260930-100000.000000000-deepseek-chat.jsonl.meta",
		meta: func(title string) string {
			return fmt.Sprintf(`{"id":"20260930-100000.000000000-deepseek-chat","created_at":"2026-09-30T10:00:00Z","updated_at":"2026-09-30T10:00:20Z","workspace_root":"/tmp/proj","custom_title":%q}`, title)
		},
	}, {
		name:       "kimi",
		env:        map[string]string{"DEJA_KIMI_ROOT": "kimi"},
		transcript: "kimi/sessions/wd_proj_0123456789ab/session_retry01/agents/main/wire.jsonl",
		body: `{"type":"metadata","protocol_version":"1.4"}` + "\n" +
			`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}],"toolCalls":[]},"time":1788253201000}` + "\n",
		sidecar: "kimi/sessions/wd_proj_0123456789ab/session_retry01/state.json",
		meta: func(title string) string {
			return fmt.Sprintf(`{"createdAt":"2026-09-01T09:00:00.000Z","updatedAt":"2026-09-01T09:00:02.000Z","title":%q,"workDir":"/tmp/proj"}`, title)
		},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			isolateStores(t, tmp)
			root := filepath.Join(tmp, "store")
			for k, v := range tc.env {
				t.Setenv(k, filepath.Join(root, v))
			}
			at := time.Now().Add(-time.Hour)
			writeAt(t, filepath.Join(root, tc.transcript), tc.body, at)
			dir := filepath.Join(tmp, "index.db")
			indexPass(t, dir)
			harness, id := onlySession(t, dir)

			// The sidecar lands after the transcript, the order Roo and Kilo
			// write a new task in.
			sidecar := filepath.Join(root, tc.sidecar)
			writeAt(t, sidecar, tc.meta("fix the retry loop"), at.Add(time.Minute))
			indexPass(t, dir)
			matchesRebuild(t, dir, harness, id)

			// A rename rewrites it alone, same size.
			writeAt(t, sidecar, tc.meta("cap the retry at three"), at.Add(2*time.Minute))
			indexPass(t, dir)
			matchesRebuild(t, dir, harness, id)
		})
	}
}

// A rename that lands with an appended turn reaches the append path, which
// sees only the tail. Renamed to a title too thin to keep, the row kept the
// long title it had before the rename, where a rebuild widens the new one
// from the session's first turn, and it stayed until a rebuild (#4592).
func TestRenameToAThinTitleWithAnAppendedTurn(t *testing.T) {
	tmp := t.TempDir()
	isolateStores(t, tmp)
	root := filepath.Join(tmp, "store")
	t.Setenv("DEJA_KIMI_ROOT", filepath.Join(root, "kimi"))
	session := filepath.Join(root, "kimi", "sessions", "wd_proj_0123456789ab", "session_retry01")
	wire := filepath.Join(session, "agents", "main", "wire.jsonl")
	state := filepath.Join(session, "state.json")
	meta := func(title string) string {
		return fmt.Sprintf(`{"createdAt":"2026-09-01T09:00:00.000Z","updatedAt":"2026-09-01T09:00:02.000Z","title":%q,"workDir":"/tmp/proj"}`, title)
	}
	turn := func(text string, ms int64) string {
		return fmt.Sprintf(`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":%q}],"toolCalls":[]},"time":%d}`, text, ms) + "\n"
	}
	body := `{"type":"metadata","protocol_version":"1.4"}` + "\n" + turn("fix the retry loop", 1788253201000)
	at := time.Now().Add(-time.Hour)
	writeAt(t, wire, body, at)
	writeAt(t, state, meta("investigate the flaky retry loop in the http client"), at)
	dir := filepath.Join(tmp, "index.db")
	indexPass(t, dir)

	writeAt(t, state, meta("retry loop"), at.Add(time.Minute))
	body += turn("now run the tests", 1788253261000)
	writeAt(t, wire, body, at.Add(time.Minute))
	indexPass(t, dir)
	harness, id := onlySession(t, dir)
	matchesRebuild(t, dir, harness, id)

	// The next turn alone is a tail too; the row has to stay right after it.
	writeAt(t, wire, body+turn("and the lint", 1788253321000), at.Add(2*time.Minute))
	indexPass(t, dir)
	matchesRebuild(t, dir, harness, id)
}

// A long tool output is stored as its head plus its tail, and both ends were
// cut at a byte count. The result is indexed and served back through recall, so
// a character split there is a broken byte inside an answer (#1319). One byte
// of padding moves the cut off the boundary it happened to land on.
func TestSignalLinesCutsOnRuneBoundaries(t *testing.T) {
	for _, pad := range []string{"", "a", "ab"} {
		text := pad + strings.Repeat("сборка упала на предпродакшене ", 400)
		got := signalLines(text)
		if !utf8.ValidString(got) {
			t.Errorf("pad %q: the stored output is not valid UTF-8", pad)
		}
		if len(got) == 0 {
			t.Errorf("pad %q: nothing was kept", pad)
		}
	}
}

// The keeping rule itself is unchanged: a short output survives whole, and a
// long one keeps both ends rather than only its head.
func TestSignalLinesStillKeepsBothEnds(t *testing.T) {
	short := "npm ERR! code ELIFECYCLE"
	if got := signalLines(short); got != short {
		t.Errorf("a short output was changed: %q", got)
	}
	head := strings.Repeat("a", signalFloor)
	tail := "the error at the very end: ELIFECYCLE"
	got := signalLines(head + strings.Repeat("b", signalTail*2) + tail)
	if !strings.Contains(got, tail) {
		t.Errorf("the tail, where errors cluster, was dropped:\n%q", got[max(0, len(got)-80):])
	}
	if !strings.HasPrefix(got, "aaa") {
		t.Errorf("the head was dropped: %q", got[:20])
	}
}

// A store deja read and could not use is the one case where nothing else will
// mention it: the skip line covers a store it could not open, and the count is
// computed and then thrown away for a store that yielded no session because its
// file would not parse (#2229). That is #794's shape, one branch over.
func TestAStoreThatYieldedNothingButRefusedLinesIsNarrated(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	cline := filepath.Join(tmp, "cline")
	t.Setenv("DEJA_CLINE_ROOT", cline)

	// One healthy store, so the run has a line to print either way and the
	// silence below is about the other one.
	proj := filepath.Join(claude, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"s1","timestamp":%q,"cwd":"/work/app",`+
		`"message":{"role":"user","content":"the pool timed out during the migration"}}`, stamp)
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// And one store whose only session will not parse.
	if err := os.MkdirAll(cline, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("[")
	for i := range 50 {
		fmt.Fprintf(&b, `{"ts":%d,"type":"say","say":"text","text":"turn %d about the pool"},`, 1782900000+i, i)
	}
	truncated := strings.TrimSuffix(b.String(), ",") // no closing bracket
	if err := os.WriteFile(filepath.Join(cline, "1234567890.messages.json"), []byte(truncated), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(tmp, "index.db")
	var out strings.Builder
	if err := Ensure(dir, "", true, &out); err != nil {
		t.Fatal(err)
	}
	said := out.String()

	// The premise: deja did notice, and filed it where doctor reads. Either
	// bucket counts — a task that will not parse is a path deja could not read
	// (#2232), a line inside a JSONL transcript is a line.
	health := IngestHealth(dir)
	var counted int
	for _, e := range health {
		counted += e.MalformedLines + e.FailedFiles
	}
	if counted == 0 {
		t.Fatalf("nothing was recorded as unreadable, so this measures nothing:\n%s\n%v", said, health)
	}
	// The healthy store is narrated, or the run said nothing at all.
	if !strings.Contains(said, "claude: 1 session") {
		t.Fatalf("the run did not narrate the store it read:\n%s", said)
	}
	// And the one it could not use is named too.
	if !strings.Contains(said, "cline") {
		t.Errorf("the run says nothing about the store whose session it refused:\n%s", said)
	}
}

// A store that reads slowly has to say so while it is happening. An index run
// over a 520 MB opencode store printed one line and then nothing for 13m54s,
// and nothing on screen said which store it was in or that it was still moving
// (#3555).
func TestAStoreThatReadsSlowlySaysSoWhileItIsReading(t *testing.T) {
	defer shortenSlowRead(t, 20*time.Millisecond, 20*time.Millisecond)()
	w := &safeBuilder{}
	stop := sayItIsStillReading("opencode", time.Now(), w)
	waitFor(t, func() bool { return strings.Contains(w.String(), "still reading") })
	stop()

	line := firstLine(w.String())
	if !strings.HasPrefix(line, "deja: opencode: still reading (") {
		t.Fatalf("line = %q — it has to name the store and that it is still moving", line)
	}
	// And it stops when the read lands, rather than narrating a store that is
	// already indexed.
	after := w.String()
	time.Sleep(80 * time.Millisecond)
	if w.String() != after {
		t.Fatalf("kept talking after the read landed:\n%s", w.String())
	}
}

// The notes pseudo-source narrates under the name a person knows it by.
func TestTheStillReadingLineUsesTheNameAPersonSees(t *testing.T) {
	defer shortenSlowRead(t, 10*time.Millisecond, 10*time.Millisecond)()
	w := &safeBuilder{}
	stop := sayItIsStillReading("deja", time.Now(), w)
	waitFor(t, func() bool { return strings.Contains(w.String(), "still reading") })
	stop()
	if !strings.Contains(w.String(), "deja: notes: still reading") {
		t.Fatalf("line = %q", firstLine(w.String()))
	}
}

// Nothing is said for a store that reads at the ordinary speed, and the wait
// that was worth reporting is reported.
func TestOnlyAWaitWorthReportingReachesTheLine(t *testing.T) {
	const line = "deja: opencode: 227 sessions, 3731 messages"
	if got := withReadTime(line, 900*time.Millisecond); got != line {
		t.Errorf("a fast read stamped its time: %q", got)
	}
	if got := withReadTime(line, 14*time.Minute); got != line+" — the read took 14m0s" {
		t.Errorf("got %q", got)
	}
}

// A suppressed narration stays suppressed: the terminal build paints its own
// display and a raw line would land in the middle of it.
func TestTheStillReadingLineRespectsASilencedRun(t *testing.T) {
	defer shortenSlowRead(t, 5*time.Millisecond, 5*time.Millisecond)()
	SuppressHarnessNarration = true
	defer func() { SuppressHarnessNarration = false }()
	w := &safeBuilder{}
	stop := sayItIsStillReading("opencode", time.Now(), w)
	time.Sleep(40 * time.Millisecond)
	stop()
	if w.String() != "" {
		t.Fatalf("wrote %q into a silenced run", w.String())
	}
}

func shortenSlowRead(t *testing.T, after, every time.Duration) func() {
	t.Helper()
	oldAfter, oldEvery := readSlowAfter, readStillReadingEvery
	readSlowAfter, readStillReadingEvery = after, every
	return func() { readSlowAfter, readStillReadingEvery = oldAfter, oldEvery }
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("nothing said while the read was still running")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func firstLine(s string) string {
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return before
	}
	return s
}

// safeBuilder is the progress writer for these tests: the notice runs in its
// own goroutine, so the test reads what it wrote under the same lock.
type safeBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *safeBuilder) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuilder) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// A control byte in tracked text is invisible in an editor and invisible in a
// diff, so it survives review by being unreadable. One is in this repository:
// a comment in `cmd/deja/doctor_sync_json_test.go` said the bytes on the wire
// carry no raw escape and carried one (#1983). Another reached a comment on a
// branch during this work and was found only because a reviewer ran `od -c`
// over the diff, which is not a review anyone can be asked to repeat.
//
// Tab, newline and carriage return are the three a text file is made of. A file
// holding a NUL is taken as binary and skipped — today that is two PNGs and two
// GIFs and nothing else, no fixture among them, and the cost of the heuristic is
// that a text file which somehow contains a NUL is skipped with them.
func TestNoControlBytesInTrackedText(t *testing.T) {
	root := repoRoot(t)
	names, err := filesUnderReview(root)
	if err != nil {
		t.Skipf("no git checkout to ask: %v", err)
	}

	var found []string
	for _, name := range names {
		if len(name) == 0 {
			continue
		}
		path := filepath.Join(root, name)
		b, err := os.ReadFile(path)
		if err != nil {
			// A file that cannot be read is not this test's finding, but it
			// is not a pass either.
			if !os.IsNotExist(err) {
				t.Errorf("cannot read %s: %v", name, err)
			}
			continue
		}
		if bytes.IndexByte(b, 0) >= 0 {
			continue
		}
		for i, c := range b {
			if c == '\t' || c == '\n' || c == '\r' {
				continue
			}
			if c < 0x20 || c == 0x7f {
				line := 1 + bytes.Count(b[:i], []byte("\n"))
				found = append(found, fmt.Sprintf("  %s:%d: byte 0x%02x", name, line, c))
				break
			}
		}
	}
	if len(found) > 0 {
		t.Errorf("control bytes in the files a reviewer would see, which no editor and no diff will show:\n%s",
			strings.Join(found, "\n"))
	}
}

// filesUnderReview is what git would show a reviewer: everything tracked, plus
// the files that are untracked and not ignored.
//
// From git rather than from the filesystem, for the reason the test's comment
// gives: a walk reads a developer's build output, their .venv and the
// agent-session directories this checkout's .gitignore expects, all of them
// full of captured terminal escapes, and that failure lands only on the
// machines with junk in the tree.
//
// `--others --exclude-standard` is the other half of the same list. Without it
// the guard is silent exactly while a new file is being written and speaks only
// after `git add`, in CI, on someone else's machine — which is how a literal
// escape byte reached #2091.
func filesUnderReview(root string) ([]string, error) {
	out, err := exec.CommandContext(context.Background(), "git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for name := range bytes.SplitSeq(out, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		names = append(names, string(name))
	}
	return names, nil
}

// repoRoot walks up to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}

// The list is what a reviewer would see, which includes a file that has not
// been added yet — the guard used to be silent exactly while one was being
// written (#2098).
func TestTheFileListHoldsANewFileBeforeItIsAdded(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	// Without its own config the scratch repo inherits the developer's
	// core.excludesFile, and what this test asserts about ignoring is then
	// whatever that file happens to say.
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "junk/\n")
	write("committed.go", "package p\n")
	git("add", ".gitignore", "committed.go")
	git("commit", "-q", "-m", "seed")
	write("brand_new.go", "package p\n")
	write("junk/captured.log", "\x1b[31m\n")

	names, err := filesUnderReview(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	if !got["committed.go"] {
		t.Error("a committed file is missing from the list")
	}
	if !got["brand_new.go"] {
		t.Error("a file that has not been added yet is missing from the list")
	}
	if got["junk/captured.log"] {
		t.Error("an ignored file is in the list, which is what reading the filesystem would do")
	}
}

// A pass that runs while a command is still running reads its call without
// the result. The result lands by the next pass, in the tail it appends, and
// the command stored already never got the exit 0 a rebuild gives it: `deja`
// said it ran, never that it passed. A clean answer to a call that ran no
// command is still appended.
func TestACommandWhoseResultLandsAPassLaterGetsItsExit(t *testing.T) {
	for _, tc := range []struct {
		name, call string
	}{
		{"command", hBash("app", "s1", 10, 7, "x", "go build ./...", "ok", false)},
		{"edit", hEdit("app", "s1", 10, 7, "x", "main.go", "a()", "b()")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTwoWayEnv(t)
			a := filepath.Join(h.tmp, "a.db")
			h.put("app", "s1", hPrompt("app", "s1", 10, 0, "why does the build fail"))
			if err := Ensure(a, "", false, nil); err != nil {
				t.Fatal(err)
			}
			call, result, _ := strings.Cut(tc.call, "\n")
			h.add("app", "s1", call+"\n")
			if err := Ensure(a, "", false, nil); err != nil {
				t.Fatal(err)
			}
			h.add("app", "s1", result)
			if err := Ensure(a, "", false, nil); err != nil {
				t.Fatal(err)
			}
			b := filepath.Join(h.tmp, "b.db")
			copyDir(t, a, b)
			if err := Ensure(b, "", true, nil); err != nil {
				t.Fatal(err)
			}
			diffSnaps(t, tc.name, takeSnap(t, a), takeSnap(t, b))
		})
	}
}
