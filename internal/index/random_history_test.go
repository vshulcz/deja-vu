package index

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
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
	for _, seed := range historySeeds() {
		t.Run(strconv.FormatInt(seed, 10), func(t *testing.T) { runRandomHistory(t, seed, false) })
	}
}

// The same histories with error lines a few sessions share, so the walls and
// frictext.bin have something to hold.
func TestRandomHistoriesWithWallsMatchARebuild(t *testing.T) {
	for _, seed := range historySeeds() {
		t.Run(strconv.FormatInt(seed, 10), func(t *testing.T) { runRandomHistory(t, seed, true) })
	}
}

func historySeeds() []int64 {
	seeds := []int64{*historySeed}
	if *historySeed == 0 {
		seeds = seeds[:0]
		for i := 1; i <= *historyCount; i++ {
			seeds = append(seeds, int64(i))
		}
	}
	return seeds
}

var historyWords = strings.Fields(`pool queue cache webhook retry ledger outbox kafka
	migration deadlock mutex config timeout pgbouncer worker billing payments
	signature rotation schema index tokenizer parser flaky linker`)

var historyProjects = []string{"app", "svc", "infra"}

// Openings too thin to name a session: the title waits for a later question.
var historyThin = []string{"hi", "ok", "continue", "fix it", "pgbouncer?"}

// prompt is a question, or now and then a thin turn.
func (r *historyRun) prompt(thinOdds int) string {
	if r.rng.Intn(thinOdds) == 0 {
		return historyThin[r.rng.Intn(len(historyThin))]
	}
	return "why does the " + r.words(4) + " fail"
}

type historySession struct {
	project, sid string
	day, min     int
	calls        int
	// torn holds the second half of a line written without it.
	torn string
	// said is the last line the agent said, kept to be written again verbatim,
	// time and all, as a store that duplicates a line does.
	said string
	// first is the body the transcript was first written with.
	first string
}

type historyRun struct {
	t     *testing.T
	h     *twoWayEnv
	rng   *rand.Rand
	live  []*historySession
	next  int
	log   []string
	index string
	more  historyStores
	walls bool
	// awaiting holds sessions whose prompt is in the log and whose transcript
	// the next step writes.
	awaiting []awaitingTranscript
	// shapes, when set, draws the steps a pass lands in the middle of: a
	// transcript rewritten and moved at once, which keeps the old file beside
	// the new one, a call whose result lands a pass later, and a rewrite that
	// replays the turns the transcript started with. Its own source, so the
	// histories without it replay as they were.
	shapes *rand.Rand
}

type awaitingTranscript struct {
	s    *historySession
	body string
}

func runRandomHistory(t *testing.T, seed int64, walls bool) {
	t.Helper()
	h := newTwoWayEnv(t)
	r := &historyRun{t: t, h: h, rng: rand.New(rand.NewSource(seed)), index: filepath.Join(h.tmp, "a.db"), walls: walls, shapes: rand.New(rand.NewSource(-seed))} //nolint:gosec // a seeded fixture, not crypto
	r.openStores()
	// Even seeds extend the offset table on every pass; odd ones leave the
	// tail to the readers' walk, as a small append does on a real store.
	if seed%2 == 0 {
		defer func(floor, share int64) { recordOffsetsRefreshFloor, recordOffsetsRefreshShare = floor, share }(recordOffsetsRefreshFloor, recordOffsetsRefreshShare)
		recordOffsetsRefreshFloor, recordOffsetsRefreshShare = 0, 1<<62
	}
	steps := 24
	for i := range steps {
		r.anyStep()
		if err := Ensure(r.index, "", false, nil); err != nil {
			t.Fatalf("seed %d, pass after %q: %v", seed, r.log[len(r.log)-1], err)
		}
		if i%12 == 11 || i == steps-1 {
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
	if err := Ensure(b, "", true, nil); err != nil {
		t.Fatal(err)
	}
	label := fmt.Sprintf("seed %d after step %d", seed, step)
	got, want := takeSnap(t, r.index), takeSnap(t, b)
	if diffSnaps(t, label, got, want) > 0 {
		t.Fatalf("%s; replay with -args -history-seed=%d. Steps:\n  %s", label, seed, strings.Join(r.log, "\n  "))
	}
	samePostings(t, r.index, b)
	offsetTableMatchesAWalk(t, r.index, false)
}

// wall is an error line a few sessions share, so friction has walls to name.
// The port differs between runs and not in the wall's hash, so which run's
// line a wall shows depends on which session is read first. Nothing in a
// history without walls.
func (r *historyRun) wall() string {
	if !r.walls {
		return ""
	}
	if r.rng.Intn(2) == 0 {
		return "zsh:1: command not found: pgbouncer\n"
	}
	return fmt.Sprintf("dial tcp 127.0.0.1:%d: connect: connection refused\n", 5432+r.rng.Intn(3)*1000)
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
		if s.said != "" && r.rng.Intn(6) == 0 {
			b.WriteString(s.said)
		}
		// Some turns share a minute with the one before.
		if r.rng.Intn(4) > 0 {
			s.min++
		}
		if s.min > 59 {
			s.day, s.min = s.day+1, 0
		}
		s.calls++
		id := fmt.Sprintf("%s-%d", s.sid, s.calls)
		switch r.rng.Intn(5) {
		case 0:
			b.WriteString(hPrompt(s.project, s.sid, s.day, s.min, r.prompt(4)))
		case 1:
			s.said = hSay(s.project, s.sid, s.day, s.min, "The "+r.words(5)+" needs a fix.")
			b.WriteString(s.said)
		case 2:
			b.WriteString(hBash(s.project, s.sid, s.day, s.min, id, "go test ./"+historyWords[r.rng.Intn(len(historyWords))]+"/...",
				"--- FAIL: TestPool (0.01s)\n    pool_test.go:12: "+r.words(3)+"\n"+r.wall()+"FAIL", true))
		case 3:
			b.WriteString(hBash(s.project, s.sid, s.day, s.min, id, "go build ./...", r.wall(), false))
		default:
			b.WriteString(hEdit(s.project, s.sid, s.day, s.min, id, historyWords[r.rng.Intn(len(historyWords))]+".go", r.words(2), r.words(2)))
		}
		if r.walls && r.rng.Intn(2) == 0 && s.min < 59 {
			s.min++
			b.WriteString(hBash(s.project, s.sid, s.day, s.min, id+"w", "make migrate", r.wall(), true))
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
	// A session whose prompt was logged a pass before its transcript: the
	// transcript lands now, as Claude Code writes it after the prompt hook.
	if len(r.awaiting) > 0 {
		a := r.awaiting[0]
		r.awaiting = r.awaiting[1:]
		h.put(a.s.project, a.s.sid, a.body)
		r.live = append(r.live, a.s)
		r.log = append(r.log, "the transcript of "+a.s.sid+" lands")
		return
	}
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
	if s != nil && op != 0 && op != 1 && r.shapes != nil && r.shapes.Intn(6) == 0 {
		r.shapeStep(s)
		return
	}
	switch op {
	case 0, 1:
		r.next++
		s = &historySession{project: historyProjects[r.rng.Intn(len(historyProjects))], sid: fmt.Sprintf("s%d", r.next), day: 1 + r.rng.Intn(20)}
		first := "start: " + r.words(5)
		if r.rng.Intn(3) == 0 {
			first = historyThin[r.rng.Intn(len(historyThin))]
		}
		body := hPrompt(s.project, s.sid, s.day, 0, first) + r.turns(s)
		s.first = body
		r.promptLogged(s, first)
		if r.next%3 == 0 {
			r.awaiting = append(r.awaiting, awaitingTranscript{s, body})
			r.log = append(r.log, "the prompt of "+s.sid+" in "+s.project+" logged before its transcript")
			return
		}
		h.put(s.project, s.sid, body)
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

// shapeStep is one of the steps shapes draws, on s.
func (r *historyRun) shapeStep(s *historySession) {
	h := r.h
	switch r.shapes.Intn(3) {
	case 0:
		// Written again and moved to another project before the next pass:
		// no rename, the old file is kept and the new one is a copy.
		to := historyProjects[r.shapes.Intn(len(historyProjects))]
		if to == s.project {
			to = "moved"
		}
		s.day++
		s.min = 0
		h.put(s.project, s.sid, hPrompt(s.project, s.sid, s.day, 0, "again: "+r.words(5))+r.turns(s))
		if err := os.MkdirAll(filepath.Dir(h.path(to, s.sid)), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.Rename(h.path(s.project, s.sid), h.path(to, s.sid)); err != nil {
			r.t.Fatal(err)
		}
		r.log = append(r.log, "rewrite "+s.sid+" and move it from "+s.project+" to "+to)
		s.project = to
	case 1:
		// A command whose result lands a pass after its call.
		if s.min > 58 {
			s.day, s.min = s.day+1, 0
		}
		s.min++
		s.calls++
		out, failed := "ok", false
		switch r.shapes.Intn(3) {
		case 0:
			out, failed = "Exit code 1\n--- FAIL: TestPool\n"+r.wall()+"FAIL", true
		case 1:
			out = r.wall() + "ok"
		}
		call, result, _ := strings.Cut(hBash(s.project, s.sid, s.day, s.min, fmt.Sprintf("%s-%d", s.sid, s.calls), "go test ./"+historyWords[r.shapes.Intn(len(historyWords))]+"/...", out, failed), "\n")
		h.add(s.project, s.sid, call+"\n")
		s.torn = result
		r.log = append(r.log, "a call of "+s.sid+" whose result lands a pass later")
	default:
		// Written again with the turns it started with, as a resume that
		// replays them does, then more.
		if s.first == "" {
			s.first = hPrompt(s.project, s.sid, s.day, 0, "replayed: "+r.words(3))
		}
		h.put(s.project, s.sid, s.first+r.turns(s))
		r.log = append(r.log, "rewrite "+s.sid+" replaying its first turns")
	}
}
