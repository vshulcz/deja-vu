package index

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A Postgres-backed Hermes has no state.db to stat, so it went dark: the whole
// harness was invisible while doctor called the store healthy. It is indexed
// from a query fingerprint now, and picks up new rows incrementally (#1018).
func TestHermesPostgresIsIndexedAndIncremental(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	t.Setenv("DEJA_HERMES_PG_DSN", "postgres://fake/hermes")

	// A tiny fake store: two rows, growing to three between reads.
	rows := []string{
		`{"session_id":"h1","role":"user","content":"we chose bounded channels for backpressure","timestamp":1785000000}`,
		`{"session_id":"h1","role":"assistant","content":"pgx was pinned instead","timestamp":1785000060}`,
	}
	fake := func(_, sql string) ([]byte, error) {
		if strings.HasPrefix(sql, "select count(*)") {
			return []byte(fmt.Sprintf("%d|%d\n", len(rows), 1785000000+60*(len(rows)-1))), nil
		}
		return []byte("[" + strings.Join(rows, ",") + "]"), nil
	}
	defer sources.SetHermesPGRunner(fake)()

	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	hits, err := Search(dir, query.Options{Query: "backpressure"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("a postgres-backed hermes session was not indexed")
	}
	if hits[0].Harness != "hermes" {
		t.Fatalf("harness = %q", hits[0].Harness)
	}

	// A new row lands; an incremental pass must find it without a forced rebuild.
	rows = append(rows, `{"session_id":"h2","role":"user","content":"the retry queue overflowed under load","timestamp":1785000120}`)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	hits, err = Search(dir, query.Options{Query: "retry queue overflowed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("the incremental pass missed the new postgres row")
	}
}

// An unset DSN is an env that was turned off for one run, not a deletion: the
// sessions already indexed must survive it, the way an unmounted disk's do.
func TestHermesPostgresSurvivesADisabledDSN(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	t.Setenv("DEJA_HERMES_PG_DSN", "postgres://fake/hermes")

	fake := func(_, sql string) ([]byte, error) {
		if strings.HasPrefix(sql, "select count(*)") {
			return []byte("1|1785000000\n"), nil
		}
		return []byte(`[{"session_id":"h1","role":"user","content":"a decision about queue backpressure","timestamp":1785000000}]`), nil
	}
	restore := sources.SetHermesPGRunner(fake)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	restore()

	// DSN gone this run. The store must not be tombstoned out of the index.
	t.Setenv("DEJA_HERMES_PG_DSN", "")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	hits, err := Search(dir, query.Options{Query: "backpressure"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("a disabled DSN dropped the postgres sessions as if forgotten")
	}
}
