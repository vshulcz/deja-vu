package index

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// zedEnv points every other store somewhere empty and returns the store path.
func zedEnv(t *testing.T) (tmp, db string) {
	t.Helper()
	for _, bin := range []string{"sqlite3", "zstd"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	tmp = t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_GROK_DB", filepath.Join(tmp, "none-grok.db"))
	t.Setenv("DEJA_HERMES_HOME", filepath.Join(tmp, "none-hermes"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db = filepath.Join(tmp, "threads.db")
	t.Setenv("DEJA_ZED_DB", db)
	t.Setenv("DEJA_ZED_ROOT", filepath.Join(tmp, "zed"))
	return tmp, db
}

// zedThread writes one thread, replacing it if it is already there. The body is
// really compressed rather than recorded, so the row is what Zed would store.
func zedThread(t *testing.T, db, id, updated string, texts []string) {
	t.Helper()
	msgs := make([]string, 0, len(texts))
	for i, text := range texts {
		msgs = append(msgs, fmt.Sprintf(`{"User":{"id":"u%d","content":[{"Text":%q}]}}`, i, text))
	}
	body := fmt.Sprintf(`{"version":"0.3.0","title":"pool timeouts","updated_at":%q,"messages":[%s]}`,
		updated, strings.Join(msgs, ","))
	cmd := exec.Command("zstd", "-q", "-3", "-c")
	cmd.Stdin = strings.NewReader(body)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("compress: %v", err)
	}
	stmts := `create table if not exists threads (
    id text primary key, summary text not null, updated_at text not null,
    data_type text not null, data blob not null, parent_id text,
    folder_paths text, folder_paths_order text, created_at text);
` + fmt.Sprintf("insert or replace into threads (id,summary,updated_at,data_type,data,folder_paths) values (%q,'pool timeouts',%q,'zstd',x'%s','[\"/work/app\"]');",
		id, updated, hex.EncodeToString(out.Bytes()))
	c := exec.Command("sqlite3", db)
	c.Stdin = strings.NewReader(stmts)
	if o, err := c.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, o)
	}
}

func zedHits(t *testing.T, dir, marker string) int {
	t.Helper()
	got, err := Search(dir, search.Options{Query: marker, All: true})
	if err != nil {
		t.Fatal(err)
	}
	return len(got)
}

// zedTurns counts the messages holding a marker, across every session that has
// it. Counting sessions cannot see the failure this store risks: a thread that
// comes back whole is one session either way, and the turn inside it doubling
// is what the goose comment describes.
func zedTurns(t *testing.T, dir, marker string) int {
	t.Helper()
	got, err := Search(dir, search.Options{Query: marker, All: true})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range got {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, marker) {
				n++
			}
		}
	}
	return n
}

// zed is the third store in #2075 and the one whose cursor is not the others':
// ParseZedDBSince selects *threads* updated after the watermark and returns each
// whole, so a continued thread hands back the turns already held. That is the
// goose shape, and it is why the store cannot simply be stamped.
func TestTheZedStoreIsAskedOnlyForWhatIsNew(t *testing.T) {
	tmp, db := zedEnv(t)
	zedThread(t, db, "t1", "2026-01-01T10:00:00Z", []string{"marker-zed-one"})

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if zedHits(t, dir, "marker-zed-one") != 1 {
		t.Fatalf("the store was not indexed at all, so this measures nothing")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Files[db].LastUpdated == 0 {
		t.Errorf("the store carries no watermark, so the next pass reads it whole")
	}

	// The thread gains a turn: it comes back whole, earlier turns included.
	zedThread(t, db, "t1", "2026-01-01T11:00:00Z", []string{"marker-zed-one", "marker-zed-two"})
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := zedHits(t, dir, "marker-zed-two"); n != 1 {
		t.Errorf("the turn added to the thread is not indexed: %d hits", n)
	}
	// Counted as turns, not as sessions: the thread is one session however
	// many times its first turn was added.
	if n := zedTurns(t, dir, "marker-zed-one"); n != 1 {
		t.Errorf("the earlier turn is held %d times", n)
	}
	if n := zedTurns(t, dir, "marker-zed-two"); n != 1 {
		t.Errorf("the added turn is held %d times", n)
	}
}

// And a thread the pass did not ask about is still there afterwards: the store
// changes whenever any thread in it does, so the changed-file rule would take
// the quiet ones without fromDatabase knowing this store.
func TestAQuietZedThreadSurvivesAPassThatDidNotAskForIt(t *testing.T) {
	tmp, db := zedEnv(t)
	zedThread(t, db, "quiet", "2026-01-01T09:00:00Z", []string{"marker-zed-quiet"})
	zedThread(t, db, "busy", "2026-01-01T10:00:00Z", []string{"marker-zed-busy"})

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if zedHits(t, dir, "marker-zed-quiet") != 1 || zedHits(t, dir, "marker-zed-busy") != 1 {
		t.Fatalf("both threads were not indexed, so this measures nothing")
	}

	zedThread(t, db, "busy", "2026-01-01T11:00:00Z", []string{"marker-zed-busy", "marker-zed-added"})
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := zedHits(t, dir, "marker-zed-added"); n != 1 {
		t.Errorf("the added turn is not indexed: %d hits", n)
	}
	if n := zedHits(t, dir, "marker-zed-quiet"); n != 1 {
		t.Errorf("the thread the pass did not ask about was dropped: %d hits", n)
	}
	_ = os.Remove(filepath.Join(tmp, "unused"))
}
