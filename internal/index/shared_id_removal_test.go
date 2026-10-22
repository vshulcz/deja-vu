package index

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Two transcripts with one id in two projects (#699): a turn both hold is
// written once, under whichever copy was read first. Removing that copy's
// project took every record from its path, the surviving copy's commands and
// outputs with them, and left the row on the deleted file until a rebuild
// (#4310).
func TestRemovingOneCopyOfASharedIDKeepsWhatTheOtherHolds(t *testing.T) {
	c, tmp := newCFStore(t)
	for i := range 6 {
		c.write("mmm", i, fmt.Sprintf("fix the store test %d", i))
	}
	c.write("aaa", 0, "the same id in another project")
	c.appendTurn("aaa", 0, 30, "cargo build", "error[E0425]: cannot find value `x` in this scope")
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(c.path("aaa", 0))); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	key := "claude:" + c.sid(0)
	held := func(dir string) (string, map[string]int) {
		t.Helper()
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		recs, err := ReadRecords(dir)
		if err != nil {
			t.Fatal(err)
		}
		by := map[string]int{}
		for _, r := range recs {
			if r.Record.Key == key {
				by[filepath.Base(filepath.Dir(r.Record.SourcePath))+" "+r.Record.Role]++
			}
		}
		return m.Sessions[key].Path, by
	}
	fresh := filepath.Join(t.TempDir(), "index.db")
	if err := Ensure(fresh, "", true, nil); err != nil {
		t.Fatal(err)
	}
	gotPath, got := held(dir)
	wantPath, want := held(fresh)
	if gotPath != wantPath || !reflect.DeepEqual(got, want) {
		t.Errorf("after the update: path %s, records %v\na full build:      path %s, records %v", gotPath, got, wantPath, want)
	}
	if a, b := ReadCommandFails(dir), ReadCommandFails(fresh); !reflect.DeepEqual(a, b) {
		t.Errorf("failure table after the update\n%+v\nfull build\n%+v", a, b)
	}
}
