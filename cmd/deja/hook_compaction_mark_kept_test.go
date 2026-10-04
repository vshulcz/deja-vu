package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A forked agent that compacts runs PreCompact under its parent's session id,
// and the forget that follows took the parent's delivery mark with it: the
// parent's packet from a day earlier, naming PRs merged since and a request
// many turns old, was delivered again on the parent's next prompt.
func TestForgetKeepsThePacketDeliveryMark(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	rememberInjectedIDsFor(dir, "parent", "proj", []string{"some-session", "compaction:abc123"})
	rememberInjectedIDs(dir, compactionFailureKey("parent"), compactionUnavailableToken("/w"))

	forgetInjected(dir, "parent")
	forgetInjected(dir, compactionFailureKey("parent"))

	got := alreadyInjected(dir, "parent")
	if !got["compaction:abc123"] {
		t.Fatalf("the delivery mark went with the forget: %v", got)
	}
	if got["some-session"] {
		t.Fatalf("the session's recall rows survived the forget: %v", got)
	}
	if len(alreadyInjected(dir, compactionFailureKey("parent"))) != 0 {
		t.Fatal("the failure mark must still clear, so a new capture can be delivered")
	}
}

// A rotation keeps the caller's lines and the recent tail. A delivery mark
// written days earlier by a long session is in neither when another session
// rotates the file.
func TestRotationKeepsCompactionMarks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	p := dir + ".hookseen"
	var b strings.Builder
	b.WriteString("parent compaction:abc123 2026-10-03T12:08:57Z proj\n")
	for i := 0; i < 6000; i++ {
		fmt.Fprintf(&b, "other-%d session-%d 2026-10-04T00:00:00Z proj\n", i%50, i)
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	rotateHookseen(p, "someone-else")
	if !alreadyInjected(dir, "parent")["compaction:abc123"] {
		t.Fatal("rotation dropped the packet's delivery mark")
	}
	if got := len(loadSeen(dir)); got > 4100 {
		t.Fatalf("rotation kept %d lines", got)
	}
}
