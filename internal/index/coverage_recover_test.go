package index

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

// EnsureForSearch must resolve dir=="" through DefaultDir, and a second call
// against an already-fresh, intact index must take the cheap "return nil"
// path instead of re-scanning or re-indexing.
func TestEnsureForSearchDefaultDirAndFastPath(t *testing.T) {
	hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	proj := filepath.Join(claudeRoot, "p")
	write(t, filepath.Join(proj, "s.jsonl"),
		claudeLine("s1", "2026-01-02T03:04:05Z", "fastpathneedle"))

	o := search.Options{Query: "fastpathneedle"}
	if err := EnsureForSearch("", o, false, nil); err != nil {
		t.Fatalf("EnsureForSearch dir=='' err=%v", err)
	}
	// Second call: nothing changed, force=false -> manifestFresh+recordsIntact
	// must short-circuit to nil without touching updateIndex/rebuild.
	if err := EnsureForSearch("", o, false, nil); err != nil {
		t.Fatalf("EnsureForSearch fast path err=%v", err)
	}
	if ss, err := Search("", o); err != nil || len(ss) != 1 {
		t.Fatalf("search after fast path=%#v err=%v", ss, err)
	}
}

// SearchWithRecovery must pass through a clean search unchanged, and when the
// forced rebuild it triggers on a corrupt index itself fails, it must surface
// that failure rather than mask it.
func TestSearchWithRecoveryPassthroughAndForcedRebuildFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission simulation is unix-only")
	}
	tmp := hermeticIndexEnv(t)
	parent := filepath.Join(tmp, "parent")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "idx")
	writeTinyIndex(t, dir)

	o := search.Options{Query: "alpha"}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	// EnsureForSearch always indexes with scope=="", so the first call above
	// rebuilds from (empty) real sources and discards the fixture; restore it.
	writeTinyIndex(t, dir)
	ss, err := SearchWithRecovery(dir, o, nil)
	if err != nil || len(ss) != 1 {
		t.Fatalf("clean passthrough ss=%#v err=%v", ss, err)
	}

	// Corrupt a bucket so Search reports errCorruptIndex, and make the
	// recovery rebuild's dir+".tmp" staging MkdirAll fail by making its
	// parent read-only (the lock file already exists, so lockDir itself
	// still succeeds).
	bucketPath := filepath.Join(dir, "buckets", bucket("talpha")+".bin")
	if err := os.WriteFile(bucketPath, []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(parent, 0o755) }()
	if _, err := SearchWithRecovery(dir, o, nil); err == nil {
		t.Fatal("SearchWithRecovery with blocked rebuild returned nil error")
	}
}

// Recent/RecentProject/FindByPrefix/HasManifest must all resolve dir=="" and
// propagate a missing-manifest error rather than panicking; FindByPrefix must
// pick the most recently updated of several prefix matches and surface a
// records read error separately from the manifest read.
func TestRecentFamilyDefaultDirAndMissingManifest(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	if HasManifest("") {
		t.Fatal("HasManifest('') true with nothing built")
	}
	missing := filepath.Join(tmp, "does-not-exist")
	if _, err := Recent(missing, 1); err == nil {
		t.Fatal("Recent missing manifest returned nil")
	}
	if _, err := RecentProject(missing, "p", 1); err == nil {
		t.Fatal("RecentProject missing manifest returned nil")
	}
	if _, _, err := FindByPrefix(missing, "p"); err == nil {
		t.Fatal("FindByPrefix missing manifest returned nil")
	}

	if err := EnsureForSearch("", search.Options{Query: "pfx", All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	write(t, filepath.Join(claudeRoot, "p1", "a.jsonl"), claudeLine("pfx-a", "2026-01-01T00:00:00Z", "pfx one"))
	write(t, filepath.Join(claudeRoot, "p2", "b.jsonl"), claudeLine("pfx-b", "2026-01-01T01:00:00Z", "pfx two"))
	if err := EnsureForSearch("", search.Options{Query: "pfx", All: true}, true, nil); err != nil {
		t.Fatal(err)
	}
	found, ok, err := FindByPrefix("", "pfx")
	if err != nil || !ok || found.ID != "pfx-b" {
		t.Fatalf("FindByPrefix multi-match latest: found=%#v ok=%v err=%v", found, ok, err)
	}

	// Now delete records.bin: readManifest still succeeds but recordsForKey
	// must surface its own error distinctly.
	dir2 := DefaultDir()
	if err := os.Remove(filepath.Join(dir2, "records.bin")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := FindByPrefix("", "pfx"); err == nil {
		t.Fatal("FindByPrefix with missing records.bin returned nil")
	}
	if _, err := RecentProject("", "p", 1); err == nil {
		t.Fatal("RecentProject with missing records.bin returned nil")
	}
}

// A dir+".tmp" staging path blocked by a pre-existing regular file must fail
// MkdirAll in both full-rebuild entry points; a records.bin path blocked by a
// pre-existing directory must fail the subsequent os.Create.
func TestRebuildStagingDirBlocked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission simulation is unix-only")
	}
	tmp := hermeticIndexEnv(t)

	// rebuild/rebuildForSearch always os.RemoveAll(dir+".tmp") before
	// recreating it, so a pre-existing file or directory there gets wiped
	// before MkdirAll runs. To make the staging MkdirAll itself fail, the
	// dir's parent must be read-only: RemoveAll on the not-yet-existing
	// tmp path is then a harmless no-op, but MkdirAll cannot create it.
	blocked := filepath.Join(tmp, "blocked")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(blocked, 0o755) }()

	dir := filepath.Join(blocked, "idx-a")
	if err := rebuild(dir, "", "", map[string]FileState{}, nil); err == nil {
		t.Fatal("rebuild with read-only parent returned nil")
	}

	dir2 := filepath.Join(blocked, "idx-b")
	if err := rebuildForSearch(dir2, search.Options{}, "", map[string]FileState{}, nil); err == nil {
		t.Fatal("rebuildForSearch with read-only parent returned nil")
	}
}

// A codex session appearing both in a rollout file (real project, later
// timestamps) and in history.jsonl (Project=="history", zero timestamps
// because ts is omitted) under the same session id drives every branch of
// the session-merge logic in both rebuild() and writeSessionsWithSync():
// Started.IsZero, old.Updated.After, Project=="history" carry-over, and
// Title=="" carry-over.
func TestCodexDualSourceDuplicateMergeBranches(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	codexRoot := os.Getenv("DEJA_CODEX_ROOT")
	write(t, filepath.Join(codexRoot, "sessions", "2026", "01", "01", "rollout-2026-01-01T12-00-00-dup1.jsonl"),
		`{"type":"session_meta","timestamp":"2026-01-01T12:00:00Z","payload":{"session_id":"dup1","cwd":"/p/real-project"}}`+"\n"+
			`{"timestamp":"2026-01-01T12:00:01Z","payload":{"role":"user","content":"rollout dupneedle question"}}`+"\n")
	// ts omitted (not a number) -> parseTimeAny returns zero time, so the
	// history-derived session has Started==Updated==zero.
	write(t, filepath.Join(codexRoot, "history.jsonl"),
		`{"session_id":"dup1","text":"history dupneedle question"}`+"\n")

	dir := filepath.Join(tmp, "rebuild-idx")
	if err := rebuild(dir, "codex", "", currentFiles("codex"), nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "dupneedle", All: true})
	if err != nil || len(ss) != 1 {
		t.Fatalf("rebuild merged codex dup: ss=%#v err=%v", ss, err)
	}
	if ss[0].Project != "real-project" {
		t.Fatalf("rebuild merge did not carry real project: %#v", ss[0])
	}

	dir2 := filepath.Join(tmp, "writesessions-idx")
	tmp2 := dir2 + ".tmp"
	if err := os.MkdirAll(filepath.Join(tmp2, "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	sessLoad := loadProgress("codex", nil)
	if err := writeSessionsWithSync(tmp2, dir2, sessLoad, currentFiles("codex"), "", importedState{}); err != nil {
		t.Fatal(err)
	}
	ss2, err := Search(dir2, search.Options{Query: "dupneedle", All: true})
	if err != nil || len(ss2) != 1 {
		t.Fatalf("writeSessionsWithSync merged codex dup: ss=%#v err=%v", ss2, err)
	}
	if ss2[0].Project != "real-project" {
		t.Fatalf("writeSessionsWithSync merge did not carry real project: %#v", ss2[0])
	}
}

// redactForIngest must attribute redaction counts to the opencode database
// file when the source path (the session's project dir) is not itself a
// tracked file, and skip that attribution when sourcePath already is the db
// path; carryRedactions must skip an old file entry no longer present in the
// new Files map; Redactions("") must resolve DefaultDir.
func TestRedactForIngestAndCarryRedactionsBranches(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	db := os.Getenv("DEJA_OPENCODE_DB")

	m := &Manifest{Files: map[string]FileState{db: {Path: db}}}
	secret := "AKIA" + "IOSFODNN7EXAMPLE"
	redactForIngest(m, filepath.Join(tmp, "opencode-project-dir"), "leak "+secret)
	if m.Files[db].Redactions == 0 {
		t.Fatalf("redaction not attributed to opencode db: %#v", m.Files[db])
	}

	m2 := &Manifest{Files: map[string]FileState{}}
	redactForIngest(m2, filepath.Join(tmp, "some-other-source"), "leak "+secret)
	if len(m2.Files) != 0 {
		t.Fatalf("redaction created a phantom entry: %#v", m2.Files)
	}

	m3 := &Manifest{Files: map[string]FileState{}}
	carryRedactions(m3, Manifest{Files: map[string]FileState{"gone": {Path: "gone", Redactions: 3}}}, map[string]bool{})
	if m3.Redacted != 0 {
		t.Fatalf("carryRedactions carried a file absent from the new map: %#v", m3)
	}

	dir := DefaultDir()
	writeTinyIndex(t, dir)
	if stats, err := Redactions(""); err != nil || stats.Total == 0 {
		t.Fatalf("Redactions('') = %#v err=%v", stats, err)
	}
}

// updateIndex must skip a syncImportPath entry when scanning old.Files for
// removals (it is virtual, never a real removed source); a corrupt bucket
// discovered mid-append must fail with errCorruptIndex and be caught by
// updateIndex, triggering a full rebuild instead of surfacing the error.
func TestUpdateIndexSyncImportSkipAndCorruptAppendRebuild(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	file := filepath.Join(claudeRoot, "p", "s.jsonl")
	write(t, file, claudeLine("s1", "2026-01-02T03:04:05Z", "syncskip needle one"))

	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Manually mark the virtual import path as tracked so updateIndex's
	// removed-file scan walks over it.
	m.Files[syncImportPath] = FileState{Path: syncImportPath}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	// Append a second line so the append-eligible path runs and old.Files
	// still contains syncImportPath during the removed-file scan.
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(claudeLine("s1", "2026-01-02T03:05:05Z", "syncskip needle two")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatalf("update with virtual import path in Files: %v", err)
	}
	if ss, err := Search(dir, search.Options{Query: "syncskip needle two", All: true}); err != nil || len(ss) == 0 {
		t.Fatalf("appended message missing: %#v err=%v", ss, err)
	}

	// Corrupt-bucket-during-append case: precreate a garbage bucket file for
	// the exact bucket a new token will land in, then append content whose
	// new token hashes into that bucket.
	dir2 := filepath.Join(tmp, "idx2")
	file2 := filepath.Join(claudeRoot, "p2", "s2.jsonl")
	write(t, file2, claudeLine("s2", "2026-01-02T03:04:05Z", "corruptbucket base"))
	if err := Ensure(dir2, "", false, nil); err != nil {
		t.Fatal(err)
	}
	tok := "zzcorruptcollision"
	bpath := filepath.Join(dir2, "buckets", bucket("t"+tok)+".bin")
	if err := os.WriteFile(bpath, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	f2, err := os.OpenFile(file2, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f2.WriteString(claudeLine("s2", "2026-01-02T03:05:05Z", tok)); err != nil {
		t.Fatal(err)
	}
	f2.Close()
	if err := Ensure(dir2, "", false, nil); err != nil {
		t.Fatalf("append with corrupt bucket must self-heal via rebuild, got err=%v", err)
	}
	if ss, err := Search(dir2, search.Options{Query: tok, All: true}); err != nil || len(ss) == 0 {
		t.Fatalf("healed content missing after corrupt-bucket rebuild: %#v err=%v", ss, err)
	}
}

// appendIncremental must surface (not swallow) a genuine, non-corrupt bucket
// read failure so updateIndex's "append: %w" wrap path executes.
func TestUpdateIndexAppendNonCorruptErrorWrapped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based unreadable file simulation is unix-only")
	}
	tmp := hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	file := filepath.Join(claudeRoot, "p", "s.jsonl")
	write(t, file, claudeLine("s1", "2026-01-02T03:04:05Z", "permneedle base"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	tok := "permlockedtoken"
	bpath := filepath.Join(dir, "buckets", bucket("t"+tok)+".bin")
	if err := os.WriteFile(bpath, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bpath, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(bpath, 0o644) }()
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(claudeLine("s1", "2026-01-02T03:05:05Z", tok)); err != nil {
		t.Fatal(err)
	}
	f.Close()
	err = Ensure(dir, "", false, nil)
	if err == nil {
		t.Fatal("Ensure with unreadable bucket file returned nil error")
	}
}

func TestAddIndexKeysDedupBranch(t *testing.T) {
	buckets := bucketPostings{}
	addIndexKeys(buckets, "alpha alpha alpha", 5, 1, time.Time{})
	total := 0
	for _, toks := range buckets {
		for _, posts := range toks {
			total += len(posts)
		}
	}
	if total != 1 {
		t.Fatalf("addIndexKeys duplicate token dedup failed, total postings=%d", total)
	}
}
