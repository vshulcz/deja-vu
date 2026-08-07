package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end guard for #1098: an accented word stored decomposed (NFD, "e" +
// U+0301) must be found by a query typed precomposed (NFC, U+00E9), and the
// reverse. This exercises the whole CLI path — ingest canonicalisation, the
// postings, and the search package's snippet layer — where the index-level
// test alone would not, because search.RunDetailed re-filters on the raw query.
func TestSearchFindsAccentAcrossNormalisation(t *testing.T) {
	const nfc = "caf\u00e9"       // café precomposed: U+00E9
	const nfd = "caf\u0065\u0301" // café decomposed: e + U+0301

	forms := map[string]string{"nfc": nfc, "nfd": nfd}
	for sName, sForm := range forms {
		for qName, qForm := range forms {
			t.Run(sName+"_stored_"+qName+"_query", func(t *testing.T) {
				hermeticEnv(t)
				store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
				if err := os.MkdirAll(store, 0o755); err != nil {
					t.Fatal(err)
				}
				line := `{"type":"user","message":{"role":"user","content":"deployed to the ` + sForm + ` server"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"a1","cwd":"/proj"}`
				if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := captureRun(t, "index"); err != nil {
					t.Fatal(err)
				}
				out, err := captureRun(t, qForm)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out, "a1") {
					t.Fatalf("stored %s, query %s: session a1 not in output:\n%s", sName, qName, out)
				}
			})
		}
	}
}
