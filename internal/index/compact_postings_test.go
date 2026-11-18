package index

import (
	"bytes"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// The streamed pass must write what moving the whole map wrote, including
// overlays, fresh-only buckets and neighbour words in different buckets (#6072).
func TestCompactingPostingsMatchWholeMap(t *testing.T) {
	for _, tc := range []string{"whole", "missing", "bad base", "bad overlay", "search only", "no neighbours", "no carried"} {
		t.Run(tc, func(t *testing.T) {
			proj, inc := replaceStore(t, 8)
			old, err := readManifest(inc)
			if err != nil {
				t.Fatal(err)
			}
			bdir := filepath.Join(inc, "buckets")
			data, err := readBucket(filepath.Join(bdir, bucket("twhy")+".bin"))
			if err != nil {
				t.Fatal(err)
			}
			// An overlay-only word survives through carried records. A base
			// bucket containing only a replaced record must disappear.
			ov := overlayPath(filepath.Join(bdir, bucket("twhy")+".bin"), 1)
			if err = writeBucket(ov, map[string][]posting{"twalrus": data["twhy"]}); err != nil {
				t.Fatal(err)
			}
			data, err = readBucket(filepath.Join(bdir, bucket("ts007")+".bin"))
			if err != nil {
				t.Fatal(err)
			}
			if err = writeBucket(filepath.Join(bdir, bucket("txdeleted")+".bin"), map[string][]posting{"txdeleted": data["ts007"]}); err != nil {
				t.Fatal(err)
			}
			old.BucketFiles = countBucketFiles(bdir)
			workerCap := min(runtime.NumCPU(), compactPostingsWorkers)
			baseNames, err := postingBucketNames(bdir, old.BucketFiles)
			if err != nil {
				t.Fatal(err)
			}
			if len(baseNames) <= workerCap {
				t.Fatalf("control: store has %d buckets, need more than the %d workers", len(baseNames), workerCap)
			}
			neighbours := map[string][]string{
				"why": {"walrus", "quokka", "ledgerfresh", "xdeleted", "qqabsent", "zabsent"},
			}
			if tc != "no neighbours" {
				if err = writeGobAtomic(cooccurPath(inc), neighbours); err != nil {
					t.Fatal(err)
				}
			} else if err = os.Remove(cooccurPath(inc)); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			switch tc {
			case "missing":
				if err = os.Remove(ov); err != nil {
					t.Fatal(err)
				}
			case "bad base", "bad overlay":
				p := ov
				if tc == "bad base" {
					p = filepath.Join(bdir, bucket("twhy")+".bin")
				}
				if err = os.WriteFile(p, []byte("not a bucket"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			backup := t.TempDir()
			if err = os.Mkdir(filepath.Join(backup, "buckets"), 0o700); err != nil {
				t.Fatal(err)
			}
			for rel := range indexFiles(t, inc) {
				if rel != "records.bin" && rel != "cooccur.gob" && filepath.Dir(rel) != "buckets" {
					continue
				}
				b, rerr := os.ReadFile(filepath.Join(inc, rel))
				if rerr != nil {
					t.Fatal(rerr)
				}
				if err = os.WriteFile(filepath.Join(backup, rel), b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			changed := filepath.Join(proj, "s007.jsonl")
			rewriteSession(t, proj, "s007")
			parityWrite(t, changed, replaceBody("s007", "quokka ledgerfresh "), false)
			old.SearchOnly = tc == "search only"
			rp := &replacePass{
				dir: inc, old: old, progress: io.Discard, files: maps.Clone(old.Files),
				changed: map[string]FileState{changed: old.Files[changed]}, removed: map[string]bool{},
			}
			if tc == "no carried" {
				for p := range old.Files {
					if p != changed {
						rp.removed[p] = true
						delete(rp.files, p)
					}
				}
			}
			wasAppend := appendReplacing
			appendReplacing = false
			t.Cleanup(func() { appendReplacing = wasAppend })
			compactBucketsHeld.Store(0)
			compactBucketsPeak.Store(0)
			if err = rp.run(nil); err != nil {
				t.Fatal(err)
			}
			if rp.appending || !rp.swapped || len(rp.buckets) == 0 {
				t.Fatal("control: no compacting pass with fresh postings committed")
			}
			if tc == "whole" {
				peak := compactBucketsPeak.Load()
				if peak == 0 {
					t.Fatal("compacting replacement did not stream any decoded buckets")
				}
				if peak > int64(workerCap) {
					t.Errorf("compacting replacement held %d decoded buckets, worker cap is %d", peak, workerCap)
				}
				t.Logf("compacting replacement: %d base buckets, peak %d decoded buckets, worker cap %d", len(baseNames), peak, workerCap)
			}
			if held := compactBucketsHeld.Load(); held != 0 {
				t.Errorf("compacting replacement still holds %d decoded buckets", held)
			}
			want, err := carriedPostings(backup, old, rp.carried)
			if err != nil {
				t.Fatal(err)
			}
			mergePostings(want, rp.buckets)
			ref := t.TempDir()
			if err = writeBucketsConcurrent(ref, want); err != nil {
				t.Fatal(err)
			}
			assertBucketFilesEqual(t, filepath.Join(inc, "buckets"), ref)
			var absent map[string]bool
			if !rp.m.SearchOnly {
				absent = cooccurUnindexed(backup, want)
			}
			if tc == "whole" && (!absent["xdeleted"] || !absent["qqabsent"] || !absent["zabsent"] || absent["walrus"] || absent["quokka"] || absent["ledgerfresh"]) {
				t.Fatalf("control: neighbour words do not cover kept, fresh and absent postings: %v", absent)
			}
			// Ask the same path for its result before commit scrubs the map,
			// and hold its progress count to the whole-map writer's count.
			staged := t.TempDir()
			if err = os.Mkdir(filepath.Join(staged, "buckets"), 0o700); err != nil {
				t.Fatal(err)
			}
			copyPass := *rp
			copyPass.dir, copyPass.tmp = backup, staged
			progress := &recordingProgress{}
			progress.Phase("writing postings", 0)
			SetProgress(progress)
			t.Cleanup(func() { SetProgress(nil) })
			got, err := copyPass.writeCompactedPostings()
			SetProgress(nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, absent) {
				t.Errorf("unposted = %v, want %v", got, absent)
			}
			if got := progress.advances["writing postings"]; got != len(want) {
				t.Errorf("advanced %d buckets, want %d", got, len(want))
			}
			assertBucketFilesEqual(t, filepath.Join(staged, "buckets"), ref)
			if tc == "whole" {
				// A write failure must not be mistaken for a damaged input.
				blocked := t.TempDir()
				if err = os.WriteFile(filepath.Join(blocked, "buckets"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
				copyPass.tmp = blocked
				passKeyed.Store(0)
				if _, werr := copyPass.writeCompactedPostings(); werr == nil {
					t.Fatal("writing through a regular file succeeded")
				}
				if got := passKeyed.Load(); got != 0 {
					t.Errorf("write failure retokenized %d records", got)
				}
			}
		})
	}
}

// carriedPostings and movedPostings keep the former whole-map implementation
// as the reference for the streaming writer, including its retokenize fallback.
func carriedPostings(dir string, old Manifest, c *carriedRecords) (bucketPostings, error) {
	if len(c.old) == 0 {
		return bucketPostings{}, nil
	}
	c.index()
	if out, err := movedPostings(filepath.Join(dir, "buckets"), old.BucketFiles, c); err == nil {
		return out, nil
	}
	return retokenizeCarried(filepath.Join(dir, "records.bin"), tablesFromManifest(old), c)
}

func movedPostings(bdir string, want int, c *carriedRecords) (bucketPostings, error) {
	names, err := postingBucketNames(bdir, want)
	if err != nil {
		return nil, err
	}
	workers := min(runtime.NumCPU(), len(names))
	parts := make([]map[string][]posting, len(names))
	var next atomic.Int64
	// The first error, under a lock: readBucket's errors are of several
	// types, which an atomic.Value refuses to hold in turn.
	var mu sync.Mutex
	var failed error
	fail := func(err error) {
		mu.Lock()
		if failed == nil {
			failed = err
		}
		mu.Unlock()
	}
	stopped := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return failed != nil
	}
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for {
				i := int(next.Add(1)) - 1
				if i >= len(names) || stopped() {
					return
				}
				data, err := readBucket(filepath.Join(bdir, names[i]))
				if err != nil {
					fail(err)
					return
				}
				parts[i] = c.move(data)
			}
		})
	}
	wg.Wait()
	if failed != nil {
		return nil, failed
	}
	out := make(bucketPostings, len(names))
	for i, n := range names {
		if len(parts[i]) > 0 {
			out[strings.TrimSuffix(n, ".bin")] = parts[i]
		}
	}
	return out, nil
}

func assertBucketFilesEqual(t *testing.T, gotDir, wantDir string) {
	t.Helper()
	got, want := indexFiles(t, gotDir), indexFiles(t, wantDir)
	if len(got) != len(want) {
		t.Errorf("bucket files = %d, want %d", len(got), len(want))
	}
	for name := range want {
		a, err := os.ReadFile(filepath.Join(gotDir, name))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(wantDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("bucket %s differs from the whole-map writer", name)
		}
	}
}
