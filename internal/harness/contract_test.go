package harness_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/harness"
)

func allContractSpecs(t *testing.T) []harness.Spec {
	t.Helper()
	dir := t.TempDir()
	return []harness.Spec{
		&harness.JSONMCPSpec{
			HarnessName: "claude",
			Path:        filepath.Join(dir, "claude", "mcp.json"),
			BlockKey:    "mcpServers",
			EntryKey:    "deja",
			Entry: map[string]any{
				"command": "/usr/local/bin/deja",
				"args":    []string{"mcp"},
			},
		},
		&harness.TOMLMCPSpec{
			HarnessName: "codex",
			Path:        filepath.Join(dir, "codex", "config.toml"),
			BlockHeader: "[mcp_servers.deja]",
			Block:       "[mcp_servers.deja]\ncommand = \"/usr/local/bin/deja\"\nargs = [\"mcp\"]\n",
		},
		&harness.FileAssetSpec{
			HarnessName:       "codex-skill",
			Path:              filepath.Join(dir, "skills", "deja-history", "SKILL.md"),
			Content:           []byte("---\nname: deja-history\n---\n\nSearch past sessions.\n"),
			OwnedMarker:       "name: deja-history",
			GuardUserEdits:    true,
			PruneEmptyParents: 1,
		},
	}
}

func TestHarnessSpecStateMachineAndIdempotency(t *testing.T) {
	for _, spec := range allContractSpecs(t) {
		t.Run(spec.Name(), func(t *testing.T) {
			if spec.TargetPath() == "" {
				t.Fatal("TargetPath is empty")
			}
			det, err := spec.Detect()
			if err != nil {
				t.Fatalf("Detect(missing): %v", err)
			}
			if det.Present {
				t.Fatalf("Detect(missing) = true")
			}
			probe, err := spec.Probe()
			if err != nil {
				t.Fatalf("Probe(missing): %v", err)
			}
			if probe.Present || probe.Wired {
				t.Fatalf("Probe(missing) = %+v, want Present=false Wired=false", probe)
			}

			// Unwire on missing target must be a no-op and must not create the file.
			unwireMiss, err := spec.Unwire()
			if err != nil {
				t.Fatalf("Unwire(missing): %v", err)
			}
			if unwireMiss.Action != harness.ActionUnchanged {
				t.Fatalf("Unwire(missing) action = %q, want unchanged", unwireMiss.Action)
			}
			if _, statErr := os.Stat(spec.TargetPath()); !os.IsNotExist(statErr) {
				t.Fatalf("Unwire(missing) created target file %s", spec.TargetPath())
			}

			w1, err := spec.Wire()
			if err != nil {
				t.Fatalf("Wire(1): %v", err)
			}
			if w1.Action != harness.ActionWrote {
				t.Fatalf("Wire(1) action = %q, want wrote", w1.Action)
			}

			det, err = spec.Detect()
			if err != nil || !det.Present {
				t.Fatalf("Detect(wired) = (%+v, %v), want Present=true", det, err)
			}
			probe, err = spec.Probe()
			if err != nil || !probe.Present || !probe.Wired {
				t.Fatalf("Probe(wired) = (%+v, %v), want Present=true Wired=true", probe, err)
			}

			// Second Wire must be idempotent and leave bytes identical.
			before, err := os.ReadFile(spec.TargetPath())
			if err != nil {
				t.Fatal(err)
			}
			w2, err := spec.Wire()
			if err != nil {
				t.Fatalf("Wire(2): %v", err)
			}
			if w2.Action != harness.ActionUnchanged {
				t.Fatalf("Wire(2) action = %q, want unchanged", w2.Action)
			}
			after, err := os.ReadFile(spec.TargetPath())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("Wire(2) modified bytes:\nbefore=%q\nafter=%q", before, after)
			}

			u1, err := spec.Unwire()
			if err != nil {
				t.Fatalf("Unwire(1): %v", err)
			}
			if u1.Action != harness.ActionRemoved && u1.Action != harness.ActionUpdated {
				t.Fatalf("Unwire(1) action = %q, want removed or updated", u1.Action)
			}
			probe, err = spec.Probe()
			if err != nil {
				t.Fatalf("Probe(unwired): %v", err)
			}
			if probe.Wired {
				t.Fatalf("Probe(unwired) still wired: %+v", probe)
			}

			u2, err := spec.Unwire()
			if err != nil {
				t.Fatalf("Unwire(2): %v", err)
			}
			if u2.Action != harness.ActionUnchanged {
				t.Fatalf("Unwire(2) action = %q, want unchanged", u2.Action)
			}
		})
	}
}

func TestHarnessSpecDetectErrors(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "regular-file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := &harness.JSONMCPSpec{
		HarnessName: "claude",
		Path:        filepath.Join(blocker, "mcp.json"),
		Entry:       map[string]any{"command": "deja", "args": []string{"mcp"}},
	}
	if _, err := spec.Detect(); err == nil {
		t.Fatal("Detect through non-directory path succeeded, want error")
	}
}

func TestJSONAndTOMLCorruptionMatrix(t *testing.T) {
	cases := []struct {
		name      string
		kind      string
		content   string
		unwireErr bool
	}{
		{name: "malformed JSON", kind: "json", content: `{"mcpServers": {`, unwireErr: true},
		{name: "JSON null root", kind: "json", content: "null\n", unwireErr: true},
		{name: "JSON array root", kind: "json", content: "[]\n", unwireErr: true},
		{name: "JSON non-object block", kind: "json", content: `{"mcpServers": ["bad"]}`, unwireErr: false},
		{name: "TOML unclosed header", kind: "toml", content: "[mcp_servers.deja\ncommand = \"deja\"\n", unwireErr: true},
		{name: "TOML inline table conflict", kind: "toml", content: "mcp_servers = { other = { command = \"npx\" } }\n", unwireErr: false},
		{name: "TOML array of tables conflict", kind: "toml", content: "[[mcp_servers.deja]]\ncommand = \"deja\"\nargs = [\"mcp\"]\n", unwireErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config."+tc.kind)
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			var spec harness.Spec
			if tc.kind == "json" {
				spec = &harness.JSONMCPSpec{
					HarnessName: "test-json",
					Path:        path,
					BlockKey:    "mcpServers",
					Entry:       map[string]any{"command": "deja", "args": []string{"mcp"}},
				}
			} else {
				spec = &harness.TOMLMCPSpec{
					HarnessName: "test-toml",
					Path:        path,
					BlockHeader: "[mcp_servers.deja]",
					Block:       "[mcp_servers.deja]\ncommand = \"deja\"\nargs = [\"mcp\"]\n",
				}
			}
			if _, err := spec.Probe(); err == nil {
				t.Fatalf("Probe(%s) succeeded on corrupt input, want error", tc.name)
			}
			if _, err := spec.Wire(); err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("Wire(%s) err = %v, want error naming %s", tc.name, err, path)
			}
			afterWire, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(afterWire) != tc.content {
				t.Fatalf("Wire(%s) mutated corrupt file: got %q", tc.name, afterWire)
			}
			uRes, uErr := spec.Unwire()
			if tc.unwireErr && uErr == nil {
				t.Fatalf("Unwire(%s) succeeded, want error", tc.name)
			}
			if !tc.unwireErr && (uErr != nil || uRes.Action != harness.ActionUnchanged) {
				t.Fatalf("Unwire(%s) = (%+v, %v), want unchanged, nil", tc.name, uRes, uErr)
			}
		})
	}
}

func TestJSONCNestedCommentsTrailingCommasAndBOMCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openclaw.json")
	bom := "\xEF\xBB\xBF"
	raw := bom +
		"{\r\n" +
		"  // user heading comment\r\n" +
		"  \"mcp\": {\r\n" +
		"    /* keep this server */\r\n" +
		"    \"servers\": {\r\n" +
		"      \"other\": { \"command\": \"npx\", },\r\n" +
		"    },\r\n" +
		"  },\r\n" +
		"}\r\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := &harness.JSONMCPSpec{
		HarnessName: "openclaw",
		Path:        path,
		BlockKey:    "mcp.servers",
		Entry:       map[string]any{"command": "/usr/bin/deja", "args": []string{"mcp"}},
	}
	w, err := spec.Wire()
	if err != nil {
		t.Fatalf("Wire(JSONC): %v", err)
	}
	if w.Action != harness.ActionUpdated {
		t.Fatalf("Wire action = %q, want updated", w.Action)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.HasPrefix(s, bom) || !strings.Contains(s, "\r\n") {
		t.Fatalf("Wire(JSONC) dropped BOM or CRLF: %q", s)
	}
	if !strings.Contains(s, "// user heading comment") || !strings.Contains(s, "/* keep this server */") {
		t.Fatalf("Wire(JSONC) stripped comments: %s", s)
	}
	probe, err := spec.Probe()
	if err != nil || !probe.Wired {
		t.Fatalf("Probe(JSONC) = (%+v, %v), want Wired=true", probe, err)
	}
	if _, uErr := spec.Unwire(); uErr != nil {
		t.Fatalf("Unwire(JSONC): %v", uErr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "\"other\"") || strings.Contains(string(after), "\"deja\"") {
		t.Fatalf("Unwire(JSONC) unexpected content:\n%s", after)
	}
}

func TestForeignDejaAdoptionNonDejaFilesystemAndDuplicates(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mcp.json")
		initial := map[string]any{
			"mcpServers": map[string]any{
				"deja-old": map[string]any{
					"command": "/old/deja",
					"args":    []string{"mcp"},
				},
				"filesystem": map[string]any{
					"command": "npx",
					"args":    []string{"-y", "@modelcontextprotocol/server-filesystem", "/Users/alex/.deja"},
				},
			},
		}
		raw, _ := json.MarshalIndent(initial, "", "  ")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		spec := &harness.JSONMCPSpec{
			HarnessName: "cursor",
			Path:        path,
			BlockKey:    "mcpServers",
			Entry:       map[string]any{"command": "/new/deja", "args": []string{"mcp"}},
		}
		w, err := spec.Wire()
		if err != nil {
			t.Fatalf("Wire: %v", err)
		}
		if w.AdoptedKey != "deja-old" {
			t.Fatalf("AdoptedKey = %q, want deja-old", w.AdoptedKey)
		}
		if len(w.LeftKeys) != 0 {
			t.Fatalf("LeftKeys = %v, want empty (filesystem must not count)", w.LeftKeys)
		}
		probe, err := spec.Probe()
		if err != nil || !probe.Wired {
			t.Fatalf("Probe = (%+v, %v)", probe, err)
		}
		b, _ := os.ReadFile(path)
		var parsed map[string]map[string]map[string]any
		if jErr := json.Unmarshal(b, &parsed); jErr != nil {
			t.Fatal(jErr)
		}
		if _, ok := parsed["mcpServers"]["deja"]; ok {
			t.Fatalf("unexpected duplicate deja key created alongside adopted deja-old: %v", parsed)
		}
		if _, ok := parsed["mcpServers"]["filesystem"]; !ok {
			t.Fatal("non-deja filesystem server was removed")
		}
		// Add a second custom deja entry to verify LeftKeys reporting.
		parsed["mcpServers"]["deja-custom"] = map[string]any{"command": "deja", "args": []string{"mcp"}}
		b2, _ := json.MarshalIndent(parsed, "", "  ")
		if wErr := os.WriteFile(path, b2, 0o600); wErr != nil {
			t.Fatal(wErr)
		}
		w2, err := spec.Wire()
		if err != nil {
			t.Fatal(err)
		}
		if len(w2.LeftKeys) != 1 || w2.LeftKeys[0] != "deja-custom" {
			t.Fatalf("LeftKeys = %v, want [deja-custom]", w2.LeftKeys)
		}
		u, err := spec.Unwire()
		if err != nil {
			t.Fatal(err)
		}
		if len(u.LeftKeys) != 1 || u.LeftKeys[0] != "deja-custom" {
			t.Fatalf("Unwire LeftKeys = %v, want [deja-custom]", u.LeftKeys)
		}
		b3, _ := os.ReadFile(path)
		if !strings.Contains(string(b3), "filesystem") || !strings.Contains(string(b3), "deja-custom") || strings.Contains(string(b3), "deja-old") {
			t.Fatalf("unexpected Unwire result:\n%s", b3)
		}
	})

	t.Run("TOML", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		raw := "[mcp_servers.deja-old]\ncommand = \"/old/deja\"\nargs = [\"mcp\"]\n\n" +
			"[mcp_servers.filesystem]\ncommand = \"npx\"\nargs = [\"-y\", \"/Users/alex/.deja\"]\n"
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		spec := &harness.TOMLMCPSpec{
			HarnessName: "codex",
			Path:        path,
			BlockHeader: "[mcp_servers.deja]",
			Block:       "[mcp_servers.deja]\ncommand = \"/new/deja\"\nargs = [\"mcp\"]\n",
		}
		w, err := spec.Wire()
		if err != nil {
			t.Fatal(err)
		}
		if w.AdoptedKey != "deja-old" {
			t.Fatalf("TOML AdoptedKey = %q, want deja-old", w.AdoptedKey)
		}
		if len(w.LeftKeys) != 0 {
			t.Fatalf("TOML LeftKeys = %v, want empty", w.LeftKeys)
		}
		if _, err := spec.Unwire(); err != nil {
			t.Fatal(err)
		}
		after, _ := os.ReadFile(path)
		if !strings.Contains(string(after), "[mcp_servers.filesystem]") || strings.Contains(string(after), "deja-old") {
			t.Fatalf("unexpected TOML after Unwire:\n%s", after)
		}
	})
}
