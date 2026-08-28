package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/stats"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// viewTranscripts caps how many recent sessions embed message previews; the
// rest stay browsable by metadata. viewPreviewBytes caps each preview so the
// page stays a single fast file even over a multi-gigabyte store.
const (
	viewTranscripts  = 200
	viewPreviewBytes = 6 << 10
	viewRecalls      = 100
	// The same bound for notes, for the same reason: a store with a thousand
	// promoted notes made the page 7.6 MB, which is not the single fast file
	// the transcript cap exists to keep (#2111).
	viewNotes = 200
)

type viewSession struct {
	ID      string `json:"id"`
	Harness string `json:"harness"`
	Project string `json:"project"`
	Title   string `json:"title"`
	Updated string `json:"updated"`
	Preview string `json:"preview,omitempty"`
}

type viewRecall struct {
	Time     string   `json:"time"`
	Kind     string   `json:"kind"`
	Sessions int      `json:"sessions"`
	Bytes    int      `json:"bytes"`
	Policy   string   `json:"policy,omitempty"`
	Terms    []string `json:"terms,omitempty"`
	Digest   string   `json:"digest"`
}

type viewNote struct {
	Project string   `json:"project"`
	State   string   `json:"state"`
	Title   string   `json:"title"`
	Text    string   `json:"text"`
	Tags    []string `json:"tags,omitempty"`
	At      string   `json:"at"`
}

type viewPage struct {
	GeneratedAt   string
	TotalSessions int
	Harnesses     int
	DateStart     string
	DateEnd       string
	SessionsJSON  template.JS
	RecallsJSON   template.JS
	NotesJSON     template.JS
	PreviewCount  int
	// PolicyRule names the rule that held things back, as the CLI note names
	// it — the activation and what it allows. Not the file it lives in: that
	// path sits under the reader's home directory and this page is meant to be
	// passed around (#2354).
	PolicyRule string
	// SessionsWithheld is how many sessions a trust rule kept off the page.
	SessionsWithheld int
	// NotesWithheld is how many promoted notes a trust rule kept off the page.
	NotesWithheld int
	// RecallsWithheld is how many stored digests a trust rule kept off the page.
	RecallsWithheld int
	// RecallCount is how many injections the page carries and TotalRecalls how
	// many the log holds, for the same reason the two note counts exist.
	RecallCount  int
	TotalRecalls int
	// NoteCount is how many notes the page carries and TotalNotes how many
	// there are, so the page can say when those differ (#2111).
	NoteCount  int
	TotalNotes int
}

func runView(dir string, args []string) error {
	out := ""
	openBrowser := true
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--out":
			if i+1 >= len(args) {
				return fmt.Errorf("view: --out needs a path")
			}
			i++
			out = args[i]
		case "--no-open":
			openBrowser = false
		default:
			return fmt.Errorf("view: unknown flag %q", args[i])
		}
	}
	if err := index.EnsureForSearch(dir, query.Options{All: true}, false, os.Stderr); err != nil {
		return err
	}
	if dir == "" {
		dir = index.DefaultDir()
	}
	if out == "" {
		out = dir + ".view.html"
	}
	path, masked, err := writeViewHTML(dir, out)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "deja: view written to %s\n", path)
	// The page is a self-contained file whose whole purpose is to be looked at
	// and passed around, so it carries the same redaction floor the other
	// outbound paths (share, sync export, promote --to) print — but only when it
	// actually holds masked content, so local browsing stays quiet (#857).
	if masked > 0 {
		fmt.Fprintf(os.Stderr, "deja: %d secret%s masked in this page. pattern redaction is a floor — review before sharing; rotate anything that leaked.\n", masked, pluralS(masked))
	}
	if openBrowser {
		openInBrowser(path)
	}
	return nil
}

// redactMask is redact.Text without its counts, for the fields below.
func redactMask(s string) string {
	out, _ := redact.Text(s)
	return out
}

// safeLineForPage is redactMask plus the scrub every printed row gets: a page
// is read by a person too, and a bidi override reverses a line in a browser as
// surely as in a terminal (#2090). For the one-line fields — a project, a
// title — where a line break would be a second row rather than a paragraph.
func safeLineForPage(s string) string { return search.SafeLine(redactMask(s)) }

// safeTitleForPage bounds as well as scrubs. A note's title carries no bound
// into the index on purpose — the state suffix is what a one-line surface reads
// it for — so the readers clip it, and this page is one of them (#2092).
func safeTitleForPage(s string) string { return search.SafeNoteTitle(redactMask(s)) }

// safeNameForPage is for the fields a person copies rather than reads — a
// project is what `--project` is given back — so the spaces inside it are part
// of it, the way they are in a path (#2044).
func safeNameForPage(s string) string { return search.SafePath(redactMask(s)) }

// safeTextForPage is the same for a body, where the newlines are meant.
func safeTextForPage(s string) string { return search.SafeText(redactMask(s)) }

func writeViewHTML(dir, out string) (string, int, error) {
	abs, err := filepath.Abs(out)
	if err != nil {
		return "", 0, err
	}
	metas, err := index.Recent(dir, 0)
	if err != nil {
		return "", 0, err
	}
	// The page is titles, project names and message previews — the whole of
	// what the trust policy exists to keep off the screen, and it is written to
	// a file meant to be looked at and passed around. Every other surface
	// consulted the policy and this one did not: it embedded imported sessions
	// a local-only rule had already withheld from search, the listing, stats
	// and the agent. Browsing, so the search activation governs it (#937).
	hiddenProjects := policyHiddenProjects(policy.ActivationSearch, metas)
	metas, policyHidden := policyFilterSessionsCounted(policy.ActivationSearch, metas)
	if note := policyHiddenNote(policy.ActivationSearch, policyHidden); note != "" {
		fmt.Fprint(os.Stderr, note)
	}
	report := stats.Build(metas, time.Now())
	page := viewPage{
		GeneratedAt:   time.Now().Format("2006-01-02 15:04"),
		TotalSessions: report.TotalSessions,
		Harnesses:     len(report.Harnesses),
		// On the page too, not only on stderr: the file is what someone looks
		// at and passes on, and a page a rule emptied read as a machine with
		// no history at all — the misread #2319 closed on friction (#2321).
		SessionsWithheld: policyHidden,
		PolicyRule:       policy.Load().Describe(policy.ActivationSearch),
	}
	if len(metas) > 0 {
		page.DateEnd = metas[0].Updated.Local().Format("2006-01-02")
		page.DateStart = metas[len(metas)-1].Updated.Local().Format("2006-01-02")
	}
	sessions := make([]viewSession, 0, len(metas))
	for i, s := range metas {
		v := viewSession{
			ID: s.ID, Harness: s.Harness, Project: s.Project,
			Title:   strings.TrimSpace(s.Title),
			Updated: s.Updated.Format("2006-01-02 15:04"),
		}
		if i < viewTranscripts {
			if full, ok, err := index.FindByPrefix(dir, s.ID); err == nil && ok {
				v.Preview = sessionPreview(full.Messages)
				page.PreviewCount++
			}
		}
		sessions = append(sessions, v)
	}
	// Every injection there is, so the page can say what it left behind: the
	// tab used to claim it held all of them while carrying the newest hundred
	// (#2313). Reading them all costs nothing extra — Snapshots parses the
	// whole file and cuts at the end either way.
	allRecalls := usage.Snapshots(dir, 0)
	// A digest is titles, project names and message text already assembled —
	// the three things the filter above keeps off this page — and it outlives
	// the rule it was served under: content recalled while imported sessions
	// were allowed stayed on the page after a local-only rule withheld them
	// everywhere else (#2315). The record has no project field, so what is
	// recognisable is the name of a project being withheld now.
	allRecalls, page.RecallsWithheld = withoutHiddenProjects(allRecalls, hiddenProjects)
	page.TotalRecalls = len(allRecalls)
	if len(allRecalls) > viewRecalls {
		allRecalls = allRecalls[:viewRecalls]
	}
	page.RecallCount = len(allRecalls)
	recalls := make([]viewRecall, 0, len(allRecalls))
	for _, sn := range allRecalls {
		recalls = append(recalls, viewRecall{
			Time: sn.Time.Local().Format("2006-01-02 15:04"), Kind: sn.Kind,
			Sessions: sn.Sessions, Bytes: sn.Bytes, Policy: sn.Policy,
			Terms: sn.Terms, Digest: sn.Digest,
		})
	}
	loaded := sources.LoadPromotedNotes()
	// A promoted note is a session's decision in the reader's own words, and it
	// keeps the project it came from — so a note promoted from an imported
	// session stayed on this page after a local-only rule withheld that project
	// from search, the listing and the agent. Same gap as the digests above,
	// with the project known exactly rather than recognised in prose (#2317).
	loaded, page.NotesWithheld = notesAllowedOnPage(loaded)
	// By date, newest first: LoadPromotedNotes returns them in the order they
	// were first written to the file, so cutting that keeps an arbitrary set
	// rather than the newest — and the page's own order was the file's.
	sort.SliceStable(loaded, func(i, j int) bool { return loaded[i].At.After(loaded[j].At) })
	page.TotalNotes = len(loaded)
	if len(loaded) > viewNotes {
		loaded = loaded[:viewNotes]
	}
	page.NoteCount = len(loaded)
	notes := make([]viewNote, 0, len(loaded))
	for _, n := range loaded {
		notes = append(notes, viewNote{
			Project: n.Project, State: n.State, Title: n.Title, Text: n.Text,
			Tags: n.Tags, At: n.At.Format("2006-01-02"),
		})
	}
	// Redact here rather than trusting the index: the page is the artifact
	// someone opens and passes on, and the index it is built from may have been
	// written by an older deja or before a pattern was fixed.
	for i := range sessions {
		// The id too: it is a filename stem for most harnesses, and a
		// filename takes a control byte on every Unix filesystem.
		sessions[i].ID = safeNameForPage(sessions[i].ID)
		sessions[i].Title = safeTitleForPage(sessions[i].Title)
		sessions[i].Preview = clipForPage(safeTextForPage(sessions[i].Preview))
		sessions[i].Project = safeNameForPage(sessions[i].Project)
	}
	for i := range recalls {
		recalls[i].Digest = safeTextForPage(recalls[i].Digest)
		for j := range recalls[i].Terms {
			recalls[i].Terms[j] = safeLineForPage(recalls[i].Terms[j])
		}
	}
	for i := range notes {
		notes[i].Title = safeTitleForPage(notes[i].Title)
		// Capped like a session's preview, and for the same reason: a promoted
		// note carries whatever was promoted, and one of a megabyte outweighed
		// a hundred sessions on the page (#2100).
		notes[i].Text = clipForPage(safeTextForPage(notes[i].Text))
		// A note's project and tags are what the user typed, so they are text
		// like the rest rather than structure deja minted.
		notes[i].Project = safeNameForPage(notes[i].Project)
		for j := range notes[i].Tags {
			notes[i].Tags[j] = safeNameForPage(notes[i].Tags[j])
		}
	}
	sj, err := json.Marshal(sessions)
	if err != nil {
		return "", 0, err
	}
	rj, err := json.Marshal(recalls)
	if err != nil {
		return "", 0, err
	}
	nj, err := json.Marshal(notes)
	if err != nil {
		return "", 0, err
	}
	page.SessionsJSON = jsonForScript(sj)
	page.RecallsJSON = jsonForScript(rj)
	page.NotesJSON = jsonForScript(nj)
	var b strings.Builder
	if err := viewTemplate.Execute(&b, page); err != nil {
		return "", 0, fmt.Errorf("render view: %w", err)
	}
	// The page holds a masked spot for every secret taken out of it, here or at
	// ingest; counting the rendered page reports both without claiming which,
	// and counts what a reader will actually find in the file.
	masked := strings.Count(b.String(), redact.Marker)
	if err := os.WriteFile(abs, []byte(b.String()), 0o644); err != nil {
		// A path on a disk that is gone came back as the bare syscall, which
		// says nothing about what to change (#1036).
		return "", 0, fmt.Errorf("cannot write the view to %s — %s", abs, writeFailureReason(err))
	}
	return abs, masked, nil
}

// jsonForScript makes embedded JSON safe inside a <script> block.
func jsonForScript(b []byte) template.JS {
	s := string(b)
	s = strings.ReplaceAll(s, "</", "<\\/")
	return template.JS(s) // #nosec G203 -- JSON-marshalled, script-closer escaped
}

// sessionPreview flattens the first messages of a transcript into a capped
// plain-text preview. Text comes from the index, so it is already redacted.
func sessionPreview(msgs []model.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		t := strings.TrimSpace(m.Text)
		if t == "" || digest.IsAgentArtifact(t) || strings.HasPrefix(t, "<local-command") ||
			strings.HasPrefix(t, "<command-") || strings.HasPrefix(t, "Caveat:") {
			continue
		}
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(t)
		b.WriteString("\n")
		if b.Len() >= viewPreviewBytes {
			break
		}
	}
	// Whole messages, and the trailing cut is left to clipForPage: cutting here
	// happens before the redaction below, so a secret sliced in half stops
	// matching the pattern that would have masked it and the half is printed.
	return b.String()
}

// clipForPage bounds a cell at the size a preview gets, cutting on a rune
// boundary and saying that it cut.
func clipForPage(s string) string {
	if len(s) <= viewPreviewBytes {
		return s
	}
	cut := viewPreviewBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func openInBrowser(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	_ = cmd.Start()
}

// withoutHiddenProjects drops the digests that name a withheld project, and
// says how many went. Substring rather than equality: the digest renders the
// project inside a line of prose, and the name is what a reader would see.
func withoutHiddenProjects(snaps []usage.Snapshot, hidden map[string]bool) ([]usage.Snapshot, int) {
	pol := policy.Load()
	kept := make([]usage.Snapshot, 0, len(snaps))
	for _, sn := range snaps {
		if snapshotWithheld(pol, sn, hidden) {
			continue
		}
		kept = append(kept, sn)
	}
	return kept, len(snaps) - len(kept)
}

// snapshotWithheld decides whether a stored digest may go on the page. A record
// that names its own projects is answered by the policy alone, which is what
// makes it right when the sessions behind it have left the index (#2324). One
// written before that field existed can only be recognised by the names of the
// projects a rule is hiding now, and that is the older, weaker test.
func snapshotWithheld(pol policy.Policy, sn usage.Snapshot, hidden map[string]bool) bool {
	if len(sn.Projects) > 0 {
		for _, project := range sn.Projects {
			if !pol.Allows(policy.ActivationSearch, project) {
				return true
			}
		}
		return false
	}
	for project := range hidden {
		if strings.Contains(sn.Digest, project) {
			return true
		}
	}
	return false
}

// The page and `deja forget` read an old record differently on purpose. Here a
// name anywhere in the text is enough, because the cost of a wrong match is a
// digest of your own work missing from a page you can regenerate. forget looks
// only where a digest renders a project, because there the cost is deleting a
// record that cannot come back (#2330).

// notesAllowedOnPage drops the promoted notes whose project a rule withholds,
// and says how many went. Browsing, so the search activation governs it — the
// same activation the session list on this page is filtered by.
func notesAllowedOnPage(notes []sources.PromotedNote) ([]sources.PromotedNote, int) {
	p := policy.Load()
	kept := make([]sources.PromotedNote, 0, len(notes))
	for _, n := range notes {
		if n.Project != "" && !p.Allows(policy.ActivationSearch, n.Project) {
			continue
		}
		kept = append(kept, n)
	}
	return kept, len(notes) - len(kept)
}
