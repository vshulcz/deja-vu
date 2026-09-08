package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Kimi appends everything to the context under the user role and says who wrote
// it in message.origin.kind. Keyed on the role alone, a hook's own stdout and a
// <system-reminder> were the person's words — so deja recalled its own
// injection back as a `User:` line, citing itself (#3199).
//
// Measured on this machine's store: five user-role records were injections and
// one a hook_result, against four the person typed.
func TestKimiKeepsOnlyThePersonsTurns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session_1.jsonl")

	// Through the encoder: the hook_result body carries quotes of its own, and
	// pasted into a literal it made a line no parser reads — a test that passes
	// because the record never arrived.
	rec := func(origin any, text string) string {
		msg := map[string]any{"role": "user", "content": text}
		if origin != nil {
			msg["origin"] = origin
		}
		b, err := json.Marshal(map[string]any{
			"type": "context.append_message", "time": "2026-08-01T10:00:00Z", "message": msg,
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	kind := func(k string) any { return map[string]any{"kind": k} }

	lines := []string{
		rec(kind("user"), "why does the glimwrax fetcher time out"),
		rec(kind("injection"), "<system-reminder>Permission mode is now acceptEdits</system-reminder>"),
		rec(kind("hook_result"), `<hook_result hook_event="UserPromptSubmit">deja-vu — you have been here: zorbflax</hook_result>`),
		rec(kind("background_task"), "quuxnotify: background task 7 finished"),
		rec(kind("system_trigger"), "the session was resumed"),
		rec(kind("compaction_summary"), "Summary: the session so far"),
		// An older protocol wrote no origin at all, and a newer one may write
		// the object with nothing in it. Both are the person's, or every
		// session from before the field existed comes back empty.
		rec(nil, "and the second thing I asked"),
		rec(map[string]any{}, "the third thing, with an origin that names no kind"),
		rec(kind("slash_command"), "/compact"),
		`{"type":"context.append_message","time":"2026-08-01T10:01:00Z","message":{"role":"assistant","content":"because the pool is exhausted"}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseKimiFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	var users []string
	for _, m := range ss[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	want := []string{
		"why does the glimwrax fetcher time out",
		"and the second thing I asked",
		"the third thing, with an origin that names no kind",
		"/compact",
	}
	if strings.Join(users, "|") != strings.Join(want, "|") {
		t.Fatalf("user turns =\n  %s\nwant\n  %s", strings.Join(users, "\n  "), strings.Join(want, "\n  "))
	}
	// The assistant's half is untouched: this is about who wrote a user record.
	found := false
	for _, m := range ss[0].Messages {
		if m.Role == "assistant" && strings.Contains(m.Text, "pool is exhausted") {
			found = true
		}
	}
	if !found {
		t.Error("the assistant's answer went with the host's lines")
	}
}
