package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// With imports and no configured peer, the Sync section returned early on "no
// other machines yet" — while the manifest knew which machines the sessions had
// come from, and recall was answering with them (#2378).
func TestDoctorNamesTheMachinesThatSentWork(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	importFrom := func(machine string, ids ...string) {
		t.Helper()
		batch := t.TempDir()
		// Written as a batch is written on the wire: `origin` is the machine
		// that exported it, and its type is unexported here.
		var lines []string
		for _, id := range ids {
			lines = append(lines, fmt.Sprintf(
				`{"harness":"claude","session_id":%q,"project":"work/app","role":"user",`+
					`"text":"work from %s","time":%q,"origin":%q}`,
				id, machine, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC).Format(time.RFC3339), machine))
		}
		if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runSync(dir, []string{"import", batch}); err != nil {
			t.Fatal(err)
		}
	}
	importFrom("laptop.local", "a0", "a1", "a2")
	importFrom("desktop", "b0", "b1")

	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	sync := doctorSection(out, "Sync:")
	if strings.Contains(sync, "no other machines yet") {
		t.Errorf("doctor reports no machines while holding work from two:\n%s", sync)
	}
	for _, want := range []string{"laptop.local", "desktop"} {
		if !strings.Contains(sync, want) {
			t.Errorf("doctor does not name %q as a machine work came from:\n%s", want, sync)
		}
	}

	// A machine with neither peers nor imports still says so.
	hermeticEnv(t)
	empty := index.DefaultDir()
	if err := index.Ensure(empty, "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doctorSection(out, "Sync:"), "no other machines yet") {
		t.Errorf("a machine that has exchanged nothing lost its line:\n%s", doctorSection(out, "Sync:"))
	}
}

// doctorSection returns the lines of one section of the report.
func doctorSection(out, header string) string {
	i := strings.Index(out, header)
	if i < 0 {
		return "(no " + header + " section)"
	}
	rest := out[i:]
	if j := strings.Index(rest[len(header):], "\n\n"); j >= 0 {
		return rest[:len(header)+j]
	}
	return rest
}
