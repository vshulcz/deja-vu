package search

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestSearchRanksAndFilters(t *testing.T) {
	now := time.Now()
	ss := []model.Session{{ID: "a", Harness: "claude", Project: "p", Updated: now, Messages: []model.Message{{Role: "user", Text: "needle needle", Time: now}}}, {ID: "b", Harness: "codex", Project: "p", Updated: now.Add(-24 * time.Hour), Messages: []model.Message{{Role: "assistant", Text: "needle", Time: now}}}}
	hits, err := Run(ss, Options{Query: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Session.ID != "a" || hits[0].Count != 2 {
		t.Fatalf("bad hits: %#v", hits)
	}
	hits, err = Run(ss, Options{Query: "needle", Harness: "codex", Role: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Session.ID != "b" {
		t.Fatalf("bad filter: %#v", hits)
	}
}

func TestPrintJSONSessionContextAndHelpers(t *testing.T) {
	now := time.Now()
	s := model.Session{ID: "abcdef1234567890", Harness: "claude", Project: "proj", Updated: now, Messages: []model.Message{
		{Role: "user", Text: "hello needle world", Time: now},
		{Role: "assistant", Text: strings.Repeat("tool_use ", 80), Time: now},
		{Role: "assistant", Text: "```go\nfmt.Println(needle)\n```", Time: now},
	}}
	hits := []Hit{{Session: s, Count: 1, Snippets: []string{"hello needle"}}}
	var b bytes.Buffer
	Print(&b, hits, Options{Query: "needle", JSON: true})
	var decoded []Hit
	if err := json.Unmarshal(b.Bytes(), &decoded); err != nil || len(decoded) != 1 {
		t.Fatalf("bad json %q err=%v", b.String(), err)
	}
	b.Reset()
	PrintSession(&b, s)
	if !strings.Contains(b.String(), "# claude") || !strings.Contains(b.String(), "[tool/local output collapsed]") {
		t.Fatalf("bad session: %q", b.String())
	}
	b.Reset()
	PrintContext(&b, s, "needle")
	if !strings.Contains(b.String(), "# deja context:") || !strings.Contains(b.String(), "fmt.Println") {
		t.Fatalf("bad context: %q", b.String())
	}
	if got, ok := FindByPrefix([]model.Session{s}, "abcdef"); !ok || got.ID != s.ID {
		t.Fatalf("FindByPrefix got %#v ok=%v", got, ok)
	}
	if got := Recent([]model.Session{s, {ID: "old", Updated: now.Add(-time.Hour)}}, 1); len(got) != 1 || got[0].ID != s.ID {
		t.Fatalf("Recent=%#v", got)
	}
}

func TestHighlightDateSnippetAndColorBranches(t *testing.T) {
	if got := highlight("Needle and thread", "needle", false, true); !strings.Contains(got, cMatch+"Needle"+cReset) {
		t.Fatalf("highlight literal=%q", got)
	}
	if got := highlight("abc 123", `\d+`, true, true); !strings.Contains(got, cMatch+"123"+cReset) {
		t.Fatalf("highlight regex=%q", got)
	}
	if got := highlight("jwt and refresh", "jwt refresh", false, true); strings.Count(got, cMatch) != 2 {
		t.Fatalf("highlight tokens=%q", got)
	}
	if got := highlight("x", "x", false, false); got != "x" {
		t.Fatalf("highlight no color=%q", got)
	}
	for _, h := range []string{"claude", "codex", "opencode", "other"} {
		if !strings.Contains(harnessTag(h, true), "["+h+"]") || harnessTag(h, false) != "["+h+"]" {
			t.Fatalf("bad harness tag %q", h)
		}
	}
	now := time.Now()
	for _, tt := range []time.Time{now, now.AddDate(0, 0, -3), now.AddDate(0, -1, 0), now.AddDate(-1, 0, 0)} {
		if relativeDate(tt) == "" {
			t.Fatalf("empty relative date")
		}
	}
	if got := snippet("needle at start "+strings.Repeat("x", 300), "needle", nil); !strings.HasPrefix(got, "needle") || !strings.HasSuffix(got, "…") {
		t.Fatalf("snippet start=%q", got)
	}
	if got := snippet(strings.Repeat("x", 300)+" needle", "needle", nil); !strings.HasPrefix(got, "…") || !strings.Contains(got, "needle") {
		t.Fatalf("snippet end=%q", got)
	}
	if got := snippet("no direct tokens but has jwt", "jwt refresh", nil); !strings.Contains(got, "jwt") {
		t.Fatalf("snippet token=%q", got)
	}
	os.Setenv("NO_COLOR", "1")
	defer os.Unsetenv("NO_COLOR")
	if colorOK(os.Stdout) {
		t.Fatalf("NO_COLOR ignored")
	}
}

func TestMultiWordSearchRequiresAllTokensAndCountsOccurrences(t *testing.T) {
	now := time.Now()
	ss := []model.Session{
		{ID: "both", Harness: "claude", Project: "p", Updated: now, Messages: []model.Message{{Role: "user", Text: "refresh the jwt access token, then jwt again", Time: now}}},
		{ID: "one", Harness: "claude", Project: "p", Updated: now, Messages: []model.Message{{Role: "user", Text: "jwt only", Time: now}}},
	}
	hits, err := Run(ss, Options{Query: "jwt refresh token"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Session.ID != "both" || hits[0].Count != 4 {
		t.Fatalf("bad multi-word hits: %#v", hits)
	}
}

func TestPrintPlainWhenNotTTY(t *testing.T) {
	now := time.Now()
	hits := []Hit{{Session: model.Session{ID: "abcdef1234567890", Harness: "opencode", Project: "deja", Updated: now}, Count: 1, Snippets: []string{"hello needle"}}}
	var b bytes.Buffer
	Print(&b, hits, Options{Query: "needle"})
	out := b.String()
	if strings.Contains(out, "\x1b[") || !strings.Contains(out, "[opencode]") || !strings.Contains(out, "1 matches") {
		t.Fatalf("bad plain output: %q", out)
	}
}

func TestSnippetPrefersProseOverToolDump(t *testing.T) {
	text := "netcat output noise needle\n1: package main\n2: func main() {}\nUser asked about needle migration strategy and we concluded use small batches."
	hits, err := Run([]model.Session{{ID: "s", Harness: "claude", Project: "p", Updated: time.Now(), Messages: []model.Message{{Role: "assistant", Text: text}}}}, Options{Query: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || strings.Contains(hits[0].Snippets[0], "1: package") || strings.Contains(hits[0].Snippets[0], "netcat") {
		t.Fatalf("noisy snippet: %#v", hits)
	}
	if !strings.Contains(hits[0].Snippets[0], "migration strategy") {
		t.Fatalf("missing prose snippet: %#v", hits[0].Snippets[0])
	}
}
