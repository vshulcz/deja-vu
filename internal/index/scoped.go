package index

import (
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// ScopedFrom is the answer a search of the same query narrowed to scope
// would give, cut from whole, the wider search run with Options.PoolScope
// set to scope. ok is false when the narrower search would have read
// something whole did not, or answered from another tier: no session of the
// scope matched exactly, too few did and it would add a tail of the nearest
// wording, or whole read only part of the scope's matches.
func ScopedFrom(whole SearchResult, scope []string) (SearchResult, bool) {
	if len(scope) == 0 || whole.Tier != query.TierExact || whole.Fuzzy || whole.Stemmed || whole.Neighbour ||
		whole.Strict > 0 || whole.Directory != "" || len(whole.StrictIDs) > 0 {
		return SearchResult{}, false
	}
	if whole.Unread != nil && !whole.Unread.ScopeWhole {
		return SearchResult{}, false
	}
	var ss []model.Session
	for _, s := range whole.Sessions {
		if query.ProjectAnyMatches(s.Project, s.From, scope) {
			ss = append(ss, s)
		}
	}
	// A thin answer is given a relevance tail, which only the narrower search
	// itself can draw.
	if len(ss) < thinAND {
		return SearchResult{}, false
	}
	return SearchResult{Sessions: ss, Tier: whole.Tier, Variants: whole.Variants}, true
}

// AnyInScope reports whether any indexed session belongs to scope by the
// filter a search applies. With none, a search narrowed to it finds nothing on
// any tier, and recall run outside every known project used to spend most of
// its time walking the whole ladder to learn that.
func AnyInScope(dir string, scope []string) bool {
	if len(scope) == 0 {
		return true
	}
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return true
	}
	for _, meta := range m.Sessions {
		if query.ProjectAnyMatches(meta.Project, meta.From, scope) {
			return true
		}
	}
	return false
}
