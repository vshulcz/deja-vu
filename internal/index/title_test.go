package index

import (
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A title the ingest borrowed from plumbing is not what a person typed. On an
// agent-heavy store that family is most of the titles, so a surface picking a
// phrase out of them has to skip it (#3714).
func TestTitleIsBorrowed(t *testing.T) {
	for _, s := range []string{
		"harness output: verify the shard rebalance report",
		"  harness output: something",
		"tool output: 42 files changed",
	} {
		if !TitleIsBorrowed(s) {
			t.Errorf("%q is a borrowed title", s)
		}
	}
	for _, s := range []string{
		"",
		"the shard rebalance keeps flapping",
		"output of the harness is fine to talk about",
	} {
		if TitleIsBorrowed(s) {
			t.Errorf("%q is a person's own title", s)
		}
	}
}

// A session the agent opened itself, or one whose prompts are all harness
// plumbing, printed a blank line in `deja last` and on the first screen — three
// of four rows carrying no information at all (#692).
func TestSessionTitleFallsBackToTheAssistant(t *testing.T) {
	at := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	msg := func(role, text string, minute int) model.Message {
		return model.Message{Role: role, Text: text, Time: at.Add(time.Duration(minute) * time.Minute)}
	}
	cases := []struct {
		name string
		msgs []model.Message
		want string
	}{
		{"user turn wins", []model.Message{
			msg("assistant", "capped the pool at 200", 0),
			msg("user", "the pool starves under load", 1),
		}, "the pool starves under load"},
		{"no user turn at all", []model.Message{
			msg("assistant", "only assistant speech here", 0),
		}, "only assistant speech here"},
		{"user turns are all plumbing", []model.Message{
			msg("user", "<local-command-stdout>ok</local-command-stdout>", 0),
			msg("assistant", "done", 1),
		}, "done"},
		{"assistant plumbing is skipped too", []model.Message{
			msg("assistant", "<command-name>/clear</command-name>", 0),
			msg("assistant", "cleared it", 1),
		}, "cleared it"},
		// Tool output is not speech — naming a session after a stack trace is
		// how the titles in #636 happened — so it never outranks a turn. With
		// no turn at all it is the only thing left, and it is marked so the
		// line does not read as something a person said.
		{"tool output loses to speech", []model.Message{
			msg("tool-output", "panic: runtime error", 0),
			msg("assistant", "looking at the pool", 1),
		}, "looking at the pool"},
		{"only tool output", []model.Message{
			msg("tool-output", "panic: runtime error", 0),
			msg("files", "/repo/pool.go", 1),
		}, "tool output: panic: runtime error"},
	}
	for _, c := range cases {
		if got := sessionTitle(model.Session{Messages: c.msgs}); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Taking whichever line sat first in the file disagreed with the import path,
// which orders by time — so one store titled locally and the same store
// imported elsewhere could disagree (#769).
func TestSessionTitleTakesTheEarliestTurn(t *testing.T) {
	at := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	msg := func(role, text string, minute int) model.Message {
		return model.Message{Role: role, Text: text, Time: at.Add(time.Duration(minute) * time.Minute)}
	}
	cases := []struct {
		name string
		msgs []model.Message
		want string
	}{
		{"assistant turns stored out of order", []model.Message{
			msg("assistant", "and then we rolled the migration back", 5),
			msg("assistant", "started the zeppelin migration", 0),
		}, "started the zeppelin migration"},
		{"user turns stored out of order", []model.Message{
			msg("user", "and what about the rollback", 5),
			msg("user", "how do we start the migration", 0),
		}, "how do we start the migration"},
		{"a user turn still beats an earlier assistant one", []model.Message{
			msg("assistant", "capped the pool at 200", 0),
			msg("user", "the pool starves under load", 5),
		}, "the pool starves under load"},
		// No clock at all: file order is the only order there is.
		{"no timestamps", []model.Message{
			{Role: "assistant", Text: "first assistant line with no clock"},
			{Role: "assistant", Text: "second assistant line with no clock"},
		}, "first assistant line with no clock"},
		// A mix: the undated line must not displace a dated one that came
		// first, and must not be treated as time zero either.
		{"dated first, undated after", []model.Message{
			msg("assistant", "dated opening line", 0),
			{Role: "assistant", Text: "undated line that follows"},
		}, "dated opening line"},
		{"undated first, dated after", []model.Message{
			{Role: "assistant", Text: "undated opening line"},
			msg("assistant", "dated line that follows", 5),
		}, "undated opening line"},
		// Plumbing is skipped whatever its position.
		{"earliest is plumbing", []model.Message{
			msg("user", "<local-command-stdout>ok</local-command-stdout>", 0),
			msg("user", "the real question", 5),
		}, "the real question"},
		{"only tool output", []model.Message{
			msg("tool-output", "panic: boom", 0),
		}, "tool output: panic: boom"},
	}
	for _, c := range cases {
		if got := sessionTitle(model.Session{Messages: c.msgs}); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// #1100 named the tool-only session so it would stop listing as an empty
// bracket. A session whose every turn is harness plumbing — a CLI's own
// stdout, a task notification — fell off the end of the same chain and listed
// as one: on this machine's index of 1,670, one row is a bracket and an id and
// nothing else. The surfaces that try to fill it in call firstUserTitle, which
// reads .Messages, and they are all fed by Recent, which returns metadata only
// (#2548).
func TestAPlumbingOnlySessionIsStillNamed(t *testing.T) {
	at := time.Date(2026, 8, 11, 15, 48, 0, 0, time.UTC)
	s := model.Session{ID: "plumbing", Harness: "claude", Project: "proj", Updated: at, Messages: []model.Message{
		{Role: "user", Text: "<local-command-stdout>That session is still running as a background agent. Open `claude agents` to attach.</local-command-stdout>", Time: at},
	}}
	meta := metaForSession(s)
	if meta.Title == "" {
		t.Fatal("the session lists as a bare id")
	}
	if want := "harness output: That session is still running as a backgroun…"; meta.Title != want {
		t.Errorf("title = %q, want %q", meta.Title, want)
	}
	// Not the reader's question and not the agent's words.
	if meta.AgentTitle {
		t.Error("harness plumbing was marked as the agent's own words")
	}
	// A session that has something better to say is unaffected.
	s.Messages = append(s.Messages, model.Message{Role: "user", Text: "why does the pool run out under load?", Time: at.Add(time.Minute)})
	if meta := metaForSession(s); meta.Title != "why does the pool run out under load?" {
		t.Errorf("a real turn lost its title: %q", meta.Title)
	}
}

// The receiving machine derives the title from the record stream alone, so the
// two paths have to agree — the same pairing #1100 keeps for the tool-only
// session.
func TestImportNamesAPlumbingOnlySessionTheSameWay(t *testing.T) {
	dir := t.TempDir()
	exp := filepath.Join(dir, "export")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 11, 15, 48, 0, 0, time.UTC)
	b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: "plumbing", Project: "proj",
		Role: "user", Text: "<local-command-stdout>That session is still running as a background agent.</local-command-stdout>", Time: at})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), []byte(string(b)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = Import(filepath.Join(dir, "index"), exp); err != nil {
		t.Fatal(err)
	}
	ss, err := Recent(filepath.Join(dir, "index"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("imported %d sessions, want 1", len(ss))
	}
	if !strings.HasPrefix(ss[0].Title, "harness output: That session is still running") {
		t.Errorf("imported title = %q, want the name the local path gives", ss[0].Title)
	}
	if ss[0].AgentTitle {
		t.Error("imported plumbing was marked as the agent's own words")
	}
}

// Two ways a secret reached the stored title. The derived title was cut to 60
// runes and only then redacted, so a secret straddling the cut lost the tail
// its pattern needs and the plaintext head stayed; and the incremental
// fallback, which fills a title that was empty on the first pass, redacted
// nothing at all. Both land in sessions.gob and on every page that prints a
// title.
func TestDerivedTitleRedactsBeforeItCuts(t *testing.T) {
	at := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	// 29 characters of lead, so the 60-rune cut falls between the password and
	// the "@" that the url-credentials pattern needs.
	const straddling = "rotate the staging secret ok https://deploy:hunter2swordfish@internal.example.com/x"
	cases := []struct {
		name   string
		msgs   []model.Message
		absent string
	}{
		{"secret across the cut", []model.Message{
			{Role: "user", Text: straddling, Time: at},
		}, "hunter2swordfish"},
		{"secret before the cut", []model.Message{
			{Role: "user", Text: "ship it with AKIAIOSFODNN7EXAMPLE now", Time: at}, //nolint:gosec // a fake secret the redaction test plants
		}, "AKIAIOSFODNN7EXAMPLE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := metaForSession(model.Session{ID: "s1", Harness: "claude", Messages: c.msgs}).Title
			if strings.Contains(got, c.absent) {
				t.Fatalf("the stored title carries the secret in the clear: %q", got)
			}
			if !strings.Contains(got, "[redacted:") {
				t.Fatalf("title %q lost the redaction marker", got)
			}
			if n := len([]rune(got)); n > 61 { // 60 plus the ellipsis
				t.Fatalf("title is %d runes, want the 60-rune cut: %q", n, got)
			}
		})
	}
}

// The second pass fills a title the first pass could not derive: the session
// opened with harness plumbing, so it was indexed titleless, and the real
// first turn arrived on a later append carrying a key.
func TestIncrementalTitleFallbackRedacts(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	file := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s.jsonl")
	write(t, file, claudeLine("s1", "2026-08-07T10:00:00Z", "<command-name>/init</command-name>"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The first pass has nothing but the slash command to go on, so the row
	// carries the stand-in #2548 gave it rather than a bare id — and the real
	// first turn, when it arrives, still replaces it.
	if got := m.Sessions["claude:s1"].Title; !titlePlaceholder(got) {
		t.Fatalf("precondition: the first pass already titled the session %q", got)
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(claudeLine("s1", "2026-08-07T10:01:00Z", "ship it with AKIAIOSFODNN7EXAMPLE now")); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	title := m.Sessions["claude:s1"].Title
	if title == "" {
		t.Fatalf("the append did not fill the title at all: %#v", m.Sessions["claude:s1"])
	}
	if strings.Contains(title, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("the incremental fallback stored an unredacted key: %q", title)
	}
	if titlePlaceholder(title) {
		t.Fatalf("the stand-in outlived the turn that should have replaced it: %q", title)
	}
}

// Incremental ingest reuses SessionMeta for a file it has already read, so a
// title derived under older rules survives every ordinary `deja index`. Three
// title fixes shipped without a format bump and left blank titles in place
// (#784); the bump is the repair, and this pins that the repair works.
func TestStaleFormatRederivesTitles(t *testing.T) {
	root, dir := allHarnessEnv(t)
	claudeFile := filepath.Join(root, "claude", "-tmp-p", "s.jsonl")
	write(t, claudeFile, claudeLine("s1", "2026-01-02T03:04:05Z", "ballast pump controller retries twice"))

	o := search.Options{Query: "ballast", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := m.Sessions["claude:s1"]
	if !ok {
		t.Fatalf("session not in manifest: %v", m.Sessions)
	}
	if meta.Title == "" {
		t.Fatal("no title to lose")
	}

	// What an index built by an older binary holds: a readable store whose
	// titles came from rules this build no longer uses.
	meta.Title = ""
	m.Sessions["claude:s1"] = meta
	m.Version = version - 1
	if err = writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	// Touching nothing else: the transcript is unchanged, which is exactly the
	// case incremental ingest skips.
	if err = EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Sessions["claude:s1"].Title; got == "" {
		t.Error("an index from an older format kept its blank title")
	}
	if _, err := os.Stat(filepath.Join(dir, "records.bin")); err != nil {
		t.Fatal(err)
	}
}

// A session of nothing but tool output — a hook run, an agent driven entirely
// by another program — has no user turn and no assistant turn. It listed as an
// empty bracket in `deja last` while `stats` printed a dash for the same
// session: two screens, two answers, neither of them saying what the session
// was.
func TestToolOnlySessionIsNamedAfterItsOutput(t *testing.T) {
	at := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	s := model.Session{ID: "toolonly", Harness: "claude", Project: "proj", Updated: at, Messages: []model.Message{
		{Role: roleToolOutput, Text: "npm ERR! ENOENT missing package.json", Time: at},
		{Role: roleToolOutput, Text: "exit status 1", Time: at.Add(time.Minute)},
	}}
	meta := metaForSession(s)
	if meta.Title != "tool output: npm ERR! ENOENT missing package.json" {
		t.Errorf("title = %q, want it named after the first output", meta.Title)
	}
	// The mark belongs to the assistant's own words; tool output is neither
	// the reader's question nor the agent's, and "agent: npm ERR!" would be a
	// third wrong answer.
	if meta.AgentTitle {
		t.Error("tool output was marked as the agent's own words")
	}
}

// The title is derived again on the receiving machine, from the record stream
// alone. A tool-only session that lists by name here must not list as a bare
// id there.
func TestImportNamesAToolOnlySessionTheSameWay(t *testing.T) {
	dir := t.TempDir()
	exp := filepath.Join(dir, "export")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	var batch strings.Builder
	for i, text := range []string{"npm ERR! ENOENT missing package.json", "exit status 1"} {
		b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: "toolonly", Project: "proj",
			Role: roleToolOutput, Text: text, Time: at.Add(time.Duration(i) * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		batch.WriteString(string(b) + "\n")
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), []byte(batch.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(filepath.Join(dir, "index"), exp); err != nil {
		t.Fatal(err)
	}
	ss, err := Recent(filepath.Join(dir, "index"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("imported %d sessions, want 1", len(ss))
	}
	if ss[0].Title != "tool output: npm ERR! ENOENT missing package.json" {
		t.Errorf("imported title = %q, want the same name the local path gives", ss[0].Title)
	}
	if ss[0].AgentTitle {
		t.Error("imported tool output was marked as the agent's own words")
	}
}

// Speech still wins wherever it exists, on both paths: #636 was titles that
// were stack traces, and that must not come back through the fallback.
func TestSpeechStillOutranksToolOutputOnImport(t *testing.T) {
	dir := t.TempDir()
	exp := filepath.Join(dir, "export")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	var batch strings.Builder
	for i, r := range []struct{ role, text string }{
		{roleToolOutput, "panic: runtime error"},
		{"assistant", "looking at the pool"},
		{"user", "the pool starves under load"},
	} {
		b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: "mixed", Project: "proj",
			Role: r.role, Text: r.text, Time: at.Add(time.Duration(i) * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		batch.WriteString(string(b) + "\n")
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), []byte(batch.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(filepath.Join(dir, "index"), exp); err != nil {
		t.Fatal(err)
	}
	ss, err := Recent(filepath.Join(dir, "index"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("imported %d sessions, want 1", len(ss))
	}
	if ss[0].Title != "the pool starves under load" {
		t.Errorf("imported title = %q, want the user turn", ss[0].Title)
	}
}
