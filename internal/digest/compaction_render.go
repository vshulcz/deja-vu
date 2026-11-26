package digest

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
)

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
	c.Carry = append([]model.ContextCarry(nil), c.Carry...)
	c.Rules = append([]model.ContextFact(nil), c.Rules...)
	for i := range c.Rules {
		c.Rules[i].Text = limitContextText(redactContextText(c.Rules[i].Text), standingRuleBytes)
		c.Rules[i].Provenance = redactContextRef(c.Rules[i].Provenance)
	}
	c.Modified = append([]model.ContextFact(nil), c.Modified...)
	for i := range c.Modified {
		c.Modified[i].Text = limitContextText(redactContextText(c.Modified[i].Text), contextFactBytes)
		c.Modified[i].Provenance = redactContextRef(c.Modified[i].Provenance)
	}
	c.Goal.Text = limitContextText(redactContextText(c.Goal.Text), goalBytes)
	c.Goal.Provenance = redactContextRef(c.Goal.Provenance)
	for i := range c.Carry {
		c.Carry[i].Text = limitContextText(redactContextText(c.Carry[i].Text), carryTextBytes)
		c.Carry[i].Key = limitContextText(redactContextText(c.Carry[i].Key), carryTextBytes)
		c.Carry[i].Kind = limitContextText(c.Carry[i].Kind, 16)
	}
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
	add(c.Goal.Provenance)
	add(c.Objective.Provenance)
	for _, fact := range c.Modified {
		add(fact.Provenance)
	}
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
		case len(c.Modified) > 0:
			c.Modified = c.Modified[1:]
		case len(c.Conclusions) > 0:
			c.Conclusions = c.Conclusions[1:]
		case len(c.Gaps) > 1:
			c.Gaps = c.Gaps[:len(c.Gaps)-1]
		case len(c.Conflicts) > 1:
			c.Conflicts = c.Conflicts[:len(c.Conflicts)-1]
		case c.Goal.Text != "":
			c.Goal = model.ContextFact{}
		case len(c.Rules) > 0:
			c.Rules = c.Rules[:len(c.Rules)-1]
		case len(c.Objective.Text) > len(cutMark)+8:
			c.Objective.Text = limitContextText(c.Objective.Text, len(c.Objective.Text)/2)
		// The carried list goes last. It is at most twelve lines, and it holds
		// what the host summary drops: 75% of the still-open #N items the
		// summaries lost on 189 compactions were on it.
		case len(c.Carry) > 0:
			c.Carry = c.Carry[:len(c.Carry)-1]
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
	return renderContext(c, byteBudget,
		"Compaction context from normalized transcript records. Conclusions and open items are recorded claims, not independently verified.\n",
		true)
}

// RenderHandoffContext is the same packet for a session handed to another
// agent. A handoff has no hook that checked the repository, so the freshness
// line would only ever say it was not recorded.
func RenderHandoffContext(c model.CompactionContext, byteBudget int) string {
	return renderContext(c, byteBudget,
		"State of the session from its transcript records. Conclusions and open items are its own claims, not independently verified.\n",
		false)
}

func renderContext(c model.CompactionContext, byteBudget int, header string, compaction bool) string {
	if byteBudget <= 0 {
		byteBudget = defaultRenderBudget
	}
	c = RedactCompactionContext(c)
	const omitted = "\nOmitted transcript records are not represented here.\n"
	limit := max(byteBudget-len(omitted), 0)
	var b strings.Builder
	omittedAny := c.Truncated
	add := func(chunk string) {
		if chunk == "" {
			return
		}
		if b.Len()+len(chunk) > limit {
			omittedAny = true
			return
		}
		b.WriteString(chunk)
	}
	add(header)
	latest := "Latest request"
	if compaction {
		add(renderFreshness(c.Freshness))
		latest = "Latest request before compaction"
	}
	// Ahead of the objective because the budget cuts from the bottom, and this
	// is the part the host's own summary does not carry.
	addCarrySection(&b, &omittedAny, limit, c.Carry)
	addRulesSection(&b, &omittedAny, limit, c.Rules)
	switch {
	case c.Objective.Text != "" && c.Goal.Text != "":
		// The newest request alone is a step; the first one says what the steps
		// are for. Labelled, so neither reads as the other.
		add("\nObjective\n- Session started with: " + c.Goal.Text + provenanceSuffix(c.Goal.Provenance) + "\n" +
			"- " + latest + ": " + c.Objective.Text + provenanceSuffix(c.Objective.Provenance) + "\n")
	case c.Objective.Text != "":
		add("\nObjective\n- " + c.Objective.Text + provenanceSuffix(c.Objective.Provenance) + "\n")
	}
	addOpenSection(&b, &omittedAny, limit, "Explicit gaps", c.Gaps)
	addOpenSection(&b, &omittedAny, limit, "Explicit conflicts", c.Conflicts)
	// Conclusions before the command list, for the reason boundCompactionContext
	// drops the list first: the render budget cuts from the bottom, so whatever
	// is printed last is what a tight packet loses.
	addFactSection(&b, &omittedAny, limit, "Assistant-reported conclusions", c.Conclusions)
	addFactSection(&b, &omittedAny, limit, "Modified in this session", c.Modified)
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
	return UTF8SafeCut(head, 12)
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
