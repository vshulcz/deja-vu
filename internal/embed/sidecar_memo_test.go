package embed

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A sidecar read twice by one process is parsed once, and a rewrite of the file
// is seen by the next read.
func TestReadIsMemoisedUntilTheFileChanges(t *testing.T) {
	dir, records := threeSessionStore(t)
	mk := func(n int) Sidecar {
		vs := make([]Vector, 0, n)
		for _, r := range records[:n] {
			vs = append(vs, Vector{Offset: r.Offset, Key: r.Record.Key, Values: []float32{1, 0}})
		}
		return Sidecar{Model: "m", Generation: "g", Dim: 2, Covered: n, Vectors: vs}
	}
	if err := write(dir, mk(1)); err != nil {
		t.Fatal(err)
	}
	a, err := Read(dir)
	if err != nil || len(a.Vectors) != 1 {
		t.Fatalf("first read = %d vectors, err %v", len(a.Vectors), err)
	}
	if b, _ := Read(dir); len(b.Vectors) != 1 || &b.Vectors[0] != &a.Vectors[0] {
		t.Fatal("the second read parsed the file again")
	}
	if err := write(dir, mk(2)); err != nil {
		t.Fatal(err)
	}
	if c, _ := Read(dir); len(c.Vectors) != 2 {
		t.Fatalf("a rewritten sidecar read as %d vectors, want 2", len(c.Vectors))
	}
}

// The memo lets go of the vectors once no read has asked for them for memoIdle,
// so a long-lived process does not keep them between questions.
func TestReadMemoIsDroppedWhenIdle(t *testing.T) {
	dir, records := threeSessionStore(t)
	old := memoIdle
	memoIdle = 20 * time.Millisecond
	t.Cleanup(func() { memoIdle = old })
	car := Sidecar{Model: "m", Generation: "g", Dim: 2, Covered: 1, Vectors: []Vector{{Offset: records[0].Offset, Key: records[0].Record.Key, Values: []float32{1, 0}}}}
	if err := write(dir, car); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		readMemo.Lock()
		held := readMemo.car.Vectors != nil
		readMemo.Unlock()
		if !held {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the memo still holds the vectors after it went idle")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if again, err := Read(dir); err != nil || len(again.Vectors) != 1 {
		t.Fatalf("read after the memo was dropped = %d vectors, err %v", len(again.Vectors), err)
	}
}

// Vectors are used only by the model and passage prefix that made them.
func TestMadeMatchesModelAndPassagePrefix(t *testing.T) {
	saidModelMismatch = false
	e5 := &Client{Model: "e5", PassagePrefix: "passage: "}
	if !e5.Made(Sidecar{Model: "e5|passage: "}) || !e5.Made(Sidecar{}) {
		t.Fatal("the client's own vectors were refused")
	}
	if e5.Made(Sidecar{Model: "e5"}) || (&Client{Model: "e5"}).Made(Sidecar{Model: "e5|passage: "}) || e5.Made(Sidecar{Model: "nomic|passage: "}) {
		t.Fatal("vectors of another model or prefix were taken")
	}
	if n := e5.MadeNote(Sidecar{Model: "e5"}); !strings.Contains(n, `"e5"`) || !strings.Contains(n, `"e5|passage: "`) {
		t.Fatalf("note = %q", n)
	}
	if e5.MadeNote(Sidecar{Model: "e5"}) != "" {
		t.Fatal("the note was repeated")
	}
}

// memoSidecar is a sidecar of n two-value vectors over the first n records; the
// byte size of the file depends on n alone, not on x.
func memoSidecar(records []index.OffsetRecord, n int, x float32) Sidecar {
	vs := make([]Vector, 0, n)
	for _, r := range records[:n] {
		vs = append(vs, Vector{Offset: r.Offset, Key: r.Record.Key, Values: []float32{x, 0}})
	}
	return Sidecar{Model: "m", Generation: "g", Dim: 2, Covered: n, Vectors: vs}
}

// A rewrite through write is seen by the next read even when the new file has
// the size and the modification time of the one the memo holds.
func TestWriteDropsTheMemoEvenWhenSizeAndTimeAreUnchanged(t *testing.T) {
	dir, records := threeSessionStore(t)
	if err := write(dir, memoSidecar(records, 2, 1)); err != nil {
		t.Fatal(err)
	}
	if a, err := Read(dir); err != nil || a.Vectors[0].Values[0] != 1 {
		t.Fatalf("first read = %+v, err %v", a.Vectors, err)
	}
	st, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := write(dir, memoSidecar(records, 2, 7)); err != nil {
		t.Fatal(err)
	}
	// The clock of a coarse file system, or two writes in one tick.
	if err := os.Chtimes(Path(dir), st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	if now, err := os.Stat(Path(dir)); err != nil || now.Size() != st.Size() || !now.ModTime().Equal(st.ModTime()) {
		t.Fatalf("the rewrite does not look like the old file (%v), so this measures nothing", err)
	}
	if b, err := Read(dir); err != nil || b.Vectors[0].Values[0] != 7 {
		t.Fatalf("the read after a rewrite = %+v, err %v; want the new vectors", b.Vectors, err)
	}
}

// The memo is trusted for one file only: a file with another size, another
// modification time or another path is parsed afresh, whichever of the three
// still matches. The files here are changed behind write's back.
func TestReadMemoNoticesAChangedSizeTimeOrPath(t *testing.T) {
	dir, records := threeSessionStore(t)
	other := t.TempDir()
	// contents writes a sidecar into a scratch directory and returns its bytes.
	// It goes through write, which drops the memo, so every file is made before
	// the first read.
	contents := func(n int, x float32) []byte {
		t.Helper()
		if err := write(other, memoSidecar(records, n, x)); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(Path(other))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	put := func(target string, b []byte, mod time.Time) {
		t.Helper()
		if err := os.WriteFile(Path(target), b, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(Path(target), mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	one, two, five, nine := contents(1, 1), contents(2, 1), contents(2, 5), contents(2, 9)
	mod := time.Now().Add(-time.Hour).Truncate(time.Second)
	put(dir, one, mod)
	if a, err := Read(dir); err != nil || len(a.Vectors) != 1 || a.Vectors[0].Values[0] != 1 {
		t.Fatalf("first read = %+v, err %v", a.Vectors, err)
	}

	// Another size, the same time.
	put(dir, two, mod)
	if b, err := Read(dir); err != nil || len(b.Vectors) != 2 {
		t.Fatalf("a file of another size read as %d vectors, err %v; want 2", len(b.Vectors), err)
	}

	// The same size, another time.
	put(dir, five, mod.Add(time.Minute))
	if c, err := Read(dir); err != nil || c.Vectors[0].Values[0] != 5 {
		t.Fatalf("a file of another time read as %+v, err %v; want the new vectors", c.Vectors, err)
	}

	// Another path, the same size and time.
	elsewhere := t.TempDir()
	put(dir, five, mod)
	if _, err := Read(dir); err != nil {
		t.Fatal(err)
	}
	put(elsewhere, nine, mod)
	if d, err := Read(elsewhere); err != nil || d.Vectors[0].Values[0] != 9 {
		t.Fatalf("a file at another path read as %+v, err %v; want its own vectors", d.Vectors, err)
	}
}
