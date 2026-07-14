package index

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	var first bytes.Buffer
	if err := Ensure(dir, "claude", false, &first); err != nil {
		t.Fatal(err)
	}
	if first.Len() == 0 {
		t.Fatal("first build did not print progress")
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
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
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
	if err := writeJSON(filepath.Join(dir, "manifest.json"), m); err != nil {
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
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
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
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
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
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	files := currentFiles("claude")
	if _, ok := files[link]; ok {
		t.Fatalf("symlink was indexed: %#v", files[link])
	}
}
