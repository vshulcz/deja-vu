package index

import (
	"bytes"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

// zedThreadRow builds one row the way Zed writes it: the thread document
// compressed into a zstd frame, addressed by a hex blob literal.
func zedThreadRow(t *testing.T, id, title, updated, text string) string {
	t.Helper()
	body := `{"version":"0.3.0","title":"` + title + `","updated_at":"` + updated + `","messages":[` +
		`{"User":{"id":"u-` + id + `","content":[{"Text":"` + text + `"}]}},` +
		`{"Agent":{"content":[{"Text":"reply to ` + id + `"}],"tool_results":{}}}]}`
	cmd := exec.Command("zstd", "-q", "-3", "-c")
	cmd.Stdin = strings.NewReader(body)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("compress thread: %v", err)
	}
	return "insert or replace into threads (id,summary,updated_at,data_type,data,folder_paths,created_at) values ('" +
		id + "','" + title + "','" + updated + "','zstd',x'" + hex.EncodeToString(out.Bytes()) +
		"','/w/app','" + updated + "');"
}

// A zed thread untouched since the watermark must survive an incremental pass
// triggered by a change to the shared threads.db — the failure every db-backed
// harness here is one mistake away from, since the whole store is one file.
func TestZedIncrementalKeepsUntouchedThreads(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd not available")
	}
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "index")
	db := filepath.Join(tmp, "zed", "threads", "threads.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	schema := `create table if not exists threads (
    id text primary key, summary text not null, updated_at text not null,
    data_type text not null, data blob not null, parent_id text,
    folder_paths text, folder_paths_order text, created_at text);`
	run := func(sql string) {
		t.Helper()
		cmd := exec.Command("sqlite3", db)
		cmd.Stdin = strings.NewReader(sql)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("seed: %v: %s", err, out)
		}
	}

	run(schema + zedThreadRow(t, "zed-old", "old thread", "2026-07-19T10:00:00+00:00", "oldzedfact about the retry budget"))
	if err := Ensure(dir, "", false, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if hits, _ := Search(dir, search.Options{Query: "oldzedfact", All: true}); len(hits) == 0 {
		t.Fatal("old zed thread not indexed on first pass")
	}

	run(zedThreadRow(t, "zed-new", "new thread", "2026-07-19T11:00:00+00:00", "newzedfact about the cache key"))
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(db, future, future)
	if err := Ensure(dir, "", false, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if hits, _ := Search(dir, search.Options{Query: "newzedfact", All: true}); len(hits) == 0 {
		t.Fatal("new zed thread not indexed on incremental pass")
	}
	if hits, _ := Search(dir, search.Options{Query: "oldzedfact", All: true}); len(hits) == 0 {
		t.Fatal("REGRESSION: untouched zed thread vanished after incremental pass")
	}
}

// Discovery has to find the store through Zed's own path logic, and the hit has
// to carry the harness name so `--harness zed` and the search tag work.
func TestZedStoreIsDiscoveredAndTagged(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd not available")
	}
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "index")
	db := filepath.Join(tmp, "zed", "threads", "threads.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	schema := `create table threads (
    id text primary key, summary text not null, updated_at text not null,
    data_type text not null, data blob not null, parent_id text,
    folder_paths text, folder_paths_order text, created_at text);`
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = strings.NewReader(schema + zedThreadRow(t, "zed-tagged", "tagged", "2026-07-19T12:00:00+00:00", "zedtaggedfact about the parser"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	if err := Ensure(dir, "", false, os.Stderr); err != nil {
		t.Fatal(err)
	}
	hits, err := Search(dir, search.Options{Query: "zedtaggedfact", All: true})
	if err != nil || len(hits) == 0 {
		t.Fatalf("hits = %#v, err = %v", hits, err)
	}
	if hits[0].Harness != "zed" {
		t.Fatalf("harness = %q, want zed", hits[0].Harness)
	}
	if hits[0].Project != "w/app" {
		t.Fatalf("project = %q, want the folder_paths project", hits[0].Project)
	}
	// Filtering by harness must reach it, and must not reach it under another
	// name — a tag that matches everything is not a tag.
	tagged, err := Search(dir, search.Options{Query: "zedtaggedfact", All: true, Harness: "zed"})
	if err != nil || len(tagged) == 0 {
		t.Fatalf("--harness zed found nothing: %#v, err = %v", tagged, err)
	}
	other, err := Search(dir, search.Options{Query: "zedtaggedfact", All: true, Harness: "claude"})
	if err != nil || len(other) != 0 {
		t.Fatalf("--harness claude reached a zed thread: %#v, err = %v", other, err)
	}
}
