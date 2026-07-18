package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

func TestRememberCLIAndMCP(t *testing.T) {
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(t.TempDir(), "notes.jsonl"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	if err := runRemember([]string{"--project", "cli-project", "durable cli decision"}); err != nil {
		t.Fatal(err)
	}
	result, err := callMCPTool("remember", []byte(`{"text":"durable mcp conclusion"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "notes") {
		t.Fatalf("mcp result=%q", result)
	}
	if err := index.EnsureForSearch(index.DefaultDir(), search.Options{All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := index.Search(index.DefaultDir(), search.Options{Query: "durable", All: true})
	if err != nil || len(ss) != 2 {
		t.Fatalf("search=%#v err=%v", ss, err)
	}
}

func TestRememberValidation(t *testing.T) {
	if err := run([]string{"version"}); err != nil {
		t.Fatal(err)
	}
	if err := run(nil); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"sources"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"remember"}); err == nil {
		t.Fatal("dispatch accepted missing text")
	}
	for _, args := range [][]string{{}, {""}, {"--project"}, {"--bad", "text"}, {"one", "two"}} {
		if err := runRemember(args); err == nil {
			t.Fatalf("args=%v accepted", args)
		}
	}
}

func TestRememberDefaultProjectAndAppendError(t *testing.T) {
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(t.TempDir(), "notes.jsonl"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	if err := runRemember([]string{"defaulted project note"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", t.TempDir())
	if err := runRemember([]string{"cannot append"}); err == nil {
		t.Fatal("append to directory accepted")
	}
}

func TestRememberMCPValidation(t *testing.T) {
	if _, err := callMCPTool("remember", []byte("{")); err == nil {
		t.Fatal("malformed remember arguments accepted")
	}
	t.Setenv("DEJA_NOTES_FILE", t.TempDir())
	if _, err := callMCPTool("remember", []byte(`{"text":"stored"}`)); err == nil {
		t.Fatal("MCP append error ignored")
	}
}
