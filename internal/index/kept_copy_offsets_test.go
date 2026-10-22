package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A rebuild leaves a session's live copy ahead of its deleted, kept one in
// the log. A rewrite of the live copy then files the turns it no longer holds
// under the kept one, appended at the end, while the kept copy's own records
// stay where they were. The offset table listed the moved turns first: the
// right offsets, in another order than a walk of the log gives (#5522).
func TestTurnsMovedToAKeptCopyKeepTheOffsetTableInLogOrder(t *testing.T) {
	h := newTwoWayEnv(t)
	a := filepath.Join(h.tmp, "a.db")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h.tmp, "config"))
	// Enough else in the log that the rewrite appends to it rather than
	// writing it again.
	for _, sid := range []string{"s1", "s2", "s4", "s5"} {
		body := ""
		var bodySb22 strings.Builder
		for i := range 8 {
			bodySb22.WriteString(hPrompt("svc", sid, 1, i, "why does the ledger fail on retry number "+sid))
		}
		body += bodySb22.String()
		h.put("svc", sid, body)
	}
	build := func(rebuild bool) {
		t.Helper()
		if err := Ensure(a, "", rebuild, nil); err != nil {
			t.Fatal(err)
		}
	}
	h.put("infra", "s3", hPrompt("infra", "s3", 2, 0, "why does the pool fail")+
		hBash("infra", "s3", 2, 1, "a1", "make migrate", "ok", false))
	build(false)
	h.put("aa", "s3", hPrompt("aa", "s3", 2, 0, "copy: why does the queue fail")+
		hBash("aa", "s3", 2, 2, "b1", "go test ./...", "ok", false))
	build(true)
	if err := os.Remove(h.path("infra", "s3")); err != nil {
		t.Fatal(err)
	}
	build(true)
	h.put("aa", "s3", hPrompt("aa", "s3", 2, 3, "again: why does the cache fail"))
	build(false)
	if m, err := readManifest(a); err != nil || m.DeadBytes == 0 {
		t.Fatalf("the rewrite wrote the log again (dead bytes 0, %v), so this measures nothing", err)
	}
	offsetTableMatchesAWalk(t, a, true)
}
