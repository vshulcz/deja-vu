package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A transcript sharing an id with one in another project, added after the
// first pass, was mined together with the row's copy: the error in one file
// and the remedy in the other made a second sighting of another session's
// fix, which a rebuild never saw (#5504). The tables a pass writes must be the
// ones a rebuild of the same stores writes.
func TestSharedIDCopyFixesMatchARebuild(t *testing.T) {
	for _, mode := range []string{"copy-after", "copy-before", "remove-row", "remove-other", "rewrite-copy", "append-row", "control-other-id"} {
		for _, proj := range []string{"fork2", "aa"} {
			t.Run(mode+"/"+proj, func(t *testing.T) {
				h := newTwoWayEnv(t)
				orig := hPrompt("app", "s6", 6, 0, "why does the pool fail") +
					hBash("app", "s6", 6, 1, "a1", "make migrate", "dial tcp 127.0.0.1:7432: connect: connection refused\n", true)
				copyOf := func(id string) string {
					return hPrompt(proj, id, 6, 0, "copy: the pool fails") +
						hEdit(proj, id, 6, 2, "e1", "kafka.go", "a", "b") +
						hBash(proj, id, 6, 3, "b1", "make migrate", "ok", false) +
						hBash(proj, id, 6, 4, "b2", "make seed", "dial tcp 127.0.0.1:5432: connect: connection refused\n", true) +
						hEdit(proj, id, 6, 5, "e3", "seed.go", "a", "b") +
						hBash(proj, id, 6, 6, "b3", "make seed", "ok", false)
				}
				// A second session with the same wall, so it is a known error.
				other := hPrompt("svc", "s4", 7, 0, "why does the ledger fail") +
					hBash("svc", "s4", 7, 1, "a2", "make migrate", "dial tcp 127.0.0.1:6432: connect: connection refused\n", true) +
					hEdit("svc", "s4", 7, 2, "e2", "config.go", "a", "b") +
					hBash("svc", "s4", 7, 3, "b2", "make migrate", "ok", false)
				h.put("app", "s6", orig)
				h.put("svc", "s4", other)
				if mode != "copy-after" && mode != "control-other-id" {
					h.put(proj, "s6", copyOf("s6"))
				}
				inc := filepath.Join(h.tmp, "inc")
				if err := Ensure(inc, "", false, nil); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "control-other-id":
					h.put(proj, "s66", copyOf("s66"))
				case "copy-after":
					h.put(proj, "s6", copyOf("s6"))
				case "append-row":
					// The pass appends the row's new output after the copy's
					// in the log, where a rebuild writes it before.
					h.add("app", "s6", hBash("app", "s6", 6, 8, "a9", "make migrate", "dial tcp 127.0.0.1:8432: connect: connection refused\n", true))
				case "rewrite-copy":
					h.put(proj, "s6", copyOf("s6")+hBash(proj, "s6", 6, 7, "b4", "make seed", "ok", false))
				case "remove-row", "remove-other":
					// Removing the copy the row names hands the row to the other.
					m, err := readManifest(inc)
					if err != nil {
						t.Fatal(err)
					}
					gone, kept := h.path("app", "s6"), h.path(proj, "s6")
					if m.Sessions["claude:s6"].Path != gone {
						gone, kept = kept, gone
					}
					if mode == "remove-other" {
						gone = kept
					}
					if err := os.Remove(gone); err != nil {
						t.Fatal(err)
					}
				}
				if err := Ensure(inc, "", false, nil); err != nil {
					t.Fatal(err)
				}
				if m, err := readManifest(inc); err != nil || (mode != "control-other-id" && !strings.HasPrefix(mode, "remove") && !m.Sessions["claude:s6"].Shared) {
					t.Fatalf("the two s6 transcripts do not collide, so this measures nothing: %v", err)
				}
				// Rebuilt from a copy, which keeps a removed transcript's
				// session as the index holding it does.
				fresh := filepath.Join(h.tmp, "fresh")
				copyDir(t, inc, fresh)
				if err := Ensure(fresh, "", true, nil); err != nil {
					t.Fatal(err)
				}
				a, b := takeSnap(t, inc), takeSnap(t, fresh)
				for _, k := range []string{"fixes", "fixraw", "friction texts"} {
					if a.sidecars[k] != b.sidecars[k] {
						t.Errorf("%s after the pass:\n  %s\nafter a rebuild:\n  %s", k, a.sidecars[k], b.sidecars[k])
					}
				}
			})
		}
	}
}
