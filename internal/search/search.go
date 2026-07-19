package search

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
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
	Query                     string
	Regex                     bool
	Harness, Project, Role    string
	Since                     time.Duration
	All, JSON, Fuzzy, Stemmed bool
	NoEmbed                   bool
	Semantic                  bool                `json:"-"`
	FuzzyVariants             map[string][]string `json:"-"`
	Tier                      string              `json:"-"`
}

const (
	TierExact    = "exact"
	TierClose    = "close"
	TierSemantic = "semantic"
)

type Hit struct {
	Session    model.Session `json:"session"`
	Count      int           `json:"count"`
	Snippets   []string      `json:"snippets"`
	Score      float64       `json:"score"`
	Tier       string        `json:"tier"`
	TierDetail string        `json:"tier_detail,omitempty"`
}

const (
	bm25K1        = 1.2
	bm25B         = 0.75
	userRoleBoost = 1.3
)

type bm25Document struct {
	hit       Hit
	termCount []int
	userCount []int
	length    int
}

func Run(ss []model.Session, o Options) ([]Hit, error) {
	var re *regexp.Regexp
	qlow := strings.ToLower(o.Query)
	qtoks, phrases := QueryParts(o.Query)
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
	merged := mergeSessions(ss)
	documents := make([]bm25Document, 0, len(merged))
	df := make([]int, len(qtoks))
	corpusDocuments := 0
	corpusLength := 0
	for _, s := range merged {
		if o.Harness != "" && s.Harness != o.Harness {
			continue
		}
		if o.Project != "" && !strings.Contains(strings.ToLower(s.Project), strings.ToLower(o.Project)) {
			continue
		}
		if !cut.IsZero() && s.Updated.Before(cut) {
			continue
		}
		tier := o.Tier
		if tier == "" {
			tier = TierExact
		}
		doc := bm25Document{hit: Hit{Session: s, Tier: tier}, termCount: make([]int, len(qtoks)), userCount: make([]int, len(qtoks))}
		if len(qtoks) == 0 {
			doc.termCount = []int{0}
			doc.userCount = []int{0}
		}
		for _, m := range s.Messages {
			if o.Role != "" && m.Role != o.Role {
				continue
			}
			c := 0
			if re != nil {
				c = len(re.FindAllStringIndex(m.Text, -1))
			} else {
				low := strings.ToLower(m.Text)
				if !MatchesParts(m.Text, qtoks, phrases, o.FuzzyVariants) {
					c = 0
				} else if len(qtoks) <= 1 && len(phrases) == 0 && o.FuzzyVariants == nil {
					if strings.Contains(low, qlow) {
						c = strings.Count(low, qlow)
					}
				} else {
					if o.FuzzyVariants != nil {
						c = countAllVariants(low, qtoks, o.FuzzyVariants)
					} else {
						c = countAllTokens(low, qtoks)
					}
					for _, phrase := range phrases {
						c += strings.Count(low, phrase)
					}
				}
			}
			if c > 0 {
				doc.hit.Count += c
				if doc.hit.Tier == TierClose && doc.hit.TierDetail == "" {
					doc.hit.TierDetail = variantDetail(m.Text, qtoks, o.FuzzyVariants)
				}
				if len(doc.hit.Snippets) < 3 {
					doc.hit.Snippets = append(doc.hit.Snippets, snippet(m.Text, o.Query, re))
				}
			}
			low := strings.ToLower(m.Text)
			doc.length += countDocumentWords(low, qtoks, o.FuzzyVariants, doc.termCount, doc.userCount, m.Role == "user")
			if len(qtoks) == 1 && doc.termCount[0] == 0 && strings.Contains(low, qlow) {
				n := strings.Count(low, qlow)
				doc.termCount[0] += n
				if m.Role == "user" {
					doc.userCount[0] += n
				}
			}
		}
		corpusDocuments++
		corpusLength += doc.length
		for i, n := range doc.termCount {
			if n > 0 {
				df[i]++
			}
		}
		if doc.hit.Count > 0 {
			documents = append(documents, doc)
		}
	}
	avgLength := 0.0
	if corpusDocuments > 0 {
		avgLength = float64(corpusLength) / float64(corpusDocuments)
	}
	hits := scoreBM25(documents, df, corpusDocuments, avgLength, len(qtoks) == 0)
	if !o.All && len(hits) > 15 {
		hits = hits[:15]
	}
	return hits, nil
}

func scoreBM25(documents []bm25Document, df []int, corpusDocuments int, avgLength float64, emptyQuery bool) []Hit {
	now := time.Now()
	hits := make([]Hit, 0, len(documents))
	for _, doc := range documents {
		score := 0.0
		for i, tf := range doc.termCount {
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (float64(corpusDocuments-df[i])+.5)/(float64(df[i])+.5))
			norm := 1 - bm25B
			if avgLength > 0 {
				norm += bm25B * float64(doc.length) / avgLength
			}
			term := idf * (float64(tf) * (bm25K1 + 1)) / (float64(tf) + bm25K1*norm)
			if doc.userCount[i] > 0 {
				term += idf * (float64(doc.userCount[i]) * (bm25K1 + 1)) / (float64(tf) + bm25K1*norm) * (userRoleBoost - 1)
			}
			score += term
		}
		if emptyQuery {
			score = float64(doc.hit.Count)
		}
		score *= freshnessDecay(doc.hit.Session.Updated, now)
		doc.hit.Score = score
		hits = append(hits, doc.hit)
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			if hits[i].Session.Updated.Equal(hits[j].Session.Updated) {
				return hits[i].Session.ID < hits[j].Session.ID
			}
			return hits[i].Session.Updated.After(hits[j].Session.Updated)
		}
		return hits[i].Score > hits[j].Score
	})
	return hits
}

func freshnessDecay(updated, now time.Time) float64 {
	if updated.IsZero() {
		return 0
	}
	age := now.Sub(updated).Hours() / 24
	if age <= 0 {
		return 1
	}
	return 1 / (1 + age)
}

func countDocumentWords(s string, terms []string, variants map[string][]string, counts, userCounts []int, user bool) int {
	length := 0
	start := -1
	for i := 0; i <= len(s); {
		isWord := false
		size := 0
		if i < len(s) {
			r, n := utf8.DecodeRuneInString(s[i:])
			size = n
			isWord = unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
		}
		if isWord && start < 0 {
			start = i
		} else if !isWord && start >= 0 {
			word := s[start:i]
			length++
			for j, term := range terms {
				matched := word == term
				if !matched {
					// A word matching a fuzzy/stem variant of this term counts
					// toward its term frequency so BM25 ranks the hit instead of
					// falling back to recency-only order.
					for _, v := range variants[term] {
						if word == v {
							matched = true
							break
						}
					}
				}
				if matched {
					counts[j]++
					if user {
						userCounts[j]++
					}
				}
			}
			start = -1
		}
		if i == len(s) {
			break
		}
		i += size
	}
	return length
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
	for i := range hits {
		if hits[i].Tier == "" {
			hits[i].Tier = TierExact
		}
	}
	if o.JSON {
		if o.Semantic {
			_ = json.NewEncoder(w).Encode(struct {
				Hits     []Hit `json:"hits"`
				Semantic bool  `json:"semantic"`
			}{hits, true})
		} else if o.Stemmed {
			_ = json.NewEncoder(w).Encode(struct {
				Hits     []Hit               `json:"hits"`
				Stemmed  bool                `json:"stemmed"`
				Variants map[string][]string `json:"variants,omitempty"`
			}{hits, true, o.FuzzyVariants})
		} else if o.Fuzzy {
			_ = json.NewEncoder(w).Encode(struct {
				Hits  []Hit `json:"hits"`
				Fuzzy bool  `json:"fuzzy"`
			}{hits, true})
		} else {
			_ = json.NewEncoder(w).Encode(hits)
		}
		return
	}
	color := colorOK(w)
	for _, h := range hits {
		d := "-"
		if !h.Session.Updated.IsZero() {
			d = relativeDate(h.Session.Updated)
		}
		if color {
			fmt.Fprintf(w, "%s%s %-10s %s %s %s %s %s%s%d matches%s%s\n", cBold, harnessTag(h.Session.Harness, true), h.Session.Project, cDim+"·"+cReset+cBold, d, cDim+"·"+cReset+cBold, short(h.Session.ID), cDim+"— "+cReset, cBold, h.Count, cReset, tierLabel(h))
		} else {
			fmt.Fprintf(w, "[%s] %-10s · %s · %s — %d matches%s\n", h.Session.Harness, h.Session.Project, d, short(h.Session.ID), h.Count, tierLabel(h))
		}
		for _, sn := range h.Snippets {
			fmt.Fprintf(w, "  %s\n", highlight(sn, o.Query, o.Regex, color))
		}
	}
}

func tierLabel(h Hit) string {
	if h.Tier == "" || h.Tier == TierExact {
		return ""
	}
	if h.TierDetail == "" {
		return " · " + h.Tier
	}
	return " · " + h.Tier + " (" + h.TierDetail + ")"
}

func variantDetail(text string, terms []string, variants map[string][]string) string {
	low := strings.ToLower(text)
	for _, term := range terms {
		for _, variant := range variants[term] {
			if strings.Contains(low, variant) {
				return term + "->" + variant
			}
		}
	}
	return ""
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
	terms, phrases := QueryParts(query)
	budget := 8000
	written := printContextChunks(w, s, budget, func(m model.Message) (bool, bool) {
		matched := qlow != "" && (strings.Contains(strings.ToLower(m.Text), qlow) || MatchesParts(m.Text, terms, phrases, nil))
		return matched || m.Role == "user", matched
	})
	if written > 0 {
		return
	}
	// The session can match with query terms spread across messages, so no
	// single message qualifies above; show an overview instead of a bare header.
	if qlow != "" {
		fmt.Fprintf(w, "\nNo single message contains the full query; showing the session's opening exchange.\n")
	}
	printContextChunks(w, s, budget, func(m model.Message) (bool, bool) { return true, false })
}

func printContextChunks(w io.Writer, s model.Session, budget int, include func(m model.Message) (ok, matched bool)) int {
	written := 0
	for _, m := range s.Messages {
		if written >= budget {
			break
		}
		ok, matched := include(m)
		if !ok {
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
	return written
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

// Snippet formats a message for search results, including semantic matches.
func Snippet(s, q string) string { return snippet(s, q, nil) }

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

func countAllVariants(low string, toks []string, variants map[string][]string) int {
	total := 0
	for _, tok := range toks {
		count := strings.Count(low, tok)
		for _, variant := range variants[tok] {
			count += strings.Count(low, variant)
		}
		if count == 0 {
			return 0
		}
		total += count
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
