package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestLooksLikeQuestion(t *testing.T) {
	for _, s := range []string{
		"did we fix the jwks cache stampede before?",
		"why does the pool exhaust under load",
		"сколько по времени займут такие прогоны",
		"репозиторий готов к публикации?",
	} {
		if !looksLikeQuestion(s) {
			t.Errorf("%q is a question", s)
		}
	}
	// Instructions to an agent repeat verbatim across sessions by construction,
	// which is exactly why they must not count as questions someone asked.
	for _, s := range []string{
		"Use the deja recall tool to search for: openclaw harness",
		"Call the build_check tool with scope all",
		"Reply with only the raw JSON returned by the tool",
		"Continue if you have next steps, or stop and ask for clarification",
		"",
		"   ",
	} {
		if looksLikeQuestion(s) {
			t.Errorf("%q is not a question someone asked", s)
		}
	}
}

func TestNotAskedRejectsHarnessText(t *testing.T) {
	for _, s := range []string{
		"<system-reminder>do the thing</system-reminder>",
		"[Request interrupted by user]",
		"The following tool was executed by the user",
		"This session is being continued from a previous conversation",
		"Continue from where you left off.",
		"<deja-recall>...</deja-recall>",
	} {
		if !notAsked(s) {
			t.Errorf("%q is written by a harness, not a person", s)
		}
	}
	if notAsked("why is the index rebuilding on every run?") {
		t.Error("a real question should survive")
	}
}

func TestQuestionStemNeedsSubstance(t *testing.T) {
	if got := questionStem("Why  does THE pool exhaust, under load?"); got != "why does the pool exhaust under load" {
		t.Fatalf("got %q", got)
	}
	if questionStem("ok thanks") != "" {
		t.Fatal("four words or fewer is not a question worth matching")
	}
}

func TestAskedHashesAreStableAndBounded(t *testing.T) {
	q := "why does the connection pool exhaust under load?"
	a := askedHashes([]model.Message{{Role: "user", Text: q}})
	b := askedHashes([]model.Message{{Role: "user", Text: "  WHY does the connection POOL exhaust, under load? "}})
	if len(a) != 1 || len(b) != 1 || a[0] != b[0] {
		t.Fatalf("the same question asked twice must hash alike: %v vs %v", a, b)
	}
	// Assistant turns are not questions the user asked.
	if got := askedHashes([]model.Message{{Role: "assistant", Text: q}}); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	var many []model.Message
	for i := 0; i < 30; i++ {
		many = append(many, model.Message{Role: "user", Text: "why does thing number " + string(rune('a'+i)) + " fail here?"})
	}
	if got := askedHashes(many); len(got) != askedQuestionCap {
		t.Fatalf("stored %d hashes, want the cap of %d", len(got), askedQuestionCap)
	}
	// The same question twice in one session is one question.
	if got := askedHashes([]model.Message{{Role: "user", Text: q}, {Role: "user", Text: q}}); len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

// askedFixture writes real transcripts and indexes them, the way the other
// tests in this package do: FindAskedTwice reads the manifest the ingest wrote,
// so a hand-built store would test something else.
func askedFixture(t *testing.T, sessions map[string][]string, when map[string]string) string {
	t.Helper()
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, msgs := range sessions {
		var b strings.Builder
		for _, m := range msgs {
			fmt.Fprintf(&b, `{"type":"user","sessionId":%q,"cwd":"/tmp/app","timestamp":%q,"message":{"role":"user","content":%q}}`+"\n",
				id, when[id], m)
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFindAskedTwicePicksTheWidestSpan(t *testing.T) {
	// The same question two months apart, and another asked twice in one
	// afternoon. The second is a person retrying; the first is the one worth a
	// line on a screen.
	q := "why does the connection pool exhaust under load?"
	flaky := "how do i make the flaky test stop failing?"
	dir := askedFixture(t,
		map[string][]string{
			"a": {q},
			"b": {q},
			"c": {flaky},
			"d": {flaky},
		},
		map[string]string{
			"a": "2026-03-01T10:00:00Z",
			"b": "2026-05-01T10:00:00Z",
			"c": "2026-05-02T10:00:00Z",
			"d": "2026-05-02T14:00:00Z",
		})
	got, ok := FindAskedTwice(dir)
	if !ok {
		t.Fatal("a question asked two months apart should be found")
	}
	if got.Text != q {
		t.Fatalf("got %q", got.Text)
	}
	if len(got.Sessions) != 2 || !got.Sessions[0].Updated.After(got.Sessions[1].Updated) {
		t.Fatalf("sessions should come back newest first: %+v", got.Sessions)
	}
}

func TestFindAskedTwiceStaysQuietWithoutARepeat(t *testing.T) {
	dir := askedFixture(t,
		map[string][]string{"a": {"why does the pool exhaust under load?"}},
		map[string]string{"a": "2026-03-01T10:00:00Z"})
	if _, ok := FindAskedTwice(dir); ok {
		t.Fatal("one asking is not a repeat")
	}
	if _, ok := FindAskedTwice(t.TempDir()); ok {
		t.Fatal("an empty store has nothing to say")
	}
}

func TestPrefixMatchesCounts(t *testing.T) {
	dir := askedFixture(t,
		map[string][]string{
			"aa1": {"why does the pool exhaust under load?"},
			"aa2": {"why does the cache stampede?"},
			"bb1": {"why is the build slow?"},
		},
		map[string]string{
			"aa1": "2026-03-01T10:00:00Z",
			"aa2": "2026-03-02T10:00:00Z",
			"bb1": "2026-03-03T10:00:00Z",
		})
	if got := PrefixMatches(dir, "aa"); got != 2 {
		t.Fatalf("PrefixMatches(aa) = %d, want 2", got)
	}
	if got := PrefixMatches(dir, "aa1"); got != 1 {
		t.Fatalf("an exact id matches once, got %d", got)
	}
	if got := PrefixMatches(dir, "zz"); got != 0 {
		t.Fatalf("got %d, want none", got)
	}
	if got := PrefixMatches(dir, ""); got != 0 {
		t.Fatal("an empty prefix is not a question worth answering")
	}
	// FindByPrefix keeps picking the newest of the matches.
	s, ok, err := FindByPrefix(dir, "aa")
	if err != nil || !ok || s.ID != "aa2" {
		t.Fatalf("got %q ok=%v err=%v, want the newest match", s.ID, ok, err)
	}
}
