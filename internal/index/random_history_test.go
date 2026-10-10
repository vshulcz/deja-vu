package index

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A random history of appends, new sessions, torn and unterminated last
// lines, rewrites, moves and deletions, indexed one pass at a time, must
// leave what a forced rebuild of the same stores leaves. The fixed seeds run
// on every test run. Flags, since TestMain clears the environment:
//
//	go test ./internal/index -run RandomHistories -args -histories=200
//	go test ./internal/index -run RandomHistories -args -history-seed=17
var (
	historyCount = flag.Int("histories", 4, "random histories to run")
	historySeed  = flag.Int64("history-seed", 0, "run this one history")
)

func TestRandomHistoriesMatchARebuild(t *testing.T) {
	seeds := []int64{*historySeed}
	if *historySeed == 0 {
		seeds = seeds[:0]
		for i := 1; i <= *historyCount; i++ {
			seeds = append(seeds, int64(i))
		}
	}
	for _, seed := range seeds {
		t.Run(fmt.Sprint(seed), func(t *testing.T) { runRandomHistory(t, seed) })
	}
}

var historyWords = strings.Fields(`pool queue cache webhook retry ledger outbox kafka
	migration deadlock mutex config timeout pgbouncer worker billing payments
	signature rotation schema index tokenizer parser flaky linker`)

var historyProjects = []string{"app", "svc", "infra"}

type historySession struct {
	project, sid string
	day, min     int
	calls        int
	// torn holds the second half of a line written without it.
	torn string
}

type historyRun struct {
	t     *testing.T
	h     *twoWayEnv
	rng   *rand.Rand
	live  []*historySession
	next  int
	log   []string
	index string
}

func runRandomHistory(t *testing.T, seed int64) {
	h := newTwoWayEnv(t)
	r := &historyRun{t: t, h: h, rng: rand.New(rand.NewSource(seed)), index: filepath.Join(h.tmp, "a.db")}
	steps := 30
	for i := 0; i < steps; i++ {
		r.step()
		if err := Ensure(r.index, "claude", false, nil); err != nil {
			t.Fatalf("seed %d, pass after %q: %v", seed, r.log[len(r.log)-1], err)
		}
		if i%10 == 9 || i == steps-1 {
			r.compare(seed, i)
		}
	}
}

// compare rebuilds a copy of the index from the same stores and diffs it with
// the one kept up to date pass by pass.
func (r *historyRun) compare(seed int64, step int) {
	t := r.t
	t.Helper()
	b := filepath.Join(r.h.tmp, fmt.Sprintf("rebuild-%d.db", step))
	copyDir(t, r.index, b)
	if err := Ensure(b, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	label := fmt.Sprintf("seed %d after step %d", seed, step)
	if diffSnaps(t, label, takeSnap(t, r.index), takeSnap(t, b)) > 0 {
		t.Fatalf("%s; replay with -args -history-seed=%d. Steps:\n  %s", label, seed, strings.Join(r.log, "\n  "))
	}
}

func (r *historyRun) words(n int) string {
	w := make([]string, n)
	for i := range w {
		w[i] = historyWords[r.rng.Intn(len(historyWords))]
	}
	return strings.Join(w, " ")
}

// turns writes one to three turns of s and moves its clock on.
func (r *historyRun) turns(s *historySession) string {
	var b strings.Builder
	for n := 1 + r.rng.Intn(3); n > 0; n-- {
		s.min++
		if s.min > 59 {
			s.day, s.min = s.day+1, 0
		}
		s.calls++
		id := fmt.Sprintf("%s-%d", s.sid, s.calls)
		switch r.rng.Intn(5) {
		case 0:
			b.WriteString(hPrompt(s.project, s.sid, s.day, s.min, "why does the "+r.words(4)+" fail"))
		case 1:
			b.WriteString(hSay(s.project, s.sid, s.day, s.min, "The "+r.words(5)+" needs a fix."))
		case 2:
			b.WriteString(hBash(s.project, s.sid, s.day, s.min, id, "go test ./"+historyWords[r.rng.Intn(len(historyWords))]+"/...",
				"--- FAIL: TestPool (0.01s)\n    pool_test.go:12: "+r.words(3)+"\nFAIL", true))
		case 3:
			b.WriteString(hBash(s.project, s.sid, s.day, s.min, id, "go build ./...", "", false))
		default:
			b.WriteString(hEdit(s.project, s.sid, s.day, s.min, id, historyWords[r.rng.Intn(len(historyWords))]+".go", r.words(2), r.words(2)))
		}
	}
	return b.String()
}

func (r *historyRun) pick() *historySession {
	if len(r.live) == 0 {
		return nil
	}
	return r.live[r.rng.Intn(len(r.live))]
}

func (r *historyRun) step() {
	h := r.h
	s := r.pick()
	op := r.rng.Intn(10)
	if s == nil {
		op = 0
	}
	// A torn line is finished before anything else touches its file: an
	// agent writes the rest of a line before it writes the next one.
	if s != nil && s.torn != "" && op != 0 {
		h.add(s.project, s.sid, s.torn)
		s.torn = ""
		r.log = append(r.log, "finish the torn line of "+s.sid)
		return
	}
	switch op {
	case 0, 1:
		r.next++
		s = &historySession{project: historyProjects[r.rng.Intn(len(historyProjects))], sid: fmt.Sprintf("s%d", r.next), day: 1 + r.rng.Intn(20)}
		h.put(s.project, s.sid, hPrompt(s.project, s.sid, s.day, 0, "start: "+r.words(5))+r.turns(s))
		r.live = append(r.live, s)
		r.log = append(r.log, "new "+s.sid+" in "+s.project)
	case 2, 3, 4:
		h.add(s.project, s.sid, r.turns(s))
		r.log = append(r.log, "append to "+s.sid)
	case 5:
		// The last line complete but its newline not written yet.
		h.add(s.project, s.sid, strings.TrimSuffix(r.turns(s), "\n"))
		s.torn = "\n"
		r.log = append(r.log, "append to "+s.sid+" without the final newline")
	case 6:
		// Half a line on disk, the rest on a later pass.
		line := r.turns(s)
		cut := len(line) / 2
		h.add(s.project, s.sid, line[:cut])
		s.torn = line[cut:]
		r.log = append(r.log, "append half a line to "+s.sid)
	case 7:
		// Written again shorter, the way a rewrite or a compaction leaves it.
		s.day++
		s.min = 0
		h.put(s.project, s.sid, hPrompt(s.project, s.sid, s.day, 0, "again: "+r.words(5))+r.turns(s))
		r.log = append(r.log, "rewrite "+s.sid)
	case 8:
		to := historyProjects[r.rng.Intn(len(historyProjects))]
		if to == s.project {
			r.log = append(r.log, "touch "+s.sid)
			at := time.Now().Add(time.Duration(r.rng.Intn(5)+1) * time.Second)
			if err := os.Chtimes(h.path(s.project, s.sid), at, at); err != nil {
				r.t.Fatal(err)
			}
			return
		}
		from := h.path(s.project, s.sid)
		if err := os.MkdirAll(filepath.Dir(h.path(to, s.sid)), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.Rename(from, h.path(to, s.sid)); err != nil {
			r.t.Fatal(err)
		}
		r.log = append(r.log, "move "+s.sid+" from "+s.project+" to "+to)
		s.project = to
	case 9:
		if err := os.Remove(h.path(s.project, s.sid)); err != nil {
			r.t.Fatal(err)
		}
		for i, l := range r.live {
			if l == s {
				r.live = append(r.live[:i], r.live[i+1:]...)
				break
			}
		}
		r.log = append(r.log, "delete "+s.sid)
	}
}
