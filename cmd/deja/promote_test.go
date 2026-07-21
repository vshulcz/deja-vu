package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"io"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func promoteFixture(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-proj", "sess.jsonl"), "sess1234", []string{
		`{"type":"user","sessionId":"sess1234","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"how do we rotate the signing key without downtime"}}`,
		`{"type":"assistant","sessionId":"sess1234","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":[{"type":"text","text":"double-publish the JWKS for one TTL, then swap the active kid"}]}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPromoteWritesNoteWithProvenanceAndState(t *testing.T) {
	dir := promoteFixture(t)
	var out bytes.Buffer
	if err := runPromote(dir, []string{"sess1234"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "promoted claude:sess1234 as accepted") {
		t.Fatalf("receipt wrong:\n%s", out.String())
	}
	b, err := os.ReadFile(sources.NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{`"kind":"promoted"`, `"session":"claude:sess1234"`, `"state":"accepted"`, "signing key", "double-publish"} {
		if !strings.Contains(got, want) {
			t.Fatalf("note missing %q:\n%s", want, got)
		}
	}
}

func TestPromoteCorrectionAppends(t *testing.T) {
	dir := promoteFixture(t)
	if err := runPromote(dir, []string{"sess1234"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := runPromote(dir, []string{"sess1234", "--state", "superseded", "--note", "replaced by the KMS flow"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(sources.NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), `"session":"claude:sess1234"`) != 2 {
		t.Fatalf("correction must append, not rewrite:\n%s", b)
	}
	ss, err := sources.ParseNotesFile(sources.NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("both entries must fold into one note session, got %d", len(ss))
	}
	if !strings.HasSuffix(ss[0].Title, "[superseded]") {
		t.Fatalf("title must carry the latest state, got %q", ss[0].Title)
	}
	if len(ss[0].Messages) != 2 || !strings.Contains(ss[0].Messages[1].Text, "[superseded]") {
		t.Fatalf("messages must keep the full history: %#v", ss[0].Messages)
	}
}

func TestPromoteRejectsBadStateAndNotes(t *testing.T) {
	dir := promoteFixture(t)
	if err := runPromote(dir, []string{"sess1234", "--state", "maybe"}, &bytes.Buffer{}); err == nil {
		t.Fatal("bad state must fail")
	}
	if err := runPromote(dir, []string{"missing"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown prefix must fail")
	}
}

func TestPromoteExportsMarkdown(t *testing.T) {
	dir := promoteFixture(t)
	md := filepath.Join(t.TempDir(), "NOTES.md")
	if err := runPromote(dir, []string{"sess1234", "--to", md}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(md)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{"- state: accepted", "- source: claude:sess1234", "double-publish"} {
		if !strings.Contains(got, want) {
			t.Fatalf("markdown missing %q:\n%s", want, got)
		}
	}
}

func TestPromotedNoteOutranksTranscriptInSearch(t *testing.T) {
	dir := promoteFixture(t)
	if err := runPromote(dir, []string{"sess1234", "--note", "signing key rotation: double-publish JWKS for one TTL then swap kid"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	hits := searchHits(t, dir, "signing key")
	if len(hits) < 2 {
		t.Fatalf("want note + transcript, got %d hits", len(hits))
	}
	if hits[0].Session.Harness != "deja" {
		t.Fatalf("promoted note must rank first, got %s:%s", hits[0].Session.Harness, hits[0].Session.ID)
	}
	if !strings.Contains(hits[0].Session.Title, "[accepted]") {
		t.Fatalf("state must show in the result title, got %q", hits[0].Session.Title)
	}
}

func searchHits(t *testing.T, dir, q string) []search.Hit {
	t.Helper()
	o := search.Options{Query: q, All: true}
	result, err := index.SearchWithRecoveryDetailed(dir, o, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	o.Tier = result.Tier
	hits, err := search.Run(result.Sessions, o)
	if err != nil {
		t.Fatal(err)
	}
	return hits
}
