// Command denialrate measures how often an agent says something does not
// exist while the index could have answered.
//
// The claim behind #4004 and #4005 is that agents deny things that are sitting
// in the store. This counts it. For every assistant turn that asserts
// non-existence, the user turn in front of it is the question that turn was
// answering, so it becomes a recall query; hits from sessions that ended after
// the denial are dropped, because answering with work that had not happened yet
// proves nothing. A hit counts when the text deja would show carries one of the
// question's identifying words — the same containment rule `deja bench context`
// scores coverage with, rather than a second definition of "relevant".
//
// The control is the part that makes the rate mean anything: the same pipeline
// over assistant turns that deny nothing. If recall "would have hit" as often
// there, the number is measuring the index's tendency to return something.
//
//	go run ./scripts/denialrate -index /tmp/deja-copy [-limit 200] [-json]
//
// It prints counts only. The corpus is somebody's own history: -quote exists
// for reading a handful of cases locally and its output goes nowhere near a
// commit.
//
// The answer, on 2817 sessions of one machine's real history — 457 denying
// turns against 42008 that deny nothing — is that none of the three rules
// separates them:
//
//	-judge denial    50.6% against a control of 50.8%
//	-judge question  68.5% against 72.5% — the control is higher
//	-judge rare      88.7% against 81.8%, with both arms saturated
//
// The strict rule, where the denying sentence has to name the thing it denies,
// is flat: an agent saying something is not there is no likelier to have the
// answer in the store than an agent saying anything else. It also leaves half
// the denials unjudgable, because half of them name nothing — "I don't see
// any" with the object two sentences earlier. Loosening the rule to the whole
// question buys judgability and loses the gap. So #4006 is a negative: the
// denial rate is not measurable this way, and the wording rules in #4005 ship
// on cost, not on a number this driver could produce.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/prompt"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// denials are the sentences an agent writes when it reports absence. They are
// matched against prose with code fences removed: "no such file or directory"
// inside pasted tool output is the shell talking, not the agent.
var denials = []*regexp.Regexp{
	regexp.MustCompile(`(?i)there (?:is|are) no (?:such )?\w`),
	regexp.MustCompile(`(?i)there'?s no (?:such )?\w`),
	regexp.MustCompile(`(?i)no such (?:command|file|flag|option|setting|function|method|tool)`),
	regexp.MustCompile(`(?i)does not (?:exist|appear to exist)`),
	regexp.MustCompile(`(?i)doesn'?t (?:exist|appear to exist)`),
	regexp.MustCompile(`(?i)i (?:don'?t|do not) see (?:any|a|an)\b`),
	regexp.MustCompile(`(?i)i (?:couldn'?t|could not|cannot|can'?t) find`),
	regexp.MustCompile(`(?i)(?:nothing|none) (?:of that|like that) (?:exists|is here)`),
	regexp.MustCompile(`(?i)(?:не существует|нет такой|нет такого|такой команды нет|такого файла нет)`),
	regexp.MustCompile(`(?i)(?:не нашёл|не нашел|ничего не нашёл|ничего не нашел|не вижу)`),
}

var fence = regexp.MustCompile("(?s)```.*?```")

// sentence ends at a stop or a line break. The denied name has to be read out
// of the sentence that denies something, not out of the turn: the first
// backticked span of a long answer is usually a file it just edited, and
// scoring that produced entities like "thesis.tex:76" and "57b686c".
var sentenceEnd = regexp.MustCompile(`[.!?\n]+`)

func sentences(prose string) []string {
	parts := sentenceEnd.Split(prose, -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// denialSentences are the sentences of a turn that report absence.
func denialSentences(prose string) []string {
	var out []string
	for _, sent := range sentences(prose) {
		if isDenial(sent) {
			out = append(out, sent)
		}
	}
	return out
}

type verdict struct {
	arm      string
	entity   string
	from     string
	query    string
	hit      bool
	tier     string
	why      string
	session  string
	harness  string
	whenTurn time.Time
}

func main() {
	dir := flag.String("index", "", "index directory to query (required; a copy, not the live one)")
	allowLive := flag.Bool("allow-live", false, "permit running against the default index directory")
	limit := flag.Int("limit", 200, "how many denials to examine (0 = all)")
	control := flag.Int("control", 200, "how many non-denial turns to run as the control (0 = off)")
	judgeRule := flag.String("judge", "denial", "where the denied name comes from: denial (the denying sentence names it), question (the antecedent's naming words), rare (the antecedent's rarest word, and only when the store itself calls it rare)")
	quote := flag.Bool("quote", false, "print the query and the matched line for each case — local reading only")
	asJSON := flag.Bool("json", false, "print the result as JSON")
	seed := flag.Int64("seed", 7, "sampling seed")
	flag.Parse()

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "denialrate: -index is required")
		os.Exit(2)
	}
	if !*allowLive {
		if abs, err := filepath.Abs(*dir); err == nil && abs == index.DefaultDir() {
			fmt.Fprintln(os.Stderr, "denialrate: that is the live index; copy it and point -index at the copy, or pass -allow-live")
			os.Exit(2)
		}
	}

	ss := loadCorpus()
	if len(ss) == 0 {
		fmt.Fprintln(os.Stderr, "denialrate: no sessions read — check the DEJA_*_ROOT variables")
		os.Exit(1)
	}

	cases, controls := collect(ss)
	population := map[string]int{"denial": len(cases), "control": len(controls)}
	rng := rand.New(rand.NewSource(*seed))
	cases = sample(rng, cases, *limit)
	controls = sample(rng, controls, *control)

	verdicts := make([]verdict, 0, len(cases)+len(controls))
	for _, c := range cases {
		verdicts = append(verdicts, judge(*dir, c, "denial", *judgeRule))
	}
	for _, c := range controls {
		verdicts = append(verdicts, judge(*dir, c, "control", *judgeRule))
	}
	report(verdicts, len(ss), population, *quote, *asJSON)
}

// turn is one assistant message and the user message it was answering.
type turn struct {
	session    model.Session
	antecedent string
	when       time.Time
	text       string
}

func loadCorpus() []model.Session {
	var ss []model.Session
	ss = append(ss, sources.LoadClaude()...)
	ss = append(ss, sources.LoadCodex()...)
	ss = append(ss, sources.LoadOpencode()...)
	return ss
}

// collect pairs every assistant turn with the user turn in front of it, and
// splits them on whether the assistant reported absence.
func collect(ss []model.Session) (cases, controls []turn) {
	for _, s := range ss {
		last := ""
		for _, m := range s.Messages {
			text := digest.MessageText(m.Text)
			if m.Role == "user" {
				// A bracketed harness note — "[Request interrupted by user]" —
				// is not a question the next turn answers, and it became the
				// query for four of the first fourteen cases read.
				bracketed := strings.HasPrefix(strings.TrimSpace(text), "[")
				if !digest.IsAgentArtifact(m.Text) && !bracketed && len(strings.Fields(text)) >= 3 {
					last = text
				}
				continue
			}
			if m.Role != "assistant" || last == "" {
				continue
			}
			prose := fence.ReplaceAllString(text, " ")
			if len(strings.Fields(prose)) < 3 {
				continue
			}
			t := turn{session: s, antecedent: last, when: m.Time, text: prose}
			if t.when.IsZero() {
				// Without a stamp there is no "older than this turn", and the
				// age restriction is the whole honesty of the measurement.
				continue
			}
			if isDenial(prose) {
				cases = append(cases, t)
			} else {
				controls = append(controls, t)
			}
		}
	}
	return cases, controls
}

func isDenial(prose string) bool {
	for _, re := range denials {
		if re.MatchString(prose) {
			return true
		}
	}
	return false
}

func sample(rng *rand.Rand, in []turn, n int) []turn {
	if n <= 0 || n >= len(in) {
		if n == 0 {
			return nil
		}
		return in
	}
	rng.Shuffle(len(in), func(i, j int) { in[i], in[j] = in[j], in[i] })
	return in[:n]
}

// judge runs the recall the agent did not run, as of that turn.
//
// What is judged is the thing the sentence denied, not the words of the
// conversation around it. Reading the question's rarest word instead — the
// first version of this — scored on "нем", "долго" and "yahoo": a real token
// of a real session, and nothing to do with what the agent said was missing.
func judge(dir string, t turn, arm, rule string) verdict {
	v := verdict{arm: arm, query: t.antecedent, session: t.session.ID, harness: t.session.Harness, whenTurn: t.when}
	entity, from := deniedEntity(t, rule)
	if rule == "rare" {
		entity, from = rareQuestionTerm(dir, t)
	}
	v.from = from
	if entity == "" {
		v.why = "nothing named to check"
		return v
	}
	v.entity = entity
	res, err := index.SearchWithRecoveryDetailed(dir, query.Options{
		Query:           entity,
		All:             true,
		Limit:           10,
		ExcludeSessions: map[string]bool{t.session.ID: true},
		Now:             t.when,
	}, nil)
	if err != nil {
		v.why = "search failed: " + err.Error()
		return v
	}
	v.tier = res.Tier
	for _, h := range res.Sessions {
		if !h.Updated.Before(t.when) {
			// Work that had not happened yet is not an answer the agent missed.
			continue
		}
		if strings.Contains(strings.ToLower(shownText(h)), strings.ToLower(entity)) {
			v.hit = true
			v.why = "an older session says it"
			return v
		}
	}
	v.why = "no session older than the turn says it"
	return v
}

// rareIDFFloor is how rare a word has to be before this counts it as the
// thing the turn was about. It is the ranking's own strong-term floor: below
// it a word is ordinary, and an ordinary word appears in an older session
// whatever the turn said — which is how the first version of this measurement
// scored 31.6% on words like "нем" and "долго".
const rareIDFFloor = 3.0

// rareQuestionTerm is the antecedent's rarest word that the store itself
// weighs as identifying. Same rule in both arms, so the control measures the
// same thing.
func rareQuestionTerm(dir string, t turn) (string, string) {
	terms := identifying(prompt.Terms(t.antecedent))
	if len(terms) == 0 {
		return "", "question"
	}
	res, err := index.SearchWithRecoveryDetailed(dir, query.Options{
		Query:           t.antecedent,
		All:             true,
		Limit:           1,
		ExcludeSessions: map[string]bool{t.session.ID: true},
		Now:             t.when,
	}, nil)
	if err != nil {
		return "", "question"
	}
	best, bestIDF := "", rareIDFFloor
	for _, term := range terms {
		if w, ok := res.TermIDF[term]; ok && w > bestIDF {
			best, bestIDF = term, w
		}
	}
	return best, "question"
}

// named is what a denial sentence points at: a backticked or quoted name, or
// the words right after "no such" / "there is no". Those are the shapes a
// denial takes when it names anything at all — and when it names nothing
// ("there is no such command"), the entity is in the turn before it.
var named = []*regexp.Regexp{
	regexp.MustCompile("`([^`\n]{2,60})`"),
	regexp.MustCompile(`"([^"\n]{2,60})"`),
	regexp.MustCompile(`'([^'\n]{2,60})'`),
	regexp.MustCompile(`(?i)no such ([\p{L}\p{N}_./-]{2,40}(?: [\p{L}\p{N}_./-]{2,40})?)`),
	regexp.MustCompile(`(?i)there (?:is|are) no ([\p{L}\p{N}_./-]{2,40}(?: [\p{L}\p{N}_./-]{2,40})?)`),
	regexp.MustCompile(`(?i)(?:нет такой|нет такого|не нашёл|не нашел) ([\p{L}\p{N}_./-]{2,40}(?: [\p{L}\p{N}_./-]{2,40})?)`),
}

// deniedEntity picks the name the denial itself carries. Under rule "question"
// it falls back to the antecedent's own naming words, which is the weaker
// extractor: it is reported as its own subset rather than mixed in.
func deniedEntity(t turn, rule string) (entity, from string) {
	// The sentence that denies, for a denial; for a control turn there is no
	// such sentence, so every sentence is a candidate and the extractor is the
	// same one. That keeps the two arms comparable: a name this turn points at.
	cands := denialSentences(t.text)
	if len(cands) == 0 {
		cands = sentences(t.text)
	}
	for _, sent := range cands {
		for _, re := range named {
			for _, m := range re.FindAllStringSubmatch(sent, -1) {
				cand := strings.TrimSpace(m[1])
				// A shell line pasted inside the sentence is the shell denying,
				// not the agent: "manifest.gob: no such file or directory".
				if strings.Contains(strings.ToLower(cand), "no such file") {
					continue
				}
				if generic(cand) || len(strings.Fields(cand)) > 3 {
					continue
				}
				if search.HasIdentifierTerm(strings.Fields(cand)) {
					return cand, "denial"
				}
			}
		}
	}
	if rule != "question" {
		return "", "denial"
	}
	terms := identifying(prompt.Terms(t.antecedent))
	if len(terms) == 0 {
		return "", "question"
	}
	return terms[0], "question"
}

// generic drops the words a denial uses to say "a thing of this kind" rather
// than to name one: "no such command" denies a command whose name is in the
// question, and scoring the word "command" would count every session that
// mentions one.
var genericWords = map[string]bool{
	"command": true, "commands": true, "file": true, "files": true, "flag": true,
	"option": true, "setting": true, "settings": true, "function": true, "method": true,
	"directory": true, "folder": true, "field": true, "tool": true, "script": true,
	"variable": true, "test": true, "tests": true, "error": true, "errors": true,
	"команды": true, "команда": true, "файл": true, "файла": true, "флаг": true,
	"папки": true, "папка": true, "функция": true, "функции": true, "настройки": true,
}

func generic(s string) bool {
	fields := strings.Fields(strings.ToLower(s))
	if len(fields) == 0 {
		return true
	}
	for _, f := range fields {
		if !genericWords[strings.Trim(f, ".,:;()")] {
			return false
		}
	}
	return true
}

// identifying keeps the words of the question that name something, by the rule
// the recall gate already uses. A denial about "that" cannot be judged.
func identifying(terms []string) []string {
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		if search.HasIdentifierTerm([]string{t}) {
			out = append(out, strings.ToLower(t))
		}
	}
	return out
}

// shownText is what the reader would see of a hit: the passages that matched,
// not the session. Judging against the whole transcript would count a hit deja
// never displays.
func shownText(s model.Session) string {
	var b strings.Builder
	for _, m := range s.Messages {
		b.WriteString(digest.MessageText(m.Text))
		b.WriteByte('\n')
	}
	b.WriteString(s.Title)
	return b.String()
}

type armResult struct {
	Population int     `json:"population"`
	Examined   int     `json:"examined"`
	Judgable   int     `json:"judgable"`
	Hits       int     `json:"would_have_hit"`
	Rate       float64 `json:"rate"`
}

type result struct {
	Sessions int                  `json:"sessions_read"`
	Arms     map[string]armResult `json:"arms"`
}

func report(vs []verdict, sessions int, population map[string]int, quote, asJSON bool) {
	out := result{Sessions: sessions, Arms: map[string]armResult{}}
	for _, arm := range []string{"denial", "control"} {
		r := armResult{Population: population[arm]}
		for _, v := range vs {
			if v.arm != arm {
				continue
			}
			r.Examined++
			if v.entity == "" {
				continue
			}
			r.Judgable++
			if v.hit {
				r.Hits++
			}
		}
		if r.Judgable > 0 {
			r.Rate = float64(r.Hits) / float64(r.Judgable)
		}
		if r.Examined > 0 {
			out.Arms[arm] = r
		}
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return
	}
	fmt.Printf("sessions read: %d\n\n", sessions)
	fmt.Printf("%-9s %11s %9s %9s %14s %7s\n", "arm", "in corpus", "examined", "judgable", "would have hit", "rate")
	arms := make([]string, 0, len(out.Arms))
	for arm := range out.Arms {
		arms = append(arms, arm)
	}
	sort.Strings(arms)
	for _, arm := range arms {
		r := out.Arms[arm]
		fmt.Printf("%-9s %11d %9d %9d %14d %6.1f%%\n", arm, r.Population, r.Examined, r.Judgable, r.Hits, 100*r.Rate)
	}
	if quote {
		fmt.Println("\n-- cases (local reading only)")
		for _, v := range vs {
			if v.arm != "denial" {
				continue
			}
			fmt.Printf("[%s] hit=%v from=%s entity=%q tier=%s — %s\n  q: %s\n", v.harness, v.hit, v.from, v.entity, v.tier, v.why, oneLine(v.query, 140))
		}
	}
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > n {
		s = string([]rune(s)[:n]) + "…"
	}
	return s
}
