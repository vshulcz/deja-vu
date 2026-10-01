package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// `gemini --resume <id>` appends the resumed turns to the original transcript
// and leaves a second file with the same sessionId: a header and a $set holding
// only the <session_context> preamble, which deja strips. That stub took the
// id, and since it held nothing the row was dropped — every resumed session
// went missing from search, show and last (#4213).

const resumeID = "008140a7-3349-4cca-bc66-b32418429ebf"

const resumePreamble = `{"$set":{"messages":[{"id":"ctx","timestamp":"%s","type":"user","content":[{"text":"<session_context>\nThis is the Gemini CLI. We are setting up the context for our chat.\n</session_context>"}]}]}}`

func geminiResumeHeader(start string) string {
	return `{"sessionId":"` + resumeID + `","projectHash":"bc04","startTime":"` + start + `","lastUpdated":"` + start + `","kind":"main"}`
}

func geminiPreamble(at string) string {
	return strings.Replace(resumePreamble, "%s", at, 1)
}

// The original transcript before the resume.
func geminiOriginal() string {
	return strings.Join([]string{
		geminiResumeHeader("2026-10-01T13:04:39.847Z"),
		geminiPreamble("2026-10-01T13:04:39.849Z"),
		`{"id":"u1","timestamp":"2026-10-01T13:04:40.000Z","type":"user","content":[{"text":"the users table needs an email column"}]}`,
		`{"id":"g1","timestamp":"2026-10-01T13:04:44.698Z","type":"gemini","content":"Add the email column as nullable first."}`,
		`{"$set":{"lastUpdated":"2026-10-01T13:04:44.702Z"}}`,
	}, "\n") + "\n"
}

// What the resume appends to the original.
func geminiResumedTail() string {
	return strings.Join([]string{
		`{"$set":{"sessionId":"` + resumeID + `"}}`,
		`{"id":"u2","timestamp":"2026-10-01T13:07:52.492Z","type":"user","content":[{"text":"what about existing rows"}]}`,
		`{"id":"g2","timestamp":"2026-10-01T13:07:55.100Z","type":"gemini","content":"Add NOT NULL DEFAULT '' to the schema migration."}`,
		`{"$set":{"lastUpdated":"2026-10-01T13:07:55.101Z"}}`,
	}, "\n") + "\n"
}

// The file the resume leaves behind.
func geminiStub() string {
	return geminiResumeHeader("2026-10-01T13:07:52.403Z") + "\n" + geminiPreamble("2026-10-01T13:07:52.409Z") + "\n"
}

func geminiChats() string {
	return filepath.Join(os.Getenv("DEJA_GEMINI_ROOT"), "tmp", "proj", "chats")
}

// assertResumedSessionHeld checks the session is findable, filed under the
// transcript that holds it, and not reported as two conversations sharing an id.
func assertResumedSessionHeld(t *testing.T, dir, realPath, query string) {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	row, ok := m.Sessions["gemini:"+resumeID]
	if !ok {
		t.Fatalf("the resumed session has no row; rows: %v", m.Sessions)
	}
	if row.Path != realPath {
		t.Errorf("row is filed under %s, want the transcript holding the conversation %s", row.Path, realPath)
	}
	if row.Shared {
		t.Error("the stub was counted as a second conversation sharing the id")
	}
	ss, err := Search(dir, search.Options{Query: query, All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != resumeID {
		t.Fatalf("%q found %d sessions, want the resumed one", query, len(ss))
	}
}

func TestGeminiResumeStubDoesNotTakeTheSessionFullBuild(t *testing.T) {
	for _, tc := range []struct{ name, real, stub string }{
		// The names Gemini gives them: the stub's stamp sorts after.
		{"stub after", "session-2026-10-01T13-04-008140a7.jsonl", "session-2026-10-01T13-07-008140a7.jsonl"},
		// And the other way round, so the order the files are read in does
		// not decide it.
		{"stub first", "session-b-008140a7.jsonl", "session-a-008140a7.jsonl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			real := filepath.Join(geminiChats(), tc.real)
			write(t, real, geminiOriginal()+geminiResumedTail())
			write(t, filepath.Join(geminiChats(), tc.stub), geminiStub())
			dir := filepath.Join(tmp, "idx")
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			if n := ReportCollisions(); n != 0 {
				t.Errorf("reported %d id collisions for one resumed session", n)
			}
			// The stub is still a transcript that held nothing, whichever
			// order it was read in.
			if n := ReportEmptySessions(); n != 1 {
				t.Errorf("reported %d empty transcripts, want the stub", n)
			}
			assertResumedSessionHeld(t, dir, real, "schema migration")
		})
	}
}

// The order it happens in: the session is indexed, then the resume appends to
// it and leaves the stub, and the next pass sees both.
func TestGeminiResumeStubDoesNotTakeTheSessionIncremental(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	real := filepath.Join(geminiChats(), "session-2026-10-01T13-04-008140a7.jsonl")
	write(t, real, geminiOriginal())
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	assertResumedSessionHeld(t, dir, real, "email column")

	appendFile(t, real, geminiResumedTail())
	write(t, filepath.Join(geminiChats(), "session-2026-10-01T13-07-008140a7.jsonl"), geminiStub())
	bumpMtime(t, real)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := ReportCollisions(); n != 0 {
		t.Errorf("reported %d id collisions for one resumed session", n)
	}
	assertResumedSessionHeld(t, dir, real, "schema migration")
}

// The stub on its own in a later pass, with the original unchanged, and the
// reverse: a stub already indexed when the original arrives.
func TestGeminiResumeStubAloneInALaterPass(t *testing.T) {
	t.Run("stub after", func(t *testing.T) {
		tmp := hermeticIndexEnv(t)
		real := filepath.Join(geminiChats(), "session-b-008140a7.jsonl")
		write(t, real, geminiOriginal()+geminiResumedTail())
		dir := filepath.Join(tmp, "idx")
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(geminiChats(), "session-a-008140a7.jsonl"), geminiStub())
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		if n := ReportCollisions(); n != 0 {
			t.Errorf("reported %d id collisions for one resumed session", n)
		}
		assertResumedSessionHeld(t, dir, real, "schema migration")
	})
	t.Run("stub first", func(t *testing.T) {
		tmp := hermeticIndexEnv(t)
		// Something else indexed first, so the stub arrives in an update
		// rather than the first build.
		write(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "other.jsonl"),
			claudeLine("s-other", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
		dir := filepath.Join(tmp, "idx")
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(geminiChats(), "session-z-008140a7.jsonl"), geminiStub())
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		real := filepath.Join(geminiChats(), "session-b-008140a7.jsonl")
		write(t, real, geminiOriginal()+geminiResumedTail())
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		if n := ReportCollisions(); n != 0 {
			t.Errorf("reported %d id collisions for one resumed session", n)
		}
		assertResumedSessionHeld(t, dir, real, "schema migration")
	})
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func bumpMtime(t *testing.T, path string) {
	t.Helper()
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

// The original deleted — Gemini's own cleanup, or by hand — in the same pass
// the stub arrives beside it. The stub is not the transcript moving: the
// rename rule took it for one, the deleted file's records went, and the row
// was left on a file with nothing in it, even after a rebuild.
func TestGeminiResumeStubIsNotARenameOfADeletedTranscript(t *testing.T) {
	// Alone, the pass takes the append path; with another Gemini transcript
	// changed beside it, the replacement path and its rename rule.
	for _, withChange := range []bool{false, true} {
		t.Run(map[bool]string{false: "append path", true: "replacement path"}[withChange], func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			real := filepath.Join(geminiChats(), "session-2026-10-01T13-04-008140a7.jsonl")
			write(t, real, geminiOriginal()+geminiResumedTail())
			other := filepath.Join(geminiChats(), "session-2026-09-30T10-00-0badc0de.jsonl")
			otherHeader := `{"sessionId":"0badc0de","startTime":"2026-09-30T10:00:00Z","lastUpdated":"2026-09-30T10:00:00Z"}` + "\n"
			write(t, other, otherHeader+`{"id":"o1","timestamp":"2026-09-30T10:00:01Z","type":"user","content":[{"text":"the cache warms on boot"}]}`+"\n")
			dir := filepath.Join(tmp, "idx")
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(real); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(geminiChats(), "session-2026-10-01T13-07-008140a7.jsonl"), geminiStub())
			if withChange {
				appendFile(t, other, `{"id":"o2","timestamp":"2026-09-30T10:00:02Z","type":"gemini","content":"It does."}`+"\n")
				bumpMtime(t, other)
			}
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			assertResumedSessionHeld(t, dir, real, "schema migration")
			if err := Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			assertResumedSessionHeld(t, dir, real, "schema migration")
		})
	}
}

// The empty-transcript rule is for pairs that would be reported as a clash.
// A store pair attributeSession already knows keeps its own answer, and a held
// row with text but no words (emoji, punctuation) is still a conversation.
func TestClaimSessionOnlyOverridesAClash(t *testing.T) {
	empty := model.Session{Harness: "goose", ID: "g", Path: "/h/.local/share/goose/sessions/sessions.db",
		Messages: []model.Message{{Role: "user", Text: "  "}}}
	held := SessionMeta{Harness: "goose", ID: "g", Path: "/h/.local/share/goose/sessions/g.jsonl", Words: 3}
	if owns, collided := claimSession(held, empty); !owns || collided {
		t.Errorf("goose db over its jsonl: owns=%v collided=%v, want the store rule (owns, no clash)", owns, collided)
	}

	text := model.Session{Harness: "gemini", ID: "x", Path: "/c/session-a-x.jsonl",
		Messages: []model.Message{{Role: "user", Text: "the pool deadlocked"}}}
	emoji := SessionMeta{Harness: "gemini", ID: "x", Path: "/c/session-b-x.jsonl", Words: 0}
	if _, collided := claimSession(emoji, text); !collided {
		t.Error("a held row of emoji gave up the id without a clash being reported")
	}
	stub := SessionMeta{Harness: "gemini", ID: "x", Path: "/c/session-b-x.jsonl", NoText: true}
	if owns, collided := claimSession(stub, text); !owns || collided {
		t.Errorf("over a row with no text: owns=%v collided=%v, want owns, no clash", owns, collided)
	}
}

// The original deleted while the stub beside it stays, then the stub gains a
// turn of its own. Now an arrival with text in the same directory, it was
// taken for the deleted transcript moving, and the transcript's records went
// for good — a rebuild has nothing to bring them back from.
func TestGeminiResumedStubIsNotARenameOfTheDeletedTranscript(t *testing.T) {
	for _, withChange := range []bool{false, true} {
		t.Run(map[bool]string{false: "append path", true: "replacement path"}[withChange], func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			real := filepath.Join(geminiChats(), "session-2026-10-01T13-04-008140a7.jsonl")
			stub := filepath.Join(geminiChats(), "session-2026-10-01T13-07-008140a7.jsonl")
			write(t, real, geminiOriginal()+geminiResumedTail())
			write(t, stub, geminiStub())
			other := filepath.Join(geminiChats(), "session-2026-09-30T10-00-0badc0de.jsonl")
			write(t, other, `{"sessionId":"0badc0de","startTime":"2026-09-30T10:00:00Z","lastUpdated":"2026-09-30T10:00:00Z"}`+"\n"+
				`{"id":"o1","timestamp":"2026-09-30T10:00:01Z","type":"user","content":[{"text":"the cache warms on boot"}]}`+"\n")
			dir := filepath.Join(tmp, "idx")
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(real); err != nil {
				t.Fatal(err)
			}
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			appendFile(t, stub, `{"id":"u3","timestamp":"2026-10-01T13:09:00.000Z","type":"user","content":[{"text":"backfill the tenants table next"}]}`+"\n")
			bumpMtime(t, stub)
			if withChange {
				appendFile(t, other, `{"id":"o2","timestamp":"2026-09-30T10:00:02Z","type":"gemini","content":"It does."}`+"\n")
				bumpMtime(t, other)
			}
			found := func(stage string) {
				t.Helper()
				for _, q := range []string{"schema migration", "tenants table"} {
					ss, err := Search(dir, search.Options{Query: q, All: true})
					if err != nil {
						t.Fatal(err)
					}
					if len(ss) != 1 || ss[0].ID != resumeID {
						t.Errorf("%s: %q found %d sessions, want the resumed one", stage, q, len(ss))
					}
				}
			}
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			found("after the stub grew")
			if err := Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			found("after a rebuild")
		})
	}
}

// The stub indexed on its own, then the transcript arrives. The row changes
// hands; folding the transcript into the stub's row counted the stub's
// preamble on top, one more message than a rebuild of the same files.
func TestGeminiTranscriptTakingAStubRowMatchesARebuild(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	write(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "other.jsonl"),
		claudeLine("s-other", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(geminiChats(), "session-z-008140a7.jsonl"), geminiStub())
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(geminiChats(), "session-b-008140a7.jsonl"), geminiOriginal()+geminiResumedTail())
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	row := func() SessionMeta {
		t.Helper()
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return m.Sessions["gemini:"+resumeID]
	}
	got := row()
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	want := row()
	if got.Counted != want.Counted || got.Words != want.Words || got.LastMsg != want.LastMsg || got.NoText != want.NoText || !got.Started.Equal(want.Started) || got.Path != want.Path {
		t.Errorf("row after the transcript arrived: Counted=%d Words=%d Started=%v Path=%s; a rebuild gives Counted=%d Words=%d Started=%v Path=%s",
			got.Counted, got.Words, got.Started, got.Path, want.Counted, want.Words, want.Started, want.Path)
	}
}
