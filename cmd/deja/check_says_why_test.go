package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// `deja check -` is the form a person types; the hook it shares its body with
// is the one that must stay silent. Today both are: an empty plan, a plan with
// nothing to search on, a store with no index, and a plan deja looked up and
// found nothing for all print nothing and exit 0, so the reader cannot tell
// which of the four happened (#2564).
func TestCheckSaysWhyItFoundNothing(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")

	say := func(plan string) string {
		t.Helper()
		var out, errs bytes.Buffer
		if err := runCheckTo(dir, []string{"-"}, strings.NewReader(plan), &out, &errs); err != nil {
			t.Fatalf("check: %v", err)
		}
		if out.Len() != 0 {
			t.Fatalf("stdout is for findings only, got %q", out.String())
		}
		return errs.String()
	}

	// No index at all: deja never looked, and saying nothing claims it did.
	if got := say("Plan: change the pgbouncer pool size."); !strings.Contains(got, "index") {
		t.Errorf("with no index, check said %q", strings.TrimSpace(got))
	}

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// A plan with nothing in it to search on.
	if got := say("   \n  "); !strings.Contains(got, "plan") {
		t.Errorf("on an empty plan, check said %q", strings.TrimSpace(got))
	}
	// And a plan it really did look up.
	got := say("Plan: change the pgbouncer pool size and the retry budget.")
	if !strings.Contains(got, "nothing") {
		t.Errorf("after looking and finding nothing, check said %q", strings.TrimSpace(got))
	}
}
