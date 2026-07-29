// Command decisionbench asks whether the session that *decided* something wins
// over sessions that merely talk about it.
//
// That is the claim behind #507 — "how do you filter signal from noise?" — and
// it is not answerable by the benchmarks already in the repo: bench recall
// returns 1.00 and bench prompt fires 10/12 with no false positives, so both
// are saturated and would show any change as a flat line.
//
// The shape is deliberately adversarial. For each question there is exactly one
// session that reached a conclusion, and several that mention the same terms
// *more often* while deciding nothing: someone asking, someone quoting a log,
// someone linking a doc. A lexical ranker should get these wrong, and if it does
// not, the feature this measures is not needed.
//
//	go run ./scripts/decisionbench [-v]
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// reportUsage asks what the reuse signal is worth. Two sessions answer the
// question equally well on the text; one of them is the one agents keep pulling
// back. That is the only signal deja has about what actually helped someone,
// as opposed to what reads like an answer — and until now nothing measured
// whether the 1.2× ceiling on it does anything at all.
func reportUsage(sessions []model.Session, verbose bool) {
	// Two equally-worded sessions per topic, one of them worn.
	var corpus []model.Session
	var probes []probe
	worn := map[string]int{}
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for i, t := range []string{"retry budget", "cache stampede", "leader election", "schema drift"} {
		day := base.AddDate(0, 0, -i)
		a := "used-" + fmt.Sprint(i)
		b := "unused-" + fmt.Sprint(i)
		text := t + " is the thing we keep hitting; here is what happens and why"
		corpus = append(corpus, session(a, day, []string{text, "and a second turn about " + t}))
		corpus = append(corpus, session(b, day, []string{text, "and a second turn about " + t}))
		worn[a] = 6
		probes = append(probes, probe{query: t, decision: a})
	}
	corpus = append(corpus, sessions...)

	for _, boost := range []struct {
		name string
		worn map[string]int
	}{{"off", nil}, {"on", worn}} {
		first := 0
		for _, p := range probes {
			hits, err := search.Run(corpus, search.Options{Query: p.query, All: true, RecallWorn: boost.worn})
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			if len(hits) > 0 && hits[0].Session.ID == p.decision {
				first++
			} else if verbose && boost.name == "on" {
				top := "—"
				if len(hits) > 0 {
					top = hits[0].Session.ID
				}
				fmt.Printf("  miss  %-38q want=%s top=%s\n", p.query, p.decision, top)
			}
		}
		fmt.Printf("reuse %-4s %d · ranked first %d (%.0f%%)\n", boost.name, len(probes), first, 100*float64(first)/float64(len(probes)))
	}

	// The ceiling question is not "is it enough for a tie" but "can it win one
	// it should lose". A heavily-used session that barely matches must not
	// outrank one that answers the question squarely.
	var risk []model.Session
	riskWorn := map[string]int{}
	kept := 0
	for i, t := range []string{"connection pool", "index rebuild", "token refresh"} {
		day := base.AddDate(0, 0, -i)
		strong := "strong-" + fmt.Sprint(i)
		wornWeak := "worn-weak-" + fmt.Sprint(i)
		risk = append(risk, session(strong, day, []string{
			t + " " + t + " keeps failing, and here is the whole story of " + t,
			"more about " + t,
		}))
		risk = append(risk, session(wornWeak, day, []string{
			"mostly about something else entirely, though " + t + " came up once",
			"the rest is unrelated",
		}))
		riskWorn[wornWeak] = 40 // far past anything the cap allows
		risk = append(risk, session("filler-"+fmt.Sprint(i), day, []string{"unrelated chatter"}))
		hits, _ := search.Run(risk, search.Options{Query: t, All: true, RecallWorn: riskWorn})
		if len(hits) > 0 && hits[0].Session.ID == strong {
			kept++
		} else if verbose {
			top := "—"
			if len(hits) > 0 {
				top = hits[0].Session.ID
			}
			fmt.Printf("  miss  %-38q want=%s top=%s\n", t, strong, top)
		}
	}
	fmt.Printf("reuse cap  %d · relevance still wins %d (%.0f%%)\n", 3, kept, 100*float64(kept)/3)
}

// report is the same measurement for a second set of questions.
func report(label string, sessions []model.Session, probes []probe, verbose bool) {
	first := 0
	for _, p := range probes {
		hits, err := search.Run(sessions, search.Options{Query: p.query, All: true})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if len(hits) > 0 && hits[0].Session.ID == p.decision {
			first++
			continue
		}
		if verbose {
			top := "—"
			if len(hits) > 0 {
				top = hits[0].Session.ID
			}
			fmt.Printf("  miss  %-38q want=%s top=%s\n", p.query, p.decision, top)
		}
	}
	fmt.Printf("%s %d · ranked first %d (%.0f%%)\n", label, len(probes), first, 100*float64(first)/float64(len(probes)))
}

type probe struct {
	query    string
	decision string // session id that concluded something
}

func main() {
	verbose := flag.Bool("v", false, "print every miss")
	flag.Parse()

	sessions, probes, noise := corpus()
	hit1, hit3 := 0, 0
	for _, p := range probes {
		hits, err := search.Run(sessions, search.Options{Query: p.query, All: true})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		rank := -1
		for i, h := range hits {
			if h.Session.ID == p.decision {
				rank = i + 1
				break
			}
		}
		switch {
		case rank == 1:
			hit1++
			hit3++
		case rank >= 2 && rank <= 3:
			hit3++
		}
		if *verbose && rank != 1 {
			top := "—"
			if len(hits) > 0 {
				top = hits[0].Session.ID
			}
			fmt.Printf("  miss  %-34q decision=%s rank=%d top=%s\n", p.query, p.decision, rank, top)
		}
	}
	fmt.Printf("decisions  %d · ranked first %d (%.0f%%) · in top 3 %d (%.0f%%)\n",
		len(probes), hit1, 100*float64(hit1)/float64(len(probes)), hit3, 100*float64(hit3)/float64(len(probes)))
	report("noise     ", sessions, noise, *verbose)
	reportUsage(sessions, *verbose)
}

// corpus builds, for each question, one deciding session and several louder
// distractors. The distractors repeat the query terms more than the decision
// does — that is the trap, and it is the ordinary case in a real store, where
// most sessions that mention a topic never settled anything about it.
func corpus() ([]model.Session, []probe, []probe) {
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var out []model.Session
	var probes, noise []probe

	topics := []struct {
		id       string
		terms    string
		decision string
	}{
		{"pgbouncer", "prepared statements pgbouncer", "We pinned pgx to 5.4.3. Root cause was that pgbouncer in transaction mode cannot hold prepared statements across connections."},
		{"queue", "job queue redis", "Decision: we moved the job queue to Postgres advisory locks, because redis eviction does not respect list keys."},
		{"hydration", "hydration mismatch dates", "Fixed by formatting dates on the client after mount; the server rendered them in UTC and the browser in the local zone."},
		{"bundle", "bundle size icons", "We dropped the barrel file. Root cause: re-exporting every icon defeated tree shaking."},
		{"jwt", "jwt refresh clock skew", "Traced it to clock skew between the auth pods; we set a 60s leeway and moved to 15m tokens."},
		{"trigram", "trigram index size", "We dropped the trigram index instead of tuning it — it was 40% of the database and helped 2% of queries."},
		{"oom", "reindex job memory", "Fixed by streaming the postings writes per bucket; the job had been loading the whole map first."},
		{"backup", "backup restore flaky", "The fix was waiting on the archive marker rather than a fixed sleep."},
	}

	for i, t := range topics {
		day := base.AddDate(0, 0, -i*3)
		// The session that concluded something. It says the terms once.
		out = append(out, session(t.id+"-decided", day, []string{
			t.terms + " keeps failing",
			t.decision,
		}))
		// Louder sessions that decide nothing.
		out = append(out, session(t.id+"-asking", day.AddDate(0, 0, -1), []string{
			"anyone seen " + t.terms + "? " + t.terms + " again today",
			"still looking at " + t.terms + ", no idea yet",
		}))
		out = append(out, session(t.id+"-log", day.AddDate(0, 0, -2), []string{
			"dumping the log for " + t.terms,
			strings.Repeat(t.terms+" warn ", 6),
		}))
		out = append(out, session(t.id+"-link", day.AddDate(0, 0, -4), []string{
			"docs about " + t.terms,
			"see the upstream page on " + t.terms + " and the " + t.terms + " faq",
		}))
		probes = append(probes, probe{query: t.terms, decision: t.id + "-decided"})

		// A second shape, where nobody concluded anything: a human answer with
		// no decision vocabulary against a pasted log that repeats the terms.
		// The decision boost cannot help here — this is the case that asks
		// whether noise costs a session anything.
		// The exact tier requires the query terms to meet inside one message,
		// not merely inside one session — cross-message co-occurrence is what
		// the relevance tier is for. A fixture that spreads them across turns
		// measures the ladder, not the ranking.
		out = append(out, session(t.id+"-answer", day.AddDate(0, 0, -6), []string{
			"what is going on with " + t.terms + "?",
			t.terms + " only happens on staging after a deploy, never locally",
		}))
		out = append(out, session(t.id+"-paste", day.AddDate(0, 0, -7), []string{
			"pasting the output",
			strings.Repeat("WARN "+t.terms+" staging deploy retry=3 conn=17 elapsed=812ms\n", 14),
		}))
		noise = append(noise, probe{query: t.terms + " staging deploy", decision: t.id + "-answer"})
	}
	return out, probes, noise
}

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
