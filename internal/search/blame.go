package search

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/termwidth"

	"github.com/vshulcz/deja-vu/internal/query"
)

type BlameTarget struct {
	FullPath string
	Base     string
	Stem     string
	// Line is the line the reader asked about, 0 when they named the file
	// alone. `path:line` has always been accepted and the number thrown away;
	// it is what the line-level answer needs to turn a line into the commit
	// that wrote it (#1181).
	Line int
	// LineNote says why a line spec the reader typed was not used, and is
	// empty when they typed none or typed a usable one. `a.txt:99` already
	// says "the file has 3 lines" (#3726); `a.txt:0` and `a.txt:abc` said
	// nothing at all and answered about the file as though no line had been
	// asked for (#3738).
	LineNote string
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
	// The same bound the search hits carry: the messages that mention the file
	// rather than the session's whole transcript, which was 46 MB of one JSON
	// answer on a real store (#3620).
	MessagesTotal  int  `json:"messages_total,omitempty"`
	MessagesCapped bool `json:"messages_capped,omitempty"`
	// matched are the indices of those messages, in message order.
	matched []int
	// Specificity is how fully the session named the file — a path against a
	// bare name — and is what orders the answer before the score does (#2840).
	Specificity float64 `json:"specificity"`
	Tier        string  `json:"tier"`
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

// maxBlamePath is PATH_MAX on Linux, the longest of the common limits.
const maxBlamePath = 4096

func ResolveBlamePath(name string) (BlameTarget, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return BlameTarget{}, fmt.Errorf("path required")
	}
	// Longer than any file system allows, and every directory in it is a form
	// matched against every message: a pasted 100 KB blob took over 20s.
	if len(name) > maxBlamePath {
		return BlameTarget{}, fmt.Errorf("path is %d bytes, longer than any file path (%d)", len(name), maxBlamePath)
	}
	name, line, lineNote := cutLineSuffix(name)
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
	return BlameTarget{
		FullPath: full,
		Base:     base,
		Stem:     stem,
		Line:     line,
		LineNote: lineNote,
	}, nil
}

// cutLineSuffix is trimLineSuffix that keeps the number it cut: the first one,
// so `file.go:120:14` reads as line 120 rather than column 14.
//
// The third return is what to say when a line spec was typed and cannot be
// used. Falling back to the file-level answer is right — it is a correct
// answer to a near-miss question — but doing it silently reads as though the
// line had never been asked about, which is the thing #3726 set out to stop
// one case earlier (#3738).
func cutLineSuffix(name string) (string, int, string) {
	trimmed := trimLineSuffix(name)
	if trimmed == name {
		note := mistypedLineNote(name)
		if note == "" {
			return name, 0, ""
		}
		// The note promises the whole file, so the file is what is searched:
		// searching `pool.go:abc` as written found nothing under a line that
		// said it was answering for pool.go. A file really named that way is
		// still taken as written.
		if _, err := os.Stat(name); err == nil {
			return name, 0, ""
		}
		head, _, _ := lastColon(name)
		return head, 0, note
	}
	rest := strings.TrimPrefix(name[len(trimmed):], ":")
	head, _, _ := strings.Cut(rest, ":")
	n, err := strconv.Atoi(head)
	if err != nil {
		// Digits trimLineSuffix accepted but strconv could not: a number past
		// what an int holds. It is a line spec, just not a reachable line.
		return trimmed, 0, fmt.Sprintf("%q is too large to be a line number", head)
	}
	if n <= 0 {
		return trimmed, 0, fmt.Sprintf("there is no line %d", n)
	}
	return trimmed, n, ""
}

// mistypedLineNote covers the specs trimLineSuffix never cut, because what
// follows the colon is not digits: `a.txt:abc`, `a.txt:-1`, `a.txt:2.5`. The
// whole string stays the path, which is the behaviour a colon in a real
// filename needs, so this says what the reader probably meant rather than
// changing what is searched.
//
// Narrow on purpose: only when the part before the colon looks like a filename
// — it has an extension — and the part after holds no separator. `notes:draft`
// and `C:\src\main.go` are left alone. A file genuinely named `a.txt:abc`
// would collect the note and still be searched for as written, which is a
// sentence too many rather than a wrong answer.
func mistypedLineNote(name string) string {
	head, tail, ok := lastColon(name)
	if !ok || tail == "" || allDigits(tail) {
		return ""
	}
	if strings.ContainsAny(tail, "/\\") || filepath.Ext(head) == "" {
		return ""
	}
	return fmt.Sprintf("%q is not a line number", tail)
}

// Blame ranks every session that carries evidence for the file, in full. The
// listing cut lives in CapBlame.
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
	// `scripts/load` and `bin/cache` are named by an ordinary word, and every
	// session that said "under load" or "the cache layer" mentioned it. For
	// such a name only the path counts: its directory and the name together.
	inDir := ""
	if ordinaryWordName(target.Base) {
		inDir = strings.ToLower(filepath.ToSlash(filepath.Join(filepath.Base(filepath.Dir(target.FullPath)), target.Base)))
	}
	hits := make([]BlameHit, 0)
	for _, session := range mergeSessions(ss) {
		if o.Harness != "" && session.Harness != o.Harness {
			continue
		}
		if !query.ProjectMatches(session.Project, session.From, o.Project) {
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
		for mi, message := range session.Messages {
			// The transcript's record of a past `deja blame` names the file in
			// every line of its own output, so blame ranked its own answer as
			// the history of the file and quoted it back. Recall was fixed for
			// this in #2068 and blame reads the same transcripts: measured on
			// the paths agents actually asked about, the top snippet came back
			// as "=== deja blame internal/index/retrieval.go …" — deja's own
			// output, written into the transcript by an agent exercising it.
			text := withoutOwnReport(withoutOwnCallLog(message.Text))
			// A tool's own sentence about the file is not why the file looks
			// the way it does. Measured over the paths agents actually asked
			// about on a real store, 1,567 of the 7,417 quoted snippets were
			// one — "The file … has been updated successfully", a write
			// confirmation, a patch receipt — and `digest.IsAgentArtifact`,
			// which recall has used since #2068, already recognised 1,565 of
			// them. Tool output only: a command record starts with "$ " and is
			// an artifact by that predicate, and what a session ran after
			// touching a file is evidence rather than echo (#3721).
			if message.Role == sources.RoleToolOutput && digest.IsAgentArtifact(text) {
				continue
			}
			// Nor is a data dump: a JSON document or a long file listing names
			// the file as one entry among many, and every entry counted as a
			// mention. On a real store the top row for a busy file was a
			// session with 1113 of them, quoted as raw JSON from deja's own
			// --json output (#4776).
			if message.Role == sources.RoleToolOutput && isDataDump(text) {
				continue
			}
			count, level := mentionScore(text, base, forms)
			if inDir != "" && count > 0 {
				count = pathFormCount(strings.ToLower(filepath.ToSlash(text)), inDir)
			}
			if count == 0 {
				continue
			}
			hit.Count += count
			if level > specificity {
				specificity = level
			}
			mentions = append(mentions, mention{text, count, level, message.Role})
			hit.matched = append(hit.matched, mi)
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
		hit.Specificity = specificity
		hits = append(hits, hit)
	}
	sort.Slice(hits, func(i, j int) bool {
		// Whether the session named a path at all, before anything else: the
		// description this answers to promises the most specific mention
		// first, and recency alone put a bare mention above the sessions that
		// spelled the path out (#2840).
		//
		// Whether, not how deeply — ordering on the depth itself put one
		// mention of an unrelated file above a session that had worked on this
		// one for a thousand lines, which is the opposite of what blame
		// answers.
		if namedAPath(hits[i]) != namedAPath(hits[j]) {
			return namedAPath(hits[i])
		}
		// Among sessions that wrote the path out, newest first, the way a log
		// reads: by score, one that named it a few more times sat above a newer
		// one, and the listing read 06-29, 06-27, 06-28. Bare mentions keep the
		// score, which is what holds an echo of the name below real work.
		if namedAPath(hits[i]) && !hits[i].Session.Updated.Equal(hits[j].Session.Updated) {
			return hits[i].Session.Updated.After(hits[j].Session.Updated)
		}
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Session.ID < hits[j].Session.ID
	})
	// And the rule promote promises, which the score cannot express here: a
	// note mentions the file once where the transcript it distils mentions it
	// many times, so scoring alone put the transcript above its own note on
	// every blame (#2829).
	liftNotesBy(hits, func(h BlameHit) model.Session { return h.Session })
	dropRepeatedSnippets(hits)
	return hits
}

// dropRepeatedSnippets removes a quote the answer has already shown.
//
// A fork, a resumed session and a subagent run each carry the transcript they
// were forked from, so one piece of evidence arrives under several ids and the
// answer says it several times. Read from a real store: `deja blame
// internal/search/search.go` quoted the same two lines under four different
// sessions, all four forks of one, and those eight lines were most of the
// answer.
//
// Ordering is untouched — the sessions still rank as they did, and a session
// left with nothing new to show keeps its row, because "this session worked on
// the file too" is the other half of what blame answers.
func dropRepeatedSnippets(hits []BlameHit) {
	seen := map[string]bool{}
	for i := range hits {
		kept := hits[i].Snippets[:0]
		for _, sn := range hits[i].Snippets {
			key := strings.Join(strings.Fields(sn), " ")
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			kept = append(kept, sn)
		}
		hits[i].Snippets = kept
	}
}

// namedAPath reports whether the session wrote the file as a path rather than
// as a bare name, and said enough for that to be evidence. It is the coarse
// half of specificity — the half that orders the answer; the rest is left to
// the score, where the number of mentions is.
//
// Said something, because naming the path and saying nothing else is not
// working on a file: measured on a real store, a pasted absolute path and a
// `git diff --stat` row each took the top of the answer from the session that
// had debugged it (#2854).
func namedAPath(h BlameHit) bool { return h.Specificity > 1.0 }

// BlameCap is how many hits the default listing shows. The rest are behind
// --all, so a caller that cuts the list here owes the reader the count it cut
// from — blame is an answer to "who touched this file", and ten of forty reads
// as the whole answer unless something says otherwise (#2299).
const BlameCap = 10

// CapBlame trims a ranked list to BlameCap unless the reader asked for all of
// it. Kept apart from Blame so the caller still holds the full length: the cut
// used to happen inside the ranking, where nothing downstream could tell ten
// hits from ten of forty.
func CapBlame(hits []BlameHit, o BlameOptions) []BlameHit {
	if !o.All && len(hits) > BlameCap {
		return hits[:BlameCap]
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
	// A path with nothing said around it is not a session working on the file:
	// a `git diff --stat` row and a pasted absolute path each name it once and
	// took the top of the answer from the session that had debugged it
	// (#2854). What counts is the path plus something about it, on the line it
	// is on — asked of the whole message, a diffstat qualifies on its own
	// summary line ("2 files changed, 6 insertions(+)") and the rule catches
	// only messages shorter than three words.
	explained := aLineSaysMoreThanThePath(low, base)
	for _, form := range forms {
		// The bare basename is one of the forms, and it matches inside a path
		// as well as on its own — so every mention was already "specific" and
		// the rule below could never fire. A name is what the caller asked
		// with; a path is what says which file the session meant (#2840).
		if !strings.Contains(strings.Trim(form, "/"), "/") || !explained {
			continue
		}
		if pathFormCount(low, form) > 0 {
			candidate := 1.0 + float64(len(strings.Split(form, "/")))/4
			if candidate > level {
				level = candidate
			}
		}
	}
	// Only the target's own directories count, which is what the forms above
	// are. Crediting any directory a session wrote instead — measured on a
	// real store — handed `blame Makefile` to three other projects' Makefiles
	// and dropped this repo's own from rank 2 to rank 24, because "wrote a
	// deep path" is a proxy for working in a deep tree rather than for meaning
	// this file. A target with no directory of its own leaves every mention at
	// 1.0, and the rule sits out (#2840).
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

// aLineSaysMoreThanThePath reports whether some line naming the file carries
// words of its own beside the paths on it. Three, so a diffstat row
// (`cmd/deja/mcp.go | 4 +-`) and a bare pasted path do not read as a session
// discussing the file, while a line that says what was done to it does.
//
// Per line rather than per message, because a diffstat's own summary line
// would otherwise vouch for every path above it.
func aLineSaysMoreThanThePath(low, base string) bool {
	for _, line := range strings.Split(low, "\n") {
		if strings.Contains(line, base) && saysMoreThanThePath(line) {
			return true
		}
	}
	return false
}

// saysMoreThanThePath counts the words on a line that are not part of a path.
// Letters by Unicode rather than by ASCII: a session that says what it did in
// Russian or Chinese is saying it (#2854).
func saysMoreThanThePath(low string) bool {
	words, unspaced := 0, 0
	for _, field := range strings.Fields(low) {
		if strings.ContainsAny(field, "/\\") {
			continue
		}
		letters, script := 0, 0
		for _, r := range field {
			if !unicode.IsLetter(r) {
				continue
			}
			letters++
			// Chinese, Japanese and Korean put no spaces between words, so a
			// whole sentence arrives as one field and counting fields counts
			// it as one word. Their letters are counted instead, which is the
			// same question asked in the units that script uses (#2854).
			if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
				script++
			}
		}
		unspaced += script
		if unspaced >= 4 {
			return true
		}
		if letters >= 2 && utf8.RuneCountInString(field) >= 2 {
			words++
			if words >= 3 {
				return true
			}
		}
	}
	return false
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

// ordinaryWordName is a file name that is also a plain word in a sentence:
// no extension, no capital, not a dotfile. `Makefile`, `LICENSE` and
// `.bashrc` are names nobody writes by accident; `load` and `cache` are not.
func ordinaryWordName(name string) bool {
	if name == "" || filepath.Ext(name) != "" {
		return false
	}
	for _, r := range name {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
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

// SessionTitle is what a session was asked to do, for a caller outside this
// package that names a session on screen.
func SessionTitle(s model.Session) string { return sessionTitle(s) }

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

// boundedBlameHit is boundedHit for a blame answer: the messages that mention
// the file, bounded, with the session's real count beside them.
func boundedBlameHit(h BlameHit) BlameHit {
	var total int
	h.Session.Messages, total = boundedSelection(h.Session, h.matched)
	if total > 0 {
		h.MessagesTotal, h.MessagesCapped = total, true
	}
	return h
}

func PrintBlame(w io.Writer, hits []BlameHit, jsonOutput bool) {
	for i := range hits {
		if hits[i].Tier == "" {
			hits[i].Tier = TierExact
		}
	}
	if jsonOutput {
		// The printed rows below are filtered; this one was not, and it is the
		// row a dashboard reads (#3616).
		out := make([]BlameHit, len(hits))
		copy(out, hits)
		for i := range out {
			out[i] = boundedBlameHit(out[i])
			out[i].Session = SafeSession(out[i].Session)
			out[i].Title = SafeText(out[i].Title)
			out[i].Snippets = SafeStrings(out[i].Snippets)
			out[i].LifecycleNote = SafeText(out[i].LifecycleNote)
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	PrintBlameWidth(w, hits, 0)
}

// PrintBlameWidth prints the blame rows in the search result header's form —
// harness, project, day, short id, then the session's question — so the two
// lists read alike. width is the terminal's (0 for a pipe): the question is
// cut to it and the quoted lines under a row wrap at spaces.
func PrintBlameWidth(w io.Writer, hits []BlameHit, width int) {
	for i := range hits {
		if hits[i].Tier == "" {
			hits[i].Tier = TierExact
		}
	}
	color := colorOK(w)
	// One layout for the whole list: when the widest header leaves too little
	// room for the question beside it, every question goes on the line under
	// its header rather than some rows one way and some the other.
	questionUnder := false
	if width > 0 {
		for _, hit := range hits {
			if hit.Title == "" {
				continue
			}
			plain := fmt.Sprintf("[%s] %s · %s · %s", hit.Session.Harness, SafeLine(hit.Session.Project), blameDate(hit), SafeLine(short(hit.Session.ID)))
			if width-termwidth.Columns(plain)-3 < 32 {
				questionUnder = true
				break
			}
		}
	}
	for _, hit := range hits {
		date := blameDate(hit)
		// id, project and title reach a terminal here and the agent through the
		// MCP blame tool. All three are free text from the transcript — an
		// imported peer's title especially — so a bare escape or bidi run would
		// repaint the line or reorder it. SafeLine strips them the way the
		// digest and snippet paths already do.
		id := SafeLine(short(hit.Session.ID))
		project := SafeLine(hit.Session.Project)
		title := SafeLine(hit.Title)
		plain := fmt.Sprintf("[%s] %s · %s · %s", hit.Session.Harness, project, date, id)
		// Too little room beside the header and the question goes on the line
		// under it rather than being cut to three words.
		under := ""
		if title != "" && width > 0 {
			if questionUnder {
				under, title = "  "+fitLine(title, width-2), ""
			} else {
				title = fitLine(title, width-termwidth.Columns(plain)-3)
			}
		}
		if color {
			sep := cDim + "·" + cReset + cBold
			fmt.Fprintf(w, "%s%s %s %s %s %s %s%s", cBold, harnessTag(hit.Session.Harness, true), project, sep, date, sep, id, cReset)
			if title != "" {
				fmt.Fprintf(w, " %s %s", cDim+"—"+cReset, title)
			}
		} else {
			fmt.Fprint(w, plain)
			if title != "" {
				fmt.Fprintf(w, " — %s", title)
			}
		}
		fmt.Fprintln(w)
		if under != "" {
			fmt.Fprintln(w, under)
		}
		if line := BlameLifecycleLine(hit); line != "" {
			line = termwidth.Indent(line, width, "  ", "    ")
			if color {
				line = cDim + line + cReset
			}
			fmt.Fprintln(w, line)
		}
		for _, text := range hit.Snippets {
			text = termwidth.Indent(text, width, "  ", "    ")
			if color {
				text = cDim + text + cReset
			}
			fmt.Fprintln(w, text)
		}
	}
}

func blameDate(hit BlameHit) string {
	if hit.Session.Updated.IsZero() {
		return "-"
	}
	return absoluteDate(hit.Session.Updated)
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
	case "accepted":
		// The one state that is not a warning. blame answers "who decided
		// this", so a decision that still stands is the answer rather than a
		// caveat about it (#2514).
		head = "this is the standing decision"
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

// dumpListingPaths is how many distinct file paths make tool output a listing
// rather than output about the files in it. A failing build or a commit names a
// handful; `git status` on a busy tree, `find` and an index dump name dozens.
const dumpListingPaths = 10

// rowPrefix is how far into a line a JSON record may start and still make the
// line a row of data: room for a timestamp and a separator.
const rowPrefix = 40

// dumpMinBytes is the least tool output that can be a dump.
const dumpMinBytes = 200

// isDataDump reports whether tool output is a document or a listing rather
// than something said about a file: JSON, whole or cut off where the harness
// truncated it, rows of JSON records, or at least dumpListingPaths distinct
// paths on lines that are mostly nothing else. A stack trace names as many files, and alternates them
// with the code that ran, so it stays history.
func isDataDump(text string) bool {
	t := strings.TrimSpace(text)
	// One small object is a record, an API's error body or a config line:
	// it names the file once and counts once. Dumps run to hundreds of bytes.
	if len(t) < dumpMinBytes {
		return false
	}
	// The document can follow a line or two of its own: deja prints "deja:
	// updated 2 files" to stderr ahead of its --json, and the harness keeps
	// both. JSON from there on that is most of the output is the dump.
	//
	// rows and rowBytes count lines that open a JSON record within a short
	// prefix, as `sqlite3` prints "2026-05-24 20:02|{"type": …}" — values long
	// enough that the key count below does not see them as data (#4780).
	rows, rowBytes := 0, 0
	for off := 0; off < len(t); {
		line := t[off:]
		// Or after a one-word label on its line: a web search hands back
		// "Links: [{"title": …".
		// Only the label's width is searched: the whole rest of the text made
		// this quadratic in the number of lines.
		head := line
		if len(head) > 26 {
			head = head[:26]
		}
		if i := strings.Index(head, ": "); i > 0 && i <= 24 && !strings.ContainsAny(line[:i], " \t\n") {
			if startsJSON(line[i+2:]) && (len(line)-i-2)*2 >= len(t) {
				return true
			}
		}
		if startsJSON(line) && len(line)*2 >= len(t) {
			return true
		}
		nl := strings.IndexByte(line, '\n')
		end := nl
		if end < 0 {
			end = len(line)
		}
		if prefix := line[:min(end, rowPrefix)]; strings.Contains(prefix, `{"`) {
			rows++
			rowBytes += end
		}
		if nl < 0 {
			break
		}
		off += nl + 1
	}
	if rows >= 3 && rowBytes*2 >= len(t) {
		return true
	}
	// Or rows that each carry a JSON record, as a database query prints
	// them: a key every 40 bytes is data, where prose quoting a small
	// object has one or two in a paragraph.
	if strings.Count(t, `":`)*40 >= len(t) {
		return true
	}
	seen := map[string]bool{}
	lines, pathLines := 0, 0
	for _, line := range strings.Split(t, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines++
		found, diagnostic := false, false
		for _, tok := range strings.FieldsFunc(line, dumpFieldSep) {
			if p, at := pathToken(tok); p != "" {
				seen[p] = true
				found = true
				diagnostic = diagnostic || at
			}
		}
		// file.go:12:3 is a compiler, a linter or a test runner saying
		// something about that line — a report, not a listing, however many
		// files it names.
		if diagnostic {
			return false
		}
		if found {
			pathLines++
		}
	}
	return len(seen) >= dumpListingPaths && pathLines*10 >= lines*6
}

// startsJSON reports whether s opens a JSON object or array of values.
func startsJSON(s string) bool {
	s = strings.TrimLeft(s, " \t\r")
	if len(s) < 2 || (s[0] != '{' && s[0] != '[') {
		return false
	}
	rest := strings.TrimLeft(s[1:], " \t\r\n")
	return rest != "" && strings.ContainsRune(`"{[`, rune(rest[0]))
}

func dumpFieldSep(r rune) bool {
	return strings.ContainsRune(" \t\r\"',()[]{}", r)
}

// pathToken is tok as a file path — something/name.ext — or "", and whether
// a :line followed it.
func pathToken(tok string) (string, bool) {
	at := false
	if i := strings.IndexByte(tok, ':'); i > 0 {
		if !strings.ContainsAny(tok[:i], `/\`) {
			return "", false // a URL scheme or a key, not a path
		}
		at = i+1 < len(tok) && tok[i+1] >= '0' && tok[i+1] <= '9'
		tok = tok[:i]
	}
	slash := strings.LastIndexAny(tok, `/\`)
	if slash < 0 {
		return "", false
	}
	// A file extension is short and lower case; a Go trace's
	// pkg/path.Func is not one.
	name := tok[slash+1:]
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 {
		return "", false
	}
	ext := name[dot+1:]
	if ext == "" || len(ext) > 6 || ext[0] < 'a' || ext[0] > 'z' {
		return "", false
	}
	for _, r := range ext {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return "", false
		}
	}
	return tok, at
}
