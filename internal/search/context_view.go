package search

import (
	"fmt"
	"io"
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/harnesscolor"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// repeatedStamps names the minutes this session renders more than once from
// different instants — what the fall-back hour does twice a year. Nothing else
// is affected: an ordinary transcript keeps its narrower column.
func repeatedStamps(ms []model.Message) map[string]bool {
	seen := map[string]time.Time{}
	repeated := map[string]bool{}
	for _, m := range ms {
		if m.Time.IsZero() {
			continue
		}
		stamp := m.Time.Format("2006-01-02 15:04")
		if first, ok := seen[stamp]; ok {
			if !first.Equal(m.Time) {
				repeated[stamp] = true
			}
			continue
		}
		seen[stamp] = m.Time
	}
	return repeated
}

// PrintSessionStyled writes a whole session for a terminal: the harness in its colour,
// timestamps dimmed and roles bold so a long session scans by turn, and every
// message wrapped to width at spaces. color false and width 0 is the plain
// text a pipe gets.
func PrintSessionStyled(w io.Writer, s model.Session, color bool, width int) {
	// Project and id are transcript text a harness wrote, and this is one
	// line: an escape byte in either recolours the transcript that follows, a
	// carriage return rewinds the header, and a newline splits it into two
	// lines of what reads as deja's own output. PrintContext below has said
	// this since #1090; this header was missed by it.
	harness := s.Harness
	if color {
		harness = harnesscolor.Paint(s.Harness, s.Harness, true) + cBold
		fmt.Fprintf(w, "%s# %s · %s · %s%s\n", cBold, harness, SafeLine(s.Project), SafeLine(short(s.ID)), cReset)
	} else {
		fmt.Fprintf(w, "# %s · %s · %s\n", harness, SafeLine(s.Project), SafeLine(short(s.ID)))
	}
	repeated := repeatedStamps(s.Messages)
	now := time.Now()
	for _, m := range s.Messages {
		txt := redact.SafeForDisplay(collapseTool(wroteForDisplay(m)))
		if strings.TrimSpace(txt) == "" {
			continue
		}
		t := ""
		if !m.Time.IsZero() {
			// The day in the form every other screen prints it ("Mar 27",
			// "Nov 29 2025"), in the timestamp's own zone as before.
			day := m.Time.Format("Jan 2 2006")
			if m.Time.Year() == now.Year() {
				day = m.Time.Format("Jan 2")
			}
			stamp := day + " " + m.Time.Format("15:04")
			if repeated[m.Time.Format("2006-01-02 15:04")] {
				// The clocks went back and this minute happened twice. Both
				// stamps are right, which is why an hour of conversation reads
				// as a duplicated message without the offset (#1788).
				stamp += m.Time.Format(" -07:00")
			}
			if color {
				stamp = cDim + stamp + cReset
			}
			t = stamp + " "
		}
		role := m.Role
		if color {
			role = cBold + role + cReset
		}
		fmt.Fprintf(w, "\n%s%s:\n%s\n", t, role, termwidth.WrapText(SafeText(txt), width))
	}
}

// wroteForDisplay is a message's text as show prints it. A "wrote" record is a
// path and the hashes of the lines written there (sources.RoleWrote), and show
// printed the hashes bare, which read as an unlabelled commit sha.
func wroteForDisplay(m model.Message) string {
	if m.Role != "wrote" {
		return m.Text
	}
	path, hashes, ok := strings.Cut(m.Text, "\n")
	if !ok {
		return m.Text
	}
	n := len(strings.Fields(hashes))
	lines := "lines"
	if n == 1 {
		lines = "line"
	}
	return fmt.Sprintf("%s\n(%d written %s, kept as hashes for `deja blame`)", path, n, lines)
}

// roleMatches accepts the role names the help text documents. `--role tool`
// is what `deja help` promises and "tool-output" is what is stored, so the
// documented spelling matched nothing — silently, with a healthy exit.
func roleMatches(stored, want string) bool {
	return model.RoleMatches(stored, want)
}

// RoleMatches is roleMatches for the callers that filter records themselves.
// `deja stats` is one: it retrieves everything and narrows locally, and its own
// comparison was a bare string equality, so `--role tool` reported an empty
// store while `--role tool-output` counted six records in it.
func RoleMatches(stored, want string) bool { return model.RoleMatches(stored, want) }

// IsWorkRecord reports whether a message records what an agent did rather than
// what was said. Tool records are bulk-repetitive by nature and are excluded
// from conversational context windows unless explicitly requested.
func IsWorkRecord(role string) bool {
	return model.IsWorkRole(role)
}

func isWorkRecord(role string) bool { return IsWorkRecord(role) }

// ContextBudget is how much of a session PrintContext prints. The whole
// transcript would be tens of times an answer's size, and it is one
// recall_context away when an agent genuinely needs it.
const ContextBudget = 8000

// withoutHarnessEnvelopes takes the host's own blocks off the user turns
// before the window is chosen: teammate messages, task notifications, system
// reminders, deja's own recall coming back. Every user turn was kept whatever
// it held, so a session that carries a few of these spent the budget on them —
// ten real questions on this machine's index came back 38% envelope, three of
// them 89%, one of them twelve idle notifications (#3323). A turn that was
// nothing but the envelope is not context and goes; a turn with the person's
// words beside it keeps the words. Only a closed block goes: an unclosed tag
// is as often a person quoting one as a host truncating one, and this surface
// prints the turn back.
func withoutHarnessEnvelopes(s model.Session) model.Session {
	msgs := make([]model.Message, 0, len(s.Messages))
	for _, m := range s.Messages {
		if m.Role == "user" && strings.Contains(m.Text, "<") {
			left := digest.StripClosedHarnessBlocks(m.Text)
			if left == "" {
				continue
			}
			m.Text = left
		}
		msgs = append(msgs, m)
	}
	s.Messages = msgs
	return s
}

// PrintContext writes the plain-text briefing for one session: a header line,
// then the turns around what query matched.
func PrintContext(w io.Writer, s model.Session, query string) {
	// Project and id are transcript text a harness wrote, and this is one line:
	// an escape byte in either recolours the header and a carriage return
	// rewinds it. The body below is already SafeText'd; the header was not, so
	// `deja show`, `deja ctx` and the MCP context tools printed it raw (#1090).
	fmt.Fprintf(w, "# deja context: %s · %s · %s", s.Harness, SafeLine(s.Project), SafeLine(s.ID))
	if !s.Updated.IsZero() {
		// The reader's zone, as `deja last`, resources/list and the MCP recall
		// listing already do. This header formatted the timestamp as it came —
		// UTC for every source but aider — so a reader far enough from it saw
		// ctx name one day and those surfaces name the next for one session.
		// ctx is the briefing written to be handed to another agent, which
		// makes its header a date that gets repeated back (#856).
		//
		// Not everything is local yet: `deja show` prints per-message stamps in
		// the timestamp's own zone, and the superseded marker is minted in UTC
		// because lifecycle.go compares against it. Those want their own change.
		fmt.Fprintf(w, " · updated %s", s.Updated.Local().Format("2006-01-02"))
	}
	fmt.Fprintln(w)
	for _, r := range s.UserRules {
		fmt.Fprintf(w, "%s%s\n", UserRuleLabel, SafeLine(r))
	}
	if evContextBody(w, s) {
		return
	}
	printContextBody(w, s, query)
}

// PrintContextStyled is PrintContext for a terminal: the header in the form
// the other screens print (harness in its colour, the short id, "Nov 29 2025"),
// headings bold, and the turns wrapped to width at spaces. A pipe keeps
// PrintContext's text, which is also what the MCP context tools hand an agent.
func PrintContextStyled(w io.Writer, s model.Session, query string, color bool, width int) {
	if !color && width <= 0 {
		PrintContext(w, s, query)
		return
	}
	head := "# deja context: " + harnesscolor.Paint(s.Harness, s.Harness, color)
	if color {
		head += cBold
	}
	head += " · " + SafeLine(s.Project) + " · " + SafeLine(short(s.ID))
	if !s.Updated.IsZero() {
		head += " · updated " + absoluteDate(s.Updated)
	}
	if color {
		head = cBold + head + cReset
	}
	fmt.Fprintln(w, head)
	var body strings.Builder
	printContextBody(&body, s, query)
	text := termwidth.WrapText(body.String(), width)
	if color {
		lines := strings.Split(text, "\n")
		for i, l := range lines {
			if strings.HasPrefix(l, "## ") {
				lines[i] = cBold + l + cReset
			}
		}
		text = strings.Join(lines, "\n")
	}
	fmt.Fprint(w, text)
}

func printContextBody(w io.Writer, s model.Session, query string) {
	s = withoutHarnessEnvelopes(s)
	qlow := lower(query)
	terms, phrases := QueryParts(query)
	budget := ContextBudget
	// The reply to an included turn comes with it. Every user turn was kept
	// and every assistant turn had to match, but the decision lives in the
	// answer and is worded nothing like the question: `ctx "http client"`
	// handed an agent "the http client hammered the server on failure" and
	// dropped "we decided to cap retries at 3" from the turn below it. That
	// is the problem statement without its resolution (#R8).
	parts := contextQueryParts(terms, phrases, qlow)
	prevKept := false
	written := printContextChunks(w, s, budget, func(m model.Message) (bool, []int) {
		// How much of each part of the query the turn carries, counted
		// separately. countIn scores a turn only when it carries the whole
		// query, so on a two-word question the turns holding the identifying
		// word alone weighed nothing, no turn qualified, and the window fell
		// back to the session's opening — 8 KB about something else (#2726).
		hits := contextPartHits(m.Text, parts)
		carries := false
		for _, h := range hits {
			if h > 0 {
				carries = true
				break
			}
		}
		keep := carries || m.Role == "user" || (m.Role == "assistant" && prevKept)
		prevKept = keep
		return keep, hits
	}, func(m model.Message) string { return clipCompactionSummary(m.Text, parts) })
	if written > 0 {
		return
	}
	// The session can match with query terms spread across messages, so no
	// single message qualifies above; show an overview instead of a bare header.
	if qlow != "" {
		fmt.Fprintf(w, "\nNo single message contains the full query; showing the session's opening exchange.\n")
	}
	printContextChunks(w, s, budget, func(model.Message) (bool, []int) { return true, nil },
		func(m model.Message) string { return clipCompactionSummary(m.Text, parts) })
}

// compactionSummaryLineCap and compactionSummaryByteCap bound what a compaction
// summary contributes to a context window.
const (
	compactionSummaryLineCap = 8
	compactionSummaryByteCap = 700
)

// compactionSummaryLead says what was cut and why, so the lines below are not
// read as the whole turn.
const compactionSummaryLead = "[compaction summary of an earlier part of this session — the matching lines only]"

// clipCompactionSummary reduces a compaction summary to the lines that carry
// the query, or returns "" for a turn that is not one.
//
// A summary is the longest message a session ever holds and it names everything
// the session did, so it matches almost any question and then fills the window
// on its own. Measured over eight questions an agent would ask, on a real
// store: three answers came back carrying one, and it was 95% of each of them —
// 44% of every byte `recall_context` served. None of it answered the question.
func clipCompactionSummary(text string, parts []string) string {
	if !digest.IsCompactionSummary(text) {
		return ""
	}
	// Nothing was asked, so nothing can be matched: `deja ctx <id>` and the MCP
	// resource read a session with no query, and there the summary is the best
	// account of it there is.
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(compactionSummaryLead)
	lines := 0
	for raw := range strings.SplitSeq(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		hits := contextPartHits(line, parts)
		if hits == nil {
			continue
		}
		carries := false
		for _, h := range hits {
			if h > 0 {
				carries = true
				break
			}
		}
		if !carries {
			continue
		}
		b.WriteString("\n")
		b.WriteString(line)
		lines++
		if lines >= compactionSummaryLineCap || b.Len() >= compactionSummaryByteCap {
			break
		}
	}
	if lines == 0 {
		// It matched as a whole and no single line does — a query whose words
		// are spread through it. Name the subject and stop: the summary is
		// there, and it is not what was asked about.
		return compactionSummaryLead + "\n" + firstLines(text, 2)
	}
	return b.String()
}

// firstLines is the first n non-empty lines, for naming what a clipped turn was
// about.
func firstLines(text string, n int) string {
	var out []string
	for raw := range strings.SplitSeq(text, "\n") {
		if line := strings.TrimSpace(raw); line != "" {
			out = append(out, line)
			if len(out) == n {
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

// contextQueryParts is what the digest weighs a turn against: the query's terms
// and phrases, or the raw query when tokenising left nothing — a reader who
// typed punctuation still gets scored against what they typed.
func contextQueryParts(terms, phrases []string, qlow string) []string {
	parts := make([]string, 0, len(terms)+len(phrases)+1)
	parts = append(parts, terms...)
	parts = append(parts, phrases...)
	if len(parts) == 0 && qlow != "" {
		parts = append(parts, qlow)
	}
	return parts
}

// contextPartHits counts each part of the query in one turn.
func contextPartHits(text string, parts []string) []int {
	if len(parts) == 0 {
		return nil
	}
	low := lower(text)
	hits := make([]int, len(parts))
	for i, p := range parts {
		hits[i] = strings.Count(low, p)
	}
	return hits
}

// contextTurn is a turn the digest keeps, before it is rendered.
type contextTurn struct {
	role string
	raw  string
	// hits is how often the turn says each part of the query. Kept per part
	// rather than summed, because how much a part is worth is not known until
	// every kept turn has been seen: see contextTurnWeights.
	hits []int
}

// carriesQuery reports whether the turn says any part of the query at all.
func (t contextTurn) carriesQuery() bool {
	for _, h := range t.hits {
		if h > 0 {
			return true
		}
	}
	return false
}

// contextTurnWeights scores each kept turn by the rarity of the query parts it
// carries, so the budget window lands where the identifying word is.
//
// A word the session repeats on every turn says nothing about which passage was
// wanted; the word that picked this session out of the index says everything.
// Rarity is measured across the kept turns themselves — no index lookup, and it
// is the same reasoning the ranking uses for identifying words (#2480).
// Repetition inside one turn is damped: a turn returning to the subject should
// outrank one that mentions it once, but not forty turns of scaffolding.
func contextTurnWeights(turns []contextTurn, parts int) []float64 {
	weights := make([]float64, len(turns))
	if parts == 0 {
		return weights
	}
	df := make([]int, parts)
	for _, t := range turns {
		for i, h := range t.hits {
			if i < parts && h > 0 {
				df[i]++
			}
		}
	}
	idf := make([]float64, parts)
	for i, n := range df {
		if n > 0 {
			idf[i] = math.Log(1 + float64(len(turns))/float64(n))
		}
	}
	// How much of the query the turn covers multiplies what it is worth. The
	// turn that says every word of the question is the one the reader meant,
	// and demanding all of them was the whole strength of the old rule; keeping
	// it as a multiplier holds that strength while still letting a turn with
	// one identifying word anchor the window when no turn carries them all.
	for j, t := range turns {
		var sum float64
		covered := 0
		for i, h := range t.hits {
			if i < parts && h > 0 {
				sum += idf[i] * math.Log1p(float64(h))
				covered++
			}
		}
		weights[j] = sum * float64(covered)
	}
	return weights
}

// renderContextTurnsWorkers is how many turns render at once. One worker per
// core; a session small enough that the goroutines cost more than the work
// renders in place.
var renderContextTurnsWorkers = runtime.NumCPU

// renderContextTurns renders each kept turn, in parallel when there are enough
// of them to pay for it. Results are written by index, so the digest is the
// same bytes in the same order however many cores run it.
func renderContextTurns(turns []contextTurn) []string {
	out := make([]string, len(turns))
	workers := min(renderContextTurnsWorkers(), len(turns))
	if workers < 2 || len(turns) < 32 {
		for i, t := range turns {
			out[i] = SafeText(contextText(t.raw, t.carriesQuery()))
		}
		return out
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(turns) {
					return
				}
				out[i] = SafeText(contextText(turns[i].raw, turns[i].carriesQuery()))
			}
		}()
	}
	wg.Wait()
	return out
}

func printContextChunks(w io.Writer, s model.Session, budget int, include func(m model.Message) (ok bool, hits []int), clip func(m model.Message) string) int {
	// A digest is what someone pipes into a prompt, so it carries the
	// conversation. The work records — tool output, the files a turn touched, the
	// commands it ran, the spans it replaced — are indexed and searchable by role
	// and are not what anyone means by context; before they were labelled
	// honestly (#560) they arrived as `user` and filled this with `## tool-output`
	// blocks. Collect the kept turns in one ordered pass so the include callback's
	// prevKept bookkeeping still runs left to right.
	type chunk struct {
		text string
		// weight is how much of the query this turn carries, by rarity, so the
		// window can sit where the identifying words are rather than where the
		// common ones repeat.
		weight float64
		// msg is the turn's place in the session, so the line after a cut
		// window can say whether the last turn was shown.
		msg int
	}
	// The include callback carries state from turn to turn (prevKept), so which
	// turns are kept is decided in one ordered pass. Rendering them is not:
	// redaction and prose collapsing are the bulk of the command's time — 3.5 s
	// of a 4.5 s run on a 30001-message session — and every turn is rendered
	// before the 8 KB window can say which ones get printed (#1790). Rendering
	// only the window was tried and reverted (#1742): collapsed sizes differ
	// from predicted ones and the window moves. Spreading the same renders
	// across cores keeps the output identical by construction.
	var kept []contextTurn
	var keptAt []int
	parts := 0
	// Turns the include rule passed over, and the session's last turn: both
	// are what the line under a cut window reports.
	skipped, last := 0, -1
	for i, m := range s.Messages {
		if isWorkRecord(m.Role) {
			continue
		}
		if strings.TrimSpace(m.Text) != "" {
			last = i
		}
		ok, hits := include(m)
		if !ok {
			if strings.TrimSpace(m.Text) != "" {
				skipped++
			}
			continue
		}
		if len(hits) > parts {
			parts = len(hits)
		}
		raw := m.Text
		if clip != nil {
			if short := clip(m); short != "" {
				raw = short
			}
		}
		kept = append(kept, contextTurn{role: m.Role, raw: raw, hits: hits})
		keptAt = append(keptAt, i)
	}
	weights := contextTurnWeights(kept, parts)
	rendered := renderContextTurns(kept)
	var chunks []chunk
	for i, k := range kept {
		text := rendered[i]
		if strings.TrimSpace(text) == "" {
			continue
		}
		chunks = append(chunks, chunk{fmt.Sprintf("\n## %s\n\n%s\n", k.role, text), weights[i], keptAt[i]})
	}
	if len(chunks) == 0 {
		return 0
	}
	// A window that holds less than the session says so, and the room for
	// saying it comes out of the budget before the window is chosen: 8 KB that
	// ended mid-word with nothing after it read as the whole session (#5392).
	total := 0
	for _, c := range chunks {
		total += len(c.text)
	}
	if skipped > 0 || total > budget {
		budget = max(0, budget-contextCutReserve)
	}
	// Anchor the budget window where the matched turns are densest, with one turn
	// of lead-in (usually the question when the answer is what matched). Walking
	// from the top let earlier scaffolding crowd the match out of budget: a query
	// whose answer sat deep in a long session got 8KB of unrelated opening and
	// none of the turns it found (#R8-budget). Anchoring on the first match alone
	// left the other half of that: a word mentioned once in passing at the top
	// pinned the window there, and the exchange that settled the question — every
	// later mention of it — fell outside the budget again (#1322). No match means
	// the fallback overview, which wants the session's start, so it stays at 0.
	start, span := 0, 0
	best := 0.0
	sum, bytes, left := 0.0, 0, 0
	for right, c := range chunks {
		sum += c.weight
		bytes += len(c.text)
		for left < right && bytes > budget {
			sum -= chunks[left].weight
			bytes -= len(chunks[left].text)
			left++
		}
		if sum > best {
			best, start, span = sum, left, bytes
		}
	}
	// The lead-in is only worth having if it does not push a matched turn back
	// out of the budget it was just chosen for.
	if best > 0 && start > 0 && span+len(chunks[start-1].text) <= budget {
		start--
	}
	written, end, cutLeft := 0, start, 0
	for i, c := range chunks[start:] {
		if written >= budget {
			break
		}
		text := c.text
		if written+len(text) > budget {
			cut := cutAtWord(text, budget-written)
			// A heading and a few words is not a turn shown; it is left out
			// and counted with the others.
			if cut < strings.Index(text, "\n\n")+2+contextCutMinBody {
				break
			}
			cutLeft = len(text) - cut
			text = strings.TrimRight(text[:cut], " \t\n")
		}
		fmt.Fprint(w, text)
		written += len(text)
		end = start + i + 1
	}
	if written == 0 {
		return 0
	}
	id := SafeLine(s.ID)
	if cutLeft > 0 {
		note := fmt.Sprintf(" …\n[this turn is cut here, %s more of it left out — `deja show %s` prints the rest]\n", approxSize(cutLeft), id)
		fmt.Fprint(w, note)
		written += len(note)
	}
	shown := end - start
	if left := start + len(chunks) - end + skipped; left > 0 || cutLeft > 0 {
		lastShown := chunks[end-1].msg == last
		final := ""
		switch {
		case !lastShown:
			final = ", the last turn not among them"
		case cutLeft > 0:
			final = ", the last one cut short"
		}
		note := fmt.Sprintf("\n[%d of %d turns shown%s — `deja show %s` prints every turn]\n", shown, shown+left, final, id)
		fmt.Fprint(w, note)
		written += len(note)
	}
	return written
}

// contextCutReserve is the room kept for the two lines a cut window prints,
// and contextCutMinBody the least of a turn worth printing before its marker.
const (
	contextCutReserve = 320
	contextCutMinBody = 160
)

// cutAtWord is where to cut text to fit n bytes: the last space within reach,
// or the last whole rune when a word runs longer than that.
func cutAtWord(text string, n int) int {
	if n >= len(text) {
		return len(text)
	}
	cut := max(0, n)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	if i := strings.LastIndexAny(text[:cut], " \t\n"); i > 0 && i >= cut-200 {
		return i
	}
	return cut
}

// approxSize is a byte count the way a reader sizes text.
func approxSize(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%d KB", (n+512)/1024)
}
