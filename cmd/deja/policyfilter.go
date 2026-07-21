package main

import (
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

// policyFilterHits drops search hits the trust policy blocks on this
// activation path. The default policy blocks nothing.
func policyFilterHits(activation string, hits []search.Hit) []search.Hit {
	return policy.Filter(policy.Load(), activation, hits, func(h search.Hit) string {
		return h.Session.Project
	})
}
