// Command blockcarry asks how often the block deja hands over carries what the
// session settled, rather than a line that merely mentions the subject.
//
// The instrument is #2243's: replay every session that settled something, ask
// with the terms the hook would extract from that session's own opening
// question, build the block the hook would build, and judge it with the same
// digest.CarriesDecision the block uses to pick a line. Misses are attributed
// by the role of the line that did carry a decision, because the three groups
// need different work.
//
//	go run ./scripts/blockcarry [-limit N] [-v]
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/prompt"
	"github.com/vshulcz/deja-vu/internal/search"
)

func main() {
	limit := flag.Int("limit", 0, "stop after this many sessions that settled something")
	verbose := flag.Bool("v", false, "print each miss")
	flag.Parse()

	dir := index.DefaultDir()
	// Whole sessions, rebuilt from the records: the ranked paths hand back
	// what matched, and this has to judge the block a session would produce
	// for its own question.
	recs, err := index.ReadRecords(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "blockcarry:", err)
		os.Exit(1)
	}
	ss := sessionsOf(recs)
	settled, carried, empty := 0, 0, 0
	missBy := map[string]int{}
	for _, s := range ss {
		if !sessionSettledSomething(s) {
			continue
		}
		asked := openingQuestion(s)
		if asked == "" {
			continue
		}
		terms := prompt.Terms(asked)
		if len(terms) == 0 {
			continue
		}
		settled++
		block := search.AutoRecallDigestForAsked([]model.Session{s}, 4096, terms, asked)
		switch {
		case strings.TrimSpace(block) == "":
			empty++
		case blockCarriesDecision(block):
			carried++
		default:
			missBy[whereTheDecisionWas(s, terms)]++
			if *verbose {
				fmt.Printf("MISS %s %s\n  asked: %s\n%s\n", s.Harness, s.ID, cut(asked, 100), block)
			}
		}
		if *limit > 0 && settled >= *limit {
			break
		}
	}
	fmt.Printf("sessions that settled something   %6d\n", settled)
	fmt.Printf("  the block carried it            %6d  (%.0f%%)\n", carried, pct(carried, settled))
	fmt.Printf("  the block came back empty       %6d\n", empty)
	for _, role := range []string{"assistant", "user", "tool/other"} {
		fmt.Printf("  decision line not taken, %-10s %5d\n", role, missBy[role])
	}
}

// sessionsOf folds the records back into sessions, in file order, which is the
// order they were written in.
func sessionsOf(recs []index.OffsetRecord) []model.Session {
	byKey := map[string]*model.Session{}
	var order []string
	for _, or := range recs {
		r := or.Record
		s := byKey[r.Key]
		if s == nil {
			harness, id := r.Key, r.Key
			if i := strings.IndexByte(r.Key, ':'); i > 0 {
				harness, id = r.Key[:i], r.Key[i+1:]
			}
			s = &model.Session{ID: id, Harness: harness, Path: r.SourcePath}
			byKey[r.Key] = s
			order = append(order, r.Key)
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
		s.Touch(r.Time)
	}
	out := make([]model.Session, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out
}

func pct(n, of int) float64 {
	if of == 0 {
		return 0
	}
	return 100 * float64(n) / float64(of)
}

func cut(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// sessionSettledSomething is the same test the block uses on a line, asked of
// the session: did anyone in it conclude anything at all.
func sessionSettledSomething(s model.Session) bool {
	for _, m := range s.Messages {
		if m.Role != "assistant" {
			continue
		}
		for _, line := range strings.Split(m.Text, "\n") {
			if digest.CarriesDecision(line) {
				return true
			}
		}
	}
	return false
}

// openingQuestion is what the session was asked first, which is the closest
// thing to the question a later reader would ask about it.
func openingQuestion(s model.Session) string {
	for _, m := range s.Messages {
		if m.Role != "user" {
			continue
		}
		text := strings.TrimSpace(m.Text)
		if len(text) < 12 {
			continue
		}
		return text
	}
	return ""
}

func blockCarriesDecision(block string) bool {
	for _, line := range strings.Split(block, "\n") {
		if digest.CarriesDecision(line) {
			return true
		}
	}
	return false
}

// whereTheDecisionWas attributes a miss to the role of the line that settled
// the asked question, so the three groups can be worked on separately: a
// printout is not a decision, and a decision the person stated is a different
// problem from one the agent stated and the block dropped.
func whereTheDecisionWas(s model.Session, terms []string) string {
	best := "tool/other"
	for _, m := range s.Messages {
		for _, line := range strings.Split(m.Text, "\n") {
			if !digest.CarriesDecision(line) || search.TermHitsLowered(strings.ToLower(line), terms) == 0 {
				continue
			}
			switch m.Role {
			case "assistant":
				return "assistant"
			case "user":
				best = "user"
			}
		}
	}
	return best
}
