package index

import (
	"flag"
	"fmt"
	"io"
	"math/rand"
	"path/filepath"
	"testing"
)

// The random histories of TestRandomHistoriesMatchARebuild, indexed the way
// `deja index` does it: a freshness check, then the pass it hands its walk
// to, with a store changing in between, or another deja's pass. The fixed
// seeds lost a session and a command to the stale walk (#5503). A sweep:
//
//	go test ./internal/index -run RandomCheckedHistories -args -checked-histories=200
var checkedHistories = flag.Int("checked-histories", 0, "random checked histories to run from seed 2000, in place of the fixed ones")

func TestRandomCheckedHistoriesMatchARebuild(t *testing.T) {
	seeds := []int64{2123, 5016}
	if *historySeed != 0 {
		seeds = []int64{*historySeed}
	} else if *checkedHistories > 0 {
		seeds = seeds[:0]
		for i := 0; i < *checkedHistories; i++ {
			seeds = append(seeds, int64(2000+i))
		}
	}
	for _, seed := range seeds {
		t.Run(fmt.Sprint(seed), func(t *testing.T) { runCheckedHistory(t, seed) })
	}
}

func runCheckedHistory(t *testing.T, seed int64) {
	h := newTwoWayEnv(t)
	r := &historyRun{t: t, h: h, rng: rand.New(rand.NewSource(seed)), index: filepath.Join(h.tmp, "a.db"), walls: seed%3 == 0}
	// The fixed seeds replay #5503's histories as they were found.
	if seed != 2123 && seed != 5016 {
		r.shapes = rand.New(rand.NewSource(-seed))
	}
	r.openStores()
	// The sweep that found the seeds above made a copy of a session on one
	// step in eight; the draw stays so they replay the same histories.
	step := func() {
		r.rng.Intn(8)
		r.anyStep()
	}
	steps := 48
	for i := 0; i < steps; i++ {
		step()
		var err error
		switch mode := r.rng.Intn(4); mode {
		case 0:
			err = Ensure(r.index, "", false, nil)
		case 1, 2:
			fresh, _, c := CheckUpToDate(r.index, "")
			if mode == 2 && c != nil {
				step()
				r.log[len(r.log)-1] += " (between check and pass)"
			}
			if !fresh {
				if c != nil {
					err = EnsureChecked(c, false, io.Discard)
				} else {
					err = Ensure(r.index, "", false, nil)
				}
			}
		case 3:
			fresh, _, c := CheckUpToDate(r.index, "")
			step()
			r.log[len(r.log)-1] += " (another pass between check and pass)"
			if err := Ensure(r.index, "", false, nil); err != nil {
				t.Fatal(err)
			}
			if !fresh && c != nil {
				err = EnsureChecked(c, false, io.Discard)
			}
		}
		if err != nil {
			t.Fatalf("seed %d, pass after %q: %v", seed, r.log[len(r.log)-1], err)
		}
		if i%12 == 11 || i == steps-1 {
			// One more ordinary pass picks up what the check's walk could not
			// have seen.
			if err := Ensure(r.index, "", false, nil); err != nil {
				t.Fatal(err)
			}
			r.compare(seed, i)
		}
	}
}
