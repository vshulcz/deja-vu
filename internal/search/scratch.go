package search

import (
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
)

// WithoutIgnored drops the sessions the trust policy says deja does not recall
// from: by default an agent runtime's own scratch tree, and whatever else the
// policy file names.
//
// Applied where sessions enter a search rather than inside the scorer. The
// error-signature and relevance tiers build their hits without going through
// the scoring loop, so a filter there covered one path of three and the
// transcript still came back (#2050).
func WithoutIgnored(ss []model.Session) []model.Session {
	p := policy.Load()
	out := ss[:0:0]
	for _, s := range ss {
		if p.Ignored(s.Path, s.Project) {
			continue
		}
		out = append(out, s)
	}
	return out
}
