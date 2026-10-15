package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
)

// The instant every test below prints: 23:45 UTC is the next day fourteen
// hours east, so a date printed in the source's zone reads a day early.
var zoneProbeAt = time.Date(2026, 7, 20, 23, 45, 0, 0, time.UTC)

func readerEast(t *testing.T) {
	t.Helper()
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	time.Local = time.FixedZone("kiritimati", 14*3600)
}

// #5053: the page's header ranged over reader-zone days and its rows printed
// the source's zone, so a session looked to fall outside the range it covered.
// The notes tab had the same split.
func TestViewPrintsSessionsAndNotesInTheReadersZone(t *testing.T) {
	hermeticEnv(t)
	readerEast(t)
	notes := filepath.Join(t.TempDir(), "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	line := `{"ts":"` + zoneProbeAt.Format(time.RFC3339) + `","project":"tmp/app","text":"retry budget stays at 5","kind":"promoted","session":"claude:zone1","state":"accepted"}` + "\n"
	if err := os.WriteFile(notes, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	ts := zoneProbeAt.Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-app", "zone1.jsonl"), "zone1", []string{
		`{"type":"user","sessionId":"zone1","timestamp":"` + ts + `","message":{"role":"user","content":"why does the pool run dry"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "page.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)const S=(.*?),R=(.*?),N=(.*?);\n`).FindStringSubmatch(readFileString(t, out))
	if m == nil {
		t.Fatal("the page's script no longer starts with the three arrays")
	}
	var rows []viewSession
	if err := json.Unmarshal([]byte(m[1]), &rows); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		if r.ID == "zone1" {
			got = append(got, r.Updated)
		}
	}
	if len(got) != 1 || got[0] != "2026-07-21 13:45" {
		t.Errorf("session row updated = %v, want the reader's 2026-07-21 13:45", got)
	}
	var ns []viewNote
	if err := json.Unmarshal([]byte(m[3]), &ns); err != nil {
		t.Fatal(err)
	}
	if len(ns) == 0 || ns[0].At != "2026-07-21" {
		t.Errorf("notes = %+v, want the note dated the reader's 2026-07-21", ns)
	}
}

func TestStatsPageDatesRowsInTheReadersZone(t *testing.T) {
	readerEast(t)
	s := model.Session{Harness: "claude", ID: "s1", Project: "p", Started: zoneProbeAt, Updated: zoneProbeAt,
		Messages: []model.Message{{Role: "user", Text: "hi"}}}
	page, err := newStatsHTMLPage(stats.Report{TotalSessions: 1, TotalMessages: 1}, []model.Session{s})
	if err != nil {
		t.Fatal(err)
	}
	if js := string(page.SessionsJSON); !strings.Contains(js, "2026-07-21") {
		t.Errorf("stats row is not dated the reader's day: %s", js)
	}
}

func TestPlanFindingSinceIsTheReadersDay(t *testing.T) {
	readerEast(t)
	match := samplePlanMatches("p")[0]
	for i := range match.Wall.Sessions {
		match.Wall.Sessions[i].Updated = zoneProbeAt.Add(time.Duration(i) * 48 * time.Hour)
	}
	if got := formatPlanFinding(match); !strings.Contains(got, "since 2026-07-21") {
		t.Errorf("plan finding %q, want since the reader's 2026-07-21", got)
	}
}

// The earlier-attempt label is now the reader's day, and the rival it is
// matched against has to be read the same way, or a rejected rival no longer
// clears it.
func TestDemoteRejectedMatchesTheReadersDay(t *testing.T) {
	readerEast(t)
	hits := []search.Hit{
		{Session: model.Session{ID: "old", Project: "p", Updated: zoneProbeAt.AddDate(0, 0, -5)}, Superseded: "2026-07-21"},
		{Session: model.Session{ID: "rej", Project: "p", Updated: zoneProbeAt}, Lifecycle: lifecycleRejected},
	}
	demoteRejected(hits)
	for _, h := range hits {
		if h.Session.ID == "old" && h.Superseded != "" {
			t.Errorf("an attempt superseded only by a rejected session kept its label %q", h.Superseded)
		}
	}
}

// doctor printed when the stores were last read in the zone of the process
// that wrote the manifest.
func TestDoctorStoresReadIsInTheReadersZone(t *testing.T) {
	hermeticEnv(t)
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	time.Local = time.FixedZone("pago", -11*3600)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-app", "d1.jsonl"), "d1", []string{
		`{"type":"user","sessionId":"d1","timestamp":"` + zoneProbeAt.Format(time.RFC3339) + `","message":{"role":"user","content":"the retry storm"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Written later than the read, so doctor names both.
	later := time.Now().Add(3 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "manifest.gob"), later, later); err != nil {
		t.Fatal(err)
	}
	time.Local = time.FixedZone("kiritimati", 14*3600)
	want := "stores read " + index.ManifestSourcesReadAt(dir).In(time.Local).Format("2006-01-02 15:04")
	var buf bytes.Buffer
	doctorIndex(&buf, doctorIndexReport{State: "fresh", Path: dir}, dir)
	if !strings.Contains(buf.String(), want) {
		t.Errorf("doctor does not say %q:\n%s", want, buf.String())
	}
}
