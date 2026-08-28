package main

import (
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

// A full `.hookseen` used to stop the dedup dead, and #1967 replaced that with
// a rotation — "the per-prompt hook then re-injected the same sessions turn
// after turn". The token writer three functions below kept the old behaviour,
// so the surfaces whose whole job is not repeating themselves — the per-prompt
// block, the two tool hooks — lost their dedup once the file filled, until some
// session injection happened to rotate it (#2164).
func TestTheTokenDedupSurvivesAFullHookseen(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := dir + ".hookseen"
	var b strings.Builder
	for b.Len() < (1<<20)+4096 {
		b.WriteString("ses-old some-token 2026-01-01T00:00:00Z\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	rememberInjectedIDs(dir, "ses1", "tok-1")
	if !alreadyInjected(dir, "ses1")["tok-1"] {
		t.Errorf("a token written while the file was full does not read back as seen")
	}
	// The rotation kept this session's line rather than starting the file over.
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > 1<<20 {
		t.Errorf("the file is still %d bytes, so nothing rotated", fi.Size())
	}

	// The session writer is unchanged, and the two keep working together: a
	// session recorded after the rotation is still seen, and so is a second
	// token.
	rememberInjected(dir, "ses1", []model.Session{{ID: "sess-a"}})
	rememberInjectedIDs(dir, "ses1", "tok-2")
	seen := alreadyInjected(dir, "ses1")
	for _, want := range []string{"tok-1", "sess-a", "tok-2"} {
		if !seen[want] {
			t.Errorf("%q is not remembered after the file filled and rotated", want)
		}
	}
	// And the rotation cannot be talked into keeping a megabyte of one
	// session's own lines: that was what made every later write rotate again.
	var mine strings.Builder
	for mine.Len() < (1<<20)+4096 {
		mine.WriteString("ses1 token-of-my-own 2026-01-01T00:00:00Z\n")
	}
	if err := os.WriteFile(p, []byte(mine.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	rememberInjectedIDs(dir, "ses1", "tok-3")
	fi, err = os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > 1<<20 {
		t.Errorf("the file is %d bytes of one session's own lines, so the next write rotates it again", fi.Size())
	}
	if !alreadyInjected(dir, "ses1")["tok-3"] {
		t.Errorf("the token written through that rotation is not remembered")
	}
}
