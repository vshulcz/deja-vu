package digest

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/sources"
)

const (
	defaultContextRecords = 64
	defaultContextText    = 32 << 10
	defaultContextItems   = 8
	maxContextBytes       = 24 << 10
	defaultRenderBudget   = 6 << 10
	contextFactBytes      = 512
	contextObjectiveBytes = 1024
	// The hook's targeted transcript reader is capped at 4 MiB. Keep the same
	// ceiling here so a direct caller cannot turn "find the last user request"
	// into an unbounded walk through an enormous session.
	maxContextScanBytes = 4 << 20
)

// ExtractOptions bounds the normalized transcript window examined at a
// compaction. Zero values select the conservative defaults; callers may lower
// them but cannot make an unbounded extractor.
type ExtractOptions struct {
	MaxRecords   int
	MaxTextBytes int
	MaxItems     int
}

func (o ExtractOptions) normalized() ExtractOptions {
	if o.MaxRecords <= 0 || o.MaxRecords > defaultContextRecords {
		o.MaxRecords = defaultContextRecords
	}
	if o.MaxTextBytes <= 0 || o.MaxTextBytes > defaultContextText {
		o.MaxTextBytes = defaultContextText
	}
	if o.MaxItems <= 0 || o.MaxItems > defaultContextItems {
		o.MaxItems = defaultContextItems
	}
	return o
}

// ExtractCompactionContext derives bounded, structured state from a session's
// normalized transcript records. It never calls a model, reads a transcript,
// or infers a passed test/open task from prose. The caller supplies repository
// freshness separately because inspecting a repository is a hook concern, not
// a property of the transcript.
func ExtractCompactionContext(s model.Session, opts ExtractOptions) model.CompactionContext {
	opts = opts.normalized()
	window, clipped := contextWindow(s.Messages, opts)
	c := model.CompactionContext{Truncated: clipped}
	c.Objective = contextObjective(s, window)

	for i := len(window) - 1; i >= 0; i-- {
		m := window[i]
		ref := contextRef(s, m)
		switch m.Role {
		case "assistant":
			if IsAgentArtifact(m.Text) {
				continue
			}
			// The open items are taken out of the text the conclusion is built
			// from: one turn often states both — "Fixed and the suite is green.
			// Осталось: сбросить счётчик …" — and the packet printed that
			// sentence twice, once as a gap and once inside the conclusion, out
			// of a budget that drops items to fit.
			open := explicitOpenItems(m.Text, ref)
			if CarriesDecision(m.Text) {
				text := contextProse(withoutOpenLines(m.Text, open), contextFactBytes)
				if text != "" && !hasFact(c.Conclusions, text) {
					if len(c.Conclusions) == opts.MaxItems {
						c.Truncated = true
					} else {
						c.Conclusions = append(c.Conclusions, model.ContextFact{Text: text, Provenance: ref})
					}
				}
			}
			for _, item := range open {
				if item.Kind == "gap" {
					if len(c.Gaps) == opts.MaxItems {
						c.Truncated = true
					} else if !hasOpenItem(c.Gaps, item.Text) {
						c.Gaps = append(c.Gaps, item.ContextOpenItem)
					}
				} else if len(c.Conflicts) == opts.MaxItems {
					c.Truncated = true
				} else if !hasOpenItem(c.Conflicts, item.Text) {
					c.Conflicts = append(c.Conflicts, item.ContextOpenItem)
				}
			}
		case sources.RoleCommand:
			if !isVerificationCommand(m.Text) {
				continue
			}
			command := contextCommand(m.Text)
			if command == "" || hasTest(c.Tests, command) {
				continue
			}
			if len(c.Tests) == opts.MaxItems {
				c.Truncated = true
				continue
			}
			c.Tests = append(c.Tests, model.ContextTest{
				Command: command, Outcome: commandOutcome(m.Text), Provenance: ref,
			})
		}
	}

	// Entries were selected from newest to oldest. Presenting them in transcript
	// order lets a resumed agent see the progression without treating the first
	// retained item as the current state.
	reverseFacts(c.Conclusions)
	reverseTests(c.Tests)
	reverseOpenItems(c.Gaps)
	reverseOpenItems(c.Conflicts)
	c = RedactCompactionContext(c)
	rebuildSources(&c)
	return boundCompactionContext(c)
}

func contextWindow(messages []model.Message, opts ExtractOptions) ([]model.Message, bool) {
	var reverse []model.Message
	keptBytes, scannedBytes := 0, 0
	truncated := false
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		scannedBytes += len(m.Text)
		if scannedBytes > maxContextScanBytes {
			return reverseMessages(reverse), true
		}
		if len(m.Text) > opts.MaxTextBytes {
			truncated = true
			continue
		}
		// Tool output, paths and edits can account for almost every record in a
		// real transcript. They are not candidates for the structured packet, so
		// they do not crowd an earlier objective or a recorded verification out of
		// the bounded window.
		if !contextCandidate(m) {
			continue
		}
		if keptBytes+len(m.Text) > opts.MaxTextBytes {
			truncated = true
			continue
		}
		if len(reverse) == opts.MaxRecords {
			truncated = true
			continue
		}
		reverse = append(reverse, m)
		keptBytes += len(m.Text)
	}
	return reverseMessages(reverse), truncated
}

func contextCandidate(m model.Message) bool {
	return m.Role == "user" || m.Role == "assistant" || m.Role == sources.RoleCommand
}

func reverseMessages(in []model.Message) []model.Message {
	out := make([]model.Message, len(in))
	for i := range in {
		out[len(in)-1-i] = in[i]
	}
	return out
}

func contextObjective(s model.Session, window []model.Message) model.ContextFact {
	for i := len(window) - 1; i >= 0; i-- {
		m := window[i]
		if m.Role != "user" || IsAgentArtifact(m.Text) || IsCompactionSummary(strings.TrimSpace(m.Text)) {
			continue
		}
		text := contextProse(m.Text, contextObjectiveBytes)
		if text != "" && !trivialContinuation(text) {
			return model.ContextFact{Text: text, Provenance: contextRef(s, m)}
		}
	}
	// A command/tool-heavy tail can legitimately contain more than the packet's
	// 64 retained records. Keep walking the already-bounded session for the
	// newest substantive user objective rather than letting a tool stream erase
	// the task being resumed.
	scanned := 0
	for i := len(s.Messages) - 1; i >= 0; i-- {
		m := s.Messages[i]
		scanned += len(m.Text)
		if scanned > maxContextScanBytes {
			break
		}
		if m.Role != "user" || IsAgentArtifact(m.Text) || IsCompactionSummary(strings.TrimSpace(m.Text)) {
			continue
		}
		text := contextProse(m.Text, contextObjectiveBytes)
		if text != "" && !trivialContinuation(text) {
			return model.ContextFact{Text: text, Provenance: contextRef(s, m)}
		}
	}
	// The first user record after a host compaction is its summary, not a
	// statement from the user. Its bounded primary intent is still the only
	// honest objective when the retained tail contains only "continue".
	for _, m := range s.Messages {
		if m.Role != "user" || !IsCompactionSummary(strings.TrimSpace(m.Text)) {
			continue
		}
		if text := contextProse(compactionIntent(m.Text), contextObjectiveBytes); text != "" {
			return model.ContextFact{Text: text, Provenance: contextRef(s, m)}
		}
		break
	}
	return model.ContextFact{}
}

// trivialContinuation reports whether a user turn is a way of saying "carry on"
// rather than the task being resumed.
//
// The list it held was English and exact, so a reader who types "продолжай" —
// the commonest turn on the machine this was measured on — became the packet's
// objective, which is the first line a resuming agent reads. nudgeWords covers
// the same idea in the languages a real store holds.
//
// Only the nudge words themselves, not worthAsAsk: that helper also applies a
// minimum length, which is right for picking a handover ask and wrong here —
// "Repair the parser." is eighteen runes and is the task.
func trivialContinuation(text string) bool {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if text == "" {
		return true
	}
	// Punctuation between the words too: "thanks, continue" and "ага, давай" are
	// the same turn as without the comma.
	low := strings.ToLower(strings.Trim(text, " .,!?:;"))
	low = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		switch r {
		case ',', ';', '.', '!', '?', ':':
			return ' '
		}
		return r
	}, low)), " ")
	// "ok go on", "да давай дальше": nudges all the way down, and nothing else.
	// Bounding by length instead let a new instruction through as a nudge — and
	// "давай <do X>" is how an instruction is usually given on this store:
	// measured against this rule, "давай починим экспортер", "ok cap the retries
	// at three", "continue with the parser fix", "yes revert it" and "go fix the
	// pool" all read as "carry on", so the packet kept naming the task the reader
	// had just replaced.
	return onlyNudges(low)
}

// continuationWords are the nudge words plus the ways of saying only "carry on"
// that the handover picker has no use for: `nudgeWords` is shared with it, and
// these belong to this rule alone.
var continuationWords = append(append([]string{}, nudgeWords...),
	"y", "proceed", "please", "please continue", "continue please", "please do it")

// onlyNudges reports whether the turn is made of nudge words and nothing else.
// Longest match first, so "давай дальше" is one nudge rather than "давай" plus a
// word it does not know.
func onlyNudges(low string) bool {
	for low != "" {
		best := ""
		for _, nudge := range continuationWords {
			if len(nudge) <= len(best) {
				continue
			}
			if low == nudge || strings.HasPrefix(low, nudge+" ") {
				best = nudge
			}
		}
		if best == "" {
			return false
		}
		low = strings.TrimSpace(strings.TrimPrefix(low, best))
	}
	return true
}

func contextRef(s model.Session, m model.Message) model.ContextRef {
	return model.ContextRef{SessionID: s.ID, Harness: s.Harness, Role: m.Role, At: m.Time}
}

func contextProse(text string, limit int) string {
	text, _ = redact.Text(text)
	text = MessageText(text)
	return limitContextText(text, limit)
}

func contextCommand(text string) string {
	text, _ = redact.Text(text)
	text = strings.TrimSpace(redact.SafeForDisplay(text))
	return limitContextText(text, contextFactBytes)
}

func limitContextText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	if limit <= len(cutMark) {
		return ""
	}
	return strings.TrimSpace(UTF8SafeCut(text, limit-len(cutMark))) + cutMark
}

type classifiedOpenItem struct {
	model.ContextOpenItem
	Kind string
}

// withoutOpenLines is the message without the lines that became open items, so a
// turn that states a conclusion and what is left over is charged once for each.
func withoutOpenLines(text string, open []classifiedOpenItem) string {
	if len(open) == 0 {
		return text
	}
	drop := make(map[string]bool, len(open))
	for _, item := range open {
		drop[item.Text] = true
	}
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if drop[contextProse(line, contextFactBytes)] {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func explicitOpenItems(text string, ref model.ContextRef) []classifiedOpenItem {
	var out []classifiedOpenItem
	for _, line := range strings.Split(text, "\n") {
		line = contextProse(line, contextFactBytes)
		if line == "" {
			continue
		}
		low := strings.ToLower(line)
		kind := ""
		switch {
		case hasAnyLabel(low, gapLabels):
			kind = "gap"
		case hasAnyLabel(low, conflictLabels):
			kind = "conflict"
		}
		if kind != "" {
			out = append(out, classifiedOpenItem{ContextOpenItem: model.ContextOpenItem{Text: line, Provenance: ref}, Kind: kind})
		}
	}
	return out
}

// gapLabels are the ways a turn says what is still to be done, at the start of
// the line where the shape is unambiguous.
//
// The list was gap/unverified/missing/blocked, and nobody writes those: measured
// over 72137 assistant lines on a real store, that shape appeared once. The words
// below, in the same line-initial shape, appear 28 times and every one of the
// fourteen sampled was genuinely an open item ("Осталось: **страница сравнения**
// …", "Что осталось: две дальние формулировки"). The shape is what keeps this
// precise — "осталось" in the middle of a sentence is usually "12 of 17 jobs are
// left", which is not an open item at all.
var gapLabels = []string{
	"gap", "unverified", "missing", "blocked",
	"still open", "open question", "open questions", "open item", "open items",
	"what's left", "whats left", "left to do", "not done", "not verified", "todo",
	"осталось", "что осталось", "остаётся", "остается", "остался",
	"открытый вопрос", "открытые вопросы", "не сделал", "чего не сделал",
	"не доделал", "не проверено",
}

var conflictLabels = []string{"conflict", "disagreement", "contradiction", "конфликт", "противоречие"}

func hasAnyLabel(line string, labels []string) bool {
	for _, label := range labels {
		if hasLabel(line, label) {
			return true
		}
	}
	return false
}

// hasLabel reports whether the line opens with this label. The dash forms are
// how the same thing is written in prose — "**Открытый вопрос — OpenClaw плагин
// …**" — and the bold markers are stripped rather than spelled into every label.
func hasLabel(line, label string) bool {
	line = strings.TrimLeft(line, "-*#> \t")
	line = strings.TrimPrefix(line, "**")
	if !strings.HasPrefix(line, label) {
		return false
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(line, label), "**")
	rest = strings.TrimLeft(rest, " ")
	for _, sep := range []string{":", "—", "-", "–"} {
		if strings.HasPrefix(rest, sep) {
			return true
		}
	}
	return false
}

func hasFact(facts []model.ContextFact, text string) bool {
	for _, fact := range facts {
		if normalizedContextText(fact.Text) == normalizedContextText(text) {
			return true
		}
	}
	return false
}

func hasOpenItem(items []model.ContextOpenItem, text string) bool {
	for _, item := range items {
		if normalizedContextText(item.Text) == normalizedContextText(text) {
			return true
		}
	}
	return false
}

func hasTest(tests []model.ContextTest, command string) bool {
	for _, test := range tests {
		if normalizedContextText(test.Command) == normalizedContextText(command) {
			return true
		}
	}
	return false
}

func normalizedContextText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

func reverseFacts(items []model.ContextFact) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func reverseTests(items []model.ContextTest) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func reverseOpenItems(items []model.ContextOpenItem) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func isVerificationCommand(command string) bool {
	low := strings.ToLower(command)
	for _, marker := range []string{
		"go test", "go vet", "pytest", "cargo test", "swift test", "npm test", "pnpm test", "yarn test",
		"vitest", "jest", "rspec", "mvn test", "gradlew test", "make test", "make check",
	} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

var commandExitRE = regexp.MustCompile(`(?:→|->)\s*exit\s+([0-9]+)\b`)

func commandOutcome(command string) string {
	m := commandExitRE.FindStringSubmatch(command)
	if len(m) != 2 {
		return "recorded"
	}
	if m[1] == "0" {
		return "passed"
	}
	return "failed"
}

// RedactCompactionContext copies c and redacts every transcript-derived text
// field. Storage calls it immediately before persisting; extraction and
// rendering call it too, so a new caller cannot accidentally create a second
// unredacted boundary. DEJA_NO_REDACT remains the documented opt-out through
// redact.Text.
func RedactCompactionContext(c model.CompactionContext) model.CompactionContext {
	c.Conclusions = append([]model.ContextFact(nil), c.Conclusions...)
	c.Tests = append([]model.ContextTest(nil), c.Tests...)
	c.Gaps = append([]model.ContextOpenItem(nil), c.Gaps...)
	c.Conflicts = append([]model.ContextOpenItem(nil), c.Conflicts...)
	c.Sources = append([]model.ContextRef(nil), c.Sources...)
	c.Objective.Text = limitContextText(redactContextText(c.Objective.Text), contextObjectiveBytes)
	c.Objective.Provenance = redactContextRef(c.Objective.Provenance)
	for i := range c.Conclusions {
		c.Conclusions[i].Text = limitContextText(redactContextText(c.Conclusions[i].Text), contextFactBytes)
		c.Conclusions[i].Provenance = redactContextRef(c.Conclusions[i].Provenance)
	}
	for i := range c.Tests {
		c.Tests[i].Command = limitContextText(redactContextText(c.Tests[i].Command), contextFactBytes)
		c.Tests[i].Provenance = redactContextRef(c.Tests[i].Provenance)
	}
	for i := range c.Gaps {
		c.Gaps[i].Text = limitContextText(redactContextText(c.Gaps[i].Text), contextFactBytes)
		c.Gaps[i].Provenance = redactContextRef(c.Gaps[i].Provenance)
	}
	for i := range c.Conflicts {
		c.Conflicts[i].Text = limitContextText(redactContextText(c.Conflicts[i].Text), contextFactBytes)
		c.Conflicts[i].Provenance = redactContextRef(c.Conflicts[i].Provenance)
	}
	for i := range c.Sources {
		c.Sources[i] = redactContextRef(c.Sources[i])
	}
	c.Freshness.Head = limitContextText(redactContextText(c.Freshness.Head), contextFactBytes)
	c.Freshness.Branch = limitContextText(redactContextText(c.Freshness.Branch), contextFactBytes)
	c.Freshness.WorktreeState = limitContextText(redactContextText(c.Freshness.WorktreeState), contextFactBytes)
	c.Freshness.Error = limitContextText(redactContextText(c.Freshness.Error), contextFactBytes)
	return boundCompactionContext(c)
}

func redactContextRef(ref model.ContextRef) model.ContextRef {
	// A harness normally supplies short opaque identifiers. They are still
	// external strings: bound and redact them before manifest persistence so a
	// malformed transcript cannot evade the packet's 24 KiB storage ceiling or
	// put a credential in a provenance label.
	ref.SessionID = limitContextText(redactContextText(ref.SessionID), 256)
	ref.Harness = limitContextText(redactContextText(ref.Harness), 64)
	ref.Role = limitContextText(redactContextText(ref.Role), 64)
	return ref
}

func redactContextText(text string) string {
	text, _ = redact.Text(text)
	return strings.TrimSpace(redact.SafeForDisplay(text))
}

func rebuildSources(c *model.CompactionContext) {
	seen := map[model.ContextRef]bool{}
	c.Sources = nil
	add := func(ref model.ContextRef) {
		if ref.SessionID == "" || seen[ref] {
			return
		}
		seen[ref] = true
		c.Sources = append(c.Sources, ref)
	}
	add(c.Objective.Provenance)
	for _, fact := range c.Conclusions {
		add(fact.Provenance)
	}
	for _, test := range c.Tests {
		add(test.Provenance)
	}
	for _, item := range c.Gaps {
		add(item.Provenance)
	}
	for _, item := range c.Conflicts {
		add(item.Provenance)
	}
}

func boundCompactionContext(c model.CompactionContext) model.CompactionContext {
	for {
		rebuildSources(&c)
		if serializedContextBytes(c) <= maxContextBytes {
			return c
		}
		c.Truncated = true
		switch {
		// The command list goes before the conclusions. A resuming agent can
		// re-run a command; it cannot re-derive what the last session settled,
		// and that is the whole reason the packet exists. Measured on the local
		// model with a packet over its budget: conclusions-first answered the
		// question 3 of 3 times against 1 of 3 when the command list was kept
		// and the conclusions were the first thing dropped.
		case len(c.Tests) > 0:
			c.Tests = c.Tests[1:]
		case len(c.Conclusions) > 0:
			c.Conclusions = c.Conclusions[1:]
		case len(c.Gaps) > 1:
			c.Gaps = c.Gaps[:len(c.Gaps)-1]
		case len(c.Conflicts) > 1:
			c.Conflicts = c.Conflicts[:len(c.Conflicts)-1]
		case len(c.Objective.Text) > len(cutMark)+8:
			c.Objective.Text = limitContextText(c.Objective.Text, len(c.Objective.Text)/2)
		default:
			return c
		}
	}
}

func serializedContextBytes(c model.CompactionContext) int {
	b, err := json.Marshal(c)
	if err != nil {
		return maxContextBytes + 1
	}
	return len(b)
}

// RenderCompactionContext formats a bounded continuation packet. It labels all
// state as transcript-derived and preserves explicit absence of a repository
// freshness check rather than implying that a repository is clean.
func RenderCompactionContext(c model.CompactionContext, byteBudget int) string {
	if byteBudget <= 0 {
		byteBudget = defaultRenderBudget
	}
	c = RedactCompactionContext(c)
	const omitted = "\nOmitted transcript records are not represented here.\n"
	limit := byteBudget - len(omitted)
	if limit < 0 {
		limit = 0
	}
	var b strings.Builder
	omittedAny := c.Truncated
	add := func(chunk string) bool {
		if chunk == "" {
			return true
		}
		if b.Len()+len(chunk) > limit {
			omittedAny = true
			return false
		}
		b.WriteString(chunk)
		return true
	}
	add("Compaction context from normalized transcript records. Conclusions and open items are recorded claims, not independently verified.\n")
	add(renderFreshness(c.Freshness))
	if c.Objective.Text != "" {
		add("\nObjective\n- " + c.Objective.Text + provenanceSuffix(c.Objective.Provenance) + "\n")
	}
	addOpenSection(&b, &omittedAny, limit, "Explicit gaps", c.Gaps)
	addOpenSection(&b, &omittedAny, limit, "Explicit conflicts", c.Conflicts)
	// Conclusions before the command list, for the reason boundCompactionContext
	// drops the list first: the render budget cuts from the bottom, so whatever
	// is printed last is what a tight packet loses.
	// Conclusions before the command list, for the reason boundCompactionContext
	// drops the list first: the render budget cuts from the bottom, so whatever
	// is printed last is what a tight packet loses.
	addFactSection(&b, &omittedAny, limit, "Assistant-reported conclusions", c.Conclusions)
	addTestSection(&b, &omittedAny, limit, c.Tests)
	if omittedAny && b.Len()+len(omitted) <= byteBudget {
		b.WriteString(omitted)
	}
	out := strings.TrimSpace(b.String())
	if len(out)+1 <= byteBudget {
		return out + "\n"
	}
	if len(out) <= byteBudget {
		return out
	}
	out = strings.TrimSpace(limitContextText(out, byteBudget))
	if len(out)+1 <= byteBudget {
		return out + "\n"
	}
	return out
}

// renderFreshness says whether the conclusions below still describe this
// checkout, in the words a resuming agent can act on.
//
// It used to print the fingerprints: `head=<40 hex>, branch=master,
// worktree=<64 hex>, checked=<stamp>` — 150 bytes of a 1.4 KB packet, and the
// worktree hash is deja's own digest of the tree, which nothing outside deja can
// do anything with. What the agent needs is the verdict the recovery path has
// already worked out: unchanged, or changed and here is the commit it was
// captured at, which is enough for a diff.
//
// The error case also read as "Repository freshness unavailable: Repository
// changed since compaction." — the recovery path puts its verdict in the same
// field, so the prefix contradicted it.
func renderFreshness(f model.RepositoryFreshness) string {
	if strings.HasPrefix(f.WorktreeState, "partial:") {
		return "Repository fingerprint is partial; validate files and tests before reusing conclusions.\n"
	}
	if f.Error != "" {
		line := f.Error
		if !strings.HasPrefix(strings.ToLower(line), "repository") {
			line = "Repository freshness unavailable: " + line
		}
		if head := shortHead(f.Head); head != "" {
			line += " Captured at " + head + "."
		}
		return line + "\n"
	}
	if f.Head == "" && f.Branch == "" {
		return "Repository freshness unavailable: hook did not record it.\n"
	}
	line := "Repository unchanged since capture"
	if f.Branch != "" {
		line += " (branch " + f.Branch + ")"
	}
	return line + ".\n"
}

// shortHead is a commit an agent can pass to git, without the rest of the hash.
func shortHead(head string) string {
	if len(head) < 12 {
		return head
	}
	return head[:12]
}

func addOpenSection(b *strings.Builder, omitted *bool, limit int, title string, items []model.ContextOpenItem) {
	if len(items) == 0 {
		return
	}
	if b.Len()+len("\n"+title+"\n") > limit {
		*omitted = true
		return
	}
	b.WriteString("\n" + title + "\n")
	for _, item := range items {
		line := "- " + item.Text + provenanceSuffix(item.Provenance) + "\n"
		if b.Len()+len(line) > limit {
			*omitted = true
			return
		}
		b.WriteString(line)
	}
}

func addTestSection(b *strings.Builder, omitted *bool, limit int, tests []model.ContextTest) {
	if len(tests) == 0 {
		return
	}
	if b.Len()+len("\nRecorded verification commands\n") > limit {
		*omitted = true
		return
	}
	b.WriteString("\nRecorded verification commands\n")
	for _, test := range tests {
		line := "- [" + test.Outcome + "] " + test.Command + provenanceSuffix(test.Provenance) + "\n"
		if b.Len()+len(line) > limit {
			*omitted = true
			return
		}
		b.WriteString(line)
	}
}

func addFactSection(b *strings.Builder, omitted *bool, limit int, title string, facts []model.ContextFact) {
	if len(facts) == 0 {
		return
	}
	if b.Len()+len("\n"+title+"\n") > limit {
		*omitted = true
		return
	}
	b.WriteString("\n" + title + "\n")
	for _, fact := range facts {
		line := "- " + fact.Text + provenanceSuffix(fact.Provenance) + "\n"
		if b.Len()+len(line) > limit {
			*omitted = true
			return
		}
		b.WriteString(line)
	}
}

func provenanceSuffix(ref model.ContextRef) string {
	if ref.SessionID == "" {
		return ""
	}
	s := " [" + ref.Harness + ":" + Short(ref.SessionID) + "/" + ref.Role
	if !ref.At.IsZero() {
		s += " @" + ref.At.UTC().Format(time.RFC3339)
	}
	return s + "]"
}
