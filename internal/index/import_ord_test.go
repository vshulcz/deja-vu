package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Every new session asked nextSessionOrd for its ordinal, and that walks the
// whole manifest — so importing n sessions cost O(n²): a 100k batch took 306s
// against 1.9s for forgetting the same 100k (#1024).
func TestImportedOrdinalsAreDistinctAndCheap(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dir := filepath.Join(tmp, "index.db")
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var body []byte
	const n = 300
	for i := 0; i < n; i++ {
		b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: fixedID(i), Project: "work/api",
			Role: "user", Text: "record about the pool cap"})
		if err != nil {
			t.Fatal(err)
		}
		body = append(append(body, b...), '\n')
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := Import(dir, exp); err != nil || got != n {
		t.Fatalf("import: %d records, %v", got, err)
	}

	// The ordinal is what postings point at, so a repeat would merge two
	// sessions' hits into one row.
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uint32]string{}
	for _, m := range metas {
		if prev, ok := seen[m.Ord]; ok {
			t.Fatalf("sessions %s and %s share ordinal %d", prev, m.ID, m.Ord)
		}
		seen[m.Ord] = m.ID
	}
	if len(seen) != n {
		t.Errorf("got %d distinct ordinals for %d sessions", len(seen), n)
	}

	// A second batch continues above the first rather than colliding with it.
	body = nil
	for i := n; i < n+50; i++ {
		b, _ := json.Marshal(SyncRecord{Harness: "claude", SessionID: fixedID(i), Project: "work/api",
			Role: "user", Text: "another record"})
		body = append(append(body, b...), '\n')
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-2.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	metas, err = AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen = map[uint32]string{}
	for _, m := range metas {
		if prev, ok := seen[m.Ord]; ok {
			t.Fatalf("after the second batch, %s and %s share ordinal %d", prev, m.ID, m.Ord)
		}
		seen[m.Ord] = m.ID
	}
	if len(seen) != n+50 {
		t.Errorf("got %d distinct ordinals for %d sessions", len(seen), n+50)
	}
}

func fixedID(i int) string {
	const digits = "0123456789"
	return "s" + string([]byte{digits[i/100%10], digits[i/10%10], digits[i%10]})
}
