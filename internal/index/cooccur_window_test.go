package index

import (
	"sync"
	"testing"
)

// The co-occurrence pass used to hold every session's distinct tokens until
// the last session was tokenised: 1.4 GB of a 12k-session cold build's peak.
// Results now go to the consumer in order, and no more than the window are
// produced ahead of it.
func TestParallelInOrderHoldsNoMoreThanTheWindow(t *testing.T) {
	rerankWorkers = func() int { return 8 }
	t.Cleanup(func() { rerankWorkers = defaultRerankWorkers })

	const n, window = 500, 16
	var mu sync.Mutex
	consumed, ahead := 0, 0
	var got []int
	parallelInOrder(n, window, func(i int) int {
		mu.Lock()
		if d := i - consumed + 1; d > ahead {
			ahead = d
		}
		mu.Unlock()
		return i * 3
	}, func(i, v int) {
		if v != i*3 {
			t.Fatalf("result %d handed to index %d", v, i)
		}
		got = append(got, i)
		mu.Lock()
		consumed = i + 1
		mu.Unlock()
	})
	if len(got) != n {
		t.Fatalf("consumed %d of %d", len(got), n)
	}
	for i, v := range got {
		if v != i {
			t.Fatalf("index %d consumed at position %d", v, i)
		}
	}
	if ahead > window {
		t.Fatalf("produced %d results ahead of the consumer, window is %d", ahead, window)
	}
}
