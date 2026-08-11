// Command reusebench asks how far the reuse signal reaches. deja boosts a
// session agents keep pulling back (wornBoost, capped +20%), on the theory that
// what the user reused before is what they need again. decisionbench proves the
// boost breaks a tie and respects the relevance ceiling; this asks the question
// in between: when the answer the user keeps reusing is worded more quietly than
// a louder session that only looks like an answer, does the reuse history still
// surface it — and at what text gap does the +20% cap run out?
//
// Each topic pits a recurring answer (reused, worded once) against a distractor
// that repeats the query terms `extra` more times and was never reused. The
// sweep over `extra` is the reach curve: with the boost off vs on, how often the
// reused answer still ranks first as the distractor gets louder.
//
//	go run ./scripts/reusebench
package main

import (
	"fmt"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func session(id string, when time.Time, texts []string) model.Session {
	s := model.Session{ID: id, Harness: "claude", Project: "app", Started: when, Updated: when}
	for i, t := range texts {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		s.Messages = append(s.Messages, model.Message{Role: role, Text: t, Time: when.Add(time.Duration(i) * time.Minute)})
	}
	return s
}

func pct(a, n int) string { return fmt.Sprintf("%3.0f%%", 100*float64(a)/float64(n)) }

func main() {
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	topics := []string{
		"retry budget", "cache stampede", "leader election", "schema drift",
		"token refresh", "index rebuild", "connection pool", "memory leak",
	}
	const reuse = 6

	fmt.Println("reusebench · does reuse-history rescue a recurring answer a louder session would bury?")
	fmt.Printf("%-12s %-9s %-9s %-7s\n", "distractor", "worn-off", "worn-on", "lift")
	for _, extra := range []int{0, 1, 2, 3, 4} {
		off, on, n := 0, 0, 0
		for i, t := range topics {
			day := base.AddDate(0, 0, -i)
			ans := "answer-" + fmt.Sprint(i)
			dis := "distractor-" + fmt.Sprint(i)

			// The answer states the topic and that it was settled — quiet, one
			// mention of the terms per turn.
			answer := session(ans, day, []string{t + " issue", "we resolved " + t + " before and it held"})

			// The distractor repeats the query terms `extra` more times and was
			// never reused: it looks more like an answer on the text alone.
			loud := t
			for k := 0; k < extra; k++ {
				loud += " " + t
			}
			distractor := session(dis, day, []string{loud + " keeps failing", "raw logs about " + t})

			corpus := []model.Session{answer, distractor}
			worn := map[string]int{ans: reuse}

			hb, _ := search.Run(corpus, search.Options{Query: t, All: true})
			if len(hb) > 0 && hb[0].Session.ID == ans {
				off++
			}
			hw, _ := search.Run(corpus, search.Options{Query: t, All: true, RecallWorn: worn})
			if len(hw) > 0 && hw[0].Session.ID == ans {
				on++
			}
			n++
		}
		fmt.Printf("+%-11d %-9s %-9s %+d\n", extra, pct(off, n), pct(on, n), on-off)
	}
}
