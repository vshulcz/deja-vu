package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// scrubStore writes one claude transcript that pasted a connection URL with a
// password in it, ages the file past the busy window, and builds the index the
// report reads.
func scrubStore(t *testing.T) (dir, transcript string) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-w-s")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","sessionId":"leaky-1","cwd":"/w/s","timestamp":"2026-07-02T10:00:00Z","message":{"role":"user","content":"staging is down, here is the dsn: postgres://svc:hunter2pass@db.internal:5432/app"}}` + "\n" +
		`{"type":"assistant","sessionId":"leaky-1","cwd":"/w/s","timestamp":"2026-07-02T10:01:00Z","message":{"role":"assistant","content":"the build id is 9f2c4b7ae1d8306f5b4c2a190e7d6f38b1c4e5a2 and the retry budget stays at three"}}` + "\n"
	transcript = filepath.Join(store, "leaky-1.jsonl")
	if err := os.WriteFile(transcript, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(transcript, old, old); err != nil {
		t.Fatal(err)
	}
	dir = index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir, transcript
}

// The report named a file and a kind; scrub puts the marker the index uses where
// the value was, keeps a copy beside it, and leaves the rest of the line intact.
func TestScrubRewritesWhatTheReportNamed(t *testing.T) {
	dir, transcript := scrubStore(t)
	var out strings.Builder
	if err := runSecretsScrub(dir, false, &out); err != nil {
		t.Fatalf("scrub: %v", err)
	}
	after, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "hunter2pass") {
		t.Errorf("the password is still in the transcript:\n%s", after)
	}
	if !strings.Contains(string(after), "[redacted:url-credentials]") {
		t.Errorf("the marker the index uses is not there:\n%s", after)
	}
	if !strings.Contains(string(after), "staging is down") || !strings.Contains(string(after), "db.internal:5432") {
		t.Errorf("the rest of the message did not survive:\n%s", after)
	}
	// Never widened: the hex build id is the entropy rule's business, and the
	// report does not name it.
	if !strings.Contains(string(after), "9f2c4b7ae1d8306f5b4c2a190e7d6f38b1c4e5a2") {
		t.Errorf("a counted rule was applied to someone's history:\n%s", after)
	}
	backups := scrubBackupsIn(filepath.Dir(transcript))
	if len(backups) != 1 {
		t.Fatalf("copies beside the file: %v", backups)
	}
	orig, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(orig), "hunter2pass") {
		t.Errorf("the copy is not the original:\n%s", orig)
	}
	if !strings.Contains(out.String(), "url-credentials") || !strings.Contains(out.String(), "rewrote") {
		t.Errorf("the run said:\n%s", out.String())
	}
}

// --dry-run says what it would do and touches nothing, which is what a person
// runs first on their own history.
func TestScrubDryRunChangesNothing(t *testing.T) {
	dir, transcript := scrubStore(t)
	before, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runSecretsScrub(dir, true, &out); err != nil {
		t.Fatalf("scrub --dry-run: %v", err)
	}
	after, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("the dry run rewrote the file:\n%s", after)
	}
	if got := scrubBackupsIn(filepath.Dir(transcript)); len(got) != 0 {
		t.Errorf("the dry run left copies: %v", got)
	}
	if !strings.Contains(out.String(), "would replace") || !strings.Contains(out.String(), "--scrub") {
		t.Errorf("the dry run said:\n%s", out.String())
	}
}

// A transcript an agent is inside is append-only and held open, so rewriting it
// is how a conversation gets truncated. Both signals are checked: the mark the
// hooks leave, and a file written seconds ago on a machine with no hooks.
func TestScrubRefusesALiveTranscript(t *testing.T) {
	dir, transcript := scrubStore(t)
	markSessionLive(dir, "leaky-1")
	var out strings.Builder
	if err := runSecretsScrub(dir, false, &out); err != nil {
		t.Fatalf("scrub: %v", err)
	}
	if b, _ := os.ReadFile(transcript); !strings.Contains(string(b), "hunter2pass") {
		t.Error("a live session's transcript was rewritten")
	}
	if !strings.Contains(out.String(), "an agent is in that session now") {
		t.Errorf("the refusal does not say why:\n%s", out.String())
	}

	dir2, transcript2 := scrubStore(t)
	now := time.Now()
	if err := os.Chtimes(transcript2, now, now); err != nil {
		t.Fatal(err)
	}
	var out2 strings.Builder
	if err := runSecretsScrub(dir2, false, &out2); err != nil {
		t.Fatalf("scrub: %v", err)
	}
	if b, _ := os.ReadFile(transcript2); !strings.Contains(string(b), "hunter2pass") {
		t.Error("a transcript written seconds ago was rewritten")
	}
	if !strings.Contains(out2.String(), "still being written") {
		t.Errorf("the refusal does not say why:\n%s", out2.String())
	}
}

// A database store has no per-session file, so scrub cannot reach it — and has
// to say so rather than silently doing less than the report showed.
func TestScrubSaysWhatItCannotReach(t *testing.T) {
	findings := []index.SecretFinding{
		{Kind: "url-credentials", Harness: "opencode", ID: "ses_1", Count: 1},
		{Kind: "bearer-token", Harness: "zed", ID: "z1", Count: 2},
		{Kind: "entropy", Harness: "claude", ID: "c1", Path: "/does/not/matter", Count: 9},
	}
	targets, unreachable := scrubTargets(findings)
	if len(targets) != 0 {
		t.Errorf("a database store produced targets: %v", targets)
	}
	if unreachable["no transcript file — the store is a database"] != 2 {
		t.Errorf("the database findings were counted as %v", unreachable)
	}
	if unreachable["not a named rule"] != 1 {
		t.Errorf("a counted rule was not reported as out of reach: %v", unreachable)
	}
	var out strings.Builder
	printScrubUnreachable(&out, unreachable)
	if !strings.Contains(out.String(), "2 findings out of reach") {
		t.Errorf("the tail reads:\n%s", out.String())
	}
}

// The report reads the index, which redacts decoded message text; a value the
// raw file spells differently is not reachable from here, and the honest answer
// is to leave the file alone and say nothing matched.
func TestScrubLeavesAFileItCannotMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "odd.jsonl")
	body := `{"text":"no credential shape in here at all"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	got := scrubOne(scrubTarget{path: path, id: "x", harn: "claude", kinds: map[string]bool{"url-credentials": true}}, nil, false)
	if got.skipped == "" {
		t.Errorf("a file with nothing to match was rewritten: %+v", got)
	}
	if b, _ := os.ReadFile(path); string(b) != body {
		t.Errorf("the file changed:\n%s", b)
	}
	if len(scrubBackupsIn(dir)) != 0 {
		t.Error("a copy was left beside a file nothing was done to")
	}
}

// A symlinked transcript is left alone: a rename over a link replaces the link
// and leaves what it pointed at holding the credential, which is the opposite of
// what the reader asked for.
func TestScrubWillNotFollowASymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.jsonl")
	if err := os.WriteFile(real, []byte(`{"t":"postgres://svc:hunter2pass@db:5432/app"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	targets, unreachable := scrubTargets([]index.SecretFinding{
		{Kind: "url-credentials", Harness: "claude", ID: "s1", Path: link, Count: 1},
	})
	if len(targets) != 0 {
		t.Errorf("a symlink was taken as a transcript to rewrite: %v", targets)
	}
	if unreachable["the transcript is a symlink — edit what it points at"] != 1 {
		t.Errorf("the symlink was counted as %v", unreachable)
	}
	if b, _ := os.ReadFile(real); !strings.Contains(string(b), "hunter2pass") {
		t.Error("what the link pointed at was rewritten")
	}
}

// The report counts markers in the index, which redacts decoded message text;
// the scrub reads raw bytes. When it reaches fewer than the report counted, the
// reader has to be told, or they close a file that still holds a value.
func TestScrubSaysWhenItReachedFewerThanTheReportCounted(t *testing.T) {
	var out strings.Builder
	printScrub(&out, []scrubOutcome{{
		target:  scrubTarget{path: "/w/s/leaky.jsonl", found: 3},
		written: map[string]int{"url-credentials": 1},
	}}, nil, false)
	if !strings.Contains(out.String(), "1 of the 3 the report counted here") {
		t.Errorf("the gap is invisible:\n%s", out.String())
	}
}

// A failed rewrite must name the copy it left behind: it is the only thing
// between the reader and a half-written transcript.
func TestAFailedRewriteNamesTheCopy(t *testing.T) {
	var out strings.Builder
	printScrub(&out, []scrubOutcome{{
		target:  scrubTarget{path: "/w/s/leaky.jsonl"},
		skipped: "could not replace it: read-only file system — the original is still at /w/s/leaky.jsonl.deja-backup-20260924-101500",
	}}, nil, false)
	if !strings.Contains(out.String(), "deja-backup-20260924-101500") {
		t.Errorf("the copy is not named:\n%s", out.String())
	}
}
