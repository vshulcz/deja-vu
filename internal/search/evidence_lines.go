package search

// Recall shows the record a model's conclusion rests on instead of the
// conclusion, and says who wrote every other line. The receipts were found at
// index time (internal/evidence), so nothing here reads a transcript: a line
// is placed in the session's messages the index already handed over, and its
// receipt is a keyed lookup.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/vshulcz/deja-vu/internal/evidence"
	"github.com/vshulcz/deja-vu/internal/model"
)

// UserRuleLabel marks the one kind of recalled line an agent is told to
// follow; the frame and the MCP instructions name it.
const UserRuleLabel = evidence.LabelUserRule

func evidenceReader(dir string) *evidence.Reader {
	if dir == "" {
		// index.DefaultDir, which this package cannot import.
		if dir = os.Getenv("DEJA_INDEX_DIR"); dir == "" {
			h, _ := os.UserHomeDir()
			dir = filepath.Join(h, ".cache", "deja", "index.db")
		}
	}
	return evidence.Open(dir)
}

// evFind places a shown line among the session's messages: the one whose text
// carries the line's opening. onlyModel narrows it to what a model said.
func evFind(s model.Session, line string, onlyModel bool, norms map[int]string) (model.Message, bool) {
	k := evidence.Norm(line)
	if r := []rune(k); len(r) > 60 {
		k = string(r[:60])
	}
	if len([]rune(k)) < 12 {
		return model.Message{}, false
	}
	for i, m := range s.Messages {
		if onlyModel && m.Role != "assistant" {
			continue
		}
		n, ok := norms[i]
		if !ok {
			n = evidence.Norm(m.Text)
			norms[i] = n
		}
		if strings.Contains(n, k) {
			return m, true
		}
	}
	return model.Message{}, false
}

// IsUserRule reports whether a shown line is one of the rules the surface
// found for this session in the caller's project (index.MarkUserRules). The
// line has to open with the rule: a snippet that starts elsewhere in the
// message may be the pasted part of it.
func IsUserRule(s model.Session, line string) bool {
	line = strings.TrimPrefix(strings.TrimSpace(line), evidence.LabelUser)
	for _, r := range s.UserRules {
		if evidence.SameLine(r, line) {
			return true
		}
	}
	return false
}

// takenBack reports a line naming a command the person took back, other
// than the line that takes it back.
func takenBack(s model.Session, line string) bool {
	if len(s.TakenBack) == 0 || len(evidence.Revoked(line)) > 0 {
		return false
	}
	for _, c := range evidence.Commands(line) {
		if slices.Contains(s.TakenBack, c) {
			return true
		}
	}
	return false
}

// evPersonLabel names who wrote a session's user-role line, by the source.
func evPersonLabel(s model.Session, text string) string {
	switch {
	case s.Harness == "deja":
		return evidence.LabelNote
	case evidence.Spawned(s):
		return evidence.LabelParent
	case evidence.AgentReport(text):
		return evidence.LabelAgent
	}
	return evidence.LabelUser
}

// personLabel is evPersonLabel for a line a person's turn holds, saying what
// it is to the reader: the person's rule for this project, a rule they took
// back, or an instruction that is neither, which is to be asked about.
func personLabel(s model.Session, line string) string {
	l := evPersonLabel(s, line)
	if l != evidence.LabelUser {
		return l
	}
	switch {
	case IsUserRule(s, line):
		return evidence.LabelUserRule
	case takenBack(s, line):
		return evidence.LabelTakenBack
	case evidence.RuleLike(line) && len(evidence.Revoked(line)) == 0:
		return evidence.LabelUnmarked
	}
	return l
}

// markUserRules relabels the person's lines and the outputs that carry an
// instruction to agents, by personLabel, then scrubs them (scrubLine).
func markUserRules(s model.Session, lines []string) []string {
	return scrubLines(s, labelLines(s, lines))
}

func labelLines(s model.Session, lines []string) []string {
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if rest, ok := strings.CutPrefix(t, evidence.LabelUser); ok {
			lines[i] = personLabel(s, rest) + rest
		} else if rest, ok := strings.CutPrefix(t, "Output: "); ok && evidence.AddressesAgent(rest) {
			lines[i] = evidence.LabelOutputOrder + rest
		}
	}
	return lines
}

// personLabels open a line a person's turn holds; toolLabels one a tool
// printed or a parent agent wrote. Nothing else is cut: what a model said, a
// subagent's report and an agent's note are the remedies recall exists to
// carry.
var (
	personLabels = []string{evidence.LabelUnmarked, evidence.LabelUser, "User: "}
	toolLabels   = []string{evidence.LabelOutputOrder, "Output: ", "Ran: ", evidence.LabelParent}
)

// cutPersonOrders is a person's turn of s as recall may show it: whole when a
// person typed it in the reader's project, else without its orders (a parent
// agent's prompt, another project) or without the orders that were pasted or
// came from a headless run.
func cutPersonOrders(s model.Session, text string) string {
	return evidence.CutOrders(text, s.Foreign || evidence.Spawned(s), s.Planted)
}

// scrubLine is line as recall may show it: orders cut from what no person in
// this project typed (evidence.CutOrders), and false for a rule the person
// took back, which is not shown.
func scrubLine(s model.Session, line string) (string, bool) {
	t := strings.TrimSpace(line)
	body := strings.TrimPrefix(t, "- ")
	if strings.HasPrefix(body, evidence.LabelUserRule) {
		return line, true
	}
	if strings.HasPrefix(body, evidence.LabelTakenBack) {
		return "", false
	}
	cut := func(rest string, f func(string) string) (string, bool) {
		c := f(rest)
		if c == rest {
			return line, true
		}
		if strings.TrimSpace(strings.ReplaceAll(c, evidence.LeftOut, "")) == "" {
			return "", false
		}
		return strings.Replace(line, rest, c, 1), true
	}
	for _, l := range personLabels {
		if rest, ok := strings.CutPrefix(body, l); ok {
			return cut(rest, func(x string) string { return cutPersonOrders(s, x) })
		}
	}
	for _, l := range toolLabels {
		if rest, ok := strings.CutPrefix(body, l); ok {
			return cut(rest, func(x string) string { return evidence.CutOrders(x, true, nil) })
		}
	}
	return line, true
}

func scrubLines(s model.Session, lines []string) []string {
	out := lines[:0]
	for _, l := range lines {
		if kept, ok := scrubLine(s, l); ok {
			out = append(out, kept)
		}
	}
	return out
}

// ScrubLines is scrubLine over the lines a surface shows for s.
func ScrubLines(s model.Session, lines []string) []string {
	return scrubLines(s, slices.Clone(lines))
}

// ScrubMessages is s with its messages as recall may show them: orders cut
// from tool output and from the person turns no person in this project
// typed. The messages are copied; s may share them with a cache.
func ScrubMessages(s *model.Session) {
	ms := make([]model.Message, 0, len(s.Messages))
	for _, m := range s.Messages {
		switch m.Role {
		case "user":
			m.Text = cutPersonOrders(*s, m.Text)
		case "tool-output":
			m.Text = evidence.CutOrders(m.Text, true, nil)
		}
		if strings.TrimSpace(strings.ReplaceAll(m.Text, evidence.LeftOut, "")) == "" {
			continue
		}
		ms = append(ms, m)
	}
	s.Messages = ms
}

// EvidenceRewrite takes the lines a recall surface is about to show for one
// session and puts each in its author's words: a model line becomes the
// records its turn holds, followed by the line itself under the model's label,
// and a person's line is marked as theirs. The model's line is never dropped:
// it is where the remedy is said ("switched RoundCents to decimal"), and the
// record that backs it can be the failure it fixed. A session the receipts do
// not cover keeps its lines and only has orders cut (ScrubLines).
func EvidenceRewrite(s model.Session, dir string, lines []string) []string {
	return evidenceRewrite(s, dir, lines, false)
}

func evidenceRewrite(s model.Session, dir string, lines []string, assumeModel bool, skip ...string) []string {
	rd := evidenceReader(dir)
	if !rd.Covers(s.Updated) {
		return ScrubLines(s, lines)
	}
	norms := map[int]string{}
	type res struct {
		line   string
		model  bool
		backed []string
		label  string
	}
	rs := make([]res, len(lines))
	for i, l := range lines {
		rs[i].line = l
		if s.Harness == "deja" {
			rs[i].label = evidence.LabelNote
			continue
		}
		// A conclusion line is the opening of a model message: the key finds
		// it without walking the session.
		if assumeModel {
			if b := rd.Lookup(s.Harness, s.ID, l); len(b) > 0 {
				rs[i].model, rs[i].backed = true, b
				continue
			}
		}
		m, ok := evFind(s, l, assumeModel, norms)
		if !ok {
			rs[i].model = assumeModel
			continue
		}
		switch m.Role {
		case "assistant", "summary":
			rs[i].model = true
			if m.Role == "assistant" {
				if b := rd.Lookup(s.Harness, s.ID, m.Text); len(b) > 0 {
					rs[i].backed = b
				}
			}
		case "user":
			rs[i].label = evPersonLabel(s, m.Text)
		case "tool-output":
			if evidence.AgentReport(m.Text) {
				rs[i].label = evidence.LabelAgent
			} else {
				rs[i].label = "Output: "
			}
		case "command":
			rs[i].label = "Ran: "
		case "edit":
			rs[i].label = "Edit: "
		}
	}
	var out []string
	seen := map[string]bool{}
	add := func(l string) {
		k := evidence.Norm(l)
		if seen[k] {
			return
		}
		for _, sk := range skip {
			if sk != "" && strings.Contains(k, evidence.Norm(sk)) {
				return
			}
		}
		seen[k] = true
		out = append(out, l)
	}
	for _, r := range rs {
		switch {
		case len(r.backed) > 0:
			for _, b := range r.backed {
				add(b)
			}
			// Backed by the person's words alone, the record says nothing the
			// model did, and the line keeps the unbacked label.
			label := evidence.LabelModelSaid
			if !slices.ContainsFunc(r.backed, func(b string) bool { return !strings.HasPrefix(b, evidence.LabelUser) }) {
				label = evidence.LabelModel
			}
			add(label + strings.TrimPrefix(strings.TrimSpace(r.line), "→ "))
		case r.model:
			add(evidence.LabelModel + strings.TrimPrefix(strings.TrimSpace(r.line), "→ "))
		case r.label != "":
			add(r.label + strings.TrimPrefix(strings.TrimSpace(r.line), "- "))
		default:
			out = append(out, r.line)
		}
	}
	return markUserRules(s, out)
}

// contextBodyMaxMessages bounds the session printed record by record. The
// question-focused body picks the region of a long session that matched; this
// one prints from the top, so it is kept to sessions short enough to fit.
const contextBodyMaxMessages = 60

// evContextBody prints a session as its records, each under who wrote it,
// from the messages the index holds.
func evContextBody(w io.Writer, s model.Session) bool {
	if len(s.Messages) == 0 || len(s.Messages) > contextBodyMaxMessages || s.Held > 0 ||
		!evidenceReader("").Covers(s.Updated) {
		return false
	}
	// A record longer than its clip would be cut with nothing saying where
	// the rest is; the question-focused body says so.
	clip := map[string]int{"user": 1500, "command": 400, "edit": 1500, "tool-output": 1000, "assistant": 800, "summary": 800}
	for _, m := range s.Messages {
		if n, ok := clip[m.Role]; ok && len([]rune(strings.TrimSpace(m.Text))) > n {
			return false
		}
	}
	for _, m := range s.Messages {
		t := strings.TrimSpace(m.Text)
		if t == "" {
			continue
		}
		switch m.Role {
		case "user":
			head := "user"
			switch evPersonLabel(s, t) {
			case evidence.LabelNote:
				head = "note an agent saved"
			case evidence.LabelParent:
				head = "prompt written by a parent agent"
			case evidence.LabelAgent:
				head = "report a subagent wrote (its words, not a record)"
			}
			fmt.Fprintf(w, "## %s\n%s\n", head, SafeText(evClip(t, 1500)))
		case "command":
			fmt.Fprintf(w, "## ran\n%s\n", SafeText(evClip(t, 400)))
		case "edit":
			fmt.Fprintf(w, "## edit (replaced span)\n%s\n", SafeText(evClip(t, 1500)))
		case "tool-output":
			head := "tool output"
			if evidence.AgentReport(t) {
				head = "report a subagent wrote (its words, not a record)"
			}
			fmt.Fprintf(w, "## %s\n%s\n", head, SafeText(evClip(t, 1000)))
		case "assistant", "summary":
			fmt.Fprintf(w, "## said by the model (its words, not a record)\n%s\n", SafeText(evClip(t, 800)))
		}
	}
	return true
}

func evClip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// userRuleLines writes the rules found for s in the caller's project, each
// under the one mark an agent is told to follow.
func userRuleLines(b *strings.Builder, s model.Session, indent string) {
	for _, r := range s.UserRules {
		fmt.Fprintf(b, "%s%s%s\n", indent, evidence.LabelUserRule, digestLine(r))
	}
}
