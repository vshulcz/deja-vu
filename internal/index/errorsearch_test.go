package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// writeTestStore builds a real index directory from sessions given in memory.
func writeTestStore(t *testing.T, dir string, ss ...model.Session) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
}

func mkOptions(q string) query.Options { return query.Options{Query: q, All: true} }

// A pasted error carries paths, line numbers and goroutine ids, so an AND over
// its words matches nothing and the word ladder falls through to ranking by
// whichever token happens to be rare. The session that hit that exact error is
// already fingerprinted; match on the fingerprint.
func TestPastedErrorFindsTheSessionThatHitIt(t *testing.T) {
	dir := t.TempDir()
	hit := model.Session{
		Harness: "claude", ID: "hit", Project: "p",
		Messages: []model.Message{
			{Role: "user", Text: "starting the worker"},
			{Role: "tool-output", Text: "worker.py:41: ModuleNotFoundError: No module named 'aiokafka'"},
			{Role: "command", Text: "uv pip install aiokafka"},
		},
	}
	other := model.Session{
		Harness: "claude", ID: "other", Project: "p",
		Messages: []model.Message{
			{Role: "user", Text: "unrelated work on the parser"},
			{Role: "assistant", Text: "renamed the token stream"},
		},
	}
	writeTestStore(t, dir, hit, other)

	// The paste is not the stored line: different file, different line number,
	// a traceback around it. Only the error itself is shared.
	paste := "Traceback (most recent call last):\n" +
		"  File \"/srv/app/main.py\", line 7, in <module>\n" +
		"ModuleNotFoundError: No module named 'aiokafka'\n"
	res, err := SearchDetailed(dir, mkOptions(paste))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) == 0 {
		t.Fatal("the pasted error found nothing")
	}
	if res.Sessions[0].ID != "hit" {
		t.Errorf("wrong session first: %q", res.Sessions[0].ID)
	}
}

// The tier serves the neighbourhood of the error rather than the error line
// alone: what a reader needs is the command that came after it.
func TestErrorTierCarriesWhatFollowedTheError(t *testing.T) {
	dir := t.TempDir()
	writeTestStore(t, dir, model.Session{
		Harness: "claude", ID: "hit", Project: "p",
		Messages: []model.Message{
			{Role: "user", Text: "starting the worker"},
			{Role: "tool-output", Text: "worker.py:41: ModuleNotFoundError: No module named 'aiokafka'"},
			{Role: "command", Text: "uv pip install aiokafka"},
		},
	})
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := errorSigSearch(dir, m, mkOptions("ModuleNotFoundError: No module named 'aiokafka'"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(res.Sessions))
	}
	var text string
	for _, msg := range res.Sessions[0].Messages {
		text += msg.Text + "\n"
	}
	if !strings.Contains(text, "uv pip install aiokafka") {
		t.Errorf("the recovery is not in the result:\n%s", text)
	}
}

// An ordinary question must not be routed through the error tier: it has no
// error line, so the tier stays silent and the word ladder keeps the case.
func TestOrdinaryQuestionIsNotTreatedAsAnError(t *testing.T) {
	dir := t.TempDir()
	writeTestStore(t, dir, model.Session{
		Harness: "claude", ID: "s", Project: "p",
		Messages: []model.Message{{Role: "tool-output", Text: "worker.py:41: ModuleNotFoundError: No module named 'aiokafka'"}},
	})
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := errorSigSearch(dir, m, mkOptions("how do we deploy the worker"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 0 {
		t.Errorf("a question with no error line was answered by the error tier: %+v", res.Sessions)
	}
}

// A long trace carries several error lines; the session that hit more of them
// is the closer match, ahead of a fresher session that hit only one. And the
// result reports Total/Capped honestly when more than the window matched.
func TestErrorTierRanksByHowMuchOfThePasteMatched(t *testing.T) {
	now := time.Now()
	// "broad" hits two of the pasted error lines; "narrow" is newer but hits one.
	broad := model.Session{
		Harness: "claude", ID: "broad", Project: "p", Updated: now.Add(-time.Hour),
		Messages: []model.Message{
			{Role: "tool-output", Text: "panic: runtime error: invalid memory address"},
			{Role: "tool-output", Text: "psql: connection refused on port 5432"},
			{Role: "command", Text: "restarted postgres and added a nil guard"},
		},
	}
	narrow := model.Session{
		Harness: "claude", ID: "narrow", Project: "p", Updated: now,
		Messages: []model.Message{
			{Role: "tool-output", Text: "psql: connection refused on port 5432"},
			{Role: "command", Text: "brew services start postgresql"},
		},
	}
	dir := t.TempDir()
	writeTestStore(t, dir, broad, narrow)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	paste := "panic: runtime error: invalid memory address\n...\npsql: connection refused on port 5432\n"
	res, err := errorSigSearch(dir, m, mkOptions(paste))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) < 2 {
		t.Fatalf("both sessions should match, got %d", len(res.Sessions))
	}
	if res.Sessions[0].ID != "broad" {
		t.Errorf("the session that hit more of the paste is not first: %q", res.Sessions[0].ID)
	}
	if res.Tier != query.TierRelevance {
		t.Errorf("wrong tier %q", res.Tier)
	}
}
