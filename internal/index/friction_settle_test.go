package index

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/policy"
)

// A settled wall leaves the surfaces that say what is still in the way, and
// stays in TopFriction, which the history surfaces read (#5628).
func TestASettledWallLeavesOpenFrictionOnly(t *testing.T) {
	dir := frictionIgnoreStore(t)
	walls := TopFriction(dir, 5, nil)
	if len(walls) != 1 {
		t.Fatalf("want one wall, got %d", len(walls))
	}
	if walls[0].Hash == 0 || len(WallHandle(walls[0].Hash)) != 8 {
		t.Fatalf("wall has no handle: %+v", walls[0])
	}
	if _, ok := FindFriction(dir, nil); !ok {
		t.Fatal("the wall is not open before any settle")
	}
	if err := policy.SettleWall(WallHandle(walls[0].Hash), time.Now()); err != nil {
		t.Fatal(err)
	}
	if f, ok := FindFriction(dir, nil); ok {
		t.Errorf("a settled wall is still open: %+v", f)
	}
	if len(TopFriction(dir, 5, nil)) != 1 {
		t.Error("settling took the wall out of the history")
	}
}

// The plan hook warns about walls a plan is about to meet; a settled one is
// not something to warn about.
func TestPlanFrictionSkipsASettledWall(t *testing.T) {
	dir := planCorpus(t, 0)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "policy.json"))
	out := PlanFrictionMatches(dir, planSteps("pytest_asyncio"), nil, 0)
	if len(out) != 1 {
		t.Fatalf("findings = %d, want 1", len(out))
	}
	// These sessions carry no start time, so the newest update stands in.
	newest := NewestStart(out[0].Wall.Sessions)
	if newest.IsZero() {
		t.Fatal("no time for sessions that carry only an update")
	}
	if err := policy.SettleWall(WallHandle(out[0].Wall.Hash), newest.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if out := PlanFrictionMatches(dir, planSteps("pytest_asyncio"), nil, 0); len(out) != 0 {
		t.Errorf("a settled wall was reported on a plan: %+v", out)
	}
}
