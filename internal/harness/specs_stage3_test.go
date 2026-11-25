package harness_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/harness"
)

func TestYAMLSectionSpecValidationAndFeatures(t *testing.T) {
	t.Run("duplicate inline and sequence refusals", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			content string
			seqMsg  string
			wantErr string
		}{
			{
				name:    "duplicate top-level section",
				content: "extensions:\n  a: {}\nextensions:\n  b: {}\n",
				wantErr: "written twice at the top level",
			},
			{
				name:    "inline flow value when CheckInlineValue",
				content: "extensions: {}\n",
				wantErr: "on one line",
			},
			{
				name:    "quoted section spelling",
				content: "\"extensions\": foo\n",
				wantErr: "block form",
			},
			{
				name:    "sequence value under section with default message",
				content: "extensions:\n  - a\n",
				wantErr: "holds a list",
			},
			{
				name:    "sequence value under section with custom message",
				content: "extensions:\n  - a\n",
				seqMsg:  "extensions: holds a list, custom",
				wantErr: "custom",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				p := filepath.Join(t.TempDir(), "config.yaml")
				if err := os.WriteFile(p, []byte(tc.content), 0o644); err != nil {
					t.Fatal(err)
				}
				spec := &harness.YAMLSectionSpec{
					HarnessName:      "goose",
					Path:             p,
					SectionKey:       "extensions",
					CheckInlineValue: true,
					SequenceMessage:  tc.seqMsg,
				}
				if _, err := spec.Wire(); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Wire() err = %v, want substring %q", err, tc.wantErr)
				}
			})
		}
	})

	t.Run("probe errors and disabled state", func(t *testing.T) {
		dir := t.TempDir()
		blocker := filepath.Join(dir, "file")
		if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		badSpec := &harness.YAMLSectionSpec{Path: filepath.Join(blocker, "config.yaml")}
		if _, err := badSpec.Probe(); err == nil {
			t.Fatal("Probe through regular file succeeded, want error")
		}

		p := filepath.Join(dir, "config.yaml")
		spec := &harness.YAMLSectionSpec{HarnessName: "goose", Path: p, SectionKey: "extensions:"}
		if err := os.WriteFile(p, []byte("extensions:\n  - item\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := spec.Probe(); err == nil {
			t.Fatal("Probe on sequence section succeeded, want error")
		}
		if err := os.WriteFile(p, []byte("   \n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if pr, err := spec.Probe(); err != nil || pr.Wired {
			t.Fatalf("Probe(blank) = (%+v, %v), want unwired", pr, err)
		}
		if err := os.WriteFile(p, []byte("extensions:\n  deja:\n    enabled: false\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		pr, err := spec.Probe()
		if err != nil || !pr.Wired || !pr.Disabled {
			t.Fatalf("Probe(disabled) = (%+v, %v), want wired & disabled", pr, err)
		}
		w, err := spec.Wire()
		if err != nil || !w.PreservedDisabled || w.Note == "" {
			t.Fatalf("Wire(disabled) = (%+v, %v), want PreservedDisabled+Note", w, err)
		}
	})

	t.Run("4-space indent doc-end PadAppendedSection and WasBlockAdded vs pre-existing empty section", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "config.yaml")
		// File with 4-space child entry and YAML ... marker.
		initial := "other: 1\nextensions:\n    custom:\n        enabled: true\n...\n"
		if err := os.WriteFile(p, []byte(initial), 0o644); err != nil {
			t.Fatal(err)
		}
		spec := &harness.YAMLSectionSpec{
			HarnessName:        "hermes",
			Path:               p,
			SectionKey:         "extensions:",
			RespectDocEnd:      true,
			PadAppendedSection: true,
		}
		w, err := spec.Wire()
		if err != nil || w.Action != harness.ActionUpdated {
			t.Fatalf("Wire = (%+v, %v)", w, err)
		}
		got, _ := os.ReadFile(p)
		if !strings.Contains(string(got), "    deja:\n      enabled: true") {
			t.Fatalf("expected 4-space indent for deja, got:\n%s", got)
		}

		// Second config: no extensions section, has ... document end -> added BEFORE ... and removed on Unwire.
		p2 := filepath.Join(t.TempDir(), "docend.yaml")
		if err := os.WriteFile(p2, []byte("theme: dark\n...\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		spec2 := &harness.YAMLSectionSpec{
			HarnessName:        "hermes",
			Path:               p2,
			SectionKey:         "mcp_servers:",
			RespectDocEnd:      true,
			PadAppendedSection: true,
		}
		if _, err := spec2.Wire(); err != nil {
			t.Fatal(err)
		}
		wiredDoc, _ := os.ReadFile(p2)
		if bytes.Index(wiredDoc, []byte("mcp_servers:")) > bytes.Index(wiredDoc, []byte("...")) {
			t.Fatalf("mcp_servers written after ...:\n%s", wiredDoc)
		}
		if _, err := spec2.Unwire(); err != nil {
			t.Fatal(err)
		}
		unwiredDoc, _ := os.ReadFile(p2)
		if strings.Contains(string(unwiredDoc), "mcp_servers:") {
			t.Fatalf("generated mcp_servers left behind:\n%s", unwiredDoc)
		}

		// Third config: pre-existing empty mcp_servers: must stay on Unwire when DropEmptySection=false.
		p3 := filepath.Join(t.TempDir(), "preexisting.yaml")
		if err := os.WriteFile(p3, []byte("mcp_servers:\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		spec3 := &harness.YAMLSectionSpec{
			HarnessName: "hermes",
			Path:        p3,
			SectionKey:  "mcp_servers:",
		}
		if _, err := spec3.Wire(); err != nil {
			t.Fatal(err)
		}
		if _, err := spec3.Unwire(); err != nil {
			t.Fatal(err)
		}
		after3, _ := os.ReadFile(p3)
		if string(after3) != "mcp_servers:\n" {
			t.Fatalf("pre-existing empty mcp_servers not preserved:\n%q", string(after3))
		}
	})

	t.Run("EditExisting CheckRemovable ContainsExactEntry OtherDejaNames FormatNote CRLF BOM", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "config.yaml")
		bomCRLF := append([]byte{0xEF, 0xBB, 0xBF}, []byte("other: 1\r\nextensions:\r\n  deja:\r\n    enabled: true\r\n")...)
		if err := os.WriteFile(p, bomCRLF, 0o644); err != nil {
			t.Fatal(err)
		}
		editedCalls := 0
		spec := &harness.YAMLSectionSpec{
			HarnessName:        "goose",
			Path:               p,
			SectionKey:         "extensions:",
			DropEmptySection:   true,
			ContainsExactEntry: true,
			BuildEntry: func(indent string, _ bool) string {
				return indent + "deja:\n" + indent + "  enabled: true\n"
			},
			EditExisting: func(doc string) (string, bool, bool, string, error) {
				editedCalls++
				if strings.Contains(doc, "trigger-edit") {
					return strings.ReplaceAll(doc, "trigger-edit", "edited"), true, false, "", nil
				}
				return "", false, false, "", nil
			},
			OtherDejaNames: func(_ string) []string { return []string{"legacy-deja"} },
			FormatNote: func(_ string, _ bool, others []string) string {
				return "others=" + strings.Join(others, ",")
			},
		}
		// ContainsExactEntry -> ActionUnchanged + FormatNote.
		w, err := spec.Wire()
		if err != nil || w.Action != harness.ActionUnchanged || w.Note != "others=legacy-deja" {
			t.Fatalf("Wire exact = (%+v, %v)", w, err)
		}
		if editedCalls != 1 {
			t.Fatalf("editedCalls = %d, want 1", editedCalls)
		}
		// Trigger EditExisting handled branch and keep BOM+CRLF.
		if werr := os.WriteFile(p, append([]byte{0xEF, 0xBB, 0xBF}, []byte("other: trigger-edit\r\n")...), 0o644); werr != nil {
			t.Fatal(werr)
		}
		w2, err := spec.Wire()
		if err != nil || w2.Action != harness.ActionUpdated || w2.Note != "others=legacy-deja" {
			t.Fatalf("Wire handled = (%+v, %v)", w2, err)
		}
		raw2, _ := os.ReadFile(p)
		if !bytes.HasPrefix(raw2, []byte{0xEF, 0xBB, 0xBF}) || !bytes.Contains(raw2, []byte("\r\n")) {
			t.Fatalf("lost BOM or CRLF on EditExisting write: %q", raw2)
		}

		// CheckRemovable blocks both Wire fallback and Unwire.
		spec.EditExisting = nil
		spec.CheckRemovable = func(_ string) error { return errors.New("cannot remove custom") }
		if _, werr := spec.Wire(); werr == nil || !strings.Contains(werr.Error(), "cannot remove custom") {
			t.Fatalf("Wire CheckRemovable err = %v", werr)
		}
		if _, uerr := spec.Unwire(); uerr == nil || !strings.Contains(uerr.Error(), "cannot remove custom") {
			t.Fatalf("Unwire CheckRemovable err = %v", uerr)
		}
	})
}

func TestJSONHooksSpecModesAndGuards(t *testing.T) {
	t.Run("event-map adoption retirement wrappers and JSONC guard", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "settings.json")
		spec := &harness.JSONHooksSpec{
			HarnessName: "qwen",
			Path:        p,
			Mode:        harness.JSONHooksEventMap,
			Retired:     map[string]string{"BeforeAgent": "", "OldCmd": "hook-old"},
			Rules: []harness.JSONHookRule{
				{
					Event:         "SessionStart",
					Matcher:       "startup|resume",
					Command:       "/usr/local/bin/deja hook-context",
					Timeout:       15,
					AdoptTimeout:  true,
					StatusMessage: "loading context",
				},
			},
			RefuseJSONC: true,
		}
		// Seed file with retired hook, stale timeout, duplicate direct entry, and wrapped entry.
		seed := `{
  "hooks": {
    "BeforeAgent": [{"hooks": [{"type": "command", "command": "/old/deja hook-context"}]}],
    "OldCmd": [
      {"hooks": [{"type": "command", "command": "/old/deja hook-old"}]},
      {"hooks": [{"type": "command", "command": "other-tool"}]}
    ],
    "SessionStart": [
      {"matcher": "old", "hooks": [{"type": "command", "command": "/old/deja hook-context", "timeout": 1}]},
      {"hooks": [{"type": "command", "command": "/dup/deja hook-context"}]},
      {"hooks": [{"type": "command", "command": "env X=1 deja hook-prompt"}]}
    ]
  }
}`
		if err := os.WriteFile(p, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		w, err := spec.Wire()
		if err != nil || w.Action != harness.ActionUpdated {
			t.Fatalf("Wire = (%+v, %v)", w, err)
		}
		if w.BeforeRoot == nil || w.AfterRoot == nil {
			t.Fatalf("expected BeforeRoot & AfterRoot populated: %+v", w)
		}
		data, _ := os.ReadFile(p)
		var root map[string]any
		if uerr := json.Unmarshal(data, &root); uerr != nil {
			t.Fatal(uerr)
		}
		hooks, ok := root["hooks"].(map[string]any)
		if !ok {
			t.Fatalf("hooks type = %T", root["hooks"])
		}
		if _, exists := hooks["BeforeAgent"]; exists {
			t.Fatalf("retired BeforeAgent left behind: %v", hooks)
		}
		oldCmd, ok := hooks["OldCmd"].([]any)
		if !ok {
			t.Fatalf("OldCmd type = %T", hooks["OldCmd"])
		}
		if len(oldCmd) != 1 {
			t.Fatalf("expected non-deja OldCmd survivor kept, got %v", oldCmd)
		}
		ss, ok := hooks["SessionStart"].([]any)
		if !ok {
			t.Fatalf("SessionStart type = %T", hooks["SessionStart"])
		}
		if len(ss) != 2 { // first adopted, second deduplicated, third non-matching prompt kept
			t.Fatalf("SessionStart len = %d, want 2: %v", len(ss), ss)
		}
		first, ok := ss[0].(map[string]any)
		if !ok {
			t.Fatalf("SessionStart[0] type = %T", ss[0])
		}
		if first["matcher"] != "startup|resume" {
			t.Fatalf("matcher = %v, want startup|resume", first["matcher"])
		}
		firstHooks, ok := first["hooks"].([]any)
		if !ok || len(firstHooks) == 0 {
			t.Fatalf("first hooks = %T", first["hooks"])
		}
		h0, ok := firstHooks[0].(map[string]any)
		if !ok {
			t.Fatalf("h0 type = %T", firstHooks[0])
		}
		if h0["timeout"] != float64(15) || h0["statusMessage"] != "loading context" {
			t.Fatalf("adopted hook = %v", h0)
		}

		// JSONC with no changes returns ActionUnchanged.
		jsoncUnchanged := "{\n  // user comment\n  \"theme\": \"dark\",\n  \"hooks\": {\n    \"SessionStart\": [\n      {\"matcher\": \"startup|resume\", \"hooks\": [{\"type\": \"command\", \"command\": \"/usr/local/bin/deja hook-context\", \"timeout\": 15, \"statusMessage\": \"loading context\"}]}\n    ]\n  }\n}\n"
		if werr := os.WriteFile(p, []byte(jsoncUnchanged), 0o644); werr != nil {
			t.Fatal(werr)
		}
		wc, err := spec.Wire()
		if err != nil || wc.Action != harness.ActionUnchanged {
			t.Fatalf("Wire(JSONC noop) = (%+v, %v), want unchanged", wc, err)
		}
		// JSONC where Unwire needs edit returns refusal without losing comments.
		if _, uerr := spec.Unwire(); uerr == nil || !strings.Contains(uerr.Error(), "carries comments") {
			t.Fatalf("Unwire(JSONC edit) err = %v, want comment refusal", uerr)
		}
		// JSONC where Wire needs edit returns refusal.
		if werr := os.WriteFile(p, []byte("{\n  // user comment\n  \"theme\": \"dark\"\n}\n"), 0o644); werr != nil {
			t.Fatal(werr)
		}
		if _, werr := spec.Wire(); werr == nil || !strings.Contains(werr.Error(), "carries comments") {
			t.Fatalf("Wire(JSONC edit) err = %v, want comment refusal", werr)
		}
		if uc, uerr := spec.Unwire(); uerr != nil || uc.Action != harness.ActionUnchanged {
			t.Fatalf("Unwire(JSONC noop) = (%+v, %v)", uc, uerr)
		}
	})

	t.Run("event-map MaxTimeout cap RewriteMatcher and wrapper preservation", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "codex-hooks.json")
		spec := &harness.JSONHooksSpec{
			HarnessName:    "codex",
			Path:           p,
			RewriteMatcher: true,
			Rules: []harness.JSONHookRule{{
				Event:      "SessionEnd",
				Matcher:    "",
				Command:    "/usr/local/bin/deja hook-session-end",
				Timeout:    3,
				MaxTimeout: 3,
			}},
		}
		seed := `{
  "hooks": {
    "SessionEnd": [
      {"matcher": "stale", "hooks": [{"type": "command", "command": "/old/deja hook-session-end", "timeout": 10}, {"type": "command", "command": "other"}]},
      {"hooks": [{"type": "command", "command": "env A=1 /old/deja hook-session-end"}]}
    ]
  }
}`
		if err := os.WriteFile(p, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := spec.Wire(); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(p)
		var root map[string]any
		_ = json.Unmarshal(raw, &root)
		hooksMap, ok := root["hooks"].(map[string]any)
		if !ok {
			t.Fatalf("hooks type = %T", root["hooks"])
		}
		list, ok := hooksMap["SessionEnd"].([]any)
		if !ok || len(list) == 0 {
			t.Fatalf("SessionEnd list = %T", hooksMap["SessionEnd"])
		}
		entry, ok := list[0].(map[string]any)
		if !ok {
			t.Fatalf("SessionEnd[0] type = %T", list[0])
		}
		if _, hasMatcher := entry["matcher"]; hasMatcher {
			t.Fatalf("expected matcher deleted when rule.Matcher empty: %v", entry)
		}
		entryHooks, ok := entry["hooks"].([]any)
		if !ok || len(entryHooks) == 0 {
			t.Fatalf("entry hooks = %T", entry["hooks"])
		}
		h0, ok := entryHooks[0].(map[string]any)
		if !ok {
			t.Fatalf("h0 type = %T", entryHooks[0])
		}
		if h0["timeout"] != float64(3) {
			t.Fatalf("timeout = %v, want capped 3", h0["timeout"])
		}
		// Wrapped second file: wrapper stays intact, lower timeout stays.
		if err := os.WriteFile(p, []byte(`{"hooks":{"SessionEnd":[{"hooks":[{"type":"command","command":"env A=1 /old/deja hook-session-end","timeout":1}]}]}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := spec.Wire(); err != nil {
			t.Fatal(err)
		}
		raw, _ = os.ReadFile(p)
		if !strings.Contains(string(raw), "env A=1 /old/deja hook-session-end") {
			t.Fatalf("wrapper command was overwritten: %s", raw)
		}
	})

	t.Run("flat-list foreign guard partial keep AfterRemove and PruneEmptyParents", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "hooks", "deja.json")
		foreign := `{"version":"v1","hooks":[{"name":"custom","trigger":"SessionStart","action":{"type":"command","command":"echo hi"}}]}`
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(foreign), 0o644); err != nil {
			t.Fatal(err)
		}
		afterRemoved := false
		spec := &harness.JSONHooksSpec{
			HarnessName:       "kiro-global",
			Path:              p,
			Mode:              harness.JSONHooksFlatList,
			OwnedMarker:       `"deja-digest"`,
			ForeignNote:       "not owned",
			RemoveWhenEmpty:   true,
			PruneEmptyParents: 1,
			AfterRemove: func(_ string) error {
				afterRemoved = true
				return nil
			},
			Rules: []harness.JSONHookRule{{
				Name:    "deja-digest",
				Event:   "SessionStart",
				Command: "/usr/local/bin/deja hook-context --plain",
			}},
		}
		pr, err := spec.Probe()
		if err != nil || !pr.Present || pr.Wired {
			t.Fatalf("Probe(foreign) = (%+v, %v)", pr, err)
		}
		wf, err := spec.Wire()
		if err != nil || wf.Action != harness.ActionUnchanged || wf.Note != "not owned" {
			t.Fatalf("Wire(foreign) = (%+v, %v)", wf, err)
		}
		uf, err := spec.Unwire()
		if err != nil || uf.Action != harness.ActionUnchanged {
			t.Fatalf("Unwire(foreign) = (%+v, %v)", uf, err)
		}

		// Owned file with extra user hook keeps user hook on Unwire.
		ownedPlusUser := `{"version":"v1","hooks":[{"name":"deja-digest","trigger":"SessionStart","action":{"type":"command","command":"/usr/local/bin/deja hook-context --plain"}},{"name":"mine","trigger":"Stop","action":{"type":"command","command":"echo bye"}}]}`
		if werr := os.WriteFile(p, []byte(ownedPlusUser), 0o644); werr != nil {
			t.Fatal(werr)
		}
		up, err := spec.Unwire()
		if err != nil || up.Action != harness.ActionUpdated {
			t.Fatalf("Unwire(partial) = (%+v, %v)", up, err)
		}
		if afterRemoved {
			t.Fatal("AfterRemove called when user hook remained")
		}
		// Wire adds deja-digest back alongside user hook, then remove user hook & verify full cleanup.
		if _, werr := spec.Wire(); werr != nil {
			t.Fatal(werr)
		}
		if werr := os.WriteFile(p, []byte(`{"version":"v1","hooks":[{"name":"deja-digest","trigger":"SessionStart","action":{"type":"command","command":"/usr/local/bin/deja hook-context --plain"}}]}`), 0o644); werr != nil {
			t.Fatal(werr)
		}
		ur, err := spec.Unwire()
		if err != nil || ur.Action != harness.ActionRemoved || !afterRemoved {
			t.Fatalf("Unwire(remove-all) = (%+v, %v), afterRemoved=%v", ur, err, afterRemoved)
		}
		if _, statErr := os.Stat(filepath.Dir(p)); !os.IsNotExist(statErr) {
			t.Fatalf("expected empty parent dir pruned, stat=%v", statErr)
		}
	})
}

func TestMarkedBlockSpecPairedAndLineModes(t *testing.T) {
	t.Run("paired-fence markers placeholder doc-end indent and validation", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "cordis.patch.yml")
		const start = "# deja mcp:start — generated by deja install, safe to delete"
		const end = "# deja mcp:end"
		const arrNote = "# (this block stands in for the empty array the layer held)"

		spec := &harness.MarkedBlockSpec{
			HarnessName:      "dsh",
			Path:             p,
			StartMarker:      start,
			EndMarker:        end,
			EmptyPlaceholder: "[]",
			PlaceholderNote:  arrNote,
			RespectDocEnd:    true,
			IndentFunc: func(doc string) string {
				if strings.Contains(doc, "  - id: user") {
					return "  "
				}
				return ""
			},
			BuildBlock: func(_ string) (string, string) {
				return start + "\n- id: mcp-deja\n" + end, "disabled note"
			},
			IsDisabled: func(patched string) bool {
				return strings.Contains(patched, "disabled: true")
			},
		}

		// Half-marked errors (#1701).
		for _, tc := range []struct {
			name    string
			content string
			want    string
		}{
			{name: "missing start", content: end + "\n", want: "has no start marker"},
			{name: "missing end", content: start + "\n- id: x\n", want: "has no end marker"},
			{name: "inverted", content: end + "\n" + start + "\n", want: "end marker above its start marker"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if err := os.WriteFile(p, []byte(tc.content), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := spec.Probe(); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("Probe err = %v, want %q", err, tc.want)
				}
				if _, err := spec.Wire(); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("Wire err = %v, want %q", err, tc.want)
				}
				if _, err := spec.Unwire(); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("Unwire err = %v, want %q", err, tc.want)
				}
			})
		}

		// Generated layer with [] and ... restores [] before ... on Unwire (#3271, #5181).
		if err := os.WriteFile(p, []byte("# layer\n[]\n...\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		w, err := spec.Wire()
		if err != nil || w.Action != harness.ActionUpdated || w.Note != "" { // IsDisabled false -> clears disabled note
			t.Fatalf("Wire([]) = (%+v, %v)", w, err)
		}
		wired, _ := os.ReadFile(p)
		if !bytes.Contains(wired, []byte(arrNote)) || bytes.Index(wired, []byte(start)) > bytes.Index(wired, []byte("...")) {
			t.Fatalf("unexpected wired [] doc:\n%s", wired)
		}
		u, err := spec.Unwire()
		if err != nil || u.Action != harness.ActionUpdated {
			t.Fatalf("Unwire([]) = (%+v, %v)", u, err)
		}
		restored, _ := os.ReadFile(p)
		if string(restored) != "# layer\n[]\n...\n" {
			t.Fatalf("restored = %q, want %q", string(restored), "# layer\n[]\n...\n")
		}

		// Relocates legacy block after ..., keeps indent and ValidatePatched blocks bad edits.
		if werr := os.WriteFile(p, []byte("  - id: user\n...\n"+start+"\n- id: old\n"+end+"\n"), 0o644); werr != nil {
			t.Fatal(werr)
		}
		if _, werr := spec.Wire(); werr != nil {
			t.Fatal(werr)
		}
		moved, _ := os.ReadFile(p)
		if !bytes.Contains(moved, []byte("  - id: mcp-deja")) || bytes.Index(moved, []byte(start)) > bytes.Index(moved, []byte("...")) {
			t.Fatalf("expected indented block relocated before ...:\n%s", moved)
		}

		spec.ValidatePatched = func(_ string, _ []byte, _ string) error {
			return errors.New("yaml write broke")
		}
		if _, err := spec.Wire(); err == nil || !strings.Contains(err.Error(), "yaml write broke") {
			t.Fatalf("ValidatePatched err = %v", err)
		}
	})

	t.Run("line-marker comment and table header boundaries and PreValidateWire", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "config.toml")
		spec := &harness.MarkedBlockSpec{
			HarnessName: "kimi",
			Path:        p,
			LineMarker:  "# deja: managed hook",
			TableHeader: "[[hooks]]",
			Block:       "# deja: managed hook\n[[hooks]]\nevent = \"SessionEnd\"\ncommand = \"/usr/local/bin/deja hook-session-end\"\n",
			PreValidateWire: func(doc string) error {
				if strings.Contains(doc, "hooks = []") {
					return errors.New("inline hooks key")
				}
				return nil
			},
			StripExtra: func(doc string) string {
				return strings.ReplaceAll(doc, "# legacy-unmarked\n", "")
			},
		}
		seed := "# legacy-unmarked\n# deja: managed hook\n[[hooks]]\nevent = \"Old\"\n# user comment before user hook (#1699)\n[[hooks]]\nevent = \"Stop\"\ncommand = \"echo hi\"\n"
		if err := os.WriteFile(p, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		w, err := spec.Wire()
		if err != nil || w.Action != harness.ActionUpdated {
			t.Fatalf("Wire = (%+v, %v)", w, err)
		}
		after, _ := os.ReadFile(p)
		s := string(after)
		if strings.Contains(s, "event = \"Old\"") || strings.Contains(s, "# legacy-unmarked") {
			t.Fatalf("old block or extra line survived:\n%s", s)
		}
		if !strings.Contains(s, "# user comment before user hook (#1699)\n[[hooks]]\nevent = \"Stop\"") {
			t.Fatalf("user hook/comment lost:\n%s", s)
		}
		u, err := spec.Unwire()
		if err != nil || u.Action != harness.ActionUpdated {
			t.Fatalf("Unwire = (%+v, %v)", u, err)
		}
		afterUnwire, _ := os.ReadFile(p)
		if !strings.Contains(string(afterUnwire), "event = \"Stop\"") || strings.Contains(string(afterUnwire), "# deja: managed hook") {
			t.Fatalf("unexpected unwire result:\n%s", afterUnwire)
		}
		if err := os.WriteFile(p, []byte("hooks = []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := spec.Wire(); err == nil || !strings.Contains(err.Error(), "inline hooks key") {
			t.Fatalf("PreValidateWire err = %v", err)
		}
	})
}

func TestStage3CoverageEdgeBranches(t *testing.T) {
	t.Run("YAMLSectionSpec hooks, quotes, comments, EOF, and IO errors", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "config.yaml")
		// Quoted keys, inline comments, non-section keys, deep nested fields under deja, EOF without LF.
		seed := strings.Join([]string{
			`other_section:`,
			`  "a:b": 'val # not comment' # real comment`,
			`  plain line without colon`,
			`extensions: # section comment`,
			`  # comment inside section`,
			`  custom:`,
			`    nested: true`,
			`  deja:`,
			`    args:`,
			`      - "--flag"`,
			`    enabled: false`,
		}, "\n")
		if err := os.WriteFile(p, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		customInspectCalled, customRemoveCalled := false, false
		spec := &harness.YAMLSectionSpec{
			HarnessName:        "hermes",
			Path:               p,
			SectionKey:         "", // default mcp_servers:
			DefaultIndent:      "    ",
			PadAppendedSection: true,
			RespectDocEnd:      false,
			Inspect: func(_ string) harness.YAMLProbeInfo {
				customInspectCalled = true
				return harness.YAMLProbeInfo{DejaKeys: []string{"deja"}, Disabled: true, DisabledFor: []string{"enabled"}}
			},
			RemoveEntries: func(doc string) string {
				customRemoveCalled = true
				return strings.ReplaceAll(doc, "  deja:\n    args:\n      - \"--flag\"\n    enabled: false", "")
			},
			EditExisting: func(doc string) (string, bool, bool, string, error) {
				if strings.Contains(doc, "fail-edit") {
					return "", false, false, "", errors.New("edit boom")
				}
				return "", false, false, "base-note", nil
			},
		}
		// Inspect empty-section EOF without trailing newline and default sectionKey/defaultIndent.
		if err := os.WriteFile(p, []byte("mcp_servers:"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := spec.Wire(); err != nil {
			t.Fatal(err)
		}
		if !customInspectCalled || !customRemoveCalled {
			t.Fatalf("custom hooks not called: inspect=%v remove=%v", customInspectCalled, customRemoveCalled)
		}

		// Test built-in inspect/remove with quoted YAML, deep fields, no trailing newline, and Unwire ActionUnchanged.
		p2 := filepath.Join(dir, "builtin.yaml")
		if err := os.WriteFile(p2, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		builtin := &harness.YAMLSectionSpec{
			HarnessName:        "goose",
			Path:               p2,
			SectionKey:         "extensions:",
			DropEmptySection:   true,
			PadAppendedSection: true,
		}
		pr, err := builtin.Probe()
		if err != nil || !pr.Wired || !pr.Disabled {
			t.Fatalf("Probe(builtin) = (%+v, %v)", pr, err)
		}
		// Unwire removes deja, then second Unwire is unchanged with LeftKeys=nil.
		if _, uerr := builtin.Unwire(); uerr != nil {
			t.Fatal(uerr)
		}
		u2, err := builtin.Unwire()
		if err != nil || u2.Action != harness.ActionUnchanged {
			t.Fatalf("Unwire second = (%+v, %v)", u2, err)
		}

		// Append new section with PadAppendedSection and no RespectDocEnd, non-empty file without trailing LF.
		p3 := filepath.Join(dir, "append.yaml")
		if err := os.WriteFile(p3, []byte("theme: light"), 0o644); err != nil {
			t.Fatal(err)
		}
		ap := &harness.YAMLSectionSpec{
			HarnessName:        "hermes",
			Path:               p3,
			SectionKey:         "extensions:",
			PadAppendedSection: true,
			EditExisting: func(doc string) (string, bool, bool, string, error) {
				if strings.Contains(doc, "fail-edit") {
					return "", false, false, "", errors.New("edit boom")
				}
				return strings.ReplaceAll(doc, "theme: light", "theme: dark"), true, false, "handled note", nil
			},
		}
		if w, err := ap.Wire(); err != nil || w.Note != "handled note" {
			t.Fatalf("Wire EditExisting baseNote = (%+v, %v)", w, err)
		}
		ap.EditExisting = func(_ string) (string, bool, bool, string, error) {
			return "", false, false, "", errors.New("edit boom")
		}
		if _, err := ap.Wire(); err == nil || !strings.Contains(err.Error(), "edit boom") {
			t.Fatalf("Wire EditExisting err = %v", err)
		}
		ap.EditExisting = nil
		if w, err := ap.Wire(); err != nil || w.Action != harness.ActionUpdated {
			t.Fatalf("Wire append = (%+v, %v)", w, err)
		}

		// Section at top level followed by sequence without indent vs indented sequence, and comment before child in DropEmptySection.
		p4 := filepath.Join(dir, "comments.yaml")
		if err := os.WriteFile(p4, []byte("extensions:\n  # kept comment\n  other: {}\n  deja:\n    enabled: true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cSpec := &harness.YAMLSectionSpec{HarnessName: "goose", Path: p4, SectionKey: "extensions:", DropEmptySection: true}
		if _, err := cSpec.Unwire(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p4, []byte("extensions:\nnext_top: 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if w, err := cSpec.Wire(); err != nil || w.Action != harness.ActionUpdated {
			t.Fatalf("Wire empty section above top key = (%+v, %v)", w, err)
		}

		// IO error paths via Ops.
		errOps := &harness.Ops{
			ReadFile:  func(string) ([]byte, error) { return nil, errors.New("read fail") },
			WriteFile: func(string, []byte, []byte) (string, error) { return "", errors.New("write fail") },
		}
		ioSpec := &harness.YAMLSectionSpec{HarnessName: "goose", Path: p4, SectionKey: "extensions:", Ops: errOps}
		if _, err := ioSpec.Probe(); err == nil {
			t.Fatal("Probe expected read fail")
		}
		if _, err := ioSpec.Wire(); err == nil {
			t.Fatal("Wire expected read fail")
		}
		if _, err := ioSpec.Unwire(); err == nil {
			t.Fatal("Unwire expected read fail")
		}
		errOps.ReadFile = func(string) ([]byte, error) { return []byte("a: 1\n"), nil }
		if _, err := ioSpec.Wire(); err == nil {
			t.Fatal("Wire expected write fail")
		}
		ioSpec.RemoveEntries = func(string) string { return "changed: 1\n" }
		if _, err := ioSpec.Unwire(); err == nil {
			t.Fatal("Unwire expected write fail")
		}
	})

	t.Run("JSONHooksSpec custom callbacks, quotes, malformed inputs, and IO errors", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "hooks.json")

		// Custom callbacks, Version, quoted shell words, non-command hook entries, IsAnyDejaHook, MarshalJSON.
		customSpec := &harness.JSONHooksSpec{
			HarnessName:    "kiro-global",
			Path:           p,
			Mode:           harness.JSONHooksFlatList,
			Version:        "v2",
			OwnedMarker:    `"deja"`,
			ForeignNote:    "foreign",
			IsCommandMatch: func(actual, _ string) (bool, bool) { return strings.Contains(actual, "deja"), true },
			IsAnyDejaHook: func(entry map[string]any) bool {
				hs, ok := entry["hooks"].([]any)
				if !ok || len(hs) == 0 {
					return false
				}
				first, ok := hs[0].(map[string]any)
				if !ok {
					return false
				}
				cmd, _ := first["command"].(string)
				return strings.Contains(cmd, "legacy-deja")
			},
			Rules: []harness.JSONHookRule{{
				Name:    "",
				Event:   "SessionStart",
				Command: `'/path/with space/deja' hook-context --flag="v\"1" arg\ 2`,
			}},
			Ops: &harness.Ops{
				MarshalJSON: func(_ []byte, v map[string]any) ([]byte, error) {
					b, err := json.Marshal(v)
					return append(b, '\n'), err
				},
			},
		}
		// Seed with non-map array entries and event/legacy match.
		if err := os.WriteFile(p, []byte(`{"version":"v2","hooks":[null,{"name":"legacy","trigger":"Stop","action":{"command":"legacy-deja"}}],"note":"deja"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		pr, err := customSpec.Probe()
		if err != nil || !pr.Wired {
			t.Fatalf("Probe(flat custom) = (%+v, %v)", pr, err)
		}
		if _, werr := customSpec.Wire(); werr != nil {
			t.Fatal(werr)
		}
		// Unwire when non-deja survives with OwnedMarker triggers no-op (len(removed)==0).
		if werr := os.WriteFile(p, []byte(`{"version":"v2","hooks":[null,{"name":"other","trigger":"Stop","action":{"command":"echo"}}],"note":"deja"}`), 0o644); werr != nil {
			t.Fatal(werr)
		}
		ur, err := customSpec.Unwire()
		if err != nil || ur.Action != harness.ActionUnchanged {
			t.Fatalf("Unwire(flat nothing removed) = (%+v, %v)", ur, err)
		}
		// Event-map with default shell word splitting (single/double/backslash quotes) and non-command hook entries.
		pMap := filepath.Join(dir, "map.json")
		mapSpec := &harness.JSONHooksSpec{
			HarnessName: "claude",
			Path:        pMap,
			RefuseJSONC: true,
			Rules: []harness.JSONHookRule{{
				Event:   "SessionStart",
				Command: `'/usr/local/bin/deja' "hook-context" arg\ 1`,
				Timeout: 10,
			}},
		}
		if werr := os.WriteFile(pMap, []byte(`{"hooks":{"SessionStart":[null,{"hooks":[null,{"type":"http"},{"type":"command","command":"'/usr/local/bin/deja' \"hook-context\" arg\\ 1"}]}]}}`), 0o644); werr != nil {
			t.Fatal(werr)
		}
		mp, err := mapSpec.Probe()
		if err != nil || !mp.Wired {
			t.Fatalf("Probe(quoted event map) = (%+v, %v)", mp, err)
		}
		// Null JSON, malformed JSON, malformed JSONC, read/write/remove/AfterRemove errors.
		for _, bad := range []string{"{bad json", "null\n"} {
			if err := os.WriteFile(pMap, []byte(bad), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _ = mapSpec.Probe()
			_, _ = mapSpec.Wire()
			_, _ = mapSpec.Unwire()
		}
		if err := os.WriteFile(pMap, []byte("// comment\n{bad"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := mapSpec.Wire(); err == nil {
			t.Fatal("Wire expected bad JSONC error")
		}
		if err := os.WriteFile(p, []byte(`{"version":"v1","hooks":[{"name":"x","trigger":"SessionStart","action":{"command":"deja hook-context"}}],"note":"deja"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		customSpec.RemoveWhenEmpty = true
		customSpec.AfterRemove = func(string) error { return errors.New("after remove fail") }
		if _, err := customSpec.Unwire(); err == nil || !strings.Contains(err.Error(), "after remove fail") {
			t.Fatalf("Unwire AfterRemove err = %v", err)
		}
		// Ops read/write/remove errors.
		errOps := &harness.Ops{
			ReadFile:   func(string) ([]byte, error) { return []byte(`{"hooks":{}}`), nil },
			WriteFile:  func(string, []byte, []byte) (string, error) { return "", errors.New("werr") },
			RemoveFile: func(string) error { return errors.New("rerr") },
		}
		for _, m := range []string{harness.JSONHooksEventMap, harness.JSONHooksFlatList} {
			es := &harness.JSONHooksSpec{
				HarnessName:     "e",
				Path:            p,
				Mode:            m,
				RemoveWhenEmpty: true,
				Ops:             errOps,
				Rules:           []harness.JSONHookRule{{Name: "d", Event: "SessionStart", Command: "deja hook-context"}},
			}
			_, _ = es.Wire()
			errOps.ReadFile = func(string) ([]byte, error) {
				return []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"deja hook-context"}]}]}}`), nil
			}
			if m == harness.JSONHooksFlatList {
				errOps.ReadFile = func(string) ([]byte, error) {
					return []byte(`{"hooks":[{"name":"d","trigger":"SessionStart","action":{"command":"deja hook-context"}}]}`), nil
				}
			}
			_, _ = es.Unwire()
			errOps.ReadFile = func(string) ([]byte, error) { return []byte(`{"hooks":{}}`), nil }
		}
		_ = os.WriteFile(p, []byte(`{}`), 0o644)
		errOps.ReadFile = func(string) ([]byte, error) { return nil, errors.New("read fail") }
		customSpec.Ops = errOps
		customSpec.Path = p
		if _, err := customSpec.Wire(); err == nil {
			t.Fatal("Wire expected read fail")
		}
		if _, err := customSpec.Unwire(); err == nil {
			t.Fatal("Unwire expected read fail")
		}
		if _, err := customSpec.Probe(); err == nil {
			t.Fatal("Probe expected read fail")
		}
	})

	t.Run("MarkedBlockSpec custom callbacks, placeholders without DocEnd, and IO errors", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "patch.yml")
		const start = "# deja:start"
		const end = "# deja:end"
		const note = "# placeholder note"
		spec := &harness.MarkedBlockSpec{
			HarnessName:      "dsh",
			Path:             p,
			StartMarker:      start,
			EndMarker:        end,
			MarkerSubject:    "custom block",
			EmptyPlaceholder: "[]",
			PlaceholderNote:  note,
			RespectDocEnd:    true,
			DocEndLine:       func(line string) bool { return strings.TrimSpace(line) == "END" },
			HasEntry:         func(doc string) bool { return strings.Contains(doc, "keep-me") },
			IsWired:          func(doc string) bool { return strings.Contains(doc, start) },
			IsDisabled:       func(_ string) bool { return true },
			BuildBlock:       func(string) (string, string) { return start + "\n- id: d\n" + end, "" },
		}
		// Doc starts with YAML headers and [] without END line -> restores [] at tail on Unwire when RespectDocEnd has no match.
		if err := os.WriteFile(p, []byte("---\n[]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := spec.Wire(); err != nil {
			t.Fatal(err)
		}
		pr, err := spec.Probe()
		if err != nil || !pr.Wired || !pr.Disabled {
			t.Fatalf("Probe(IsDisabled) = (%+v, %v)", pr, err)
		}
		if _, err := spec.Unwire(); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(p)
		if string(got) != "---\n[]\n" {
			t.Fatalf("expected [] restored after ---, got %q", string(got))
		}
		// Append block without RespectDocEnd to non-empty file, and Unwire keeping non-empty user content.
		p2 := filepath.Join(dir, "plain.yml")
		if err := os.WriteFile(p2, []byte("keep-me: 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		plainSpec := &harness.MarkedBlockSpec{
			HarnessName:      "dsh",
			Path:             p2,
			StartMarker:      start,
			EndMarker:        end,
			EmptyPlaceholder: "[]",
			Block:            start + "\n- id: d\n" + end,
		}
		if _, err := plainSpec.Wire(); err != nil {
			t.Fatal(err)
		}
		// Add custom entry + block so Unwire skips placeholder restoration.
		if _, err := plainSpec.Unwire(); err != nil {
			t.Fatal(err)
		}

		// Line-marker mode with custom StopPrefixes & IsWired and IO errors.
		p3 := filepath.Join(dir, "hooks.toml")
		if err := os.WriteFile(p3, []byte("# m\n[[hooks]]\nx = 1\n[other]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		lineSpec := &harness.MarkedBlockSpec{
			HarnessName:  "kimi",
			Path:         p3,
			LineMarker:   "# m",
			StopPrefixes: []string{"[other]"},
			IsWired:      func(doc string) bool { return strings.Contains(doc, "# m") },
			Block:        "# m\n[[hooks]]\ny = 2\n",
		}
		if _, err := lineSpec.Probe(); err != nil {
			t.Fatal(err)
		}
		if err := lineSpec.CheckMarkers("ignored"); err != nil {
			t.Fatal(err)
		}
		errOps := &harness.Ops{
			ReadFile:  func(string) ([]byte, error) { return nil, errors.New("read fail") },
			WriteFile: func(string, []byte, []byte) (string, error) { return "", errors.New("write fail") },
		}
		lineSpec.Ops = errOps
		if _, err := lineSpec.Probe(); err == nil {
			t.Fatal("Probe expected read fail")
		}
		if _, err := lineSpec.Wire(); err == nil {
			t.Fatal("Wire expected read fail")
		}
		if _, err := lineSpec.Unwire(); err == nil {
			t.Fatal("Unwire expected read fail")
		}
		errOps.ReadFile = func(string) ([]byte, error) { return []byte("# m\n[[hooks]]\n[other]\n"), nil }
		if _, err := lineSpec.Wire(); err == nil {
			t.Fatal("Wire expected write fail")
		}
		if _, err := lineSpec.Unwire(); err == nil {
			t.Fatal("Unwire expected write fail")
		}
	})
	t.Run("JSONHooksFlatListZeroByteForeignFile", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "deja.json")
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		spec := &harness.JSONHooksSpec{
			HarnessName:       "json-hooks-flat-zero",
			Path:              p,
			Mode:              harness.JSONHooksFlatList,
			OwnedMarker:       `"deja-digest"`,
			ForeignNote:       "foreign hook file",
			RemoveWhenEmpty:   true,
			PruneEmptyParents: 1,
			Rules: []harness.JSONHookRule{{
				Name:    "deja-digest",
				Event:   "SessionStart",
				Command: "/usr/local/bin/deja hook-context --plain",
			}},
		}
		wr, err := spec.Wire()
		if err != nil {
			t.Fatalf("Wire error: %v", err)
		}
		if wr.Action != harness.ActionUnchanged || wr.Note != "foreign hook file" {
			t.Fatalf("got WireResult %+v, want unchanged with foreign note", wr)
		}
		ur, err := spec.Unwire()
		if err != nil {
			t.Fatalf("Unwire error: %v", err)
		}
		if ur.Action != harness.ActionUnchanged || ur.Note != "" {
			t.Fatalf("got UnwireResult %+v, want unchanged with empty note", ur)
		}
		info, err := os.Stat(p)
		if err != nil || info.Size() != 0 {
			t.Fatalf("zero-byte foreign file modified or removed: info=%v err=%v", info, err)
		}
	})
}
