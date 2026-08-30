package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The MCP rows learned in #2216 that "wired" is not "can start". The hook rows
// print the same word for a hook whose command names a binary that is gone —
// and the recorded-path check goes quiet as soon as one target is reinstalled
// from the new location, which stamps the record for every target. Doctor then
// says nothing at all about a dead recall hook (#2686).
func TestDoctorNamesHooksRunningABinaryThatIsGone(t *testing.T) {
	tmp := hermeticEnv(t)
	gone := filepath.Join(tmp, "old", "deja")

	claude := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": gone + " hook-context"},
		}}},
		"PreCompact": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": gone + " hook-precompact"},
		}}},
	}}
	b, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, b, 0o644); err != nil {
		t.Fatal(err)
	}

	cursor := filepath.Join(sources.CursorCLIHome(), "hooks.json")
	if err := os.MkdirAll(filepath.Dir(cursor), 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := map[string]any{"hooks": map[string]any{
		"sessionStart": []any{map[string]any{"command": gone + " hook-context"}},
	}}
	if b, err = json.Marshal(hooks); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cursor, b, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorHooks(&out)
	got := out.String()
	for _, want := range []string{
		"runs " + gone + ", which is not there",
		"deja install claude-auto",
		"deja install cursor-auto",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor does not say %q:\n%s", want, got)
		}
	}

	// A hook that runs a binary which is there says nothing extra.
	here := filepath.Join(tmp, "deja")
	if err := os.WriteFile(here, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{claude, cursor} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, bytes.ReplaceAll(b, []byte(gone), []byte(here)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out.Reset()
	doctorHooks(&out)
	if got := out.String(); strings.Contains(got, "which is not there") {
		t.Errorf("a hook running the binary that is there was called dead:\n%s", got)
	}
}
