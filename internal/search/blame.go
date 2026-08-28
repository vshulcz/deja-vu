package search

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

type BlameTarget struct {
	FullPath string
	Base     string
	Stem     string
}

type BlameOptions struct {
	Harness string
	Project string
	Since   time.Duration
	All     bool
}

type BlameHit struct {
	Session  model.Session `json:"session"`
	Title    string        `json:"title"`
	Count    int           `json:"count"`
	Snippets []string      `json:"snippets"`
	Score    float64       `json:"score"`
	Tier     string        `json:"tier"`
	// Lifecycle carries a decision that did not hold. blame answers "who
	// decided this", and it was answering with the accepted line of a decision
	// that had been taken back (#1017).
	Lifecycle     string `json:"lifecycle,omitempty"`
	LifecycleNote string `json:"lifecycle_note,omitempty"`
	LifecycleAt   string `json:"lifecycle_at,omitempty"`
}

// trimLineSuffix drops the `:266` or `:266:12` an editor, a stack trace or a
// compiler error leaves on a path. blame works at file granularity, so the line
// number is precision it cannot use — and taken literally it became part of the
// basename and matched nothing (#1625).
//
// Only trailing digits, and only while something is left in front: a colon is
// legal in a unix filename, and `C:\src\main.go` carries one that must survive.
func trimLineSuffix(name string) string {
	for i := 0; i < 2; i++ {
		head, tail, ok := lastColon(name)
		if !ok || tail == "" || !allDigits(tail) || head == "" {
			return name
		}
		if filepath.Base(head) == "" || strings.HasSuffix(head, ":") {
			return name
		}
		name = head
	}
	return name
}

// lastColon splits on the final colon, ignoring one that would leave nothing in
// front of it.
func lastColon(name string) (string, string, bool) {
	i := strings.LastIndexByte(name, ':')
	if i <= 0 {
		return "", "", false
	}
	return name[:i], name[i+1:], true
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func ResolveBlamePath(name string) (BlameTarget, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return BlameTarget{}, fmt.Errorf("path required")
	}
	name = trimLineSuffix(name)
	full, err := filepath.Abs(name)
	if err != nil {
		return BlameTarget{}, err
	}
	full = filepath.Clean(full)
	base := filepath.Base(full)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return BlameTarget{}, fmt.Errorf("path must name a file")
	}
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		stem = base
	}
	return BlameTarget{FullPath: full, Base: base, Stem: stem}, nil
}

func Blame(ss []model.Session, target BlameTarget, o BlameOptions) []BlameHit {
	// One clock reading for the whole ranking. Called per session, the decay
	// differed by nanoseconds between candidates, so sessions with identical
	// evidence and identical timestamps sorted differently on every run — five
	// runs, five different top hits once blame started seeing the sessions that
	// only touched the file (#688).
	now := time.Now()
	cut := time.Time{}
	if o.Since > 0 {
		cut = now.Add(-o.Since)
	}
	base := strings.ToLower(filepath.ToSlash(target.Base))
	forms := blameForms(target.FullPath)
	hits := make([]BlameHit, 0)
	for _, session := range mergeSessions(ss) {
		if o.Harness != "" && session.Harness != o.Harness {
			continue
		}
		if o.Project != "" && !strings.Contains(strings.ToLower(session.Project), strings.ToLower(o.Project)) {
			continue
		}
		if !cut.IsZero() && session.Updated.Before(cut) {
			continue
		}
		hit := BlameHit{Session: session, Title: sessionTitle(session), Tier: TierExact}
		specificity := 0.0
		// Quote the messages that say the most about the file, not the first two
		// to name it. A session that mentions a file in passing and discusses it
		// properly further down was quoted on the passing line, which is the
		// evidence a reader judges the hit by (#1329).
		type mention struct {
			text  string
			count int
			level float64
			role  string
		}
		var mentions []mention
		for _, message := range session.Messages {
			count, level := mentionScore(message.Text, base, forms)
			if count == 0 {
				continue
			}
			hit.Count += count
			if level > specificity {
				specificity = level
			}
			mentions = append(mentions, mention{message.Text, count, level, message.Role})
		}
		// A path-shaped mention outranks a bare filename however often the bare
		// name is repeated; among equally specific ones, the message that keeps
		// returning to the file wins; among equals, the order they were said in.
		sort.SliceStable(mentions, func(i, j int) bool {
			if mentions[i].level != mentions[j].level {
				return mentions[i].level > mentions[j].level
			}
			return mentions[i].count > mentions[j].count
		})
		for i := 0; i < len(mentions) && i < 2; i++ {
			hit.Snippets = append(hit.Snippets, blameSnippet(mentions[i].text, mentions[i].role, target))
		}
		if hit.Count == 0 {
			continue
		}
		score := float64(hit.Count) * (1 + specificity)
		if projectContainsFile(session.Project, target.FullPath) {
			score *= 1.35
		}
		hit.Score = score * freshnessDecay(session.Updated, now)
		hits = append(hits, hit)
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if !hits[i].Session.Updated.Equal(hits[j].Session.Updated) {
			return hits[i].Session.Updated.After(hits[j].Session.Updated)
		}
		return hits[i].Session.ID < hits[j].Session.ID
	})
	if !o.All && len(hits) > 10 {
		hits = hits[:10]
	}
	return hits
}

func blameForms(full string) []string {
	clean := strings.ToLower(filepath.ToSlash(filepath.Clean(full)))
	parts := strings.Split(strings.TrimPrefix(clean, "/"), "/")
	forms := make([]string, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		form := strings.Join(parts[i:], "/")
		forms = append(forms, "/"+form, form)
	}
	return forms
}

func mentionScore(text, base string, forms []string) (int, float64) {
	low := strings.ToLower(filepath.ToSlash(text))
	count := 0
	level := 1.0
	for _, form := range forms {
		if pathFormCount(low, form) > 0 {
			candidate := 1.0 + float64(len(strings.Split(form, "/")))/4
			if candidate > level {
				level = candidate
			}
		}
	}
	for pos := 0; ; {
		i := strings.Index(low[pos:], base)
		if i < 0 {
			break
		}
		i += pos
		if pathComponentOrWord(low, i, i+len(base)) {
			count++
		}
		pos = i + len(base)
	}
	return count, level
}

func pathFormCount(s, form string) int {
	count := 0
	for pos := 0; ; {
		i := strings.Index(s[pos:], form)
		if i < 0 {
			return count
		}
		i += pos
		if boundary(s, i, true) && boundary(s, i+len(form), false) {
			count++
		}
		pos = i + len(form)
	}
}

func pathComponentOrWord(s string, start, end int) bool {
	if start > 0 && end < len(s) && s[start-1] == '/' && s[end] == '/' {
		return true
	}
	return boundary(s, start, true) && boundary(s, end, false)
}

func boundary(s string, at int, before bool) bool {
	if at == 0 || at == len(s) {
		return true
	}
	if before {
		r, _ := utf8.DecodeLastRuneInString(s[:at])
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
	}
	r, _ := utf8.DecodeRuneInString(s[at:])
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
}

func projectContainsFile(project, full string) bool {
	if project == "" || !filepath.IsAbs(project) {
		return false
	}
	root := filepath.Clean(project)
	rel, err := filepath.Rel(root, full)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "."
}

func sessionTitle(s model.Session) string {
	if s.Title != "" {
		return s.Title
	}
	for _, message := range s.Messages {
		if message.Role == "user" {
			text := strings.Join(strings.Fields(message.Text), " ")
			runes := []rune(text)
			if len(runes) > 60 {
				return string(runes[:60]) + "..."
			}
			return text
		}
	}
	return ""
}

func PrintBlame(w io.Writer, hits []BlameHit, jsonOutput bool) {
	for i := range hits {
		if hits[i].Tier == "" {
			hits[i].Tier = TierExact
		}
	}
	if jsonOutput {
		_ = json.NewEncoder(w).Encode(hits)
		return
	}
	color := colorOK(w)
	for _, hit := range hits {
		date := "-"
		if !hit.Session.Updated.IsZero() {
			date = hit.Session.Updated.Format("2006-01-02")
		}
		// id, project and title reach a terminal here and the agent through the
		// MCP blame tool. All three are free text from the transcript — an
		// imported peer's title especially — so a bare escape or bidi run would
		// repaint the line or reorder it. SafeLine strips them the way the
		// digest and snippet paths already do.
		id := SafeLine(short(hit.Session.ID))
		project := SafeLine(hit.Session.Project)
		title := SafeLine(hit.Title)
		if color {
			sep := cDim + " · " + cReset
			fmt.Fprintf(w, "%s%s%s %s%s%s%s", harnessTag(hit.Session.Harness, true), sep, date, cBold+id+cReset, sep, project, "")
			if title != "" {
				fmt.Fprintf(w, "%s%s", sep, cBold+title+cReset)
			}
		} else {
			fmt.Fprintf(w, "%s · %s · %s · %s", date, hit.Session.Harness, id, project)
			if title != "" {
				fmt.Fprintf(w, " · %s", title)
			}
		}
		fmt.Fprintln(w)
		if line := BlameLifecycleLine(hit); line != "" {
			if color {
				fmt.Fprintf(w, "  %s%s%s\n", cDim, line, cReset)
			} else {
				fmt.Fprintf(w, "  %s\n", line)
			}
		}
		for _, text := range hit.Snippets {
			if color {
				fmt.Fprintf(w, "  %s%s%s\n", cDim, text, cReset)
			} else {
				fmt.Fprintf(w, "  %s\n", text)
			}
		}
	}
}

// blameSnippet renders one mention. The prose path collapses runs of whitespace
// — right for a sentence, wrong for a file whose name holds two spaces, which
// came back with one and then found nothing when it was pasted into restore
// (#2044). A files record is a list of paths, so the line that names the file is
// printed as a path instead.
func blameSnippet(text, role string, target BlameTarget) string {
	switch role {
	case "files":
		if line := pathLineFor(text, target); line != "" {
			return SafePath(line)
		}
	case "edit":
		// An edit is "path\nspan": the first line is the file, the rest is what
		// stopped existing and is prose as far as a snippet goes.
		path, span, _ := strings.Cut(text, "\n")
		if pathLineFor(path, target) != "" {
			// A space put nothing between the two, and this command is written
			// for paths that contain spaces (the comment above), so the reader
			// could not tell where the path ended — on the line they paste into
			// `deja restore` (#2284). An em dash cannot occur in either half by
			// accident.
			if cut := snippet(span, target.Base, nil); strings.TrimSpace(cut) != "" {
				return strings.TrimSpace(SafePath(path)) + " — " + strings.TrimSpace(cut)
			}
			return strings.TrimSpace(SafePath(path))
		}
	}
	return snippet(text, target.Base, nil)
}

// pathLineFor picks the line of a record that names the file blame was asked
// about. By the file's own name, not by containing it: "mypool.go" contains
// "pool.go", and printing a sibling as the answer is the same wrong-path bug
// this renderer exists to fix. The full path wins over a bare match, so a
// vendored copy does not stand in for the file itself.
func pathLineFor(text string, target BlameTarget) string {
	base := strings.ToLower(target.Base)
	full := crossSlash(target.FullPath)
	fallback := ""
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		slashed := crossSlash(trimmed)
		if full != "" && slashed == full {
			return line
		}
		if crossBase(trimmed) == base && fallback == "" {
			fallback = line
		}
	}
	return fallback
}

// crossSlash and crossBase read a path the way the rest of deja does: a store
// synced from Windows holds "C:\\src\\app\\x.go", and on a unix host
// filepath sees one segment — which made the picker miss the line and fall back
// to the prose renderer, quietly bringing the collapsing back (#2044).
func crossSlash(p string) string {
	return strings.ToLower(strings.ReplaceAll(p, "\\", "/"))
}

func crossBase(p string) string {
	s := crossSlash(p)
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// BlameLifecycleLine words a withdrawn decision for blame the way search words
// it: what happened, not the name of the state.
func BlameLifecycleLine(h BlameHit) string {
	if h.Lifecycle == "" {
		return ""
	}
	var head string
	switch h.Lifecycle {
	case "rejected":
		head = "this was tried and rejected"
	case "superseded":
		head = "a later decision replaced this"
	case "stale":
		head = "marked stale — may no longer hold"
	default:
		head = SafeLine(h.Lifecycle)
	}
	// LifecycleAt and especially LifecycleNote are free text carried from
	// another machine by sync, and this line reaches a terminal and the MCP
	// blame tool — sanitise them like the fields above.
	if h.LifecycleAt != "" {
		head += " (" + SafeLine(h.LifecycleAt) + ")"
	}
	if h.LifecycleNote != "" {
		head += ": " + SafeNote(h.LifecycleNote)
	}
	return head
}
