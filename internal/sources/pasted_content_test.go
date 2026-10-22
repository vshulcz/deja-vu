package sources

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestUnwrapPastedContent(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{
			name: "whole prompt is a paste",
			in:   "<pasted_content id=\"dd73\">\nSet up and test ~/.claude/hooks/x.sh\n</pasted_content id=\"dd73\">",
			want: "Set up and test ~/.claude/hooks/x.sh",
		},
		{
			name: "paste then the person's question",
			in:   "<pasted_content id=\"543b\">\nhttps://example.com/pull/1\n</pasted_content id=\"543b\">\n\n\nWhat's next?",
			want: "https://example.com/pull/1\n\n\n\n\nWhat's next?",
		},
		{
			name: "closed mid-line does not glue words",
			in:   "see <pasted_content id=\"cc08\">a failed session?</pasted_content id=\"cc08\">Can you check",
			want: "see \na failed session?\nCan you check",
		},
		{
			name: "two pastes",
			in:   "<pasted_content id=\"a\">one</pasted_content id=\"a\"> and <pasted_content id=\"b\">two</pasted_content id=\"b\">",
			want: "one\n and \ntwo",
		},
		{
			name: "a paste quoting a wrapper keeps the quote's unmatched tag",
			in:   "<pasted_content id=\"9ab0\">it showed '<pasted_content id=\"dd73\"> Set up'</pasted_content id=\"9ab0\">",
			want: "it showed '<pasted_content id=\"dd73\"> Set up'",
		},
		{
			name: "a person naming the tag keeps the sentence",
			in:   "strip the <pasted_content> wrapper from titles",
			want: "strip the <pasted_content> wrapper from titles",
		},
		{
			name: "an open with no matching close is left",
			in:   "<pasted_content id=\"a\">text</pasted_content id=\"b\">",
			want: "<pasted_content id=\"a\">text</pasted_content id=\"b\">",
		},
	} {
		if got := UnwrapPastedContent(tc.in); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// Claude Code wraps pasted text in <pasted_content id="…">…</pasted_content
// id="…">, and both readers stored the tags as the person's words: sessions
// were titled `<pasted_content id="dd73"> Set up and test…` and the install's
// demo question asked "what did we decide about pasted_content set?".
func TestClaudeReadersUnwrapPastedContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	body := `{"type":"user","sessionId":"s1","timestamp":"2026-08-10T10:00:00Z","message":{"role":"user","content":"<pasted_content id=\"dd73\">\nSet up and test the reminder hook\n</pasted_content id=\"dd73\">\n\nwhy does it not fire"}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-08-10T10:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"the <pasted_content id=\"x\">quoted</pasted_content id=\"x\"> stays as written"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, parse := range map[string]func(string, int64) ([]model.Session, error){
		"typed":   parseClaudeTypedFromOffset,
		"generic": parseClaudeGenericFromOffset,
	} {
		ss, err := parse(path, 0)
		if err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 {
			t.Fatalf("%s: %v, %+v", name, err, ss)
		}
		if got, want := ss[0].Messages[0].Text, "Set up and test the reminder hook\n\n\n\nwhy does it not fire"; got != want {
			t.Errorf("%s: user text = %q, want %q", name, got, want)
		}
		if got, want := ss[0].Messages[1].Text, `the <pasted_content id="x">quoted</pasted_content id="x"> stays as written`; got != want {
			t.Errorf("%s: assistant text = %q, want %q", name, got, want)
		}
	}
}
