package index

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// spillJobs is a corpus with a shared vocabulary, sessions that come back out
// of order, tool records, and more messages than one worker batch.
func spillJobs() []tokenJob {
	var jobs []tokenJob
	when := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	off := int64(0)
	for i := 0; i < 5000; i++ {
		sid := uint32(i/7 + 1)
		if i%11 == 0 {
			sid = uint32(3) // a session that steps back
		}
		text := fmt.Sprintf("the build failed on runSpillWorker step %d with error code %d 日本語のテスト", i%97, i%13)
		jobs = append(jobs, tokenJob{text: text, offset: off, sid: sid, when: when.Add(time.Duration(i) * time.Hour), tool: i%3 == 0})
		off += int64(len(text) + 40)
	}
	return jobs
}

func spillDirFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = b
	}
	return out
}

// distinctJobs are messages of words that never repeat, enough of them that a
// worker has to spill before its batch ends.
func distinctJobs() []tokenJob {
	var jobs []tokenJob
	when := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	off := int64(0)
	for i := 0; i < 300; i++ {
		var b strings.Builder
		for w := 0; w < 1000; w++ {
			fmt.Fprintf(&b, "w%dx%d ", i, w)
		}
		jobs = append(jobs, tokenJob{text: b.String(), offset: off, sid: uint32(i + 1), when: when})
		off += int64(b.Len())
	}
	return jobs
}

// The two-pass build must write the buckets the in-memory path writes, byte for
// byte, whatever the spill file looks like in between.
func TestSpilledBucketsMatchTheInMemoryOnes(t *testing.T) {
	t.Run("shared vocabulary", func(t *testing.T) { checkSpillMatchesMemory(t, spillJobs()) })
	t.Run("early spill", func(t *testing.T) { checkSpillMatchesMemory(t, distinctJobs()) })
}

func checkSpillMatchesMemory(t *testing.T, jobs []tokenJob) {
	mem := bucketPostings{}
	for _, j := range jobs {
		addIndexKeys(mem, j.text, j.offset, j.sid, j.when, j.tool)
	}
	memDir := filepath.Join(t.TempDir(), "mem")
	if err := os.MkdirAll(memDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeBucketsConcurrent(memDir, mem); err != nil {
		t.Fatal(err)
	}

	tmp := t.TempDir()
	sp, err := newSpiller(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.cleanup()
	if err := sp.run(func(push func(tokenJob)) error {
		for _, j := range jobs {
			push(j)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := sp.bucketCount(), len(mem); got != want {
		t.Fatalf("bucketCount = %d, want %d", got, want)
	}
	spDir := filepath.Join(tmp, "buckets")
	if err := os.MkdirAll(spDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := sp.writeBuckets(spDir); err != nil {
		t.Fatal(err)
	}

	want, got := spillDirFiles(t, memDir), spillDirFiles(t, spDir)
	if len(got) != len(want) {
		t.Fatalf("%d bucket files, want %d", len(got), len(want))
	}
	names := make([]string, 0, len(want))
	for n := range want {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !bytes.Equal(got[n], want[n]) {
			t.Errorf("bucket %s differs from the in-memory build", n)
		}
	}
}

// A posting used to carry its whole token and a full offset into the spill:
// 2.4 GB for a 12k-session store, and the workers spent the phase in write(2)
// (#5526). A token is written once per batch now.
func TestSpillWritesATokenOncePerBatch(t *testing.T) {
	jobs := spillJobs()
	postings, tokBytes := 0, 0
	for _, j := range jobs {
		eachIndexKey(j.text, j.when, func(tok string) {
			postings++
			tokBytes += len(tok)
		})
	}
	tmp := t.TempDir()
	sp, err := newSpiller(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.cleanup()
	if err := sp.run(func(push func(tokenJob)) error {
		for _, j := range jobs {
			push(j)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := sp.flushWriters(); err != nil {
		t.Fatal(err)
	}
	spilled := 0
	for _, b := range spillDirFiles(t, sp.dir) {
		spilled += len(b)
	}
	// Less than the token bytes alone, which every posting used to repeat.
	if spilled >= tokBytes {
		t.Fatalf("spilled %d bytes for %d postings (%d bytes of tokens): tokens are written per posting", spilled, postings, tokBytes)
	}
}
