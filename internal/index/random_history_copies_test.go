package index

import (
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The histories with walls, with now and then a second transcript of a live
// session's id in another project, which then grows, is written again or goes
// (#699). Fixes and frictext.bin took both copies' records in the order a
// pass left them in the log, which a rebuild does not (#5504).
func TestRandomHistoriesWithSharedCopiesMatchARebuild(t *testing.T) {
	for _, seed := range historySeeds() {
		t.Run(strconv.FormatInt(seed, 10), func(t *testing.T) { runCopiesHistory(t, seed, false) })
	}
}

// The same with each copy written on its session's own day, so a copy can
// hold a turn its session holds, identical to the second. Which copy such a
// turn is filed under, and in what order a deleted copy's turns sit among
// the live ones', moved the command tables (#5509, #5508).
func TestRandomHistoriesWithSameDayCopiesMatchARebuild(t *testing.T) {
	seeds := []int64{15, 18, 19, 24}
	if *historySeed != 0 {
		seeds = []int64{*historySeed}
	}
	for _, seed := range seeds {
		t.Run(strconv.FormatInt(seed, 10), func(t *testing.T) { runCopiesHistory(t, seed, true) })
	}
}

func runCopiesHistory(t *testing.T, seed int64, sameDay bool) {
	t.Helper()
	h := newTwoWayEnv(t)
	r := &historyRun{t: t, h: h, rng: rand.New(rand.NewSource(seed)), index: filepath.Join(h.tmp, "a.db"), walls: true} //nolint:gosec // a seeded fixture, not crypto
	r.openStores()
	var copies []*historySession
	copyStep := func() {
		if len(copies) > 0 && r.rng.Intn(2) == 0 {
			i := r.rng.Intn(len(copies))
			c := copies[i]
			switch r.rng.Intn(3) {
			case 0:
				h.add(c.project, c.sid, r.turns(c))
				r.log = append(r.log, "append to the copy of "+c.sid+" in "+c.project)
			case 1:
				h.put(c.project, c.sid, hPrompt(c.project, c.sid, c.day, 0, "copy again: "+r.words(4))+r.turns(c))
				r.log = append(r.log, "rewrite the copy of "+c.sid+" in "+c.project)
			default:
				if err := os.Remove(h.path(c.project, c.sid)); err != nil {
					t.Fatal(err)
				}
				copies = append(copies[:i], copies[i+1:]...)
				r.log = append(r.log, "delete the copy of "+c.sid+" in "+c.project)
			}
			return
		}
		s := r.pick()
		if s == nil {
			r.step()
			return
		}
		proj := []string{"fork", "aa"}[r.rng.Intn(2)]
		for _, c := range copies {
			if c.project == proj && c.sid == s.sid {
				r.step()
				return
			}
		}
		// Days after any the sessions reach, so no turn of a copy is one its
		// session holds: which copy a shared turn is filed under is #5492's.
		c := &historySession{project: proj, sid: s.sid, day: s.day}
		if !sameDay {
			c.day = 25 + r.rng.Intn(3)
		}
		h.put(proj, s.sid, hPrompt(proj, s.sid, s.day, 0, "copy: "+r.words(5))+r.turns(c))
		copies = append(copies, c)
		r.log = append(r.log, "copy of "+s.sid+" in "+proj)
	}
	steps := 36
	for i := range steps {
		if r.rng.Intn(5) == 0 {
			copyStep()
		} else {
			r.anyStep()
		}
		if err := Ensure(r.index, "", false, nil); err != nil {
			t.Fatalf("seed %d, pass after %q: %v", seed, r.log[len(r.log)-1], err)
		}
		if i%12 == 11 || i == steps-1 {
			r.compare(seed, i)
		}
	}
}
