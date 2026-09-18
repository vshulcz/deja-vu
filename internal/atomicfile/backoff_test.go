package atomicfile

import (
	"testing"
	"time"
)

// The retry that publishes a file lost to a reader polling in a loop: flat
// waits keep step with a loop, so every attempt landed inside the reader's next
// open and the whole budget went nowhere (#3743). Three properties are what the
// fix rests on, and none of them is visible in a passing rename.

func TestTheWaitGrowsAndStopsGrowing(t *testing.T) {
	var prev time.Duration
	for i := range renameTries {
		got := renameBackoff(i)
		floor := renameWait << min(i, renameShifts)
		if floor > renameWaitMax {
			floor = renameWaitMax
		}
		if got < floor || got > floor+floor/2 {
			t.Fatalf("attempt %d waited %v, want between %v and %v", i, got, floor, floor+floor/2)
		}
		if got > renameWaitMax+renameWaitMax/2 {
			t.Fatalf("attempt %d waited %v, past the ceiling %v", i, got, renameWaitMax)
		}
		if i > 0 && i <= renameShifts && got < prev/2 {
			t.Fatalf("attempt %d waited %v after %v; the schedule is meant to grow", i, got, prev)
		}
		prev = got
	}
}

// The jitter is the half that breaks the phase lock, and a constant would pass
// every bound above.
func TestTheWaitIsNotTheSameNumberEveryTime(t *testing.T) {
	seen := map[time.Duration]int{}
	for range 200 {
		seen[renameBackoff(renameShifts)]++
	}
	if len(seen) < 10 {
		t.Fatalf("the ceiling wait took %d distinct values over 200 draws; a retry that always waits the same amount is what this replaced", len(seen))
	}
}

// A budget is only worth stating if it is long enough to outlast a reader that
// holds the file for a poll or two. 100 ms was not, on a runner where a sleep
// rounds up to 15.6 ms.
func TestTheBudgetOutlastsAPollingReader(t *testing.T) {
	var floor time.Duration
	for i := range renameTries {
		d := renameWait << min(i, renameShifts)
		if d > renameWaitMax {
			d = renameWaitMax
		}
		floor += d
	}
	if floor < time.Second {
		t.Fatalf("the retry gives up after %v at worst; a windows publish needs longer than one reader's grip", floor)
	}
}
