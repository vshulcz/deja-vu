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

// The nine plugin-shaped wirings hold the same absolute path in JavaScript,
// TypeScript, Python, or quoted inside a command — and said "wired" while the
// binary they run is gone (#2688).
func TestDoctorNamesPluginHooksRunningABinaryThatIsGone(t *testing.T) {
	tmp := hermeticEnv(t)
	gone := filepath.Join(tmp, "old", "deja")

	cases := []struct {
		name string
		path string
		body string
	}{
		{"opencode", filepath.Join(opencodeConfigHome(), "opencode", "plugins", "deja.js"),
			"const raw = await $`\"" + gone + "\" hook-context`.text()\n"},
		{"hermes", filepath.Join(sources.HermesHome(), "plugins", "deja", "__init__.py"),
			"DEJA = \"" + gone + "\"\nsubprocess.run([DEJA, \"hook-context\"])\n"},
		{"goose", gooseHookPath(),
			"{\"hooks\":{\"session_start\":[{\"command\":\"'" + gone + "' hook-goose\"}]}}\n"},
	}
	for _, c := range cases {
		if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(c.path, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	doctorAutoRecall(&out)
	got := out.String()
	if n := strings.Count(got, "runs "+gone); n != len(cases) {
		t.Errorf("%d of %d rows name the binary that is gone:\n%s", n, len(cases), got)
	}
	for _, c := range cases {
		if !strings.Contains(got, "deja install "+c.name+"-auto") {
			t.Errorf("%s is not told how to repair itself:\n%s", c.name, got)
		}
	}

	// And a binary that is there is not called dead — the marker check has
	// nothing to do with this, so a wired row stays one line.
	here := filepath.Join(tmp, "deja")
	if err := os.WriteFile(here, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		b, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(c.path, bytes.ReplaceAll(b, []byte(gone), []byte(here)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out.Reset()
	doctorAutoRecall(&out)
	got = out.String()
	if strings.Contains(got, "which is not there") {
		t.Errorf("a plugin running the binary that is there was called dead:\n%s", got)
	}
	// And they are still wired: a check that quietly turned every row stale
	// would pass the line above.
	for _, c := range cases {
		wired := false
		for _, line := range strings.Split(got, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), c.name+" ") && strings.Contains(line, "wired") {
				wired = true
			}
		}
		if !wired {
			t.Errorf("%s no longer reads wired:\n%s", c.name, got)
		}
	}
}

// The files these rows read are not all deja's own code. aider's is a digest
// of past sessions, so any path a transcript once mentioned is in there, and
// the hook files themselves carry settings that end in deja without being it
// (#2688).
func TestDoctorDoesNotCallAPathInPassingADeadHook(t *testing.T) {
	tmp := hermeticEnv(t)
	nowhere := filepath.Join(tmp, "coding", "deja")

	aider := aiderContextPath()
	if err := os.MkdirAll(filepath.Dir(aider), 0o755); err != nil {
		t.Fatal(err)
	}
	// A digest of past sessions carries commands those sessions ran, deja's
	// own among them — the one shape that looks exactly like a live hook.
	digest := "- Session: ran `" + nowhere + " hook-context` after the rebuild\n"
	if err := os.WriteFile(aider, []byte(digest), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorAutoRecall(&out)
	if got := out.String(); strings.Contains(got, "which is not there") {
		t.Errorf("a path quoted in a recalled transcript was read as a hook:\n%s", got)
	}

	// And a setting whose name ends in deja is a directory, not the binary.
	claude := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	here := filepath.Join(tmp, "deja")
	if err := os.WriteFile(here, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{"env":{"DEJA_INDEX_DIR":"` + filepath.Join(tmp, "cache", "deja") + `"},` +
		`"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"` + here + ` hook-context"}]}]}}`
	if err := os.WriteFile(claude, []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorHooks(&out)
	if got := out.String(); strings.Contains(got, "which is not there") {
		t.Errorf("an index directory was read as the binary:\n%s", got)
	}
}
