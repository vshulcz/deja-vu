package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Two aiders started together leave no header AiderSessionAt can call this
// run's: asker() caches only a non-empty answer, so once the header window
// (launch to launch+1m) has closed with nothing resolved, it still walks the
// whole history again on every later call instead of settling on the
// fallback sid (#5295).
//
// A wall-clock comparison stood here before and was flaky on a shared
// runner (a slow run's "first" call could cost as little as a fast one's
// "second"). This pins the same bug on behaviour instead: the history is
// rewritten after the window has closed to a shape a fresh walk would
// resolve, so a re-walk is caught by the id it would return, not by timing.
func TestAiderAskerStopsWalkingOnceTheWindowCloses(t *testing.T) {
	tmp := t.TempDir()
	history := filepath.Join(tmp, ".aider.chat.history.md")

	launch, err := time.ParseInLocation("2006-01-02 15:04:05", "2026-10-01 09:00:00", time.Local)
	if err != nil {
		t.Fatal(err)
	}
	before := "\n# aider chat started at 2026-09-01 09:00:00\n\n#### filler  \n\nok\n\n"
	// Two aiders started in the same launch second, both past offset: nothing
	// says which header is this run's.
	ambiguous := "\n# aider chat started at 2026-10-01 09:00:00\n\n#### one  \n\nok\n\n" +
		"\n# aider chat started at 2026-10-01 09:00:00\n\n#### two  \n\nok\n\n"
	if err := os.WriteFile(history, []byte(before+ambiguous), 0o644); err != nil {
		t.Fatal(err)
	}
	offset := int64(len(before))

	l := &aiderLive{history: history, offset: offset, launch: launch, sid: "aider-test-fallback"}

	if first := l.asker(); first != l.sid {
		t.Fatalf("ambiguous launch: asker() returned %q, want the fallback %q", first, l.sid)
	}

	// The window is long closed, so the fallback is final. Rewrite the
	// history to one header in the same window: a walk that still runs now
	// would resolve to that session's id instead of keeping the fallback.
	resolved := "\n# aider chat started at 2026-10-01 09:00:00\n\n#### one  \n\nok\n\n"
	if err := os.WriteFile(history, []byte(before+resolved), 0o644); err != nil {
		t.Fatal(err)
	}
	if second := l.asker(); second != l.sid {
		t.Fatalf("second call returned %q, want the same fallback %q: asker() is still walking the whole history after the window closed", second, l.sid)
	}
}
