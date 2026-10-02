package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// gjc and Kimchi reopen a session only from the directory it ran in: from
// anywhere else gjc refuses ("is in another project") and Kimchi offers to fork
// it. The header line records that directory, and resume printed no cd
// (#4395, #4400).
func TestResumeGjcAndKimchiRunInTheSessionDirectory(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := func(name, cwd string) string {
		t.Helper()
		p := filepath.Join(tmp, name+".jsonl")
		header := `{"type":"session","version":3,"id":"01a0f93c-7bdc-7074-a7dd-641dd530ce0c","timestamp":"2026-10-01T21:00:00.000Z"`
		if cwd != "" {
			// Encoded, as the clients write it: a Windows path spliced in
			// raw is `C:\Users\…`, an invalid JSON escape, and the header
			// did not decode at all.
			enc, _ := json.Marshal(cwd)
			header += `,"cwd":` + string(enc)
		}
		body := header + "}\n" + `{"type":"message","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, c := range []struct{ harness, cmd string }{
		{"gjc", "gjc --resume 01a0f93c-7bdc-7074-a7dd-641dd530ce0c"},
		{"kimchi", "kimchi --session 01a0f93c-7bdc-7074-a7dd-641dd530ce0c"},
		// Senpi does what Kimchi does: from another directory it asks to fork
		// the session into it (#4426).
		{"senpi", "senpi --session 01a0f93c-7bdc-7074-a7dd-641dd530ce0c"},
		// prime-agent refuses a session from another project and points at
		// --fork (#4408), so a gone directory needs the same line.
		{"prime", "prime-agent --resume 01a0f93c-7bdc-7074-a7dd-641dd530ce0c"},
	} {
		t.Run(c.harness, func(t *testing.T) {
			s := model.Session{Harness: c.harness, ID: "01a0f93c-7bdc-7074-a7dd-641dd530ce0c", Path: transcript(c.harness, proj)}
			dir, cmd, err := resumeCommand(s)
			if err != nil {
				t.Fatal(err)
			}
			if dir != proj || cmd != c.cmd {
				t.Fatalf("got (%q, %q), want (%q, %q)", dir, cmd, proj, c.cmd)
			}
			if note := resumeDirGoneNote(s, dir); note != "" {
				t.Errorf("a note about a directory that is there: %q", note)
			}

			// The directory is gone: no cd that stops the command, and a line
			// saying what the client will do instead.
			gone := filepath.Join(tmp, "gone")
			s.Path = transcript(c.harness+"-gone", gone)
			dir, _, err = resumeCommand(s)
			if err != nil || dir != "" {
				t.Fatalf("dir = %q, err = %v; want no cd into a directory that is gone", dir, err)
			}
			if note := resumeDirGoneNote(s, dir); !strings.Contains(note, gone) || !strings.Contains(note, "fork") {
				t.Errorf("note = %q, want it to name %s and the fork", note, gone)
			}

			// A header with no cwd: nothing to cd into, nothing to say.
			s.Path = transcript(c.harness+"-nocwd", "")
			if dir, _, _ := resumeCommand(s); dir != "" {
				t.Errorf("dir = %q from a header with no cwd", dir)
			}
			if note := resumeDirGoneNote(s, ""); note != "" {
				t.Errorf("note = %q from a header with no cwd", note)
			}
		})
	}
}
