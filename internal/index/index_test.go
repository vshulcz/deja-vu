package index

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func TestIndexIngestSkipAndSearch(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-shulcz-deja-vu")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"fast opencode needle"}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"answer text"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	var first bytes.Buffer
	if err := Ensure(dir, "claude", false, &first); err != nil {
		t.Fatal(err)
	}
	if first.Len() == 0 {
		t.Fatal("first build did not print progress")
	}
	if !HasManifest(dir) {
		t.Fatal("manifest missing after build")
	}
	projectSessions, err := RecentProject(dir, filepath.Join("deja", "vu"), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(projectSessions) != 1 || projectSessions[0].ID != "s1" || len(projectSessions[0].Messages) != 2 {
		t.Fatalf("bad project sessions: %#v", projectSessions)
	}
	filteredRecent, err := RecentMatching(dir, 2, search.Options{Harness: "claude", Project: "deja"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filteredRecent) != 1 || filteredRecent[0].ID != "s1" {
		t.Fatalf("bad filtered recent sessions: %#v", filteredRecent)
	}
	filteredRecent, err = RecentMatching(dir, 2, search.Options{Harness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filteredRecent) != 0 {
		t.Fatalf("unexpected filtered recent sessions: %#v", filteredRecent)
	}
	var second bytes.Buffer
	if err := Ensure(dir, "claude", false, &second); err != nil {
		t.Fatal(err)
	}
	if second.Len() != 0 {
		t.Fatalf("fresh index rebuilt unexpectedly: %q", second.String())
	}
	ss, err := Search(dir, search.Options{Query: "code"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "s1" || ss[0].Messages[0].Text != "fast opencode needle" {
		t.Fatalf("bad search sessions: %#v", ss)
	}
}

func TestSyncExportImportSearchIdempotent(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-sync")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"type":"user","sessionId":"sync1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"syncneedle question"}}` + "\n" +
		`{"type":"assistant","sessionId":"sync1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"syncneedle answer"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "sync1.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dirA := filepath.Join(tmp, "a.db")
	if err := EnsureForSearch(dirA, search.Options{Query: "syncneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	batchDir := filepath.Join(tmp, "batches")
	n, err := Export(dirA, batchDir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("exported %d, want 2", n)
	}
	again, err := Export(dirA, batchDir)
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Fatalf("second export = %d, want 0", again)
	}
	dirB := filepath.Join(tmp, "b.db")
	imported, err := Import(dirB, batchDir)
	if err != nil {
		t.Fatal(err)
	}
	if imported != 2 {
		t.Fatalf("imported %d, want 2", imported)
	}
	ss, err := Search(dirB, search.Options{Query: "syncneedle", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].Harness != "claude" || !strings.HasPrefix(ss[0].Project, "imported:") || len(ss[0].Messages) != 2 {
		t.Fatalf("bad imported search result: %#v", ss)
	}
	recent, err := Recent(dirB, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].Harness != "claude" {
		t.Fatalf("bad recent imported session: %#v", recent)
	}
	found, ok, err := FindByPrefix(dirB, recent[0].ID[:12])
	if err != nil {
		t.Fatal(err)
	}
	if !ok || len(found.Messages) != 2 {
		t.Fatalf("FindByPrefix imported ok=%v found=%#v", ok, found)
	}
	if imported, err = Import(dirB, batchDir); err != nil {
		t.Fatal(err)
	} else if imported != 0 {
		t.Fatalf("re-import added %d records", imported)
	}
	ss, err = Search(dirB, search.Options{Query: "syncneedle", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("re-import duplicated records: %#v", ss)
	}
}

func TestSyncImportBadJSONAndEmptyExport(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "idx.db")
	if err := initEmptyIndex(dir); err != nil {
		t.Fatal(err)
	}
	n, err := Export(dir, filepath.Join(tmp, "out"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("empty export=%d", n)
	}
	badDir := filepath.Join(tmp, "bad")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "bad.jsonl"), []byte("{bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(filepath.Join(tmp, "new.db"), badDir); err == nil || !strings.Contains(err.Error(), "bad.jsonl") {
		t.Fatalf("bad import err=%v", err)
	}
	if got := ImportedSessionID("claude", "abc"); !strings.HasPrefix(got, "imported-") {
		t.Fatalf("ImportedSessionID=%q", got)
	}
}

func TestMultiWordSearchUsesAllPostingsAndDoesNotFullScan(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-shulcz-deja-vu")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]string{
		"s1": "token comes before jwt and refresh later",
		"s2": "jwt only",
		"s3": "refresh token only",
	}
	for id, text := range fixtures {
		data := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":%q}}`+"\n", id, text)
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(dir, search.Options{Query: "jwt refresh token", Harness: "claude"}, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "jwt refresh token", Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := search.Run(ss, search.Options{Query: "jwt refresh token", Harness: "claude", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Session.ID != "s1" {
		t.Fatalf("multi-word AND failed: sessions=%#v hits=%#v", ss, hits)
	}

	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Sessions["claude:unposted"] = SessionMeta{ID: "unposted", Harness: "claude", Project: filepath.Join("deja", "vu"), Path: "manual", Updated: time.Now()}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	rec, err := os.OpenFile(filepath.Join(dir, "records.bin"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = writeRecord(rec, Record{Key: "claude:unposted", Role: "user", Text: "jwt only refresh would appear during a full scan", Time: time.Now()})
	if closeErr := rec.Close(); err != nil {
		t.Fatal(err)
	} else if closeErr != nil {
		t.Fatal(closeErr)
	}
	ss, err = Search(dir, search.Options{Query: "jwt only refresh", Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 0 {
		t.Fatalf("search fell back to full scan despite indexed query tokens: %#v", ss)
	}
}

func TestIncrementalOnlyReingestsChangedFile(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-shulcz-deja-vu")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	s1 := filepath.Join(proj, "s1.jsonl")
	s2 := filepath.Join(proj, "s2.jsonl")
	if err := os.WriteFile(s1, []byte(`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"alpha needle"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s2, []byte(`{"type":"user","sessionId":"s2","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"beta stable"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(s1, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:06:05Z","message":{"role":"assistant","content":"gamma appended"}}` + "\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second)
	_ = os.Chtimes(s1, now, now)
	var log bytes.Buffer
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "deja: updated 1 file (1 new messages)") {
		t.Fatalf("incremental log missing partial ingest line: %q", log.String())
	}
	if strings.Contains(log.String(), "indexing sessions") {
		t.Fatalf("incremental path printed scary full-index line: %q", log.String())
	}
	if lastIngestFiles != 1 {
		t.Fatalf("incremental ingest touched %d files, want 1", lastIngestFiles)
	}
	ss, err := Search(dir, search.Options{Query: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "s2" {
		t.Fatalf("unchanged file was not preserved: %#v", ss)
	}
	ss, err = Search(dir, search.Options{Query: "appended"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "s1" {
		t.Fatalf("changed file was not reingested: %#v", ss)
	}
}

func TestIncrementalAppendOneFileBenchmarkStyle(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-shulcz-large")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	const fileCount = 30
	for i := 0; i < fileCount; i++ {
		p := filepath.Join(proj, fmt.Sprintf("s%02d.jsonl", i))
		var b strings.Builder
		for j := 0; j < 30; j++ {
			fmt.Fprintf(&b, `{"type":"user","sessionId":"s%02d","timestamp":"2026-01-02T03:%02d:05Z","message":{"role":"user","content":"fixture stable-%02d message-%02d"}}`+"\n", i, j%60, i, j)
		}
		if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(dir, search.Options{Query: "stable", Harness: "claude"}, false, nil); err != nil {
		t.Fatal(err)
	}
	if lastIngestFiles != fileCount {
		t.Fatalf("full ingest touched %d files, want %d", lastIngestFiles, fileCount)
	}
	changed := filepath.Join(proj, "s17.jsonl")
	f, err := os.OpenFile(changed, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		fmt.Fprintf(f, `{"type":"assistant","sessionId":"s17","timestamp":"2026-01-02T04:%02d:05Z","message":{"role":"assistant","content":"one-file incremental-needle new-%02d"}}`+"\n", i, i)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(changed, now, now)
	var log bytes.Buffer
	if err := EnsureForSearch(dir, search.Options{Query: "incremental-needle", Harness: "claude"}, false, &log); err != nil {
		t.Fatal(err)
	}
	if lastIngestFiles != 1 {
		t.Fatalf("incremental ingest touched %d files, want exactly 1", lastIngestFiles)
	}
	if got, want := log.String(), "deja: updated 1 file (12 new messages)"; !strings.Contains(got, want) {
		t.Fatalf("incremental log = %q, want %q", got, want)
	}
	if strings.Contains(log.String(), "indexing sessions") {
		t.Fatalf("incremental path printed full-index line: %q", log.String())
	}
	ss, err := Search(dir, search.Options{Query: "incremental-needle", Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := search.Run(ss, search.Options{Query: "incremental-needle", Harness: "claude", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Session.ID != "s17" || hits[0].Count != 12 {
		t.Fatalf("bad incremental search hits: %#v", hits)
	}
}

func TestIndexRecentFindRecordsAndBranches(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	idx := filepath.Join(tmp, "custom-index")
	t.Setenv("DEJA_INDEX_DIR", idx)
	if DefaultDir() != idx {
		t.Fatalf("DefaultDir env ignored")
	}
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-demo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(proj, "s1.jsonl")
	line1 := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"alpha needle"}}` + "\n"
	line2 := `{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"beta answer"}}` + "\n"
	if err := os.WriteFile(p, []byte(line1+line2), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	if err := Ensure(idx, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := Recent(idx, 1); err != nil || len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("Recent=%#v err=%v", got, err)
	}
	if got, ok, err := FindByPrefix(idx, "s"); err != nil || !ok || len(got.Messages) != 2 {
		t.Fatalf("FindByPrefix=%#v ok=%v err=%v", got, ok, err)
	}
	recs, err := recordsForKey(filepath.Join(idx, "records.bin"), "claude:s1")
	if err != nil || len(recs) != 2 {
		t.Fatalf("records=%#v err=%v", recs, err)
	}
	if got, err := parseAppendedFile("", p, FileState{Size: int64(len(line1))}); err != nil || len(got) != 1 || got[0].Messages[0].Text != "beta answer" {
		t.Fatalf("parse appended=%#v err=%v", got, err)
	}
	if got, err := parseChangedFile("", p, FileState{}); err != nil || len(got) != 1 {
		t.Fatalf("parse changed=%#v err=%v", got, err)
	}
	if !strings.HasPrefix(bucket("a"), "x") || bucket("ab") != "ab" {
		t.Fatalf("short bucket failed")
	}

	// Shrinking a file forces the non-append replacement path.
	if err := os.WriteFile(p, []byte(line1), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(p, time.Now().Add(3*time.Second), time.Now().Add(3*time.Second))
	var log bytes.Buffer
	if err := Ensure(idx, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "incremental index") {
		t.Fatalf("want non-append incremental log, got %q", log.String())
	}
	if got, ok, err := FindByPrefix(idx, "s1"); err != nil || !ok || len(got.Messages) != 1 {
		t.Fatalf("after shrink=%#v ok=%v err=%v", got, ok, err)
	}

	// Removing the file also takes the rebuild/replacement path and drops records.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(proj, time.Now().Add(4*time.Second), time.Now().Add(4*time.Second))
	log.Reset()
	if err := Ensure(idx, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "removed_files=1") {
		t.Fatalf("want removed file log, got %q", log.String())
	}
	if got, err := Recent(idx, 10); err != nil || len(got) != 0 {
		t.Fatalf("recent after remove=%#v err=%v", got, err)
	}
}

func TestSetOpencodeLastUpdated(t *testing.T) {
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_OPENCODE_DB", db)
	files := map[string]FileState{db: {Path: db}}
	now := time.Now()
	setOpencodeLastUpdated(files, map[string]SessionMeta{"opencode:s": {Harness: "opencode", Updated: now}})
	if files[db].LastUpdated != now.UnixNano() {
		t.Fatalf("LastUpdated=%d", files[db].LastUpdated)
	}
}

func TestIndexHelperBranchCoverage(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_INDEX_DIR", "")
	if !strings.Contains(DefaultDir(), filepath.Join(".cache", "deja", "index.db")) {
		t.Fatalf("DefaultDir default=%q", DefaultDir())
	}
	if title := sessionTitle(modelSession("<local-command>x", "Caveat: y", "real title here")); title != "real title here" {
		t.Fatalf("sessionTitle=%q", title)
	}
	meta := SessionMeta{Harness: "claude", Project: "deja-vu", Updated: time.Now()}
	if !sessionMetaMatches(meta, search.Options{Harness: "claude", Project: "deja", Since: time.Hour}) || sessionMetaMatches(meta, search.Options{Harness: "codex"}) || sessionMetaMatches(meta, search.Options{Project: "missing"}) || sessionMetaMatches(SessionMeta{Updated: time.Now().Add(-48 * time.Hour)}, search.Options{Since: time.Hour}) {
		t.Fatalf("sessionMetaMatches branches failed")
	}

	codexRoot := filepath.Join(tmp, "codex")
	t.Setenv("DEJA_CODEX_ROOT", codexRoot)
	hist := filepath.Join(codexRoot, "history.jsonl")
	if err := os.MkdirAll(filepath.Dir(hist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hist, []byte(`{"session_id":"h","text":"history","ts":"2026-01-02T03:04:05Z"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	roll := filepath.Join(codexRoot, "sessions", "2026", "01", "02", "rollout-2026-01-02T03-04-05-r.jsonl")
	if err := os.MkdirAll(filepath.Dir(roll), 0o755); err != nil {
		t.Fatal(err)
	}
	rollLine := `{"timestamp":"2026-01-02T03:04:05Z","payload":{"role":"user","message":"roll msg"}}` + "\n"
	if err := os.WriteFile(roll, []byte(rollLine), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{hist, roll} {
		if got, err := parseChangedFile("", p, FileState{}); err != nil || len(got) != 1 {
			t.Fatalf("parseChanged %s=%#v %v", p, got, err)
		}
		if got, err := parseAppendedFile("", p, FileState{Size: 0}); err != nil || len(got) != 1 {
			t.Fatalf("parseAppended %s=%#v %v", p, got, err)
		}
	}
	db := filepath.Join(tmp, "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)
	if got, err := parseChangedFile("", db, FileState{LastUpdated: time.Now().UnixNano()}); err != nil || got != nil {
		t.Fatalf("opencode changed missing=%#v %v", got, err)
	}
	if got, err := parseAppendedFile("", db, FileState{}); err != nil || got != nil {
		t.Fatalf("opencode appended missing=%#v %v", got, err)
	}
	if got, err := parseChangedFile("", filepath.Join(tmp, "unknown.txt"), FileState{}); err != nil || got != nil {
		t.Fatalf("unknown=%#v %v", got, err)
	}
}

func modelSession(texts ...string) model.Session {
	s := model.Session{Harness: "claude", ID: "s"}
	for _, txt := range texts {
		s.Messages = append(s.Messages, model.Message{Role: "user", Text: txt})
	}
	return s
}

func BenchmarkColdEnsureSynthetic(b *testing.B) {
	tmp := b.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-shulcz-synthetic")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		b.Fatal(err)
	}
	const fileCount = 200
	const messagesPerFile = 200
	for i := 0; i < fileCount; i++ {
		p := filepath.Join(proj, fmt.Sprintf("s%03d.jsonl", i))
		var sb strings.Builder
		for j := 0; j < messagesPerFile; j++ {
			role := "user"
			if j%2 == 1 {
				role = "assistant"
			}
			fmt.Fprintf(&sb, `{"type":%q,"sessionId":"s%03d","timestamp":"2026-01-02T03:%02d:%02dZ","message":{"role":%q,"content":"synthetic cold index token file-%03d msg-%03d shared needle alpha beta gamma delta repeated words for tokenizer throughput"}}`+"\n", role, i, (j/60)%60, j%60, role, i, j)
		}
		if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	b.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := filepath.Join(tmp, fmt.Sprintf("index-%d.db", i))
		start := time.Now()
		if err := Ensure(dir, "claude", false, nil); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(time.Since(start).Milliseconds()), "ensure_ms")
	}
}

func BenchmarkWarmSearchSynthetic(b *testing.B) {
	tmp := b.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-shulcz-warm")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		b.Fatal(err)
	}
	const fileCount = 1250
	const messagesPerFile = 12
	for i := 0; i < fileCount; i++ {
		p := filepath.Join(proj, fmt.Sprintf("s%04d.jsonl", i))
		var sb strings.Builder
		for j := 0; j < messagesPerFile; j++ {
			role := "user"
			if j%2 == 1 {
				role = "assistant"
			}
			needle := "common filler"
			if i%125 == 0 && j == 3 {
				needle = "warm-single-digit-needle"
			}
			fmt.Fprintf(&sb, `{"type":%q,"sessionId":"s%04d","timestamp":"2026-01-02T03:%02d:%02dZ","message":{"role":%q,"content":"synthetic warm search corpus file-%04d msg-%02d %s alpha beta gamma delta repeated tokenizer words"}}`+"\n", role, i, (j/60)%60, j%60, role, i, j, needle)
		}
		if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	b.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	o := search.Options{Query: "warm-single-digit-needle", Harness: "claude", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		b.Fatal(err)
	}
	bench := func(name string, o search.Options, wantHits int) {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				start := time.Now()
				ss, err := Search(dir, o)
				if err != nil {
					b.Fatal(err)
				}
				hits, err := search.Run(ss, o)
				if err != nil {
					b.Fatal(err)
				}
				if len(hits) != wantHits {
					b.Fatalf("hits=%d, want %d", len(hits), wantHits)
				}
				b.ReportMetric(float64(time.Since(start).Microseconds())/1000, "warm_ms")
			}
		})
	}
	bench("selective", o, 10)
	bench("fat-top15", search.Options{Query: "synthetic", Harness: "claude"}, 15)
}

func TestEachRecordIgnoresTruncatedTail(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "records.bin")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeRecord(f, Record{Key: "claude:s1", SourcePath: "s1.jsonl", Role: "user", Text: "complete"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{99, 0, 0, 0, '{'}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	var got []Record
	if err := eachRecord(p, func(r Record) { got = append(got, r) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "complete" {
		t.Fatalf("bad records: %#v", got)
	}
}

func TestCurrentFilesSkipsSymlinks(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "project")
	outside := filepath.Join(tmp, "outside.jsonl")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(proj, "linked.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	files := currentFiles("claude")
	if _, ok := files[link]; ok {
		t.Fatalf("symlink was indexed: %#v", files[link])
	}
}

func TestOldJSONManifestRebuildsTransparently(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"old manifest rebuild needle"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := Manifest{Version: version - 1, Files: map[string]FileState{}, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), Scope: "h:claude"}
	if err := writeManifest(dir, old); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(dir, search.Options{Query: "needle", Harness: "claude"}, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "needle", Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "s1" {
		t.Fatalf("old index was not rebuilt: %#v", ss)
	}
}

func TestRedactsSecretsAtIngest(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	secrets := []string{
		"AKIA" + "ABCDEFGHIJKLMNOP",
		"aws_secret_access_key=abcdEFGH12345678abcdEFGH12345678",
		"api_key=0123456789abcdef",
		"password=abcdefabcdefabcd",
		"Authorization: abcdefabcdefabcd",
		"Bearer eyJhbGciOiJIUzI1NiJ9abcdef",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEAfakefakefakefakefake\n-----END RSA PRIVATE KEY-----",
		"ghp_" + "1234567890abcdefghijklmnop",
		"gho_1234567890abcdefghijklmnop",
		"gith" + "ub_pat_1234567890abcdefghijklmnop",
		"sk-1" + "2345678901234567890",
		"npm_" + "123456789012345678901234567890",
		"xoxb" + "-123456789012-123456789012-abcdefghijkl",
		"xoxp" + "-123456789012-123456789012-abcdefghijkl",
		"AIza123456789012345678901234567890",
		"postgres://user:pass1234567890@localhost/db",
	}
	text := "redactionmarker before " + strings.Join(secrets, " middle ") + " after"
	data := fmt.Sprintf(`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":%q}}`+"\n", text)
	p := filepath.Join(proj, "s1.jsonl")
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(dir, search.Options{Query: "redactionmarker", Harness: "claude"}, false, nil); err != nil {
		t.Fatal(err)
	}
	recordBytes, err := os.ReadFile(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if bytes.Contains(recordBytes, []byte(secret)) {
			t.Fatalf("records.bin contains secret %q", secret)
		}
		ss, err := Search(dir, search.Options{Query: secret, Harness: "claude"})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 0 {
			t.Fatalf("search for secret %q returned %#v", secret, ss)
		}
	}
	ss, err := Search(dir, search.Options{Query: "redactionmarker", Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || !strings.Contains(ss[0].Messages[0].Text, "[redacted:") || !strings.Contains(ss[0].Messages[0].Text, "before") || !strings.Contains(ss[0].Messages[0].Text, "after") {
		t.Fatalf("surrounding words not searchable/redacted: %#v", ss)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Redacted < len(secrets) || m.Files[p].Redactions < len(secrets) {
		t.Fatalf("redaction counters not reported: total=%d file=%d", m.Redacted, m.Files[p].Redactions)
	}
	stats, err := Redactions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != m.Redacted || stats.Files[p] != m.Files[p].Redactions {
		t.Fatalf("Redactions()=%#v manifest=%#v", stats, m.Files[p])
	}
}
