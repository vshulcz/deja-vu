package main

import (
	"fmt"
	"regexp"
	"strings"
)

// statYellow marks a row that needs a look: stale, broken, unreadable.
const statYellow = "\x1b[33m"

// doctorScreen lays the text report out for a terminal. The rows are the ones
// printDoctorText writes for a pipe; this only arranges them:
//
//   - one line on top with the verdict and the command that fixes it;
//   - in MCP wiring, Commands and Hooks, a row that only says "missing" for an
//     agent that is not on this machine folds into one counted line, the way
//     Harness stores already folds its absent stores (#4625);
//   - the Index keys line up, long lines wrap at a space under their own
//     column, and the state words carry colour when colour is on.
//
// all keeps every row, as `deja doctor --all` asks.
func doctorScreen(text string, report doctorReport, all, colour bool, width int) string {
	present := doctorAgentsHere(report)
	var out []string
	if v := doctorVerdict(report, present); v != "" {
		out = append(out, v, "")
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	section := ""
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if doctorSectionHeading(line) || line == "" {
			section = line
			out = append(out, line)
			continue
		}
		switch section {
		case "MCP wiring:", "Commands:", "Hooks:":
			// The section runs to the next blank line.
			end := i
			for end < len(lines) && lines[end] != "" {
				end++
			}
			out = append(out, doctorFoldSection(section, lines[i:end], present, all)...)
			i = end - 1
			continue
		case "Index:":
			line = doctorIndexKey(line)
		}
		out = append(out, line)
	}
	var b strings.Builder
	section = ""
	for _, line := range out {
		if doctorSectionHeading(line) {
			section = line
		}
		for _, l := range doctorWrap(line, width) {
			if colour {
				l = doctorColourState(section, l)
			}
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

var doctorHeadingRe = regexp.MustCompile(`^[A-Z][A-Za-z ]*:$`)

func doctorSectionHeading(line string) bool {
	return doctorHeadingRe.MatchString(line)
}

// doctorAgentKey puts the names the sections use for one agent under one key:
// the store is "claude" and the wiring "claude-code", the codex hook row is
// "codex-hook", and VS Code's chat store is "copilot-chat" where its MCP row is
// "vscode".
func doctorAgentKey(name string) string {
	switch name {
	case "claude":
		return "claude-code"
	case "codex-hook":
		return "codex"
	case "copilot-chat":
		return "vscode"
	}
	return name
}

// doctorAgentsHere is every agent this machine shows a sign of: a config deja
// would wire (what `deja install --auto` walks), a store with something in it,
// or anything of deja's already written for it. The shared skill file is not
// a sign: one file there speaks for nine agents.
func doctorAgentsHere(report doctorReport) map[string]bool {
	here := map[string]bool{}
	for _, t := range existingTargets() {
		here[doctorAgentKey(t)] = true
	}
	for _, s := range report.Stores {
		if s.State != "missing" && s.Name != "deja" {
			here[doctorAgentKey(s.Name)] = true
		}
	}
	for _, m := range report.MCP {
		if m.State != "config-missing" {
			here[doctorAgentKey(m.Name)] = true
		}
	}
	shared := sharedSkillPath()
	for _, a := range report.AutoRecall {
		if a.State != "missing" && a.Path != shared {
			here[doctorAgentKey(a.Name)] = true
		}
	}
	for _, c := range report.Commands {
		if c.State == "written" || c.State == "someone else's" {
			here[doctorAgentKey(c.Name)] = true
		}
	}
	return here
}

// doctorVerdict is the line a reader needs before any section: how much
// history deja found, how many of the agents here can reach it, and the one
// command that wires the rest.
func doctorVerdict(report doctorReport, present map[string]bool) string {
	stores, sessions := 0, 0
	for _, s := range report.Stores {
		if s.State != "missing" && s.Name != "deja" {
			stores++
		}
		sessions += s.IndexedSessions
	}
	wired := map[string]bool{}
	for _, m := range report.MCP {
		if m.State == "wired" || m.State == "plugin" {
			wired[doctorAgentKey(m.Name)] = true
		}
	}
	for _, a := range report.AutoRecall {
		if a.State == "wired" || a.State == "plugin" {
			wired[doctorAgentKey(a.Name)] = true
		}
	}
	agents, reached := 0, 0
	for _, m := range report.MCP {
		k := doctorAgentKey(m.Name)
		if !present[k] {
			continue
		}
		agents++
		if wired[k] {
			reached++
		}
	}
	if stores == 0 && sessions == 0 && agents == 0 {
		return "no agent history or config here — `deja sources` shows where deja looked"
	}
	head := doctorCount(sessions, "session") + " from " + doctorCount(stores, "store")
	switch {
	case agents == 0:
		return head + "; no agent config on this machine to wire"
	case reached == agents:
		return fmt.Sprintf("%s; %d of %d agents wired", head, reached, agents)
	}
	return fmt.Sprintf("%s; %d of %d agents wired — `deja install --auto`", head, reached, agents)
}

var (
	// Guidance "written" or "skill" for an agent that is not here is the
	// shared skill file, which one install writes for all of them.
	doctorMCPAbsent  = regexp.MustCompile(`^config missing +guidance (missing|unsupported|written) `)
	doctorFileAbsent = regexp.MustCompile(`^(missing|skill) `)
)

// doctorFoldSection folds the rows of one wiring section that say nothing but
// "not here" about an agent this machine does not have. A row with a note
// under it keeps its place: the note is the reason to read it.
func doctorFoldSection(section string, lines []string, present map[string]bool, all bool) []string {
	type block struct {
		lines []string
		name  string
		state string
	}
	var blocks []block
	var skillNote string
	for _, l := range lines {
		if strings.Contains(l, "`skill` means") {
			skillNote = l
			continue
		}
		if strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "   ") && len(blocks) >= 0 {
			name, rest, _ := strings.Cut(strings.TrimPrefix(l, "  "), " ")
			blocks = append(blocks, block{lines: []string{l}, name: name, state: strings.TrimLeft(rest, " ")})
			continue
		}
		if len(blocks) == 0 {
			blocks = append(blocks, block{})
		}
		blocks[len(blocks)-1].lines = append(blocks[len(blocks)-1].lines, l)
	}
	absent := doctorFileAbsent
	if section == "MCP wiring:" {
		absent = doctorMCPAbsent
	}
	var out []string
	folded, skills := 0, false
	for _, b := range blocks {
		if !all && b.name != "" && len(b.lines) == 1 && absent.MatchString(b.state) && !present[doctorAgentKey(b.name)] {
			folded++
			continue
		}
		if strings.HasPrefix(b.state, "skill ") {
			skills = true
		}
		out = append(out, b.lines...)
	}
	if skills && skillNote != "" {
		out = append(out, skillNote)
	}
	if folded > 0 {
		noun := "more agent"
		if len(out) == 0 {
			noun = "agent"
		}
		out = append(out, fmt.Sprintf("  %s not on this machine — `deja doctor --all` lists them", doctorCount(folded, noun)))
	}
	return out
}

// doctorIndexKeys are the keys the Index section prints, longest first where
// one is a prefix of another.
var doctorIndexKeys = []string{"exclusions", "integrity", "freshness", "last sync", "location", "security", "expiring", "status", "format", "ingest", "clock"}

// doctorIndexKey pads an Index row's key to the widest one, so the values
// start in one column.
func doctorIndexKey(line string) string {
	for _, k := range doctorIndexKeys {
		if rest, ok := strings.CutPrefix(line, "  "+k+" "); ok {
			return fmt.Sprintf("  %-10s %s", k, strings.TrimLeft(rest, " "))
		}
	}
	return line
}

// doctorWrap breaks a line longer than width at its last space that fits, and
// hangs the rest under the line's value column, so a wrapped row still reads
// as one row. The spacing inside the line is kept as written — that is the
// alignment. A word longer than the room left (a long path) stays whole.
func doctorWrap(line string, width int) []string {
	if width <= 0 || len([]rune(line)) <= width {
		return []string{line}
	}
	return wrapHanging(line, width, doctorHang(line))
}

// wrapHanging is doctorWrap with the continuation column given.
func wrapHanging(line string, width, hang int) []string {
	if width <= 0 || len([]rune(line)) <= width {
		return []string{line}
	}
	var out []string
	cur := []rune(line)
	pad := strings.Repeat(" ", hang)
	for len(cur) > width {
		// A space inside `...` is part of a command to copy, not a break.
		breakable := make([]bool, len(cur))
		quoted := false
		word := 0
		for i, r := range cur {
			if r == '`' {
				quoted = !quoted
			}
			breakable[i] = r == ' ' && !quoted && i > hang && cur[i-1] != ' ' && !inPath(cur, word, i)
			if r == ' ' {
				word = i + 1
			}
		}
		cut := -1
		for i := width; i > hang; i-- {
			if breakable[i] {
				cut = i
				break
			}
		}
		if cut < 0 {
			// Nothing fits: break at the next space past the width instead.
			for i := width; i < len(cur); i++ {
				if breakable[i] {
					cut = i
					break
				}
			}
		}
		if cut < 0 {
			break
		}
		out = append(out, strings.TrimRight(string(cur[:cut]), " "))
		rest := strings.TrimLeft(string(cur[cut:]), " ")
		if rest == "" {
			cur = nil
			break
		}
		cur = []rune(pad + rest)
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

// inPath reports whether the space at i sits inside a path, as in
// "~/Library/Application Support/...": the word before it starts a path and
// the one after it goes on with a slash.
func inPath(line []rune, word, i int) bool {
	// "a, b": the comma ends one path and the space starts the next.
	if word >= i || (line[word] != '~' && line[word] != '/') || line[i-1] == ',' {
		return false
	}
	for j := i + 1; j < len(line) && line[j] != ' '; j++ {
		if line[j] == '/' {
			return true
		}
	}
	return false
}

// doctorHang is the column a wrapped line continues at: where the value starts
// on a row with a key column ("  key    value"), else two in from the line's
// own indent.
func doctorHang(line string) int {
	indent := len(line) - len(strings.TrimLeft(line, " "))
	// A row under a twelve-wide name column, even when the name fills it.
	if indent == 2 && len(line) > 15 && line[14] == ' ' && line[15] != ' ' && !strings.Contains(strings.TrimRight(line[2:14], " "), " ") {
		return 15
	}
	body := line[indent:]
	if i := strings.Index(body, "  "); i > 0 && !strings.Contains(body[:i], " ") {
		j := i
		for j < len(body) && body[j] == ' ' {
			j++
		}
		if col := indent + j; col <= 28 {
			return col
		}
	}
	// An Index row after doctorIndexKey: key, one space, value.
	if indent == 2 && len(line) > 13 && line[12] == ' ' && line[13] != ' ' && strings.TrimSpace(line[2:12]) != "" {
		for _, k := range doctorIndexKeys {
			if strings.HasPrefix(line[2:], k+" ") {
				return 13
			}
		}
	}
	// A line that is already a continuation keeps its own column.
	if indent > 2 {
		return indent
	}
	return indent + 2
}

var doctorStateWords = []struct {
	word, colour string
}{
	{"config missing", statDim},
	{"not wired", statYellow},
	{"someone else's", statYellow},
	{"no adapter", statYellow},
	{"missing", statDim},
	{"unsupported", statDim},
	{"found", statGreen},
	{"wired", statGreen},
	{"written", statGreen},
	{"plugin", statGreen},
	{"installed", statGreen},
	{"skill", statGreen},
	{"ok", statGreen},
	{"stale", statYellow},
	{"broken", statYellow},
	{"unreadable", statYellow},
	{"denied", statYellow},
	{"untrusted", statYellow},
	{"unplugged", statYellow},
	{"excluded", statYellow},
	{"parsed-zero", statYellow},
	{"needs-sqlite3", statYellow},
	{"needs-zstd", statYellow},
}

// doctorColourState colours the state words of a row in the sections that
// have a state column. Only the stretch between the name and the path is
// looked at, so a path or a note that happens to hold one of the words keeps
// its own colour.
func doctorColourState(section, line string) string {
	switch section {
	case "Harness stores:", "Tools:", "MCP wiring:", "Commands:", "Hooks:":
	default:
		return line
	}
	if len(line) < 16 || !strings.HasPrefix(line, "  ") || line[2] == ' ' || line[14] != ' ' {
		return line
	}
	head, tail := line[:15], line[15:]
	end := len(tail)
	for _, stop := range []string{"~", "/", "(", "  "} {
		if i := strings.Index(tail, stop); i >= 0 && i < end {
			end = i
		}
	}
	// MCP rows carry two states: "config missing guidance missing".
	if section == "MCP wiring:" {
		if i := strings.Index(tail, "guidance "); i >= 0 {
			end = i + len("guidance ")
			for end < len(tail) && tail[end] != ' ' {
				end++
			}
		}
	}
	return head + doctorPaintWords(tail[:end]) + tail[end:]
}

func doctorPaintWords(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		matched := false
		for _, sw := range doctorStateWords {
			if strings.HasPrefix(s, sw.word) && (len(s) == len(sw.word) || s[len(sw.word)] == ' ') {
				b.WriteString(sw.colour + sw.word + statReset)
				s = s[len(sw.word):]
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		i := strings.IndexByte(s, ' ')
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i+1])
		s = s[i+1:]
		for len(s) > 0 && s[0] == ' ' {
			b.WriteByte(' ')
			s = s[1:]
		}
	}
	return b.String()
}
