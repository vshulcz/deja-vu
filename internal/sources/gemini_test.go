package sources

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func geminiTree(t *testing.T) (root, chats string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("DEJA_GEMINI_ROOT", root)
	chats = filepath.Join(root, "tmp", "my-proj", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, chats
}

func TestParseGeminiOldJSON(t *testing.T) {
	_, chats := geminiTree(t)
	doc := `{"sessionId":"session-old-1","projectHash":"abc","startTime":"2026-01-16T18:34:00.000Z","lastUpdated":"2026-01-16T18:35:00.000Z","messages":[
	 {"id":"m1","timestamp":"2026-01-16T18:34:01.000Z","type":"user","content":"Hello gemneedle"},
	 {"id":"m2","timestamp":"2026-01-16T18:34:02.000Z","type":"gemini","model":"gemini-3-flash","content":"Hi there!"},
	 {"id":"m3","timestamp":"2026-01-16T18:34:03.000Z","type":"info","content":"noise"}]}`
	p := filepath.Join(chats, "session-old-1.json")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "session-old-1" || ss[0].Harness != "gemini" {
		t.Fatalf("bad session: %#v", ss)
	}
	if len(ss[0].Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (info skipped): %#v", len(ss[0].Messages), ss[0].Messages)
	}
	if ss[0].Messages[1].Role != "assistant" || ss[0].Messages[1].Text != "Hi there!" {
		t.Fatalf("assistant wrong: %#v", ss[0].Messages[1])
	}
	if ss[0].Project != "my-proj" {
		t.Fatalf("project = %q", ss[0].Project)
	}
}

func TestParseGeminiJSONLWithRewind(t *testing.T) {
	_, chats := geminiTree(t)
	lines := `{"sessionId":"sess-new-1","projectHash":"abc","startTime":"2026-07-01T10:00:00.000Z","lastUpdated":"2026-07-01T10:00:00.000Z","kind":"main"}
{"id":"u1","timestamp":"2026-07-01T10:00:01.000Z","type":"user","content":[{"text":"first question"}]}
{"id":"a1","timestamp":"2026-07-01T10:00:02.000Z","type":"gemini","content":"wrong answer","model":"gemini-3"}
{"$rewindTo":"a1"}
{"id":"a2","timestamp":"2026-07-01T10:00:05.000Z","type":"gemini","content":"right answer","model":"gemini-3"}
{"$set":{"lastUpdated":"2026-07-01T10:00:06.000Z"}}
`
	p := filepath.Join(chats, "session-2026-07-01T10-00-sess-new-1.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "sess-new-1" {
		t.Fatalf("bad session: %#v", ss)
	}
	m := ss[0].Messages
	if len(m) != 2 {
		t.Fatalf("messages = %d, want 2 (rewind dropped wrong answer): %#v", len(m), m)
	}
	if m[0].Text != "first question" || m[1].Text != "right answer" {
		t.Fatalf("rewind replay wrong: %#v", m)
	}
	if ss[0].Updated.Second() != 6 {
		t.Fatalf("$set lastUpdated not applied: %v", ss[0].Updated)
	}
}

func TestGeminiProjectFromRegistry(t *testing.T) {
	root, chats := geminiTree(t)
	if err := os.WriteFile(filepath.Join(root, "projects.json"), []byte(`{"projects":{"/Users/x/work/cool-app":"my-proj"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := `{"sessionId":"s1","startTime":"2026-01-16T18:34:00.000Z","lastUpdated":"2026-01-16T18:34:00.000Z","messages":[{"id":"m1","timestamp":"2026-01-16T18:34:01.000Z","type":"user","content":"q"}]}`
	p := filepath.Join(chats, "session-s1.json")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ss=%v err=%v", ss, err)
	}
	if ss[0].Project != "work/cool-app" {
		t.Fatalf("project = %q, want work/cool-app", ss[0].Project)
	}
}

func TestGeminiDedupePrefersJSONL(t *testing.T) {
	in := []model.Session{
		{Harness: "gemini", ID: "dup1", Path: "/a/session-dup1.json"},
		{Harness: "gemini", ID: "dup1", Path: "/a/session-dup1.jsonl"},
		{Harness: "gemini", ID: "solo", Path: "/a/session-solo.json"},
	}
	out := dedupeGeminiSessions(in)
	if len(out) != 2 {
		t.Fatalf("deduped to %d, want 2", len(out))
	}
	if out[0].ID != "dup1" || !strings.HasSuffix(out[0].Path, ".jsonl") {
		t.Fatalf("jsonl not preferred: %#v", out[0])
	}
}

// Resuming a .jsonl session leaves a second .jsonl under its id holding only
// the preamble. Neither replaces the other here, and a .jsonl with fewer
// messages than the .json it shares an id with does not replace it (#4213).
func TestGeminiDedupeKeepsAResumedTranscript(t *testing.T) {
	msgs := func(n int) []model.Message { return make([]model.Message, n) }
	out := dedupeGeminiSessions([]model.Session{
		{Harness: "gemini", ID: "r", Path: "/a/session-13-04-r.jsonl", Messages: msgs(6)},
		{Harness: "gemini", ID: "r", Path: "/a/session-13-07-r.jsonl", Messages: msgs(1)},
	})
	if len(out) != 2 {
		t.Fatalf("kept %d of two .jsonl transcripts sharing an id, want both", len(out))
	}
	out = dedupeGeminiSessions([]model.Session{
		{Harness: "gemini", ID: "o", Path: "/a/session-o.json", Messages: msgs(6)},
		{Harness: "gemini", ID: "o", Path: "/a/session-o-stub.jsonl", Messages: msgs(1)},
	})
	if len(out) != 1 || !strings.HasSuffix(out[0].Path, ".json") {
		t.Fatalf("a preamble-only .jsonl replaced the .json holding the conversation: %#v", out)
	}
	out = dedupeGeminiSessions([]model.Session{
		{Harness: "gemini", ID: "o", Path: "/a/session-o.jsonl", Messages: msgs(6)},
		{Harness: "gemini", ID: "o", Path: "/a/session-o.json", Messages: msgs(6)},
	})
	if len(out) != 1 || !strings.HasSuffix(out[0].Path, ".jsonl") {
		t.Fatalf("the .json replaced its .jsonl rewrite: %#v", out)
	}
	// A rewind after the rewrite leaves the .jsonl shorter than the .json it
	// came from; it is still the current one, in either order.
	for _, in := range [][]model.Session{
		{{Harness: "gemini", ID: "w", Path: "/a/session-w.json", Messages: msgs(6)}, {Harness: "gemini", ID: "w", Path: "/a/session-w.jsonl", Messages: msgs(3)}},
		{{Harness: "gemini", ID: "w", Path: "/a/session-w.jsonl", Messages: msgs(3)}, {Harness: "gemini", ID: "w", Path: "/a/session-w.json", Messages: msgs(6)}},
	} {
		out = dedupeGeminiSessions(in)
		if len(out) != 1 || out[0].Path != "/a/session-w.jsonl" {
			t.Fatalf("the stale .json won over its rewound .jsonl rewrite: %#v", out)
		}
	}
	// And with the resume stub beside them, the rewrite still pairs with the
	// file it was named after.
	out = dedupeGeminiSessions([]model.Session{
		{Harness: "gemini", ID: "w", Path: "/a/session-a-stub.jsonl", Messages: msgs(1)},
		{Harness: "gemini", ID: "w", Path: "/a/session-w.json", Messages: msgs(6)},
		{Harness: "gemini", ID: "w", Path: "/a/session-w.jsonl", Messages: msgs(3)},
	})
	var paths []string
	for _, s := range out {
		paths = append(paths, s.Path)
	}
	if got := strings.Join(paths, " "); !strings.Contains(got, "/a/session-w.jsonl") || strings.Contains(got, "/a/session-w.json ") || strings.HasSuffix(got, ".json") {
		t.Fatalf("stub, .json and its rewrite kept %s, want the rewrite and not the .json", got)
	}
}

// With a resume stub and the real .jsonl both held, an older .json named after
// neither is weighed against the conversation, not against whichever .jsonl
// happened to be read first: read stub-first it replaced the stub and sat
// beside the transcript it is an older copy of.
func TestGeminiDedupeIgnoresReadOrder(t *testing.T) {
	msgs := func(n int) []model.Message { return make([]model.Message, n) }
	files := []model.Session{
		{Harness: "gemini", ID: "x", Path: "/a/a-stub.jsonl", Messages: msgs(1)},
		{Harness: "gemini", ID: "x", Path: "/a/b-real.jsonl", Messages: msgs(12)},
		{Harness: "gemini", ID: "x", Path: "/a/c-old.json", Messages: msgs(5)},
	}
	for _, order := range [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		var in []model.Session
		for _, i := range order {
			in = append(in, files[i])
		}
		var paths []string
		for _, s := range dedupeGeminiSessions(in) {
			paths = append(paths, s.Path)
		}
		sort.Strings(paths)
		if got := strings.Join(paths, " "); got != "/a/a-stub.jsonl /a/b-real.jsonl" {
			t.Errorf("read in order %v kept %s, want the stub and the real .jsonl", order, got)
		}
	}
}

func TestGeminiMessageTimeFallback(t *testing.T) {
	_, chats := geminiTree(t)
	doc := `{"sessionId":"s1","startTime":"2026-07-15T10:00:00.000Z","lastUpdated":"2026-07-15T10:00:00.000Z","messages":[{"id":"m1","type":"user","content":"no timestamp here"}]}`
	p := filepath.Join(chats, "session-s1.json")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("ss=%#v err=%v", ss, err)
	}
	if ss[0].Messages[0].Time.IsZero() || ss[0].Messages[0].Time != ss[0].Started {
		t.Fatalf("message time not defaulted to start: %v vs %v", ss[0].Messages[0].Time, ss[0].Started)
	}
}

// On --resume Gemini writes its rebuilt history back as a $set snapshot, and
// that history leaves out user turns starting with <hook_context> — the
// prompts deja's per-prompt recall was attached to. The turn stays indexed
// (#4214). Shape from Gemini CLI 0.60.0.
func TestGeminiResumeSnapshotKeepsTheHookedPrompt(t *testing.T) {
	_, chats := geminiTree(t)
	lines := `{"sessionId":"sess-resume-1","projectHash":"abc","startTime":"2026-10-01T13:04:39.847Z","lastUpdated":"2026-10-01T13:04:39.847Z","kind":"main"}
{"$set":{"messages":[{"id":"ctx","timestamp":"2026-10-01T13:04:39.850Z","type":"user","content":[{"text":"<session_context>\nThis is the Gemini CLI.</session_context>"}]}],"lastUpdated":"2026-10-01T13:04:39.850Z"}}
{"id":"u1","timestamp":"2026-10-01T13:04:40.000Z","type":"user","content":[{"text":"<hook_context>&lt;deja-recall&gt;old&lt;/deja-recall&gt;</hook_context>\n\nthe tidewren migration fails on null defaults"}]}
{"id":"g1","timestamp":"2026-10-01T13:04:41.000Z","type":"gemini","content":"add NOT NULL DEFAULT ''"}
{"$set":{"sessionId":"sess-resume-1"}}
{"$set":{"messages":[{"id":"ctx","timestamp":"2026-10-01T13:04:39.850Z","type":"user","content":[{"text":"<session_context>\nThis is the Gemini CLI.</session_context>"}]},{"id":"g1","timestamp":"2026-10-01T13:04:41.000Z","type":"gemini","content":[{"text":"add NOT NULL DEFAULT ''"}]}],"lastUpdated":"2026-10-01T13:07:52.000Z"}}
{"id":"u2","timestamp":"2026-10-01T13:07:52.500Z","type":"user","content":[{"text":"what was the fix?"}]}
{"id":"g2","timestamp":"2026-10-01T13:07:55.000Z","type":"gemini","content":"NOT NULL DEFAULT ''"}
`
	p := filepath.Join(chats, "session-2026-10-01T13-04-sess-resume-1.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var all []string
	for _, m := range ss[0].Messages {
		all = append(all, m.Role+": "+m.Text)
	}
	got := strings.Join(all, " | ")
	if !strings.Contains(got, "tidewren migration fails") {
		t.Fatalf("the prompt before the resume was lost: %s", got)
	}
	if strings.Count(got, "add NOT NULL DEFAULT ''") != 1 || !strings.Contains(got, "what was the fix?") {
		t.Fatalf("turns doubled or missing after the snapshot: %s", got)
	}
	if i, j := strings.Index(got, "tidewren"), strings.Index(got, "what was the fix?"); i > j {
		t.Fatalf("turns out of order: %s", got)
	}
}

// Compression, a split tool turn and a turn rewritten under its id all reach
// the reader as Gemini's own history; the snapshot is taken in its order and
// nothing is doubled (#4214 review).
func TestGeminiSnapshotsFollowGeminisHistory(t *testing.T) {
	_, chats := geminiTree(t)
	parse := func(name, lines string) string {
		t.Helper()
		p := filepath.Join(chats, name)
		if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseGeminiFile(p)
		if err != nil || len(ss) != 1 {
			t.Fatalf("parse %s: %v, %d sessions", name, err, len(ss))
		}
		var all []string
		for _, m := range ss[0].Messages {
			all = append(all, m.Text)
		}
		return strings.Join(all, " | ")
	}
	head := `{"sessionId":"s-%s","projectHash":"abc","startTime":"2026-10-01T13:00:00.000Z","lastUpdated":"2026-10-01T13:00:00.000Z","kind":"main"}` + "\n"
	msg := func(id, typ, text string) string {
		return `{"id":"` + id + `","timestamp":"2026-10-01T13:00:01.000Z","type":"` + typ + `","content":[{"text":"` + text + `"}]}`
	}
	set := func(ms ...string) string { return `{"$set":{"messages":[` + strings.Join(ms, ",") + `]}}` + "\n" }

	// Compression: the kept tail comes back under new ids.
	got := parse("session-2026-10-01T13-00-s-compress.jsonl", strings.Replace(head, "%s", "compress", 1)+
		msg("u1", "user", "first question alpha")+"\n"+msg("g1", "gemini", "first answer alpha")+"\n"+
		msg("u2", "user", "second question beta")+"\n"+msg("g2", "gemini", "second answer beta")+"\n"+
		set(msg("n1", "user", "summary of earlier"), msg("n2", "user", "second question beta"), msg("n3", "gemini", "second answer beta"))+
		msg("u3", "user", "third")+"\n")
	if strings.Count(got, "second question beta") != 1 || !strings.HasSuffix(got, "third") {
		t.Errorf("compression doubled the tail: %s", got)
	}

	// A turn new to the snapshot lands where the snapshot puts it.
	got = parse("session-2026-10-01T13-00-s-split.jsonl", strings.Replace(head, "%s", "split", 1)+
		msg("q1", "user", "q one")+"\n"+msg("a1", "gemini", "a one")+"\n"+msg("q2", "user", "q two")+"\n"+
		set(msg("q1", "user", "q one"), msg("mid", "gemini", "the middle"), msg("a1", "gemini", "a one"), msg("q2", "user", "q two")))
	if got != "q one | the middle | a one | q two" {
		t.Errorf("snapshot order lost: %s", got)
	}

	// The same id written twice is one turn.
	got = parse("session-2026-10-01T13-00-s-rewrite.jsonl", strings.Replace(head, "%s", "rewrite", 1)+
		msg("q1", "user", "q one")+"\n"+msg("a1", "gemini", "thinking")+"\n"+msg("a1", "gemini", "the answer")+"\n")
	if got != "q one | the answer" {
		t.Errorf("a rewritten turn was kept twice: %s", got)
	}
}
