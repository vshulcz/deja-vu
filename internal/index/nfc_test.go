package index

import (
	"os"
	"path/filepath"
	"testing"

	search "github.com/vshulcz/deja-vu/internal/query"
)

// An accented word can be typed and stored in either Unicode normalisation:
// precomposed (NFC) or decomposed (NFD, base + combining mark). Some editors
// and the macOS filesystem emit NFD, so a store and a query can carry the same
// word in different forms. The fold table covers Latin, Cyrillic and Greek, so
// all four combinations must find the record for each (#1098).
func TestNFCNFDSearchAllCombos(t *testing.T) {
	scripts := []struct{ name, nfc, nfd string }{
		{"latin", "café", "café"},  // café: é vs e+U+0301
		{"cyrillic", "мой", "мой"}, // мой: й vs и+U+0306
		{"greek", "ά", "ά"},        // ά: U+03AC vs α+U+0301
	}
	for _, sc := range scripts {
		stored := map[string]string{"nfc": sc.nfc, "nfd": sc.nfd}
		queries := map[string]string{"nfc": sc.nfc, "nfd": sc.nfd}
		for sName, sForm := range stored {
			for qName, qForm := range queries {
				t.Run(sc.name+"_"+sName+"_stored_"+qName+"_query", func(t *testing.T) {
					tmp := t.TempDir()
					claudeRoot := filepath.Join(tmp, "claude")
					proj := filepath.Join(claudeRoot, "-tmp-app")
					if err := os.MkdirAll(proj, 0o755); err != nil {
						t.Fatal(err)
					}
					line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"deployed to the ` + sForm + ` server"}}` + "\n"
					if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
						t.Fatal(err)
					}
					t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
					dir := filepath.Join(tmp, "index.db")
					t.Setenv("DEJA_INDEX_DIR", dir)
					if err := Ensure(dir, "", true, nil); err != nil {
						t.Fatal(err)
					}
					got, err := Search(dir, search.Options{Query: qForm, All: true})
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, s := range got {
						if s.ID == "s1" {
							found = true
						}
					}
					if !found {
						t.Fatalf("%s stored %s query %s: want s1, got %d sessions", sc.name, sName, qName, len(got))
					}
				})
			}
		}
	}
}
