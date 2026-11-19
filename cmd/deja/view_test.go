package main

import (
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// A page a rule emptied read as a machine with no history: no sessions, a bare
// arrow for the date range, and a note counting zero "most recent sessions".
// The reason was on stderr, and the file is the thing people look at (#2321).
func TestViewSaysWhenARuleEmptiedThePage(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own connection pool question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"activations":{"search":{"local":false,"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyPath)

	out := filepath.Join(t.TempDir(), "view.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)
	if strings.Contains(page, "connection pool question") {
		t.Fatalf("the withheld session is on the page, so this measures nothing")
	}
	if !strings.Contains(page, "trust policy") {
		t.Errorf("the page does not say a rule emptied it:\n%s", pageStatsBlock(page))
	}
	if strings.Contains(page, "the 0 most recent sessions") {
		t.Errorf("the page counts zero most-recent sessions:\n%s", pageStatsBlock(page))
	}
	if strings.Contains(page, "<b> → </b>") {
		t.Errorf("the page renders an empty date range:\n%s", pageStatsBlock(page))
	}
}

// pageStatsBlock is the head of the page body, for a readable failure.
func pageStatsBlock(page string) string {
	i := strings.Index(page, `class="stats"`)
	if i < 0 {
		return "(no stats block)"
	}
	end := min(i+600, len(page))
	return page[i:end]
}

// An empty view page — nothing indexed, nothing typed — said `nothing matches
// — try deja "" for full-text search`, a search for nothing. The hint is for a
// query that matched nothing; an empty store says that instead.
func TestViewEmptyPageDoesNotSuggestAnEmptySearch(t *testing.T) {
	i := strings.Index(viewSource, `nothing matches — try deja "'+esc(q)+'"`)
	if i < 0 {
		t.Fatal("the no-match hint is gone from the page")
	}
	if !strings.HasSuffix(viewSource[:i], `:q?'<div class="empty">`) {
		t.Fatalf("the no-match hint is not guarded by a typed query:\n%s", viewSource[max(0, i-80):i])
	}
	if !strings.Contains(viewSource, "no sessions indexed yet") {
		t.Fatal("the empty store has no line of its own")
	}
}

// A decision promoted on one machine travels: it arrives as a session carrying
// its lifecycle state, recall ranks it as a decision and the tool hook honours
// it (#975). The page's Notes tab is built from this machine's notes file
// alone, so the tab that holds decisions held none of the ones that came from
// elsewhere (#2421).
func TestThePageCarriesADecisionThatArrivedBySync(t *testing.T) {
	tmp := hermeticEnv(t)
	batch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	rec := `{"harness":"deja","session_id":"deja-note-claude-a1","project":"work/api","role":"user",` +
		`"text":"[accepted] retry budget stays at 5 (from claude:a1)","time":"` + at + `",` +
		`"origin":"quicksilver","lifecycle":"accepted","lifecycle_note":"retry budget stays at 5",` +
		`"lifecycle_at":"` + at + `"}`
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), []byte(rec+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	captureBoth(t, "sync", "import", batch)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(tmp, "page.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)
	m := regexp.MustCompile(`(?s)const S=(.*?),R=(.*?),N=(.*?);\n`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the page's script no longer starts with the three arrays")
	}
	var carried []map[string]any
	if err := json.Unmarshal([]byte(m[3]), &carried); err != nil {
		t.Fatalf("the notes array does not decode: %v", err)
	}
	found := false
	for _, n := range carried {
		text, _ := n["text"].(string)
		state, _ := n["state"].(string)
		if strings.Contains(text, "retry budget stays at 5") {
			found = true
			if state != "accepted" {
				t.Errorf("the decision arrived without its state: %v", n)
			}
		}
	}
	if !found {
		t.Errorf("a decision that arrived by sync is on no tab that holds decisions:\n%s", m[3])
	}
}

// A promoted note's title is the session's own opening line — the question —
// and the decision is what the person wrote. The Notes tab led each row with
// the badge and the title, so the page read "accepted: should we move the
// reports to a redis cache", which is the misread #2455 fixed for agents,
// still on the page a person passes around (#2460).
func TestTheNotesTabLeadsWithTheDecision(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := `{"type":"user","sessionId":"dec","timestamp":"` + at + `","cwd":"/work/app",` +
		`"message":{"role":"user","content":"should we move the reports to a redis cache"}}`
	if err := os.WriteFile(filepath.Join(store, "dec.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	rec := `{"ts":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","project":"work/app","kind":"promoted",` +
		`"session":"claude:dec","state":"accepted","title":"should we move the reports to a redis cache",` +
		`"text":"no redis in front of the reports: they are materialised"}`
	if err := os.WriteFile(notes, []byte(rec+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(tmp, "page.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)
	m := regexp.MustCompile(`(?s)const S=(.*?),R=(.*?),N=(.*?);\n`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the page's script no longer starts with the three arrays")
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(m[3]), &rows); err != nil {
		t.Fatalf("the notes array does not decode: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want one note on the page, got %d", len(rows))
	}
	// Both halves reach the page: the decision and the question it answers.
	title, _ := rows[0]["title"].(string)
	text, _ := rows[0]["text"].(string)
	if !strings.Contains(text, "no redis in front of the reports") {
		t.Errorf("the decision is not on the page: %q / %q", title, text)
	}
	if !strings.Contains(title, "should we move the reports") {
		t.Errorf("the question is not on the page: %q / %q", title, text)
	}
	// And the row is built decision-first. The page renders itself, so this is
	// where a Go test can hold it: the headline takes the decision and falls
	// back to the title only when a note has no text of its own.
	row := regexp.MustCompile(`function rowN\(n\)\{([^\n]*)`).FindStringSubmatch(page)
	if row == nil {
		t.Fatalf("the page no longer builds its note rows in rowN")
	}
	if !strings.Contains(row[1], "n.text||n.title") {
		t.Errorf("the note row does not lead with the decision:\n%s", row[1])
	}
	head := strings.Index(row[1], "class=\"t\"")
	body := strings.Index(row[1], "<pre")
	if head < 0 || body < 0 || head > body {
		t.Errorf("the row no longer puts a headline above a body:\n%s", row[1])
	}
}

// A note promoted from an imported session keeps that project. The session list
// obeys a local-only rule and the Notes tab rendered the note whole — the same
// gap #2315 closed for digests, one tab over (#2317).
func TestViewWithholdsNotesFromHiddenProjects(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own connection pool question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	batch := t.TempDir()
	rec := index.SyncRecord{
		Harness: "claude", SessionID: "peersess", Project: "secretclient/api",
		Role: "assistant", Text: "the quaxbolt overflow was an int32 cast",
		Time: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSync(dir, []string{"import", batch}); err != nil {
		t.Fatal(err)
	}
	// One note from the peer's project, one from the machine's own.
	lines := []map[string]any{
		{"ts": time.Now().UTC().Format(time.RFC3339Nano), "kind": "promoted",
			"session": "claude:imported-1", "state": "accepted", "project": "imported:secretclient/api",
			"title": "peer decision", "text": "the quaxbolt overflow was an int32 cast"},
		{"ts": time.Now().UTC().Format(time.RFC3339Nano), "kind": "promoted",
			"session": "claude:minesess", "state": "accepted", "project": "tmp/mine",
			"title": "my decision", "text": "my own connection pool question"},
	}
	var buf strings.Builder
	for _, l := range lines {
		enc, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(enc)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(notes, []byte(buf.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "view.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	if page := readFileString(t, out); !strings.Contains(page, "peer decision") {
		t.Fatalf("the peer's note is not on the page with no policy set, so this measures nothing")
	}

	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyPath)

	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)
	if strings.Contains(page, "peer decision") || strings.Contains(page, "secretclient") {
		t.Errorf("the page carries a note from a project the policy withholds")
	}
	if !strings.Contains(page, "my decision") {
		t.Errorf("the page dropped the machine's own note")
	}
}

// The page caps a session's preview so it stays a single fast file. A note's
// body is the same kind of content in the same page and had no cap, so one
// promoted note outweighed a hundred sessions (#2100).
func TestANoteDoesNotOutweighThePage(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)

	render := func(what string) int64 {
		t.Helper()
		if _, err := captureRun(t, "index"); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "view.html")
		if _, err := captureRun(t, "view", "--no-open", "--out", out); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d bytes", what, fi.Size())
		return fi.Size()
	}
	small := render("an empty store")

	huge := strings.Repeat("the pool was exhausted. ", 40000) // about a megabyte
	line, err := json.Marshal(map[string]any{
		"ts": time.Now().UTC().Format(time.RFC3339Nano), "kind": "promoted",
		"session": "claude:s1", "state": "accepted", "project": "app",
		"title": "pool sizing", "text": huge,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	withNote := render("plus one note of a megabyte")

	// The premise: the note reached the page at all.
	if withNote <= small {
		t.Fatalf("the note did not reach the page, so this measures nothing")
	}
	// A promoted note reaches the page twice — as a note, and as the deja
	// session it is indexed as, whose preview is capped too — so the page grows
	// by two capped copies and the rows around them.
	if grew := withNote - small; grew > 3*viewPreviewBytes {
		t.Errorf("one note grew the page by %d bytes; the preview cap is %d", grew, viewPreviewBytes)
	}
}

// The page bounds its transcripts and each cell's text, and carried every note
// there is — a thousand of them made it 7.6 MB, which is not the single fast
// file the cap on transcripts exists to keep (#2111).
func TestThePageCarriesTheNewestNotesAndSaysSo(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)

	body := strings.Repeat("the connection pool was exhausted while the migration held the lock. ", 90)
	var b strings.Builder
	const total = 1000
	for i := range total {
		// Oldest first in the file, so file order and date order disagree —
		// which is what makes the sort part of the fix rather than a detail.
		line, err := json.Marshal(map[string]any{
			"ts":   time.Now().Add(-time.Duration(total-i) * time.Hour).UTC().Format(time.RFC3339Nano),
			"kind": "promoted", "session": fmt.Sprintf("claude:s%04d", i),
			"state": "accepted", "project": "app",
			"title": fmt.Sprintf("decision %04d", i),
			// The marker sits below the first line, which is the note's own
			// display row and is on the page for every session (#2539); the
			// bound this measures is the one on the body.
			"text": fmt.Sprintf("decision %04d\nbody-marker-%04d ", i, i) + body,
		})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(notes, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "view.html")
	if _, err := captureRun(t, "view", "--no-open", "--out", out); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the notes reached the page.
	if !strings.Contains(string(page), "body-marker-0999") {
		t.Fatalf("the newest note's body is not on the page, so this measures nothing")
	}
	if n := len(page); n > 3<<20 {
		t.Errorf("the page is %d bytes for %d notes", n, total)
	}
	// The oldest note's row still appears among the sessions — a note is
	// indexed as one, and those stay browsable by metadata — but its body is
	// what the page carries for a note, and that is what the bound is about.
	if strings.Contains(string(page), "body-marker-0000") {
		t.Errorf("the page carries the body of the oldest of %d notes, so nothing was bounded", total)
	}
	if !strings.Contains(string(page), "most recent notes") {
		t.Errorf("the page does not say that it carries a subset of the notes")
	}
}

// notesPayload returns the page's notes array as it was written into the
// script, before any JSON decoding — the notes are their own array beside the
// sessions, and reading the whole page instead would be satisfied by the same
// text appearing in a session preview.
func notesPayload(t *testing.T, page string) string {
	t.Helper()
	const marker = ",N="
	_, after, ok := strings.Cut(page, marker)
	if !ok {
		t.Fatalf("the view page no longer embeds a notes array; this test reads the wrong thing now")
	}
	rest := after
	if j := strings.Index(rest, "];"); j >= 0 {
		return rest[:j+1]
	}
	return rest
}

// The page carries promoted notes in their own array, and a note's project,
// title, text and tags are all text the user typed. Session text has been
// pinned against closing the page's script block since the view landed; the
// notes array had no such test, and notes written before #1811 can still hold
// a tag with anything in it (#1817).
func TestNoteTagsCannotCloseThePagesScript(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"v1","cwd":"/proj","timestamp":"2026-08-22T01:00:00Z","message":{"role":"user","content":"the retry budget question"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}

	// A promoted note as an older deja would have stored it: tags uncleaned.
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	body, err := json.Marshal(map[string]any{
		"ts": "2026-08-20T10:00:00Z", "project": "proj", "kind": "promoted",
		"session": "sess1", "state": "accepted", "title": "retries",
		"text": "we cap retries at three",
		"tags": []string{"ok", "red\x1b[31malert\x1b[0m", "</script><script>alert(1)</script>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(notes, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := index.DefaultDir()
	if err = index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	path, _, err := writeViewHTML(dir, filepath.Join(tmp, "view.html"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	notesJSON := notesPayload(t, string(page))

	if strings.Contains(notesJSON, "</script>") {
		t.Errorf("a note's tag closed the page's script block:\n%s", notesJSON)
	}
	// The control names the escaped form the page actually writes: looking for
	// "</script>" anywhere on the page would be satisfied by the template's own
	// closing tag, and looking at the whole page would be satisfied by the same
	// tag appearing in a session preview.
	if !strings.Contains(notesJSON, `alert(1)`) {
		t.Errorf("the hostile tag never reached the notes array, so the assertion above proves nothing:\n%s", notesJSON)
	}
	if !strings.Contains(notesJSON, `"ok"`) {
		t.Errorf("the ordinary tag beside it is missing too:\n%s", notesJSON)
	}
}

// #2112 sorts the notes newest-first in Go before it cuts them, and nothing
// pinned the order that reaches the page — the cap test only asks which notes
// are on it. This reads the array the page hands its script, and then asks the
// script itself whether it draws that array as given: the order the reader
// sees is the two together, and either half can move on its own.
func TestTheNotesTabIsRenderedNewestFirst(t *testing.T) {
	hermeticEnv(t)
	notes := sources.NotesFile()
	if err := os.MkdirAll(filepath.Dir(notes), 0o755); err != nil {
		t.Fatal(err)
	}
	// Written oldest first, so file order and date order disagree, and one
	// session promoted twice so its note is carried by the later record.
	var b strings.Builder
	for _, r := range []struct {
		sid, title string
		hours      int
	}{
		{"claude:s1", "oldest decision", 100},
		{"claude:s2", "middle decision", 50},
		{"claude:s3", "newest decision", 2},
		{"claude:s1", "oldest decision, promoted again", 1},
	} {
		line, err := json.Marshal(map[string]any{
			"ts":   time.Now().Add(-time.Duration(r.hours) * time.Hour).UTC().Format(time.RFC3339Nano),
			"kind": "promoted", "session": r.sid, "state": "accepted", "project": "app",
			"title": r.title, "text": "body of " + r.title,
		})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(notes, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "view.html")
	if _, err := captureRun(t, "view", "--no-open", "--out", out); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := noteTitlesInPageOrder(t, string(page))
	want := []string{"oldest decision, promoted again", "newest decision", "middle decision"}
	if len(got) != len(want) {
		t.Fatalf("page carries %d notes, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("notes reach the page in this order: %q\nwant newest first: %q", got, want)
			break
		}
	}
	// The other half: the script draws that array, and a sort of its own there
	// would move the reader's newest note with the array above untouched.
	script := string(page)
	if !strings.Contains(script, "N.map(rowN)") {
		t.Errorf("the notes list is no longer drawn straight from the array — check what reorders it")
	}
	for _, reorder := range []string{"N.sort", "N.reverse", "N=N.slice().sort", "N.toSorted"} {
		if strings.Contains(script, reorder) {
			t.Errorf("the page reorders the notes with %s, so the order above is not what the reader sees", reorder)
		}
	}
}

// noteTitlesInPageOrder reads the titles out of the notes array the page hands
// its script. A session row carries "title" beside "updated" and a recall row
// has no title at all, so only a note matches; a title holding a quote arrives
// escaped and drops out of the count rather than moving in it.
func noteTitlesInPageOrder(t *testing.T, page string) []string {
	t.Helper()
	re := regexp.MustCompile(`"title":"([^"]*)","text"`)
	matches := re.FindAllStringSubmatch(page, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// The Notes tab named `deja remember` in its own sentence and was built from
// promoted records only, so the one place a remembered note is named is the
// one place it does not appear (#2403).
func TestTheNotesTabPromisesOnlyWhatItHolds(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	line := `{"type":"user","sessionId":"plain1","timestamp":"` + at + `","cwd":"/work/app",` +
		`"message":{"role":"user","content":"the retry budget on main"}}`
	if err := os.WriteFile(filepath.Join(store, "plain1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	remembered := `{"ts":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","project":"work/app",` +
		`"text":"keep the retry budget at 3"}`
	if err := os.WriteFile(notes, []byte(remembered+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(tmp, "page.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)

	m := regexp.MustCompile(`(?s)const S=(.*?),R=(.*?),N=(.*?);\n`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the page's script no longer starts with the three arrays")
	}
	var carried []map[string]any
	if err := json.Unmarshal([]byte(m[3]), &carried); err != nil {
		t.Fatalf("the notes array does not decode: %v", err)
	}
	holdsIt := false
	for _, n := range carried {
		if text, _ := n["text"].(string); strings.Contains(text, "retry budget at 3") {
			holdsIt = true
		}
	}
	// The sentence under the tab is a promise about what is above it: name
	// `deja remember` there and either its notes are on the tab, or the
	// sentence says where they are instead.
	tab := regexp.MustCompile(`(?s)<div id="tab-notes".*?</p>`).FindString(page)
	if tab == "" {
		t.Fatalf("the notes tab is no longer shaped as one block with a note under it")
	}
	if !holdsIt && strings.Contains(tab, "deja remember") && !strings.Contains(tab, "Sessions tab") {
		t.Errorf("the tab names `deja remember`, carries none of its notes, and does not say where they are:\n%s", tab)
	}
}

// The page said how much a rule had hidden and never which rule, while the CLI
// names it. It is also the artifact that leaves the machine, so it names the
// rule and not the file the rule lives in (#2354).
func TestThePageNamesTheRuleThatHidThings(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own connection pool question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	batch := t.TempDir()
	rec := index.SyncRecord{
		Harness: "claude", SessionID: "peersess", Project: "secretclient/api",
		Role: "user", Text: "the client ledger cutover",
		Time: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSync(dir, []string{"import", batch}); err != nil {
		t.Fatal(err)
	}

	policyPath := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyPath)

	out := filepath.Join(t.TempDir(), "view.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)
	if !strings.Contains(page, "Held back by the trust policy") {
		t.Fatalf("nothing was held back, so this measures nothing:\n%s", pageStatsBlock(page))
	}
	// The activation as well as the rule, the way the CLI note reads: on a
	// machine whose activations differ, "local-only" alone reads as deja's one
	// rule (#2367).
	if !strings.Contains(page, "search: local-only") {
		t.Errorf("the page does not name the rule and the door it governs:\n%s", pageStatsBlock(page))
	}
	// The rule, not the file it lives in: the page is passed around and the
	// path sits under the reader's home directory.
	if strings.Contains(page, policyPath) {
		t.Errorf("the page carries the path of the policy file")
	}
}

// viewSessionsEmbedded pulls the array the page hands to its own script.
func viewSessionsEmbedded(t *testing.T, page string) []map[string]any {
	t.Helper()
	const marker = "const S="
	_, after, ok := strings.Cut(page, marker)
	if !ok {
		t.Fatalf("the view page no longer embeds a session array; this test reads the wrong thing now")
	}
	var rows []map[string]any
	dec := json.NewDecoder(strings.NewReader(after))
	if err := dec.Decode(&rows); err != nil {
		t.Fatalf("decode embedded sessions: %v", err)
	}
	return rows
}

// The page is titles, project names and message previews written to a file
// meant to be looked at and passed around. Under a local-only rule every other
// surface withheld the imported sessions and this one embedded them whole.
func TestViewHonoursTheTrustPolicy(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own connection pool question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The imported prefix only exists on sessions that actually arrived by
	// sync, so the peer's session is brought in the way a peer's session
	// arrives rather than written into the local store by hand.
	batch := t.TempDir()
	rec := index.SyncRecord{
		Harness: "claude", SessionID: "peersess", Project: "secretclient/api",
		Role: "user", Text: "connection pool tuning for the client",
		Time: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSync(dir, []string{"import", batch}); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "view.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	before := len(viewSessionsEmbedded(t, readFileString(t, out)))
	if before < 2 {
		t.Fatalf("both sessions must be in the page with no policy set, got %d", before)
	}

	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyPath)

	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)
	for _, row := range viewSessionsEmbedded(t, page) {
		project, _ := row["project"].(string)
		if strings.HasPrefix(project, "imported:") {
			t.Fatalf("the page embeds %q, which the trust policy withholds from every other surface: %v", project, row)
		}
	}
	if strings.Contains(page, "secretclient") {
		t.Fatalf("a withheld project name is still somewhere in the page")
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The preview is cut by bytes, so a non-ASCII transcript can land the cut in
// the middle of a rune and the page then shows a replacement glyph.
func TestSessionPreviewCutsOnARuneBoundary(t *testing.T) {
	msgs := make([]model.Message, 0, 90)
	for range 90 {
		msgs = append(msgs, model.Message{Role: "user", Text: "x" + strings.Repeat("é", 40)})
	}
	// The trailing cut moved to clipForPage, which runs after the redaction —
	// cutting before it would slice a secret in half and print the half (#2100).
	out := clipForPage(sessionPreview(msgs))
	if len(out) <= viewPreviewBytes {
		t.Fatalf("fixture did not reach the cap: %d bytes", len(out))
	}
	if !utf8.ValidString(out) {
		t.Fatalf("preview is not valid UTF-8, tail: %q", out[len(out)-16:])
	}
	if strings.ContainsRune(out, utf8.RuneError) {
		t.Fatalf("preview ends in a replacement char, tail: %q", out[len(out)-16:])
	}
	if !strings.HasSuffix(out, "…") {
		t.Fatalf("preview lost its ellipsis, tail: %q", out[len(out)-16:])
	}
}

// #2316 recognises a withheld project by its name, learned from the sessions in
// the index. Forget those sessions and the name is unknown, so the digest went
// back onto the page. The record now carries the projects it was built from,
// which the policy can answer for on its own (#2324).
func TestViewWithholdsADigestWhoseProjectIsGone(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own connection pool question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// A digest served from a peer's project, with no session of that project
	// left in the index — forgotten, or simply never indexed here again.
	usage.RecordServedFrom(dir, usage.KindRecall,
		"<deja-recall>\n1. [claude] a peer project · the quaxbolt overflow was an int32 cast\n",
		1, 400, nil, []string{"imported:secretclient/api"}, "local+imported")
	usage.RecordServedFrom(dir, usage.KindRecall,
		"<deja-recall>\n1. [claude] tmp/mine · my own connection pool question\n",
		1, 400, nil, []string{"tmp/mine"}, "local+imported")

	out := filepath.Join(t.TempDir(), "view.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	if page := readFileString(t, out); !strings.Contains(page, "int32 cast") {
		t.Fatalf("the peer's digest is not on the page with no rule set, so this measures nothing")
	}

	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyPath)

	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)
	if strings.Contains(page, "int32 cast") {
		t.Errorf("a digest from a withheld project is on the page, though no session of it is left to name it")
	}
	if !strings.Contains(page, "my own connection pool question") {
		t.Errorf("the page dropped a digest about the machine's own work")
	}
}

// The page bounds its transcripts (viewPreviewBytes) and its notes (#2111).
// Digest text has no bound of its own: what keeps it small is the injection
// log's own rotation, which rewrites past 512 KB keeping the last records — so
// however much is written, the tab can only carry what the log still holds.
//
// This holds the page to that. Four megabytes of digests go in; the page stays
// one fast file, and the log is left with a handful of records rather than the
// five hundred that were written.
func TestThePageIsBoundedByTheInjectionLogsRotation(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Each digest the size of the largest a writer records — a whole-session
	// read over deja://session/… measured 8,216 bytes.
	body := strings.Repeat("the migration kept failing on the ledger table. ", 170)
	const wrote = 500
	for range wrote {
		usage.RecordDigestPolicy(dir, usage.KindResource, "<deja-recall>\n"+body+"\n", 1, 400, "local+imported")
	}

	kept := usage.Snapshots(dir, 0)
	if len(kept) >= wrote {
		t.Errorf("the log kept %d of %d digests — rotation is what bounds the page", len(kept), wrote)
	}

	out := filepath.Join(t.TempDir(), "view.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// The bound the notes cap is held to.
	if n := len(page); n > 3<<20 {
		t.Errorf("the page is %d bytes after %d digests were written", n, wrote)
	}
	if !strings.Contains(string(page), "the migration kept failing") {
		t.Fatal("no digest text reached the page, so this measures nothing")
	}
}

// A digest served while imported content was allowed keeps the peer's project
// and text. The session list on the page obeys a later local-only rule and the
// Recalls tab embedded the same three things — titles, project names, message
// text — verbatim (#2315).
func TestViewWithholdsRecallsFromHiddenProjects(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own connection pool question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	batch := t.TempDir()
	rec := index.SyncRecord{
		Harness: "claude", SessionID: "peersess", Project: "secretclient/api",
		Role: "assistant", Text: "the quaxbolt overflow was an int32 cast in the client's ledger",
		Time: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSync(dir, []string{"import", batch}); err != nil {
		t.Fatal(err)
	}

	// The digest as it was served while imported content was allowed.
	usage.RecordDigestPolicy(dir, usage.KindRecall,
		"<deja-recall>\n1. [claude] imported:secretclient/api · the quaxbolt overflow was an int32 cast\n",
		1, 400, "local+imported")
	// And one about the machine's own work, which nothing withholds.
	usage.RecordDigestPolicy(dir, usage.KindRecall,
		"<deja-recall>\n1. [claude] tmp/mine · my own connection pool question\n",
		1, 400, "local+imported")

	out := filepath.Join(t.TempDir(), "view.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	if page := readFileString(t, out); !strings.Contains(page, "int32 cast") {
		t.Fatalf("the peer's digest is not on the page with no policy set, so this measures nothing")
	}

	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyPath)

	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)
	if strings.Contains(page, "int32 cast") || strings.Contains(page, "secretclient") {
		t.Errorf("the page embeds a digest from a project the policy withholds")
	}
	// The reader's own recall is still there — the rule hides a project, not
	// the tab.
	if !strings.Contains(page, "my own connection pool question") {
		t.Errorf("the page dropped a digest about the machine's own work")
	}
}

// Two rules can be in force at once — one door served a digest, another hides
// things from this page — and the page answers both: each row carries the rule
// its digest was served under, the note names the rule holding rows back. The
// per-row half was rendered by the page's own script and pinned by nothing.
func TestThePageCarriesTheRuleEachDigestWasServedUnder(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own connection pool question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	usage.RecordDigestPolicy(dir, usage.KindRecall,
		"<deja-recall>\n1. my own connection pool question\n", 1, 400, "local+imported")

	out := filepath.Join(t.TempDir(), "view.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)
	m := regexp.MustCompile(`(?s)const S=(.*?),R=(.*?),N=(.*?);`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("the page no longer embeds a recalls array; this test reads the wrong thing now")
	}
	var recalls []struct {
		Kind   string `json:"kind"`
		Policy string `json:"policy"`
	}
	if err := json.Unmarshal([]byte(m[2]), &recalls); err != nil {
		t.Fatalf("decode recalls: %v", err)
	}
	if len(recalls) != 1 {
		t.Fatalf("recalls = %d, want the one just recorded", len(recalls))
	}
	if recalls[0].Policy != "local+imported" {
		t.Errorf("the row does not say which rule served it: %+v", recalls[0])
	}
	// And the page's own renderer prints it, rather than carrying it unused.
	if !strings.Contains(page, "r.policy") {
		t.Errorf("the page carries the rule but never shows it")
	}
}

// The Recalls tab carries the newest hundred injections and told the reader it
// carried every one of them — a claim the Notes tab beside it already knows how
// to qualify (#2313).
func TestThePageSaysTheRecallsTabIsAWindow(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	for i := range 130 {
		usage.RecordDigestInto(dir, usage.KindDejaVu,
			fmt.Sprintf("<deja-recall>\n  - digest-marker-%03d\n", i), fmt.Sprintf("agent%03d", i), 1, 100,
			[]string{"quaxbolt"}, "r0")
	}

	out := filepath.Join(t.TempDir(), "view.html")
	if _, err := captureRun(t, "view", "--no-open", "--out", out); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the newest injection is on the page and an older one is not.
	if !strings.Contains(string(page), "digest-marker-129") {
		t.Fatalf("the newest injection is not on the page, so this measures nothing")
	}
	if strings.Contains(string(page), "digest-marker-000") {
		t.Fatalf("the page carries all 130 injections, so nothing is being held back")
	}
	if strings.Contains(string(page), "every injection an agent received") {
		t.Errorf("the page still claims to hold every injection")
	}
	if !strings.Contains(string(page), "of 130") {
		t.Errorf("the page does not say how many injections there are:\n%s", recallNote(string(page)))
	}
}

// recallNote is the sentence under the Recalls tab, for a readable failure.
func recallNote(page string) string {
	i := strings.Index(page, `id="rlist"`)
	if i < 0 {
		return "(no recalls tab)"
	}
	end := min(i+400, len(page))
	return page[i:end]
}

// The page is a file on disk whose whole purpose is to be looked at and passed
// around, and `share` and `sync export` both redact on the way out. This one
// wrote whatever the index held — an index from an older deja, or one built
// before a pattern was fixed — and then counted the markers ingest had written
// and called them masked here (#1768).
func TestTheViewRedactsWhatTheIndexKept(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	const secret = "8f14e45fceea167a5a36dedd4bea2543" //nolint:gosec // fake secret the redaction must hide
	const token = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	rec := `{"type":"user","sessionId":"v1","cwd":"/w","timestamp":"2026-08-22T01:00:00Z","message":{"role":"user","content":"vexlorb {\"api_key\": \"` + secret + `\"} and ` + token + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NO_REDACT", "1")
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NO_REDACT", "")

	out := filepath.Join(tmp, "view.html")
	path, masked, err := writeViewHTML(dir, out)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	if strings.Contains(page, secret) {
		t.Error("the page carries an api key the index kept")
	}
	if strings.Contains(page, token) {
		t.Error("the page carries a github token the index kept")
	}
	if masked < 2 {
		t.Errorf("the page reports %d masked, and it masked at least two", masked)
	}
	if !strings.Contains(page, redact.Marker) {
		t.Error("nothing in the page says a secret was taken out of it")
	}
}

func viewFixtureIndex(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-app", "one.jsonl"), "one", []string{
		`{"type":"user","sessionId":"one","timestamp":"` + ts + `","message":{"role":"user","content":"<local-command-caveat>plumbing</local-command-caveat>"}}`,
		`{"type":"user","sessionId":"one","timestamp":"` + ts + `","message":{"role":"user","content":"real question about </script> escaping"}}`,
		`{"type":"user","sessionId":"one","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"text","text":"real answer"}]}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The view page is a self-contained file made to be passed around, so it must
// carry the same redaction floor the other outbound paths print when it holds a
// masked secret — and stay quiet when it does not, so local browsing is silent
// (#857).
func TestViewNamesTheRedactionFloorWhenThePageHoldsAMaskedSecret(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-app", "sec.jsonl"), "sec", []string{
		`{"type":"user","sessionId":"sec","timestamp":"` + ts + `","message":{"role":"user","content":"the key was AKIAABCDABCDABCDABCD in the config"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "v.html")
	stderr, err := captureRunStderr(t, "view", "--no-open", "--out", out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "pattern redaction is a floor") {
		t.Fatalf("view did not name the redaction floor for a page carrying a masked secret:\n%s", stderr)
	}
}

func TestViewStaysQuietWhenNothingIsMasked(t *testing.T) {
	viewFixtureIndex(t)
	out := filepath.Join(t.TempDir(), "v.html")
	stderr, err := captureRunStderr(t, "view", "--no-open", "--out", out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "pattern redaction is a floor") {
		t.Fatalf("view printed the floor on a page with no masked content:\n%s", stderr)
	}
}

func TestViewGeneratesSelfContainedHTML(t *testing.T) {
	dir := viewFixtureIndex(t)
	out := filepath.Join(t.TempDir(), "view.html")
	path, _, err := writeViewHTML(dir, out)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"deja view", "Sessions", "Recalls", "Notes", "real question about"} {
		if !strings.Contains(s, want) {
			t.Fatalf("view missing %q", want)
		}
	}
	// Self-contained: no external stylesheet, script, font or image fetches.
	if regexp.MustCompile(`(?i)<(link|script|img|iframe)\b[^>]*\b(src|href)="https?://`).MatchString(s) {
		t.Fatal("view references external assets")
	}
	// Script-closer inside indexed text must not break out of the data block.
	if strings.Contains(s, "about </script>") {
		t.Fatal("unescaped script closer leaked into the page")
	}
	// Plumbing wrappers stay out of previews.
	if strings.Contains(s, "local-command-caveat") {
		t.Fatal("plumbing leaked into preview")
	}
}

func TestViewPreviewCap(t *testing.T) {
	long := strings.Repeat("word ", viewPreviewBytes)
	got := clipForPage(sessionPreview([]model.Message{{Role: "user", Text: long}}))
	if len(got) > viewPreviewBytes+8 {
		t.Fatalf("preview exceeds cap: %d", len(got))
	}
}

// Same as the stats card: the view handed back the bare syscall when its
// target sat on a disk that is gone (#1036).
func TestViewSaysWhatIsWrongWithThePath(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	_, _, err := writeViewHTML(dir, filepath.Join(tmp, "no-such-dir", "m.html"))
	if err == nil {
		t.Fatal("writing into a directory that does not exist did not fail")
	}
	if !strings.Contains(err.Error(), "its directory does not exist") {
		t.Errorf("the error does not say what is wrong: %v", err)
	}
	if strings.Contains(err.Error(), "no such file or directory") {
		t.Errorf("the syscall is still handed back: %v", err)
	}
}
