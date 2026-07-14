package search

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

const (
	cReset  = "\x1b[0m"
	cDim    = "\x1b[2m"
	cBold   = "\x1b[1m"
	cOrange = "\x1b[38;5;208m"
	cGreen  = "\x1b[32m"
	cBlue   = "\x1b[34m"
	cMatch  = "\x1b[48;5;236;38;5;230m"
)

type Options struct {
	Query                  string
	Regex                  bool
	Harness, Project, Role string
	Since                  time.Duration
	All, JSON              bool
}
type Hit struct {
	Session  model.Session `json:"session"`
	Count    int           `json:"count"`
	Snippets []string      `json:"snippets"`
	Score    float64       `json:"score"`
}

func Run(ss []model.Session, o Options) ([]Hit, error) {
	var re *regexp.Regexp
	qlow := strings.ToLower(o.Query)
	qtoks := queryTokens(o.Query)
	if o.Regex {
		var err error
		re, err = regexp.Compile("(?i)" + o.Query)
		if err != nil {
			return nil, err
		}
	}
	cut := time.Time{}
	if o.Since > 0 {
		cut = time.Now().Add(-o.Since)
	}
	var hits []Hit
	for _, s := range mergeSessions(ss) {
		if o.Harness != "" && s.Harness != o.Harness {
			continue
		}
		if o.Project != "" && !strings.Contains(strings.ToLower(s.Project), strings.ToLower(o.Project)) {
			continue
		}
		if !cut.IsZero() && s.Updated.Before(cut) {
			continue
		}
		h := Hit{Session: s}
		for _, m := range s.Messages {
			if o.Role != "" && m.Role != o.Role {
				continue
			}
			c := 0
			if re != nil {
				c = len(re.FindAllStringIndex(m.Text, -1))
			} else {
				low := strings.ToLower(m.Text)
				if len(qtoks) <= 1 {
					if strings.Contains(low, qlow) {
						c = strings.Count(low, qlow)
					}
				} else {
					c = countAllTokens(low, qtoks)
				}
			}
			if c > 0 {
				h.Count += c
				if len(h.Snippets) < 3 {
					h.Snippets = append(h.Snippets, snippet(m.Text, o.Query, re))
				}
			}
		}
		if h.Count > 0 {
			age := time.Since(s.Updated).Hours() / 24
			h.Score = float64(h.Count) * 1000 / (1 + age)
			hits = append(hits, h)
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].Session.Updated.After(hits[j].Session.Updated)
		}
		return hits[i].Score > hits[j].Score
	})
	if !o.All && len(hits) > 15 {
		hits = hits[:15]
	}
	return hits, nil
}

func mergeSessions(in []model.Session) []model.Session {
	by := map[string]*model.Session{}
	for _, s := range in {
		k := s.Harness + ":" + s.ID
		if by[k] == nil {
			cp := s
			by[k] = &cp
		} else {
			by[k].Messages = append(by[k].Messages, s.Messages...)
			by[k].Touch(s.Updated)
			if by[k].Project == "history" {
				by[k].Project = s.Project
			}
		}
	}
	out := make([]model.Session, 0, len(by))
	for _, s := range by {
		out = append(out, *s)
	}
	return out
}

func Print(w io.Writer, hits []Hit, o Options) {
	if o.JSON {
		_ = json.NewEncoder(w).Encode(hits)
		return
	}
	color := colorOK(w)
	for _, h := range hits {
		d := "-"
		if !h.Session.Updated.IsZero() {
			d = relativeDate(h.Session.Updated)
		}
		if color {
			fmt.Fprintf(w, "%s%s %-10s %s %s %s %s %s%s%d matches%s\n", cBold, harnessTag(h.Session.Harness, true), h.Session.Project, cDim+"·"+cReset+cBold, d, cDim+"·"+cReset+cBold, short(h.Session.ID), cDim+"— "+cReset, cBold, h.Count, cReset)
		} else {
			fmt.Fprintf(w, "[%s] %-10s · %s · %s — %d matches\n", h.Session.Harness, h.Session.Project, d, short(h.Session.ID), h.Count)
		}
		for _, sn := range h.Snippets {
			fmt.Fprintf(w, "  %s\n", highlight(sn, o.Query, o.Regex, color))
		}
	}
}

func FindByPrefix(ss []model.Session, p string) (model.Session, bool) {
	for _, s := range mergeSessions(ss) {
		if strings.HasPrefix(s.ID, p) {
			return s, true
		}
	}
	return model.Session{}, false
}

func PrintSession(w io.Writer, s model.Session) {
	fmt.Fprintf(w, "# %s · %s · %s\n", s.Harness, s.Project, s.ID)
	for _, m := range s.Messages {
		txt := collapseTool(m.Text)
		if strings.TrimSpace(txt) == "" {
			continue
		}
		t := ""
		if !m.Time.IsZero() {
			t = m.Time.Format("2006-01-02 15:04") + " "
		}
		fmt.Fprintf(w, "\n%s%s:\n%s\n", t, m.Role, txt)
	}
}

func PrintContext(w io.Writer, s model.Session, query string) {
	fmt.Fprintf(w, "# deja context: %s · %s · %s", s.Harness, s.Project, s.ID)
	if !s.Updated.IsZero() {
		fmt.Fprintf(w, " · updated %s", s.Updated.Format("2006-01-02"))
	}
	fmt.Fprintln(w)
	qlow := strings.ToLower(query)
	budget := 8000
	written := 0
	for _, m := range s.Messages {
		if written >= budget {
			break
		}
		matched := qlow != "" && strings.Contains(strings.ToLower(m.Text), qlow)
		if !matched && m.Role != "user" {
			continue
		}
		text := contextText(m.Text, matched)
		if strings.TrimSpace(text) == "" {
			continue
		}
		chunk := fmt.Sprintf("\n## %s\n\n%s\n", m.Role, text)
		if written+len(chunk) > budget {
			cut := max(0, budget-written)
			for cut > 0 && !utf8.RuneStart(chunk[cut]) {
				cut--
			}
			chunk = chunk[:cut]
		}
		fmt.Fprint(w, chunk)
		written += len(chunk)
	}
}

func AutoRecallDigest(ss []model.Session, budget int) string {
	if budget <= 0 {
		budget = 2000
	}
	var b strings.Builder
	for _, s := range ss {
		if b.Len() >= budget {
			break
		}
		section := autoRecallSession(s)
		if section == "" {
			continue
		}
		if b.Len()+len(section) > budget {
			cut := budget - b.Len()
			for cut > 0 && !utf8.RuneStart(section[cut]) {
				cut--
			}
			section = section[:cut]
		}
		b.WriteString(section)
	}
	return strings.TrimSpace(b.String())
}

func autoRecallSession(s model.Session) string {
	var problem string
	var conclusions []string
	for _, m := range s.Messages {
		text := contextText(m.Text, false)
		if strings.TrimSpace(text) == "" {
			continue
		}
		switch m.Role {
		case "user":
			if problem == "" && !noiseMessage(m.Text) {
				problem = firstLine(text, 160)
			}
		case "assistant":
			if len(conclusions) < 2 {
				conclusions = append(conclusions, firstLine(text, 220))
			}
		}
	}
	if problem == "" && len(conclusions) == 0 {
		return ""
	}
	date := ""
	if !s.Updated.IsZero() {
		date = " · " + s.Updated.Format("2006-01-02")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "- **%s** `%s`%s\n", s.Project, short(s.ID), date)
	if problem != "" {
		fmt.Fprintf(&b, "  - User: %s\n", problem)
	}
	for _, c := range conclusions {
		fmt.Fprintf(&b, "  - Assistant: %s\n", c)
	}
	return b.String()
}

func noiseMessage(s string) bool {
	t := strings.TrimSpace(s)
	for _, p := range []string{"<local-command", "<command-", "<task-notification", "<teammate-message", "<bash-", "Caveat:", "<system-reminder"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

func firstLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

func Recent(ss []model.Session, n int) []model.Session {
	out := mergeSessions(ss)
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

func snippet(s, q string, re *regexp.Regexp) string {
	s = proseForSnippet(s)
	r := []rune(s)
	idx := 0
	if re != nil {
		loc := re.FindStringIndex(s)
		if loc != nil {
			idx = utf8.RuneCountInString(s[:loc[0]])
		}
	} else {
		low := strings.ToLower(s)
		b := strings.Index(low, strings.ToLower(q))
		if b < 0 {
			for _, tok := range queryTokens(q) {
				if p := strings.Index(low, tok); p >= 0 && (b < 0 || p < b) {
					b = p
				}
			}
		}
		if b > 0 {
			idx = utf8.RuneCountInString(s[:b])
		}
	}
	start := idx - 70
	if start < 0 {
		start = 0
	}
	end := start + 180
	if end > len(r) {
		end = len(r)
	}
	out := strings.TrimSpace(string(r[start:end]))
	out = strings.Trim(out, " ,.;:-\n\t")
	if start > 0 {
		out = "… " + out
	}
	if end < len(r) {
		out += " …"
	}
	return out
}

func queryTokens(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tok := range strings.Fields(strings.ToLower(s)) {
		tok = strings.Trim(tok, "\t\n\r .,;:!?()[]{}<>\"'`")
		if len(tok) < 2 || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

func countAllTokens(low string, toks []string) int {
	total := 0
	for _, tok := range toks {
		c := strings.Count(low, tok)
		if c == 0 {
			return 0
		}
		total += c
	}
	return total
}
func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
func highlight(s, q string, isRe bool, color bool) string {
	if !color {
		return s
	}
	if isRe {
		re, err := regexp.Compile("(?i)" + q)
		if err == nil {
			return re.ReplaceAllStringFunc(s, func(x string) string { return cMatch + x + cReset })
		}
	}
	if strings.Contains(strings.ToLower(s), strings.ToLower(q)) {
		return regexp.MustCompile(`(?i)`+regexp.QuoteMeta(q)).ReplaceAllStringFunc(s, func(x string) string { return cMatch + x + cReset })
	}
	toks := queryTokens(q)
	if len(toks) == 0 {
		return s
	}
	parts := make([]string, 0, len(toks))
	for _, t := range toks {
		parts = append(parts, regexp.QuoteMeta(t))
	}
	return regexp.MustCompile(`(?i)(`+strings.Join(parts, "|")+`)`).ReplaceAllStringFunc(s, func(x string) string { return cMatch + x + cReset })
}

func colorOK(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

func harnessTag(h string, color bool) string {
	tag := "[" + h + "]"
	if !color {
		return tag
	}
	switch h {
	case "claude":
		return cOrange + tag + cReset + cBold
	case "codex":
		return cGreen + tag + cReset + cBold
	case "opencode":
		return cBlue + tag + cReset + cBold
	}
	return tag
}

func relativeDate(t time.Time) string {
	now := time.Now()
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.Date()
	today := time.Date(y1, m1, d1, 0, 0, 0, 0, now.Location())
	day := time.Date(y2, m2, d2, 0, 0, 0, 0, now.Location())
	days := int(today.Sub(day).Hours() / 24)
	if days == 0 {
		return "today"
	}
	if days > 0 && days < 7 {
		return fmt.Sprintf("%dd ago", days)
	}
	if y1 == y2 {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 2006")
}
func collapseTool(s string) string {
	if strings.Contains(s, "tool_use") || strings.Contains(s, "tool_result") || strings.Contains(s, "<local-command") {
		if utf8.RuneCountInString(s) > 400 {
			return "[tool/local output collapsed]"
		}
	}
	return s
}

var (
	ansiRE       = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	lineNumberRE = regexp.MustCompile(`^\s*\d{1,5}[:|]\s+`)
	toolDumpRE   = regexp.MustCompile(`(?i)(tool_use|tool_result|<local-command|netcat|npm ERR!|panic:|goroutine \d+)`)
)

func proseForSnippet(s string) string {
	s = ansiRE.ReplaceAllString(s, "")
	var keep []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || lineNumberRE.MatchString(line) || toolDumpRE.MatchString(line) {
			continue
		}
		keep = append(keep, line)
	}
	out := strings.Join(keep, " ")
	out = strings.Join(strings.Fields(out), " ")
	if out == "" {
		out = strings.Join(strings.Fields(ansiRE.ReplaceAllString(s, "")), " ")
	}
	return out
}

func contextText(s string, matched bool) string {
	s = ansiRE.ReplaceAllString(s, "")
	if strings.Contains(s, "```") {
		return strings.TrimSpace(s)
	}
	if matched {
		return proseForSnippet(s)
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 8 {
		lines = lines[:8]
	}
	return proseForSnippet(strings.Join(lines, "\n"))
}
