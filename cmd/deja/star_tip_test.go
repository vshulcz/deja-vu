package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStarTipLine(t *testing.T) {
	dir := t.TempDir()

	// Under 25 recalls returns "" and does not write the stamp.
	if got := starTipLine(dir, 0); got != "" {
		t.Fatalf("starTipLine(0) = %q, want empty", got)
	}
	if got := starTipLine(dir, 24); got != "" {
		t.Fatalf("starTipLine(24) = %q, want empty", got)
	}
	if got := starTipLine("", 25); got != "" {
		t.Fatalf("starTipLine(empty dir) = %q, want empty", got)
	}
	stamp := filepath.Join(dir, "star-tip-shown")
	if _, err := os.Stat(stamp); err == nil {
		t.Fatalf("stamp file %s exists prematurely", stamp)
	}

	// At >= 25 recalls returns star tip on first call and writes stamp.
	const want = "  If it helps      gh repo star vshulcz/deja-vu · https://github.com/vshulcz/deja-vu"
	if got := starTipLine(dir, 25); got != want {
		t.Fatalf("starTipLine(25) = %q, want %q", got, want)
	}
	if _, err := os.Stat(stamp); err != nil {
		t.Fatalf("stamp file %s missing after first call: %v", stamp, err)
	}

	// Second call returns empty.
	if got := starTipLine(dir, 25); got != "" {
		t.Fatalf("starTipLine(25) second call = %q, want empty", got)
	}
	if got := starTipLine(dir, 50); got != "" {
		t.Fatalf("starTipLine(50) subsequent call = %q, want empty", got)
	}
}
