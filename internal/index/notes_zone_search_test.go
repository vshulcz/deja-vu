package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/query"
)

// Only `deja index` regrouped drifted note buckets (#1058), so a search, `deja
// last` or the hook served the same local day as two rows for as long as the
// user never typed that command.
func TestSearchRegroupsNoteBucketsAfterAZoneChange(t *testing.T) {
	tmp := t.TempDir()
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	body := `{"ts":"2026-08-01T23:15:00Z","project":"proj","text":"the pool cap is 20"}` + "\n" +
		`{"ts":"2026-08-01T23:30:00Z","project":"proj","text":"the ticker window stays at 30s"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	time.Local = time.UTC

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := noteBuckets(t, dir); len(got) != 1 {
		t.Fatalf("one zone, one day: buckets = %v", got)
	}

	// Nine hours east: both notes belong to the next day here, and a note
	// written since lands in the bucket this machine would build.
	time.Local = time.FixedZone("east", 9*60*60)
	if err := os.WriteFile(notes, []byte(body+`{"ts":"2026-08-02T00:10:00Z","project":"proj","text":"rotate the certificate"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(dir, query.Options{}, false, nil); err != nil {
		t.Fatal(err)
	}
	got := noteBuckets(t, dir)
	if len(got) != 1 || got[0] != "deja-2026-08-02-proj" {
		t.Fatalf("one local day served as %v, want one bucket deja-2026-08-02-proj", got)
	}
}

func noteBuckets(t *testing.T, dir string) []string {
	t.Helper()
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range metas {
		if m.Harness == "deja" {
			out = append(out, m.ID)
		}
	}
	return out
}
