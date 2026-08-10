package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The sync write path (writeSessionsWithSync) derives the session Title and
// Touched list from the raw ss it also redacts message-by-message for the
// record log. The Title is the one plaintext field it computes from user prose,
// so a secret in the first message must be scrubbed before it reaches
// sessions.gob — and nothing else in the index dir may hold it either.
func TestNoSecretSurvivesTheSyncBuild(t *testing.T) {
	const secret = "ghp_abcdefghijklmnop0123456789QRSTUV"
	dir := nlIndex(t, "how do I rotate "+secret+" in my deploy pipeline before the audit")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var checked int
	var walk func(string)
	walk = func(p string) {
		info, err := os.Stat(p)
		if err != nil {
			return
		}
		if info.IsDir() {
			subs, _ := os.ReadDir(p)
			for _, s := range subs {
				walk(filepath.Join(p, s.Name()))
			}
			return
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return
		}
		checked++
		if strings.Contains(string(b), secret) {
			t.Errorf("raw secret survived into %s", p)
		}
	}
	for _, e := range entries {
		walk(filepath.Join(dir, e.Name()))
	}
	if checked == 0 {
		t.Fatal("no index files were read")
	}
}
