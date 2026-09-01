package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// omp, OpenClaw and prime write pi-format transcripts and have offset parsers
// in the registry, but the append gate never listed them, so every pass that
// saw one new turn re-read the whole file. pi, the fourth reader of the same
// shape, has been on the list all along.
//
// What has to hold before they join it: an appended turn lands in the session
// it continues, with the identity the whole read gives — which is what #2870's
// header fix made true for this reader.
func TestThePiShapedStoresAppendInsteadOfRereading(t *testing.T) {
	head := func(id, cwd string) string {
		return `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-01-02T03:04:05Z","cwd":"` + cwd + `"}` + "\n"
	}
	turn := func(role, ts, text string) string {
		return `{"type":"message","id":"m-` + ts + `","timestamp":"` + ts +
			`","message":{"role":"` + role + `","content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
	}

	cases := []struct {
		harness string
		env     string
		// rel is the transcript path under the store root.
		rel string
	}{
		{"omp", "DEJA_OMP_ROOT", filepath.Join("-tmp-demo", "2026-01-02T03-04-05_ses-a.jsonl")},
		{"openclaw", "DEJA_OPENCLAW_ROOT", filepath.Join("agent-1", "sessions", "ses-b.jsonl")},
		{"prime", "DEJA_PRIME_ROOT", filepath.Join("-tmp-demo", "2026-01-02T03-04-05_ses-c.jsonl")},
	}
	for _, tc := range cases {
		t.Run(tc.harness, func(t *testing.T) {
			root := t.TempDir()
			dir := t.TempDir()
			setHome(t, filepath.Join(root, "home"))
			t.Setenv("USERPROFILE", os.Getenv("HOME"))
			store := filepath.Join(root, tc.harness)
			t.Setenv(tc.env, store)
			path := filepath.Join(store, tc.rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			id := "ses-" + tc.harness
			body := head(id, "/tmp/demo") + turn("user", "2026-01-02T03:04:10Z", "why does pgbouncer time out")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			before := currentFileStateFor(t, path)

			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(turn("assistant", "2026-01-02T03:05:00Z", "raised the pool to 40")); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			after := currentFileStateFor(t, path)
			if !canAppendIncremental(map[string]FileState{path: after}, map[string]FileState{path: before}) {
				t.Fatalf("%s still re-reads the whole file for one appended turn", tc.harness)
			}
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}

			// The appended turn has to be findable, in the session it continues
			// and not beside it — the failure the header fix removed.
			all, err := Recent(dir, 50)
			if err != nil {
				t.Fatal(err)
			}
			var mine []string
			for _, s := range all {
				if s.Harness == tc.harness {
					mine = append(mine, s.ID)
				}
			}
			if len(mine) != 1 {
				t.Fatalf("%s: %d sessions after the append (%v), want the one it continued",
					tc.harness, len(mine), mine)
			}
			if mine[0] != id {
				t.Errorf("%s: session id = %q, want the header's %q", tc.harness, mine[0], id)
			}

			// Searched for, not listed: Recent carries metadata and no message
			// bodies, and asserting on those found nothing even after a full
			// rebuild — the instrument, not the index.
			hits, err := Search(dir, query.Options{Query: "raised the pool to 40", Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, s := range hits {
				if s.Harness != tc.harness {
					continue
				}
				for _, m := range s.Messages {
					if strings.Contains(m.Text, "raised the pool to 40") {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("%s: the appended turn is not in the index", tc.harness)
			}
		})
	}
}
