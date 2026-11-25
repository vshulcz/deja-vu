package harness_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/harness"
)

func TestJSONOpsHooksAndRepurposedAdoptedKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.jsonc")
	initial := "// user header\n{\n  \"theme\": \"dark\",\n  \"count\": 42,\n  \"ok\": true,\n  \"empty\": null,\n  \"quote\": \"a\\\"b\",\n  \"mcp\": {\n    \"servers\": {\n      \"legacy-wrap\": {\"command\": \"sh\", \"args\": [\"-c\", \"deja mcp\"]},\n      \"deja-srv\": {\"command\": \"deja\", \"args\": [\"mcp\"], \"env\": {\"KEEP\": \"1\"}, \"timeout\": 30, \"disabled\": true, \"transport\": {\"command\": \"deja\", \"args\": [\"mcp\"]}}\n    }\n  }\n}\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	var editCalls []string
	var marshalCalled bool
	ops := &harness.Ops{
		IsDejaEntry: func(m map[string]any) bool {
			cmd, _ := m["command"].(string)
			return cmd == "sh" || cmd == "deja"
		},
		IsDejaServer: func(m map[string]any) bool {
			cmd, _ := m["command"].(string)
			return cmd == "deja"
		},
		EditJSONC: func(data []byte, blockPath, id string, entry map[string]any, uninstall bool, dropFrom int) ([]byte, error) {
			editCalls = append(editCalls, strings.Join([]string{blockPath, id, boolWord(uninstall)}, ":"))
			if uninstall {
				return bytes.ReplaceAll(data, []byte("\""+id+"\":"), []byte("\""+id+"\": null, \"_drop_"+id+"\":")), nil
			}
			return append(bytes.Clone(data), []byte("// edited\n")...), nil
		},
		MarshalJSON: func(old []byte, root map[string]any) ([]byte, error) {
			marshalCalled = true
			return append([]byte("{\n  \"custom\": true\n}\n"), nil...), nil
		},
	}

	spec := &harness.JSONMCPSpec{
		HarnessName:     "test-jsonc",
		Path:            path,
		BlockKey:        "mcp.servers",
		EntryKey:        "deja",
		IgnoreSwitchOff: true,
		Entry: map[string]any{
			"command": "deja",
			"args":    []string{"mcp"},
		},
		Ops: ops,
	}

	probe, err := spec.Probe()
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if !probe.Wired || !probe.Disabled || !slices.Equal(probe.DejaKeys, []string{"deja-srv", "legacy-wrap"}) {
		t.Fatalf("unexpected probe: %+v", probe)
	}

	// Custom EditJSONC path on JSONC Wire.
	wres, err := spec.Wire()
	if err != nil {
		t.Fatalf("Wire failed: %v", err)
	}
	if wres.AdoptedKey != "deja-srv" || wres.PreservedDisabled || wres.PrevEntry == nil {
		t.Fatalf("unexpected Wire result: %+v", wres)
	}
	if len(editCalls) != 1 || editCalls[0] != "mcp.servers:deja-srv:wire" {
		t.Fatalf("unexpected EditJSONC calls: %v", editCalls)
	}

	// Rewrite valid JSON with nested block to verify custom EditJSONC Unwire multi-key collapse.
	collapsePath := filepath.Join(dir, "collapse.jsonc")
	collapseBody := "// keep\n{\n  \"a\": 1\n}\n"
	if err := os.WriteFile(collapsePath, []byte(collapseBody), 0o600); err != nil {
		t.Fatal(err)
	}
	added := map[string]bool{}
	adopted := map[string][]string{}
	hookOps := &harness.Ops{
		RecordBlockAdded:  func(p, k string) { added[p+"|"+k] = true },
		WasBlockAdded:     func(p, k string) bool { return added[p+"|"+k] },
		ForgetBlockAdded:  func(p, k string) { delete(added, p+"|"+k) },
		RecordAdoptedKey:  func(p, b, k string) { adopted[p+"|"+b] = append(adopted[p+"|"+b], k) },
		AdoptedKeys:       func(p, b string) []string { return adopted[p+"|"+b] },
		ForgetAdoptedKeys: func(p, b string) { delete(adopted, p+"|"+b) },
		EditJSONC: func(data []byte, blockPath, id string, entry map[string]any, uninstall bool, dropFrom int) ([]byte, error) {
			if !uninstall {
				return []byte("// c\n{\"mcp\": {\"servers\": {\"deja\": {\"command\": \"deja\", \"args\": [\"mcp\"]}, \"old\": {\"command\": \"deja\", \"args\": [\"mcp\"]}}}}\n"), nil
			}
			if dropFrom == 0 {
				return []byte("// c\n{}\n"), nil
			}
			return data, nil
		},
	}
	cspec := &harness.JSONMCPSpec{
		HarnessName: "hook-collapse",
		Path:        collapsePath,
		BlockKey:    "mcp.servers",
		Entry:       map[string]any{"command": "deja", "args": []string{"mcp"}},
		Ops:         hookOps,
	}
	if _, err := cspec.Wire(); err != nil {
		t.Fatalf("hook Wire failed: %v", err)
	}
	hookOps.RecordAdoptedKey(collapsePath, "mcp.servers", "old")
	ures, err := cspec.Unwire()
	if err != nil {
		t.Fatalf("hook Unwire failed: %v", err)
	}
	if len(ures.RemovedKeys) != 2 || ures.Action != harness.ActionUpdated {
		t.Fatalf("expected 2 removed keys, got %+v", ures)
	}

	// Plain JSON merge + transport strip + MarshalJSON hook + repurposed adopted key kept.
	plainPath := filepath.Join(dir, "plain.json")
	plainBody := "{\n  \"mcpServers\": {\n    \"deja-vu\": {\n      \"command\": \"deja\",\n      \"args\": [\"mcp\"],\n      \"env\": {\"FOO\": \"bar\"},\n      \"timeout\": 15,\n      \"disabled\": true,\n      \"transport\": {\"command\": \"deja\"}\n    }\n  }\n}\n"
	if err := os.WriteFile(plainPath, []byte(plainBody), 0o600); err != nil {
		t.Fatal(err)
	}
	plainSpec := &harness.JSONMCPSpec{
		HarnessName: "plain",
		Path:        plainPath,
		BlockKey:    "mcpServers",
		Entry: map[string]any{
			"command": "deja",
			"args":    []string{"mcp"},
		},
		Ops: &harness.Ops{
			MarshalJSON: ops.MarshalJSON,
		},
	}
	pw, err := plainSpec.Wire()
	if err != nil {
		t.Fatalf("plain Wire failed: %v", err)
	}
	if !marshalCalled || !pw.PreservedDisabled || pw.AdoptedKey != "deja-vu" {
		t.Fatalf("unexpected plain Wire result: %+v marshalCalled=%v", pw, marshalCalled)
	}

	// Simulate user repurposing adopted key to a non-deja server before Unwire.
	repurposed := "{\n  \"mcpServers\": {\n    \"deja-vu\": {\n      \"command\": \"npx\",\n      \"args\": [\"other\"]\n    }\n  }\n}\n"
	if err := os.WriteFile(plainPath, []byte(repurposed), 0o600); err != nil {
		t.Fatal(err)
	}
	pu, err := plainSpec.Unwire()
	if err != nil {
		t.Fatalf("plain Unwire failed: %v", err)
	}
	if pu.Action != harness.ActionUnchanged {
		t.Fatalf("expected repurposed adopted key to remain untouched, got %+v", pu)
	}
}

func TestJSONEdgePathsPrimaryFirstAndErrors(t *testing.T) {
	dir := t.TempDir()

	// Primary key first ahead of sorted other deja keys, including []string/[]any/transport entries.
	probePath := filepath.Join(dir, "order.json")
	probeBody := `{
  "mcpServers": {
    "z-deja": {"transport": {"command": "deja", "args": ["mcp"]}},
    "a-deja": {"command": ["deja", "mcp"]},
    "deja": {"command": "deja", "args": ["mcp"]}
  }
}`
	if err := os.WriteFile(probePath, []byte(probeBody), 0o600); err != nil {
		t.Fatal(err)
	}
	pspec := &harness.JSONMCPSpec{Path: probePath, BlockKey: "mcpServers"}
	pres, err := pspec.Probe()
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	wantOrder := []string{"deja", "a-deja", "z-deja"}
	if !slices.Equal(pres.DejaKeys, wantOrder) {
		t.Fatalf("DejaKeys = %v, want %v", pres.DejaKeys, wantOrder)
	}

	// IgnoreSwitchOff clears inherited disabled flags on plain JSON.
	ignorePath := filepath.Join(dir, "ignore.json")
	if err := os.WriteFile(ignorePath, []byte(`{"mcpServers":{"deja":{"command":"deja","args":["mcp"],"disabled":true,"enabled":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ispec := &harness.JSONMCPSpec{
		Path:            ignorePath,
		BlockKey:        "mcpServers",
		IgnoreSwitchOff: true,
		Entry:           map[string]any{"command": "deja", "args": []string{"mcp"}},
	}
	ires, err := ispec.Wire()
	if err != nil {
		t.Fatalf("Wire IgnoreSwitchOff failed: %v", err)
	}
	if ires.PreservedDisabled {
		t.Fatalf("expected PreservedDisabled=false, got %+v", ires)
	}
	afterIgnore, err := os.ReadFile(ignorePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(afterIgnore), "disabled") || strings.Contains(string(afterIgnore), "enabled") {
		t.Fatalf("expected disabled/enabled stripped, got %s", afterIgnore)
	}

	// Escaped dot path amp\.mcpServers round-trip on JSONC + missing nested block creation.
	ampPath := filepath.Join(dir, "amp.jsonc")
	if err := os.WriteFile(ampPath, []byte("// amp settings\n{\n  \"other\": true\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ampSpec := &harness.JSONMCPSpec{
		Path:     ampPath,
		BlockKey: `amp\.mcpServers.nested`,
		Entry:    map[string]any{"command": "deja", "args": []string{"mcp"}},
	}
	if _, err := ampSpec.Wire(); err != nil {
		t.Fatalf("escaped dot JSONC Wire failed: %v", err)
	}
	aprobe, err := ampSpec.Probe()
	if err != nil || !aprobe.Wired {
		t.Fatalf("escaped dot Probe = %+v, err=%v", aprobe, err)
	}
	aunwire, err := ampSpec.Unwire()
	if err != nil || aunwire.Action != harness.ActionUpdated {
		t.Fatalf("escaped dot Unwire = %+v, err=%v", aunwire, err)
	}

	// JSONC null leaf is refused, while plain JSON null leaf is replaced.
	nullJSONC := filepath.Join(dir, "null.jsonc")
	if err := os.WriteFile(nullJSONC, []byte("// comment\n{\"mcpServers\": null}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&harness.JSONMCPSpec{Path: nullJSONC, Entry: map[string]any{"command": "deja"}}).Wire(); err == nil || !strings.Contains(err.Error(), "is not an object deja can edit") {
		t.Fatalf("expected nonObjectBlockError on JSONC null, got %v", err)
	}

	nullJSON := filepath.Join(dir, "null.json")
	if err := os.WriteFile(nullJSON, []byte("{\"mcpServers\": null}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&harness.JSONMCPSpec{Path: nullJSON, Entry: map[string]any{"command": "deja"}}).Wire(); err != nil {
		t.Fatalf("expected plain JSON null to be replaced, got %v", err)
	}

	// Non-object intermediate array error.
	badMid := filepath.Join(dir, "badmid.json")
	if err := os.WriteFile(badMid, []byte("{\"mcp\": []}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&harness.JSONMCPSpec{Path: badMid, BlockKey: "mcp.servers", Entry: map[string]any{"command": "deja"}}).Probe(); err == nil || !strings.Contains(err.Error(), `"mcp" is not an object deja can edit`) {
		t.Fatalf("expected intermediate error, got %v", err)
	}
}

func TestTOMLParityEdgeCasesAndErrors(t *testing.T) {
	dir := t.TempDir()

	// Primary-first Probe + single-quoted values + multiline string containing fake header.
	tomlPath := filepath.Join(dir, "codex.toml")
	tomlBody := `model_instructions = """
[mcp_servers.fake]
command = "deja"
"""

[mcp_servers.z-deja]
command = '/usr/local/bin/deja'
args = ['mcp']

[mcp_servers.deja]
command = "deja"
args = ["mcp"]
startup_timeout_sec = 45
# user comment
enabled = false
`
	if err := os.WriteFile(tomlPath, []byte(tomlBody), 0o600); err != nil {
		t.Fatal(err)
	}
	tspec := &harness.TOMLMCPSpec{
		Path:        tomlPath,
		BlockHeader: "[mcp_servers.deja]",
		OwnedKeys:   []string{"type", "command", "args"},
		Block: `[mcp_servers.deja]
command = "deja"
args = ["mcp"]`,
	}
	tprobe, err := tspec.Probe()
	if err != nil {
		t.Fatalf("TOML Probe failed: %v", err)
	}
	if !tprobe.Disabled || !slices.Equal(tprobe.DejaKeys, []string{"deja", "z-deja"}) {
		t.Fatalf("unexpected TOML Probe: %+v", tprobe)
	}

	// Wire preserves startup_timeout_sec, comment, and enabled=false.
	twire, err := tspec.Wire()
	if err != nil {
		t.Fatalf("TOML Wire failed: %v", err)
	}
	if !twire.PreservedDisabled || !slices.Equal(twire.LeftKeys, []string{"z-deja"}) {
		t.Fatalf("unexpected TOML Wire: %+v", twire)
	}
	updatedTOML, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updatedTOML), "startup_timeout_sec = 45") || !strings.Contains(string(updatedTOML), "# user comment") || !strings.Contains(string(updatedTOML), "enabled = false") {
		t.Fatalf("expected reader-owned TOML lines preserved, got:\n%s", updatedTOML)
	}

	// IgnoreSwitchOff removes enabled/disabled flags on update.
	tspec.IgnoreSwitchOff = true
	if _, err := tspec.Wire(); err != nil {
		t.Fatalf("TOML Wire IgnoreSwitchOff failed: %v", err)
	}
	noSwitch, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(noSwitch), "enabled = false") {
		t.Fatalf("expected enabled=false stripped with IgnoreSwitchOff, got:\n%s", noSwitch)
	}

	// Duplicate primary blocks deduplication + preserved disabled flag.
	dupPath := filepath.Join(dir, "dup.toml")
	dupBody := `[mcp_servers.deja]
command = "deja"
args = ["mcp"]
disabled = true

[mcp_servers.deja]
command = "deja"
args = ["mcp"]
`
	if err := os.WriteFile(dupPath, []byte(dupBody), 0o600); err != nil {
		t.Fatal(err)
	}
	dres, err := (&harness.TOMLMCPSpec{
		Path:        dupPath,
		BlockHeader: "[mcp_servers.deja]",
		Block: `[mcp_servers.deja]
command = "deja"
args = ["mcp"]`,
	}).Wire()
	if err != nil || !dres.PreservedDisabled {
		t.Fatalf("dup TOML Wire = %+v, err=%v", dres, err)
	}
	dupAfter, _ := os.ReadFile(dupPath)
	if strings.Count(string(dupAfter), "[mcp_servers.deja]") != 1 || !strings.Contains(string(dupAfter), "disabled = true") {
		t.Fatalf("expected single block with disabled preserved, got:\n%s", dupAfter)
	}

	// Foreign/URL block on adopted key gets full replacement with rewritten header & disabled preserved.
	urlPath := filepath.Join(dir, "url.toml")
	urlBody := `[mcp_servers."my.deja"]
url = "http://203.0.113.10:8080/mcp"
args = ["deja", "mcp"]
disabled = true
`
	if err := os.WriteFile(urlPath, []byte(urlBody), 0o600); err != nil {
		t.Fatal(err)
	}
	ures, err := (&harness.TOMLMCPSpec{
		Path:  urlPath,
		Block: "[mcp_servers.deja]\ncommand = \"deja\"\nargs = [\"mcp\"]",
	}).Wire()
	if err != nil || ures.AdoptedKey != "my.deja" || !ures.PreservedDisabled {
		t.Fatalf("url adopted Wire = %+v, err=%v", ures, err)
	}
	urlAfter, _ := os.ReadFile(urlPath)
	if strings.Contains(string(urlAfter), "203.0.113.10") || !strings.Contains(string(urlAfter), `[mcp_servers."my.deja"]`) || !strings.Contains(string(urlAfter), "disabled = true") {
		t.Fatalf("unexpected url adopted output:\n%s", urlAfter)
	}

	// Repurposed adopted TOML key + sub-table is preserved on Unwire.
	keepPath := filepath.Join(dir, "keep-adopted.toml")
	adoptedMemo := map[string][]string{keepPath + "|mcp_servers": {"deja-vu"}}
	keepBody := `[mcp_servers.deja-vu]
command = "npx"
args = ["other-mcp"]

[mcp_servers.deja-vu.env]
API_KEY = "secret"
`
	if err := os.WriteFile(keepPath, []byte(keepBody), 0o600); err != nil {
		t.Fatal(err)
	}
	kres, err := (&harness.TOMLMCPSpec{
		Path: keepPath,
		Ops:  &harness.Ops{AdoptedKeys: func(p, b string) []string { return adoptedMemo[p+"|"+b] }},
	}).Unwire()
	if err != nil || kres.Action != harness.ActionUnchanged {
		t.Fatalf("expected repurposed TOML adopted key kept, got %+v, err=%v", kres, err)
	}

	// Conflicts: [[mcp_servers]] array-of-tables and inline dotted key.
	arrPath := filepath.Join(dir, "arr.toml")
	if err := os.WriteFile(arrPath, []byte("[[mcp_servers]]\ncommand = \"other\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&harness.TOMLMCPSpec{Path: arrPath, Block: "[mcp_servers.deja]\ncommand=\"deja\"\n"}).Wire(); err == nil {
		t.Fatal("expected array-of-tables conflict error")
	}

	inlinePath := filepath.Join(dir, "inline.toml")
	if err := os.WriteFile(inlinePath, []byte("mcp_servers.deja = { command = \"x\" }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&harness.TOMLMCPSpec{Path: inlinePath, Block: "[mcp_servers.deja]\ncommand=\"deja\"\n"}).Wire(); err == nil {
		t.Fatal("expected inline dotted key conflict error")
	}

	// FileAssetSpec + Ops error propagation.
	readErr := errors.New("boom read")
	assetSpec := &harness.FileAssetSpec{
		HarnessName: "asset",
		Path:        filepath.Join(dir, "sub", "SKILL.md"),
		Content:     []byte("content\n"),
		Ops: &harness.Ops{
			ReadFile: func(string) ([]byte, error) { return nil, readErr },
			FormatError: func(p string, e error) error {
				return errors.New("wrapped:" + p + ":" + e.Error())
			},
		},
	}
	if _, err := assetSpec.Wire(); err == nil || !strings.Contains(err.Error(), "wrapped:") {
		t.Fatalf("expected wrapped read error, got %v", err)
	}
	if ok := reflect.DeepEqual(assetSpec.Name(), "asset"); !ok || assetSpec.TargetPath() == "" {
		t.Fatalf("unexpected FileAssetSpec metadata")
	}
}

func boolWord(b bool) string {
	if b {
		return "unwire"
	}
	return "wire"
}
