package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/stats"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/usage"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// "Use a longer prefix" cannot be followed when the ids are the same string;
// naming the harness (harness:id) is the only thing that separates them (#719).
func TestShowNamesHarnessWhenIDsAreShared(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude", "proj-p")
	qwen := filepath.Join(tmp, "qwen", "projects", "proj-z", "chats")
	for _, d := range []string{claude, qwen} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	write := func(p, body string) {
		if err := os.WriteFile(p, []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(claude, "abc12345.jsonl"),
		`{"type":"user","sessionId":"abc12345","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"claude side"}}`)
	write(filepath.Join(claude, "abc99999.jsonl"),
		`{"type":"user","sessionId":"abc99999","cwd":"/w/p","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"other claude session"}}`)
	write(filepath.Join(qwen, "abc12345.jsonl"),
		`{"type":"user","sessionId":"abc12345","timestamp":"2026-07-25T10:00:00Z","message":{"role":"user","parts":[{"text":"qwen side"}]}}`)
	// A session whose whole id is also a prefix of the others: the prefix is
	// ambiguous, the id is not, and only the longer-prefix advice fits.
	write(filepath.Join(claude, "abc.jsonl"),
		`{"type":"user","sessionId":"abc","cwd":"/w/p","timestamp":"2026-07-19T10:00:00Z","message":{"role":"user","content":"the short one"}}`)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	shared, err := captureRunStderr(t, "show", "abc12345")
	if err != nil {
		t.Fatal(err)
	}
	// The advice names the harness:id form, which every selector-resolving
	// command accepts — promote/handoff/resume/share reject --harness (#872).
	if !strings.Contains(shared, "share the id") || !strings.Contains(shared, "claude:abc12345 or qwen:abc12345") {
		t.Errorf("shared id: %q", shared)
	}
	if strings.Contains(shared, "--harness") {
		t.Errorf("still advised a flag some commands reject: %q", shared)
	}
	if strings.Contains(shared, "longer prefix") {
		t.Errorf("still advised a longer prefix: %q", shared)
	}

	// An ordinary ambiguous prefix keeps the advice that works there.
	prefix, err := captureRunStderr(t, "show", "ab")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prefix, "longer prefix") {
		t.Errorf("prefix case: %q", prefix)
	}
	if strings.Contains(prefix, "share the id") {
		t.Errorf("a unique id was reported as shared: %q", prefix)
	}

	// A complete id that also starts other ids is that session: no longer
	// prefix reaches it, so it is read, and without the ambiguity note.
	whole, err := captureRunStderr(t, "show", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(whole, "sessions match") {
		t.Errorf("a complete id was reported as ambiguous: %q", whole)
	}
	if out, showErr := captureRun(t, "show", "abc"); showErr != nil || !strings.Contains(out, "the short one") {
		t.Errorf("show abc read another session: %v\n%s", showErr, out)
	}

	// One match, no chatter.
	quiet, err := captureRunStderr(t, "show", "abc99999")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(quiet, "sessions") {
		t.Errorf("unambiguous id said %q", quiet)
	}
}

// --harness narrows a prefix to that harness's sessions. The newest match
// across every harness was picked first and then refused, so `show c --harness
// codex` said no session matches while a codex session started with "c", and
// --json answered a prefix that was still ambiguous within the harness.
func TestShowHarnessNarrowsAPrefix(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude", "proj-p")
	qwen := filepath.Join(tmp, "qwen", "projects", "proj-z", "chats")
	for _, d := range []string{claude, qwen} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	write := func(p, body string) {
		if err := os.WriteFile(p, []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(qwen, "abq1.jsonl"),
		`{"type":"user","sessionId":"abq1","timestamp":"2026-07-18T10:00:00Z","message":{"role":"user","parts":[{"text":"older qwen"}]}}`)
	write(filepath.Join(qwen, "abq2.jsonl"),
		`{"type":"user","sessionId":"abq2","timestamp":"2026-07-19T10:00:00Z","message":{"role":"user","parts":[{"text":"newer qwen"}]}}`)
	write(filepath.Join(claude, "abc9.jsonl"),
		`{"type":"user","sessionId":"abc9","cwd":"/w/p","timestamp":"2026-07-25T10:00:00Z","message":{"role":"user","content":"newest overall"}}`)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "show", "ab", "--harness", "qwen")
	if err != nil {
		t.Fatalf("a qwen session starts with the prefix: %v", err)
	}
	if !strings.Contains(out, "abq2") {
		t.Fatalf("want the newest qwen match:\n%s", out)
	}
	note, _ := captureRunStderr(t, "show", "ab", "--harness", "qwen")
	if !strings.Contains(note, "2 qwen sessions match") {
		t.Errorf("no ambiguity note within the harness: %q", note)
	}
	if _, err := captureRun(t, "show", "ab", "--harness", "qwen", "--json"); err == nil || !strings.Contains(err.Error(), "--json reads one") {
		t.Errorf("--json answered an ambiguous prefix: %v", err)
	}
	if _, err := captureRun(t, "show", "abq1", "--harness", "qwen", "--json"); err != nil {
		t.Errorf("an exact id with --harness: %v", err)
	}
}

// A pasted log is one message, and the index stores the first 64 KB of it.
// `deja show` printed that much and said nothing, so a reader looking for the
// line that explains a failure searched a log they believed was whole — the
// "showed part, read as whole" family, on the door where the whole point is to
// read a session as it was (#2467).
func TestShowSaysWhenAMessageWasClipped(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	// Longer than what the index keeps for one message.
	huge := strings.Repeat("2026-08-28T10:00:00Z INFO orders.pipeline step finished with pool=8 retries=0\n", 1800)
	line := `{"type":"user","sessionId":"huge","timestamp":"` + at + `","cwd":"/work/app",` +
		`"message":{"role":"user","content":` + jsonString(huge) + `}}`
	if err := os.WriteFile(filepath.Join(store, "huge.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, errOut := captureBoth(t, "show", "huge")
	whole := out + errOut
	if len(out) >= len(huge) {
		t.Fatalf("the fixture was not clipped at all: %d bytes shown", len(out))
	}
	if !strings.Contains(whole, "stored short of what the transcript holds") {
		t.Errorf("show printed part of a message and said nothing:\n%.400s", tail(whole))
	}
}

// tail is the end of the output, where a note about the message would sit.
func tail(s string) string {
	if len(s) <= 400 {
		return s
	}
	return s[len(s)-400:]
}

// Zed keeps every thread in one threads.db, and the clip count is recorded per
// file. Looked up by path, one pasted log anywhere in the store made every
// thread in it carry the note — a two-line session told the reader it was
// incomplete (#4340).
func TestShowClippedNoteStaysOnTheSessionThatHoldsIt(t *testing.T) {
	tmp := hermeticEnv(t)
	if !sources.SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	huge := strings.Repeat("2026-08-28T10:00:00Z INFO orders.pipeline step finished with pool=8 retries=0\n", 1800)
	row := func(id, user, reply string) string {
		doc := `{"version":"0.2.0","summary":"t","updated_at":"2026-09-30T10:00:02Z","messages":[` +
			`{"id":1,"role":"user","segments":[{"type":"text","text":` + jsonString(user) + `}]},` +
			`{"id":2,"role":"assistant","segments":[{"type":"text","text":` + jsonString(reply) + `}]}]}`
		return "insert into threads (id, summary, updated_at, data_type, data, folder_paths) values ('" + id +
			"', 't', '2026-09-30T10:00:02+00:00', 'json', '" + strings.ReplaceAll(doc, "'", "''") + "', '/work/proj');\n"
	}
	sql := "create table threads (id text primary key, summary text not null, updated_at text not null, " +
		"data_type text not null, data blob not null, parent_id text, folder_paths text, folder_paths_order text, created_at text);\n" +
		row("33333333-0000-4000-8000-000000000003", huge, "that log ends in a timeout") +
		row("44444444-0000-4000-8000-000000000004", "fix the retry loop", "the loop now stops after three tries") +
		row("55555555-0000-4000-8000-000000000005", strings.Repeat("a", 64*1024), "a paste exactly at the cap, kept whole")
	db := filepath.Join(tmp, "threads.db")
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sqlite fixture: %v: %s", err, out)
	}
	t.Setenv("DEJA_ZED_DB", db)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	_, errOut := captureBoth(t, "show", "3333")
	if !strings.Contains(errOut, "one message was stored short") {
		t.Errorf("the thread with the pasted log lost its note:\n%s", errOut)
	}
	_, errOut = captureBoth(t, "show", "4444")
	if strings.Contains(errOut, "stored short") {
		t.Errorf("a whole two-message thread was told it was stored short:\n%s", errOut)
	}
	// A message exactly at the cap was stored whole; its length alone cannot
	// tell it from one the cap cut.
	_, errOut = captureBoth(t, "show", "5555")
	if strings.Contains(errOut, "stored short") {
		t.Errorf("a thread whose message fits the cap exactly was told it was stored short:\n%s", errOut)
	}
}

// The last slice of a session has no next one. The note pointed at the offset
// after it anyway, and following that advice answered "--offset 16 is past the
// end — the session has 16 messages": deja arguing with itself (#3578).
func TestTheLastSliceDoesNotOfferANextOne(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		offset, returned, total int
		wantNext                bool
		says                    string
	}{
		{name: "a middle slice", offset: 6, returned: 3, total: 16, wantNext: true, says: "7-9 of 16"},
		{name: "the last slice", offset: 13, returned: 3, total: 16, says: "14-16 of 16"},
		{name: "a slice that ends exactly", offset: 14, returned: 2, total: 16, says: "15-16 of 16"},
		{name: "the whole session at an offset", offset: 1, returned: 15, total: 16, says: "2-16 of 16"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			note := showWindowNote(tc.offset, tc.returned, tc.total)
			if !strings.Contains(note, tc.says) {
				t.Fatalf("note = %q, want it to say %q", note, tc.says)
			}
			hasNext := strings.Contains(note, "reads the next slice")
			if hasNext != tc.wantNext {
				t.Fatalf("note = %q, offering a next slice = %v, want %v", note, hasNext, tc.wantNext)
			}
			if !tc.wantNext && !strings.Contains(note, "end of the session") {
				t.Fatalf("note = %q, want it to say where the reader is", note)
			}
		})
	}
}

// The cases either side of it keep their own sentences.
func TestTheOtherWindowNotesAreUnchanged(t *testing.T) {
	if note := showWindowNote(0, 16, 16); note != "" {
		t.Errorf("a reader who asked for everything got arithmetic: %q", note)
	}
	if note := showWindowNote(0, 0, 0); !strings.Contains(note, "no messages to show") {
		t.Errorf("an empty session = %q", note)
	}
	if note := showWindowNote(16, 0, 16); !strings.Contains(note, "past the end") {
		t.Errorf("past the end = %q", note)
	}
}

// A trust rule that withholds imported content must hold on the direct-access
// commands too, not only search and last. show, share and handoff each take an
// id and reveal a whole session, so before this they leaked a peer's content a
// rule had hidden — ctx already refused here (#1026).
func TestDirectAccessHonoursTrustPolicy(t *testing.T) {
	hermeticEnv(t)
	// hermeticEnv blanks XDG_CONFIG_HOME; point it at a real dir so the policy
	// file lands where policy.Load reads it.
	cfg := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", cfg)

	// A local session and an imported one carrying a distinctive marker.
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-loc")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	loc := `{"type":"user","message":{"role":"user","content":"LOCALMARKER work"},"timestamp":"2026-07-25T10:00:00Z","sessionId":"locz","cwd":"/loc"}`
	if err := os.WriteFile(filepath.Join(store, "l.jsonl"), []byte(loc+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	// Hand-write a foreign sync batch and import it.
	shared := filepath.Join(t.TempDir(), "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	peer := `{"harness":"claude","session_id":"peerz","project":"svc","role":"user","text":"IMPORTEDMARKER secret","time":"2026-07-22T10:00:00Z"}`
	if err := os.WriteFile(filepath.Join(shared, "deja-sync-p.jsonl"), []byte(peer+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", shared); err != nil {
		t.Fatal(err)
	}

	policyPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "deja", "policy.json")
	if err := os.MkdirAll(filepath.Dir(policyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writePolicy := func(imported bool) {
		body := `{"activations":{"search":{"imported":true}}}`
		if !imported {
			body = `{"activations":{"search":{"imported":false}}}`
		}
		if err := os.WriteFile(policyPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Find the imported id while the rule allows it.
	writePolicy(true)
	last, err := captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	impID := ""
	for f := range strings.FieldsSeq(last) {
		if strings.HasPrefix(f, "imported-") {
			impID = strings.Trim(f, "]·")
			break
		}
	}
	if impID == "" {
		t.Fatalf("no imported id in last output:\n%s", last)
	}

	// Deny: every direct-access command must withhold the imported content.
	writePolicy(false)
	for _, args := range [][]string{{"show", impID}, {"share", impID}, {"handoff", impID}} {
		out, _ := captureRun(t, args...)
		if strings.Contains(out, "IMPORTEDMARKER") {
			t.Errorf("%s leaked imported content under deny-imported:\n%s", args[0], out)
		}
	}
	// promote --to wrote the imported content to a file the reader named, the
	// one leak the others do not have; the whole command must refuse first.
	exp := filepath.Join(t.TempDir(), "promoted.md")
	if out, _ := captureRun(t, "promote", impID, "--to", exp); strings.Contains(out, "IMPORTEDMARKER") {
		t.Errorf("promote leaked imported content under deny-imported:\n%s", out)
	}
	if b, err := os.ReadFile(exp); err == nil && strings.Contains(string(b), "IMPORTEDMARKER") {
		t.Errorf("promote --to wrote imported content to the export file:\n%s", string(b))
	}
	// The local session stays reachable — the rule is about origin, not access.
	if out, _ := captureRun(t, "show", "locz"); !strings.Contains(out, "LOCALMARKER") {
		t.Errorf("show over-blocked a local session under deny-imported:\n%s", out)
	}
	// Allow: the imported session comes back.
	writePolicy(true)
	if out, _ := captureRun(t, "show", impID); !strings.Contains(out, "IMPORTEDMARKER") {
		t.Errorf("show withheld imported content even when the rule allows it:\n%s", out)
	}
}

func TestSliceMessages(t *testing.T) {
	ms := make([]model.Message, 10)
	for i := range ms {
		ms[i] = model.Message{Text: string(rune('a' + i))}
	}
	cases := []struct {
		offset, limit int
		want          string
	}{
		{0, 3, "abc"},
		{7, 5, "hij"},
		{0, 50, "abcdefghij"},
		{10, 5, ""},
		// Past the end is empty, not a panic: --offset takes any number the
		// reader types.
		{99, 5, ""},
	}
	for _, c := range cases {
		var b strings.Builder
		for _, m := range sliceMessages(ms, c.offset, c.limit) {
			b.WriteString(m.Text)
		}
		if b.String() != c.want {
			t.Errorf("offset=%d limit=%d → %q, want %q", c.offset, c.limit, b.String(), c.want)
		}
	}
}

// Both flags are documented for `deja show`, and only the JSON path honoured
// them — the text output printed the whole session (#709).
// sliceMessages must survive an offset past the end — --offset takes any
// number the reader types.
func TestSliceMessagesPastTheEnd(t *testing.T) {
	ms := []model.Message{{Text: "a"}, {Text: "b"}}
	for _, off := range []int{2, 3, 1000} {
		if got := sliceMessages(ms, off, 5); len(got) != 0 {
			t.Errorf("offset %d returned %d messages", off, len(got))
		}
	}
}

func TestShowHonoursOffsetAndLimitInTextOutput(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	lines := make([]string, 0, 10)
	for i := range 10 {
		lines = append(lines, `{"type":"user","sessionId":"ten","cwd":"/w/p","timestamp":"2026-07-21T10:0`+
			string(rune('0'+i))+`:00Z","message":{"role":"user","content":"message number `+string(rune('0'+i))+`"}}`)
	}
	if err := os.WriteFile(filepath.Join(root, "ten.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	count := func(out string) int { return strings.Count(out, "message number") }

	whole, err := captureRun(t, "show", "ten")
	if err != nil {
		t.Fatal(err)
	}
	// No flags: the session is printed whole, or every reader who pipes
	// `deja show` into a pager silently loses the tail. A ten-message session
	// is also too small to be worth mentioning the slice flags for.
	if got := count(whole); got != 10 {
		t.Errorf("show without flags returned %d messages", got)
	}
	quiet, err := captureRunStderr(t, "show", "ten")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(quiet, "reads a slice") {
		t.Errorf("a ten-message session suggested slicing: %q", quiet)
	}
	limited, err := captureRun(t, "show", "ten", "--limit", "3")
	if err != nil {
		t.Fatal(err)
	}
	if got := count(limited); got != 3 {
		t.Errorf("--limit 3 returned %d messages", got)
	}
	offset, err := captureRun(t, "show", "ten", "--offset", "7")
	if err != nil {
		t.Fatal(err)
	}
	if got := count(offset); got != 3 || !strings.Contains(offset, "message number 7") || strings.Contains(offset, "message number 6") {
		t.Errorf("--offset 7 returned %d messages: %q", got, offset)
	}
	both, err := captureRun(t, "show", "ten", "--offset", "7", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}
	if got := count(both); got != 2 {
		t.Errorf("--offset 7 --limit 2 returned %d messages", got)
	}
}

// --offset and --limit were refused with one message, "needs a positive
// integer", which is wrong for --offset: 0 is the first message. Each now
// names its own floor and quotes what the reader typed, as recall does
// (#5650).
func TestShowRefusesASliceFlagNamingItsFloorAndTheValue(t *testing.T) {
	hermeticEnv(t)
	for _, tc := range []struct {
		flag, value, floor string
	}{
		{"--offset", "-1", "a number from 0 up"},
		{"--offset", "two", "a number from 0 up"},
		{"--limit", "0", "a positive number"},
		{"--limit", "-3", "a positive number"},
	} {
		t.Run(tc.flag+" "+tc.value, func(t *testing.T) {
			_, err := captureRun(t, "show", "01a00feb", tc.flag, tc.value)
			if err == nil {
				t.Fatalf("show %s %s was accepted", tc.flag, tc.value)
			}
			want := "show: " + tc.flag + " wants " + tc.floor + `, got "` + tc.value + `"`
			if err.Error() != want {
				t.Errorf("show %s %s:\n got %q\nwant %q", tc.flag, tc.value, err, want)
			}
		})
	}

	// The floors themselves are accepted.
	o, err := parseShow([]string{"01a00feb", "--offset", "0", "--limit", "1"})
	if err != nil {
		t.Fatalf("--offset 0 --limit 1: %v", err)
	}
	if o.offset != 0 || o.limit != 1 || !o.sliced {
		t.Errorf("parsed = %+v", o)
	}
}

// A whitespace-only session id is no id at all: `deja show "   "` sailed past
// the missing-id refusal because the check only looked for an empty string,
// and a leading empty argument was silently ignored instead of refused (#5936).
func TestShowWhitespaceOnlyIDIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"whitespace only", []string{"   "}},
		{"empty", []string{""}},
		{"empty before an id", []string{"", "sess1234"}},
		{"whitespace before an id", []string{"   ", "sess1234"}},
		{"whitespace after an id", []string{"sess1234", "   "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseShow(tc.args)
			if err == nil {
				t.Fatalf("parseShow(%v) returned no error", tc.args)
			}
			if !strings.Contains(err.Error(), "show needs an id prefix") {
				t.Errorf("parseShow(%v) = %v, want the missing-id refusal", tc.args, err)
			}
		})
	}

	// A real id still parses.
	o, err := parseShow([]string{"sess1234"})
	if err != nil {
		t.Fatal(err)
	}
	if o.id != "sess1234" {
		t.Errorf("parsed id = %q, want %q", o.id, "sess1234")
	}
}

// `show --json` reports the window it returned; the terminal printed the slice
// and said nothing, so a reader could not tell five turns of two hundred from a
// session that is five turns long (#2296).
func TestShowSaysWhichSliceItPrinted(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	lines := make([]string, 0, 20)
	for i := range 20 {
		at := time.Now().Add(-time.Duration(60-i) * time.Minute).UTC().Format(time.RFC3339)
		lines = append(lines, fmt.Sprintf(`{"type":"user","sessionId":"paged","timestamp":%q,"cwd":"/work/project",`+
			`"message":{"role":"user","content":"marker%02d"}}`, at, i))
	}
	if err := os.WriteFile(filepath.Join(claude, "long.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The premise: a slice really is a slice.
	out, err := captureRun(t, "show", "paged", "--harness", "claude", "--limit", "5")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "marker"); n != 5 {
		t.Fatalf("--limit 5 printed %d markers", n)
	}

	said, err := captureRunStderr(t, "show", "paged", "--harness", "claude", "--limit", "5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(said, "5") || !strings.Contains(said, "20") {
		t.Errorf("the slice line does not say 5 of 20: %q", said)
	}

	// Past the end is its own answer, not an empty session.
	said, err = captureRunStderr(t, "show", "paged", "--harness", "claude", "--offset", "100")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(said, "past the end") {
		t.Errorf("an offset past the end printed %q", said)
	}

	// An empty session is not an offset mistake.
	if got := showWindowNote(0, 0, 0); !strings.Contains(got, "no messages") {
		t.Errorf("an empty session reads as an offset error: %q", got)
	}
	// The arithmetic at the edges.
	if got := showWindowNote(15, 5, 20); !strings.Contains(got, "16-20 of 20") {
		t.Errorf("the last slice reads %q", got)
	}
	if got := showWindowNote(0, 20, 20); got != "" {
		t.Errorf("a full window still printed a note: %q", got)
	}

	// A whole session says nothing extra.
	said, err = captureRunStderr(t, "show", "paged", "--harness", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(said, "of 20") || strings.Contains(said, "past the end") {
		t.Errorf("a whole session gained a slice line: %q", said)
	}
}

// A rebuild of records.bin retired a sidecar whose vectors address records by
// offset alone (#1355), which every version 1 sidecar does. Search dropped the
// semantic tier without a word, while the unreachable endpoint beside it has
// always said so (#2208).
func TestAStaleSidecarIsNamedOnTheSearchPath(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the scheduler retries twice"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		out := make([][]float32, len(body.Input))
		for i := range out {
			out[i] = []float32{1, 0}
		}
		payload, _ := json.Marshal(map[string]any{"embeddings": out})
		_, _ = w.Write(payload)
	}))
	defer ts.Close()
	if _, err := embed.EmbedIndex(dir, &embed.Client{URL: ts.URL, Model: "test"}, nil); err != nil {
		t.Fatal(err)
	}
	asVersion1Sidecar(t, dir)

	said := func(run func(notice *os.File)) string {
		t.Helper()
		f, err := os.CreateTemp(t.TempDir(), "notice")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		run(f)
		b, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	reset := func() { saidSidecarStale = false }
	t.Cleanup(reset)

	// A sidecar search will use is not announced. It may still say the endpoint
	// is unreachable, which is a different sentence about a different thing.
	reset()
	if quiet := said(func(notice *os.File) {
		maybeRerank(dir, []search.Hit{{}}, search.Options{Query: "scheduler"}, notice)
	}); strings.Contains(quiet, "sidecar") {
		t.Fatalf("a usable sidecar was announced: %q", quiet)
	}

	// The transcript rewritten under the index and the log written again, and
	// the offsets the vectors point at are gone. An ordinary pass leaves them
	// valid: a new file is appended since #3500, a rewritten one since #5387.
	rewound := `{"type":"user","message":{"role":"user","content":"the cache eviction wiped sessions"},"timestamp":"2026-08-03T10:00:00Z","sessionId":"aaa","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(rewound), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	s, err := embed.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !embed.Stale(dir, s) {
		t.Fatal("the rebuild left the sidecar usable, so this measures nothing")
	}

	reset()
	rerank := said(func(notice *os.File) {
		maybeRerank(dir, []search.Hit{{}}, search.Options{Query: "scheduler"}, notice)
	})
	if !strings.Contains(rerank, "sidecar") || !strings.Contains(rerank, "deja embed") {
		t.Errorf("a rerank that gave up on a stale sidecar says %q", rerank)
	}
	reset()
	semantic := said(func(notice *os.File) {
		maybeSemantic(dir, nil, search.Options{Query: "scheduler"}, notice)
	})
	if !strings.Contains(semantic, "sidecar") || !strings.Contains(semantic, "deja embed") {
		t.Errorf("a semantic search that gave up on a stale sidecar says %q", semantic)
	}
	// One search runs both, and one retired file is one fact.
	reset()
	both := said(func(notice *os.File) {
		maybeRerank(dir, nil, search.Options{Query: "scheduler"}, notice)
		maybeSemantic(dir, nil, search.Options{Query: "scheduler"}, notice)
	})
	if n := strings.Count(both, "deja embed"); n != 1 {
		t.Errorf("one search said it %d times:\n%s", n, both)
	}
}

// asVersion1Sidecar rewrites the sidecar in dir the way deja wrote it before
// vectors carried their record's identity, so a test can reach the stale path
// a version 2 sidecar no longer takes.
func asVersion1Sidecar(t *testing.T, dir string) {
	t.Helper()
	s, err := embed.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	str := func(v string) {
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(v))) //nolint:gosec // fixture strings are a few bytes
		b.WriteString(v)
	}
	b.WriteString("DJV1")
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(s.Dim)) //nolint:gosec // fixture dimension is small
	str(s.Model)
	str(s.Generation)
	_ = binary.Write(&b, binary.LittleEndian, uint64(len(s.Vectors)))
	_ = binary.Write(&b, binary.LittleEndian, uint64(s.Covered)) //nolint:gosec // a fixture count, never negative
	for _, v := range s.Vectors {
		_ = binary.Write(&b, binary.LittleEndian, v.Offset)
		str(v.Key)
		_ = binary.Write(&b, binary.LittleEndian, v.Values)
	}
	if err := os.WriteFile(embed.Path(dir), b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if back, err := embed.Read(dir); err != nil || len(back.Vectors) != len(s.Vectors) || (len(back.Vectors) > 0 && back.Vectors[0].Ident != 0) {
		t.Fatalf("the version 1 sidecar does not read back: %v", err)
	}
}

// A sidecar that will not parse turns semantic search off. Falling back to the
// lexical order is right; doing it in silence is not — the same path says so
// when the endpoint is the missing piece, and doctor is a screen someone opens
// after noticing (#2201).
func TestAnUnreadableSidecarIsNamedOnTheSearchPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Cut off after the header, which is what a partial copy leaves.
	if err := os.WriteFile(embed.Path(dir), []byte("DJVE\x01\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := embed.Read(dir); err == nil {
		t.Fatal("the fixture parses, so this measures nothing")
	}

	said := func(run func(notice *os.File)) string {
		t.Helper()
		f, err := os.CreateTemp(t.TempDir(), "notice")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		run(f)
		b, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// The note is once per process, so each case starts from an unspoken one.
	reset := func() { saidSidecarUnreadable = false }
	t.Cleanup(reset)

	hits := []search.Hit{{Session: model.Session{Harness: "claude", ID: "a1", Project: "app"}}}
	reset()
	rerank := said(func(notice *os.File) {
		maybeRerank(dir, hits, search.Options{Query: "pool"}, notice)
	})
	if !strings.Contains(rerank, "sidecar") || !strings.Contains(rerank, "deja embed") {
		t.Errorf("a rerank that gave up on an unreadable sidecar says %q", rerank)
	}
	reset()
	semantic := said(func(notice *os.File) {
		maybeSemantic(dir, nil, search.Options{Query: "pool"}, notice)
	})
	if !strings.Contains(semantic, "sidecar") || !strings.Contains(semantic, "deja embed") {
		t.Errorf("a semantic search that gave up on an unreadable sidecar says %q", semantic)
	}

	// One search runs the rerank and then the semantic tier when it found
	// nothing. One broken file is one fact.
	reset()
	both := said(func(notice *os.File) {
		maybeRerank(dir, nil, search.Options{Query: "pool"}, notice)
		maybeSemantic(dir, nil, search.Options{Query: "pool"}, notice)
	})
	if n := strings.Count(both, "will not parse"); n != 1 {
		t.Errorf("one search said it %d times:\n%s", n, both)
	}

	// A directory with no sidecar at all is the ordinary case and stays quiet.
	empty := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	reset()
	quiet := said(func(notice *os.File) {
		maybeRerank(empty, hits, search.Options{Query: "pool"}, notice)
		maybeSemantic(empty, nil, search.Options{Query: "pool"}, notice)
	})
	if quiet != "" {
		t.Errorf("nothing is embedded here and deja said %q", quiet)
	}
}

// Every command in this family refuses a flag it does not take. friction and
// restore dropped one on the floor and answered as though nothing was wrong, so
// a script asking either for --json got prose on stdout and exit 0 (#2253).
//
// friction ANSWERS --json now (#1932), so it has moved to the accepted list
// below. The property this test exists for is unchanged: a flag the command
// cannot honour must be refused, never ignored. --limt keeps that covered, and
// restore still refuses --json because it does not implement one.
func TestFrictionAndRestoreRefuseAFlagTheyDoNotTake(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir := filepath.Join(tmp, "index.db")

	var out bytes.Buffer
	for _, args := range [][]string{
		{"--limt", "3"},
		{"--limit"},
		{"--jsonn"},
	} {
		out.Reset()
		err := runFriction(dir, args, &out)
		if err == nil {
			t.Errorf("friction %v was accepted, and printed %q", args, out.String())
			continue
		}
		if !strings.Contains(err.Error(), args[0]) {
			t.Errorf("friction %v: %v — the message should name the flag", args, err)
		}
	}
	// What it does take still works.
	out.Reset()
	if err := runFriction(dir, []string{"--limit", "3"}, &out); err != nil {
		t.Errorf("friction --limit 3: %v", err)
	}
	out.Reset()
	if err := runFriction(dir, []string{"--json"}, &out); err != nil {
		t.Errorf("friction --json: %v", err)
	}

	for _, args := range [][]string{
		{"api/upload.go", "--json"},
		{"api/upload.go", "-o"},
		{"api/upload.go", "--span"},
	} {
		out.Reset()
		err := runRestore(dir, args, &out)
		if err == nil {
			t.Errorf("restore %v was accepted, and printed %q", args, out.String())
			continue
		}
		if !strings.Contains(err.Error(), args[1]) {
			t.Errorf("restore %v: %v — the message should name the flag", args, err)
		}
	}
}

// A window of zero or less parsed, and every caller then skipped the cut it
// asked for: `--since -1d` searched the whole store and said nothing about it
// (#1610). Refusing it is the only answer that cannot be mistaken for a result.
func TestParseDurRefusesAWindowThatIsNotPositive(t *testing.T) {
	for _, in := range []string{"0d", "0", "-0d", "-1d", "-12h", "-90m"} {
		d, err := parseDur(in)
		if err == nil {
			t.Errorf("parseDur(%q) = %v with no error; a window has to be positive", in, d)
			continue
		}
		if !strings.Contains(err.Error(), in) {
			t.Errorf("parseDur(%q) does not name what was typed: %v", in, err)
		}
	}
	// The control: the windows deja documents still parse.
	for _, in := range []string{"30d", "12h", "90m", "45s"} {
		if _, err := parseDur(in); err != nil {
			t.Errorf("parseDur(%q) = %v, want a window", in, err)
		}
	}
}

// 365000d is a thousand years and the way people say "all of it". Multiplied
// out it overflows time.Duration and came back negative, so the window that was
// meant to hold everything held nothing and the filter was skipped (#1610).
func TestHugeDayWindowSaturatesInsteadOfWrapping(t *testing.T) {
	d, err := parseDur("365000d")
	if err != nil {
		t.Fatalf("parseDur(365000d) = %v", err)
	}
	if d <= 0 {
		t.Errorf("parseDur(365000d) = %v, want the largest window it can hold", d)
	}
}

// #2608 fixed the English README's banner. The same claim lives in the places
// someone reads before they install anything — the site's hero stat, the
// Chinese README, a guide page — and the site's own token-cost page states the
// honest pair beside it ("about 0.4 ms in process, around 25 ms on the
// LongMemEval-S haystacks"). Measured in process on a 137 MB index an exact
// answer is 3.4–6.2 ms (#2610).
func TestNothingOutsideTheReadmePromisesSubMillisecond(t *testing.T) {
	root := "../.."
	var offenders []string
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry is skipped, not a failure
		}
		if fi.IsDir() {
			switch fi.Name() {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(p) {
		case ".md", ".html", ".json", ".toml", ".yml", ".yaml":
		default:
			return nil
		}
		// The changelog is a record of what was said at the time.
		if strings.Contains(p, "CHANGELOG") {
			return nil
		}
		b, rerr := os.ReadFile(p) //nolint:gosec // reads the repo's own checked-in tree
		if rerr != nil {
			return nil //nolint:nilerr // an unreadable file is skipped, not a failure
		}
		text := string(b)
		for _, claim := range []string{"sub-millisecond", "亚毫秒", "under a millisecond", "in under a millisecond"} {
			if strings.Contains(text, claim) {
				offenders = append(offenders, strings.TrimPrefix(p, root+"/")+": "+claim)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("a lookup is milliseconds, not sub-millisecond, on a real store:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// The home page's `sources` command prints the harness names one by one and
// then a count. Crush landed as the 25th, every count on the site moved with
// it, and this list did not: it said 25 while naming 24. A list of names goes
// stale the same way a number does, and it is the one place a reader checks
// for their own agent.
func TestTheHomePageNamesEveryHarnessItCounts(t *testing.T) {
	root := filepath.Join("..", "..")
	page, err := os.ReadFile(filepath.Join(root, "docs", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	line := regexp.MustCompile(`'sources':'<p class="none">(.*?)\\n(\d+) harnesses`).FindSubmatch(page)
	if line == nil {
		t.Fatal("the home page has no `sources` line to check")
	}
	named := map[string]bool{}
	for n := range strings.FieldsSeq(strings.ReplaceAll(string(line[1]), "&nbsp;", " ")) {
		named[n] = true
	}

	b, err := os.ReadFile(filepath.Join(root, "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			ID string `json:"id"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	// The registry ids are the doc page names; the line prints what `deja
	// sources` calls each harness, and the two differ for a handful.
	alias := map[string]string{
		"claude-code": "claude",
		"continue":    "continue",
		"copilot-cli": "copilot",
	}
	missing := []string{}
	count := 0
	for _, h := range reg.Harnesses {
		if h.ID == "deja" {
			continue
		}
		count++
		name := h.ID
		if a, ok := alias[h.ID]; ok {
			name = a
		}
		if !named[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("the home page counts %d harnesses and names neither of %v", count, missing)
	}
	if len(named) != count {
		t.Errorf("the home page names %d harnesses and says %d", len(named), count)
	}
}

// datedStore writes sessions with identical text at the given ages, so nothing
// but the timestamp can decide how they rank.
func datedStore(t *testing.T, stamps map[string]string) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-tmp-projm")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for sid, stamp := range stamps {
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/tmp/projm","timestamp":"` + stamp +
			`","message":{"role":"user","content":"we reworked the invoice exporter and its retry loop"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
}

func firstHitID(t *testing.T, out string, ids ...string) string {
	t.Helper()
	best, at := "", len(out)+1
	for _, id := range ids {
		if i := strings.Index(out, id); i >= 0 && i < at {
			best, at = id, i
		}
	}
	return best
}

// The site called month, year and relative phrases "searchable tokens" and
// "ranking hints". They are read by the relevance tier alone, so they do
// nothing for a query that matched something exactly — which is the ordinary
// case (#1094). This pins both halves: the hints work where they work, and
// stay out of the way where they do not.
func TestTimeHintsRankOnlyTheNoExactMatchFallback(t *testing.T) {
	now := time.Now()
	// Step back by calendar months from mid-month so the 31st of a 31-day month
	// does not overflow a 30-day previous month back into the current one
	// (e.g. Oct 31 AddDate(0,-1,0) is Oct 1).
	midMonth := time.Date(now.Year(), now.Month(), 15, 10, 0, 0, 0, now.Location())
	lastMonth := midMonth.AddDate(0, -1, 0)
	datedStore(t, map[string]string{
		// Dated in the same calendar deja reads "last month" in — the local
		// one. Formatted through UTC and taken two hours back, the newest
		// session landed in the previous month whenever the run happened early
		// on the first of a month, so "last month" matched it too and the hint
		// had nothing to tell apart. It failed that way on 2026-09-01.
		"m_old":  midMonth.AddDate(0, -3, 0).Format("2006-01-02") + "T10:00:00Z",
		"m_last": lastMonth.Format("2006-01-02") + "T10:00:00Z",
		"m_now":  now.Format("2006-01-02") + "T10:00:00Z",
	})

	// Baseline: nothing but recency.
	base, err := captureRun(t, "search", "invoice exporter")
	if err != nil {
		t.Fatal(err)
	}
	if got := firstHitID(t, base, "m_now", "m_last", "m_old"); got != "m_now" {
		t.Fatalf("baseline is not recency-ordered (%s):\n%s", got, base)
	}

	// A query with no exact match: the hint decides.
	out, err := captureRun(t, "search", "what did we do last month")
	if err != nil {
		t.Fatal(err)
	}
	if got := firstHitID(t, out, "m_now", "m_last", "m_old"); got != "m_last" {
		t.Errorf("`last month` did not rank last month's session first (got %s):\n%s", got, out)
	}

	// The same phrase on a query that already matches: the words win, and the
	// site must not promise otherwise.
	out, err = captureRun(t, "search", "invoice exporter last month")
	if err != nil {
		t.Fatal(err)
	}
	if got := firstHitID(t, out, "m_now", "m_last", "m_old"); got != "m_now" {
		t.Errorf("a time phrase re-ranked an exact-matching query (got %s) — if this is now"+
			" intended, the site copy has to say so:\n%s", got, out)
	}
}

// A bare year never reaches the tier that reads date tokens, so it answers
// "no matches" on a store where every session carries that year.
func TestBareYearFindsNothingWhileTheYearStillRanks(t *testing.T) {
	year := time.Now().AddDate(-2, 0, 0).Format("2006")
	datedStore(t, map[string]string{
		"y_old": year + "-05-14T10:00:00Z",
		"y_new": time.Now().Add(-2*time.Hour).UTC().Format("2006-01-02") + "T10:00:00Z",
	})

	// With content words the year selects; this is what makes it a real token.
	out, err := captureRun(t, "search", "invoice exporter "+year)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "y_old") {
		t.Errorf("the year did not reach its session alongside content words:\n%s", out)
	}

	// Alone it does not, and the site no longer claims a bare year is a query.
	out, err = captureRunStderr(t, "search", year)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no matches") {
		t.Logf("a bare year now answers — the site copy can be widened again:\n%s", out)
	}
}

// The copy has to keep naming the condition, or the measurement above stops
// matching what a reader is told.
func TestSiteSearchPageScopesTheTimeHints(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", "search.html"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	if strings.Contains(page, "as searchable tokens") {
		t.Errorf("site still calls month/year general searchable tokens; they are read by the" +
			" relevance tier only (#1094)")
	}
	if !strings.Contains(page, "no exact match") {
		t.Errorf("site no longer says when the time hints apply")
	}
}

// The CLI skill gets the same treatment as the MCP one on the way in — replaced
// with a backup when deja has no record of writing it — and none of it on the
// way out. #2581 taught the deja-history path to put the reader's copy back and
// #2585 taught it to drop a copy that is deja's own; deja-search was left with
// neither, so an older deja's own skill survives an uninstall (#2596).
func TestUninstallHandlesTheCLISkillsBackupToo(t *testing.T) {
	t.Run("an older deja's own copy goes", func(t *testing.T) {
		home := cliSkillFixture(t, cliSkillFileWithBody("An older deja wrote this body."))
		path := filepath.Join(home, ".agents", "skills", "deja-search", "SKILL.md")
		if _, err := os.Stat(path + ".bak"); err != nil {
			t.Fatalf("install kept no backup, so this measures nothing: %v", err)
		}
		if _, err := captureRun(t, "uninstall", "codex"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("deja's own CLI skill survived: %v", err)
		}
		if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
			t.Errorf("a backup of deja's own CLI skill survived the uninstall")
		}
	})

	t.Run("the reader's own copy comes back", func(t *testing.T) {
		mine := "---\nname: deja-search\ndescription: my own version\n---\n\nThis file is mine.\n"
		home := cliSkillFixture(t, mine)
		path := filepath.Join(home, ".agents", "skills", "deja-search", "SKILL.md")
		if _, err := captureRun(t, "uninstall", "codex"); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil || string(b) != mine {
			t.Errorf("the reader's own CLI skill did not come back: %v\n%s", err, b)
		}
	})
}

// cliSkillFixture puts a deja-search skill in place, installs over it, and
// returns the home it did that in.
func cliSkillFixture(t *testing.T, content string) string {
	t.Helper()
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".agents", "skills", "deja-search")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "codex", "--no-index"); err != nil {
		t.Fatal(err)
	}
	return home
}

// cliSkillFileWithBody is deja's own CLI skill with a different body, which is
// what an older build left behind.
func cliSkillFileWithBody(body string) string {
	return "---\nname: " + cliSkillName + "\ndescription: " + cliSkillDesc + "\n---\n\n" + body + "\n"
}

// The CLI-only install is binary + warmup + search, and nothing else runs. If
// warmup does not leave the skill, the agent on that machine has a working
// index and no instruction to reach for it — which is the whole of #1320.
func TestWarmupWritesTheCLISkill(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "warmup"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(cliSkillPath())
	if err != nil {
		t.Fatalf("warmup left no CLI skill: %v", err)
	}
	for _, want := range []string{"name: deja-search", "deja recall \"<query>\"", "deja recall --offset", "deja --framed blame"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the skill does not teach %q", want)
		}
	}
	// search --json carries every matched message whole: 222,546 bytes for one
	// query over five 240 KB sessions, where recall answers in 4,097 (#5578).
	if strings.Contains(string(body), "search --json") {
		t.Error("the CLI skill still sends an agent to search --json")
	}
	// The point of the second skill: it must not send an agent at MCP tools
	// that a CLI-only machine never installed.
	for line := range strings.SplitSeq(string(body), "\n") {
		if strings.HasPrefix(line, "- recall") || strings.HasPrefix(line, "- how:") {
			t.Errorf("the CLI skill names an MCP tool as a thing to call: %q", line)
		}
	}
}

// Both skills describe the same index, so each has to say when it is the wrong
// door — otherwise an agent holding one of them reaches for an API this machine
// does not have.
func TestTheTwoSkillsPointAtEachOther(t *testing.T) {
	if !strings.Contains(cliSkillBody, "MCP tool") {
		t.Error("the CLI skill never mentions the MCP tool it defers to")
	}
	if !strings.Contains(skillBody, "deja recall \"<query>\"") {
		t.Error("the MCP skill never names the shell path for a session without the tools")
	}
	// The shell path is the recall page, not search --json, which ran to
	// hundreds of KB for one query (#5578).
	if strings.Contains(skillBody, "search --json") {
		t.Error("the MCP skill's shell fallback is search --json")
	}
}

// An edited skill is the user's. Every other skill deja writes keeps their
// wording rather than replacing it, and this one is written by warmup, which
// runs far more often than install.
func TestWarmupKeepsAnEditedCLISkill(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "warmup"); err != nil {
		t.Fatal(err)
	}
	mine := "---\nname: deja-search\ndescription: mine\n---\n\nmy own wording\n"
	if err := os.WriteFile(cliSkillPath(), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "warmup")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(cliSkillPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != mine {
		t.Errorf("warmup overwrote a skill the user had edited:\n%s", got)
	}
	// And says nothing about it. warmup's stdout is empty by contract; the
	// "kept your edited skill" line belongs to an install someone is watching,
	// not to every warmup for as long as the edit lives.
	if out != "" {
		t.Errorf("warmup printed on an edited skill: %q", out)
	}
}

// Uninstalling one harness out of several must not take the skill from the
// rest: it describes the CLI, which every one of them can still shell out to.
func TestUninstallKeepsTheCLISkillWhileAnotherTargetStays(t *testing.T) {
	hermeticEnv(t)
	path := wiringStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"targets":["codex","claude-code"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !cliSkillStillWanted([]string{"codex"}) {
		t.Error("removing one of two wired harnesses took the skill from the other")
	}
	if cliSkillStillWanted([]string{"codex", "claude-code"}) {
		t.Error("the last harness left and the skill stayed")
	}
	// A machine with no record at all keeps it: the CLI-only install never
	// wired anything, and that is exactly who this skill is for.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if cliSkillStillWanted(nil) {
		t.Error("an unwired machine counted as wanting the skill for a harness")
	}
}

func TestRemoveCLISkillLeavesOneTheUserRewrote(t *testing.T) {
	hermeticEnv(t)
	if _, err := writeCLISkill(); err != nil {
		t.Fatal(err)
	}
	mine := "my own wording\n"
	if err := os.WriteFile(cliSkillPath(), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeCLISkill(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(cliSkillPath()); err != nil {
		t.Errorf("uninstall deleted a skill the user had rewritten: %v", err)
	}
	// And it does take back the copy it wrote itself. (Writing over the edited
	// one is refused by design, so the edit goes first.)
	if err := os.Remove(cliSkillPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := writeCLISkill(); err != nil {
		t.Fatal(err)
	}
	if err := removeCLISkill(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cliSkillPath()); !os.IsNotExist(err) {
		t.Errorf("uninstall left deja's own copy behind: %v", err)
	}
}

// The copy in the repo is what a reader sees on GitHub and what someone on a
// machine deja cannot write to copies by hand. It has to be the file the binary
// would have written, plus the registry block only ClawHub reads (#4378).
func TestBundledCLISkillMatchesTheInstaller(t *testing.T) {
	got := string(repoFile(t, filepath.Join("skills", "deja-search", "SKILL.md")))
	if got != cliSkillRegistryFile() {
		t.Errorf("skills/deja-search/SKILL.md has drifted from cliSkillRegistryFile:\n--- file ---\n%s\n--- installer ---\n%s", got, cliSkillRegistryFile())
	}
	if strings.Replace(got, "\n"+cliSkillMeta, "", 1) != cliSkillFile() {
		t.Error("the registry copy differs from the installed skill by more than its metadata")
	}
}

// The Agent Skills spec types `metadata` as string keys to string values, and a
// reader that decodes it that way drops the whole skill when a value is a
// nested map: Crush 0.97.1 rejected deja-search on every start with "cannot
// unmarshal !!map into string" (#4378). Every skill deja writes into a shared
// skills directory has to be one such a reader accepts.
func TestInstalledSkillsKeepMetadataFlat(t *testing.T) {
	for name, file := range map[string]string{
		"deja-search":  cliSkillFile(),
		"deja-history": skillFile(""),
	} {
		parts := strings.SplitN(file, "---\n", 3)
		if len(parts) != 3 || parts[0] != "" {
			t.Fatalf("%s: no frontmatter:\n%s", name, file)
		}
		inMeta := false
		for line := range strings.SplitSeq(strings.TrimSuffix(parts[1], "\n"), "\n") {
			switch {
			case line == "metadata:":
				inMeta = true
			case !strings.HasPrefix(line, " "):
				inMeta = false
				if strings.HasPrefix(line, "metadata:") {
					t.Errorf("%s: metadata is not a block of string values: %q", name, line)
				}
			case inMeta:
				k, v, ok := strings.Cut(strings.TrimPrefix(line, "  "), ": ")
				if !ok || strings.HasPrefix(line, "   ") || strings.Contains(k, " ") || v == "" || strings.ContainsAny(v[:1], "{[|>") {
					t.Errorf("%s: metadata value is not a string: %q", name, line)
				}
			}
		}
	}
}

func TestRootSkillManifest(t *testing.T) {
	body := string(repoFile(t, "SKILL.md"))
	parts := strings.SplitN(body, "---\n", 3)
	if len(parts) != 3 || parts[0] != "" {
		t.Fatal("SKILL.md is missing YAML frontmatter")
	}
	if name := frontmatterField(body, "name"); name != "deja-vu" {
		t.Fatalf("SKILL.md name = %q, want deja-vu", name)
	}
	if frontmatterField(body, "description") == "" {
		t.Fatal("SKILL.md description is empty")
	}
	if strings.Contains(parts[1], "metadata:") {
		t.Fatal("SKILL.md frontmatter must stay flat without a metadata block")
	}
	if !strings.Contains(body, `deja recall "<query>"`) {
		t.Fatal("SKILL.md must show deja recall \"<query>\"")
	}
	if strings.Contains(body, "search --json") {
		t.Fatal("SKILL.md must not reference search --json")
	}
}

// A skill's name and its directory have to agree: Claude Code requires it, and
// Gemini renames the loser when two collide — which is how #3665 was found.
// Three harnesses were getting `name: deja-history` inside a directory called
// `deja-search`, so the manual was a file their loader could drop without a
// word (#3700).
//
// Read every file deja writes the way a harness reads it, rather than checking
// the three paths by hand.
// The install sweep is shared with the round-trip test rather than run twice:
// installing every target is the most expensive thing in this suite and the
// Windows leg pays for it (see skillNamesMatchTheirDirectories, called there).
func TestEverySkillDeclaresTheNameOfItsDirectory(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	// The three that had the bug, plus Claude's own and the shared one.
	for _, target := range []string{"kilocode", "gjc", "commandcode", "clawcode", "claude-code", "codex"} {
		if _, err := captureRun(t, "install", target, "--no-index"); err != nil {
			continue
		}
	}
	skillNamesMatchTheirDirectories(t, home)
}

// skillNamesMatchTheirDirectories walks a home and checks every SKILL.md in it.
func skillNamesMatchTheirDirectories(t *testing.T, home string) {
	t.Helper()
	found := 0
	_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.Mode().IsRegular() || filepath.Base(p) != "SKILL.md" {
			return nil //nolint:nilerr // an unreadable entry is skipped, not a failure
		}
		b, rerr := os.ReadFile(p) //nolint:gosec // reads a temp home the test built
		if rerr != nil {
			return nil //nolint:nilerr // an unreadable file is skipped, not a failure
		}
		found++
		name := frontmatterField(string(b), "name")
		if name == "" {
			t.Errorf("%s declares no name", strings.TrimPrefix(p, home))
			return nil
		}
		if dir := filepath.Base(filepath.Dir(p)); name != dir {
			t.Errorf("%s declares name %q in a directory called %q",
				strings.TrimPrefix(p, home), name, dir)
		}
		return nil
	})
	if found == 0 {
		t.Fatal("no skill files were written, so there is nothing to check")
	}
	t.Logf("checked %d skill files", found)
}

// frontmatterField reads one top-level key out of a markdown file's
// frontmatter. Top-level only: the metadata block below carries nested keys of
// its own and none of them is the name.
func frontmatterField(text, key string) string {
	if !strings.HasPrefix(text, "---") {
		return ""
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		return ""
	}
	for line := range strings.SplitSeq(text[3:3+end], "\n") {
		if after0, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.TrimSpace(after0)
		}
	}
	return ""
}

// The copy written under the old name comes out on install, and the CLI skill
// that legitimately lives in a `deja-search` directory is left alone.
func TestInstallDropsTheManualItWroteUnderTheCLISkillsName(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	retired := filepath.Join(home, ".kilocode", "skills", "deja-search", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(retired), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(retired, []byte(skillFile(skillBody)), 0o644); err != nil {
		t.Fatal(err)
	}
	// And the CLI skill of deja's own, in its own directory, which stays.
	cli := filepath.Join(home, ".agents", "skills", cliSkillName, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(cli), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "kilocode", "--no-index"); err != nil {
		t.Fatalf("install kilocode: %v", err)
	}
	if _, err := os.Stat(retired); err == nil {
		t.Errorf("the manual under the CLI skill's name survived the install")
	}
	if _, err := os.Stat(filepath.Join(home, ".kilocode", "skills", "deja-history", "SKILL.md")); err != nil {
		t.Errorf("the manual was not written under its own name: %v", err)
	}
	if _, err := os.Stat(cli); err != nil {
		t.Errorf("the CLI skill was taken with it: %v", err)
	}
}

func TestHandoffSkillFilesAndFrontmatter(t *testing.T) {
	paths := []string{
		filepath.Join("..", "..", "skills", "deja-handoff", "SKILL.md"),
		filepath.Join("..", "..", "claude-plugin", "skills", "deja-handoff", "SKILL.md"),
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		text := string(raw)
		for _, want := range []string{
			"name: deja-handoff",
			`mode: "handoff"`,
			"deja handoff",
			"--to <agent>",
			"--project <name>",
			"--harness <name>",
			"--exec",
			"deja last",
			"deja status",
			"deja install --auto",
			"npx -y @vshulcz/deja-vu handoff",
			"User said (this project)",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing %q", p, want)
			}
		}
		if strings.HasSuffix(filepath.ToSlash(p), "skills/deja-handoff/SKILL.md") &&
			!strings.Contains(filepath.ToSlash(p), "claude-plugin/") {
			if !strings.Contains(text, "openclaw:") {
				t.Errorf("%s should include openclaw registry metadata", p)
			}
		}
		if strings.Contains(filepath.ToSlash(p), "claude-plugin/") && strings.Contains(text, "\nmetadata:") {
			t.Errorf("%s must keep flat frontmatter without metadata", p)
		}
	}
}

func TestSearchSkillStandaloneScopingAndTrail(t *testing.T) {
	bundled, err := os.ReadFile(filepath.Join("..", "..", "skills", "deja-search", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	generated := cliSkillRegistryFile()
	if string(bundled) != generated {
		t.Fatalf("skills/deja-search/SKILL.md drifted from cliSkillRegistryFile()")
	}
	for _, want := range []string{
		"When running without hooks or MCP",
		"deja recall",
		"deja --framed fix",
		"deja --framed blame",
		"deja --framed trail",
		"deja status",
		"deja install --auto",
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("deja-search skill missing %q", want)
		}
	}
}

func TestHandoffProjectAndHarnessFlags(t *testing.T) {
	withStatsStores(t)

	out, err := captureRun(t, "handoff", "--project", "alpha", "--harness", "claude")
	if err != nil {
		t.Fatalf("handoff --project alpha --harness claude: %v", err)
	}
	if !strings.Contains(out, "march alpha") {
		t.Fatalf("expected newest alpha claude session c2, got:\n%s", out)
	}

	out, err = captureRun(t, "handoff", "--project", "gamma", "--harness", "codex")
	if err != nil {
		t.Fatalf("handoff --project gamma --harness codex: %v", err)
	}
	if !strings.Contains(out, "gamma") {
		t.Fatalf("expected codex session codex1, got:\n%s", out)
	}
	if _, cerr := captureRun(t, "handoff", "--harness", "codex"); cerr == nil || !strings.Contains(cerr.Error(), "no codex session indexed for this project") {
		t.Fatalf("expected project-scoped missing codex error, got %v", cerr)
	}

	out, err = captureRun(t, "handoff", "--project", "beta", "--to", "codex")
	if err != nil {
		t.Fatalf("handoff --project beta --to codex: %v", err)
	}
	if !strings.Contains(out, "long beta session") {
		t.Fatalf("expected beta session c3, got:\n%s", out)
	}

	for _, bad := range [][]string{
		{"--project"},
		{"--harness"},
		{"--harness", "not-a-real-harness"},
	} {
		if err := runHandoff(index.DefaultDir(), bad, discardWriter{}); err == nil {
			t.Errorf("runHandoff(%v) unexpectedly succeeded", bad)
		}
	}
}

func TestStatusCommandReadOnlyAndWiring(t *testing.T) {
	hermeticEnv(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	for _, k := range []string{
		"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT",
		"CODEX_THREAD_ID", "CODEX_CI", "CODEX_SANDBOX", "CODEX_MANAGED_BY_NPM",
		"GEMINI_CLI", "OPENCODE", "CURSOR_AGENT",
		"ANTIGRAVITY_AGENT", "KIMI_CLI", "QWEN_CODE",
	} {
		t.Setenv(k, "")
	}

	missingIdx := filepath.Join(t.TempDir(), "never-created-index")
	var buf bytes.Buffer
	err = runStatus(missingIdx, []string{"--json"}, &buf)
	if err != nil {
		t.Fatalf("runStatus unwired: %v", err)
	}
	if _, statErr := os.Stat(missingIdx); !os.IsNotExist(statErr) {
		t.Fatalf("runStatus must not create missing index dir %s (stat err=%v)", missingIdx, statErr)
	}

	var rep statusReport
	if jsonErr := json.Unmarshal(buf.Bytes(), &rep); jsonErr != nil {
		t.Fatalf("unmarshal status JSON: %v\n%s", jsonErr, buf.String())
	}
	if rep.HooksWired || rep.MCPWired || rep.IndexExists {
		t.Fatalf("expected unwired empty status, got %#v", rep)
	}
	if rep.InstallCommand != "deja install --auto" {
		t.Fatalf("InstallCommand = %q, want %q", rep.InstallCommand, "deja install --auto")
	}

	t.Setenv("CLAUDECODE", "1")
	buf.Reset()
	if err = runStatus(missingIdx, []string{"--json"}, &buf); err != nil {
		t.Fatalf("runStatus active harness: %v", err)
	}
	var repActive statusReport
	if err = json.Unmarshal(buf.Bytes(), &repActive); err != nil {
		t.Fatal(err)
	}
	if repActive.ActiveHarness != "claude" {
		t.Fatalf("ActiveHarness = %q, want claude", repActive.ActiveHarness)
	}

	claudeSettings := filepath.Join(home, ".claude", "settings.json")
	if err = os.MkdirAll(filepath.Dir(claudeSettings), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"deja hook-context"}]}]}}`
	if err = os.WriteFile(claudeSettings, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	if err = runStatus(missingIdx, []string{"--harness", "claude", "--json"}, &buf); err != nil {
		t.Fatalf("runStatus wired: %v", err)
	}
	var repWired statusReport
	if err = json.Unmarshal(buf.Bytes(), &repWired); err != nil {
		t.Fatal(err)
	}
	if !repWired.HooksWired || repWired.InstallCommand != "" {
		t.Fatalf("expected wired claude hooks and empty InstallCommand, got %#v", repWired)
	}
}

func TestTrailChronologicalOrderAndJSON(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "repo")
	target := filepath.Join(repo, "pkg", "auth", "token.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package auth\nfunc Parse() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-repo", "t-mid.jsonl"), "t-mid", []string{
		`{"type":"user","sessionId":"t-mid","timestamp":"2026-02-15T10:00:00Z","message":{"role":"user","content":"middle change to pkg/auth/token.go"}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-repo", "t-new.jsonl"), "t-new", []string{
		`{"type":"user","sessionId":"t-new","timestamp":"2026-03-20T10:00:00Z","message":{"role":"user","content":"newest change to pkg/auth/token.go"}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-repo", "t-old.jsonl"), "t-old", []string{
		`{"type":"user","sessionId":"t-old","timestamp":"2026-01-10T10:00:00Z","message":{"role":"user","content":"initial pkg/auth/token.go setup"}}`,
	})

	var buf bytes.Buffer
	dir := index.DefaultDir()
	if err := runTrail(dir, []string{"pkg/auth/token.go", "--all-projects", "--json"}, &buf); err != nil {
		t.Fatalf("runTrail --json: %v", err)
	}
	var rep trailReport
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatalf("unmarshal trail JSON: %v\n%s", err, buf.String())
	}
	if rep.Total != 3 || len(rep.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %#v", rep)
	}
	wantOrder := []string{"t-old", "t-mid", "t-new"}
	for i, wantID := range wantOrder {
		if rep.Entries[i].ID != wantID {
			t.Fatalf("entry[%d].ID = %q, want %q (all=%#v)", i, rep.Entries[i].ID, wantID, rep.Entries)
		}
	}

	buf.Reset()
	if err := runTrail(dir, []string{"pkg/auth/token.go", "--all-projects", "--limit", "2"}, &buf); err != nil {
		t.Fatalf("runTrail text: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "(2 of 3, oldest first):") || strings.Contains(out, "t-old") || !strings.Contains(out, "t-mid") || !strings.Contains(out, "t-new") {
		t.Fatalf("unexpected --limit 2 text output:\n%s", out)
	}

	buf.Reset()
	if err := runTrail(dir, []string{"pkg/auth/missing.go", "--all-projects"}, &buf); err != nil {
		t.Fatalf("runTrail empty: %v", err)
	}
	if !strings.Contains(buf.String(), "trail: no sessions mention or touch pkg/auth/missing.go") {
		t.Fatalf("unexpected empty trail output:\n%s", buf.String())
	}
}

func TestStatusTextErrorsAndHarnesses(t *testing.T) {
	hermeticEnv(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	envKeys := []string{
		"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT",
		"CODEX_THREAD_ID", "CODEX_CI", "CODEX_SANDBOX", "CODEX_MANAGED_BY_NPM",
		"GEMINI_CLI", "OPENCODE", "CURSOR_AGENT",
		"ANTIGRAVITY_AGENT", "KIMI_CLI", "QWEN_CODE",
	}
	clearEnvs := func() {
		for _, k := range envKeys {
			t.Setenv(k, "")
		}
	}
	clearEnvs()

	missingIdx := filepath.Join(t.TempDir(), "missing-index")
	var buf bytes.Buffer
	if err := runStatus(missingIdx, nil, &buf); err != nil {
		t.Fatalf("runStatus text unwired: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "Hooks: not wired") ||
		!strings.Contains(out, "Index: not built yet") ||
		!strings.Contains(out, "deja install --auto") {
		t.Fatalf("unexpected unwired status text:\n%s", out)
	}

	t.Setenv("CLAUDECODE", "1")
	buf.Reset()
	if err := runStatus(missingIdx, nil, &buf); err != nil {
		t.Fatalf("runStatus text active harness: %v", err)
	}
	if !strings.Contains(buf.String(), "Active harness: claude") {
		t.Fatalf("missing active harness line:\n%s", buf.String())
	}

	cases := []struct {
		env  string
		want string
	}{
		{"CODEX_THREAD_ID", "codex"},
		{"GEMINI_CLI", "gemini"},
		{"OPENCODE", "opencode"},
		{"CURSOR_AGENT", "cursor"},
		{"ANTIGRAVITY_AGENT", "antigravity"},
		{"KIMI_CLI", "kimi"},
		{"QWEN_CODE", "qwen"},
	}
	for _, tc := range cases {
		clearEnvs()
		t.Setenv(tc.env, "1")
		if got := statusDetectActiveHarness(); got != tc.want {
			t.Errorf("statusDetectActiveHarness() with %s=1 = %q, want %q", tc.env, got, tc.want)
		}
	}
	clearEnvs()

	for _, tc := range []struct{ in, want string }{
		{"claude-code", "claude"},
		{"gemini-cli", "gemini"},
		{"cursor-cli", "cursor"},
	} {
		if got := statusNormalizeHarnessInput(tc.in); got != tc.want {
			t.Errorf("statusNormalizeHarnessInput(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-proj", "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"hello status"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, discardWriter{}); err != nil {
		t.Fatalf("index.Ensure: %v", err)
	}
	claudeSettings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(claudeSettings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeSettings, []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"deja hook-context"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	if err := runStatus(dir, []string{"--harness", "claude-code"}, &buf); err != nil {
		t.Fatalf("runStatus wired text: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "Harness: claude") ||
		!strings.Contains(out, "Hooks: wired") ||
		!strings.Contains(out, "Wired targets: claude-code") ||
		!strings.Contains(out, "Index: 1 session(s)") {
		t.Fatalf("unexpected wired status text:\n%s", out)
	}

	for _, bad := range [][]string{
		{"--harness"},
		{"--harness", "bogus-harness"},
		{"--bogus"},
		{"positional"},
	} {
		if err := runStatus(missingIdx, bad, discardWriter{}); err == nil {
			t.Errorf("runStatus(%v) unexpectedly succeeded", bad)
		}
	}
}

func TestTrailParsingOrderingAndErrors(t *testing.T) {
	opts, err := parseTrailArgs([]string{"--project", "alpha", "--harness", "claude", "--limit", "5", "--", "-dash.go"})
	if err != nil {
		t.Fatalf("parseTrailArgs valid: %v", err)
	}
	if opts.project != "alpha" || opts.harness != "claude" || opts.limit != 5 || opts.path != "-dash.go" {
		t.Fatalf("unexpected parsed options: %#v", opts)
	}

	for _, bad := range [][]string{
		{},
		{"a.go", "b.go"},
		{"--", "   "},
		{"--project"},
		{"--harness"},
		{"--limit"},
		{"--limit", "0"},
		{"--limit", "201"},
		{"--bogus"},
	} {
		if _, perr := parseTrailArgs(bad); perr == nil {
			t.Errorf("parseTrailArgs(%v) unexpectedly succeeded", bad)
		}
	}

	if terr := runTrail(index.DefaultDir(), []string{"pkg/auth/token.go", "--harness", "bogus-harness"}, discardWriter{}); terr == nil {
		t.Fatal("runTrail with bogus --harness unexpectedly succeeded")
	}

	t1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	if trailSessionTime(model.Session{Updated: t2}) != t2 {
		t.Fatal("trailSessionTime should fall back to Updated when Started is zero")
	}
	if compareTrailChrono(
		model.Session{ID: "a", Harness: "claude", Started: t1, Updated: t1},
		model.Session{ID: "b", Harness: "claude", Started: t1, Updated: t2},
	) >= 0 {
		t.Fatal("compareTrailChrono should break equal Started ties by Updated")
	}
	if compareTrailChrono(
		model.Session{ID: "a", Harness: "claude", Started: t1, Updated: t1},
		model.Session{ID: "b", Harness: "claude", Started: t1, Updated: t1},
	) >= 0 {
		t.Fatal("compareTrailChrono should break equal Started/Updated ties by Key")
	}

	var buf bytes.Buffer
	err = printTrailReport(&buf, "pkg/x.go", 1, []trailEntry{{ID: "id1", Harness: "claude", Updated: t1}})
	if err != nil {
		t.Fatalf("printTrailReport: %v", err)
	}
	if !strings.Contains(buf.String(), "(untitled)") {
		t.Fatalf("expected (untitled) fallback, got:\n%s", buf.String())
	}
	buf.Reset()
	if err := printTrailReport(&buf, "pkg/x.go", 1, []trailEntry{{ID: "id2", Harness: "codex"}}); err != nil {
		t.Fatalf("printTrailReport zero time: %v", err)
	}
	if !strings.Contains(buf.String(), "  -  codex") {
		t.Fatalf("expected '-' stamp fallback, got:\n%s", buf.String())
	}
}

func TestHandoffSourceFilteredErrorsAndPrefix(t *testing.T) {
	withStatsStores(t)

	out, err := captureRun(t, "handoff", "--project", "alpha", "c2")
	if err != nil {
		t.Fatalf("handoff --project alpha c2: %v", err)
	}
	if !strings.Contains(out, "march alpha") {
		t.Fatalf("expected c2 session digest, got:\n%s", out)
	}

	for _, args := range [][]string{
		{"handoff", "--project", "beta", "c2"},
		{"handoff", "--project", "alpha", "zzzz"},
		{"handoff", "--project", "missing-proj", "--harness", "claude"},
		{"handoff", "--project", "missing-proj"},
	} {
		if _, err := captureRun(t, args...); err == nil {
			t.Errorf("captureRun(%v) unexpectedly succeeded", args)
		}
	}
}

// Install writes skills into directories that belong to the user, and replaced
// one whenever its contents differed from what deja generates. That is right
// for a stale copy an older deja wrote and wrong for a file someone edited, and
// nothing on disk told them apart: a person who tuned the wording of their
// skill lost it on the next install, silently, with no way to notice except
// reading the file again.
func TestAnEditedSkillIsKept(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(t.TempDir(), "skills", "deja-history", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	ours := []byte("---\nname: deja-history\n---\n\nask deja first.\n")
	if _, err := writeGuidanceFile(path, nil, ours); err != nil {
		t.Fatal(err)
	}

	edited := []byte("---\nname: deja-history\n---\n\nask deja first, and always in Russian.\n")
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}

	next := []byte("---\nname: deja-history\n---\n\nask deja first, then blame.\n")
	action, err := writeGuidanceFile(path, edited, next)
	if err != nil {
		t.Fatal(err)
	}
	if action != "kept" {
		t.Errorf("an edited skill reported %q, not kept", action)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "always in Russian") {
		t.Errorf("the user's own wording was overwritten:\n%s", got)
	}
}

// A copy deja wrote is still refreshed, or an improvement to the skill never
// reaches anyone who left it alone.
func TestAStaleSkillOfOursIsRefreshed(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(t.TempDir(), "skills", "deja-history", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	ours := []byte("old wording\n")
	if _, err := writeGuidanceFile(path, nil, ours); err != nil {
		t.Fatal(err)
	}
	next := []byte("new wording\n")
	if _, err := writeGuidanceFile(path, ours, next); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new wording\n" {
		t.Errorf("a skill deja wrote was not refreshed: %q", got)
	}
}

// And --force is how someone asks for deja's version back.
func TestForceTakesTheSkillBack(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(t.TempDir(), "skills", "deja-history", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := writeGuidanceFile(path, nil, []byte("ours\n")); err != nil {
		t.Fatal(err)
	}
	edited := []byte("mine\n")
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	forceGuidance = true
	defer func() { forceGuidance = false }()
	if _, err := writeGuidanceFile(path, edited, []byte("ours again\n")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "ours again\n" {
		t.Errorf("--force did not take the skill back: %q", got)
	}
}

// A block inside the user's own AGENTS.md is edited surgically and was never at
// risk, so it must not start being refused.
func TestABlockInSomeoneElsesFileIsStillWritten(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(path, []byte("their notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeGuidanceFile(path, []byte("their notes\n"), []byte("their notes\nand deja's block\n")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "deja's block") {
		t.Errorf("a guidance block was refused as if it were a skill: %q", got)
	}
}

// deja cannot tell its own pre-marks copy from a file someone else wrote at the
// same path, and treating every unmarked file as a stranger's would freeze the
// skill on every machine installed before marks existed. What it can do is say
// what it replaced: this came out as "guidance updated", with the backup
// unmentioned (#1703).
func TestInstallReportsReplacingAnUnmarkedSkill(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".claude", "skills", "deja-history")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	const mine = "---\nname: deja-history\ndescription: MY OWN skill\n---\n\nI wrote this by hand.\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "install", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "replaced") {
		t.Errorf("the run does not say a file was replaced:\n%s", out)
	}
	if !strings.Contains(out, ".bak") {
		t.Errorf("the run does not name the backup:\n%s", out)
	}
	b, readErr := os.ReadFile(path + ".bak")
	if readErr != nil {
		t.Fatalf("no backup was written: %v", readErr)
	}
	if string(b) != mine {
		t.Errorf("the backup is not what the user had:\n%s", b)
	}
}

// backupOnce keeps the first .bak it ever wrote, so a second replacement — a
// machine whose mark record was lost — would have promised a copy of the first
// file while the one being destroyed went unsaved (#1703, found in review).
func TestReplacedSkillBackupHoldsWhatWasReplaced(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".claude", "skills", "deja-history")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte("A: my first file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code"); err != nil {
		t.Fatal(err)
	}
	// The marks record is lost — a wiped cache — and a different file is there.
	if err := os.Remove(skillMarksPath()); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	const second = "C: my second file, written later\n"
	if err := os.WriteFile(path, []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != second {
		t.Errorf("the backup holds %q, not the file that was just replaced", string(b))
	}
}

// The control: a file deja wrote and recorded is updated quietly, and a fresh
// install with no file says nothing about replacing anything.
func TestInstallDoesNotCryReplacedForItsOwnSkill(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "replaced") {
		t.Errorf("a first install claims to have replaced something:\n%s", out)
	}
	out, err = captureRun(t, "install", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "replaced") {
		t.Errorf("a second install claims to have replaced something:\n%s", out)
	}
}

// A store where one harness holds almost everything is the ordinary case: on
// this machine claude holds 133k messages and zed 3.7k. The question #1556
// asks is whether an answer that exists only in the small store reaches the
// injected block, or whether the head of the ranking is simply taken by the
// harness with the most sessions.
//
// The fixture plants the answer in the small store and asks the question the
// planted session answers. It measures two things a report cannot separate on
// a private store: whether search finds the session at all, and whether the
// prompt hook cites it.
func TestASmallStoresAnswerReachesTheInjectedBlock(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	codexRoot := os.Getenv("DEJA_CODEX_ROOT")

	// The big store: many sessions about the ordinary work of the project,
	// each touching the words the questions below use. This is what makes the
	// fixture worth anything — a small store wins trivially when nothing else
	// in the corpus shares its vocabulary, and that is not the case #1556
	// measured.
	seedBigStore(t, claudeRoot)

	// The small store: five sessions, each settling something no claude
	// session mentions.
	// The question is not the planted words: a reader asks in their own
	// phrasing, which is what leaves room for the big store to answer first.
	planted := []struct{ id, topic, asked, answer, question string }{
		{"cx1", "battery cycle data",
			"the laptop health panel shows the wrong number of charges",
			"the macbook m2 battery cycle count is read from ioreg, not from the smc",
			"how do we read the cycle count off a macbook battery"},
		{"cx2", "referral credits",
			"a user says their invite bonus never showed up",
			"a referral credit is only granted after the invitee's first payment clears",
			"when does a referral actually credit the account"},
		{"cx3", "sandbox dns",
			"builds cannot reach the package mirror from inside the jail",
			"the sandbox resolver ignores /etc/resolv.conf and uses the vpn nameserver",
			"why does dns behave differently inside the sandbox"},
		{"cx4", "font fallback",
			"exported documents show tofu boxes for some characters",
			"the pdf exporter falls back to noto when a glyph is missing from the embedded font",
			"what happens to a missing glyph when we export a pdf"},
		{"cx5", "clock skew",
			"batches from the eu region are being refused at import",
			"the ledger rejects a batch whose clock skew exceeds ninety seconds",
			"how much clock skew will the ledger tolerate on a batch"},
	}
	// And older than everything in the big store: the small store must win on
	// what it says, not on when it was written.
	for i, p := range planted {
		at := time.Now().Add(-time.Duration(30*24+i) * time.Hour).UTC()
		writeCodexRollout(t, codexRoot, p.id, at, p.asked, p.answer)
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	found, cited := 0, 0
	for _, p := range planted {
		// The session is in the index: what follows is about ranking, not
		// about ingestion.
		if _, ok, err := index.FindByPrefix(index.DefaultDir(), p.id); err != nil || !ok {
			t.Fatalf("%s was never indexed (ok=%v err=%v)", p.id, ok, err)
		}
		res, err := index.SearchDetailed(index.DefaultDir(), query.Options{Query: p.question, All: true})
		if err != nil {
			t.Fatal(err)
		}
		rank := 0
		for i, s := range res.Sessions {
			if s.ID == p.id {
				rank = i + 1
				break
			}
		}
		if rank > 0 {
			found++
		}
		block := promptHookContext(t, p.question)
		in := strings.Contains(block, p.answer) || strings.Contains(block, p.id)
		if in {
			cited++
		}
		t.Logf("%-18s search rank %-3s cited %v", p.topic, rankLabel(rank), in)
	}
	t.Logf("small-store answers: returned by search %d/%d, cited by the hook %d/%d",
		found, len(planted), cited, len(planted))

	// The floor, not the finding: the block must not be single-harness by
	// construction. The numbers above are the finding, and they are what
	// #1556 asked for; they move with any ranking change, which is the point
	// of keeping them in a test rather than in a report.
	if cited == 0 {
		t.Errorf("no small-store answer reached the injected block, though search returned %d of %d",
			found, len(planted))
	}
}

func rankLabel(rank int) string {
	if rank == 0 {
		return "-"
	}
	return strconv.Itoa(rank)
}

// seedBigStore writes the dominant harness: 200 sessions of ordinary project
// work, each touching the vocabulary of the questions the tests ask, and each
// repeated five ways apart from a shard number — which is what a real store
// looks like and what makes near-duplicates worth collapsing.
func seedBigStore(t *testing.T, claudeRoot string) {
	t.Helper()
	noise := []string{
		"the battery of tests for the cycle detector kept flaking on ci",
		"a referral from the docs page credits the wrong campaign in analytics",
		"the resolver in the sandbox build reads the wrong config for dns",
		"the font in the pdf export is fine, the missing glyph is in the ui",
		"the clock in the ledger dashboard drifts, the skew banner is cosmetic",
	}
	for i := range 200 {
		at := time.Now().Add(-time.Duration(i+2) * time.Hour).UTC().Format(time.RFC3339)
		id := fmt.Sprintf("big%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-app", id+".jsonl"), "", []string{
			line("user", id, at, fmt.Sprintf("%s (shard %d)", noise[i%len(noise)], i)),
			line("assistant", id, at, fmt.Sprintf("we raised the shard %d prefetch and it settled", i)),
		})
	}
}

func line(role, id, at, text string) string {
	b, err := json.Marshal(map[string]any{
		"type": role, "sessionId": id, "timestamp": at, "cwd": "/tmp/app",
		"message": map[string]any{"role": role, "content": text},
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func writeCodexRollout(t *testing.T, root, id string, at time.Time, question, answer string) {
	t.Helper()
	stamp := at.Format(time.RFC3339)
	path := filepath.Join(root, "sessions", "2026", "08", "30",
		"rollout-"+at.Format("2006-01-02T15-04-05")+"-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"type":"session_meta","timestamp":"` + stamp + `","payload":{"session_id":"` + id + `","cwd":"/tmp/app"}}`,
		`{"type":"message","timestamp":"` + stamp + `","payload":{"role":"user","content":` + mustJSON(t, question) + `}}`,
		`{"type":"message","timestamp":"` + stamp + `","payload":{"role":"assistant","content":` + mustJSON(t, answer) + `}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func promptHookContext(t *testing.T, prompt string) string {
	t.Helper()
	var out bytes.Buffer
	payload, err := json.Marshal(map[string]string{"prompt": prompt})
	if err != nil {
		t.Fatal(err)
	}
	if err := runHookPrompt(index.DefaultDir(), bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if out.Len() == 0 {
		return ""
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("bad hook json %q: %v", out.String(), err)
	}
	return resp.HookSpecificOutput.AdditionalContext
}

// Two sessions that say the same thing about the question, differing only in a
// number, are one answer. Spending both slots on them costs the reader the
// other answer entirely — which is what the clock-skew row of the fixture
// above was: two copies of one dashboard complaint, while the session that
// answers the question sat first in search and never appeared (#1556).
func TestTwoSessionsThatDifferByANumberAreOneAnswer(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	codexRoot := os.Getenv("DEJA_CODEX_ROOT")
	seedBigStore(t, claudeRoot)
	writeCodexRollout(t, codexRoot, "answer", time.Now().Add(-30*24*time.Hour).UTC(),
		"batches from the eu region are being refused at import",
		"the ledger rejects a batch whose clock skew exceeds ninety seconds")
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	block := promptHookContext(t, "how much clock skew will the ledger tolerate on a batch")
	if n := strings.Count(block, "the skew banner is cosmetic"); n > 1 {
		t.Errorf("the block spent %d slots on one answer:\n%s", n, block)
	}
	// And the slot the duplicate gave up goes to the session that answers the
	// question, which sits below the ranking window the forty copies filled.
	if !strings.Contains(block, "ninety seconds") {
		t.Errorf("the answer never reached the block:\n%s", block)
	}
}

// The command takes nothing, and --json exists on seven of its neighbours — a
// script that reached for it here got the tab-separated table back and parsed
// it as JSON (#747).
func TestSourcesRejectsArguments(t *testing.T) {
	withTempStores(t)
	for _, arg := range []string{"--json", "--nosuchflag", "extra", "-j"} {
		_, err := captureRun(t, "sources", arg)
		if err == nil {
			t.Errorf("%q was accepted", arg)
			continue
		}
		if !strings.Contains(err.Error(), "takes no arguments") {
			t.Errorf("%q: %v", arg, err)
		}
	}
	// With nothing to reject it still prints the table.
	out, err := captureRun(t, "sources")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "claude") {
		t.Errorf("sources printed %q", out)
	}
}

// The reason on the row is what sqlite3 said, not its exit code (#3190).
func TestSourcesNamesSqlitesReasonForAStoreThatIsNotADatabase(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	if err := os.WriteFile(db, []byte("garbage, not a database, sixty-four bytes of nothing useful....."), 0o644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OPENCODE_DB", db)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	out := captureStdout(t, func() { printSources(filepath.Join(tmp, "index.db")) })
	for l := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(l, "opencode\t") {
			if !strings.Contains(l, "cannot be read — sqlite3: ") || !strings.Contains(l, "not a database") {
				t.Fatalf("row does not carry sqlite's reason: %s", l)
			}
			return
		}
	}
	t.Fatalf("no opencode row in:\n%s", out)
}

// `deja sources` answers "where did deja look", and the empty-state advice
// points people at it. Its harness list was hand-maintained and had fallen four
// behind the registry: cline, roo, pi and openclaw were absent while their
// sessions — 33 of them here — sat in the index.
func TestSourcesListsEveryHarnessInTheRegistry(t *testing.T) {
	hermeticEnv(t)
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r)
		done <- b.String()
	}()
	printSources(t.TempDir())
	_ = w.Close()
	os.Stdout = old
	out := <-done

	for _, h := range sources.Registry() {
		if !strings.Contains(out, h.Name) {
			t.Errorf("deja sources never mentions %q, so nobody can tell whether deja looked there", h.Name)
		}
	}

	// The security model tells a reader that an exclude line has to name one of
	// the stores this command prints, and it spells that count in words. It said
	// twenty-five while the command printed thirty-five — ten stores a reader
	// would not know they could exclude. The number is the row count, not the
	// registry's: deja prints its own store too.
	rows := 0
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) != "" {
			rows++
		}
	}
	word, ok := countWords[rows]
	if !ok {
		t.Fatalf("deja sources prints %d rows and this test has no word for it; add one", rows)
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "SECURITY-MODEL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "the " + word + " `deja sources` prints"; !strings.Contains(string(b), want) {
		t.Errorf("docs/SECURITY-MODEL.md does not say %q — deja sources prints %d rows", want, rows)
	}
}

// Every row names where deja looks, even before the harness is installed, so
// someone who installs it later knows where its history should appear. Four
// rows built their location only from directories that exist and printed an
// empty column on a fresh machine (#5638).
func TestSourcesNamesALocationForEveryStoreOnAnEmptyHome(t *testing.T) {
	hermeticEnv(t)
	var out bytes.Buffer
	printSourcesTo(&out, t.TempDir())

	rows := 0
	for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows++
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || strings.TrimSpace(fields[1]) == "" {
			t.Errorf("deja sources prints no location for %q on an empty home: %q", fields[0], line)
		}
	}
	if rows == 0 {
		t.Fatal("deja sources printed no rows")
	}
}

// The four rows that fall back name the default only while deja has found
// nothing: on an empty home each names its own default, and once a store
// exists the row names the roots deja reads, joined as before (#5638).
func TestSourcesFallbackRowsNameTheDefaultUntilAStoreExists(t *testing.T) {
	hermeticEnv(t)
	for _, env := range []string{"DEJA_COPILOT_CHAT_ROOTS", "DEJA_ROO_ROOTS", "DEJA_KILO_ROOTS", "DEJA_CHERRYSTUDIO_ROOTS"} {
		t.Setenv(env, "")
	}
	t.Setenv("DEJA_ROO_CLI_ROOT", filepath.Join(t.TempDir(), "absent"))
	locations := func() map[string]string {
		var out bytes.Buffer
		printSourcesTo(&out, t.TempDir())
		got := map[string]string{}
		for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
			if fields := strings.Split(line, "\t"); len(fields) >= 2 {
				got[fields[0]] = fields[1]
			}
		}
		return got
	}
	check := func(stage string, want map[string]string) {
		t.Helper()
		got := locations()
		for name, w := range want {
			if got[name] != w {
				t.Errorf("%s: %s location = %q, want %q", stage, name, got[name], w)
			}
		}
	}

	check("empty home", map[string]string{
		"copilot-chat": sources.CopilotChatDefaultRoot(),
		"roo":          sources.RooDefaultRoot(),
		"kilocode":     sources.KiloDefaultRoot(),
		"cherrystudio": sources.CherryStudioDefaultRoot(),
	})

	// Stores in hosts other than the default, so a row that kept naming the
	// default would not match.
	base := filepath.Dir(filepath.Dir(sources.CopilotChatDefaultRoot()))
	codium := filepath.Join(base, "VSCodium", "User")
	cursor := filepath.Join(base, "Cursor", "User")
	rooCodium := filepath.Join(codium, "globalStorage", sources.RooExtensionID)
	rooCursor := filepath.Join(cursor, "globalStorage", sources.RooExtensionID)
	kilo := filepath.Join(codium, "globalStorage", sources.KiloExtensionID)
	cherryPi := filepath.Join(filepath.Dir(sources.CherryStudioDefaultRoot()), ".pi", "sessions")
	for _, d := range []string{rooCodium, rooCursor, kilo, cherryPi} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sep := string(os.PathListSeparator)
	check("stores present", map[string]string{
		"copilot-chat": codium,
		"roo":          rooCodium + sep + rooCursor,
		"kilocode":     kilo,
		"cherrystudio": cherryPi,
	})
}

// `deja sources` is the command the empty-machine advice names, and a store
// deja is not allowed to read looked exactly like one nobody has used —
// `sessions=0 messages=0 size=0 B` and nothing else (#1000).
func TestSourcesSaysWhenAStoreCannotBeRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	tmp := hermeticEnv(t)
	qwen := filepath.Join(tmp, "qwen")
	projects := filepath.Join(qwen, "projects", "p1")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projects, "s.json"), []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", qwen)

	out, err := captureRun(t, "sources")
	if err != nil {
		t.Fatal(err)
	}
	if line := storeLine(out, "qwen"); strings.Contains(line, "cannot be read") {
		t.Fatalf("a readable store was reported as locked: %q", line)
	}

	if err = os.Chmod(filepath.Join(qwen, "projects"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(qwen, "projects"), 0o755) })

	out, err = captureRun(t, "sources")
	if err != nil {
		t.Fatal(err)
	}
	line := storeLine(out, "qwen")
	if !strings.Contains(line, "cannot be read") {
		t.Errorf("a locked store reads as an empty one: %q", line)
	}
	if !strings.Contains(line, "permission denied") {
		t.Errorf("the line does not say why: %q", line)
	}
	// Every other store keeps its plain row.
	if l := storeLine(out, "claude"); strings.Contains(l, "cannot be read") {
		t.Errorf("an untouched store picked up the note: %q", l)
	}
}

func storeLine(out, name string) string {
	for l := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(l, name+"\t") {
			return l
		}
	}
	return ""
}

// An excluded store is named and not read. `deja sources` drives most of its
// rows from one table and honoured the exclusion there, while aider's row and
// opencode's are written by hand below it — so the store the reporter of #3499
// actually named kept being opened, and kept printing the sqlite3 error the
// exclusion exists to silence.
func TestSourcesNamesAnExcludedStoreWithoutReadingIt(t *testing.T) {
	tmp := hermeticEnv(t)
	db := filepath.Join(tmp, "opencode.db")
	// Not a database: reading it is what produces the line this test is about.
	if err := os.WriteFile(db, []byte("not a database at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCODE_DB", db)
	history := filepath.Join(os.Getenv("HOME"), ".aider.chat.history.md")
	if err := os.MkdirAll(filepath.Dir(history), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(history, []byte("# aider chat started at 2026-09-01\n\n#### hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	row := func(name string) string {
		out := captureStdout(t, func() { printSources(filepath.Join(tmp, "index.db")) })
		for l := range strings.SplitSeq(out, "\n") {
			if strings.HasPrefix(l, name+"\t") {
				return l
			}
		}
		t.Fatalf("no %s row in:\n%s", name, out)
		return ""
	}

	t.Setenv("DEJA_EXCLUDE_HARNESSES", "")
	if got := row("opencode"); !strings.Contains(got, "sessions=") {
		t.Fatalf("an unexcluded store does not report its counts: %q", got)
	}

	t.Setenv("DEJA_EXCLUDE_HARNESSES", "opencode,aider")
	for _, name := range []string{"opencode", "aider"} {
		got := row(name)
		if !strings.Contains(got, "excluded") {
			t.Errorf("%s is excluded and its row does not say so: %q", name, got)
		}
		if strings.Contains(got, "sessions=") {
			t.Errorf("%s is excluded and was read anyway: %q", name, got)
		}
		if strings.Contains(got, "cannot be read") {
			t.Errorf("%s is excluded and still reports why it could not be read: %q", name, got)
		}
	}
}

// `deja sources` subtracts excluded sessions from every store's row and says
// how many it dropped. opencode's row was counted straight out of sqlite, so
// the one store whose count came from SQL kept reporting sessions that are not
// indexed, not searchable and not exported (#2247).
func TestSourcesCountsOpencodeWithoutExcludedProjects(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/work/keep','2026-01-02T03:00:00Z','2026-01-02T03:00:00Z');
insert into session values('s2','/work/secret','2026-01-02T03:00:00Z','2026-01-02T03:00:00Z');
insert into message values('m1','s1',1,'{"role":"user"}');
insert into message values('m2','s2',1,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"hello from keep"}');
insert into part values('p2','m2','{"type":"text","text":"hello from secret"}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}

	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OPENCODE_DB", db)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	// A claude session in the same excluded project, as the row to compare
	// against: that store has always subtracted.
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-work-secret")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"c1","timestamp":%q,"cwd":"/work/secret",`+
		`"message":{"role":"user","content":"hello from secret"}}`, at)
	if err := os.WriteFile(filepath.Join(proj, "c1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "secret")

	out := captureStdout(t, func() { printSources(filepath.Join(tmp, "index.db")) })
	row := func(name string) string {
		for l := range strings.SplitSeq(out, "\n") {
			if strings.HasPrefix(l, name+"\t") {
				return l
			}
		}
		t.Fatalf("no %s row in:\n%s", name, out)
		return ""
	}

	// The premise: the store that has always subtracted still does, or there
	// is nothing to compare against.
	if got := row("claude"); !strings.Contains(got, "sessions=0") || !strings.Contains(got, "excluded-sessions=1") {
		t.Fatalf("claude row does not show the exclusion applied: %s", got)
	}
	if got := row("opencode"); !strings.Contains(got, "sessions=1 messages=1") {
		t.Errorf("one of two opencode sessions is excluded, the row says: %s", got)
	}
	if got := row("opencode"); !strings.Contains(got, "excluded-sessions=1") {
		t.Errorf("the opencode row does not say what it dropped: %s", got)
	}
}

// And a pattern that excludes nothing must leave the numbers alone: the row is
// counted in SQL, while the loader that measures the exclusion drops a session
// holding no text at all, which SQL counts (#2247).
func TestAPatternThatMatchesNothingLeavesTheOpencodeRowAlone(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/work/keep','2026-01-02T03:00:00Z','2026-01-02T03:00:00Z');
insert into session values('s2','/work/empty','2026-01-02T03:00:00Z','2026-01-02T03:00:00Z');
insert into message values('m1','s1',1,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"hello from keep"}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OPENCODE_DB", db)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	row := func() string {
		out := captureStdout(t, func() { printSources(filepath.Join(tmp, "index.db")) })
		for l := range strings.SplitSeq(out, "\n") {
			if strings.HasPrefix(l, "opencode\t") {
				return l
			}
		}
		t.Fatalf("no opencode row in:\n%s", out)
		return ""
	}
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "")
	before := row()
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "nothing-matches-this")
	after := row()
	for _, want := range []string{"sessions=2", "messages=1"} {
		if !strings.Contains(before, want) {
			t.Fatalf("the premise is wrong — without a pattern the row reads %s", before)
		}
		if !strings.Contains(after, want) {
			t.Errorf("a pattern matching nothing changed the row to %s", after)
		}
	}
	if strings.Contains(after, "excluded-sessions=") {
		t.Errorf("nothing was excluded and the row says it dropped something: %s", after)
	}
}

// The maintained Grok CLI writes one SQLite store beside the config and no
// session files at all, so on that build the row read `sessions=0 messages=0
// size=0 B` while doctor named the store and search answered from it. The row
// has to load what the registry loads (#3225).
func TestSourcesCountsGroksDatabase(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	home := hermeticEnv(t)
	root := filepath.Join(home, "grok")
	t.Setenv("DEJA_GROK_ROOT", root)
	t.Setenv("DEJA_GROK_DB", "")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	schema := `
CREATE TABLE workspaces (id TEXT PRIMARY KEY, canonical_path TEXT);
CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
INSERT INTO sessions VALUES ('s1','w1','Pool timeouts','/work/api','2026-07-27T10:00:00.000Z');
INSERT INTO messages VALUES ('s1',0,'user','{"role":"user","content":"zibblex pool exhausted again"}','2026-07-27T10:00:00.000Z');
INSERT INTO sessions VALUES ('s2','w1','Retry cap','/work/api','2026-07-28T10:00:00.000Z');
INSERT INTO messages VALUES ('s2',0,'user','{"role":"user","content":"the retry cap is four"}','2026-07-28T10:00:00.000Z');
`
	cmd := exec.CommandContext(t.Context(), "sqlite3", filepath.Join(root, "grok.db"))
	cmd.Stdin = strings.NewReader(schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}

	// The row itself, not the loader beside it: the count comes out of
	// printSources, and a test on the pieces would have passed while the row
	// printed zeros.
	out := captureStdout(t, func() { printSources(filepath.Join(home, "index.db")) })
	line := ""
	for l := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(l, "grok\t") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no grok row:\n%s", out)
	}
	if strings.Contains(line, "sessions=0") || strings.Contains(line, "size=0 B") {
		t.Errorf("the row does not count the database:\n%s", line)
	}

	var loaded int
	for _, s := range append(sources.LoadGrok(), sources.LoadGrokDB()...) {
		loaded += len(s.Messages)
	}
	if loaded == 0 {
		t.Fatal("the fixture produced nothing, so the row proves nothing")
	}
	files := grokReadFiles()
	found := false
	for _, p := range files {
		if filepath.Base(p) == "grok.db" {
			found = true
		}
	}
	if !found {
		t.Errorf("the row's file list has no grok.db, so the size column reads 0 B: %v", files)
	}
	if n := filesSize(presentFiles(files...)); n == 0 {
		t.Errorf("the row measures %d bytes with a seeded database on disk", n)
	}
}

// `deja sources` says when a store holds a transcript the index has never
// read, in the words doctor has used since #3747. It is the command people run
// first, and its session count reads as "nothing written yet" rather than
// "files never opened" — the gap doctor was taught to name and this row was
// not (#3752).
func TestSourcesSaysWhenATranscriptWasNeverRead(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	write := func(name, sid, text string) {
		t.Helper()
		p := filepath.Join(claude, "-p-app", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-09-01T10:00:00Z","cwd":"/p/app","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	row := func() string {
		t.Helper()
		out, err := captureRun(t, "sources")
		if err != nil {
			t.Fatal(err)
		}
		return storeLine(out, "claude")
	}

	write("one.jsonl", "aaa", "the pool ran dry under load")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := row(); strings.Contains(got, "never read") {
		t.Fatalf("row = %q, want no note while everything is read", got)
	}

	// A second transcript arrives and nothing has indexed it.
	write("two.jsonl", "bbb", "the cache eviction was wrong")
	got := row()
	if !strings.Contains(got, "1 transcript never read") {
		t.Fatalf("row = %q, want it to name the transcript never read", got)
	}
	if !strings.Contains(got, "deja index") {
		t.Fatalf("row = %q, want the command that reads it", got)
	}
	// Appended after the counts, so a script reading the leading fields of the
	// tab-separated row is not moved.
	if !strings.HasPrefix(got, "claude\t") || !strings.Contains(got, "sessions=") {
		t.Fatalf("row = %q, want the existing fields where they were", got)
	}

	// After a pass it goes quiet.
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := row(); strings.Contains(got, "never read") {
		t.Fatalf("row = %q after an index pass, want the note gone", got)
	}
}

// One command must not give two answers about one store: the words and the
// count are doctor's, so the two surfaces agree about the same transcripts.
func TestSourcesAndDoctorAgreeAboutUnreadTranscripts(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	p := filepath.Join(claude, "-p-app", "seed.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s0","timestamp":"2026-09-01T10:00:00Z","cwd":"/p/app","message":{"role":"user","content":"seed"}}` + "\n"
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.jsonl", "b.jsonl"} {
		if err := os.WriteFile(filepath.Join(claude, "-p-app", name), []byte(strings.Replace(line, "s0", name, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := captureRun(t, "sources")
	if err != nil {
		t.Fatal(err)
	}
	if got := storeLine(out, "claude"); !strings.Contains(got, "2 transcripts never read — `deja index`") {
		t.Fatalf("sources row = %q, want doctor's wording for two", got)
	}
}

// Two transcripts can carry one conversation id: a harness mid-migration writes
// the same session to its old and new store, and a resumed run continues an id
// in a second file. The index keys on harness+id and holds one session for the
// pair — `deja index` even says so — while `deja sources`, the row people read
// to size their history, counted transcripts and claimed two.
func TestSourcesCountsOneConversationOnce(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-workspace-demo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, text, stamp string) {
		line := `{"type":"user","sessionId":"resumed-1","timestamp":"` + stamp +
			`","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, name), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("session.jsonl", "start the migration", "2026-07-17T09:00:00Z")
	write("resume.jsonl", "continue the migration", "2026-07-17T10:00:00Z")

	out, err := captureRun(t, "sources")
	if err != nil {
		t.Fatal(err)
	}
	line := storeLine(out, "claude")
	if !strings.Contains(line, "sessions=1 ") {
		t.Errorf("one conversation in two transcripts is not counted once: %q", line)
	}
	if !strings.Contains(line, "messages=2 ") {
		t.Errorf("both transcripts should still be read: %q", line)
	}
}

func sourcesLine(t *testing.T, harness string) string {
	t.Helper()
	out, err := captureRun(t, "sources")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, harness+"\t") {
			return line
		}
	}
	t.Fatalf("no %s line in:\n%s", harness, out)
	return ""
}

// `deja sources` answers "where is my history and how much of it is there". It
// summed the tree it looks in rather than the files it opens, so a codex store
// reported 108 MB of plugins, caches and sqlite WALs around 1.3 MB of
// transcripts (#654).
func TestSourcesSizeCountsOnlyWhatItReads(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "codex")
	if err := os.MkdirAll(filepath.Join(root, "sessions", "2026", "08"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CODEX_ROOT", root)
	rollout := `{"type":"session_meta","payload":{"id":"sess-1","cwd":"/work/app"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"why does the build fail"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "sessions", "2026", "08", "rollout-sess-1.jsonl"), []byte(rollout), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugins", "blob.bin"), make([]byte, 10<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	line := sourcesLine(t, "codex")
	if strings.Contains(line, "MB") {
		t.Errorf("the size counts files deja never opens: %s", line)
	}
	if !strings.Contains(line, "sessions=1") {
		t.Errorf("the transcript stopped being read: %s", line)
	}
	if !strings.Contains(line, filepath.Join("sessions", "2026", "08")) {
		t.Errorf("the path does not name where the transcripts are: %s", line)
	}
}

// A harness with nothing on this machine still names somewhere to look, rather
// than an empty column: the empty-machine advice sends people here.
func TestSourcesNamesARootWhenNothingIsRead(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "empty-codex")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CODEX_ROOT", root)
	line := sourcesLine(t, "codex")
	if !strings.Contains(line, root) {
		t.Errorf("an empty store names no path: %s", line)
	}
	if !strings.Contains(line, "size=0 B") {
		t.Errorf("an empty store reports a size: %s", line)
	}
}

// A discovery rule can name something that is not a file on this machine:
// hermes returns a token for a Postgres store. Reading a directory off that
// gave "." as the store's location.
func TestSourcesIgnoresAStoreThatIsNotAFile(t *testing.T) {
	tmp := hermeticEnv(t)
	profiles := filepath.Join(tmp, "hermes", "profiles")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_HERMES_ROOT", filepath.Join(tmp, "hermes"))
	t.Setenv("DEJA_HERMES_PG_DSN", "postgres://user:pw@localhost:5432/hermes")
	line := sourcesLine(t, "hermes")
	if strings.Contains(line, "\t.\t") || strings.Contains(line, "hermes-pg:") {
		t.Errorf("the location is not a place on disk: %s", line)
	}
	if !strings.Contains(line, "size=0 B") {
		t.Errorf("a store deja cannot weigh reports a size: %s", line)
	}
}

// An opencode store that would not open read as a store nobody had used —
// `sessions=0 messages=0` with no note — while every file-store row says
// "cannot be read" for the same thing (#1000, #3190).
func TestSourcesSaysAnOpencodeStoreCouldNotBeRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a file the owner cannot read")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"why does the pager stall"}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OPENCODE_DB", db)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	if err := os.Chmod(db, 0); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { printSources(filepath.Join(tmp, "index.db")) })
	for l := range strings.SplitSeq(out, "\n") {
		if !strings.HasPrefix(l, "opencode\t") {
			continue
		}
		if !strings.Contains(l, "cannot be read") {
			t.Fatalf("the unreadable store reads as unused: %s", l)
		}
		return
	}
	t.Fatalf("no opencode row in:\n%s", out)
}

// The recall deja puts into a spawned agent's prompt quoted the session that
// spawned it as "this was asked here before": the spawn recalls under a reader
// of its own, task:<parent>:<hash>, and the self check compared against that
// rather than against the parent, which is live and is the one asking (#4548).
func TestSpawnRecallLeavesOutTheSessionSpawningIt(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	now := time.Now()
	const parent = "107f9711-0294-4fbd-af91-11892a43e3ff"
	seedClaudeAt(t, claude, "app", "past-session", "the pgbouncer prepared statement error again",
		"We set default_query_exec_mode=exec for pgbouncer, because prepared statements do not survive transaction pooling.", now.Add(-48*time.Hour))
	seedClaudeAt(t, claude, "app", parent, "the pgbouncer prepared statement error is back in the orders service, what did we do about pgbouncer prepared statements?",
		"Sending an agent to dig into the pgbouncer prepared statement failure.", now.Add(-time.Minute))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Agent","cwd":"/tmp/app","session_id":"` + parent + `",` +
		`"toolInput":{"prompt":"Investigate the pgbouncer prepared statement error in the orders service: what did we do about pgbouncer prepared statements?"}}`
	out := toolHookRun(t, payload)
	var resp spawnHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("no spawn recall to check: %v (%q)", err, out)
	}
	var prompt string
	if err := json.Unmarshal(resp.HookSpecificOutput.UpdatedInput["prompt"], &prompt); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "107f9711") {
		t.Errorf("the spawned agent was handed the session spawning it as history:\n%s", prompt)
	}
	if !strings.Contains(prompt, "default_query_exec_mode") {
		t.Errorf("the session that settled it did not reach the spawned agent:\n%s", prompt)
	}
}

// opencode runs a task sub-agent as a session of its own, whose digest led with
// the session that spawned it — live, and asking (#4548). The plugin names the
// parent, and the digest leaves it out as it leaves out the asker.
func TestASubAgentsDigestLeavesOutItsParent(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	now := time.Now()
	seedClaudeAt(t, claude, "app", "older-session", "the ledger export drops the last row", "the writer missed a final flush", now.Add(-2*time.Hour))
	seedClaudeAt(t, claude, "app", "ses_parent", "the glimmerquest cache misses on every cold start", "warming it at boot from the last snapshot fixed it", now.Add(-time.Minute))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	run := func(payload string) string {
		withHookStdin(t, payload)
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}
	// Control: a session that is not the parent's child gets it.
	if out := run(`{"session_id":"ses_other","cwd":"/tmp/app"}`); !strings.Contains(out, "glimmerquest") {
		t.Fatalf("the control did not recall the parent's session:\n%s", out)
	}
	out := run(`{"session_id":"ses_child","parent_session_id":"ses_parent","cwd":"/tmp/app"}`)
	if strings.Contains(out, "glimmerquest") {
		t.Errorf("the sub-agent's digest led with the session that spawned it:\n%s", out)
	}
	if !strings.Contains(out, "ledger export") {
		t.Errorf("leaving the parent out took the rest of the digest with it:\n%s", out)
	}
}

// The plugin learns the parent from opencode itself: client.session.get answers
// with Session.Info, whose parentID is set on a task sub-agent's session.
func TestOpencodePluginNamesTheParentOfASubAgent(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin opencode would run")
	}
	dir := t.TempDir()
	plugin := filepath.Join(dir, "deja.mjs")
	if err = os.WriteFile(plugin, []byte(opencodeLegacyPluginJS("/usr/local/bin/deja")), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
import { DejaRecall } from "./deja.mjs";
const ran = [];
const $ = (strings, ...values) => {
  ran.push(strings.reduce((acc, s, i) => acc + s + (i < values.length ? String(values[i]) : ""), ""));
  return { text: async () => "", quiet: async () => {} };
};
const client = {
  tui: { showToast: async () => {} },
  session: { get: async ({ path }) => ({ data: { id: path.id, parentID: path.id === "ses_child" ? "ses_parent" : undefined } }) },
};
const hooks = await DejaRecall({ $, client, directory: "/w/p" });
await hooks["experimental.chat.system.transform"]({ sessionID: "ses_child" }, { system: [] });
await hooks["experimental.chat.messages.transform"]({ sessionID: "ses_child" },
  { messages: [{ info: { role: "user", sessionID: "ses_child" }, parts: [{ type: "text", text: "find the retry fix" }] }] });
for (const c of ran) console.log(c.includes("hook-context") ? "context:" + c : c.includes("hook-prompt") ? "prompt:" + c : "other:" + c);
`
	run := filepath.Join(dir, "drive.mjs")
	if err = os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	for _, kind := range []string{"context:", "prompt:"} {
		found := false
		for l := range strings.SplitSeq(string(out), "\n") {
			if strings.HasPrefix(l, kind) && strings.Contains(l, `"parent_session_id":"ses_parent"`) {
				found = true
			}
		}
		if !found {
			t.Errorf("the %s hook was not told the sub-agent's parent:\n%s", strings.TrimSuffix(kind, ":"), out)
		}
	}
}

// After a hit on a subagent's session, the line that matters is which session
// asked for it; after a hit on the parent, which sessions it spawned (#1385).
func TestShowNamesTheSessionsAroundASpawn(t *testing.T) {
	old := ChildrenOfSession
	t.Cleanup(func() { ChildrenOfSession = old })
	ChildrenOfSession = func(_, id string) ([]model.Session, error) {
		if id != "01a00a65-7a72-7102-9574-0de51ab0d0ee" {
			return nil, nil
		}
		return []model.Session{{ID: "01a00a65-child-one"}, {ID: "01a00a65-child-two"}}, nil
	}

	var w bytes.Buffer
	printSpawnEdges(&w, "", model.Session{
		ID:     "01a00a65-child-one",
		Kind:   "subagent_fork",
		Parent: "01a00a65-7a72-7102-9574-0de51ab0d0ee",
		Agent:  "general-purpose",
	})
	got := w.String()
	if !strings.Contains(got, "spawned from 01a00a65") || !strings.Contains(got, "general-purpose") {
		t.Errorf("the child does not name what asked for it:\n%s", got)
	}

	w.Reset()
	printSpawnEdges(&w, "", model.Session{ID: "01a00a65-7a72-7102-9574-0de51ab0d0ee"})
	got = w.String()
	if !strings.Contains(got, "spawned 2 sessions") {
		t.Errorf("the parent does not list its children:\n%s", got)
	}

	// One long session spawns a hundred agents. Naming every child buries the
	// count, which is the part a reader can act on.
	many := make([]model.Session, 0, 161)
	for i := range 161 {
		many = append(many, model.Session{ID: "aa" + strings.Repeat("b", 8) + string(rune('a'+i%26))})
	}
	ChildrenOfSession = func(_, _ string) ([]model.Session, error) { return many, nil }
	w.Reset()
	printSpawnEdges(&w, "", model.Session{ID: "busy-parent"})
	got = w.String()
	if !strings.Contains(got, "spawned 161 sessions") || !strings.Contains(got, "and 158 more") {
		t.Errorf("the children line is not summarised:\n%s", got)
	}
	if len(got) > 200 {
		t.Errorf("the children line is %d characters long:\n%s", len(got), got)
	}
	ChildrenOfSession = func(_, id string) ([]model.Session, error) {
		if id != "01a00a65-7a72-7102-9574-0de51ab0d0ee" {
			return nil, nil
		}
		return []model.Session{{ID: "01a00a65-child-one"}, {ID: "01a00a65-child-two"}}, nil
	}

	// A subagent whose harness recorded no parent gets a line about what it is
	// and no guess about who started it.
	w.Reset()
	printSpawnEdges(&w, "", model.Session{ID: "01a00a65-orphan", Kind: "subagent"})
	got = w.String()
	if !strings.Contains(got, "records no parent") {
		t.Errorf("an orphan subagent says nothing about itself:\n%s", got)
	}
	if strings.Contains(got, "spawned from") {
		t.Errorf("an edge was invented:\n%s", got)
	}

	// An ordinary session gains no line at all.
	w.Reset()
	printSpawnEdges(&w, "", model.Session{ID: "plain-session"})
	if w.Len() != 0 {
		t.Errorf("a session nobody spawned still printed:\n%s", w.String())
	}
}

// The Spec Kit extension is installed by `specify extension add`, which
// refuses a manifest that breaks its schema (extensions/EXTENSION-API-REFERENCE.md
// in github/spec-kit) and installs nothing. These are the rules it checks, plus
// the ones only we know: the version is the release's, and every deja call in
// the command keeps the query behind `--`.
func TestSpecKitExtensionManifest(t *testing.T) {
	m := parseSimpleYAML(t, string(repoFile(t, "extensions/spec-kit/extension.yml")))

	if m.str("schema_version") != "1.0" {
		t.Fatalf("schema_version = %q, Spec Kit reads 1.0", m.str("schema_version"))
	}
	ext := m.mapping("extension")
	id := ext.str("id")
	if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(id) {
		t.Fatalf("extension.id %q does not match ^[a-z0-9-]+$", id)
	}
	for _, k := range []string{"name", "version", "description", "author", "repository", "license"} {
		if ext.str(k) == "" {
			t.Errorf("extension.%s is empty; the schema requires it", k)
		}
	}
	if d := ext.str("description"); len(d) >= 200 {
		t.Errorf("description is %d characters; the catalog wants under 200", len(d))
	}
	// X.Y.Z only: Spec Kit rejects 1.0 and 1.0.0-beta.
	extVersion := ext.str("version")
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(extVersion) {
		t.Fatalf("extension.version %q is not X.Y.Z", extVersion)
	}

	// scripts/pinmanifests moves this with every other release manifest, and
	// the release packs the directory after that pin. A version that disagrees
	// with the Gemini manifest means the pin missed it.
	var gemini struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(repoFile(t, "gemini-extension.json"), &gemini); err != nil {
		t.Fatal(err)
	}
	if extVersion != gemini.Version {
		t.Errorf("extension.version is %s, the other release manifests say %s — run scripts/pinmanifests", extVersion, gemini.Version)
	}
	release := string(repoFile(t, ".github/workflows/release.yml"))
	pin, pack := strings.Index(release, "go run ./scripts/pinmanifests -version"), strings.Index(release, `zip -qr "$GITHUB_WORKSPACE/dist/deja-spec-kit.zip"`)
	if pin < 0 || pack < 0 || pack < pin {
		t.Errorf("release.yml must pack deja-spec-kit.zip after pinning the manifests (pin at %d, pack at %d)", pin, pack)
	}
	if !strings.Contains(release, "gh release upload \"${GITHUB_REF_NAME}\" dist/deja-spec-kit.zip") {
		t.Error("release.yml packs deja-spec-kit.zip and never attaches it to the release")
	}

	req := m.mapping("requires")
	if !strings.HasPrefix(req.str("speckit_version"), ">=") {
		t.Errorf("requires.speckit_version %q has no lower bound", req.str("speckit_version"))
	}
	tools := req.list("tools")
	if len(tools) != 1 || tools[0].str("name") != "deja" || tools[0].str("required") != "true" {
		t.Errorf("requires.tools should declare the deja binary as required, got %v", tools)
	}

	cmdName := regexp.MustCompile(`^speckit\.` + regexp.QuoteMeta(id) + `\.[a-z0-9-]+$`)
	provided := map[string]bool{}
	cmds := m.mapping("provides").list("commands")
	if len(cmds) == 0 {
		t.Fatal("provides.commands is empty")
	}
	for _, c := range cmds {
		name, file := c.str("name"), c.str("file")
		if !cmdName.MatchString(name) {
			t.Errorf("command %q does not match speckit.%s.<command>", name, id)
		}
		if c.str("description") == "" {
			t.Errorf("command %s has no description", name)
		}
		if file == "" || strings.HasPrefix(file, "/") || strings.Contains(file, "..") {
			t.Errorf("command %s file %q must be a relative path inside the extension", name, file)
			continue
		}
		provided[name] = true
		body := string(repoFile(t, "extensions/spec-kit/"+file))
		front, ok := strings.CutPrefix(body, "---\n")
		if !ok || !strings.Contains(front, "\n---\n") {
			t.Errorf("%s has no frontmatter", file)
			continue
		}
		front, rest, _ := strings.Cut(front, "\n---\n")
		if parseSimpleYAML(t, front).str("description") == "" {
			t.Errorf("%s frontmatter has no description", file)
		}
		if !strings.Contains(rest, "$ARGUMENTS") {
			t.Errorf("%s never reads $ARGUMENTS", file)
		}
		// A query is a feature's words and may start with a dash; without --
		// deja reads it as a flag and fails.
		for _, call := range regexp.MustCompile("deja (recall|files|search|fix|how) [^`\\n]*").FindAllString(rest, -1) {
			if f := strings.Fields(call); len(f) < 3 || f[2] != "--" {
				t.Errorf("%s runs %q without -- before the query", file, call)
			}
		}
	}

	hooks := m.mapping("hooks")
	if len(hooks) == 0 {
		t.Fatal("no hooks: the before_implement suggestion is gone")
	}
	for event, v := range hooks {
		h, ok := v.(yamlMap)
		if !ok {
			t.Errorf("hook %s is not a single mapping", event)
			continue
		}
		if !provided[h.str("command")] {
			t.Errorf("hook %s runs %q, which this extension does not provide", event, h.str("command"))
		}
		// optional renders as a suggestion the user answers; false would run
		// the recall on every implement without asking.
		if h.str("optional") != "true" || h.str("prompt") == "" {
			t.Errorf("hook %s must stay optional with a prompt", event)
		}
	}
}

// yamlMap is the subset of YAML the Spec Kit manifest uses: nested mappings,
// lists of mappings and scalars, quoted or plain. Enough to read the fields
// Spec Kit validates without taking a YAML dependency for one test.
type yamlMap map[string]any

func (m yamlMap) str(k string) string {
	s, _ := m[k].(string)
	return s
}

func (m yamlMap) mapping(k string) yamlMap {
	v, _ := m[k].(yamlMap)
	return v
}

func (m yamlMap) list(k string) []yamlMap {
	items, _ := m[k].([]any)
	var out []yamlMap
	for _, it := range items {
		if mm, ok := it.(yamlMap); ok {
			out = append(out, mm)
		}
	}
	return out
}

type yamlLine struct {
	indent int
	text   string
}

func parseSimpleYAML(t *testing.T, src string) yamlMap {
	t.Helper()
	var lines []yamlLine
	for i, raw := range strings.Split(src, "\n") {
		if strings.Contains(raw, "\t") {
			t.Fatalf("line %d has a tab; YAML indentation is spaces", i+1)
		}
		text := strings.TrimSpace(raw)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		lines = append(lines, yamlLine{len(raw) - len(strings.TrimLeft(raw, " ")), text})
	}
	p := &yamlParser{t: t, lines: lines}
	m := p.mapping(0)
	if p.pos != len(p.lines) {
		t.Fatalf("unparsed YAML from %q", p.lines[p.pos].text)
	}
	return m
}

type yamlParser struct {
	t     *testing.T
	lines []yamlLine
	pos   int
}

func (p *yamlParser) mapping(indent int) yamlMap {
	m := yamlMap{}
	for p.pos < len(p.lines) && p.lines[p.pos].indent == indent && !strings.HasPrefix(p.lines[p.pos].text, "- ") {
		k, v := p.pair(p.lines[p.pos].text)
		p.pos++
		if _, dup := m[k]; dup {
			p.t.Fatalf("duplicate key %q", k)
		}
		m[k] = v
		if v == nil {
			m[k] = p.block(indent)
		}
	}
	return m
}

// block reads what follows a "key:" with nothing after it.
func (p *yamlParser) block(parent int) any {
	if p.pos >= len(p.lines) || p.lines[p.pos].indent < parent || (p.lines[p.pos].indent == parent && !strings.HasPrefix(p.lines[p.pos].text, "- ")) {
		return ""
	}
	ln := p.lines[p.pos]
	if !strings.HasPrefix(ln.text, "- ") {
		return p.mapping(ln.indent)
	}
	var items []any
	for p.pos < len(p.lines) && p.lines[p.pos].indent == ln.indent && strings.HasPrefix(p.lines[p.pos].text, "- ") {
		item := strings.TrimPrefix(p.lines[p.pos].text, "- ")
		if !strings.Contains(item, ": ") && !strings.HasSuffix(item, ":") {
			items = append(items, unquoteYAML(p.t, item))
			p.pos++
			continue
		}
		// "- key: value" opens a mapping whose other keys sit two columns in.
		p.lines[p.pos] = yamlLine{ln.indent + 2, item}
		items = append(items, p.mapping(ln.indent+2))
	}
	return items
}

func (p *yamlParser) pair(text string) (string, any) {
	k, v, ok := strings.Cut(text, ":")
	if !ok || strings.ContainsAny(k, " \"'") {
		p.t.Fatalf("not a key: value line: %q", text)
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return k, nil
	}
	return k, unquoteYAML(p.t, v)
}

func unquoteYAML(t *testing.T, v string) string {
	t.Helper()
	if strings.HasPrefix(v, `"`) {
		s, err := strconv.Unquote(v)
		if err != nil {
			t.Fatalf("bad quoted scalar %s: %v", v, err)
		}
		return s
	}
	if strings.HasPrefix(v, "[") || strings.HasPrefix(v, "{") || strings.HasPrefix(v, "'") {
		t.Fatalf("flow and single-quoted scalars are outside this parser: %s", v)
	}
	return v
}

// writeQuillmarshSessions writes n Claude transcripts, the newest first, each
// mentioning quillmarsh.
func writeQuillmarshSessions(t *testing.T, from, n int, newest time.Time) {
	t.Helper()
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := from; i < from+n; i++ {
		at := newest.Add(-time.Duration(i) * time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"quillmarsh rollout step %d, ticket zq%04d"},"timestamp":%q,"sessionId":"q%04d","cwd":"/proj"}`+"\n", i, i, at, i)
		if err := os.WriteFile(filepath.Join(store, fmt.Sprintf("q%04d.jsonl", i)), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// snapshotAt copies the index as it stands when the build says its newest
// slice is searchable — the index a SIGINT right after that line leaves.
type snapshotAt struct {
	t        *testing.T
	from, to string
	took     bool
}

func (s *snapshotAt) Write(p []byte) (int, error) {
	if !s.took && bytes.Contains(p, []byte("searchable now")) {
		s.took = true
		copyIndexTree(s.t, s.from, s.to)
	}
	return len(p), nil
}

func copyIndexTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		b, err := os.ReadFile(p) //nolint:gosec // copies a temp index tree the test built
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// After a big first build is cut off past its early slice, the index holds the
// newest sessions and is missing the oldest. The note said "the very newest
// sessions may not appear yet", the opposite of what was missing, and gave no
// count (#5390).
func TestAnInterruptedFirstBuildSaysHowMuchItCovers(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "1")
	writeQuillmarshSessions(t, 0, 450, time.Now().Add(-time.Hour))
	dir := filepath.Join(tmp, "index.db")
	cut := filepath.Join(tmp, "cut.db")
	shot := &snapshotAt{t: t, from: dir, to: cut}
	if err := index.Ensure(dir, "", false, shot); err != nil {
		t.Fatal(err)
	}
	if !shot.took {
		t.Fatal("the build published no early slice, so this proves nothing")
	}
	// The build that picks it up again holds the lock while it runs.
	unlock := holdIndexLock(t, cut)
	defer unlock()

	text, _, _, _, err := recallTextResult(cut, "quillmarsh rollout", "", 0, 0, recallMCPBudget)
	if err != nil {
		t.Fatal(err)
	}
	want := "covers only the 200 most recent of 450 sessions found; older ones are missing"
	if !strings.Contains(text, want) {
		t.Errorf("recall does not say what the cut-off index covers, want %q in:\n%s", want, text)
	}
	if strings.Contains(text, "very newest") {
		t.Errorf("recall says the newest are missing when it is the oldest:\n%s", text)
	}

	// A question only an old session answers misses, and the miss says why.
	miss, _, _, _, err := recallTextResult(cut, "zq0449", "", 0, 0, recallMCPBudget)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(miss, want) {
		t.Errorf("a miss on the cut-off index does not say the oldest are missing, want %q in:\n%s", want, miss)
	}

	var cli bytes.Buffer
	if err := ensureForCLISearch(cut, search.Options{Query: "quillmarsh", All: true}, false, &cli); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cli.String(), want) {
		t.Errorf("the CLI does not say what the cut-off index covers, want %q in:\n%s", want, cli.String())
	}
}

// During an ordinary refresh the note names the index's date and how many
// sessions it holds, rather than "the index as it was" (#5390).
func TestARefreshSaysTheIndexAgeAndSize(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "1")
	writeQuillmarshSessions(t, 0, 30, time.Now().Add(-time.Hour))
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	writeQuillmarshSessions(t, 30, 10, time.Now().Add(-time.Hour))
	unlock := holdIndexLock(t, dir)
	defer unlock()

	at := index.SnapshotCoverage(dir).At.Local().Format("2006-01-02 15:04")
	want := "answering from the index of " + at
	text, _, _, _, err := recallTextResult(dir, "quillmarsh rollout", "", 0, 0, recallMCPBudget)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, want) || !strings.Contains(text, "30 sessions") {
		t.Errorf("recall during a refresh gives no date or size, want %q and 30 sessions in:\n%s", want, text)
	}
	var cli bytes.Buffer
	if err := ensureForCLISearch(dir, search.Options{Query: "quillmarsh", All: true}, false, &cli); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cli.String(), want) || !strings.Contains(cli.String(), "30 sessions") {
		t.Errorf("the CLI during a refresh gives no date or size, want %q and 30 sessions in:\n%s", want, cli.String())
	}
}

// The line says what a walk found when there was one, and stays one line.
func TestTheCoverageLineCountsWhatIsPending(t *testing.T) {
	at := time.Now().Add(-3 * time.Minute)
	line := coverageLine(index.Coverage{Stale: true, At: at, Sessions: 7000, Pending: 3000, Found: 10000})
	for _, want := range []string{"the index of " + at.Local().Format("2006-01-02 15:04"), "3 minutes ago", "7000 sessions", "3000 of 10000 transcripts are new or changed since"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q missing from %q", want, line)
		}
	}
	if strings.Contains(line, "\n") {
		t.Errorf("the note is more than one line: %q", line)
	}
}

// An index that is behind and cannot be written stays behind. search names the
// state; the hook told the agent it "starts already knowing" a picture missing
// today's work, and MCP answered a confident negative about work that is on
// disk (#1005).
func TestTheAgentIsToldWhenTheIndexCannotCatchUp(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker window"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(tmp, "locked")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// Current and writable: nothing to say, on either surface.
	if note := staleReadOnlyNote(dir); note != "" {
		t.Errorf("a healthy index produced a warning: %q", note)
	}
	if got := emptyRecallAnswer(dir, "nothing"); !strings.Contains(got, "No prior deja sessions matched") {
		t.Errorf("the ordinary empty answer changed: %q", got)
	}

	// A session deja cannot index, and nowhere to write the result.
	newer := `{"type":"user","message":{"role":"user","content":"a session added after the parent was locked"},"timestamp":"2026-08-04T12:00:00Z","sessionId":"new","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "new.jsonl"), []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	note := staleReadOnlyNote(dir)
	if !strings.Contains(note, "cannot be updated") {
		t.Errorf("the hook says nothing about an index that cannot catch up: %q", note)
	}
	if !strings.Contains(note, parent) {
		t.Errorf("the note does not name what to change: %q", note)
	}
	got := emptyRecallAnswer(dir, "third session")
	if strings.Contains(got, "No prior deja sessions matched") {
		t.Errorf("MCP answers a confident negative over an index that cannot catch up: %q", got)
	}
	if !strings.Contains(got, "may be missing") {
		t.Errorf("the MCP answer does not say what it might be missing: %q", got)
	}

	// Writable again — even while stale — is the ordinary case: the build will
	// happen on the next command, so neither surface warns.
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if note := staleReadOnlyNote(dir); note != "" {
		t.Errorf("a stale but writable index warned anyway: %q", note)
	}
}

// The state where memory quietly stops growing: the index is behind the stores
// and the directory a rebuild writes into is read-only, so `deja index` fails
// the same way every time. Three surfaces have to agree — doctor's JSON state,
// its freshness line, and the session start, which serves what it has and says
// why it is not newer. Only the rendering was covered; the state itself was
// built by hand in a report struct rather than on disk.
func TestAStaleIndexNobodyCanRebuildIsNamedEverywhere(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(tmp, "store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(store, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	write := func(id, text string, hoursAgo int) {
		t.Helper()
		d := filepath.Join(root, "-work-app")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-time.Duration(hoursAgo) * time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":%q}}`, id, at, text)
		if err := os.WriteFile(filepath.Join(d, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("m0", "the retry budget on this project", 5)
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	// A session arrives, and then nowhere to put the rebuild.
	write("m9", "a brand new topic nobody has indexed", 1)
	if err := os.Chmod(store, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(store, 0o755) })

	raw, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Index struct {
			State string `json:"state"`
		} `json:"index"`
	}
	if err = json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("doctor --json is not JSON: %v", err)
	}
	if report.Index.State != "stale-readonly" {
		t.Errorf("index state = %q, want stale-readonly", report.Index.State)
	}

	human, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "cannot be written") {
		t.Errorf("the report does not say the index cannot be written:\n%s", doctorSection(human, "Index:"))
	}

	// And the session start serves what it has rather than going quiet.
	out := hookContextFor(t, dir, `{"hook_event_name":"SessionStart","cwd":"/work/app","session_id":"a1"}`)
	if !strings.Contains(out, "retry budget") {
		t.Errorf("the hook served nothing from an index it can still read:\n%s", out)
	}
	if !strings.Contains(out, "not writable") {
		t.Errorf("the hook does not say why the index is not newer:\n%s", out)
	}
}

// A decision that holds across a project is one fact, however many files it is
// said in front of. The line was deduped whole and its head names the file, so
// on a real store one accepted note reached 154 different files — a line on
// every edit of the session, all of it the same sentence.
func TestAStandingDecisionIsOneFact(t *testing.T) {
	first := "hook_tool.go has been worked on in 6 sessions, last 2026-09-10" +
		standingLabel + "the repository description leads with the task"
	second := "install.go has been worked on in 9 sessions, last 2026-09-10" +
		standingLabel + "the repository description leads with the task"
	if dedupeFact(first) != dedupeFact(second) {
		t.Errorf("the same standing decision counts twice:\n  %q\n  %q",
			dedupeFact(first), dedupeFact(second))
	}
	if !strings.Contains(dedupeFact(first), "repository description") {
		t.Errorf("the fact lost what it says: %q", dedupeFact(first))
	}
}

// A decision about a file is that file's, so two of them are two facts.
func TestTwoFileDecisionsAreTwoFacts(t *testing.T) {
	first := "render.go has been worked on in 6 sessions — prior decision: the renderer never re-wraps"
	second := "retry.go has been worked on in 6 sessions — prior decision: the retry budget stays at four"
	if dedupeFact(first) == dedupeFact(second) {
		t.Error("two different file decisions were counted as one fact")
	}
}

// One sentence, once per index: the install proof says it, and after that
// neither the proof nor the week note repeats it.
func TestStarLineIsSaidOncePerIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got := starLine(dir)
	if !strings.Contains(got, "https://github.com/vshulcz/deja-vu") {
		t.Fatalf("first call missing clickable URL: %q", got)
	}
	if !strings.Contains(got, "gh repo star vshulcz/deja-vu") {
		t.Fatalf("first call missing gh repo star command: %q", got)
	}
	if w := termwidth.Columns(got); w > noteWidth {
		t.Fatalf("first call is %d columns, want <= %d: %q", w, noteWidth, got)
	}
	if again := starLine(dir); again != "" {
		t.Fatalf("said twice: %q", again)
	}
	// The week note after an install that already said it stays as it was.
	usage.RecordResult(dir, usage.KindHook, 900, 2, false)
	now := time.Now()
	_ = weekNoteAt(dir, now)
	if got := weekNoteAt(dir, now.Add(8*24*time.Hour)); strings.Contains(got, "github.com") {
		t.Fatalf("week note repeated the star line: %q", got)
	}
}

// A package install has no proof, so the line waits in the session-start
// notes: never before the built note has been shown, once after it, and
// never alongside another note.
func TestSessionStartCarriesTheStarLineWhenTheInstallDidNot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := humanNotes(dir); got != "" {
		t.Fatalf("a machine with nothing indexed heard %q", got)
	}
	if _, err := os.Stat(dir + ".starnote"); err == nil {
		t.Fatal("the line was spent before anything was indexed")
	}
	// The built note has been shown.
	if err := os.WriteFile(dir+".builtnote", []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := humanNotes(dir); got != starText {
		t.Fatalf("the session after the built note = %q, want the GitHub line alone", got)
	}
	if again := humanNotes(dir); again != "" {
		t.Fatalf("said twice: %q", again)
	}
}

// The week note no longer carries it: a due week note and the line are two
// notes, and a session start shows one.
func TestWeekNoteGoesFirstAndTheStarLineWaits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+".builtnote", []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	usage.RecordResult(dir, usage.KindHook, 900, 2, false)
	old := strconv.FormatInt(time.Now().Add(-8*24*time.Hour).Unix(), 10)
	if err := os.WriteFile(dir+".weeknote", []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := humanNotes(dir); got != "deja this week: 1 recall — deja stats --card" {
		t.Fatalf("first session start = %q, want the week note alone", got)
	}
	if got := humanNotes(dir); got != starText {
		t.Fatalf("second session start = %q, want the GitHub line", got)
	}
}

// The card is meant to be pasted into a README, so "1 sessions" is read by
// everyone who visits the repo, not just its owner.
func TestCardPunchlineCountsOne(t *testing.T) {
	cases := []struct {
		name string
		r    stats.Report
		want string
	}{
		{"sessions", stats.Report{TotalSessions: 1}, "1 session of agent history, all searchable."},
		{"week recalls", stats.Report{WeekRecalls: 1}, "deja handed your agents memory 1 time this week."},
		{"repeat questions", stats.Report{RepeatQuestions: 1}, "You asked 1 question more than once — deja remembered."},
		{"recalls", stats.Report{Recall: usage.Summary{Recalls: 1}}, "deja handed your agents memory 1 time."},
	}
	for _, c := range cases {
		if got := cardPunchline(c.r); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCardPunchlineKeepsPluralAboveOne(t *testing.T) {
	if got := cardPunchline(stats.Report{TotalSessions: 4}); !strings.Contains(got, "4 sessions") {
		t.Fatalf("got %q", got)
	}
	if got := cardPunchline(stats.Report{WeekRecalls: 9}); !strings.Contains(got, "9 times") {
		t.Fatalf("got %q", got)
	}
}
