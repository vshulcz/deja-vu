package search

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestResolveBlamePathSymbolRangeGoAndPython(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "worker.go")
	goSrc := "package worker\n\nfunc helper() {\n\treturn\n}\n\nfunc (w *Worker) RunTask(id string) error {\n\tif id == \"\" {\n\t\treturn nil\n\t}\n\treturn nil\n}\n"
	if err := os.WriteFile(goFile, []byte(goSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	target, err := ResolveBlamePath(goFile + ":RunTask")
	if err != nil {
		t.Fatalf("ResolveBlamePath: %v", err)
	}
	if target.Symbol != "RunTask" || target.Line != 7 || target.EndLine != 12 || target.LineNote != "" {
		t.Fatalf("Go symbol target = %+v, want Symbol=RunTask Line=7 EndLine=12", target)
	}

	pyFile := filepath.Join(dir, "runner.py")
	pySrc := "import os\n\ndef other():\n    pass\n\ndef compute_budget(items):\n    total = 0\n    for x in items:\n        total += x\n    return total\n\nclass Next:\n    pass\n"
	if werr := os.WriteFile(pyFile, []byte(pySrc), 0o600); werr != nil {
		t.Fatal(werr)
	}

	pyTarget, err := ResolveBlamePath(pyFile + ":compute_budget")
	if err != nil {
		t.Fatalf("ResolveBlamePath py: %v", err)
	}
	if pyTarget.Symbol != "compute_budget" || pyTarget.Line != 6 || pyTarget.EndLine != 10 {
		t.Fatalf("Python symbol target = %+v, want Symbol=compute_budget Line=6 EndLine=10", pyTarget)
	}
}

func TestBlameSymbolPrioritizesSessionMentioningSymbol(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "pool.go")
	goSrc := "package pool\n\nfunc OpenPool() {}\n\nfunc ClosePool() {\n\treturn\n}\n"
	if err := os.WriteFile(goFile, []byte(goSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	target, err := ResolveBlamePath(goFile + ":ClosePool")
	if err != nil {
		t.Fatal(err)
	}

	t0 := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	sessions := []model.Session{
		{
			ID:      "newer-unrelated-symbol",
			Harness: "claude",
			Project: dir,
			Updated: t0.Add(time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "Update " + goFile + " to tweak OpenPool."},
				{Role: "assistant", Text: "Updated OpenPool in " + goFile + "."},
			},
		},
		{
			ID:      "older-matching-symbol",
			Harness: "claude",
			Project: dir,
			Updated: t0,
			Messages: []model.Message{
				{Role: "user", Text: "Fix ClosePool leak in " + goFile + "."},
				{Role: "assistant", Text: "Fixed ClosePool in " + goFile + " because idle connections lingered."},
			},
		},
	}

	hits := Blame(sessions, target, BlameOptions{})
	if len(hits) != 2 {
		t.Fatalf("len(hits) = %d, want 2", len(hits))
	}
	if hits[0].Session.ID != "older-matching-symbol" {
		t.Fatalf("top hit = %s, want older-matching-symbol that touched ClosePool", hits[0].Session.ID)
	}
}
