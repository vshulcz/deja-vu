package harness_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/harness"
)

func TestSwitchedOffPreservationAndTOMLSubtableRemoval(t *testing.T) {
	t.Run("JSON disabled and enabled=false", func(t *testing.T) {
		for _, tc := range []struct {
			flag string
			val  any
		}{
			{flag: "disabled", val: true},
			{flag: "enabled", val: false},
		} {
			path := filepath.Join(t.TempDir(), "mcp.json")
			initCfg := map[string]any{
				"mcpServers": map[string]any{
					"deja": map[string]any{
						"command": "/old/deja",
						"args":    []string{"mcp"},
						tc.flag:   tc.val,
					},
				},
			}
			raw, _ := json.MarshalIndent(initCfg, "", "  ")
			_ = os.WriteFile(path, raw, 0o600)

			spec := &harness.JSONMCPSpec{
				HarnessName: "claude",
				Path:        path,
				Entry:       map[string]any{"command": "/new/deja", "args": []string{"mcp"}},
			}
			probe, err := spec.Probe()
			if err != nil || !probe.Wired || !probe.Disabled {
				t.Fatalf("Probe(%s) = (%+v, %v), want Wired=true Disabled=true", tc.flag, probe, err)
			}
			w, err := spec.Wire()
			if err != nil {
				t.Fatal(err)
			}
			if !w.PreservedDisabled {
				t.Fatalf("Wire(%s) did not preserve disabled state: %+v", tc.flag, w)
			}
			got, _ := os.ReadFile(path)
			var out map[string]map[string]map[string]any
			_ = json.Unmarshal(got, &out)
			if out["mcpServers"]["deja"][tc.flag] != tc.val {
				t.Fatalf("switch %s=%v lost after Wire: %v", tc.flag, tc.val, out["mcpServers"]["deja"])
			}
			if _, err := spec.Unwire(); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("TOML enabled=false disabled=true and sub-table removal (#2716)", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		raw := "[mcp_servers.deja]\ncommand = \"/old/deja\"\nargs = [\"mcp\"]\nenabled = false\n\n" +
			"[mcp_servers.deja.env]\nFOO = \"bar\"\n\n" +
			"[mcp_servers.deja_other]\ncommand = \"npx\"\n"
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		spec := &harness.TOMLMCPSpec{
			HarnessName: "codex",
			Path:        path,
			BlockHeader: "[mcp_servers.deja]",
			Block:       "[mcp_servers.deja]\ncommand = \"/new/deja\"\nargs = [\"mcp\"]\n",
		}
		probe, err := spec.Probe()
		if err != nil || !probe.Wired || !probe.Disabled {
			t.Fatalf("Probe = (%+v, %v), want Wired=true Disabled=true", probe, err)
		}
		w, err := spec.Wire()
		if err != nil {
			t.Fatal(err)
		}
		if !w.PreservedDisabled {
			t.Fatalf("PreservedDisabled = false: %+v", w)
		}
		afterWire, _ := os.ReadFile(path)
		if !strings.Contains(string(afterWire), "enabled = false") || !strings.Contains(string(afterWire), "/new/deja") {
			t.Fatalf("Wire lost enabled=false or command:\n%s", afterWire)
		}
		if _, err := spec.Unwire(); err != nil {
			t.Fatal(err)
		}
		afterUnwire, _ := os.ReadFile(path)
		s := string(afterUnwire)
		if strings.Contains(s, "[mcp_servers.deja]") || strings.Contains(s, "[mcp_servers.deja.env]") {
			t.Fatalf("Unwire leaked deja or sub-table:\n%s", s)
		}
		if !strings.Contains(s, "[mcp_servers.deja_other]") {
			t.Fatalf("Unwire deleted sibling deja_other table:\n%s", s)
		}
	})
}

func TestFileAssetEditedGuardRetiredHookAndDirPruning(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "deja-history")
	target := filepath.Join(skillDir, "SKILL.md")
	retired := filepath.Join(dir, "skills", "old-deja", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(retired), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(retired, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userEdited := []byte("---\nname: deja-history\n---\n\nuser customization\n")
	if err := os.WriteFile(target, userEdited, 0o600); err != nil {
		t.Fatal(err)
	}

	retiredCalled := false
	spec := &harness.FileAssetSpec{
		HarnessName:       "codex-skill",
		Path:              target,
		Content:           []byte("---\nname: deja-history\n---\n\ncanonical\n"),
		OwnedMarker:       "name: deja-history",
		GuardUserEdits:    true,
		PruneEmptyParents: 1,
		DropRetired: func(_ string, _ bool) error {
			retiredCalled = true
			if err := os.Remove(retired); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		},
	}

	w, err := spec.Wire()
	if err != nil {
		t.Fatalf("Wire(edited): %v", err)
	}
	if !retiredCalled {
		t.Fatal("DropRetired hook was not called")
	}
	if _, statErr := os.Stat(retired); !os.IsNotExist(statErr) {
		t.Fatalf("retired file still exists: %v", statErr)
	}
	if w.Action != harness.ActionSkipped || !w.EditedByUser {
		t.Fatalf("Wire(edited) = %+v, want Action=skipped EditedByUser=true", w)
	}
	got, _ := os.ReadFile(target)
	if !bytes.Equal(got, userEdited) {
		t.Fatalf("Wire(edited) overwrote user edits: %q", got)
	}

	spec.Force = true
	wForce, err := spec.Wire()
	if err != nil || wForce.Action != harness.ActionUpdated {
		t.Fatalf("Wire(force) = (%+v, %v), want Action=updated", wForce, err)
	}

	if _, err := spec.Unwire(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Fatalf("empty skillDir was not pruned: %v", err)
	}

	spec.Force = false
	if _, err := spec.Wire(); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(skillDir, "README.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := spec.Unwire(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("sibling file in dir vanished: %v", err)
	}
}

func TestOpsHooksAndDeepCoverageEdgeCases(t *testing.T) {
	t.Run("Ops callbacks and JSON added-block collapse", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "openclaw.json")
		if err := os.WriteFile(path, []byte("{\n  \"theme\": \"dark\"\n}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var beforeWriteCalled int
		addedBlocks := map[string]bool{}
		adopted := map[string][]string{}
		ops := &harness.Ops{
			BeforeWrite: func(_ string, _ []byte) error {
				beforeWriteCalled++
				return nil
			},
			RecordBlockAdded:  func(p, bp string) { addedBlocks[p+"\x00"+bp] = true },
			WasBlockAdded:     func(p, bp string) bool { return addedBlocks[p+"\x00"+bp] },
			ForgetBlockAdded:  func(p, bp string) { delete(addedBlocks, p+"\x00"+bp) },
			RecordAdoptedKey:  func(p, bp, k string) { adopted[p+"\x00"+bp] = append(adopted[p+"\x00"+bp], k) },
			AdoptedKeys:       func(p, bp string) []string { return adopted[p+"\x00"+bp] },
			ForgetAdoptedKeys: func(p, bp string) { delete(adopted, p+"\x00"+bp) },
			IsDejaCommand: func(cmd string, args []string) bool {
				return strings.Contains(cmd, "custom-deja") && len(args) > 0
			},
			FormatError: func(p string, err error) error { return errors.New("custom:" + p + ":" + err.Error()) },
		}
		spec := &harness.JSONMCPSpec{
			HarnessName:     "openclaw",
			Path:            path,
			BlockKey:        "mcp.servers",
			IgnoreSwitchOff: true,
			Entry:           map[string]any{"command": []string{"/opt/custom-deja", "mcp"}},
			Ops:             ops,
		}
		if _, err := spec.Wire(); err != nil {
			t.Fatal(err)
		}
		if beforeWriteCalled != 1 {
			t.Fatalf("BeforeWrite count = %d, want 1", beforeWriteCalled)
		}
		if _, err := spec.Wire(); err != nil {
			t.Fatal(err)
		}
		if beforeWriteCalled != 1 {
			t.Fatalf("BeforeWrite ran on unchanged second Wire")
		}
		probe, err := spec.Probe()
		if err != nil || !probe.Wired {
			t.Fatalf("Probe with slice command = (%+v, %v)", probe, err)
		}
		if _, err := spec.Unwire(); err != nil {
			t.Fatal(err)
		}
		after, _ := os.ReadFile(path)
		if strings.Contains(string(after), "\"mcp\"") || !strings.Contains(string(after), "\"theme\"") {
			t.Fatalf("Unwire did not collapse added nested wrapper:\n%s", after)
		}
	})

	t.Run("JSONC inplace update middle removal and error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "zed.jsonc")
		raw := "{\n  \"context_servers\": {\n    // first comment\n    \"alpha\": { \"command\": \"npx\" },\n    \"deja\": { \"command\": \"/old/deja\", \"args\": [\"mcp\"] },\n    \"zeta\": { \"command\": \"uvx\" }\n  }\n}\n"
		_ = os.WriteFile(path, []byte(raw), 0o600)
		spec := &harness.JSONMCPSpec{
			HarnessName: "zed",
			Path:        path,
			BlockKey:    "context_servers",
			Entry:       map[string]any{"command": "/new/deja", "args": []string{"mcp"}},
		}
		if _, err := spec.Wire(); err != nil {
			t.Fatal(err)
		}
		if _, err := spec.Unwire(); err != nil {
			t.Fatal(err)
		}
		after, _ := os.ReadFile(path)
		s := string(after)
		if strings.Contains(s, "deja") || !strings.Contains(s, "// first comment") || !strings.Contains(s, "\"zeta\"") {
			t.Fatalf("JSONC middle removal corrupted neighbors/comments:\n%s", s)
		}

		// Unclosed block comment triggers FormatError.
		badPath := filepath.Join(t.TempDir(), "bad.jsonc")
		_ = os.WriteFile(badPath, []byte("{ /* unclosed\n"), 0o600)
		badSpec := &harness.JSONMCPSpec{Path: badPath, Entry: map[string]any{"command": "deja"}}
		if _, err := badSpec.Probe(); err == nil {
			t.Fatal("unclosed JSONC block comment should fail Probe")
		}
	})

	t.Run("TOML legacy keys multiline array disabled=true and quoted header", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		raw := "[other]\nnote = \"\"\"[unclosed inside triple quote\"\"\"\n\n" +
			"[mcp_servers.\"deja-vu\"]\ncommand = \"/old/deja\"\nargs = [\n  \"mcp\",\n]\ndisabled = true\n"
		_ = os.WriteFile(path, []byte(raw), 0o600)
		spec := &harness.TOMLMCPSpec{
			HarnessName: "codex",
			Path:        path,
			BlockHeader: "[mcp_servers.deja]",
			LegacyKeys:  []string{"deja-legacy"},
			Block:       "[mcp_servers.deja]\ncommand = \"/new/deja\"\nargs = [\"mcp\"]\n",
		}
		w, err := spec.Wire()
		if err != nil {
			t.Fatal(err)
		}
		if w.AdoptedKey != "deja-vu" || !w.PreservedDisabled || !reflect.DeepEqual(w.DisabledFor, []string{"disabled"}) {
			t.Fatalf("TOML Wire adopted/disabled unexpected: %+v", w)
		}
		// Inject a legacy block then verify Unwire removes both adopted and legacy.
		cur, _ := os.ReadFile(path)
		_ = os.WriteFile(path, append(cur, []byte("\n[mcp_servers.deja-legacy]\ncommand = \"deja\"\n")...), 0o600)
		u, err := spec.Unwire()
		if err != nil {
			t.Fatal(err)
		}
		if len(u.RemovedKeys) != 2 {
			t.Fatalf("RemovedKeys = %v, want 2 keys", u.RemovedKeys)
		}
	})

	t.Run("FileAsset custom hooks and error propagation", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "a", "b", "SKILL.md")
		var notedDir, prunedDir string
		sentinel := errors.New("drop failed")
		spec := &harness.FileAssetSpec{
			HarnessName: "asset",
			Path:        p,
			Content:     []byte("hello\n"),
			CheckWired:  func(_ string, b []byte) bool { return bytes.Contains(b, []byte("ok")) },
			CheckEdited: func(_ string, b []byte) bool { return bytes.Contains(b, []byte("hand")) },
			Ops: &harness.Ops{
				NoteCreatedDirs: func(d string) { notedDir = d },
				PruneDir:        func(d string) { prunedDir = d },
			},
		}
		if _, err := spec.Wire(); err != nil {
			t.Fatal(err)
		}
		if notedDir == "" {
			t.Fatal("NoteCreatedDirs not called")
		}
		_ = os.WriteFile(p, []byte("hand ok\n"), 0o600)
		probe, err := spec.Probe()
		if err != nil || !probe.Wired {
			t.Fatalf("Probe with CheckWired = (%+v, %v)", probe, err)
		}
		w, err := spec.Wire()
		if err != nil || !w.EditedByUser {
			t.Fatalf("Wire with CheckEdited = (%+v, %v)", w, err)
		}
		if _, err := spec.Unwire(); err != nil {
			t.Fatal(err)
		}
		if prunedDir == "" {
			t.Fatal("PruneDir not called")
		}
		spec.DropRetired = func(_ string, _ bool) error { return sentinel }
		if _, err := spec.Wire(); !errors.Is(err, sentinel) {
			t.Fatalf("Wire did not propagate DropRetired error: %v", err)
		}
		if _, err := spec.Unwire(); !errors.Is(err, sentinel) {
			t.Fatalf("Unwire did not propagate DropRetired error: %v", err)
		}
	})
}
