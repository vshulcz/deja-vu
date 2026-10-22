package index

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// The line for a store given up says how long it sat and how to wait instead
// (#5517).
func TestStallNarration(t *testing.T) {
	for _, c := range []struct {
		idle []time.Duration
		want string
	}{
		{[]time.Duration{10 * time.Minute}, "no progress for 10m0s; DEJA_STORE_TIMEOUT=0 waits"},
		{[]time.Duration{0}, "a read in the same directory stopped moving; DEJA_STORE_TIMEOUT=0 waits"},
		{[]time.Duration{time.Minute, 2 * time.Minute}, "2 reads given up, no progress for 2m0s; DEJA_STORE_TIMEOUT=0 waits"},
	} {
		if got := stallNarration(c.idle); got != c.want {
			t.Errorf("stallNarration(%v) = %q, want %q", c.idle, got, c.want)
		}
	}
}

// Nothing unread, or no index yet, carries nothing.
func TestUnreadStoresCarryWithNothingToCarry(t *testing.T) {
	dir := t.TempDir()
	if got := unreadStoresCarry(dir, "", nil, nil); got != nil {
		t.Errorf("no unread stores carried %d sessions", len(got))
	}
	if got := unreadStoresCarry(filepath.Join(dir, "none"), "", map[string]bool{"/x/state.db": true}, nil); got != nil {
		t.Errorf("no index carried %d sessions", len(got))
	}
}

// A store that read part of what it holds and gave up on the rest says both.
func TestAStoreWithSessionsAndAStalledReadSaysBoth(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub")
	}
	home := t.TempDir()
	setHome(t, home)
	hermes := filepath.Join(home, ".hermes")
	t.Setenv("DEJA_HERMES_HOME", hermes)
	t.Setenv("DEJA_HERMES_DB", "")
	t.Setenv("DEJA_HERMES_PG_DSN", "")
	for _, db := range []string{filepath.Join(hermes, "state.db"), filepath.Join(hermes, "profiles", "b", "state.db")} {
		if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The profile store stalls, the root one answers.
	bin := t.TempDir()
	script := `#!/bin/sh
case "$*" in
*"json_object('deja',1)"*) echo '{"deja":1}'; exit 0;;
*"from messages"*"order by session_id"*) ;;
*) exit 0;;
esac
case "$*" in
*profiles*) exec sleep 30;;
esac
printf '{"session_id":"h1","role":"user","content":"partneedle","timestamp":1790000001}\n'
`
	if err := os.WriteFile(filepath.Join(bin, "sqlite3"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DEJA_STORE_TIMEOUT", "300ms")
	sources.ResetSQLite3Probe()
	t.Cleanup(sources.ResetSQLite3Probe)
	sources.DiagSnapshot()
	t.Cleanup(func() { sources.DiagSnapshot() })

	var out bytes.Buffer
	ss := loadProgress("hermes", &out)
	if len(ss) != 1 {
		t.Fatalf("read %d sessions, want the root store's one", len(ss))
	}
	want := "deja: hermes: 1 session, 1 message — 1 path could not be read at all — no progress for 300ms; DEJA_STORE_TIMEOUT=0 waits"
	if !strings.Contains(out.String(), want) {
		t.Errorf("narration:\n%s\nwant %q", out.String(), want)
	}

	// Built, then asked what to carry for the root store as if unread.
	root := filepath.Join(hermes, "state.db")
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	unread := map[string]bool{root: true}
	if got := unreadStoresCarry(dir, "hermes", unread, nil); len(got) != 1 || got[0].ID != "h1" {
		t.Errorf("carried %v, want h1", got)
	}
	if got := unreadStoresCarry(dir, "hermes", unread, []model.Session{{Harness: "hermes", ID: "h1"}}); len(got) != 0 {
		t.Errorf("carried %d sessions this pass already read", len(got))
	}
	if got := unreadStoresCarry(dir, "claude", unread, nil); len(got) != 0 {
		t.Errorf("a claude pass carried %d hermes sessions", len(got))
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if got := unreadStoresCarry(dir, "", unread, nil); len(got) != 0 {
		t.Errorf("a store gone since the walk carried %d sessions", len(got))
	}
}
