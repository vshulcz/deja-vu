package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleLine(body string) string {
	return claudeLine("s1", "2026-01-01T00:00:00Z", body+strings.Repeat("x", 200))
}

// The sample has to catch what the full hash caught: a transcript truncated
// and rewritten past its old length, which looks exactly like an append. It is
// caught wherever the rewrite starts — either past SafeSize, where the indexed
// bytes really are untouched, or before it, where the window ending at
// SafeSize falls inside the rewritten region.
func TestPrefixSampleCatchesARewriteAtEveryDepth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	var b strings.Builder
	for i := 0; i < 4000; i++ {
		b.WriteString(sampleLine("original "))
	}
	original := b.String()
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	size := int64(len(original))
	want := filePrefixSample(path, size)
	if want == 0 {
		t.Fatal("no sample for a file that exists")
	}

	for _, depth := range []float64{0, 0.25, 0.5, 0.9, 0.999} {
		keep := int(float64(len(original)) * depth)
		var r strings.Builder
		r.WriteString(original[:keep])
		for r.Len() < len(original)+5000 {
			r.WriteString(sampleLine("rewritten "))
		}
		if err := os.WriteFile(path, []byte(r.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := filePrefixSample(path, size); got == want {
			t.Errorf("a rewrite keeping the first %.1f%% passed as an append", depth*100)
		}
	}

	// An append still verifies: the bytes up to the old size are the same
	// bytes, so the sample over them is unchanged.
	if err := os.WriteFile(path, []byte(original+sampleLine("appended ")), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := filePrefixSample(path, size); got != want {
		t.Errorf("an append was rejected: %d != %d", got, want)
	}
}

// The bounded read stated as a property rather than as a duration, which a
// busy machine would make flaky: two files differing only in the middle share
// a sample. That is the trade this makes. A rewind rewrites from its
// truncation point to the end, so it always disturbs the window ending at
// SafeSize; a rewrite that changes only the middle, keeps both windows and
// lands on exactly the same length is not something a transcript does.
func TestPrefixSampleDoesNotReadTheMiddle(t *testing.T) {
	dir := t.TempDir()
	big := []byte(strings.Repeat("a", 4<<20))
	a := filepath.Join(dir, "a.jsonl")
	if err := os.WriteFile(a, big, 0o644); err != nil {
		t.Fatal(err)
	}
	middle := append([]byte(nil), big...)
	middle[len(middle)/2] = 'b'
	b := filepath.Join(dir, "b.jsonl")
	if err := os.WriteFile(b, middle, 0o644); err != nil {
		t.Fatal(err)
	}
	if filePrefixSample(a, int64(len(big))) != filePrefixSample(b, int64(len(middle))) {
		t.Fatalf("the sample covers the middle, so the read is not bounded after all")
	}
}

// A file shorter than one window is covered end to end, which is the case
// every ordinary transcript starts in.
func TestPrefixSampleCoversASmallFileWhole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	body := []byte(strings.Repeat("a", 4096))
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	want := filePrefixSample(path, int64(len(body)))
	body[len(body)/2] = 'b'
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if filePrefixSample(path, int64(len(body))) == want {
		t.Fatal("a change inside a small file went unnoticed")
	}
}
