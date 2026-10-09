package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestTUIBoxFiltersParse(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	f := parseBox([]rune("codex: idempotency in:pay Claude: yesterday foo: key"), now)
	if f.text != "idempotency foo: key" {
		t.Errorf("text = %q", f.text)
	}
	if strings.Join(f.harnesses, ",") != "codex,claude" || strings.Join(f.projects, ",") != "pay" {
		t.Errorf("harnesses %v projects %v", f.harnesses, f.projects)
	}
	if !f.since.Equal(time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)) || !f.until.Equal(time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("yesterday = %v .. %v", f.since, f.until)
	}
	if len(f.chips) != 4 || f.chips[0] != [2]int{0, 6} {
		t.Errorf("chips = %v", f.chips)
	}
	if f.keeps(model.Session{Harness: "cursor", Updated: now.Add(-20 * time.Hour)}) ||
		!f.keeps(model.Session{Harness: "codex", Updated: now.Add(-20 * time.Hour)}) ||
		f.keeps(model.Session{Harness: "codex", Updated: now.Add(-time.Hour)}) {
		t.Error("keeps")
	}
	f = parseBox([]rune("retry last week"), now)
	if f.text != "retry" || f.when != "last week" || !f.since.Equal(now.AddDate(0, 0, -7)) || f.chips[0] != [2]int{6, 15} {
		t.Errorf("last week: %+v", f)
	}
	if o := parseBox([]rune("today x"), now).options([]string{"here"}); o.Since <= 0 || o.Projects[0] != "here" || o.Query != "x" {
		t.Errorf("options = %+v", o)
	}
	if f := parseBox([]rune("last nope: in:"), now); f.active() || f.text != "last nope: in:" {
		t.Errorf("nothing recognised: %+v", f)
	}
}

// A filter typed into the box narrows the answer and shows as a chip; the
// words around it are the query.
func TestTUIBoxFiltersApply(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.query = []rune("claude: in:checkout")
	a.reload()
	if len(a.rows) != 1 || a.rows[0].s.ID != "a1111111-pool" {
		t.Fatalf("filters alone list the matching sessions: %+v", a.rows)
	}
	a.query = []rune("in:payments key")
	a.reload()
	drain(t, a)
	for _, r := range a.rows {
		if !strings.Contains(r.s.Project, "payments") {
			t.Errorf("in:payments listed %s", r.s.Project)
		}
	}
	if len(a.rows) == 0 {
		t.Fatal("in:payments key found nothing")
	}
	s := screen(a.frame(120, 36))
	wantOnScreen(t, s, "in:payments key", "in payments")
	a.query = []rune("cursor: key")
	a.reload()
	drain(t, a)
	if len(a.rows) != 0 {
		t.Errorf("no cursor sessions, got %d", len(a.rows))
	}
	a.openModal(modalHelp)
	wantOnScreen(t, screen(a.frame(120, 36)), "FILTERS", "codex:", "in:api")
}
