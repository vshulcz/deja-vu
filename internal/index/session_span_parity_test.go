package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
