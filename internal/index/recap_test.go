package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Every case here is a line a real week actually printed before the trimming
// existed (#544).
func TestARecapLineIsTrimmedNotRewritten(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "a sentence that ran into a heading",
			in:   "the provider pass has to run before the assignment rules ## Next",
			want: "the provider pass has to run before the assignment rules",
		},
		{
			name: "a sentence that swallowed a command",
			in:   "``` $ registry/cursor.html sitemap=2026-07-17 git=2026-09-21 ```",
			want: "",
		},
		{
			name: "a list marker from the message it came from",
			in:   "- the watchdog now waits twelve minutes before it calls a reconnect broken",
			want: "the watchdog now waits twelve minutes before it calls a reconnect broken",
		},
		{
			name: "a sentence that began inside a bold run",
			in:   "who stars it.** the radar now takes fresh stars with their profiles",
			want: "the radar now takes fresh stars with their profiles",
		},
		{
			name: "a fragment",
			in:   "fixed that",
			want: "",
		},
		{
			name: "the agent narrating its own turn",
			in:   "Report delivered to team-lead in full, with the architecture map and the coverage numbers",
			want: "",
		},
		{
			name: "an instruction it was given",
			in:   "Verify important conclusions by reading the actual source files before answering",
			want: "",
		},
		{
			name: "the agent announcing its next step",
			in:   "Now let me verify one more potential issue — whether the append path can have kept files",
			want: "",
		},
		{
			name: "deja's own recall, quoted back",
			in:   "déjà vu: tokscale was already configured for local usage — reusing it (deja:ses_f45a9)",
			want: "",
		},
	}
	for _, c := range cases {
		if got := trimRecapLine(c.in); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// A line over the ceiling is cut at a sentence end, not mid-word, and never
// rewritten.
func TestALongLineIsCutAtASentence(t *testing.T) {
	first := strings.Repeat("the pool was exhausted and the retry budget was wrong. ", 3)
	got := trimRecapLine(first + strings.Repeat("tail ", 40))
	if got == "" {
		t.Fatal("a long line was dropped instead of cut")
	}
	if len([]rune(got)) > recapLineMax {
		t.Errorf("the cut line is %d runes, over the %d ceiling: %q", len([]rune(got)), recapLineMax, got)
	}
	if !strings.HasSuffix(got, ".") {
		t.Errorf("the line was not cut at a sentence: %q", got)
	}
}

// One session states a conclusion in three messages, each adding a clause. All
// three used to be rows, because a prefix comparison catches none of them —
// the restatement begins differently every time.
func TestOneConclusionStatedTwiceIsOneLine(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	at := time.Now().Add(-2 * time.Hour)
	say := func(text string, min int) model.Message {
		return model.Message{Role: "assistant", Text: text, Time: at.Add(time.Duration(min) * time.Minute)}
	}
	writeStore(t, dir, []model.Session{{
		ID: "s1", Harness: "claude", Project: "work/app", Path: "/tmp/s1.jsonl", Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: "the anomaly on the upper stroke", Time: at},
			say("fixed the anomaly: the cause was the static-shape adapter taking any ambiguous form", 1),
			say("the cause turned out to be mine: the static-shape adapter takes any ambiguous form in frame", 2),
			say("decided: the retry budget stays at four attempts because a duplicate is worse than a drop", 3),
		},
	}})
	r, err := ScanRecap(dir, 24*time.Hour, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) != 1 {
		t.Fatalf("want one session with lines, got %d: %+v", len(r.Sessions), r.Sessions)
	}
	lines := r.Sessions[0].Lines
	if len(lines) == 0 {
		t.Fatal("the fixture produced no lines at all, so this test proves nothing")
	}
	if len(lines) != 2 {
		t.Fatalf("want two lines — the restatement collapsed — got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if r.Considered != 1 || r.Spoke != 1 {
		t.Errorf("the window counts are wrong: considered=%d spoke=%d", r.Considered, r.Spoke)
	}
}

// The requirement nobody had scoped: this text is written to be pasted, so it
// goes through the outbound pass and the count of what that masked is part of
// the answer.
func TestARecapMasksWhatIdentifiesTheMachine(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	at := time.Now().Add(-time.Hour)
	writeStore(t, dir, []model.Session{{
		ID: "s2", Harness: "claude", Project: "work/net", Path: "/tmp/s2.jsonl", Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: "the handshake hangs", Time: at},
			{Role: "assistant", Time: at.Add(time.Minute),
				Text: "root cause: the default route moved to 10.14.3.7 while the source address stayed on the old subnet"},
			{Role: "assistant", Time: at.Add(2 * time.Minute),
				Text: "the fix landed in /Users/rivera/src/net/route.go and the probe now names gw.internal instead"},
		},
	}})
	r, err := ScanRecap(dir, 24*time.Hour, 3)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(allRecapLines(r), "\n")
	if all == "" {
		t.Fatal("the fixture produced no lines at all, so this test proves nothing")
	}
	for _, gone := range []string{"10.14.3.7", "/Users/rivera", "gw.internal"} {
		if strings.Contains(all, gone) {
			t.Errorf("%q reached outbound text:\n%s", gone, all)
		}
	}
	if r.Masked.Total() == 0 {
		t.Errorf("nothing was reported as masked: %v", r.Masked)
	}
}

// A project named after the home directory carries an account name, and the
// heading of text written to be pasted is the last place for one.
func TestAHomeDirectoryIsNotAProjectName(t *testing.T) {
	if !RecapProjectIsHome("Users/rivera") {
		t.Error("a home path was treated as a project")
	}
	if !RecapProjectIsHome("home/rivera") {
		t.Error("a linux home path was treated as a project")
	}
	if RecapProjectIsHome("goprojects/deja-vu") {
		t.Error("a real project was called a home directory")
	}
}

// A line too long to print is cut, and where it is cut decides whether the
// result reads as a sentence or as a bug. Three shapes reach this: a sentence
// end late enough to keep, none at all, and a single token with no space to
// fall back to — the last one is a URL or a path, which a real week prints.
func TestALineTooLongIsCutWhereItCanBe(t *testing.T) {
	long := strings.Repeat("token ", 60) // 360 runes, no sentence end
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "cut at the sentence end",
			in:   strings.Repeat("a", 150) + ". " + strings.Repeat("b", 120),
			want: strings.Repeat("a", 150) + ".",
		},
		{
			name: "no sentence end, so the last word and an ellipsis",
			in:   long,
			want: strings.TrimSpace(long[:215]) + "…",
		},
		{
			name: "one long token has no word to cut at",
			in:   strings.Repeat("x", 300),
			want: strings.Repeat("x", 220) + "…",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cutAtSentence(c.in, recapLineMax)
			if got != c.want {
				t.Errorf("cut to %d runes:\n got %q\nwant %q", recapLineMax, got, c.want)
			}
		})
	}
}

// The CLI passes its own default through, and a caller that passes none — a
// script, or the MCP surface — must not get a session with no lines at all.
func TestAskingForNoLinesPerSessionStillGivesSome(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	at := time.Now().Add(-time.Hour)
	writeStore(t, dir, []model.Session{{
		ID: "s3", Harness: "claude", Project: "work/app", Path: "/tmp/s3.jsonl", Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: "the queue drains before the worker exits", Time: at},
			{Role: "assistant", Time: at.Add(time.Minute),
				Text: "decided: the worker waits for the queue to drain, because a dropped job costs more than a slow shutdown"},
		},
	}})
	r, err := ScanRecap(dir, 24*time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) != 1 || len(r.Sessions[0].Lines) == 0 {
		t.Fatalf("a zero budget produced nothing: %+v", r.Sessions)
	}
}

// A wide window on a busy machine is thousands of sessions, and reading them
// all to print twelve is wasted work — so the scan stops at the cap. What the
// cap must not do is stay quiet about it: the counts above the lines are the
// window, and a reader comparing them has to know only the newest were read.
func TestAWideWindowStopsAtTheCapAndSaysSo(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	now := time.Now()
	sessions := make([]model.Session, 0, recapSessionCap+20)
	for i := range recapSessionCap + 20 {
		at := now.Add(-time.Duration(i) * time.Minute)
		sessions = append(sessions, model.Session{
			ID:      fmt.Sprintf("s%03d", i),
			Harness: "claude", Project: "work/app",
			Path:    fmt.Sprintf("/tmp/s%03d.jsonl", i),
			Updated: at,
			Messages: []model.Message{
				{Role: "user", Text: "the worker stalls on the third batch", Time: at},
				{Role: "assistant", Time: at,
					Text: fmt.Sprintf("decided: batch %d keeps its own cursor, because one shared cursor rewound every retry", i)},
			},
		})
	}
	writeStore(t, dir, sessions)

	r, err := ScanRecap(dir, 24*time.Hour, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Considered != recapSessionCap+20 {
		t.Errorf("considered=%d, want the whole window %d", r.Considered, recapSessionCap+20)
	}
	if r.Read != recapSessionCap {
		t.Errorf("read=%d, want the cap %d named out loud", r.Read, recapSessionCap)
	}
	if r.Spoke > recapSessionCap {
		t.Errorf("spoke=%d, which is more sessions than were read", r.Spoke)
	}
	// The newest end of the window, not an arbitrary slice of it: session s000
	// is the most recent one.
	if len(r.Sessions) == 0 || r.Sessions[0].ID != "s000" {
		t.Errorf("the newest session is not first: %+v", r.Sessions[:min(2, len(r.Sessions))])
	}
}

// A recap is text for other people, so an ignored project must not reach it —
// and the count has to say one was held back, otherwise the window looks
// smaller than it is for no stated reason.
func TestAnIgnoredProjectIsWithheldAndCounted(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	pol := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(pol, []byte(`{"ignore":["*/secret-client/*","secret-client"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)

	at := time.Now().Add(-time.Hour)
	say := func(text string) model.Message {
		return model.Message{Role: "assistant", Text: text, Time: at.Add(time.Minute)}
	}
	writeStore(t, dir, []model.Session{
		{
			ID: "open", Harness: "claude", Project: "work/app", Path: "/tmp/open.jsonl", Updated: at,
			Messages: []model.Message{
				{Role: "user", Text: "the upload retries forever", Time: at},
				say("decided: the upload gives up after four attempts, because a stuck job blocks the queue behind it"),
			},
		},
		{
			ID: "closed", Harness: "claude", Project: "secret-client", Path: "/tmp/secret-client/s.jsonl", Updated: at,
			Messages: []model.Message{
				{Role: "user", Text: "the invoice total is off by a cent", Time: at},
				say("root cause: the rounding happened twice, once per currency conversion in the invoice path"),
			},
		},
	})

	r, err := ScanRecap(dir, 24*time.Hour, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Withheld != 1 {
		t.Errorf("withheld=%d, want the ignored project counted once", r.Withheld)
	}
	if r.Considered != 1 {
		t.Errorf("considered=%d, want only the session that is not ignored", r.Considered)
	}
	all := strings.Join(allRecapLines(r), "\n")
	if all == "" {
		t.Fatal("the fixture produced no lines at all, so this test proves nothing")
	}
	if strings.Contains(all, "invoice") {
		t.Errorf("the ignored project's text reached the recap:\n%s", all)
	}
}

// The other spelling of a home directory in the manifest: not the path, the
// bare base name, which is an account name with nothing around it.
func TestTheBareHomeDirectoryNameIsAlsoNotAProject(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory to compare against")
	}
	if base := filepath.Base(home); !RecapProjectIsHome(base) {
		t.Errorf("%q is the home directory's own name and was treated as a project", base)
	}
	if RecapProjectIsHome("") {
		t.Error("a session with no project at all was called a home directory")
	}
}

func allRecapLines(r Recap) []string {
	var out []string
	for _, s := range r.Sessions {
		out = append(out, s.Lines...)
	}
	return out
}
