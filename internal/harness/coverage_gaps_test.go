package harness_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/harness"
)

func TestForeignTOMLAndJSONCScannerCoverage(t *testing.T) {
	dir := t.TempDir()

	// Foreign TOML command block (no "mcp" token) is replaced cleanly while preserving disabled flag.
	foreignPath := filepath.Join(dir, "foreign.toml")
	foreignBody := `[mcp_servers.deja]
command = "npx"
args = ["other-mcp"]
disabled = true

[mcp_servers.alt]
args = ["deja", "run"]
`
	if err := os.WriteFile(foreignPath, []byte(foreignBody), 0o600); err != nil {
		t.Fatal(err)
	}
	fspec := &harness.TOMLMCPSpec{
		Path:  foreignPath,
		Block: "[mcp_servers.deja]\ncommand = \"deja\"\nargs = [\"mcp\"]",
	}
	probe, err := fspec.Probe()
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if !slices.Equal(probe.DejaKeys, []string{"deja", "alt"}) {
		t.Fatalf("Probe DejaKeys = %v, want [deja alt]", probe.DejaKeys)
	}
	wres, err := fspec.Wire()
	if err != nil || !wres.PreservedDisabled {
		t.Fatalf("Wire = %+v, err=%v", wres, err)
	}
	after, _ := os.ReadFile(foreignPath)
	if strings.Contains(string(after), "other-mcp") || !strings.Contains(string(after), "disabled = true") {
		t.Fatalf("expected foreign block replaced cleanly, got:\n%s", after)
	}

	// JSONC block comments, escapes, nested array/object values, and atomic write failure.
	jsoncPath := filepath.Join(dir, "complex.jsonc")
	jsoncBody := "/* header\n   multiline */ {\n  \"esc\": \"a\\\\\\\"b\\nc\",\n  \"arr\": [1, \"x\", {\"k\": false}],\n  \"mcpServers\": {\n    \"keep\": {\"command\": \"npx\", \"args\": [\"y\"]}\n  }\n}"
	if err := os.WriteFile(jsoncPath, []byte(jsoncBody), 0o600); err != nil {
		t.Fatal(err)
	}
	jspec := &harness.JSONMCPSpec{
		Path:     jsoncPath,
		BlockKey: "mcpServers",
		Entry:    map[string]any{"command": "deja", "args": []string{"mcp"}},
	}
	if _, err := jspec.Wire(); err != nil {
		t.Fatalf("JSONC Wire failed: %v", err)
	}
	if _, err := jspec.Unwire(); err != nil {
		t.Fatalf("JSONC Unwire failed: %v", err)
	}

	// File parent collision exercises writeFileAtomic error path.
	blockingFile := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blockingFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	badSpec := &harness.JSONMCPSpec{
		Path:  filepath.Join(blockingFile, "config.json"),
		Entry: map[string]any{"command": "deja"},
	}
	if _, err := badSpec.Wire(); err == nil {
		t.Fatal("expected error writing under regular file")
	}

	// FileAssetSpec RemoveFile error and Probe/Wire/Unwire branches.
	assetPath := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(assetPath, []byte("custom body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	editedSpec := &harness.FileAssetSpec{
		HarnessName:    "skill",
		Path:           assetPath,
		Content:        []byte("deja body\n"),
		GuardUserEdits: true,
		CheckEdited:    func(_ string, existing []byte) bool { return strings.Contains(string(existing), "custom") },
		CheckWired:     func(_ string, existing []byte) bool { return strings.Contains(string(existing), "deja") },
		DropRetired:    func(string, bool) error { return errors.New("drop err") },
		Ops: &harness.Ops{
			RemoveFile: func(string) error { return errors.New("rm err") },
		},
	}
	if ap, err := editedSpec.Probe(); err != nil || ap.Wired {
		t.Fatalf("unexpected edited Probe = %+v, err=%v", ap, err)
	}
	if _, err := editedSpec.Wire(); err == nil || !strings.Contains(err.Error(), "drop err") {
		t.Fatalf("expected drop retired error, got %v", err)
	}
	editedSpec.DropRetired = nil
	if aw, err := editedSpec.Wire(); err != nil || aw.Action != harness.ActionSkipped {
		t.Fatalf("expected ActionSkipped for edited file, got %+v, err=%v", aw, err)
	}
	if _, err := editedSpec.Unwire(); err == nil || !strings.Contains(err.Error(), "rm err") {
		t.Fatalf("expected RemoveFile error on Unwire, got %v", err)
	}

	// FileAssetSpec clean Unwire + Probe missing/wired + TOML Lookup adopted key persistence.
	cleanPath := filepath.Join(dir, "clean", "SKILL.md")
	var pruneCalled int
	var noteDirs int
	cleanSpec := &harness.FileAssetSpec{
		HarnessName:       "skill",
		Path:              cleanPath,
		Content:           []byte("deja body\n"),
		PruneEmptyParents: 1,
		Ops: &harness.Ops{
			PruneDir:        func(string) { pruneCalled++ },
			NoteCreatedDirs: func(string) { noteDirs++ },
		},
	}
	if p, err := cleanSpec.Probe(); err != nil || p.Present || p.Wired {
		t.Fatalf("missing file Probe = %+v, err=%v", p, err)
	}
	if w, err := cleanSpec.Wire(); err != nil || w.Action != harness.ActionWrote || noteDirs == 0 {
		t.Fatalf("clean Wire = %+v, err=%v, noteDirs=%d", w, err, noteDirs)
	}
	if p, err := cleanSpec.Probe(); err != nil || !p.Present || !p.Wired {
		t.Fatalf("wired Probe = %+v, err=%v", p, err)
	}
	if u, err := cleanSpec.Unwire(); err != nil || u.Action != harness.ActionRemoved || pruneCalled == 0 {
		t.Fatalf("clean Unwire = %+v, err=%v, pruneCalled=%d", u, err, pruneCalled)
	}
	if u2, err := cleanSpec.Unwire(); err != nil || u2.Action != harness.ActionUnchanged {
		t.Fatalf("idempotent Unwire = %+v, err=%v", u2, err)
	}

	// Default memory-backed TOML adoption + Unwire round-trip + invalid TOML & JSONC trailing comma.
	memTOMLPath := filepath.Join(dir, "mem.toml")
	if err := os.WriteFile(memTOMLPath, []byte("[mcp_servers.custom-deja]\ncommand = \"deja\"\nargs = [\"mcp\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	memSpec := &harness.TOMLMCPSpec{
		Path:  memTOMLPath,
		Block: "[mcp_servers.deja]\ncommand = \"deja\"\nargs = [\"mcp\"]",
	}
	if w, err := memSpec.Wire(); err != nil || w.AdoptedKey != "custom-deja" {
		t.Fatalf("mem TOML Wire = %+v, err=%v", w, err)
	}
	// Second Wire reuses adopted memory record; then Unwire removes it.
	if w2, err := memSpec.Wire(); err != nil || w2.AdoptedKey != "custom-deja" {
		t.Fatalf("second mem TOML Wire = %+v, err=%v", w2, err)
	}
	if u, err := memSpec.Unwire(); err != nil || !slices.Equal(u.RemovedKeys, []string{"custom-deja"}) {
		t.Fatalf("mem TOML Unwire = %+v, err=%v", u, err)
	}

	// Trailing-comma JSONC without comments and empty []any command edge.
	tcPath := filepath.Join(dir, "trailing.jsonc")
	if err := os.WriteFile(tcPath, []byte("{\n  \"note\": \"// not a comment\",\n  \"mcpServers\": {\n    \"bad\": {\"command\": []},\n  },\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tcSpec := &harness.JSONMCPSpec{Path: tcPath, Entry: map[string]any{"command": "deja", "args": []string{"mcp"}}}
	if _, err := tcSpec.Wire(); err != nil {
		t.Fatalf("trailing JSONC Wire failed: %v", err)
	}
}
