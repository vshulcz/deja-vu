package main

import (
	"bytes"
	"strings"
	"testing"
)

// An output-path flag given an empty value — a scripted `--to "$OUT"` / `--out
// "$OUT"` / `-o "$OUT"` with the shell variable unset — must be refused like an
// absent one, not silently treated as "flag not passed" (#5891).
func TestOutputPathFlagsRejectEmptyValue(t *testing.T) {
	dir := t.TempDir()
	for _, val := range []string{"", "   "} {
		if err := runPromote(dir, []string{"sess1234", "--to", val}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "promote: --to needs a path") {
			t.Fatalf("runPromote([sess1234 --to %q]) = %v; want 'promote: --to needs a path'", val, err)
		}
		if err := runView(dir, []string{"--out", val}); err == nil || !strings.Contains(err.Error(), "view: --out needs a path") {
			t.Fatalf("runView([--out %q]) = %v; want 'view: --out needs a path'", val, err)
		}
		if err := runRestore(dir, []string{"sess1234", "-o", val}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "restore: -o needs value") {
			t.Fatalf("runRestore([sess1234 -o %q]) = %v; want 'restore: -o needs value'", val, err)
		}
	}
}
