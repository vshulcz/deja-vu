package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The import path counts its redactions, but a count is not proof the secret
// left the stored bytes — the cooccur leak (its count was right while the
// sidecar held raw text) is exactly that failure. Peer-pushed text is
// attacker-influenced, so this imports a secret and asserts it survives in no
// file of the index: records, manifest title, or postings.
func TestImportLeavesNoRawSecretOnDisk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	in := filepath.Join(home, "export")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	const secret = "ghp_abcdefghijklmnop0123456789QRSTUV"
	b, err := json.Marshal(SyncRecord{
		Harness: "claude", SessionID: "s1", Project: "p", Role: "user",
		Text: "please rotate " + secret + " before the deploy audit",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in, "deja-sync.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, "idx")
	if _, err := Import(dir, in); err != nil {
		t.Fatal(err)
	}

	checked := 0
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
		data, err := os.ReadFile(p)
		if err != nil {
			return
		}
		checked++
		if strings.Contains(string(data), secret) {
			t.Errorf("imported secret survived into %s", p)
		}
	}
	walk(dir)
	if checked == 0 {
		t.Fatal("no index files were read")
	}
}
