package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteBackAppendKeepsTheLastLineWhole(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "session_index.jsonl")
	if err := os.WriteFile(p, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeBackAppend(root, p, []byte(`{"b":2}`+"\n")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != `{"a":1}`+"\n"+`{"b":2}`+"\n" {
		t.Errorf("index = %q", b)
	}
	if err := writeBackAppend(root, filepath.Join(t.TempDir(), "x.jsonl"), []byte("x\n")); err == nil {
		t.Error("appended outside the store")
	}
}
