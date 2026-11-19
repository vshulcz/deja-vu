package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode"
)

// A JSON document or a long file listing in tool output names the file as one
// entry among many. Read as history it was quoted as the session's excerpt and
// every path in it counted as a mention: on a real store the top blame row for
// cmd/deja/mcp.go had 1113 mentions, both excerpts raw JSON from an agent
// running deja's own --json commands (#4776).
func TestBlameSkipsDataDumpsInToolOutput(t *testing.T) {
	now := time.Now().UTC()
	target := BlameTarget{FullPath: "/work/app/internal/pool/pool.go", Base: "pool.go", Stem: "pool"}
	jsonDump := `[{"session": {"id": "x1", "harness": "claude", "project": "app", "path": "/home/u/.claude/projects/-work-app/x1.jsonl", "touched": ["/work/app/internal/pool/pool.go", "/work/app/cmd/main.go"]}, "score": 3.2, "tier": "exact"}]`
	// Cut off mid-document, the way a harness truncates long tool output.
	truncated := strings.Repeat(`{"path":"/work/app/internal/pool/pool.go","count":3},`, 40)[:900]
	var listing strings.Builder
	for i := range 12 {
		fmt.Fprintf(&listing, " M internal/pkg%02d/file%02d.go\n", i, i)
	}
	listing.WriteString(" M internal/pool/pool.go\n")

	dumps := model.Session{
		Harness: "claude", ID: "dump", Project: "app", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "check what deja knows", Time: now},
			{Role: sources.RoleToolOutput, Text: jsonDump, Time: now},
			{Role: sources.RoleToolOutput, Text: "Web search results for query: \"pool.go\"\n\nLinks: " + jsonDump, Time: now},
			{Role: sources.RoleToolOutput, Text: strings.Repeat("ses_1|2026-05-25|/work/app|pool work|{\"role\":\"user\",\"time\":{\"created\":1},\"summary\":{\"diffs\":[{\"file\":\"internal/pool/pool.go\",\"additions\":3}]}}\n", 3), Time: now},
			// deja's stderr line lands ahead of its own --json.
			{Role: sources.RoleToolOutput, Text: "deja: updated 2 files (5 new messages)\n" + jsonDump, Time: now},
			{Role: sources.RoleToolOutput, Text: "[" + truncated, Time: now},
			{Role: sources.RoleToolOutput, Text: listing.String(), Time: now},
		},
	}
	worked := model.Session{
		Harness: "claude", ID: "worked", Project: "app", Updated: now.Add(-48 * time.Hour),
		Messages: []model.Message{
			{Role: "user", Text: "why is the pool in internal/pool/pool.go capped at 50?", Time: now},
			{Role: "assistant", Text: "internal/pool/pool.go caps at 50 because the replicas starved above that", Time: now},
		},
	}
	hits := Blame([]model.Session{dumps, worked}, target, BlameOptions{All: true})
	for _, h := range hits {
		if h.Session.ID == "dump" {
			t.Errorf("a session that only saw the file in data dumps is in the answer: %d mentions, excerpts %q", h.Count, h.Snippets)
		}
	}
	if len(hits) == 0 || hits[0].Session.ID != "worked" {
		t.Fatalf("the session that worked on the file is not first: %+v", hits)
	}

	// Controls: tool output that talks about the file is still evidence — an
	// error naming it, and a short listing of what one command touched.
	evidence := model.Session{
		Harness: "claude", ID: "err", Project: "app", Updated: now,
		Messages: []model.Message{
			{Role: sources.RoleToolOutput, Text: "internal/pool/pool.go:42:3: undefined: maxConns", Time: now},
			{Role: sources.RoleToolOutput, Text: " M internal/pool/pool.go\n M internal/pool/pool_test.go\n", Time: now},
		},
	}
	got := Blame([]model.Session{evidence}, target, BlameOptions{All: true})
	if len(got) != 1 || got[0].Count != 2 {
		t.Fatalf("tool output about the file stopped counting: %+v", got)
	}

	// A compiler or linter reporting on a dozen files is a report: each line
	// says something about a line of a file.
	var vet strings.Builder
	for i := range 12 {
		fmt.Fprintf(&vet, "internal/pkg%02d/file%02d.go:%d:2: unused variable x\n", i, i, i+3)
	}
	vet.WriteString("internal/pool/pool.go:88:2: unused variable maxConns\n")
	// And one small JSON object is an API's error body, not a dump.
	small := model.Session{
		Harness: "claude", ID: "small", Project: "app", Updated: now,
		Messages: []model.Message{
			{Role: sources.RoleToolOutput, Text: vet.String(), Time: now},
			{Role: sources.RoleToolOutput, Text: `{"error": "Failed: \"internal/pool/pool.go\" exceeds the size limit", "code": 413}`, Time: now},
		},
	}
	if got := Blame([]model.Session{small}, target, BlameOptions{All: true}); len(got) != 1 || got[0].Count != 2 {
		t.Fatalf("a linter report or a small error object was read as a dump: %+v", got)
	}

	// Output quoting a small object while saying something is not a dump.
	quoted := model.Session{
		Harness: "claude", ID: "quoted", Project: "app", Updated: now,
		Messages: []model.Message{{Role: sources.RoleToolOutput, Text: `config {"pool": 50} rejected by internal/pool/pool.go: the replicas accept at most 40 connections`, Time: now}},
	}
	if got := Blame([]model.Session{quoted}, target, BlameOptions{All: true}); len(got) != 1 {
		t.Fatalf("an error quoting a small object was read as a dump: %+v", got)
	}

	// A deep stack trace names as many files as a listing, alternated with the
	// code that ran: that is history of the failure, not a dump.
	var trace strings.Builder
	trace.WriteString("panic: pool exhausted\n\ngoroutine 1 [running]:\n")
	for i := range 12 {
		fmt.Fprintf(&trace, "app/internal/pkg%02d.Call(...)\n\t/work/app/internal/pkg%02d/call.go:%d +0x1c\n", i, i, 10+i)
	}
	trace.WriteString("app/internal/pool.(*Pool).Get(...)\n\t/work/app/internal/pool/pool.go:88 +0x2a\n")
	traced := model.Session{
		Harness: "claude", ID: "trace", Project: "app", Updated: now,
		Messages: []model.Message{{Role: sources.RoleToolOutput, Text: trace.String(), Time: now}},
	}
	if got := Blame([]model.Session{traced}, target, BlameOptions{All: true}); len(got) != 1 {
		t.Fatalf("a stack trace through the file was read as a dump: %+v", got)
	}
}

// Among sessions that named the file as a path, the listing reads newest
// first. By score, one that repeated the path more sat above a newer one, and
// the dates ran 06-29, 06-27, 06-28.
func TestBlameListsPathSessionsNewestFirst(t *testing.T) {
	target := BlameTarget{FullPath: "/repo/internal/pool/pool.go", Base: "pool.go", Stem: "pool"}
	day := func(d int) time.Time { return time.Date(2026, 6, d, 10, 0, 0, 0, time.UTC) }
	say := func(n int) []model.Message {
		ms := make([]model.Message, 0, n)
		for range n {
			ms = append(ms, model.Message{Role: "assistant", Text: "changed internal/pool/pool.go to retire idle connections sooner"})
		}
		return ms
	}
	ss := []model.Session{
		{ID: "older-busier", Harness: "claude", Project: "/repo", Updated: day(27), Messages: say(4)},
		{ID: "newest", Harness: "claude", Project: "/repo", Updated: day(29), Messages: say(1)},
		{ID: "middle", Harness: "claude", Project: "/repo", Updated: day(28), Messages: say(1)},
	}
	hits := Blame(ss, target, BlameOptions{All: true})
	got := make([]string, 0, len(hits))
	for _, h := range hits {
		got = append(got, h.Session.ID)
	}
	want := []string{"newest", "middle", "older-busier"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}

// An edit record is "path\nspan" and the two were joined with a space, so the
// line read as one path with rubbish after it — on the command whose output
// people paste into `deja restore <path>`, and which deliberately handles paths
// containing spaces (#2284).
func TestAnEditSnippetMarksWhereThePathEnds(t *testing.T) {
	target := BlameTarget{Base: "upload.go", FullPath: "/work/app/api/upload.go"}
	got := blameSnippet("/work/app/api/upload.go\nretryBudget := 3 // three is plenty", "edit", target)

	// The premise: both halves are still there, on one line.
	if !strings.HasPrefix(got, "/work/app/api/upload.go") || !strings.Contains(got, "retryBudget") {
		t.Fatalf("the snippet lost one of its halves: %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("the snippet broke into two lines: %q", got)
	}
	if strings.HasPrefix(got, "/work/app/api/upload.go retryBudget") {
		t.Errorf("nothing marks where the path ends: %q", got)
	}
	if !strings.Contains(got, "/work/app/api/upload.go — ") {
		t.Errorf("the separator is missing: %q", got)
	}

	// A path with spaces still arrives whole, which is what #2044 was about.
	spaces := BlameTarget{Base: "two  spaces.go", FullPath: "/tmp/app/two  spaces.go"}
	got = blameSnippet("/tmp/app/two  spaces.go\nsize = 20", "edit", spaces)
	if !strings.HasPrefix(got, "/tmp/app/two  spaces.go — ") {
		t.Errorf("a path holding two spaces lost them or its separator: %q", got)
	}

	// An edit with no span left is just the path, with no separator dangling.
	got = blameSnippet("/work/app/api/upload.go\n", "edit", target)
	if strings.Contains(got, "—") {
		t.Errorf("an edit with no span printed a bare separator: %q", got)
	}
}

// docs/json-output.md promises `deja blame --json` returns a stable array whose
// element shape only grows additively. It lists no fields, so this snapshot is
// the record of that shape: a renamed or removed field fails here, and a new
// one fails too so the addition is a conscious act, not a silent break.
func TestBlameHitShapeIsStable(t *testing.T) {
	want := map[string]bool{
		"session": true, "title": true, "count": true, "snippets": true,
		"score": true, "specificity": true, "tier": true,
		"lifecycle": true, "lifecycle_note": true, "lifecycle_at": true,
		// The bound on what a hit carries, and the count it was bounded
		// against: an answer used to be the size of the reader's longest
		// transcript (#3620).
		"messages_total": true, "messages_capped": true,
	}
	got := map[string]bool{}
	rt := reflect.TypeFor[BlameHit]()
	for i := range rt.NumField() {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			got[name] = true
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("BlameHit gained field %q — document it in docs/json-output.md and add it here", k)
		}
	}
	for k := range want {
		if !got[k] {
			t.Errorf("BlameHit lost field %q — a breaking change to the blame --json contract", k)
		}
	}
}

// The line says what happened rather than naming the state, so a hit that was
// taken back reads as taken back in blame too (#1019).
func TestBlameLifecycleLineWordsWhatHappened(t *testing.T) {
	for _, tc := range []struct {
		name string
		hit  BlameHit
		want string
	}{
		{"none", BlameHit{}, ""},
		{"rejected", BlameHit{Lifecycle: "rejected"}, "this was tried and rejected"},
		{"superseded", BlameHit{Lifecycle: "superseded"}, "a later decision replaced this"},
		{"stale", BlameHit{Lifecycle: "stale"}, "marked stale — may no longer hold"},
		{"unknown state passes through", BlameHit{Lifecycle: "parked"}, "parked"},
		{
			"with date and note",
			BlameHit{Lifecycle: "rejected", LifecycleAt: "2026-08-01", LifecycleNote: "deadlocked under load"},
			"this was tried and rejected (2026-08-01): deadlocked under load",
		},
		{
			"date without note",
			BlameHit{Lifecycle: "stale", LifecycleAt: "2026-07-04"},
			"marked stale — may no longer hold (2026-07-04)",
		},
	} {
		if got := BlameLifecycleLine(tc.hit); got != tc.want {
			t.Errorf("%s: line = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// `path:line` has always been accepted and the number thrown away; the
// line-level answer needs it (#1181).
func TestCutLineSuffixKeepsTheLine(t *testing.T) {
	for _, tc := range []struct {
		in   string
		path string
		line int
	}{
		{"internal/index/fixpair.go:60", "internal/index/fixpair.go", 60},
		{"internal/index/fixpair.go:60:14", "internal/index/fixpair.go", 60},
		{"internal/index/fixpair.go", "internal/index/fixpair.go", 0},
		// A zero is not a line, and the suffix was already stripped before this
		// existed: the path is what is left, which is what blame has always
		// looked up.
		{"pool.go:0", "pool.go", 0},
		{"C:/work/pool.go", "C:/work/pool.go", 0},
		{"weird:name.go", "weird:name.go", 0},
	} {
		path, line, _ := cutLineSuffix(tc.in)
		if path != tc.path || line != tc.line {
			t.Errorf("cutLineSuffix(%q) = %q, %d; want %q, %d", tc.in, path, line, tc.path, tc.line)
		}
	}
	// And the resolver carries it through.
	target, err := ResolveBlamePath("internal/index/fixpair.go:60")
	if err != nil || target.Line != 60 || target.Base != "fixpair.go" {
		t.Fatalf("target = %+v err = %v", target, err)
	}
}

// `a.txt:99` says "the file has 3 lines" (#3726). `a.txt:0` and `a.txt:abc`
// said nothing and answered about the file as though no line had been asked
// for — the same silence, one case earlier (#3738).

func TestALineSpecThatCannotBeUsedIsSaidOutLoud(t *testing.T) {
	for _, tc := range []struct {
		in   string
		path string
		line int
		note string
	}{
		// Digits trimLineSuffix accepted, that name no line.
		{"a.txt:0", "a.txt", 0, "there is no line 0"},
		{"a.txt:99999999999999999999", "a.txt", 0, "too large to be a line number"},
		// Not digits. The note says the answer is for the whole file, so the
		// file is what is searched: kept whole, `pool.go:abc` matched nothing.
		{"a.txt:abc", "a.txt", 0, `"abc" is not a line number`},
		{"a.txt:-1", "a.txt", 0, `"-1" is not a line number`},
		{"a.txt:+3", "a.txt", 0, `"+3" is not a line number`},
		{"a.txt:2.5", "a.txt", 0, `"2.5" is not a line number`},
	} {
		path, line, note := cutLineSuffix(tc.in)
		if path != tc.path || line != tc.line {
			t.Errorf("cutLineSuffix(%q) = %q, %d; want %q, %d", tc.in, path, line, tc.path, tc.line)
		}
		if !strings.Contains(note, tc.note) {
			t.Errorf("cutLineSuffix(%q) note = %q, want it to contain %q", tc.in, note, tc.note)
		}
	}
}

// A spec that works, and a path with no spec at all, have nothing to say.
func TestAUsableLineSpecSaysNothing(t *testing.T) {
	for _, in := range []string{
		"a.txt:12",
		"a.txt:12:5",
		"a.txt",
		"internal/index/fixpair.go:60",
	} {
		if _, _, note := cutLineSuffix(in); note != "" {
			t.Errorf("cutLineSuffix(%q) note = %q, want silence", in, note)
		}
	}
}

// The note must not fire on a colon that belongs to the filename. It is
// narrow on purpose: the part before the colon has to look like a filename,
// and the part after must hold no separator.
func TestAColonInAFilenameIsNotAMistypedLine(t *testing.T) {
	for _, in := range []string{
		"weird:name.go",
		"notes:draft",
		`C:\work\pool.go`,
		"C:/work/pool.go",
		"a.txt:",
	} {
		path, line, note := cutLineSuffix(in)
		if note != "" {
			t.Errorf("cutLineSuffix(%q) note = %q, want silence", in, note)
		}
		// And the path is still the whole string, as it has always been.
		if path != in || line != 0 {
			t.Errorf("cutLineSuffix(%q) = %q, %d; want the string unchanged", in, path, line)
		}
	}
}

// A file that really is named with the colon is taken as written.
func TestAnExistingFileWithAColonIsTakenAsWritten(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a colon cannot be in a Windows filename")
	}
	dir := t.TempDir()
	name := filepath.Join(dir, "a.txt:abc")
	if err := os.WriteFile(name, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, line, note := cutLineSuffix(name)
	if path != name || line != 0 || note != "" {
		t.Errorf("cutLineSuffix(%q) = %q, %d, %q; want it as written", name, path, line, note)
	}
}

// The resolver carries it, which is how the command gets to say it.
func TestResolveBlamePathCarriesTheNote(t *testing.T) {
	target, err := ResolveBlamePath("a.txt:0")
	if err != nil {
		t.Fatalf("ResolveBlamePath: %v", err)
	}
	if target.Line != 0 || !strings.Contains(target.LineNote, "no line 0") {
		t.Fatalf("target = %+v", target)
	}
	if target.Base != "a.txt" {
		t.Fatalf("base = %q, want the file the answer is about", target.Base)
	}

	clean, err := ResolveBlamePath("a.txt:12")
	if err != nil || clean.Line != 12 || clean.LineNote != "" {
		t.Fatalf("a usable spec should carry no note: %+v err = %v", clean, err)
	}
}

// What a reader has in the clipboard is `file:line` — from a stack trace, a
// compiler error, a linter, an editor's copy-reference, or deja's own lines.
// Blame took it literally, made `search.go:266` the basename, and reported that
// no session mentions the file (#1625).
func TestResolveBlamePathDropsALineNumber(t *testing.T) {
	for _, in := range []string{
		"internal/search/search.go:266",
		"internal/search/search.go:266:12",
		"/tmp/api/internal/search/search.go:266",
		"search.go:1",
	} {
		got, err := ResolveBlamePath(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got.Base != "search.go" || got.Stem != "search" {
			t.Errorf("%s resolved to base %q stem %q, want search.go/search", in, got.Base, got.Stem)
		}
	}
	// Extensionless names are the common case for this shape: make, docker and
	// just all report `Makefile:12`.
	for _, in := range []string{"Makefile:12", "Dockerfile:7", "justfile:3:9"} {
		got, err := ResolveBlamePath(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if strings.ContainsRune(got.Base, ':') {
			t.Errorf("%s kept its line number: base %q", in, got.Base)
		}
	}
	// The controls: a plain path is untouched, and a colon that is part of the
	// name — or a Windows drive letter — must survive.
	for in, want := range map[string]string{
		"internal/search/search.go": "search.go",
		"weird:name.go":             "weird:name.go",
		"notes:2026.md":             "notes:2026.md",
		"search.go:":                "search.go:",
	} {
		got, err := ResolveBlamePath(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got.Base != want {
			t.Errorf("%s resolved to base %q, want %q", in, got.Base, want)
		}
	}
}

// A drive letter and a UNC prefix carry colons and backslashes that must reach
// the matcher intact, whichever platform the binary was built for (#1625).
func TestResolveBlamePathKeepsWindowsShapes(t *testing.T) {
	for in, want := range map[string]string{
		`C:\src\main.go:266`:     "main.go",
		`C:\src\main.go`:         "main.go",
		`\\server\share\a.go:12`: "a.go",
	} {
		got, err := ResolveBlamePath(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		// On unix these are one path element; what matters either way is that
		// the drive letter is not eaten and the line number is.
		if strings.Contains(got.Base, ":266") || strings.Contains(got.Base, ":12") {
			t.Errorf("%s kept its line number: base %q", in, got.Base)
		}
		if !strings.Contains(got.Base, want) {
			t.Errorf("%s lost the file name: base %q, want it to contain %q", in, got.Base, want)
		}
	}
}

// The collision this fix accepts, written down so it is a decision and not a
// surprise: a file literally named `a:1` is read as `a` line 1. Nothing tells
// the two apart from the string alone, and the shape that reaches this command
// is overwhelmingly `Makefile:12`, not a file whose name ends in a colon and a
// digit.
func TestALiteralNameEndingInColonDigitsIsReadAsALineNumber(t *testing.T) {
	got, err := ResolveBlamePath("a:1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Base != "a" {
		t.Errorf("base %q; the documented trade-off is that this reads as a line number", got.Base)
	}
}

func TestResolveBlamePathParsesLineRangesAndAnchors(t *testing.T) {
	for _, tc := range []struct {
		in       string
		wantBase string
		wantLine int
		wantEnd  int
	}{
		{"internal/search/recall.go:65-95", "recall.go", 65, 95},
		{"internal/search/recall.go#L65-L95", "recall.go", 65, 95},
		{"internal/search/recall.go#L42", "recall.go", 42, 42},
	} {
		got, err := ResolveBlamePath(tc.in)
		if err != nil {
			t.Fatalf("ResolveBlamePath(%q): %v", tc.in, err)
		}
		if got.Base != tc.wantBase || got.Line != tc.wantLine || got.EndLine != tc.wantEnd || got.LineNote != "" {
			t.Errorf("ResolveBlamePath(%q) = {Base:%q Line:%d EndLine:%d Note:%q}, want {Base:%q Line:%d EndLine:%d Note:\"\"}",
				tc.in, got.Base, got.Line, got.EndLine, got.LineNote, tc.wantBase, tc.wantLine, tc.wantEnd)
		}
	}
}

// blame answers "who decided this", and a promoted note mentions the file once
// where the transcript it was made from mentions it many times — so the score
// put the transcript above its own note on every blame (#2829, the surface
// #2803 did not reach).
func TestBlamePutsANoteAboveItsSource(t *testing.T) {
	now := time.Now()
	source := model.Session{
		Harness: "claude", ID: "longs", Project: "api", Updated: now,
		Messages: []model.Message{
			{Role: "files", Text: "/work/api/pool.go", Time: now},
			{Role: "user", Text: "pool.go deadlocks under load", Time: now},
			{Role: "assistant", Text: "pool.go needs a bigger max", Time: now},
			{Role: "assistant", Text: "raised it in pool.go", Time: now},
		},
	}
	note := model.Session{
		Harness: "deja", ID: "deja-note-claude-longs", Project: "api", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "[accepted] the pool in pool.go was too small", Time: now},
		},
	}

	target := BlameTarget{FullPath: "/work/api/pool.go", Base: "pool.go", Stem: "pool"}
	hits := Blame([]model.Session{source, note}, target, BlameOptions{All: true})
	var noteAt, sourceAt = -1, -1
	for i, h := range hits {
		switch h.Session.ID {
		case "deja-note-claude-longs":
			noteAt = i
		case "longs":
			sourceAt = i
		}
	}
	if noteAt < 0 || sourceAt < 0 {
		t.Fatalf("both should be in the answer, got %d hits", len(hits))
	}
	if noteAt > sourceAt {
		t.Errorf("blame put the transcript above the note made from it")
	}
}

// Naming the path is evidence; naming it once and saying nothing else is not.
// A pasted absolute path and a `git diff --stat` row each mention the file once
// and outranked the session that debugged it, because the rule that puts a path
// first asked nothing about how much the session had to say (#2854).
func TestOneEchoOfThePathDoesNotOutrankAWorkingSession(t *testing.T) {
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	target := BlameTarget{FullPath: "/work/api/cmd/deja/mcp.go", Base: "mcp.go", Stem: "mcp"}

	echo := model.Session{
		Harness: "opencode", ID: "echo", Project: "api", Updated: now,
		Messages: []model.Message{{
			Role: "user", Time: now,
			Text: " cmd/deja/mcp.go | 4 +-",
		}},
	}
	worked := model.Session{
		Harness: "claude", ID: "worked", Project: "api", Updated: now.Add(-24 * time.Hour),
		Messages: []model.Message{{
			Role: "assistant", Time: now.Add(-24 * time.Hour),
			Text: "mcp.go is where the budget trim happens; mcp.go cuts from the tail, and mcp.go was what broke the windows build",
		}},
	}
	hits := Blame([]model.Session{echo, worked}, target, BlameOptions{All: true})
	if len(hits) != 2 {
		t.Fatalf("both sessions name the file: %d hits", len(hits))
	}
	if hits[0].Session.ID != "worked" {
		t.Errorf("one echo of the path outranked the session that worked on the file: %s first", hits[0].Session.ID)
	}

	// And a session that names the path *and* has something to say still comes
	// first: the rule is about evidence, not about punishing paths.
	saidMore := model.Session{
		Harness: "claude", ID: "said_more", Project: "api", Updated: now.Add(-48 * time.Hour),
		Messages: []model.Message{{
			Role: "assistant", Time: now.Add(-48 * time.Hour),
			Text: "cmd/deja/mcp.go holds the budget; cmd/deja/mcp.go trims from the tail",
		}},
	}
	hits = Blame([]model.Session{echo, worked, saidMore}, target, BlameOptions{All: true})
	if hits[0].Session.ID != "said_more" {
		t.Errorf("the session that named the path twice did not come first: %s", hits[0].Session.ID)
	}
}

// A real diffstat carries its own summary line, so asking the whole message
// whether it says anything let every path above that line ride on it — the
// question is about the line the path is on (#2854).
func TestADiffstatDoesNotVouchForItsOwnRows(t *testing.T) {
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	target := BlameTarget{FullPath: "/work/api/cmd/deja/mcp.go", Base: "mcp.go", Stem: "mcp"}
	diffstat := model.Session{
		Harness: "opencode", ID: "diffstat", Project: "api", Updated: now,
		Messages: []model.Message{{
			Role: "user", Time: now,
			Text: " cmd/deja/mcp.go      | 4 +-\n cmd/deja/recall.go   | 2 +-\n 2 files changed, 6 insertions(+), 2 deletions(-)",
		}},
	}
	worked := model.Session{
		Harness: "claude", ID: "worked", Project: "api", Updated: now.Add(-24 * time.Hour),
		Messages: []model.Message{{
			Role: "assistant", Time: now.Add(-24 * time.Hour),
			Text: "mcp.go is where the budget trim happens; mcp.go cuts from the tail, and mcp.go broke the windows build",
		}},
	}
	hits := Blame([]model.Session{diffstat, worked}, target, BlameOptions{All: true})
	if len(hits) != 2 || hits[0].Session.ID != "worked" {
		t.Errorf("a diffstat outranked the session that worked on the file: %+v", hits)
	}
}

// And a session that says what it did in a language with no Latin letters is
// saying it: the words were counted as ASCII only.
func TestWordsAreCountedInAnyScript(t *testing.T) {
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	target := BlameTarget{FullPath: "/work/api/cmd/deja/mcp.go", Base: "mcp.go", Stem: "mcp"}
	said := model.Session{
		Harness: "claude", ID: "said", Project: "api", Updated: now.Add(-48 * time.Hour),
		Messages: []model.Message{{
			Role: "assistant", Time: now.Add(-48 * time.Hour),
			Text: "починил cmd/deja/mcp.go — падал на виндах",
		}},
	}
	bare := model.Session{
		Harness: "claude", ID: "bare", Project: "api", Updated: now,
		Messages: []model.Message{{
			Role: "user", Time: now,
			Text: "mcp.go mcp.go mcp.go again",
		}},
	}
	hits := Blame([]model.Session{said, bare}, target, BlameOptions{All: true})
	if len(hits) != 2 || hits[0].Session.ID != "said" {
		t.Errorf("a session that said what it did was not read as saying it: %+v", hits)
	}
}

// Chinese and Japanese put no spaces between words, so counting fields counted
// a whole sentence as one and the rule read it as saying nothing.
func TestAScriptWithoutSpacesStillCounts(t *testing.T) {
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	target := BlameTarget{FullPath: "/work/api/cmd/deja/mcp.go", Base: "mcp.go", Stem: "mcp"}
	for _, text := range []string{
		"我修好了 cmd/deja/mcp.go 的问题",
		"cmd/deja/mcp.go のバグを直した",
	} {
		said := model.Session{
			Harness: "claude", ID: "said", Project: "api", Updated: now.Add(-48 * time.Hour),
			Messages: []model.Message{{Role: "assistant", Time: now.Add(-48 * time.Hour), Text: text}},
		}
		bare := model.Session{
			Harness: "claude", ID: "bare", Project: "api", Updated: now,
			Messages: []model.Message{{Role: "user", Time: now, Text: "mcp.go mcp.go mcp.go again"}},
		}
		hits := Blame([]model.Session{said, bare}, target, BlameOptions{All: true})
		if len(hits) != 2 || hits[0].Session.ID != "said" {
			t.Errorf("%q was read as saying nothing: %+v", text, hits)
		}
	}
}

// A transcript that ran deja keeps its output, and every line of the line-level
// answer names the file it was asked about — so blame ranked its own past
// answer as the history of the file and quoted it back. #1330 closed that for
// the shapes that existed then; the line answer is a new one (#1181).
func TestBlameDoesNotQuoteItsOwnLineAnswer(t *testing.T) {
	now := time.Now().UTC()
	target := BlameTarget{FullPath: "/work/app/internal/pool.go", Base: "pool.go", Stem: "pool"}
	own := strings.Join([]string{
		"pool.go:3 last changed in 33ec9160 · 2026-09-18 · fix: one pool per shard",
		"  written in claude · 2915986c-9f7 · app",
		"  replaced: var conns = newPool(shardCount * connsPerShard)",
		"  why, in full: deja ctx 2915986c-9f7",
	}, "\n")
	s := model.Session{
		Harness: "claude", ID: "s1", Project: "app", Updated: now,
		Messages: []model.Message{
			{Role: sources.RoleToolOutput, Text: own, Time: now},
			{Role: "assistant", Text: "internal/pool.go keeps one connection per shard because the driver pools per process", Time: now.Add(time.Second)},
		},
	}
	hits := Blame([]model.Session{s}, target, BlameOptions{All: true})
	if len(hits) != 1 {
		t.Fatalf("want the one session, got %d", len(hits))
	}
	joined := strings.Join(hits[0].Snippets, " | ")
	if strings.Contains(joined, "last changed in") || strings.Contains(joined, "why, in full") {
		t.Errorf("blame quoted its own answer back: %q", joined)
	}
	if !strings.Contains(joined, "one connection per shard") {
		t.Errorf("the sentence that says why is missing: %q", joined)
	}
}

// An agent exercising deja from a shell writes deja's own output into its
// transcript, and every line of a blame report names the file it is about. So
// blame ranked its own answer as that file's history: measured on this
// repository's sources, two of eighty-two snippets came back as
// `=== deja blame internal/index/retrieval.go ===` and the report under it.
//
// The same rule the report guard and the fix miner already apply (#2067,
// #2068, #2169).
func TestBlameDoesNotQuoteItsOwnOutput(t *testing.T) {
	now := "2026-01-02T03:04:05Z"
	_ = now
	sessions := []model.Session{{
		ID: "shell", Harness: "claude", Project: "app",
		Messages: []model.Message{
			{Role: "user", Text: "check the tool"},
			{Role: "tool-output", Text: "=== deja blame internal/index/retrieval.go ===\n" +
				"2026-07-18 · claude · file-00 · have a look at internal/index/retrieval.go"},
		},
	}, {
		ID: "real", Harness: "claude", Project: "app",
		Messages: []model.Message{
			{Role: "user", Text: "internal/index/retrieval.go keeps losing the idf map between passes"},
			{Role: "assistant", Text: "Fixed: internal/index/retrieval.go now returns it with the ranking."},
		},
	}}
	target, err := ResolveBlamePath("internal/index/retrieval.go")
	if err != nil {
		t.Fatal(err)
	}
	hits := Blame(sessions, target, BlameOptions{All: true})
	for _, h := range hits {
		for _, s := range h.Snippets {
			if strings.Contains(s, "=== deja ") {
				t.Errorf("blame quoted its own output: %q", strings.TrimSpace(s)[:min(len(s), 80)])
			}
		}
		// The report body is still matched — only the command echo is
		// dropped — so such a session can still appear. What it may not do is
		// hand deja's own words back as the file's history, which is the line
		// a reader acts on.
	}
	found := false
	for _, h := range hits {
		if h.Session.ID == "real" {
			found = true
		}
	}
	if !found {
		t.Error("the session that actually discussed the file was dropped")
	}
}

// The report body is left alone: `deja: ` opens deja's own messages to a
// terminal, and it is also how a person writes about deja.
func TestBlameKeepsALineThatMerelyNamesDeja(t *testing.T) {
	if withoutOwnReport("deja: no sessions mention nope.go") == "" {
		t.Error("a line addressed by deja was dropped from matching")
	}
	if got := withoutOwnReport("=== deja blame x.go ===\nkept"); strings.Contains(got, "=== deja") {
		t.Errorf("the command echo survived: %q", got)
	}
}

// Every new shape deja prints that names a file has to arrive with its
// recogniser, or blame reads its own answer back as the file's history. Three
// shapes came after the first pass: the silence sentence was rewritten when the
// written side landed and the recogniser kept pinning the old wording, the line
// answer can now be asked for as JSON, and `--git-note` puts the same
// attribution where `git log --notes=deja` prints it into a transcript (#3723,
// #3773, #1330).
func TestBlameDoesNotQuoteTheShapesThatCameAfterTheFirstPass(t *testing.T) {
	now := time.Now().UTC()
	target := BlameTarget{FullPath: "/work/app/internal/pool.go", Base: "pool.go", Stem: "pool"}
	for _, tc := range []struct {
		name string
		own  string
		mark string
	}{
		{
			// The answer for a line nothing is attributed to says "deja"
			// nowhere, and the cheap check that decides whether to filter a
			// message at all was looking for that word — so this shape, which
			// names the file in its first line, was never filtered.
			name: "the answer that never says deja",
			own:  "pool.go:3 last changed in 33ec9160 · 2026-09-18 · fix: one pool per shard\n  no indexed session wrote this line or the lines this commit replaced — nothing to say about this line",
			mark: "last changed in",
		},
		{
			name: "the line answer as JSON",
			own:  `{"kind":"deja.blame-line","schema_version":2,"file":"pool.go","line":3,"commit":{"sha":"33ec9160"},"attributed":true,"rule":"wrote","matched":"var conns = newPool(shardCount)","session":{"harness":"claude","id":"2915986c","project":"app","ctx":"deja ctx 2915986c"}}`,
			mark: "deja.blame-line",
		},
		{
			// The quoted turn under a line answer: the label is deja's, the
			// sentence after it is a transcript's, and a session that ran
			// blame keeps both.
			name: "the turn the line answer quotes",
			own:  "pool.go:3 written in claude · 2915986c · app\n  said just before this edit: internal/pool.go opened a connection per shard and the shard count moves at runtime",
			mark: "said just before this edit",
		},
		{
			name: "the note git log prints",
			own:  "deja: pool.go:3 written in claude · 2915986c · app (rule: replaced)\nwhy, in full: deja ctx 2915986c",
			mark: "written in claude",
		},
		{
			name: "the user turn origin quotes",
			own:  "  user turn (2m before edit): fix internal/pool/pool.go connection leak\n  user turn before edit: fix internal/pool/pool.go connection leak",
			mark: "before edit",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := model.Session{
				Harness: "claude", ID: "s1", Project: "app", Updated: now,
				Messages: []model.Message{
					{Role: sources.RoleToolOutput, Text: tc.own, Time: now},
					{Role: "assistant", Text: "internal/pool.go keeps one connection per shard because the driver pools per process", Time: now.Add(time.Second)},
				},
			}
			hits := Blame([]model.Session{s}, target, BlameOptions{All: true})
			if len(hits) != 1 {
				t.Fatalf("want the one session, got %d", len(hits))
			}
			joined := strings.Join(hits[0].Snippets, " | ")
			if strings.Contains(joined, tc.mark) {
				t.Errorf("blame quoted its own answer back: %q", joined)
			}
			if !strings.Contains(joined, "one connection per shard") {
				t.Errorf("the sentence that says why is missing: %q", joined)
			}
		})
	}
}

// The quotes under a blame hit are the evidence a reader judges it by, so they
// have to be the messages that say something about the file. Taking the first
// two mentions led with whichever line named it earliest.
func TestBlameQuotesTheMessageThatExplainsTheFile(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: "also worth a look at auth.go some day"},
			{Role: "assistant", Text: "in internal/auth/auth.go we moved the token check out of auth.go because auth.go ran it twice"},
		},
	}
	hits := Blame([]model.Session{s}, BlameTarget{Base: "auth.go", FullPath: "internal/auth/auth.go"}, BlameOptions{})
	if len(hits) == 0 || len(hits[0].Snippets) == 0 {
		t.Fatal("no blame hit, so the test measured nothing")
	}
	if !strings.Contains(hits[0].Snippets[0], "moved the token check") {
		t.Errorf("first quote is %q, not the message that explains the file", hits[0].Snippets[0])
	}
}

// A message naming the file by path is more specific evidence than one repeating
// the bare name, however often it repeats it — otherwise a fix could rank on the
// count alone and quote whichever line says "auth.go" the most.
func TestBlamePrefersThePathOverTheRepeatedBareName(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: "auth.go auth.go auth.go auth.go, anyway"},
			{Role: "assistant", Text: "the change is in internal/auth/auth.go"},
		},
	}
	hits := Blame([]model.Session{s}, BlameTarget{Base: "auth.go", FullPath: "internal/auth/auth.go"}, BlameOptions{})
	if len(hits) == 0 || len(hits[0].Snippets) == 0 {
		t.Fatal("no blame hit, so the test measured nothing")
	}
	if !strings.Contains(hits[0].Snippets[0], "internal/auth/auth.go") {
		t.Errorf("first quote is %q, not the one naming the path", hits[0].Snippets[0])
	}
}

// A fork, a resumed session and a subagent run each carry the transcript they
// came from, so one piece of evidence arrives under several ids. Read from a
// real store, `deja blame internal/search/search.go` quoted the same two lines
// under four sessions — all four forks of one — and those eight lines were most
// of the answer. On that file the quoted lines went from 18 to 11.
func TestBlameSaysAPieceOfEvidenceOnce(t *testing.T) {
	now := time.Now().UTC()
	shared := "internal/search/search.go carries the lifecycle field and the summary printed under it"
	own := "internal/search/search.go got the freshness floor raised to keep a year-old session usable"
	session := func(id, extra string, ago time.Duration) model.Session {
		msgs := []model.Message{{Role: "assistant", Text: shared, Time: now.Add(-ago)}}
		if extra != "" {
			msgs = append(msgs, model.Message{Role: "assistant", Text: extra, Time: now.Add(-ago)})
		}
		return model.Session{Harness: "claude", ID: id, Project: "deja-vu",
			Updated: now.Add(-ago), Messages: msgs}
	}
	ss := []model.Session{
		session("fork-a", own, time.Hour),
		session("fork-b", "", 2*time.Hour),
		session("fork-c", "", 3*time.Hour),
	}
	hits := Blame(ss, BlameTarget{Base: "search.go", FullPath: "internal/search/search.go"}, BlameOptions{})
	if len(hits) != 3 {
		t.Fatalf("hits = %d, want every session that touched the file", len(hits))
	}
	seen := 0
	for _, h := range hits {
		for _, sn := range h.Snippets {
			if strings.Contains(sn, "carries the lifecycle field") {
				seen++
			}
		}
	}
	if seen != 1 {
		t.Errorf("the same evidence is quoted %d times", seen)
	}
	// The session that had something of its own still says it.
	var kept bool
	for _, h := range hits {
		for _, sn := range h.Snippets {
			if strings.Contains(sn, "freshness floor") {
				kept = true
			}
		}
	}
	if !kept {
		t.Error("a session's own evidence was dropped with the duplicate")
	}
}

// PrintBlame renders to a terminal and, through the MCP blame tool, to an agent.
// id, project and title are transcript free text — an imported peer's title
// most of all — so an escape or a bidi run in them must not reach the output.
func TestPrintBlameSanitisesSessionFields(t *testing.T) {
	var b bytes.Buffer
	hits := []BlameHit{{
		Session: model.Session{
			Harness: "claude", ID: "ev\u202eil\u200bid", Project: "proj\x1b[31m",
			Updated: time.Now(),
		},
		Title: "hostile\x1btitle\u202e here", Count: 1, Tier: "exact",
		Snippets:      []string{"x"},
		Lifecycle:     "rejected",
		LifecycleNote: "note\x1bfrom\u202e a peer\u200b",
	}}
	PrintBlame(&b, hits, false)
	out := b.String()
	for _, r := range out {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			t.Fatalf("blame output carried a control rune %U: %q", r, out)
		}
	}
	for _, bad := range []rune{'\u202e', '\u200b'} {
		if strings.ContainsRune(out, bad) {
			t.Fatalf("blame output carried %U: %q", bad, out)
		}
	}
	if !strings.Contains(out, "hostile") || !strings.Contains(out, "proj") {
		t.Fatalf("blame dropped the readable text: %q", out)
	}
}

// The line a blame snippet shows has to be the file that was asked about. A
// substring match printed a sibling — "mypool.go" contains "pool.go" — as the
// answer, and pasted into restore that is a different file's spans (#2044).
func TestBlameSnippetNamesTheFileThatWasAskedAbout(t *testing.T) {
	target := BlameTarget{Base: "pool.go", FullPath: "/tmp/app/pool.go"}
	for _, c := range []struct{ name, text, want string }{
		{"a sibling whose name contains it",
			"/tmp/app/mypool.go\n/tmp/app/pool.go", "/tmp/app/pool.go"},
		{"the full path beats a bare match",
			"/tmp/app/vendor/pool.go\n/tmp/app/pool.go", "/tmp/app/pool.go"},
		{"spelled in another case",
			"/tmp/app/Pool.go", "/tmp/app/Pool.go"},
		{"its own spaces kept",
			"/tmp/app/two  spaces.go\n/tmp/app/pool.go", "/tmp/app/pool.go"},
		// A store synced from Windows: on a unix host filepath sees one
		// segment, and missing the line here drops back to the renderer that
		// collapses the spaces.
		{"a windows path", `C:\src\app\pool.go`, `C:\src\app\pool.go`},
	} {
		if got := blameSnippet(c.text, "files", target); got != c.want {
			t.Errorf("%s: blameSnippet = %q, want %q", c.name, got, c.want)
		}
	}

	// An edit record is "path\nspan": the path is a path, the span is prose.
	spaces := BlameTarget{Base: "two  spaces.go", FullPath: "/tmp/app/two  spaces.go"}
	if got := blameSnippet(`C:\src\app\two  spaces.go`, "files", BlameTarget{Base: "two  spaces.go"}); got != `C:\src\app\two  spaces.go` {
		t.Errorf("a windows path lost its spaces: %q", got)
	}

	got := blameSnippet("/tmp/app/two  spaces.go\nsize = 20", "edit", spaces)
	if !strings.HasPrefix(got, "/tmp/app/two  spaces.go") {
		t.Errorf("an edit snippet lost the file's spaces: %q", got)
	}
	if !strings.Contains(got, "size = 20") {
		t.Errorf("an edit snippet lost the span: %q", got)
	}

	// Anything else is prose, and prose is what snippet() is for.
	if got := blameSnippet("we changed pool.go twice today", "user", target); got == "" {
		t.Error("a prose mention produced no snippet")
	}
}

// A path is bounded like anything else that reaches a terminal or an MCP
// payload, and bounded from the left: the tail is what names the file.
func TestSafePathIsBoundedFromTheLeft(t *testing.T) {
	long := "/tmp/" + strings.Repeat("deep/", 200) + "pool.go"
	got := SafePath(long)
	if len([]rune(got)) > pathCap {
		t.Errorf("SafePath returned %d runes, over the %d cap", len([]rune(got)), pathCap)
	}
	if !strings.HasSuffix(got, "pool.go") {
		t.Errorf("the clip dropped the file's own name: %q", got)
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("a clipped path does not say it was clipped: %q", got)
	}
}

// A command is clipped from the other end than a path: what identifies it is
// the program it runs, so the head is what survives (#2052).
func TestSafeCommandIsClippedFromTheRight(t *testing.T) {
	long := "go test ./internal/index -run " + strings.Repeat("A", 400)
	got := SafeCommand(long)
	if len([]rune(got)) > pathCap {
		t.Errorf("SafeCommand returned %d runes, over the %d cap", len([]rune(got)), pathCap)
	}
	if !strings.HasPrefix(got, "go test ./internal/index -run ") {
		t.Errorf("the clip dropped the command itself: %q", got[:40])
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a clipped command does not say it was clipped: %q", got[len(got)-10:])
	}
	// A path keeps the other end, and both stay under the cap.
	if p := SafePath("/tmp/" + strings.Repeat("deep/", 200) + "pool.go"); !strings.HasSuffix(p, "pool.go") {
		t.Errorf("a path lost its own name: %q", p)
	}
}

// A note title ends in the state the note is in, and that suffix is what every
// one-line surface reads it for — so the clip keeps it. Only a short one: a long
// bracketed tail would otherwise carry the whole title past the bound (#2058).
func TestSafeNoteTitleKeepsTheStateAndStaysBounded(t *testing.T) {
	long := strings.Repeat("very long title ", 40)
	for _, c := range []struct {
		name, in string
		suffix   string
		bounded  bool
	}{
		{"a state survives the clip", long + " [rejected]", " [rejected]", true},
		{"a short title is untouched", "pool sizing [accepted]", " [accepted]", false},
		{"a long bracketed tail is not a state", "note [" + strings.Repeat("x", 400) + "]", "", true},
		{"no state at all", long, "", true},
	} {
		got := SafeNoteTitle(c.in)
		if c.suffix != "" && !strings.HasSuffix(got, c.suffix) {
			t.Errorf("%s: %q does not end in %q", c.name, got, c.suffix)
		}
		if c.bounded && len(got) > answerCap+noteStateCap {
			t.Errorf("%s: %d bytes, over the %d bound", c.name, len(got), answerCap+noteStateCap)
		}
		if strings.Contains(got, "\n") {
			t.Errorf("%s: the row is not one line: %q", c.name, got)
		}
	}
}

// blame tells every agent "most specific mentions come first". Asked by a bare
// filename the order was recency, and a session naming a different file with
// the same basename came ahead of the ones naming the path asked about (#2840).
func TestBlameOrdersBySpecificityBeforeRecency(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC) }
	session := func(id, text string, at time.Time) model.Session {
		return model.Session{
			Harness: "claude", ID: id, Project: "api", Updated: at,
			Messages: []model.Message{{Role: "user", Text: text, Time: at}},
		}
	}
	// Oldest first, so recency alone would reverse them.
	ss := []model.Session{
		session("abs", "we edited /work/api/internal/index/ingest.go and fixed the watermark", day(10)),
		session("rel", "the change went into internal/index/ingest.go, one function", day(11)),
		// The bare mention is the newest and says the name most often, so only
		// the rule about naming a path keeps it behind the others.
		session("base", "ingest.go ingest.go ingest.go needed a second pass, nothing else about ingest.go", day(13)),
		session("other", "we touched cmd/tools/ingest.go in the other repo entirely", day(12)),
	}

	order := func(target BlameTarget) []string {
		hits := Blame(ss, target, BlameOptions{All: true})
		out := make([]string, 0, len(hits))
		for _, h := range hits {
			out = append(out, h.Session.ID)
		}
		return out
	}
	// Asked by a bare filename deja has no tree to prefer, so the rule sits
	// out and nothing moves: crediting whatever directories a session happened
	// to write handed `blame Makefile` to three other projects, measured on a
	// real store. What follows is the query that carries a path.
	bare := order(BlameTarget{FullPath: "/work/api/ingest.go", Base: "ingest.go", Stem: "ingest"})
	if len(bare) < 4 {
		t.Fatalf("every session names the file: %v", bare)
	}
	place := func(list []string, id string) int {
		for i, got := range list {
			if got == id {
				return i
			}
		}
		return -1
	}

	// And the same when the caller names the path: the session that spells it
	// out in full is not pushed down by a newer one that says less.
	full := order(BlameTarget{FullPath: "/work/api/internal/index/ingest.go", Base: "ingest.go", Stem: "ingest"})
	// The two that wrote the path asked about come first, however new or
	// talkative the others are: the bare mention says the name four times and
	// is the newest, and the file in another tree wrote a path of its own.
	for _, ahead := range []string{"abs", "rel"} {
		for _, behind := range []string{"base", "other"} {
			if place(full, ahead) > place(full, behind) {
				t.Errorf("%s came after %s: %v", ahead, behind, full)
			}
		}
	}
}

// In real-world stores, counting the directories a session wrote made a
// deeply nested file of the same name in another project the most specific
// mention there is, and every session that had actually worked on the file
// fell off the first page (#2840).
func TestBlameDoesNotPreferADeepUnrelatedPath(t *testing.T) {
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	worked := model.Session{
		Harness: "claude", ID: "worked", Project: "api", Updated: now.Add(-72 * time.Hour),
		Messages: []model.Message{{
			Role: "assistant", Time: now.Add(-72 * time.Hour),
			Text: "internal/index/ingest.go again: ingest.go holds the watermark, ingest.go stamps it, and ingest.go is where the pass ends",
		}},
	}
	elsewhere := model.Session{
		Harness: "opencode", ID: "elsewhere", Project: "other", Updated: now,
		Messages: []model.Message{{
			Role: "user", Time: now,
			Text: "/var/folders/jn/T/opencode/monorepo/apps/catalog-svc/cmd/worker/ingest.go failed to build",
		}},
	}
	hits := Blame([]model.Session{worked, elsewhere},
		BlameTarget{FullPath: "/work/api/internal/index/ingest.go", Base: "ingest.go", Stem: "ingest"},
		BlameOptions{All: true})
	if len(hits) != 2 {
		t.Fatalf("both sessions name the file: %d hits", len(hits))
	}
	if hits[0].Session.ID != "worked" {
		t.Errorf("a deeper path in another project outranked the session that worked on the file: %s first", hits[0].Session.ID)
	}
}

func TestResolveBlamePathSymbolRangeGoAndPython(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "worker.go")
	goSrc := "package worker\n\nfunc helper() {\n\treturn\n}\n\nfunc (w *Worker) RunTask(id string) error {\n\tif id == \"\" {\n\t\treturn nil\n\t}\n\treturn nil\n}\n"
	if err := os.WriteFile(goFile, []byte(goSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	target, err := ResolveBlamePath(goFile + ":RunTask")
	if err != nil {
		t.Fatalf("ResolveBlamePath: %v", err)
	}
	if target.Symbol != "RunTask" || target.Line != 7 || target.EndLine != 12 || target.LineNote != "" {
		t.Fatalf("Go symbol target = %+v, want Symbol=RunTask Line=7 EndLine=12", target)
	}

	pyFile := filepath.Join(dir, "runner.py")
	pySrc := "import os\n\ndef other():\n    pass\n\ndef compute_budget(items):\n    total = 0\n    for x in items:\n        total += x\n    return total\n\nclass Next:\n    pass\n"
	if werr := os.WriteFile(pyFile, []byte(pySrc), 0o600); werr != nil {
		t.Fatal(werr)
	}

	pyTarget, err := ResolveBlamePath(pyFile + ":compute_budget")
	if err != nil {
		t.Fatalf("ResolveBlamePath py: %v", err)
	}
	if pyTarget.Symbol != "compute_budget" || pyTarget.Line != 6 || pyTarget.EndLine != 10 {
		t.Fatalf("Python symbol target = %+v, want Symbol=compute_budget Line=6 EndLine=10", pyTarget)
	}
}

func TestBlameSymbolPrioritizesSessionMentioningSymbol(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "pool.go")
	goSrc := "package pool\n\nfunc OpenPool() {}\n\nfunc ClosePool() {\n\treturn\n}\n"
	if err := os.WriteFile(goFile, []byte(goSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	target, err := ResolveBlamePath(goFile + ":ClosePool")
	if err != nil {
		t.Fatal(err)
	}

	t0 := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	sessions := []model.Session{
		{
			ID:      "newer-unrelated-symbol",
			Harness: "claude",
			Project: dir,
			Updated: t0.Add(time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "Update " + goFile + " to tweak OpenPool."},
				{Role: "assistant", Text: "Updated OpenPool in " + goFile + "."},
			},
		},
		{
			ID:      "older-matching-symbol",
			Harness: "claude",
			Project: dir,
			Updated: t0,
			Messages: []model.Message{
				{Role: "user", Text: "Fix ClosePool leak in " + goFile + "."},
				{Role: "assistant", Text: "Fixed ClosePool in " + goFile + " because idle connections lingered."},
			},
		},
	}

	hits := Blame(sessions, target, BlameOptions{})
	if len(hits) != 2 {
		t.Fatalf("len(hits) = %d, want 2", len(hits))
	}
	if hits[0].Session.ID != "older-matching-symbol" {
		t.Fatalf("top hit = %s, want older-matching-symbol that touched ClosePool", hits[0].Session.ID)
	}
}

func TestBlamePathResolutionAndMatching(t *testing.T) {
	target, err := ResolveBlamePath("cmd/deja/main.go")
	if err != nil || target.Base != "main.go" || target.Stem != "main" || !strings.HasSuffix(filepath.ToSlash(target.FullPath), "/cmd/deja/main.go") {
		t.Fatalf("target=%#v err=%v", target, err)
	}
	if _, err := ResolveBlamePath(""); err == nil {
		t.Fatal("empty path accepted")
	}
	now := time.Now()
	ss := []model.Session{
		{ID: "bare", Project: "other", Updated: now, Messages: []model.Message{{Role: "user", Text: "main.go needs a test"}}},
		{ID: "suffix", Project: "other", Updated: now, Messages: []model.Message{{Role: "user", Text: "we changed cmd/deja/main.go carefully"}}},
		{ID: "false", Project: "other", Updated: now, Messages: []model.Message{{Role: "user", Text: "domain.got is unrelated"}}},
	}
	hits := Blame(ss, target, BlameOptions{All: true})
	if len(hits) != 2 || hits[0].Session.ID != "suffix" || hits[1].Session.ID != "bare" {
		t.Fatalf("hits=%#v", hits)
	}
	if hits[0].Title != "we changed cmd/deja/main.go carefully" || len(hits[0].Snippets) != 1 {
		t.Fatalf("hit=%#v", hits[0])
	}
	if got := Blame([]model.Session{{ID: "x", Updated: now, Messages: []model.Message{{Text: "domain.got"}}}}, BlameTarget{FullPath: "/repo/domain.go", Base: "domain.go", Stem: "domain"}, BlameOptions{}); len(got) != 0 {
		t.Fatalf("false positive=%#v", got)
	}
}

func TestBlameFiltersProjectBoostLimitAndJSON(t *testing.T) {
	now := time.Now()
	ss := make([]model.Session, 0, 12)
	for i := range 12 {
		ss = append(ss, model.Session{ID: string(rune('a' + i)), Harness: "claude", Project: "/repo", Updated: now, Messages: []model.Message{{Role: "user", Text: "parser.go"}}})
	}
	target := BlameTarget{FullPath: "/repo/parser.go", Base: "parser.go", Stem: "parser"}
	o := BlameOptions{Harness: "claude", Project: "repo"}
	ranked := Blame(ss, target, o)
	if len(ranked) != 12 {
		t.Fatalf("ranked=%d, want every session that touched the file", len(ranked))
	}
	hits := CapBlame(ranked, o)
	if len(hits) != 10 {
		t.Fatalf("default limit=%d", len(hits))
	}
	if all := CapBlame(ranked, BlameOptions{All: true}); len(all) != 12 {
		t.Fatalf("--all limit=%d", len(all))
	}
	if got := Blame(ss, target, BlameOptions{Harness: "codex", All: true}); len(got) != 0 {
		t.Fatalf("harness filter=%#v", got)
	}
	var b bytes.Buffer
	PrintBlame(&b, hits[:1], true)
	var decoded []BlameHit
	if err := json.Unmarshal(b.Bytes(), &decoded); err != nil || len(decoded) != 1 {
		t.Fatalf("json=%q err=%v", b.String(), err)
	}
	b.Reset()
	PrintBlame(&b, hits[:1], false)
	if !strings.Contains(b.String(), "parser.go") || !strings.Contains(b.String(), "claude") {
		t.Fatalf("plain=%q", b.String())
	}
}

func TestBlameHelpersAndSince(t *testing.T) {
	if got := blameForms("/repo/cmd/main.go"); len(got) != 6 || got[0] != "/repo/cmd/main.go" || got[1] != "repo/cmd/main.go" || got[4] != "/main.go" {
		t.Fatalf("forms=%v", got)
	}
	if count, _ := mentionScore("nothing here", "main.go", nil); count != 0 {
		t.Fatalf("unexpected mention count=%d", count)
	}
	if pathFormCount("xcmd/main.go", "cmd/main.go") != 0 || pathFormCount("cmd/main.go", "cmd/main.go") != 1 {
		t.Fatal("path form boundary checks failed")
	}
	if !pathComponentOrWord("/main.go/", 1, 8) || pathComponentOrWord("xmain.go", 1, 8) || pathComponentOrWord("main.got", 0, 7) {
		t.Fatalf("boundary checks failed: slash=%v prefix=%v suffix=%v", pathComponentOrWord("/main.go/", 1, 8), pathComponentOrWord("xmain.go", 1, 8), pathComponentOrWord("main.got", 0, 7))
	}
	repo := t.TempDir()
	inRepo := filepath.Join(repo, "main.go")
	other := filepath.Join(repo, "..", "elsewhere")
	if !projectContainsFile(repo, inRepo) || projectContainsFile("relative", inRepo) || projectContainsFile(filepath.Clean(other), inRepo) {
		t.Fatal("project root checks failed")
	}
	now := time.Now()
	old := model.Session{ID: "old", Updated: now.Add(-48 * time.Hour), Messages: []model.Message{{Role: "user", Text: "main.go"}}}
	if got := Blame([]model.Session{old}, BlameTarget{FullPath: "/main.go", Base: "main.go"}, BlameOptions{Since: time.Hour, All: true}); len(got) != 0 {
		t.Fatalf("since filter=%#v", got)
	}
	if sessionTitle(model.Session{Title: "explicit"}) != "explicit" || sessionTitle(model.Session{}) != "" {
		t.Fatal("title fallback failed")
	}
	long := strings.Repeat("x", 70)
	if got := sessionTitle(model.Session{Messages: []model.Message{{Role: "user", Text: long}}}); len(got) != 63 {
		t.Fatalf("long title length=%d", len(got))
	}
}

func BenchmarkBlameVerification1000Candidates(b *testing.B) {
	now := time.Now()
	ss := make([]model.Session, 1000)
	for i := range ss {
		ss[i] = model.Session{ID: string(rune(i + 1000)), Updated: now, Messages: []model.Message{{Text: "prefix parser.go suffix"}}}
	}
	target := BlameTarget{FullPath: "/repo/internal/parser.go", Base: "parser.go", Stem: "parser"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if got := Blame(ss, target, BlameOptions{All: true}); len(got) != len(ss) {
			b.Fatalf("hits=%d", len(got))
		}
	}
}

// The decay was read from the clock once per session, so candidates with
// identical evidence and identical timestamps sorted differently on every run
// — five runs, five different top hits (#688).
func TestBlameRanksTheSameWayEveryRun(t *testing.T) {
	day := time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)
	ss := make([]model.Session, 0, 40)
	for i := range 40 {
		id := fmt.Sprintf("s%02d", i)
		ss = append(ss, model.Session{
			Harness: "claude", ID: id, Project: "p", Updated: day,
			Messages: []model.Message{{Role: "files", Text: "/repo/main.go", Time: day}},
		})
	}
	target, err := ResolveBlamePath("/repo/main.go")
	if err != nil {
		t.Fatal(err)
	}
	first := Blame(ss, target, BlameOptions{All: true})
	if len(first) == 0 {
		t.Fatal("no hits")
	}
	for run := range 20 {
		got := Blame(ss, target, BlameOptions{All: true})
		for i := range got {
			if got[i].Session.ID != first[i].Session.ID {
				t.Fatalf("run %d position %d: %q, first run had %q", run, i, got[i].Session.ID, first[i].Session.ID)
			}
		}
	}
}

func TestBlameConclusionSnippetFallback(t *testing.T) {
	day := time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)
	ss := []model.Session{{
		Harness: "claude", ID: "s1", Project: "/repo", Updated: day,
		Messages: []model.Message{
			{Role: "user", Text: "fix connection exhaustion in the database layer", Time: day},
			{Role: "command", Text: "$ git add internal/pool/pool.go && git commit -m 'fix pool'", Time: day},
			{Role: "files", Text: "/repo/internal/pool/pool.go", Time: day},
			{Role: "assistant", Text: "We decided to cap max idle connections at 32 to prevent connection exhaustion.", Time: day},
		},
	}}
	target := BlameTarget{FullPath: "/repo/internal/pool/pool.go", Base: "pool.go", Stem: "pool"}
	hits := Blame(ss, target, BlameOptions{All: true})
	if len(hits) != 1 || len(hits[0].Snippets) == 0 {
		t.Fatalf("hits = %+v", hits)
	}
	if !strings.Contains(hits[0].Snippets[0], "cap max idle connections at 32") {
		t.Fatalf("expected session conclusion as first snippet when only command/files mention the path, got %v", hits[0].Snippets)
	}

	dumpSess := []model.Session{{
		Harness: "claude", ID: "s2", Project: "/repo", Updated: day,
		Messages: []model.Message{
			{Role: "assistant", Text: `[L7 step=888 2026-06-28] | tools: edit_file({"Path":"/repo/internal/pool/pool.go","Target":"old","Replacement":"new"})`, Time: day},
			{Role: "assistant", Text: "Fixed pool.go race by locking the idle slice before eviction.", Time: day},
		},
	}}
	dumpHits := Blame(dumpSess, target, BlameOptions{All: true})
	if len(dumpHits) != 1 || len(dumpHits[0].Snippets) == 0 || strings.Contains(dumpHits[0].Snippets[0], "tools: edit_file") {
		t.Fatalf("subagent tool dump leaked into blame snippet: %+v", dumpHits)
	}
}

// A tool's own sentence about the file is not why the file looks the way it
// does. Measured over 1,439 paths on a real store, 1,567 of the 7,417 quoted
// snippets were one — a write confirmation, a patch receipt — and 540 rows had
// nothing else to show (#3721).
func TestBlameDoesNotQuoteAToolsWriteConfirmation(t *testing.T) {
	now := time.Now().UTC()
	target := BlameTarget{FullPath: "/work/app/internal/pool.go", Base: "pool.go", Stem: "pool"}
	s := model.Session{
		Harness: "claude", ID: "s1", Project: "app", Updated: now,
		Messages: []model.Message{
			{Role: "assistant", Text: "internal/pool.go keeps one connection per shard because the driver's own pool is per process", Time: now},
			{Role: sources.RoleToolOutput, Text: "The file /work/app/internal/pool.go has been updated successfully. (file state is current in your context — no need to Read it back)", Time: now.Add(time.Second)},
			{Role: sources.RoleToolOutput, Text: "File created successfully at: /work/app/internal/pool.go", Time: now.Add(2 * time.Second)},
		},
	}
	hits := Blame([]model.Session{s}, target, BlameOptions{All: true})
	if len(hits) != 1 {
		t.Fatalf("want the one session, got %d", len(hits))
	}
	joined := strings.Join(hits[0].Snippets, " | ")
	for _, echo := range []string{"has been updated successfully", "File created successfully"} {
		if strings.Contains(joined, echo) {
			t.Errorf("quoted a tool's own sentence as the reason: %q", joined)
		}
	}
	if !strings.Contains(joined, "one connection per shard") {
		t.Errorf("the sentence that says why is missing: %q", joined)
	}

	// A command the session ran is evidence, not echo — and a command record
	// starts with "$ ", which the artifact predicate treats as an artifact.
	cmd := model.Session{
		Harness: "claude", ID: "s2", Project: "app", Updated: now,
		Messages: []model.Message{
			{Role: sources.RoleCommand, Text: "$ go test ./internal/pool.go", Time: now},
		},
	}
	got := Blame([]model.Session{cmd}, target, BlameOptions{All: true})
	if len(got) != 1 || len(got[0].Snippets) == 0 {
		t.Fatalf("a command that names the file is evidence: %+v", got)
	}
}

// `blame scripts/load` answered with every session that said "under load":
// the bare name of an extensionless file is an ordinary word. Only the path
// counts for such a name, while `Makefile` keeps matching on its own.
func TestBlameWordNamedFileNeedsItsPath(t *testing.T) {
	now := time.Now()
	ss := []model.Session{
		{ID: "word", Project: "/repo", Updated: now, Messages: []model.Message{{Role: "user", Text: "the api times out under load, the cache layer returns 500"}}},
		{ID: "path", Project: "/repo", Updated: now, Messages: []model.Message{{Role: "user", Text: "scripts/load now waits for the server before it starts"}}},
	}
	hits := Blame(ss, BlameTarget{FullPath: "/repo/scripts/load", Base: "load", Stem: "load"}, BlameOptions{All: true})
	if len(hits) != 1 || hits[0].Session.ID != "path" {
		t.Fatalf("blame scripts/load = %v, want only the session naming the path", blameIDs(hits))
	}
	mk := []model.Session{{ID: "mk", Project: "/repo", Updated: now, Messages: []model.Message{{Role: "user", Text: "the Makefile test target needs -race"}}}}
	if hits := Blame(mk, BlameTarget{FullPath: "/repo/Makefile", Base: "Makefile", Stem: "Makefile"}, BlameOptions{All: true}); len(hits) != 1 {
		t.Fatalf("blame Makefile = %v, want the bare mention", blameIDs(hits))
	}
}

// A pasted blob in place of a path took 20s to rank; past PATH_MAX it is
// refused before any matching.
func TestBlameRefusesAPathLongerThanAnyFileSystemAllows(t *testing.T) {
	if _, err := ResolveBlamePath(strings.Repeat("a/", 3000) + "x.go"); err == nil {
		t.Fatal("a 6 KB path was accepted")
	}
	if _, err := ResolveBlamePath(strings.Repeat("a/", 100) + "x.go"); err != nil {
		t.Fatalf("a deep but real path was refused: %v", err)
	}
}

func blameIDs(hits []BlameHit) []string {
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.Session.ID)
	}
	return ids
}

func blameOrder(hits []BlameHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Session.ID)
	}
	return out
}

// A session holding a write or edit of the file ranks above one that only
// named it, and a write a fork copied stays its source's: recency alone put
// the parent that asked for the file above the sub-agent that wrote it, and the
// fork above the source (#5317).
func TestBlameListsTheWriterAboveASessionThatNamedTheFile(t *testing.T) {
	const path = "/home/u/work/app/notes.txt"
	target := BlameTarget{FullPath: path, Base: "notes.txt", Stem: "notes"}
	at := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	parent := model.Session{Harness: "claude", ID: "bbbbbbbb", Project: "/home/u/work/app", Started: at, Updated: at.Add(30 * time.Minute), Messages: []model.Message{
		{Role: "user", Text: "have a subagent write the release notes into " + path, Time: at},
		{Role: "assistant", Text: "done", Time: at.Add(30 * time.Minute)},
	}}
	child := model.Session{Harness: "claude", ID: "x1", Kind: "subagent", Parent: "bbbbbbbb", Project: "/home/u/work/app", Started: at.Add(time.Second), Updated: at.Add(3 * time.Second), Messages: []model.Message{
		{Role: "user", Text: "Write the release notes into " + path, Time: at.Add(time.Second)},
		{Role: sources.RoleWrote, Text: sources.WroteRecord(path, "release notes for the marmotquill exporter"), Time: at.Add(3 * time.Second)},
		{Role: sources.RoleFiles, Text: path, Time: at.Add(3 * time.Second)},
	}}
	if got := blameOrder(Blame([]model.Session{parent, child}, target, BlameOptions{All: true})); len(got) != 2 || got[0] != "x1" {
		t.Errorf("sub-agent case: order %v, want x1 first", got)
	}

	// The fork: a copy holding the source's edit at its original time, plus
	// turns of its own, so it is newer.
	copied := func() []model.Message {
		return []model.Message{
			{Role: "user", Text: "add the exporter notes", Time: at},
			{Role: sources.RoleEdit, Text: path + "\nold notes line here", Time: at.Add(time.Second)},
			{Role: "assistant", Text: "updated " + path, Time: at.Add(2 * time.Second)},
		}
	}
	src := model.Session{Harness: "claude", ID: "aaaaaaaa", Project: "/home/u/work/app", Started: at, Updated: at.Add(2 * time.Second), Messages: copied()}
	for _, kind := range []string{"fork", ""} {
		fork := model.Session{Harness: "claude", ID: "ffffffff", Project: "/home/u/work/app", Started: at, Updated: at.Add(time.Hour), Messages: append(copied(),
			model.Message{Role: "user", Text: "hi", Time: at.Add(time.Hour)})}
		if kind != "" {
			fork.Kind, fork.Parent = kind, "aaaaaaaa"
		}
		if got := blameOrder(Blame([]model.Session{fork, src}, target, BlameOptions{All: true})); len(got) != 2 || got[0] != "aaaaaaaa" {
			t.Errorf("fork marked=%v: order %v, want the source first", kind != "", got)
		}
	}

	// Control: two sessions that both wrote the file keep newest first.
	other := child
	other.ID, other.Kind, other.Parent, other.Updated = "x2", "", "", at.Add(time.Hour)
	other.Messages = []model.Message{{Role: "user", Text: "Write the release notes again into " + path, Time: at.Add(time.Hour)}, {Role: sources.RoleWrote, Text: sources.WroteRecord(path, "a different notes line written later"), Time: at.Add(time.Hour)}}
	if got := blameOrder(Blame([]model.Session{child, other}, target, BlameOptions{All: true})); len(got) != 2 || got[0] != "x2" {
		t.Errorf("two writers: order %v, want the newer x2 first", got)
	}
}
