package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestRepoSymbolMapDiskBinaryRoundTripAndCorruptFallback(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	repo := filepath.Join(tmp, "sample-repo")
	srcDir := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srcFile := filepath.Join(srcDir, "worker.go")
	if err := os.WriteFile(srcFile, []byte("package pkg\n\nfunc RunWorker() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stRoot, err := os.Stat(repo)
	if err != nil {
		t.Fatal(err)
	}
	stPkg, err := os.Stat(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	stFile, err := os.Stat(srcFile)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Round(0)
	orig := repoSymbolMap{
		at:  now,
		cwd: filepath.Clean(repo),
		files: []repoFileEntry{
			{
				rel:       "pkg/worker.go",
				stem:      "worker",
				pkg:       "pkg",
				stemWords: map[string]bool{"worker": true},
				declExact: map[string]bool{"runworker": true},
				declWords: map[string]bool{"run": true, "worker": true},
				bodyWords: map[string]int{"worker": 2, "retry": 1},
			},
			{
				rel:  "pkg/empty.go",
				stem: "empty",
				pkg:  "pkg",
			},
		},
		byStem:   map[string][]string{"worker": {"pkg/worker.go"}, "empty": {"pkg/empty.go"}},
		byDir:    map[string]int{"pkg": 2},
		stemDF:   map[string]int{"worker": 1},
		declDF:   map[string]int{"run": 1, "worker": 1},
		structDF: map[string]int{"job": 1},
		bodyDF:   map[string]int{"retry": 1},
		firstDecl: map[string]symbolDeclLoc{
			"runworker": {rel: "pkg/worker.go", line: 3, sig: "func RunWorker()"},
		},
		dirMtimes: map[string]int64{
			repo:   stRoot.ModTime().UnixNano(),
			srcDir: stPkg.ModTime().UnixNano(),
		},
		fileMtimes: map[string]int64{
			srcFile: stFile.ModTime().UnixNano(),
		},
		fileSizes: map[string]int64{
			srcFile: stFile.Size(),
		},
		numFiles: 2,
		capped:   false,
	}

	diskPath := repoSymbolDiskPath(dir, repo, repo)
	saveRepoSymbolMapToDisk(dir, repo, repo, orig)

	raw, err := os.ReadFile(diskPath)
	if err != nil {
		t.Fatalf("read %s: %v", diskPath, err)
	}
	if !strings.HasPrefix(string(raw), repoSymbolDiskMagic) {
		t.Fatalf("magic = %q, want prefix %q", string(raw[:min(len(raw), 8)]), repoSymbolDiskMagic)
	}

	got, ok := loadRepoSymbolMapFromDisk(dir, repo, repo)
	if !ok {
		t.Fatal("loadRepoSymbolMapFromDisk returned false for valid binary snapshot")
	}
	if got.at.UnixNano() != orig.at.UnixNano() {
		t.Fatalf("at = %v, want %v", got.at, orig.at)
	}
	if got.cwd != orig.cwd || got.numFiles != orig.numFiles || got.capped != orig.capped {
		t.Fatalf("header mismatch: got=(cwd=%q numFiles=%d capped=%v), want=(cwd=%q numFiles=%d capped=%v)",
			got.cwd, got.numFiles, got.capped, orig.cwd, orig.numFiles, orig.capped)
	}
	if !reflect.DeepEqual(got.files, orig.files) {
		t.Fatalf("files mismatch:\ngot:  %#v\nwant: %#v", got.files, orig.files)
	}
	if !reflect.DeepEqual(got.byStem, orig.byStem) || !reflect.DeepEqual(got.byDir, orig.byDir) ||
		!reflect.DeepEqual(got.stemDF, orig.stemDF) || !reflect.DeepEqual(got.declDF, orig.declDF) ||
		!reflect.DeepEqual(got.structDF, orig.structDF) || !reflect.DeepEqual(got.bodyDF, orig.bodyDF) ||
		!reflect.DeepEqual(got.firstDecl, orig.firstDecl) || !reflect.DeepEqual(got.dirMtimes, orig.dirMtimes) ||
		!reflect.DeepEqual(got.fileMtimes, orig.fileMtimes) || !reflect.DeepEqual(got.fileSizes, orig.fileSizes) {
		t.Fatalf("maps mismatch after binary roundtrip")
	}

	// Corrupt/truncated payloads must fail cleanly without panicking.
	for _, bad := range [][]byte{
		[]byte("DJSYMM01"),
		[]byte("OLDBAD00rest"),
		raw[:min(len(raw), 24)],
		append([]byte("DJSYMM01"), 0xff, 0xff, 0xff, 0xff, 0xff),
	} {
		if err := os.WriteFile(diskPath, bad, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok := loadRepoSymbolMapFromDisk(dir, repo, repo); ok {
			t.Fatalf("expected corrupt disk payload (len=%d) to be rejected", len(bad))
		}
	}

	// Expired TTL must fail.
	expired := orig
	expired.at = time.Now().Add(-2 * repoSymbolDiskTTL)
	saveRepoSymbolMapToDisk(dir, repo, repo, expired)
	if _, ok := loadRepoSymbolMapFromDisk(dir, repo, repo); ok {
		t.Fatal("expected expired disk payload to be rejected")
	}
}

func TestRepoSymbolMapDiskCachesDeterministicCapAndSkipsTimeout(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	prevDir := getActiveDejaDir()
	setActiveDejaDir(dir)
	t.Cleanup(func() { setActiveDejaDir(prevDir) })

	detRepo := filepath.Join(tmp, "det-capped-repo")
	if err := os.MkdirAll(filepath.Join(detRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repoSymbolMapCache.Delete(detRepo) })
	if p1, p2 := repoSymbolDiskPath(dir+"/", detRepo, detRepo), repoSymbolDiskPath(dir, detRepo, detRepo); p1 != p2 {
		t.Fatalf("repoSymbolDiskPath trailing slash mismatch: %q vs %q", p1, p2)
	}

	// Build 66 Go files of ~254 KiB each (<= maxRepoSymbolFileBytes = 256 KiB)
	// so default repoSymbolScanMaxBytes (16 MiB) triggers around file 65 while
	// staying well below repoSymbolScanMinFilesBeforeDeadline (128), keeping
	// all default globals intact and avoiding slow-VM wall-clock timeouts.
	pkgDir := filepath.Join(detRepo, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pad := "//" + strings.Repeat("a", 254*1024) + "\n"
	for i := range 66 {
		name := filepath.Join(pkgDir, "f"+strconv.Itoa(i)+".go")
		body := "package pkg\nfunc Sym" + strconv.Itoa(i) + "() {}\n" + pad
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx := loadRepoSymbolMap(detRepo)
	if !idx.capped || idx.timedOut || len(idx.files) == 0 || len(idx.files) >= repoSymbolScanMinFilesBeforeDeadline {
		t.Fatalf("expected deterministic byte cap before file-deadline threshold: capped=%v timedOut=%v files=%d",
			idx.capped, idx.timedOut, len(idx.files))
	}
	if !repoSymbolMapFresh(idx) {
		t.Fatal("expected freshly scanned deterministic capped symmap to pass repoSymbolMapFresh")
	}
	diskIdx, ok := loadRepoSymbolMapFromDisk(dir, detRepo, detRepo)
	if !ok || !diskIdx.capped || len(diskIdx.files) != len(idx.files) {
		t.Fatalf("expected deterministic capped snapshot to persist and reload with capped=true, got ok=%v capped=%v files=%d wantFiles=%d",
			ok, diskIdx.capped, len(diskIdx.files), len(idx.files))
	}

	// Wall-clock deadline truncation must remain incomplete and must not encode for disk caching.
	slowRepo := filepath.Join(tmp, "slow-repo")
	if err := os.MkdirAll(filepath.Join(slowRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repoSymbolMapCache.Delete(slowRepo) })
	for i := range 3 {
		if err := os.WriteFile(filepath.Join(slowRepo, "s"+strconv.Itoa(i)+".go"), []byte("package slow\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prevBudget := repoSymbolScanBudget
	prevMin := repoSymbolScanMinFilesBeforeDeadline
	prevHook := repoSymbolScanHook
	repoSymbolScanBudget = time.Nanosecond
	repoSymbolScanMinFilesBeforeDeadline = 1
	repoSymbolScanHook = func(string) {
		t0 := time.Now()
		for time.Since(t0) < time.Millisecond {
			time.Sleep(time.Millisecond)
		}
	}
	t.Cleanup(func() {
		repoSymbolScanBudget = prevBudget
		repoSymbolScanMinFilesBeforeDeadline = prevMin
		repoSymbolScanHook = prevHook
	})
	slowIdx := loadRepoSymbolMap(slowRepo)
	if !slowIdx.capped || !slowIdx.timedOut {
		t.Fatalf("expected wall-clock budget cap on slowIdx, got capped=%v timedOut=%v", slowIdx.capped, slowIdx.timedOut)
	}
	if _, encOK := encodeRepoSymbolMap(slowIdx); encOK {
		t.Fatal("expected wall-clock timed-out symmap not to encode for disk caching")
	}
}

func TestBM25SessionTouchedBoostSkipsRecordLogAndResolvesPromotedNote(t *testing.T) {
	tmp := hermeticEnv(t)
	claudeProj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-sample-repo")
	if err := os.MkdirAll(claudeProj, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(tmp, "sample-repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	targetFile := filepath.Join(repo, "backoff.go")
	if err := os.WriteFile(targetFile, []byte("package sample\n\nfunc QuokkaBackoff() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	otherFile := filepath.Join(repo, "unrelated.go")
	if err := os.WriteFile(otherFile, []byte("package sample\n\nfunc Unrelated() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	srcRecs := `{"type":"user","sessionId":"src-1","timestamp":"2026-07-01T10:00:00Z","cwd":` + strconv.Quote(repo) + `,"message":{"role":"user","content":"adjust helper spacing"}}` + "\n" +
		`{"type":"assistant","sessionId":"src-1","timestamp":"2026-07-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":` + strconv.Quote(targetFile) + `,"old_string":"a","new_string":"b"}}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(claudeProj, "src-1.jsonl"), []byte(srcRecs), 0o644); err != nil {
		t.Fatal(err)
	}
	otherRecs := `{"type":"user","sessionId":"src-2","timestamp":"2026-07-01T09:00:00Z","cwd":` + strconv.Quote(repo) + `,"message":{"role":"user","content":"quokkaflow retry backoff unrelated handler"}}` + "\n" +
		`{"type":"assistant","sessionId":"src-2","timestamp":"2026-07-01T09:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":` + strconv.Quote(otherFile) + `,"old_string":"x","new_string":"y"}}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(claudeProj, "src-2.jsonl"), []byte(otherRecs), 0o644); err != nil {
		t.Fatal(err)
	}

	notesFile := os.Getenv("DEJA_NOTES_FILE")
	promoted := `{"kind":"promoted","session":"claude:src-1","state":"accepted","project":"sample-repo","ts":"2026-07-01T10:05:00Z","title":"quokkaflow backoff token","text":"Solved quokkaflow retry backoff jitter in backoff handler."}` + "\n"
	if err := os.WriteFile(notesFile, []byte(promoted), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	prevDir := getActiveDejaDir()
	setActiveDejaDir(dir)
	t.Cleanup(func() { setActiveDejaDir(prevDir) })

	boostBefore, countBefore := bm25SessionTouchedBoost(dir, repo, "quokkaflow retry backoff", nil)
	if boostBefore["backoff.go"] <= 0 || countBefore["backoff.go"] == 0 {
		t.Fatalf("expected boost for backoff.go via promoted note metadata, got boost=%v count=%v", boostBefore, countBefore)
	}

	// Overwrite records.bin with zero bytes of equal length so planIndexReady
	// still passes stat checks while any record read fails to decode.
	recPath := filepath.Join(dir, "records.bin")
	recInfo, err := os.Stat(recPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recPath, make([]byte, recInfo.Size()), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(dir, "recoffsets.bin"))

	boostAfter, countAfter := bm25SessionTouchedBoost(dir, repo, "quokkaflow retry backoff", nil)
	if !reflect.DeepEqual(boostAfter, boostBefore) || !reflect.DeepEqual(countAfter, countBefore) {
		t.Fatalf("without records.bin mismatch:\ngot  boost=%v count=%v\nwant boost=%v count=%v", boostAfter, countAfter, boostBefore, countBefore)
	}
}

func TestRepoSymbolDiskCacheGuardsAndNilMaps(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(repo, "a.go")
	if err := os.WriteFile(f, []byte("package repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(repo)
	if err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}

	if repoSymbolMapMtimesMatch(repoSymbolMap{}) {
		t.Fatal("expected empty dirMtimes to fail match")
	}
	if repoSymbolMapMtimesMatch(repoSymbolMap{dirMtimes: map[string]int64{filepath.Join(tmp, "missing"): 1}}) {
		t.Fatal("expected missing dir to fail match")
	}
	if repoSymbolMapMtimesMatch(repoSymbolMap{
		dirMtimes:  map[string]int64{repo: dirInfo.ModTime().UnixNano()},
		fileMtimes: map[string]int64{filepath.Join(tmp, "missing.go"): 1},
	}) {
		t.Fatal("expected missing file to fail match")
	}
	if repoSymbolMapMtimesMatch(repoSymbolMap{
		dirMtimes:  map[string]int64{repo: dirInfo.ModTime().UnixNano()},
		fileMtimes: map[string]int64{f: fileInfo.ModTime().UnixNano()},
		fileSizes:  map[string]int64{f: fileInfo.Size() + 10},
	}) {
		t.Fatal("expected mismatched file size to fail match")
	}

	prevDir := getActiveDejaDir()
	t.Cleanup(func() { setActiveDejaDir(prevDir) })
	setActiveDejaDir(dir)
	if _, ok := repoSymbolDiskCacheAllowed(); ok {
		t.Fatal("expected cache disallowed when manifest is missing")
	}
	prevMaxFiles := repoSymbolScanMaxFiles
	repoSymbolScanMaxFiles = 10
	if _, ok := repoSymbolDiskCacheAllowed(); ok {
		repoSymbolScanMaxFiles = prevMaxFiles
		t.Fatal("expected cache disallowed with non-default scan cap")
	}
	repoSymbolScanMaxFiles = prevMaxFiles
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if gotDir, ok := repoSymbolDiskCacheAllowed(); !ok || gotDir != dir {
		t.Fatalf("expected cache allowed for %s, got %q ok=%v", dir, gotDir, ok)
	}

	capped := repoSymbolMap{at: time.Now(), capped: true}
	cappedBytes, ok := encodeRepoSymbolMap(capped)
	if !ok || len(cappedBytes) <= repoSymbolDiskHdrLen {
		t.Fatalf("unexpected deterministic capped encode: len=%d ok=%v", len(cappedBytes), ok)
	}
	cappedDec, decOK := decodeRepoSymbolMap(cappedBytes, repo)
	if !decOK || !cappedDec.capped {
		t.Fatalf("expected deterministic capped snapshot to decode with capped=true, got ok=%v capped=%v", decOK, cappedDec.capped)
	}
	badFlag := append([]byte(nil), cappedBytes...)
	badFlag[12] = 2
	if _, badOK := decodeRepoSymbolMap(badFlag, repo); badOK {
		t.Fatal("expected unsupported header flag to be rejected on decode")
	}
	timedOut := repoSymbolMap{at: time.Now(), capped: true, timedOut: true}
	if _, toOK := encodeRepoSymbolMap(timedOut); toOK {
		t.Fatal("expected timed-out symmap not to encode")
	}

	zeroAt := make([]byte, repoSymbolDiskHdrLen)
	copy(zeroAt, cappedBytes)
	zeroAt[12] = 0
	binary.LittleEndian.PutUint64(zeroAt[13:21], 0)
	if _, hdrOK := parseRepoSymbolHeader(zeroAt); hdrOK {
		t.Fatal("expected zero timestamp header to be rejected")
	}
	expired := make([]byte, repoSymbolDiskHdrLen)
	copy(expired, cappedBytes)
	expired[12] = 0
	binary.LittleEndian.PutUint64(expired[13:21], uint64(time.Now().Add(-2*repoSymbolDiskTTL).UnixNano()))
	if _, expOK := parseRepoSymbolHeader(expired); expOK {
		t.Fatal("expected expired header to be rejected")
	}
	if _, shortOK := parseRepoSymbolHeader(expired[:4]); shortOK {
		t.Fatal("expected short header to be rejected")
	}

	nilMaps := repoSymbolMap{
		at:         time.Now().Round(time.Nanosecond),
		cwd:        repo,
		dirMtimes:  map[string]int64{repo: dirInfo.ModTime().UnixNano()},
		fileMtimes: map[string]int64{f: fileInfo.ModTime().UnixNano()},
		fileSizes:  map[string]int64{f: fileInfo.Size()},
		numFiles:   1,
	}
	encoded, encOK := encodeRepoSymbolMap(nilMaps)
	if !encOK {
		t.Fatal("encodeRepoSymbolMap with nil maps failed")
	}
	saveRepoSymbolMapToDisk(dir, repo, repo, nilMaps)
	loaded, loadOK := loadRepoSymbolMapFromDisk(dir, repo, repo)
	if !loadOK || loaded.numFiles != 1 || loaded.files != nil || loaded.firstDecl != nil {
		t.Fatalf("unexpected nil-map round-trip: ok=%v loaded=%+v", loadOK, loaded)
	}
	for cut := repoSymbolDiskHdrLen; cut < len(encoded); cut++ {
		if _, truncOK := decodeRepoSymbolMap(encoded[:cut], repo); truncOK {
			t.Fatalf("expected decode failure for truncation at %d/%d", cut, len(encoded))
		}
	}
	if _, trailOK := decodeRepoSymbolMap(append(append([]byte(nil), encoded...), 0x01), repo); trailOK {
		t.Fatal("expected decode failure with trailing bytes")
	}
}
