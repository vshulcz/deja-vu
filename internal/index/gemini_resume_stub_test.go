package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
