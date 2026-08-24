package search

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A digest is ~8 KB whatever the session is, so the work it costs has to be
// bounded by what it shows rather than by what it reads. Rendering every turn
// before choosing the window spent 22 s on a 279 MB session to print 8 KB
// (#1742).
func TestContextCostIsBoundedByWhatItPrints(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	build := func(n int) model.Session {
		s := model.Session{Harness: "claude", ID: "big", Project: "proj", Updated: time.Now()}
		for i := range n {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			s.Messages = append(s.Messages, model.Message{
				Role: role,
				Text: fmt.Sprintf("shard %d ", i) + strings.Repeat("payload ", 1200),
			})
		}
		return s
	}

	var small, large bytes.Buffer
	t0 := time.Now()
	PrintContext(&small, build(200), "grumbleflux")
	smallCost := time.Since(t0)
	t1 := time.Now()
	PrintContext(&large, build(20000), "grumbleflux")
	largeCost := time.Since(t1)

	if large.Len() > 9000 || small.Len() > 9000 {
		t.Fatalf("digest is not bounded: small=%d large=%d", small.Len(), large.Len())
	}
	// 20000 turns of 10 KB is 200 MB of text. Rendering it took 15 s before the
	// line filters stopped handing every line to a regexp engine; it is under
	// 3 s now. The bound is loose enough for a loaded CI box and still fails
	// the moment the per-line scan goes back.
	if largeCost > 8*time.Second {
		t.Errorf("20000 turns cost %v to print %d bytes (200 turns: %v)", largeCost, large.Len(), smallCost)
	}
	t.Logf("200 turns: %v (%d bytes) · 20000 turns: %v (%d bytes)", smallCost, small.Len(), largeCost, large.Len())
}
