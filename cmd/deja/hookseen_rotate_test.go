package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Past 1MB the dedup file used to freeze, permanently breaking dedup so the
// per-prompt hook re-injected the same sessions forever. Rotation must keep the
// current session's dedup working and bound the file size.
func TestHookseenRotatesInsteadOfFreezing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "idx.db")
	p := dir + ".hookseen"
	// This session's line goes first, before a megabyte of other sessions'.
	// Written last it lands inside the tail rotation keeps anyway, and the rule
	// that saves it — keep every line of the current session, wherever it sits —
	// is never exercised: removing that rule left this test green.
	var b strings.Builder
	b.WriteString("mysession alreadyseen1\n")
	for i := 0; b.Len() < (1<<20)+1000; i++ {
		b.WriteString("othersess" + strings.Repeat("x", 40) + " sid\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(p)
	if before.Size() <= 1<<20 {
		t.Fatal("premise: file should start over 1MB")
	}

	// A new injection for mysession triggers rotation and still records.
	rememberInjected(dir, "mysession", []model.Session{{ID: "newone"}})

	after, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() > 1<<20 {
		t.Errorf("file did not shrink on rotation: %d bytes", after.Size())
	}
	seen := alreadyInjected(dir, "mysession")
	// The pre-rotation line for this session must survive (dedup keeps working),
	// and the new one must be recorded.
	if !seen["alreadyseen1"] {
		t.Error("rotation dropped the current session's earlier dedup entry")
	}
	if !seen["newone"] {
		t.Error("the new injection was not recorded after rotation")
	}
}
