package main

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/stats"
)

// Both shared pages build their rows by string concatenation into innerHTML,
// and some of the values land inside double-quoted attributes. json.Marshal
// already neutralises `<` and `>` on the way in, so the escaper is the only
// thing standing between a project name and an event handler — and it used to
// stop at &<>. A project name carrying a quote closed data-value="…" and the
// rest of the name parsed as attributes.
//
// escInAttribute finds the values a page interpolates into a quoted attribute.
var escInAttribute = regexp.MustCompile(`="'\s*\+\s*esc\(|="[a-z ]*'\s*\+\s*esc\(`)

func renderedStatsHTML(t *testing.T) string {
	t.Helper()
	report := stats.Report{TotalSessions: 1, TotalMessages: 1, Harnesses: []stats.HarnessStats{{Harness: "claude"}}}
	sessions := []model.Session{{
		Harness: "claude",
		Project: `imported:" onmouseover="alert(1)" x="`,
		Title:   "a note in a hostile project",
		Updated: time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC),
	}}
	page, err := newStatsHTMLPage(report, sessions)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := statsHTMLTemplate.Execute(&b, page); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func renderedView(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	page := viewPage{
		SessionsJSON: jsonForScript([]byte(`[{"id":"n","harness":"deja","project":"\" onmouseover=\"alert(1)\" x=\"","title":"t","updated":"2026-08-06"}]`)),
		RecallsJSON:  jsonForScript([]byte(`[]`)),
		NotesJSON:    jsonForScript([]byte(`[]`)),
	}
	if err := viewTemplate.Execute(&b, page); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestSharedPagesEscapeQuotesInAttributes(t *testing.T) {
	for _, page := range []struct {
		name string
		body string
	}{
		{"deja stats --html", renderedStatsHTML(t)},
		{"deja view", renderedView(t)},
	} {
		if !escInAttribute.MatchString(page.body) {
			t.Fatalf("%s: no esc() inside a quoted attribute — this check has gone vacuous, re-read the page before deleting it", page.name)
		}
		esc := escSource(t, page.name, page.body)
		if !strings.Contains(esc, "&quot;") {
			t.Fatalf("%s: esc() does not escape a double quote, so a project name can close an attribute and add an event handler: %s",
				page.name, esc)
		}
	}
}

// escSource returns the page's own escaper definition, whichever form it takes.
func escSource(t *testing.T, name, body string) string {
	t.Helper()
	for _, open := range []string{"function esc(value){", "const esc=s=>"} {
		i := strings.Index(body, open)
		if i < 0 {
			continue
		}
		rest := body[i:]
		if end := strings.Index(rest, "\n"); end > 0 {
			rest = rest[:end]
		}
		if len(rest) > 400 {
			rest = rest[:400]
		}
		return rest
	}
	t.Fatalf("%s: no esc() definition found", name)
	return ""
}

// A hostile project name must survive into the page as data, escaped, rather
// than being dropped — the page is still meant to show what it holds.
func TestStatsHTMLKeepsHostileProjectAsText(t *testing.T) {
	body := renderedStatsHTML(t)
	if !strings.Contains(body, `onmouseover=`) {
		t.Fatal("the hostile project name vanished from the page; the probe no longer tests anything")
	}
	if strings.Contains(body, `data-value="" onmouseover=`) {
		t.Fatalf("the project name closed data-value in the emitted page: %s", body)
	}
}
