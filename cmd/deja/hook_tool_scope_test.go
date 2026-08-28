package main

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The file line is scoped so that "main.go" does not collect every project's
// file of that name. The scope took the checkout's bare directory name as a
// path suffix, so editing /work/api/ledger.go was answered with seven sessions
// from a client's acme/api — and their decision (#2339).
func TestFileScopeDoesNotCrossProjectsOnABareName(t *testing.T) {
	candidates := []string{"work/api", "api"}
	cases := []struct {
		project string
		in      bool
	}{
		{"work/api", true},
		{"acme/api", false},         // another project ending the same way
		{"imported:work/api", true}, // the same project from a peer
		{"api", true},               // a store that records the bare name
		{"imported:acme/api", true}, // a peer's path is not this machine's
		{"clients/acme/api", false},
	}
	for _, c := range cases {
		meta := index.SessionMeta{Project: c.project}
		if got := fileMetaInScope(meta, "/work/api/ledger.go", candidates); got != c.in {
			t.Errorf("fileMetaInScope(project=%q) = %v, want %v", c.project, got, c.in)
		}
	}
	// The exact path always counts, whatever the project says.
	touched := index.SessionMeta{Project: "acme/api", Touched: []string{"/work/api/ledger.go"}}
	if !fileMetaInScope(touched, "/work/api/ledger.go", candidates) {
		t.Errorf("a session that touched this very path is out of scope")
	}
}
