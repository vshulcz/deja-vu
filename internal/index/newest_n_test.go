package index

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The bounded pick keeps exactly what sorting everything and cutting would,
// ties on the same moment included.
func TestNewestNMatchesAFullSort(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, size := range []int{0, 1, 59, 60, 61, 240, 241, 5000} {
		ss := make([]model.Session, size)
		for i := range ss {
			// Few distinct moments, so ties fall to harness and id.
			ss[i] = model.Session{ID: fmt.Sprint(r.Intn(size + 1)), Harness: []string{"claude", "codex"}[r.Intn(2)],
				Updated: base.Add(time.Duration(r.Intn(50)) * time.Hour)}
		}
		want := slices.Clone(ss)
		slices.SortStableFunc(want, func(a, b model.Session) int {
			if newestFirstSession(a, b) {
				return -1
			}
			if newestFirstSession(b, a) {
				return 1
			}
			return 0
		})
		if len(want) > 60 {
			want = want[:60]
		}
		got := newestN(slices.Clone(ss), 60)
		if len(got) != len(want) {
			t.Fatalf("size %d: kept %d, want %d", size, len(got), len(want))
		}
		for i := range got {
			if got[i].ID != want[i].ID || got[i].Harness != want[i].Harness || !got[i].Updated.Equal(want[i].Updated) {
				t.Fatalf("size %d: row %d is %+v, want %+v", size, i, got[i], want[i])
			}
		}
		if all := newestN(slices.Clone(ss), 0); len(all) != size {
			t.Fatalf("size %d: n=0 kept %d", size, len(all))
		}
	}
}
