package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// `deja rules candidates` lists the turns where the user corrected an agent, for
// the user's own agent to group into standing rules. deja does not group them
// itself: the same rule comes back in different words each time ("dig deeper",
// "search wider", "don't settle"), and three lexical groupings over one
// machine's 236 candidates put 0 real rules in their top 10. A frontier model
// given the same list blind put 10 real rules in its top 10 and found 6 of the
// 7 a person had grouped by hand, citing real candidates (#4086 follow-up). So
// the detector ships here and the grouping is the skill's job.

// rulesSkillSection is appended to both skills. The grouping instruction is
// the one the measurement gave the model; the stop before writing is the point,
// because a wrong rule would then sit in every agent's context in every session.
const rulesSkillSection = `## Rules the user keeps repeating

When the user asks you to find or suggest their standing rules — the things they keep having to tell their agents:

1. Run ` + "`deja rules candidates`" + ` (` + "`--since 90d`" + ` for recent ones). It lists turns where the user corrected an agent, across every tool on this machine, each numbered ` + "`#n`" + ` with the session it came from. It writes nothing.
2. Find the standing rules in that list: preferences the user would want applied in every future session, stated in at least two different sessions. Ignore one-off corrections about a specific task. For each rule give one imperative sentence, the ` + "`#n`" + ` that state it, and how many distinct sessions. At most 15, most recurring first.
3. Show the list and stop. Write nothing until the user picks. Then show the exact lines you will append to the rules file ` + "`deja rules`" + ` names, append only those, and run ` + "`deja rules sync`" + `, which copies the file into every installed agent's global rules file.

This takes a strong model: on one machine's 236 candidates a frontier model found the user's recurring rules with real citations, and a 9B local model invented candidate numbers. On a small model, say so rather than guess.`

// correctionRU and correctionEN are the markers the measurement ran with. A
// match only makes a turn a candidate: of 60 hand-labelled ones, 23% were a
// standing rule, which is why nothing here is ever applied without the user.
var (
	correctionRU = `(^|\s)(нет[,.!\s]|нет$|не так|я же (говорил|просил|сказал|писал)|не надо|зачем ты|^стоп|стоп[,.!]|ты (опять|снова)|неправильно|я не просил|не нужно|никогда не|больше не (делай|пиши|надо)|перестань|хватит|сколько раз|не то[,.!\s]|почему ты|не делай)`
	correctionEN = `(^no[,.!\s]|\bwrong\b|\bdon'?t\b|\bi said\b|\bi told you\b|\binstead\b|not what i|\bwhy did you\b|^stop\b|\bnever\b|you should(n'?t| not)|\bi asked\b)`
	correctionRe = regexp.MustCompile(`(?i)` + correctionRU + `|` + correctionEN)
	// pseudoUserRe is text a harness files under the user's role without the
	// user having typed it: system reminders and other tagged blocks, skill
	// bodies, compaction summaries, interrupt notices, hook output.
	pseudoUserRe = regexp.MustCompile(`(?i)^\s*(<|\[Request interrupted|Caveat:|This session is being continued|Base directory for this skill|Stop hook|\[SYSTEM|# |You are |Your directive|Continue the conversation|Summary:)`)
)

const (
	// A correction is said near the start of a turn; a long paste that
	// happens to contain "don't" further down is not one.
	candidateScanRunes = 400
	candidateMaxRunes  = 1500
	candidateQuote     = 300
	candidateBefore    = 150
	// Fork copies of one turn: harnesses that fork a session re-file its
	// history, and one correction must not count as two sessions agreeing.
	candidateDedupRunes = 200
)

type ruleCandidate struct {
	N           int    `json:"n"`
	Harness     string `json:"harness"`
	Date        string `json:"date"`
	Session     string `json:"session"`
	SessionID   string `json:"session_id"`
	Project     string `json:"project,omitempty"`
	Correction  string `json:"correction"`
	AgentBefore string `json:"agent_before"`
	time        time.Time
}

func runRulesCandidates(dir string, w io.Writer, args []string) error {
	asJSON := false
	limit := 0
	var since time.Duration
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--limit":
			if i+1 >= len(args) {
				return fmt.Errorf("rules candidates: --limit needs value")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return fmt.Errorf("rules candidates: --limit wants a positive number, got %q", args[i])
			}
			limit = n
		case "--since":
			if i+1 >= len(args) {
				return fmt.Errorf("rules candidates: --since needs value")
			}
			i++
			d, err := parseDur(args[i])
			if err != nil {
				return err
			}
			since = d
		default:
			return unknownFlag("rules candidates", args[i], []string{"--json", "--limit", "--since"})
		}
	}
	if err := index.Ensure(dir, "", false, os.Stderr); err != nil {
		return ensureError(dir, err)
	}
	cands, err := collectRuleCandidates(dir, since, limit)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		if cands == nil {
			cands = []ruleCandidate{}
		}
		return enc.Encode(cands)
	}
	if len(cands) == 0 {
		if line, ok := emptyStoreLine(dir, "no corrections found"); ok {
			fmt.Fprintln(w, line)
			return nil
		}
		// Which window was searched, so an empty answer is not read as "you
		// never corrected an agent".
		if since > 0 {
			fmt.Fprintln(w, fitLine(w, fmt.Sprintf("no corrections found in the last %s%s", sinceArg(args), newestSessionNote(dir, "rules candidates", since))))
			return nil
		}
		n, _ := index.SessionCount(dir)
		fmt.Fprintf(w, "no corrections found in the %d indexed session%s\n", n, pluralS(n))
		return nil
	}
	for i, c := range cands {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "#%d %s %s deja:%s\nuser: %s\nbefore it, the agent: %s\n", c.N, c.Harness, c.Date, c.Session, c.Correction, c.AgentBefore)
	}
	return nil
}

// collectRuleCandidates walks every session's user, assistant, command and edit
// records in the order they alternated and keeps a user turn that corrects what
// the agent just did. since and limit of zero mean everything; limit keeps the
// newest.
func collectRuleCandidates(dir string, since time.Duration, limit int) ([]ruleCandidate, error) {
	type state struct {
		skip                bool
		seenUser, afterTurn bool
		lastAgent           string
	}
	pol := policy.Load()
	states := map[string]*state{}
	var cutoff time.Time
	if since > 0 {
		cutoff = time.Now().Add(-since)
	}
	var out []ruleCandidate
	err := index.EachRecordInRoles(dir, []string{"user", "assistant", "command", index.RoleEdit}, func(meta index.SessionMeta, r index.Record) {
		st := states[r.Key]
		if st == nil {
			st = &state{skip: !candidateSession(meta, pol)}
			states[r.Key] = st
		}
		if st.skip {
			return
		}
		if r.Role != "user" {
			st.afterTurn = true
			if r.Role == "assistant" {
				st.lastAgent = r.Text
			} else {
				st.lastAgent = r.Role + ": " + r.Text
			}
			return
		}
		x := strings.TrimSpace(r.Text)
		if x == "" || strings.HasPrefix(x, "!") || pseudoUserRe.MatchString(x) {
			return
		}
		// The first thing the user says opens the session; it can say "don't"
		// without correcting anything, because nothing has been done yet.
		if st.seenUser && st.afterTurn && isCorrection(x) && (cutoff.IsZero() || !r.Time.Before(cutoff)) {
			out = append(out, ruleCandidate{
				Harness:     meta.Harness,
				Date:        r.Time.Local().Format("2006-01-02"),
				Session:     shortID(meta.ID),
				SessionID:   meta.ID,
				Project:     meta.Project,
				Correction:  squeezeTrim(x, candidateQuote),
				AgentBefore: squeezeTrim(st.lastAgent, candidateBefore),
				time:        r.Time,
			})
		}
		st.seenUser, st.afterTurn = true, false
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].time.Equal(out[j].time) {
			return out[i].time.Before(out[j].time)
		}
		return out[i].SessionID < out[j].SessionID
	})
	out = dedupeCandidates(out)
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	for i := range out {
		out[i].N = i + 1
	}
	return out, nil
}

func isCorrection(x string) bool {
	if len([]rune(x)) >= candidateMaxRunes {
		return false
	}
	return correctionRe.MatchString(firstRunes(x, candidateScanRunes))
}

// candidateSession drops sessions whose corrections are not the user's: a
// spawned agent's turns are its parent's words, and a session run from a
// temporary directory is a test stand or a benchmark, which is where most of
// the measured noise came from. A path the user's ignore rule covers stays out
// here as it does everywhere else.
func candidateSession(meta index.SessionMeta, pol policy.Policy) bool {
	if meta.Kind != "" || sources.IsSubagentPath(meta.Path) {
		return false
	}
	if pol.Ignored(meta.Path, meta.Project) {
		return false
	}
	for _, p := range []string{meta.Path, meta.Project} {
		if throwawayPath(p) {
			return false
		}
	}
	return true
}

// candidateTempRoots is a variable so a test, whose fixtures necessarily live
// in a temporary directory, can say which roots count as a stand.
var candidateTempRoots = func() []string { return []string{os.TempDir(), "/tmp", "/private/tmp"} }

func throwawayPath(p string) bool {
	if p == "" {
		return false
	}
	s := filepath.ToSlash(p)
	// Claude Code files a session under its cwd with every separator turned
	// into a dash, so a stand in /tmp shows up as a "-tmp-" or "-private-tmp-"
	// directory rather than under /tmp itself.
	if strings.Contains(s, "deja-bench") || strings.Contains(s, "/-private-tmp") || strings.Contains(s, "/-tmp-") {
		return true
	}
	for _, root := range candidateTempRoots() {
		root = strings.TrimSuffix(filepath.ToSlash(root), "/")
		if root != "" && (s == root || strings.HasPrefix(s, root+"/")) {
			return true
		}
	}
	return false
}

func dedupeCandidates(in []ruleCandidate) []ruleCandidate {
	seen := map[string]bool{}
	out := in[:0]
	for _, c := range in {
		k := strings.ToLower(firstRunes(c.Correction, candidateDedupRunes))
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, c)
	}
	return out
}

// squeezeTrim folds runs of whitespace to one space and cuts to n runes, marking
// the cut.
func squeezeTrim(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRight(string(r[:n-1]), " ") + "…"
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
