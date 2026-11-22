package main

import (
	"os"
	"path/filepath"
	"reflect"
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

	srcRecs := `{"type":"user","sessionId":"src-1","timestamp":"2026-07-01T10:00:00Z","cwd":` + `"` + repo + `"` + `,"message":{"role":"user","content":"adjust helper spacing"}}` + "\n" +
		`{"type":"assistant","sessionId":"src-1","timestamp":"2026-07-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":` + `"` + targetFile + `"` + `,"old_string":"a","new_string":"b"}}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(claudeProj, "src-1.jsonl"), []byte(srcRecs), 0o644); err != nil {
		t.Fatal(err)
	}
	otherRecs := `{"type":"user","sessionId":"src-2","timestamp":"2026-07-01T09:00:00Z","cwd":` + `"` + repo + `"` + `,"message":{"role":"user","content":"quokkaflow retry backoff unrelated handler"}}` + "\n" +
		`{"type":"assistant","sessionId":"src-2","timestamp":"2026-07-01T09:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":` + `"` + otherFile + `"` + `,"old_string":"x","new_string":"y"}}]}}` + "\n"
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
