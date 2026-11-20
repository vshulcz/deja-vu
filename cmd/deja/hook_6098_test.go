package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestRepoSymbolMapDiskCachePersistenceAndInvalidation(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	claudeProj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(claudeProj, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"quokka retry budget"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/repo"}` + "\n"
	if err := os.WriteFile(filepath.Join(claudeProj, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	prevDir := getActiveDejaDir()
	setActiveDejaDir(dir)
	t.Cleanup(func() { setActiveDejaDir(prevDir) })

	repo := filepath.Join(tmp, "sample-repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(repo, "alpha.go")
	if err := os.WriteFile(src, []byte("package sample\n\nfunc QuokkaRetryBudget() int { return 3 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repoSymbolMapCache.Delete(repo) })

	got1 := loadRepoSymbolMap(repo)
	if got1.numFiles != 1 || len(got1.byStem["alpha"]) != 1 || len(got1.firstDecl) == 0 {
		t.Fatalf("unexpected initial symbol map: numFiles=%d byStem=%v decls=%d", got1.numFiles, got1.byStem, len(got1.firstDecl))
	}
	symFiles, err := filepath.Glob(dir + ".symmap-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(symFiles) != 1 {
		t.Fatalf("expected 1 .symmap disk cache file beside %s, got %d (%v)", dir, len(symFiles), symFiles)
	}

	repoSymbolMapCache.Delete(repo)
	diskHit, ok := loadRepoSymbolMapFromDisk(dir, safeScanRoot(repo), repo)
	if !ok {
		t.Fatal("expected valid disk cache hit immediately after cold scan")
	}
	if diskHit.numFiles != 1 || len(diskHit.byStem["alpha"]) != 1 {
		t.Fatalf("disk cache returned stale/incomplete map: numFiles=%d byStem=%v", diskHit.numFiles, diskHit.byStem)
	}

	got2 := loadRepoSymbolMap(repo)
	if got2.numFiles != 1 || len(got2.byStem["alpha"]) != 1 {
		t.Fatalf("loadRepoSymbolMap failed to serve from disk cache: numFiles=%d", got2.numFiles)
	}

	// Adding a new file invalidates the directory mtime and triggers a fresh scan.
	if err := os.WriteFile(filepath.Join(repo, "beta.go"), []byte("package sample\n\nfunc BetaWorker() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repoSymbolMapCache.Delete(repo)
	got3 := loadRepoSymbolMap(repo)
	if got3.numFiles != 2 || len(got3.byStem["beta"]) != 1 {
		t.Fatalf("expected disk cache invalidation after adding beta.go, got numFiles=%d byStem=%v", got3.numFiles, got3.byStem)
	}

	dropHookCaches(dir)
	remaining, err := filepath.Glob(dir + ".symmap-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("dropHookCaches left .symmap files behind: %v", remaining)
	}
}

func TestAskerLineageCutForHarnessSkipsDeepseekExternalProbes(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	claudeProj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(claudeProj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeProj, "seed.jsonl"),
		[]byte(`{"type":"user","message":{"role":"user","content":"seed"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"seed-1","cwd":"/repo"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	parentSID := "parent-6098-0001"
	childSID := "child-6098-0002"
	grokDir := filepath.Join(tmp, "grok")
	t.Setenv("DEJA_GROK_ROOT", grokDir)
	sessDir := filepath.Join(grokDir, "sessions", "work", childSID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "updates.jsonl"),
		[]byte(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"subtask"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "summary.json"),
		[]byte(`{"info":{"id":"`+childSID+`","cwd":"/repo"},"session_kind":"headless","parent_session_id":"`+parentSID+`","forked_at":"2026-07-01T10:05:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if h, ok := askerHeadForHarness(childSID, "", "deepseek"); ok {
		t.Errorf("askerHeadForHarness(deepseek) probed external Grok store and returned %+v", h)
	}
	dsSet, _ := askerLineageCutForHarness(dir, childSID, "", "deepseek")
	if dsSet[parentSID] {
		t.Errorf("askerLineageCutForHarness(deepseek) should skip Grok parent lookup, got %v", dsSet)
	}

	if h, ok := askerHeadForHarness(childSID, "", ""); !ok || h.Parent != parentSID {
		t.Fatalf("askerHeadForHarness(\"\") = (%+v, %v), want Parent=%q", h, ok, parentSID)
	}
	defaultSet, _ := askerLineageCutForHarness(dir, childSID, "", "")
	if !defaultSet[parentSID] {
		t.Errorf("askerLineageCutForHarness(\"\") missing Grok parent %q: %v", parentSID, defaultSet)
	}
}
