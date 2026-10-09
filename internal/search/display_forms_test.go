package search

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The exported forms the CLI screens share: one id, one date, one tag.
func TestDisplayFormsAreTheOnesTheResultLinesPrint(t *testing.T) {
	id := "e7a3c210-5b1d-4c2e-9f00-2a6b7c8d9e01"
	if got := ShortID(id); got != short(id) || !strings.Contains(got, "…") {
		t.Errorf("ShortID(%q) = %q", id, got)
	}
	if got := ShortID("d9f2b311"); got != "d9f2b311" {
		t.Errorf("a short id changed: %q", got)
	}
	day := time.Date(2001, 11, 29, 12, 0, 0, 0, time.Local)
	if got := DisplayDate(day); got != "Nov 29 2001" {
		t.Errorf("DisplayDate = %q", got)
	}
	if got := HarnessTag("claude", false); got != "[claude]" {
		t.Errorf("HarnessTag plain = %q", got)
	}
	if got := HarnessTag("claude", true); !strings.Contains(got, "[claude]") || !strings.Contains(got, "\x1b[") {
		t.Errorf("HarnessTag colour = %q", got)
	}
	if ColorOK(&bytes.Buffer{}) {
		t.Error("a buffer is not a terminal")
	}
	if !RoleMatches("tool-output", "tool") || RoleMatches("user", "tool") {
		t.Error("RoleMatches disagrees with the search filter")
	}
	if (Hit{quoted: []string{"a"}}).QuotedMessages()[0] != "a" {
		t.Error("QuotedMessages lost its text")
	}
}

func TestSessionTitleFallsBackToTheFirstAsk(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "assistant", Text: "hello"},
		{Role: "user", Text: strings.Repeat("why does the pool drop connections ", 4)},
	}}
	if got := SessionTitle(s); !strings.HasPrefix(got, "why does the pool") || !strings.HasSuffix(got, "...") {
		t.Errorf("SessionTitle = %q", got)
	}
	s.Title = "named"
	if SessionTitle(s) != "named" {
		t.Error("an explicit title was not used")
	}
	if SessionTitle(model.Session{}) != "" {
		t.Error("a session with no ask has no title")
	}
}

// On a terminal the digest header takes the short id and the display date,
// headings go bold, and the turns wrap to the width.
func TestPrintContextStyledOnATerminal(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "shop", ID: "e7a3c210-5b1d-4c2e-9f00-2a6b7c8d9e01",
		Updated: time.Date(2001, 11, 29, 12, 0, 0, 0, time.Local),
		Messages: []model.Message{
			{Role: "user", Text: "the pool tests fail on CI with connection refused, please look at the pool and the proxy timeout together"},
			{Role: "assistant", Text: "Raised MaxConns to 20 because the checkout burst queued behind ten connections at the top of the hour."},
		},
	}
	var b bytes.Buffer
	PrintContextStyled(&b, s, "pool", true, 40)
	out := b.String()
	if !strings.Contains(out, "e7a3c210-…") || !strings.Contains(out, "Nov 29 2001") || !strings.Contains(out, "\x1b[1m") {
		t.Errorf("styled header:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n")[1:] {
		plain := strings.NewReplacer("\x1b[1m", "", "\x1b[0m", "").Replace(line)
		if len([]rune(plain)) > 60 {
			t.Errorf("line not wrapped near 40: %q", plain)
		}
	}

	b.Reset()
	PrintContextStyled(&b, s, "pool", false, 0)
	var plain bytes.Buffer
	PrintContext(&plain, s, "pool")
	if b.String() != plain.String() {
		t.Error("a pipe must get PrintContext's text unchanged")
	}
}

func TestFirstLinesSkipsBlankLines(t *testing.T) {
	if got := firstLines("\n  one \n\n two\nthree", 2); got != "one\ntwo" {
		t.Errorf("firstLines = %q", got)
	}
}
