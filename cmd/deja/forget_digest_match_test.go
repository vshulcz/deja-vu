package main

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// The fallback for records written before projects were recorded matched the
// project name anywhere in the digest, so `forget --project api` deleted a
// digest about work/web that said "a late api call" (#2330).
func TestForgetMatchesTheProjectWhereADigestNamesIt(t *testing.T) {
	match := forgetDigestMatcher(index.ForgetOptions{Project: "api"}, nil)

	elsewhere := usage.Snapshot{Digest: "<deja-recall>\n1. [claude] work/web · claude:b0 · 2 matches\n   the layout shift came from a late api call\n"}
	if match(elsewhere) {
		t.Errorf("a digest about work/web was matched because its text says \"api\"")
	}
	listed := usage.Snapshot{Digest: "<deja-recall>\n1. [claude] api · claude:a0 · 2 matches\n   the gateway timeout was a read deadline\n"}
	if !match(listed) {
		t.Errorf("a digest whose listing names the project was not matched")
	}
	hookBlock := usage.Snapshot{Digest: "<deja-recall>\n- **api** `a0` · 2026-08-28\n  the gateway timeout was a read deadline\n"}
	if !match(hookBlock) {
		t.Errorf("a hook digest naming the project was not matched")
	}
	// The recorded field still wins, and it is exact.
	recorded := usage.Snapshot{Projects: []string{"work/web"}, Digest: "1. [claude] api · anything\n"}
	if match(recorded) {
		t.Errorf("a record that names its own projects was matched by its text")
	}
	if !match(usage.Snapshot{Projects: []string{"api"}, Digest: "no project in the text at all"}) {
		t.Errorf("a record that names the project in its field was not matched")
	}
}

// Session ids are matched the same way: where the digest names one, not
// wherever the string happens to appear.
func TestForgetMatchesSessionIDsWhereADigestNamesThem(t *testing.T) {
	match := forgetDigestMatcher(index.ForgetOptions{Session: "a0"}, []string{"claude:a0"})
	if !match(usage.Snapshot{Digest: "1. [claude] api · claude:a0 · 2 matches\n"}) {
		t.Errorf("a digest listing the forgotten session was not matched")
	}
	if match(usage.Snapshot{Digest: "1. [claude] work/web · claude:b0 · 2 matches\n   we set a0 to 3 in the config\n"}) {
		t.Errorf("a digest was matched because its prose contains the id")
	}
}
