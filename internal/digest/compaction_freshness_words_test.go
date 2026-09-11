package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The freshness line is the packet's answer to "do these conclusions still
// describe this checkout". It printed the fingerprints — `head=<40 hex>,
// branch=master, worktree=<64 hex>, checked=<stamp>`, 150 bytes of a 1.4 KB
// packet — and the worktree hash is deja's own digest of the tree, which nothing
// outside deja can use.
func TestTheFreshnessLineGivesTheVerdict(t *testing.T) {
	unchanged := RenderCompactionContext(model.CompactionContext{
		Freshness: model.RepositoryFreshness{Head: "0123456789abcdef0123", Branch: "master", WorktreeState: "digest"},
	}, 1024)
	if !strings.Contains(unchanged, "Repository unchanged since capture (branch master).") {
		t.Errorf("the verdict is missing:\n%s", unchanged)
	}
	if strings.Contains(unchanged, "worktree=") || strings.Contains(unchanged, "head=") {
		t.Errorf("the fingerprints are still in the packet:\n%s", unchanged)
	}

	// Changed: the sentence the recovery path wrote, plus the commit it was
	// captured at — which is what makes a diff possible.
	changed := RenderCompactionContext(model.CompactionContext{
		Freshness: model.RepositoryFreshness{
			Head:   "0123456789abcdef0123",
			Branch: "master",
			Error:  "Repository changed since compaction. Revalidate conclusions and rerun relevant tests.",
		},
	}, 1024)
	if strings.Contains(changed, "freshness unavailable: Repository changed") {
		t.Errorf("a changed repository was reported as unavailable:\n%s", changed)
	}
	if !strings.Contains(changed, "Captured at 0123456789ab.") {
		t.Errorf("the packet does not say where to diff from:\n%s", changed)
	}

	// And a hook that recorded nothing still says so.
	none := RenderCompactionContext(model.CompactionContext{}, 1024)
	if !strings.Contains(none, "Repository freshness unavailable: hook did not record it.") {
		t.Errorf("a missing check was not named:\n%s", none)
	}
}
