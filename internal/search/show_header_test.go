package search

import (
	"strings"
	"testing"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/model"
)

// #1090 stripped the bytes a terminal acts on from the headers these two
// functions print. PrintContext got it; PrintSession — what `deja show` calls
// — did not, so the same session printed a clean header through one and a
// header carrying an escape, a carriage return or a line break through the
// other.
func TestPrintSessionHeaderIsOneSafeLine(t *testing.T) {
	for _, tc := range []struct {
		name    string
		project string
		id      string
	}{
		{"escape", "w/\x1b[31mred\x1b[0m-app", "abcdef"},
		{"carriage return", "w/app", "abc\rdef"},
		{"newline", "w/app\nFAKE ROW pretending to be deja output", "abcdef"},
		{"bell", "w/app\a", "abc\adef"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			PrintSession(&b, model.Session{
				Harness: "claude", Project: tc.project, ID: tc.id,
				Messages: []model.Message{{Role: "user", Text: "the retry queue stalls on staging"}},
			})
			header := strings.SplitN(b.String(), "\n", 2)[0]
			for _, r := range header {
				if unicode.IsControl(r) {
					t.Errorf("a control character reached the header: %q in %q", r, header)
					break
				}
			}
			// One line, and the whole header on it: a break in the project
			// name used to start a row that reads as deja's own output.
			lines := strings.Split(b.String(), "\n")
			if len(lines) < 2 || lines[1] != "" {
				t.Errorf("the header spilled onto a second line: %q", b.String())
			}
			if !strings.HasSuffix(header, tc.id[len(tc.id)-3:]) {
				t.Errorf("the header lost its tail: %q", header)
			}
		})
	}
}

// The two headers agree on the same session, which is the point.
func TestShowAndContextHeadersAgree(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "w/\x1b[31mred\x1b[0m-app", ID: "abc\rdef",
		Messages: []model.Message{{Role: "user", Text: "the retry queue stalls on staging"}},
	}
	var show, ctx strings.Builder
	PrintSession(&show, s)
	PrintContext(&ctx, s, "retry")
	showHead := strings.SplitN(show.String(), "\n", 2)[0]
	ctxHead := strings.SplitN(ctx.String(), "\n", 2)[0]
	if !strings.Contains(ctxHead, strings.TrimPrefix(showHead, "# ")) {
		t.Errorf("the two headers scrub differently:\n  show    %q\n  context %q", showHead, ctxHead)
	}
}

// A plain session is untouched.
func TestPrintSessionHeaderUnchangedForPlainText(t *testing.T) {
	var b strings.Builder
	PrintSession(&b, model.Session{
		Harness: "claude", Project: "w/app", ID: "abcdef",
		Messages: []model.Message{{Role: "user", Text: "hello"}},
	})
	if got := strings.SplitN(b.String(), "\n", 2)[0]; got != "# claude · w/app · abcdef" {
		t.Errorf("plain header changed: %q", got)
	}
}
