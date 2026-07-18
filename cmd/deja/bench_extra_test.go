package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/bench"
	"github.com/vshulcz/deja-vu/internal/model"
)

func TestWriteBenchCorpusRoundtrip(t *testing.T) {
	tmp := t.TempDir()
	sessions := bench.Generate(bench.Seed).Sessions
	if err := writeBenchCorpus(tmp, sessions[:5]); err != nil {
		t.Fatal(err)
	}
	found := 0
	if err := filepath.Walk(tmp, func(path string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			found++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if found != 5 {
		t.Fatalf("wrote %d files, want 5", found)
	}
	// Error path: root squatted by a file.
	squat := filepath.Join(tmp, "sq")
	if err := os.WriteFile(squat, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeBenchCorpus(filepath.Join(squat, "sub"), []model.Session{{ID: "a", Project: "p", Messages: []model.Message{{Role: "user", Text: "t"}}}}); err == nil {
		t.Fatal("expected mkdir error")
	}
}
