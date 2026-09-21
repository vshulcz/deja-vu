package index

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/redact"
)

// What a week of work actually was, read back out of the sessions (#544).
//
// People reconstruct this from memory several times a week — a standup, a PR
// description, release notes — while the record sits unread. `git log` answers
// what landed; it does not answer what was measured, what was tried and
// dropped, or what was decided in a repository nothing was committed to.
//
// The extraction is the one `deja ctx` and the session-start hook already use:
// digest.Conclusions, the decision-shaped sentences of a session. Prototyped
// over a real seven days it produced a draft that read like the week — and it
// is worth knowing why, because the same detector failed outright at #526,
// where it had to say why *one file* was the way it is. There the file linkage
// was wrong more often than right. Here nothing has to be linked: the question
// is what happened, so the broken part is simply absent.
//
// Three things the prototype got wrong and this fixes, all of them trimming:
// a sentence cut at a markdown heading, the same conclusion repeated from
// several messages of one session, and no length discipline at all — 60
// character fragments next to 250 character paragraphs.
//
// And one requirement nobody had scoped. This output leaves the machine: it
// goes into a PR body, a Slack message, release notes. The index redacts what
// looks like a credential, which is the right bar for a local store and the
// wrong one here — the prototype surfaced a real infrastructure IP address,
// which no rule calls a secret. So every line goes through redact.Outbound
// before it is printed, and the count of what that masked is part of the
// answer rather than a silent step.

// recapLineMin and recapLineMax are the length discipline. Below the floor a
// line is a fragment that reads as noise next to a real one; above the ceiling
// it is a paragraph nobody pastes into a standup.
const (
	recapLineMin = 40
	recapLineMax = 220
	// recapSessionCap bounds the manifest walk. A week of heavy use is tens of
	// sessions; the cap is there so `--since 365d` on a large store stays a
	// command someone can type.
	recapSessionCap = 400
)

// RecapSession is one session's lines, with where they came from.
type RecapSession struct {
	Harness string    `json:"harness"`
	ID      string    `json:"id"`
	Project string    `json:"project,omitempty"`
	Title   string    `json:"title,omitempty"`
	When    time.Time `json:"when"`
	Lines   []string  `json:"lines"`
}

// Recap is a window of work, grouped by project, newest session first.
type Recap struct {
	// Considered is how many sessions were in the window, Spoke how many of
	// them concluded anything. The gap is the honest part of the answer: a week
	// of twenty sessions where four said something is not a week of four.
	Considered int
	Spoke      int
	Sessions   []RecapSession
	Projects   []string
	// Masked is what the outbound pass removed, by kind.
	Masked   redact.Counts
	Withheld int
}

// ScanRecap reads the sessions touched inside the window and returns what they
// concluded, trimmed, deduplicated and masked for outbound use.
func ScanRecap(dir string, since time.Duration, perSession int) (Recap, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	if perSession <= 0 {
		perSession = 3
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return Recap{}, err
	}
	pol := policy.Load()
	cut := time.Now().Add(-since)
	out := Recap{Masked: redact.Counts{}}
	var metas []SessionMeta
	for _, meta := range m.Sessions {
		if meta.Updated.Before(cut) {
			continue
		}
		if pol.Ignored(meta.Path, meta.Project) {
			out.Withheld++
			continue
		}
		metas = append(metas, meta)
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Updated.After(metas[j].Updated) })
	out.Considered = len(metas)
	if len(metas) > recapSessionCap {
		metas = metas[:recapSessionCap]
	}
	ss, err := sessionsForMetas(dir, metas)
	if err != nil {
		return Recap{}, err
	}
	seenProject := map[string]bool{}
	for _, s := range ss {
		lines := recapLines(s, perSession, out.Masked)
		if len(lines) == 0 {
			continue
		}
		out.Spoke++
		out.Sessions = append(out.Sessions, RecapSession{
			Harness: s.Harness, ID: s.ID, Project: s.Project,
			Title: s.Title, When: s.Updated, Lines: lines,
		})
		if !seenProject[s.Project] {
			seenProject[s.Project] = true
			out.Projects = append(out.Projects, s.Project)
		}
	}
	sort.SliceStable(out.Sessions, func(i, j int) bool {
		return out.Sessions[i].When.After(out.Sessions[j].When)
	})
	return out, nil
}

// recapLines is the pipeline for one session: extract, trim, dedupe, mask.
func recapLines(s model.Session, max int, masked redact.Counts) []string {
	// Asked for more than the cap, because trimming drops some of what comes
	// back and a session that concluded three things should still show three.
	raw := digest.Conclusions(s, recapLineMax*max*4, max*4)
	var out []string
	var kept []map[string]bool
	for _, line := range raw {
		line = trimRecapLine(line)
		if line == "" {
			continue
		}
		words := recapWords(line)
		if sameThoughtTwice(kept, words) {
			continue
		}
		kept = append(kept, words)
		line, c := redact.Outbound(line)
		for kind, n := range c {
			masked.Add(kind, n)
		}
		out = append(out, line)
		if len(out) >= max {
			break
		}
	}
	return out
}

// processNoise are the lines an agent writes about its own turn rather than
// about the work: a review handed back, a poller that found nothing, a report
// sent to a caller. Every entry here is a line the first run over a real week
// actually printed, and all of them read as a model talking about itself —
// which is the one thing this command cannot ship (#544).
var processNoise = []string{
	"subagenthandback",
	"sent to caller",
	"report sent",
	"review complete",
	"my review is",
	"based on my review",
	"based on my thorough review",
	"nothing to act on",
	"nothing new to report",
	"stale poller",
	"i'll continue",
	"let me know",
	"no failing jobs",
	"status so far",
	"all main-only workflows verified",
	"report delivered",
	"here is the",
	// deja's own recall, quoted back by the agent that received it. The
	// ingest filter drops the injected block itself; this is the sentence an
	// agent writes about it, and it is deja telling itself what it said.
	"déjà vu:",
	"(deja:ses_",
}

// instructionOpeners are the imperative sentences that come from a prompt, a
// skill file or an injected block rather than from the work: a real week does
// not conclude "Verify important conclusions by reading the actual source
// files", which is the line a live store printed out of an agent's own
// instructions.
var instructionOpeners = []string{
	"verify ", "read ", "use ", "make sure", "note that", "remember to",
	"do not ", "don't ", "always ", "never ", "you must", "you should",
}

// recapLayoutRunes are the characters a line can only start with because of
// where it was cut out of: list markers, heading marks, and the punctuation
// that joined it to the clause above.
const recapLayoutRunes = "-*•#:,;) \t"

// isProcessNoise reports whether a line is the agent narrating its own turn or
// repeating an instruction it was given.
func isProcessNoise(s string) bool {
	low := strings.ToLower(s)
	for _, p := range processNoise {
		if strings.Contains(low, p) {
			return true
		}
	}
	for _, p := range instructionOpeners {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return false
}

// trimRecapLine applies the length discipline and cuts a sentence where a
// markdown heading started inside it. The prototype printed
// "**Причина двойная:** ## A." — one sentence, two documents.
func trimRecapLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "##"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	// A fence means the sentence swallowed a command and its output: the first
	// run printed "``` $ registry/cursor.html sitemap=2026-07-17 …" as a
	// conclusion about the week.
	if strings.Contains(s, "```") {
		return ""
	}
	// A bullet, a numbered-list marker or the punctuation left by a cut is
	// layout from the message the line was lifted out of. A real store printed
	// ": `sources` links to `#proof`…", a review finding whose subject was in
	// the clause above it.
	s = strings.TrimLeft(s, recapLayoutRunes)
	s = strings.TrimSpace(s)
	// An odd number of bold markers means the sentence began inside one and the
	// opening half is in the message above: "Кто ставит звёзды.** Радар теперь
	// берёт…" was printed that way.
	if n := strings.Count(s, "**"); n%2 == 1 {
		if i := strings.Index(s, "**"); i >= 0 {
			s = strings.TrimSpace(s[i+2:])
			// Trimmed again: cutting the dangling half exposes the punctuation
			// that joined the two clauses, which is how ": `sources` links to
			// `#proof`…" reached the screen.
			s = strings.TrimSpace(strings.TrimLeft(s, recapLayoutRunes))
		}
	}
	if isProcessNoise(s) {
		return ""
	}
	if n := utf8.RuneCountInString(s); n < recapLineMin {
		return ""
	} else if n > recapLineMax {
		s = cutAtSentence(s, recapLineMax)
	}
	return strings.TrimSpace(s)
}

// cutAtSentence cuts to at most n runes, at the last sentence end if there is
// one in the second half of what survives — otherwise at the last word, with an
// ellipsis, because a line that stops mid-word reads as a bug.
func cutAtSentence(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	head := string(runes[:n])
	if i := strings.LastIndexAny(head, ".!?"); i > len(head)/2 {
		return head[:i+1]
	}
	if i := strings.LastIndexByte(head, ' '); i > 0 {
		return head[:i] + "…"
	}
	return head + "…"
}

// recapWords is a line reduced to the set of words that carry it, which is
// what two spellings of one conclusion have in common.
func recapWords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		// Short words are grammar in every language this sees and they make
		// two unrelated sentences look alike.
		if utf8.RuneCountInString(w) < 4 {
			continue
		}
		out[w] = true
	}
	return out
}

// recapOverlap is how alike two lines have to be to count as the same thought.
// Measured against a real week: the pair that had to collapse shared 0.68 of
// its words ("Аномалию починил: дело было в X" and "Причина найдена и она моя:
// LoRA X …"), and the closest pair that had to survive shared 0.31.
const recapOverlap = 0.5

// sameThoughtTwice reports whether a line restates one already kept. A session
// concludes the same thing in three messages — the second adds a clause to the
// first — and a prefix comparison caught none of those, because the restatement
// begins differently every time.
func sameThoughtTwice(kept []map[string]bool, words map[string]bool) bool {
	if len(words) == 0 {
		return true
	}
	for _, prev := range kept {
		shared := 0
		for w := range words {
			if prev[w] {
				shared++
			}
		}
		smaller := len(words)
		if len(prev) < smaller {
			smaller = len(prev)
		}
		if smaller > 0 && float64(shared)/float64(smaller) >= recapOverlap {
			return true
		}
	}
	return false
}

// homeProjectRE is a project name that is a home directory rather than a
// project: deja names a project after the directory work happened in, and work
// done in the home directory itself came out as "Users/<name>" — a real
// account name, in the heading of text written to be pasted somewhere public.
var homeProjectRE = regexp.MustCompile(`(?i)^(Users|home)/[^/]+$`)

// RecapProjectIsHome reports whether a project name is a home directory rather
// than a project. Two spellings reach the manifest: the path shape, and the
// bare base name of the home directory, which is an account name on its own.
func RecapProjectIsHome(p string) bool {
	if p == "" {
		return false
	}
	if homeProjectRE.MatchString(p) {
		return true
	}
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return false
	}
	return strings.EqualFold(p, filepath.Base(h))
}
