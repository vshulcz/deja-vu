package main

import (
	"bytes"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// An output-path flag given an empty value — a scripted `--to "$OUT"` / `--out
// "$OUT"` / `-o "$OUT"` with the shell variable unset — must be refused like an
// absent one, not silently treated as "flag not passed" (#5891).
func TestOutputPathFlagsRejectEmptyValue(t *testing.T) {
	if err := runPromote(index.DefaultDir(), []string{"--to", ""}, &bytes.Buffer{}); err == nil {
		t.Fatal(`runPromote(..., ["--to", ""]) = nil; want an error`)
	}
	if err := runView(index.DefaultDir(), []string{"--out", ""}); err == nil {
		t.Fatal(`runView(..., ["--out", ""]) = nil; want an error`)
	}
	if err := runRestore(index.DefaultDir(), []string{"-o", ""}, &bytes.Buffer{}); err == nil {
		t.Fatal(`runRestore(..., ["-o", ""]) = nil; want an error`)
	}
}
