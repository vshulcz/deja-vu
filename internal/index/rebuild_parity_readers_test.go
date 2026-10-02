package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolateStores points HOME and the XDG dirs at tmp, so every store deja
// would read sits under the test's own directory.
func isolateStores(t *testing.T, tmp string) {
	t.Helper()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, "cache"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
}

// sessionView is what the index holds for one session: everything an
// incremental pass and a rebuild must agree on.
func sessionView(t *testing.T, dir, harness, id string) string {
	t.Helper()
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	found := false
	for _, m := range metas {
		if m.Harness != harness || m.ID != id {
			continue
		}
		found = true
		fmt.Fprintf(&b, "title=%q project=%q started=%s updated=%s\nwords=%d counted=%d asked=%v touched=%v gave_up=%v\n",
			m.Title, m.Project, m.Started.UTC().Format(time.RFC3339), m.Updated.UTC().Format(time.RFC3339),
			m.Words, m.Counted, m.Asked, m.Touched, m.GaveUp)
	}
	if !found {
		return "(not in the index)\n"
	}
	s, ok, err := FindByIdentity(dir, harness, id)
	if err != nil || !ok {
		t.Fatalf("%s:%s has a row and no session (%v)", harness, id, err)
	}
	for _, m := range s.Messages {
		fmt.Fprintf(&b, "%s|%s|%s\n", m.Role, m.Time.UTC().Format(time.RFC3339), m.Text)
	}
	return b.String()
}

// matchesRebuild fails the test when the session the incremental passes left
// in dir differs from the one an index built from scratch holds: each such
// difference is one the next --rebuild quietly changes.
func matchesRebuild(t *testing.T, dir, harness, id string) {
	t.Helper()
	fresh := filepath.Join(t.TempDir(), "rebuild.db")
	if err := Ensure(fresh, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := sessionView(t, dir, harness, id), sessionView(t, fresh, harness, id); got != want {
		t.Errorf("%s:%s after incremental passes:\n%s\nafter a rebuild:\n%s", harness, id, got, want)
	}
}

// indexPass runs one incremental pass over dir.
func indexPass(t *testing.T, dir string) {
	t.Helper()
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
}

// writeAt writes body to path and stamps it at, so a rewrite of the same size
// in the same instant still reads as a change.
func writeAt(t *testing.T, path, body string, at time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
}
