package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/peers"
	"github.com/vshulcz/deja-vu/internal/policy"
	search "github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/usage"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
)

// openclaw-auto writes into the same config the MCP entry does, and it still
// went through the parsed path only — so a `//` line refused the target, on the
// way in and on the way out, which is how a reader ends up with the hook entry
// stuck in the file (#2811).
func TestOpenClawAutoTakesACommentedConfig(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // the ordering here is deliberate\n  \"theme\": \"dark\"\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
		t.Fatalf("a commented config was refused: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "// the ordering here is deliberate") {
		t.Errorf("the comment was dropped:\n%s", b)
	}
	var root map[string]any
	if err = json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
		t.Fatalf("the file no longer parses: %v\n%s", err, b)
	}
	hooks, _ := root["hooks"].(map[string]any)
	internal, _ := mapAt(hooks, "internal")
	entries, _ := mapAt(internal, "entries")
	if entries[openclawHookName] == nil {
		t.Fatalf("the hook entry was not written:\n%s", b)
	}
	// Without the switch beside it the pack is discovered and never invoked,
	// which is the state the parsed path is careful to avoid.
	if on, _ := internal["enabled"].(bool); !on {
		t.Errorf("the entry went in without the switch that runs it:\n%s", b)
	}

	if _, err = installOpenClawAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall refused the commented config: %v", err)
	}
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), openclawHookName) {
		t.Errorf("the hook entry stayed behind:\n%s", b)
	}
	if string(b) != before {
		t.Errorf("the round trip changed the file:\nwant %q\ngot  %q", before, b)
	}
}

// The reader already has the block, their own switch and a hook of their own.
// Deja's entry comes out and nothing else moves: the switch is theirs, and
// deleting it left their hook wired and turned off (#2811).
func TestOpenClawAutoLeavesTheReadersWiringAlone(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // mine, and I mean it\n  \"hooks\": {\n    \"internal\": {\n      \"entries\": {\n        \"mine\": { \"enabled\": true }\n      },\n      \"enabled\": true\n    }\n  }\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
		t.Fatalf("a commented config was refused: %v", err)
	}
	if _, err := installOpenClawAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall refused the commented config: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != before {
		t.Errorf("the round trip changed the reader's file:\nwant %q\ngot  %q", before, b)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
		t.Fatalf("the file no longer parses: %v\n%s", err, b)
	}
}

// The switch written last in its block, with a key in front of it: taking it
// out has to take the comma in front too, or what is left is `{…,}` and every
// later run refuses the file — including across a comment between the two,
// which a backward walk stops at.
func TestOpenClawAutoRemovesASwitchWrittenLast(t *testing.T) {
	for _, before := range []string{
		"{\n  // reader's own block\n  \"hooks\": {\n    \"internal\": {}\n  }\n}\n",
		"{\n  // reader's own block\n  \"hooks\": {\n    \"internal\": {\n      \"timeoutMs\": 500\n    }\n  }\n}\n",
		"{\n  // reader's own block\n  \"hooks\": {\n    \"internal\": {\n      \"timeoutMs\": 500,\n      // the internal hook system\n      \"other\": true\n    }\n  }\n}\n",
		"{\n  \"hooks\": {\n    \"internal\": {\n      \"timeoutMs\": 500, /* keep */ \"other\": true\n    }\n  }\n}\n",
	} {
		t.Run("", func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
				t.Fatalf("a commented config was refused: %v", err)
			}
			if _, err := installOpenClawAuto("/bin/deja", true); err != nil {
				t.Fatalf("uninstall refused the commented config: %v", err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
				t.Fatalf("the file no longer parses: %v\n%s", err, b)
			}
			if strings.Contains(string(b), `"enabled"`) {
				t.Errorf("the switch deja turned on stayed behind:\n%s", b)
			}
			if string(b) != before {
				t.Errorf("the round trip changed the file:\nwant %q\ngot  %q", before, b)
			}
		})
	}
}

// A switch the reader set themselves is theirs: deja overwrites it while it is
// installed and leaves it behind on the way out, rather than deleting a setting
// it never wrote.
func TestOpenClawAutoKeepsASwitchTheReaderSet(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // mine\n  \"hooks\": {\n    \"internal\": {\n      \"enabled\": false\n    }\n  }\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
		t.Fatalf("a commented config was refused: %v", err)
	}
	if _, err := installOpenClawAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall refused the commented config: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"enabled"`) {
		t.Errorf("the reader's own switch was deleted:\n%s", b)
	}
	// And it is theirs, value and all: an install that switched internal hooks
	// on for someone who had switched them off is not something an uninstall
	// should leave behind.
	if !strings.Contains(string(b), "\"enabled\": false") {
		t.Errorf("the reader had it off and it came back on:\n%s", b)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
		t.Fatalf("the file no longer parses: %v\n%s", err, b)
	}
}

// A string value equal to a block's name is not that block: the writer read it
// as one and put a second block in beside the reader's, where the decoder takes
// the reader's and deja is silently unwired.
func TestOpenClawAutoIsNotFooledByAValueNamedLikeAKey(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // a value can be spelled like a key\n  \"hooks\": {\n    \"note\": \"internal\",\n    \"internal\": {\n      \"entries\": {\n        \"mine\": { \"enabled\": true }\n      }\n    }\n  }\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
		t.Fatalf("install refused the config: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), `"internal"`); n != 2 {
		// The key itself and the reader's string value: one of each, and no
		// second block.
		t.Errorf("%d occurrences of the key, so a second block went in beside it:\n%s", n, b)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
		t.Fatalf("the file no longer parses: %v\n%s", err, b)
	}
	hooks, _ := root["hooks"].(map[string]any)
	internal, _ := mapAt(hooks, "internal")
	entries, _ := mapAt(internal, "entries")
	if entries[openclawHookName] == nil {
		t.Errorf("the entry did not reach the reader's own block:\n%s", b)
	}
}

// A level holding something that is not an object is refused rather than
// written beside: a second key of the same name loses the decode to the
// reader's value, and deja reports an install that did nothing.
func TestOpenClawAutoRefusesALevelItCannotEdit(t *testing.T) {
	for _, before := range []string{
		"{\n  // not an object\n  \"hooks\": { \"internal\": \"off\" }\n}\n",
		"{\n  // not an object\n  \"hooks\": { \"internal\": { \"entries\": [] } }\n}\n",
	} {
		t.Run("", func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installOpenClawAuto("/bin/deja", false); err == nil {
				t.Error("a level deja cannot edit was written into anyway")
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// The MCP and plugin entries are written before the hook one, so
			// the file does change — what must not is the block deja refused.
			if strings.Count(string(b), `"internal"`) != 1 {
				t.Errorf("a second key was written beside the one deja refused:\n%s", b)
			}
			if !strings.Contains(string(b), "// not an object") {
				t.Errorf("the comment was dropped:\n%s", b)
			}
			var root map[string]any
			if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
				t.Fatalf("the file no longer parses: %v\n%s", err, b)
			}
		})
	}
}

// The population this writer is for: someone who hand-edits an annotated
// config. They move deja's switch to the end of the block and put a note above
// it, and the uninstall has to take the comma in front of it with it — the one
// after it is not there to take (#2811).
func TestOpenClawAutoRemovesASwitchTheReaderMoved(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\n  // mine\n  \"hooks\": {\n    \"internal\": {}\n  }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
		t.Fatalf("a commented config was refused: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"enabled"`) {
		t.Fatalf("the switch was not written, so there is nothing to take out:\n%s", b)
	}
	// The reader tidies: the switch goes last, with a line about it. Written
	// out rather than edited, so the fixture is exactly the shape being tested.
	var entry string
	if i := bytes.Index(b, []byte(`"deja-recall"`)); i >= 0 {
		if j := strings.Index(string(b)[i:], "}"); j >= 0 {
			entry = string(b)[i : i+j+1]
		}
	}
	if entry == "" {
		t.Fatalf("could not find the entry deja wrote:\n%s", b)
	}
	// A key of the reader's own between the entries block and the switch, or
	// the entries block takes its own comma with it when it goes and the walk
	// has nothing to find.
	moved := "{\n  // mine\n  \"hooks\": {\n    \"internal\": {\n      \"entries\": {\n        " +
		entry + "\n      },\n      \"timeoutMs\": 500,\n      // deja turned this on\n      \"enabled\": true\n    }\n  }\n}\n"
	if err = os.WriteFile(path, []byte(moved), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err = installOpenClawAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall refused the config: %v", err)
	}
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
		t.Fatalf("the file no longer parses: %v\n%s", err, b)
	}
	if strings.Contains(string(b), `"enabled"`) {
		t.Errorf("the switch deja turned on stayed behind:\n%s", b)
	}
	if !strings.Contains(string(b), "// mine") {
		t.Errorf("the reader's own comment was dropped:\n%s", b)
	}
	if !strings.Contains(string(b), `"timeoutMs": 500`) {
		t.Errorf("the reader's own key went with it:\n%s", b)
	}
}

// fakeOpenClawOnPath puts an `openclaw` of the given version first on PATH,
// laid out the way npm installs it; "" leaves none at all.
func fakeOpenClawOnPath(t *testing.T, version string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("PATH", root)
	if version == "" {
		return
	}
	pkg := filepath.Join(root, "node_modules", "openclaw")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"name":"openclaw","version":"`+version+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// npm's shim sits beside node_modules.
		if err := os.WriteFile(filepath.Join(root, "openclaw.cmd"), []byte("@echo off\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return
	}
	entry := filepath.Join(pkg, "openclaw.mjs")
	if err := os.WriteFile(entry, []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(entry, filepath.Join(bin, "openclaw")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func openclawDejaEntry(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(openclawConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(jsoncToJSON(string(b))), &root); err != nil {
		t.Fatalf("%v:\n%s", err, b)
	}
	e, _ := jsonAt(root, "plugins", "entries", openclawPluginID).(map[string]any)
	return e
}

func seedOpenClawConfig(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(sources.OpenClawStateDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(openclawConfigPath(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// OpenClaw 2026.8.1 drops a non-bundled plugin's agent_turn_prepare and
// before_prompt_build without the grant: the digest and per-prompt recall
// never ran on a current OpenClaw.
func TestOpenClawInstallGrantsConversationAccess(t *testing.T) {
	for _, version := range []string{"2026.9.8", "2026.4.24", ""} {
		t.Run("openclaw "+version, func(t *testing.T) {
			hermeticEnv(t)
			fakeOpenClawOnPath(t, version)
			seedOpenClawConfig(t, `{"plugins":{"entries":{"theirs":{"enabled":true,"hooks":{"allowConversationAccess":false}}}}}`+"\n")
			if _, err := installOpenClawPlugin("/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			e := openclawDejaEntry(t)
			if jsonAt(e, "hooks", openclawAccessKey) != true || e["enabled"] != true {
				t.Fatalf("deja's entry = %v, want enabled with the grant", e)
			}
			var root map[string]any
			b, _ := os.ReadFile(openclawConfigPath())
			_ = json.Unmarshal(b, &root)
			if jsonAt(root, "plugins", "entries", "theirs", "hooks", openclawAccessKey) != false {
				t.Errorf("another plugin's entry changed:\n%s", b)
			}
			if _, err := installOpenClawPlugin("/bin/deja", true); err != nil {
				t.Fatal(err)
			}
			if e := openclawDejaEntry(t); e != nil {
				t.Errorf("uninstall left %v", e)
			}
		})
	}
}

// 2026.4.23 and older refuse to start on the key ("Unrecognized key"), so it
// is not written there, and one left from a newer OpenClaw comes out.
func TestOpenClawInstallLeavesOldOpenClawWithoutTheKey(t *testing.T) {
	hermeticEnv(t)
	fakeOpenClawOnPath(t, "2026.4.23")
	seedOpenClawConfig(t, `{"plugins":{"entries":{"deja":{"enabled":true,"hooks":{"allowConversationAccess":true,"timeoutMs":9000}}}}}`+"\n")
	if _, err := installOpenClawPlugin("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	e := openclawDejaEntry(t)
	if _, ok := e["hooks"].(map[string]any)[openclawAccessKey]; ok {
		t.Errorf("wrote the key an OpenClaw 2026.4.23 rejects: %v", e)
	}
	if jsonAt(e, "hooks", "timeoutMs") != float64(9000) {
		t.Errorf("the reader's own hook setting went: %v", e)
	}
}

// The version comes from the config when no openclaw is on PATH.
func TestOpenClawVersionFallsBackToTheConfig(t *testing.T) {
	hermeticEnv(t)
	fakeOpenClawOnPath(t, "")
	seedOpenClawConfig(t, `{"meta":{"lastTouchedVersion":"2026.3.1"}}`+"\n")
	if openclawTakesConversationAccess() {
		t.Fatal("a config last written by 2026.3.1 counted as taking the key")
	}
	if _, err := installOpenClawPlugin("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if e := openclawDejaEntry(t); e["hooks"] != nil {
		t.Errorf("wrote hooks for an old OpenClaw: %v", e)
	}
}

// The reader's say stays: enabled: false, an explicit false on the grant,
// and their own keys on deja's entry.
func TestOpenClawInstallKeepsTheReadersEntrySettings(t *testing.T) {
	hermeticEnv(t)
	fakeOpenClawOnPath(t, "2026.9.8")
	seedOpenClawConfig(t, `{"plugins":{"entries":{"deja":{"enabled":false,"hooks":{"timeoutMs":9000}}}}}`+"\n")
	res, err := installOpenClawPlugin("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	e := openclawDejaEntry(t)
	if e["enabled"] != false {
		t.Errorf("install switched deja's plugin back on: %v", e)
	}
	// Granted, so `openclaw plugins enable deja` is all it takes.
	if jsonAt(e, "hooks", openclawAccessKey) != true || jsonAt(e, "hooks", "timeoutMs") != float64(9000) {
		t.Errorf("entry = %v", e)
	}
	if !strings.Contains(res.Note, "switched off") {
		t.Errorf("note = %q", res.Note)
	}

	seedOpenClawConfig(t, `{"plugins":{"entries":{"deja":{"enabled":true,"hooks":{"allowConversationAccess":false}}}}}`+"\n")
	res, err = installOpenClawPlugin("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if e := openclawDejaEntry(t); jsonAt(e, "hooks", openclawAccessKey) != false {
		t.Errorf("install overrode the reader's false: %v", e)
	}
	if !strings.Contains(res.Note, openclawAccessKey) {
		t.Errorf("note = %q, want it to name the key left false", res.Note)
	}
}

// A config with comments is edited as text: the grant goes in, the comment
// stays, and uninstall gives the bytes back.
func TestOpenClawConversationAccessJSONCRoundTrip(t *testing.T) {
	hermeticEnv(t)
	fakeOpenClawOnPath(t, "2026.9.8")
	seed := "{\n  // mine\n  \"plugins\": {\n    \"entries\": {\n      \"theirs\": { \"enabled\": true }\n    }\n  }\n}\n"
	seedOpenClawConfig(t, seed)
	if _, err := installOpenClawPlugin("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(openclawConfigPath())
	if !strings.Contains(string(b), "// mine") {
		t.Errorf("comment lost:\n%s", b)
	}
	if e := openclawDejaEntry(t); jsonAt(e, "hooks", openclawAccessKey) != true {
		t.Errorf("entry = %v\n%s", e, b)
	}
	if _, err := installOpenClawPlugin("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(openclawConfigPath()); string(b) != seed {
		t.Errorf("uninstall did not give the file back:\n%s", b)
	}
}

func TestOpenClawDoctorNamesTheMissingGrant(t *testing.T) {
	hermeticEnv(t)
	fakeOpenClawOnPath(t, "2026.9.8")
	seedOpenClawConfig(t, `{"plugins":{"entries":{"deja":{"enabled":true}}}}`+"\n")
	if got := clientHooksOff("openclaw"); !strings.Contains(got, openclawAccessKey) {
		t.Errorf("doctor = %q, want the missing grant named", got)
	}
	fakeOpenClawOnPath(t, "2026.7.1-2")
	if got := clientHooksOff("openclaw"); got != "" {
		t.Errorf("doctor = %q on an OpenClaw without the gate", got)
	}
	fakeOpenClawOnPath(t, "2026.9.8")
	if _, err := installOpenClawPlugin("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got := clientHooksOff("openclaw"); got != "" {
		t.Errorf("doctor = %q after install", got)
	}
}

func TestOpenClawVersionAtLeast(t *testing.T) {
	for _, tc := range []struct {
		v, min string
		want   bool
	}{
		{"2026.9.8", "2026.8.1", true},
		{"2026.7.35", "2026.8.1", false},
		{"2026.7.1-2", "2026.4.24", true},
		{"2026.4.23", "2026.4.24", false},
		{"2026.4.24", "2026.4.24", true},
		{"2026.10.1-beta.1", "2026.8.1", true},
		{"v2026.4.20", "2026.4.24", false},
		{"garbage", "2026.4.24", true},
	} {
		if got := openclawVersionAtLeast(tc.v, tc.min); got != tc.want {
			t.Errorf("%s >= %s = %v, want %v", tc.v, tc.min, got, tc.want)
		}
	}
}

// OpenClaw's heartbeat polls run on agent:main:main and fire agent:bootstrap
// and before_prompt_build like any turn. The digest call and the prompt hook
// both stamped the session live again, so a session the user had left with
// /new dropped out of their other sessions' recall for twenty minutes after
// each poll (#5270).
func TestOpenClawHeartbeatDoesNotStampTheSessionLive(t *testing.T) {
	withStatsStores(t)
	dir := index.DefaultDir()
	cwd := t.TempDir()
	ask := func(fields map[string]any) {
		t.Helper()
		b, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		withHookStdin(t, string(b))
		captureStdout(t, func() { _ = runHookContext(dir, true) })
	}
	const sid = "fd320b23-1476-4773-805e-680e122f9ef1"
	poll := map[string]any{"session_id": sid, "cwd": cwd, "source": "startup", "deja_once": true, "deja_prompt_stamps": true}

	ask(poll)
	if !liveSessionIDs(dir)[sid] {
		t.Fatal("the session's first run did not stamp it")
	}
	// /new ends it.
	endSessionLive(dir, sid)
	// A heartbeat poll on the same session: its bootstrap asks for the digest
	// the session already had.
	ask(poll)
	if liveSessionIDs(dir)[sid] {
		t.Error("a heartbeat's digest call stamped a session that had ended live again")
	}

	// Control: a host whose digest call is all it sends per turn still
	// stamps on every one of them.
	const other = "c0ffee00-1476-4773-805e-680e122f9ef1"
	once := map[string]any{"session_id": other, "cwd": cwd, "source": "startup", "deja_once": true}
	ask(once)
	endSessionLive(dir, other)
	ask(once)
	if !liveSessionIDs(dir)[other] {
		t.Error("a deja_once host without a prompt hook stopped stamping its turns")
	}
}

// The plugin's prompt hook and tool middleware stay out of a heartbeat run:
// no recall on the poll's prompt, and no stamp from it (#5270).
func TestOpenClawPluginSkipsHeartbeatTurns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin openclaw would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s %s\\n' \"$1\" \"$in\" >> " + calls + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, "index.mjs")
	if err := os.WriteFile(plugin, []byte(openclawPluginJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	pack := filepath.Join(home, "handler.mjs")
	if err := os.WriteFile(pack, []byte(openclawHandlerJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
import plugin from "` + plugin + `";
import pack from "` + pack + `";
const on = {}, hooks = {};
let middleware;
plugin.register({ on: (n, fn) => { on[n] = fn }, registerHook: (n, fn) => { hooks[n] = fn }, registerAgentToolResultMiddleware: (fn) => { middleware = fn } });
const ctx = (trigger) => ({ sessionKey: "agent:main:main", sessionId: "fd320b23-1476-4773-805e-680e122f9ef1", workspaceDir: "/w", trigger, runId: "r-" + trigger });
const tool = { toolName: "read", args: { path: "/w/a.go" }, result: { content: [] } };
for (const trigger of ["heartbeat", "user"]) {
  await on.before_prompt_build({ prompt: "Follow the heartbeat monitor scratch context when provided.", messages: [] }, ctx(trigger));
  await middleware(tool, ctx(trigger));
}
const b = { type: "agent", action: "bootstrap", sessionKey: "agent:main:main", context: { workspaceDir: "/w", bootstrapFiles: [], sessionId: "fd320b23-1476-4773-805e-680e122f9ef1" } };
await hooks["agent:bootstrap"](b);
await pack(b);
`
	run := filepath.Join(home, "drive.mjs")
	if err := os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput(); err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	got := readFile(t, calls)
	var prompts, tools, digests int
	for l := range strings.SplitSeq(strings.TrimSpace(got), "\n") {
		sub, payload, _ := strings.Cut(l, " ")
		switch sub {
		case "hook-prompt":
			prompts++
		case "hook-tool":
			tools++
		case "hook-context":
			digests++
			if !strings.Contains(payload, `"deja_prompt_stamps":true`) {
				t.Errorf("a digest call does not say the prompt hook stamps turns: %s", payload)
			}
		}
	}
	// The user turn reaches deja once each; the heartbeat turn not at all.
	if prompts != 1 || tools != 1 || digests != 2 {
		t.Errorf("hook-prompt %d, hook-tool %d, hook-context %d calls; want 1, 1, 2:\n%s", prompts, tools, digests, got)
	}
}

// The parsed writer deleted `hooks.internal.enabled` whenever the entries block
// emptied, including when the reader had set it themselves and deja only
// overwrote it — so an install switched internal hooks on for someone who had
// switched them off, and the uninstall took the setting away rather than
// giving it back (#2830, the parity the JSONC path gained in #2825).
func TestTheParsedPathGivesBackASwitchTheReaderSet(t *testing.T) {
	for _, c := range []struct {
		name  string
		start map[string]any
		// want is what hooks.internal.enabled should hold afterwards; nil for
		// "the key should be gone".
		want any
	}{
		{
			name:  "the reader had it off",
			start: map[string]any{"hooks": map[string]any{"internal": map[string]any{"enabled": false}}},
			want:  false,
		},
		{
			name:  "the reader had it on",
			start: map[string]any{"hooks": map[string]any{"internal": map[string]any{"enabled": true}}},
			want:  true,
		},
		{
			name:  "deja turned it on itself",
			start: map[string]any{},
			want:  nil,
		},
		{
			// The reader's own hook stays, and so must their switch: with an
			// entry left behind the restore was skipped and their hook, which
			// they had off, ran after deja was gone (#3204).
			name:  "the reader had no switch and keeps a hook of their own",
			start: map[string]any{"hooks": map[string]any{"internal": map[string]any{"entries": map[string]any{"theirs": map[string]any{"enabled": true}}}}},
			want:  nil,
		},
		{
			name:  "the reader had it off and keeps a hook of their own",
			start: map[string]any{"hooks": map[string]any{"internal": map[string]any{"enabled": false, "entries": map[string]any{"theirs": map[string]any{"enabled": true}}}}},
			want:  false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			before, err := json.MarshalIndent(c.start, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(before, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			// Twice: reinstalling after an upgrade is ordinary, and the second
			// install reads the switch deja itself set — recording that would
			// hand the reader back deja's own value as if it were theirs.
			if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			// While installed the switch is on whatever the reader had.
			if on := readOpenClawSwitch(t, path); on != true {
				t.Errorf("the hook was wired with the switch %v, so it never runs", on)
			}
			if _, err := installOpenClawAuto("/bin/deja", true); err != nil {
				t.Fatal(err)
			}
			if got := readOpenClawSwitch(t, path); got != c.want {
				t.Errorf("after uninstall the switch is %v, want %v", got, c.want)
			}
		})
	}
}

func readOpenClawSwitch(t *testing.T, path string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("the config no longer parses: %v\n%s", err, b)
	}
	hooks, _ := root["hooks"].(map[string]any)
	internal, _ := mapAt(hooks, "internal")
	if internal == nil {
		return nil
	}
	v, ok := internal["enabled"]
	if !ok {
		return nil
	}
	return v
}

// driveOpenClawPlugin registers the generated plugin against a stub api, calls
// one handler the way OpenClaw 2026.7.1-2 does — (event, ctx) — and returns
// every payload the plugin piped to deja.
func driveOpenClawPlugin(t *testing.T, hook, event string) []map[string]any {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin openclaw would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s\\n' \"$in\" >> " + calls + "\n"
	if err = os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, "index.mjs")
	if err = os.WriteFile(plugin, []byte(openclawPluginJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	// The ctx OpenClaw builds for these hooks carries both: the session key,
	// which names a conversation slot, and the session id its transcript file
	// is named after.
	driver := `
import plugin from "` + plugin + `";
const handlers = {};
plugin.register({ on: (name, fn) => { handlers[name] = fn } });
const ctx = { sessionKey: "agent:main:main", sessionId: "7efce465-14b2-4671-a4d0-6dc309dd4992", agentId: "main" };
await handlers[` + "`" + hook + "`" + `](` + event + `, ctx);
`
	run := filepath.Join(home, "drive.mjs")
	if err = os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, runErr := exec.CommandContext(t.Context(), node, run).CombinedOutput(); runErr != nil {
		t.Fatalf("driving the plugin: %v\n%s", runErr, out)
	}
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("the plugin never ran deja: %v", err)
	}
	var got []map[string]any
	for l := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("payload %q: %v", l, err)
		}
		got = append(got, m)
	}
	return got
}

// before_prompt_build is handed {prompt, messages}, and the session is on the
// second argument. Read off the event it was always "", so per-prompt recall
// had nothing to dedupe against and sent the same block on every turn (#4581).
func TestOpenClawPluginPromptRecallKnowsItsSession(t *testing.T) {
	got := driveOpenClawPlugin(t, "before_prompt_build", `{ prompt: "what did we do about the retry loop", messages: [] }`)
	if len(got) != 1 {
		t.Fatalf("want one hook-prompt call, got %v", got)
	}
	if id, _ := got[0]["session_id"].(string); id != "7efce465-14b2-4671-a4d0-6dc309dd4992" {
		t.Errorf("hook-prompt was sent session_id %q, so recall repeats itself on every turn", id)
	}
}

// The live stamp drops the asking session from MCP recall by its transcript
// id. The plugin stamped OpenClaw's session key — agent:main:main names no
// transcript at all — so the session asking came back as its own top hit, and
// the forget on compaction cleared rows filed under another name (#4582).
func TestOpenClawPluginStampsTheTranscriptID(t *testing.T) {
	for _, tc := range []struct{ hook, event string }{
		{"agent_turn_prepare", `{ prompt: "hello", messages: [], queuedInjections: [] }`},
		{"before_compaction", `{ messageCount: 40, tokenCount: 90000 }`},
	} {
		t.Run(tc.hook, func(t *testing.T) {
			got := driveOpenClawPlugin(t, tc.hook, tc.event)
			if len(got) != 1 {
				t.Fatalf("want one deja call, got %v", got)
			}
			if id, _ := got[0]["session_id"].(string); id != "7efce465-14b2-4671-a4d0-6dc309dd4992" {
				t.Errorf("%s sent session_id %q, not the transcript id the index knows the session by", tc.hook, id)
			}
		})
	}
}

func initOriginTestRepo(t *testing.T) (string, func(env []string, args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := filepath.Join(t.TempDir(), "myapp")
	if err := os.MkdirAll(filepath.Join(repo, "internal", "pool"), 0o755); err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		repo = resolved
	}
	git := func(env []string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=dev", "GIT_AUTHOR_EMAIL=dev@example.com",
			"GIT_COMMITTER_NAME=dev", "GIT_COMMITTER_EMAIL=dev@example.com",
		)
		cmd.Env = append(cmd.Env, env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git(nil, "init", "-q")
	git(nil, "config", "user.name", "dev")
	git(nil, "config", "user.email", "dev@example.com")
	return repo, git
}

func TestOriginAttributesReplacedAndWroteAndRefusesCompactionSummaries(t *testing.T) {
	doc := jsonOutputDoc(t)
	hermeticEnv(t)
	repo, git := initOriginTestRepo(t)
	t.Chdir(repo)

	poolFile := filepath.Join(repo, "internal", "pool", "pool.go")
	oldLine := "\tconn.closeWithoutDrainingInflightRequests()\n"
	newLine := "\tconn.drainInflightRequestsBeforeClose()\n"

	// Commit 1: initial file added at 10:05.
	if err := os.WriteFile(poolFile, []byte("package pool\n\nfunc shutdown() {\n"+oldLine+"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(nil, "add", ".")
	git([]string{"GIT_AUTHOR_DATE=2026-09-18T10:05:00Z", "GIT_COMMITTER_DATE=2026-09-18T10:05:00Z"}, "commit", "-qm", "feat(pool): initial shutdown")
	sha1 := strings.TrimSpace(git(nil, "rev-parse", "HEAD"))

	// Commit 2: replaces oldLine with newLine at 10:20.
	if err := os.WriteFile(poolFile, []byte("package pool\n\nfunc shutdown() {\n"+newLine+"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(nil, "add", ".")
	git([]string{"GIT_AUTHOR_DATE=2026-09-18T10:20:00Z", "GIT_COMMITTER_DATE=2026-09-18T10:20:00Z"}, "commit", "-qm", "fix(pool): drain inflight requests before closing")
	sha2 := strings.TrimSpace(git(nil, "rev-parse", "HEAD"))

	// Commit 3: dependency bump nobody indexed at 10:30.
	goMod := filepath.Join(repo, "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/myapp\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(nil, "add", ".")
	git([]string{"GIT_AUTHOR_DATE=2026-09-18T10:30:00Z", "GIT_COMMITTER_DATE=2026-09-18T10:30:00Z"}, "commit", "-qm", "build(deps): bump module")

	projDir := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), nonAlnum.ReplaceAllString(repo, "-"))
	// Session 1 wrote the initial file (Rule: wrote).
	writeClaudeFixture(t, filepath.Join(projDir, "sess-wrote.jsonl"), "sess-wrote", []string{
		fixtureRow("sess-wrote", "user", "2026-09-18T10:00:00Z", repo, "добавь первоначальный shutdown для пула соединений"),
		fixtureRow("sess-wrote", "assistant", "2026-09-18T10:02:00Z", repo, []any{map[string]any{
			"type": "tool_use", "id": "w1", "name": "Write",
			"input": map[string]any{
				"file_path": poolFile,
				"content":   "package pool\n\nfunc shutdown() {\n" + oldLine + "}\n",
			},
		}}),
		fixtureToolResult("sess-wrote", "2026-09-18T10:02:05Z", repo, "w1", "File created.", false),
	})

	// Session 2 replaced oldLine with newLine (Rule: replaced), after a real instruction,
	// a compaction summary, and a multi-word nudge ("ок и давай дальше").
	compactionSummary := "This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion."
	writeClaudeFixture(t, filepath.Join(projDir, "sess-edit.jsonl"), "sess-edit", []string{
		fixtureRow("sess-edit", "user", "2026-09-18T10:10:00Z", repo, "請修復連接池關閉時未排空請求的問題"),
		fixtureRow("sess-edit", "user", "2026-09-18T10:12:00Z", repo, compactionSummary),
		fixtureRow("sess-edit", "user", "2026-09-18T10:14:00Z", repo, "ок и давай дальше"),
		fixtureRow("sess-edit", "assistant", "2026-09-18T10:15:00Z", repo, "Root cause: shutdown closed the connection immediately without draining in-flight requests first."),
		fixtureRow("sess-edit", "assistant", "2026-09-18T10:15:30Z", repo, []any{map[string]any{
			"type": "tool_use", "id": "e1", "name": "Edit",
			"input": map[string]any{
				"file_path":  poolFile,
				"old_string": strings.TrimSpace(oldLine),
				"new_string": strings.TrimSpace(newLine),
			},
		}}),
		fixtureToolResult("sess-edit", "2026-09-18T10:15:35Z", repo, "e1", "Updated.", false),
	})

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// 1. Single commit JSON check on sha2 (replaced rule).
	var buf bytes.Buffer
	if err := runOrigin(dir, []string{sha2, "--json"}, &buf); err != nil {
		t.Fatalf("runOrigin sha2 --json: %v", err)
	}
	var env originEnvelopeJSON
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if env.SchemaVersion != jsonout.Version || len(env.Commits) != 1 {
		t.Fatalf("envelope = %+v", env)
	}
	c2 := env.Commits[0]
	if c2.Status != originStatusAttributed || !c2.Attributed || c2.Rule != lineRuleReplaced {
		t.Fatalf("commit 2 attribution = %+v", c2)
	}
	if c2.Session == nil || c2.Session.ID != "sess-edit" {
		t.Fatalf("session = %+v, want sess-edit", c2.Session)
	}
	// Compaction summary and bare nudge ("продолжай") must be skipped in favor of the real instruction.
	if c2.Prompt != "請修復連接池關閉時未排空請求的問題" {
		t.Errorf("prompt = %q, want the substantive user instruction", c2.Prompt)
	}
	if strings.Contains(c2.Prompt, "continued from a previous") || c2.Prompt == "продолжай" {
		t.Errorf("compaction summary or bare nudge leaked into prompt: %q", c2.Prompt)
	}
	if !strings.Contains(c2.SaidBefore, "Root cause: shutdown closed") {
		t.Errorf("said_before = %q", c2.SaidBefore)
	}
	if c2.TurnGap != "6m" || c2.WriteGap != "5m" {
		t.Errorf("gaps = turn:%q write:%q", c2.TurnGap, c2.WriteGap)
	}

	// 2. Range check over all 3 commits (sha1..HEAD -> sha3, sha2).
	buf.Reset()
	if err := runOrigin(dir, []string{sha1 + "..HEAD", "--json"}, &buf); err != nil {
		t.Fatalf("runOrigin range: %v", err)
	}
	env = originEnvelopeJSON{}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(env.Commits))
	}
	if env.Commits[0].Status != originStatusUnknown || env.Commits[0].Attributed {
		t.Errorf("bump commit should be unknown: %+v", env.Commits[0])
	}
	if env.Commits[1].Status != originStatusAttributed || env.Commits[1].Session.ID != "sess-edit" {
		t.Errorf("second commit in range = %+v", env.Commits[1])
	}

	// 3. Verify that the emitted JSON keys match docs/json-output.md section.
	section := docSection(t, doc, "## `deja origin [rev|range] --json`")
	var v any
	if err := json.Unmarshal(buf.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	emitted := map[string]bool{}
	collectJSONKeys(v, nil, nil, emitted)
	for _, k := range sortedSet(emitted) {
		if !namesKey(section, k) {
			t.Errorf("deja origin --json prints %q, and its section in docs/json-output.md never names it", k)
		}
	}

	// 4. Prose output on sha1 (wrote rule).
	buf.Reset()
	if err := runOrigin(dir, []string{sha1}, &buf); err != nil {
		t.Fatal(err)
	}
	prose := buf.String()
	for _, want := range []string{
		"written in claude · sess-wro",
		"wrote this line: conn.closeWithoutDrainingInflightRequests()",
		"user turn (2m before edit): добавь первоначальный shutdown",
		"why, in full: deja ctx sess-wro",
	} {
		if !strings.Contains(prose, want) {
			t.Errorf("prose output missing %q:\n%s", want, prose)
		}
	}
}

func TestOriginReportsUncertainOnOverlappingSessionsAndStaleWrites(t *testing.T) {
	hermeticEnv(t)
	repo, git := initOriginTestRepo(t)
	t.Chdir(repo)

	fileA := filepath.Join(repo, "internal", "pool", "a.go")
	fileB := filepath.Join(repo, "internal", "pool", "b.go")
	lineA := "\talphaConnectionPoolInitializerForShards()\n"
	lineB := "\tbetaConnectionPoolInitializerForShards()\n"

	if err := os.WriteFile(fileA, []byte("package pool\nfunc a() {\n"+lineA+"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("package pool\nfunc b() {\n"+lineB+"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(nil, "add", ".")
	git([]string{"GIT_AUTHOR_DATE=2026-09-18T12:00:00Z", "GIT_COMMITTER_DATE=2026-09-18T12:00:00Z"}, "commit", "-qm", "feat: add a and b")
	shaOverlap := strings.TrimSpace(git(nil, "rev-parse", "HEAD"))

	// Commit 2: committed 10 days after the session wrote it -> uncertain (stale write gap).
	fileC := filepath.Join(repo, "internal", "pool", "c.go")
	lineC := "\tgammaConnectionPoolInitializerForShards()\n"
	if err := os.WriteFile(fileC, []byte("package pool\nfunc c() {\n"+lineC+"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(nil, "add", ".")
	git([]string{"GIT_AUTHOR_DATE=2026-09-29T12:00:00Z", "GIT_COMMITTER_DATE=2026-09-29T12:00:00Z"}, "commit", "-qm", "feat: add c")
	shaStale := strings.TrimSpace(git(nil, "rev-parse", "HEAD"))

	projDir := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), nonAlnum.ReplaceAllString(repo, "-"))
	writeClaudeFixture(t, filepath.Join(projDir, "s-alpha.jsonl"), "s-alpha", []string{
		fixtureRow("s-alpha", "user", "2026-09-18T11:50:00Z", repo, "write a.go"),
		fixtureRow("s-alpha", "assistant", "2026-09-18T11:51:00Z", repo, []any{map[string]any{
			"type": "tool_use", "id": "wa", "name": "Write",
			"input": map[string]any{"file_path": fileA, "content": "package pool\nfunc a() {\n" + lineA + "}\n"},
		}}),
	})
	writeClaudeFixture(t, filepath.Join(projDir, "s-beta.jsonl"), "s-beta", []string{
		fixtureRow("s-beta", "user", "2026-09-18T11:52:00Z", repo, "write b.go and c.go"),
		fixtureRow("s-beta", "assistant", "2026-09-18T11:53:00Z", repo, []any{map[string]any{
			"type": "tool_use", "id": "wb", "name": "Write",
			"input": map[string]any{"file_path": fileB, "content": "package pool\nfunc b() {\n" + lineB + "}\n"},
		}}),
		fixtureRow("s-beta", "assistant", "2026-09-18T11:54:00Z", repo, []any{map[string]any{
			"type": "tool_use", "id": "wc", "name": "Write",
			"input": map[string]any{"file_path": fileC, "content": "package pool\nfunc c() {\n" + lineC + "}\n"},
		}}),
	})

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Overlapping sessions on shaOverlap -> uncertain, 2 candidates.
	var buf bytes.Buffer
	if err := runOrigin(dir, []string{shaOverlap, "--json"}, &buf); err != nil {
		t.Fatal(err)
	}
	var env originEnvelopeJSON
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Commits[0].Status != originStatusUncertain || !env.Commits[0].Uncertain || env.Commits[0].Attributed || len(env.Commits[0].Candidates) != 2 {
		t.Fatalf("overlap commit = %+v", env.Commits[0])
	}

	// Stale write (>7 days before commit) on shaStale -> uncertain.
	buf.Reset()
	if err := runOrigin(dir, []string{shaStale, "--json"}, &buf); err != nil {
		t.Fatal(err)
	}
	env = originEnvelopeJSON{}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Commits[0].Status != originStatusUncertain || !env.Commits[0].Uncertain || env.Commits[0].Attributed {
		t.Fatalf("stale commit = %+v", env.Commits[0])
	}
	if !strings.Contains(env.Commits[0].Why, "11d before the commit") {
		t.Errorf("why = %q, want 11d gap explanation", env.Commits[0].Why)
	}

	// Prose rendering of uncertain commit.
	buf.Reset()
	if err := runOrigin(dir, []string{shaStale}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "uncertain:") || !strings.Contains(buf.String(), "closest session:") {
		t.Errorf("uncertain prose:\n%s", buf.String())
	}
}

func TestParseOriginValidation(t *testing.T) {
	if o, err := parseOrigin(nil); err != nil || o.Rev != "HEAD" || o.Limit != originDefaultLimit {
		t.Errorf("default parseOrigin = %+v, %v", o, err)
	}
	if o, err := parseOrigin([]string{"--limit=5", "--harness=claude", "--project=app", "--", "main~5..main"}); err != nil ||
		o.Limit != 5 || o.Harness != "claude" || o.Project != "app" || o.Rev != "main~5..main" {
		t.Errorf("parsed = %+v, %v", o, err)
	}
	for _, bad := range [][]string{
		{"--unknown"},
		{"--limit", "0"},
		{"--limit", "999"},
		{"--harness"},
		{"--project", "  "},
		{"HEAD", "main"},
		{"--", "HEAD", "main"},
	} {
		if _, err := parseOrigin(bad); err == nil {
			t.Errorf("parseOrigin(%v) succeeded, want error", bad)
		}
	}
}

// The harness packages under extensions/ describe deja to npm, ClawHub and
// the pi gallery, and each says the count in its own way: "twenty-two other
// coding agents" (the host excluded), or "pi, Claude Code, Codex, Cursor and
// 19 more" (the named ones excluded). The day copilot-chat made it
// twenty-three, the seven files the README test pins were updated and these
// were not (#3099). Same rule, one directory over.
func TestPackagesCountTheOtherHarnesses(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)
	other, words := harnessCountWords(t, root, -1)
	dirs, err := filepath.Glob(filepath.Join(root, "extensions", "*", "package.json"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no packages under extensions/: %v", err)
	}
	more := regexp.MustCompile(`([^—.:"]*?)\band (\d+) more\b`)
	for _, pkg := range dirs {
		for _, name := range []string{"package.json", "README.md"} {
			path := filepath.Join(filepath.Dir(pkg), name)
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			text := strings.ToLower(string(b))
			rel, _ := filepath.Rel(root, path)
			// "N other coding agents": the count less the host.
			for _, w := range words {
				if w != other && strings.Contains(text, w+" other") {
					t.Errorf("%s says %q other agents; the registry has %d, so %q", rel, w, n, other)
				}
			}
			// "A, B, C and N more": the named ones plus N is the count.
			for _, m := range more.FindAllStringSubmatch(text, -1) {
				got, _ := strconv.Atoi(m[2])
				named := len(strings.Split(strings.TrimSpace(m[1]), ","))
				if named+got != n {
					t.Errorf("%s names %d agents and %d more, %d in all; the registry has %d", rel, named, got, named+got, n)
				}
			}
		}
	}
}

// The third way the count is written, after the spelled-out word and the plain
// digit: "Claude Code, Codex and 15 more" — a number that means "everything
// except the ones I just named", so it goes stale on a harness landing without
// any of the words a search for the count would find. It is also the line a
// search engine shows and the one a link preview renders, on the two pages
// people arrive at.
func TestPageTitlesCountTheRestOfTheHarnesses(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)

	for _, c := range []struct {
		file  string
		named int    // harnesses named in the phrase itself
		shape string // %d is the rest
	}{
		{"docs/index.html", 6, "Claude Code, Codex, Cursor, OpenCode, Gemini CLI, OpenClaw and %d more agents"},
		{"docs/guide/harnesses.html", 3, "Claude Code, Codex, Cursor and %d more agents"},
	} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.file)))
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		want := fmt.Sprintf(c.shape, n-c.named)
		if !strings.Contains(string(b), want) {
			t.Errorf("%s does not say %q — the registry has %d harnesses", c.file, want, n)
		}
	}

	// The home page says it in a figure of its own — "Works with 18" — sitting
	// above a grid that listed twenty. The digit test looks for "N harnesses"
	// or "N agents" and a bare number in a span is neither, so it stayed at
	// eighteen through three harnesses landing.
	home, err := os.ReadFile(filepath.Join(root, "docs", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf(`Works with <span class="fig">%d</span>`, n); !strings.Contains(string(home), want) {
		t.Errorf("docs/index.html does not say %q — the registry has %d harnesses", want, n)
	}

	// The registry pages close with "deja reads this format and N others". The
	// word was typed into the generator's template, so all twenty pages kept
	// saying seventeen for three harnesses running.
	dir := filepath.Join(root, "docs", "registry")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("registry pages: %v", err)
	}
	want := fmt.Sprintf("this format and %s others", countWordForTest(n-1))
	pages := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".html" || e.Name() == "README.html" {
			continue
		}
		b, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			t.Fatalf("%s: %v", e.Name(), readErr)
		}
		pages++
		if !strings.Contains(string(b), want) {
			t.Errorf("docs/registry/%s does not say %q", e.Name(), want)
		}
	}
	if pages != n {
		t.Errorf("%d registry pages for %d harnesses", pages, n)
	}

	// And llms.txt, which is the summary an agent reads instead of the site. It
	// names six harnesses and spells the rest, and had stood at fifteen through
	// four landing.
	llms, err := os.ReadFile(filepath.Join(root, "docs", "llms.txt"))
	if err != nil {
		t.Fatal(err)
	}
	named := strings.Count("Claude Code, Codex, Cursor, opencode, Gemini CLI, Zed", ",") + 1
	if want := fmt.Sprintf("Zed and %s more agents", countWordForTest(n-named)); !strings.Contains(string(llms), want) {
		t.Errorf("docs/llms.txt does not say %q — the registry has %d harnesses", want, n)
	}
}

// countWordForTest spells the number the generator's template spells.
func countWordForTest(n int) string {
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven",
		"eight", "nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen",
		"sixteen", "seventeen", "eighteen", "nineteen", "twenty", "twenty-one", "twenty-two",
		"twenty-three", "twenty-four", "twenty-five", "twenty-six", "twenty-seven",
		"twenty-eight", "twenty-nine", "thirty", "thirty-one", "thirty-two",
		"thirty-three", "thirty-four", "thirty-five", "thirty-six", "thirty-seven",
		"thirty-eight", "thirty-nine", "forty", "forty-one", "forty-two",
		"forty-three", "forty-four", "forty-five", "forty-six", "forty-seven",
		"forty-eight", "forty-nine", "fifty"}
	if n < 0 || n >= len(words) {
		return strconv.Itoa(n)
	}
	return words[n]
}

// carriesControl reports whether a printed line would reach the terminal with
// something on it that a terminal acts on.
func carriesControl(s string) bool {
	for _, r := range s {
		if (unicode.IsControl(r) && r != '\n') || unicode.Is(unicode.Cf, r) {
			return true
		}
	}
	return false
}

// Every line that hands the reader a command to paste puts the value in it
// through pasteSafe: raw, an escape byte in a file name, a session id or the
// reader's own query reaches the terminal, and a shell metacharacter makes the
// pasted command run something else (#2768, the rest of #1794).
func TestTheHintsThatOfferACommandQuoteWhatTheyOffer(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// A metacharacter in the name, which is what reaches this hint: a control
	// byte does not, because the indexer drops the record before it gets here
	// — that half is held at the unit level below.
	path := "/api/internal/x/parser&&x.go"
	touched := `{"type":"assistant","timestamp":"2026-07-10T10:00:00Z","sessionId":"aaaa0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` +
		path + `","old_string":"a","new_string":"b"}}]}}`
	ran := `{"type":"assistant","timestamp":"2026-07-10T10:05:00Z","sessionId":"aaaa0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./internal/x/... -run Parser"}}]}}`
	if err := os.WriteFile(filepath.Join(store, "one.jsonl"), []byte(touched+"\n"+ran+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A command is worth remembering once two sessions ran it, which is what
	// the table the `deja how` half reads is built from.
	again := strings.ReplaceAll(ran, "aaaa0001", "bbbb0002")
	if err := os.WriteFile(filepath.Join(store, "two.jsonl"), []byte(again+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// The file half, end to end: the name comes out of a transcript and the
	// command below the sentence is meant to be pasted.
	hint := roleServedHint(dir, "parser")
	if !strings.Contains(hint, "deja blame") {
		t.Fatalf("the file half of the hint is missing, so this test guards nothing:\n%s", hint)
	}
	if carriesControl(hint) {
		t.Errorf("a control byte reached the terminal:\n%q", hint)
	}
	if strings.Contains(hint, "blame parser&&x.go") {
		t.Errorf("a shell metacharacter is unquoted in a command to paste:\n%q", hint)
	}
	// And the words half, through the hint itself: `deja how` ANDs its
	// arguments, so a quoted phrase becomes one term it then requires
	// contiguously — the count in the sentence above was taken over the words
	// (#2768). Asserted on the wiring, because the wiring is what decides
	// whether the offer and the count can disagree.
	hint = roleServedHint(dir, "parser test")
	if !strings.Contains(hint, "deja how") {
		t.Fatalf("the command half of the hint is missing, so this half guards nothing:\n%s", hint)
	}
	if !strings.Contains(hint, "`deja how parser test`") {
		t.Errorf("the words were not handed over as they were counted:\n%q", hint)
	}
}

// The `deja fix` line offers an error string lifted from a transcript, and a
// shell expands `$(…)` and backticks inside double quotes as readily as
// outside them — so Go's own `%q` was not quoting for the reader who pastes.
func TestTheFixHintQuotesTheErrorItOffers(t *testing.T) {
	for _, near := range []string{"error: $(rm -rf /tmp/x) failed", "error: `id` not found"} {
		got := pasteSafe(near)
		if strings.HasPrefix(got, `"`) {
			t.Errorf("%q was double-quoted, which a shell still expands: %s", near, got)
		}
		if !strings.HasPrefix(got, "'") && !strings.HasPrefix(got, "$'") {
			t.Errorf("%q was handed over unquoted: %s", near, got)
		}
	}
}

// A query word can start with a dash — somebody asking about `-run` or
// `--limit` — and `deja how` reads it as a flag it does not have, or as one it
// does, swallowing the next word. The command's own escape says the rest is
// the query.
func TestTheHowOfferEscapesAQueryThatLooksLikeFlags(t *testing.T) {
	for _, c := range []struct {
		terms []string
		want  string
	}{
		{[]string{"go", "test"}, "`deja how go test`"},
		{[]string{"-run", "parser"}, "`deja how -- -run parser`"},
		{[]string{"why", "--limit", "ignored"}, "`deja how -- why --limit ignored`"},
	} {
		got := howOfferLine(3, "match", c.terms)
		if !strings.Contains(got, c.want) {
			t.Errorf("howOfferLine(%v) = %q, want it to carry %s", c.terms, got, c.want)
		}
	}
}

// The line promote prints when it takes a mark back offers the note's id, and
// the same sentence echoes the session it came from: one is pasted, the other
// is read.
func TestTheTakenBackLineQuotesTheIdItOffers(t *testing.T) {
	line := markTakenBack("claude:has space", "accepted", sources.Lifecycle{State: "rejected"})
	if line == "" {
		t.Fatal("the fixture no longer produces the line this test is about")
	}
	if strings.Contains(line, "`deja show deja-note-claude-has space`") {
		t.Errorf("the id is unquoted in a command to paste: %q", line)
	}
	esc := string(rune(0x1b))
	line = markTakenBack("claude:esc"+esc+"[31mX", "accepted", sources.Lifecycle{State: "rejected"})
	if carriesControl(line) {
		t.Errorf("a control byte reached the terminal: %q", line)
	}
}

// The hint hands the reader a command to paste, so the id in it cannot be
// sanitised — a sanitised id names nothing and the paste fails. It also cannot
// carry an escape byte to the terminal (#1794). ANSI-C quoting is both: the
// bytes are visible, and bash and zsh reproduce them.
func TestAPastedIdShowsItsControlBytesAndStillPastes(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"deja-2026-08-25-notes", "deja-2026-08-25-notes"},
		{"deja-2026-08-25-p\x1b[31mX", `$'deja-2026-08-25-p\e[31mX'`},
		{"two words", `'two words'`},
		{"it's", `'it'"'"'s'`},
		{"tab\there", `$'tab\there'`},
		{"nl\nhere", `$'nl\nhere'`},
		// A rendering rather than a round-trip: bash and dash drop a NUL
		// from an argument. Unreachable — argv cannot carry one and
		// Tombstones() filters keys that do — and pinned so the escape is
		// visible rather than raw if it ever arrives.
		{"nul\x00here", `$'nul\x00here'`},
		// A shell acts on more than quotes and spaces. Pasting the first of
		// these used to run `id`, and the second truncated a file.
		{"proj&&id", `'proj&&id'`},
		{"proj;id", `'proj;id'`},
		{"proj>out", `'proj>out'`},
		{"proj|id", `'proj|id'`},
		{"proj(x)", `'proj(x)'`},
		{"proj*", `'proj*'`},
		{"~proj", `'~proj'`},
		// C1 controls and the format characters: the prose half strips these,
		// so the command half cannot carry them raw. U+009B is CSI.
		{"proj\u009bX", `$'proj\u009bX'`},
		// zsh replaces a word-initial `=word` with the path of that command.
		{"=ls", `'=ls'`},
		{"a=b", `'a=b'`},
		{"proj\u202eX", `$'proj\u202eX'`},
		// A byte that is not valid UTF-8 stays that byte: decoding it to
		// U+FFFD wrote three different bytes back, so the pasted command
		// named a key that does not exist.
		{"proj\xffX", `$'proj\xffX'`},
		// An ordinary accented name is not a shell problem and stays bare.
		{"projekt-über", "projekt-über"},
	} {
		if got := pasteSafe(c.in); got != c.want {
			t.Errorf("pasteSafe(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

// And the hint itself carries no raw escape byte.
func TestTheTombstoneHintCarriesNoEscapeByte(t *testing.T) {
	hint := tombstoneHint("it is written", "deja-2026-08-25-p\x1b[31mX")
	if strings.ContainsRune(hint, 0x1b) {
		t.Errorf("the hint carries an escape byte to the terminal:\n%q", hint)
	}
	if !strings.Contains(hint, "deja forget --unforget") {
		t.Errorf("the hint no longer says how to lift the tombstone:\n%s", hint)
	}
	if !strings.Contains(hint, `\e[31mX`) {
		t.Errorf("the id is not shown the way it can be pasted back:\n%s", hint)
	}
}

// The two halves of the line agree about what a terminal acts on: the prose
// half strips it, the command half quotes it, and neither passes it through.
func TestBothHalvesOfTheHintAgreeAboutControlCharacters(t *testing.T) {
	for _, id := range []string{
		"deja-2026-08-25-p\x1b[31mX",
		"deja-2026-08-25-p\u009bX",
		"deja-2026-08-25-p\u202eX",
		"deja-2026-08-25-p\u200eX",
		"deja-2026-08-25-p\x7fX",
	} {
		hint := tombstoneHint("it is written", id)
		for _, r := range hint {
			if unicode.IsControl(r) && r != '\n' {
				t.Errorf("%q: the hint carries a control character: %q", id, hint)
				break
			}
			if unicode.Is(unicode.Cf, r) {
				t.Errorf("%q: the hint carries a format character: %q", id, hint)
				break
			}
		}
	}
}

// The ANSI-C form is bash and zsh only, so the line says so — a dash or fish
// reader pasting it would get a literal `$` and match nothing.
func TestTheHintNamesTheShellsThatCanPasteIt(t *testing.T) {
	odd := tombstoneHint("it is written", "deja-2026-08-25-p\x1b[31mX")
	if !strings.Contains(odd, "in bash or zsh") {
		t.Errorf("the quoted form is not attributed to a shell that reads it:\n%s", odd)
	}
	plain := tombstoneHint("it is written", "deja-2026-08-25-notes")
	if strings.Contains(plain, "bash or zsh") {
		t.Errorf("an ordinary id was given a shell caveat it does not need:\n%s", plain)
	}
}

// Every line that hands over a quoted value carries the shell caveat, not just
// the tombstone hint: dash takes `$'…'` literally and the command then matches
// nothing, silently.
func TestEveryQuotedPasteSaysWhichShellReadsIt(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"deja-2026-08-25-p\x1b[31mX", " (in bash or zsh)"},
		{"deja-2026-08-25-has space", ""},
		{"deja-2026-08-25-plain", ""},
	} {
		if got := pasteSafeCaveat(pasteSafe(c.in)); got != c.want {
			t.Errorf("pasteSafeCaveat(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// An id arrives from a chat wrapped in quotes or backticks, and off deja's own
// screen with the harness in front of it: `forget --list`, the undo line beside
// it and promote's receipts all print `harness:id`. `show` refused that form
// and `forget --session` accepted it while matching nothing (#921).
func TestAPastedIdIsAccepted(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "a1b2c3d4-1111-4000-8000-d6e7f8a9b0c1"
	rec := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-10T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	for _, sel := range []string{
		id,
		"claude:" + id,
		`"` + id + `"`,
		"'" + id + "'",
		"`claude:" + id + "`",
		"  " + id + "  ",
	} {
		out, err := captureRun(t, "show", sel)
		if err != nil {
			t.Errorf("show %q: %v", sel, err)
			continue
		}
		if !strings.Contains(out, search.ShortID(id)) {
			t.Errorf("show %q found nothing:\n%s", sel, out)
		}
	}

	// The harness in front of the id is honoured, not ignored: another
	// harness's name means another session.
	if _, err := captureRun(t, "show", "codex:"+id); err == nil {
		t.Error("show accepted an id under the wrong harness")
	}

	// And the same shape selects for forget rather than silently matching
	// nothing.
	out, err := captureRun(t, "forget", "--session", "claude:"+id, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "would drop: 1 session,") {
		t.Errorf("forget --session harness:id matched nothing:\n%s", out)
	}
}

// What deja prints for a file has to be what deja can find again. A name with
// two spaces came back with one, and restoring the printed path found nothing
// deja had just listed (#2044).
func TestAPrintedPathFindsItsOwnSpan(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	dir := filepath.Join(claude, "-tmp-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_INDEX_TOOL_PATHS", "1")
	t.Setenv("DEJA_INDEX_EDITS", "1")

	path := "/tmp/app/two  spaces.go"
	rec := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	body := rec(map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": "2026-01-02T03:04:05Z",
		"message":   map[string]any{"role": "user", "content": "raise the pool size"},
	}) + "\n" + rec(map[string]any{
		"type": "assistant", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": "2026-01-02T03:04:06Z",
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{"file_path": path}},
			map[string]any{"type": "tool_use", "name": "Edit", "input": map[string]any{
				"file_path": path, "old_string": "size = 20", "new_string": "size = 40"}},
		}},
	}) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "index"); err != nil {
		t.Fatalf("index: %v %s", err, out)
	}

	blamed, err := captureRun(t, "blame", "two  spaces.go")
	if err != nil {
		t.Fatal(err)
	}
	// The premise: blame found the file at all, or there is no printed path to
	// carry anywhere.
	printed := ""
	for line := range strings.SplitSeq(blamed, "\n") {
		if f := strings.TrimSpace(line); strings.HasSuffix(f, "spaces.go") {
			printed = f
			break
		}
	}
	if printed == "" {
		t.Fatalf("blame printed no path for the file:\n%s", blamed)
	}

	out, err := captureRun(t, "restore", printed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 replaced span recorded") {
		t.Errorf("restoring the path blame printed (%q) found nothing:\n%s", printed, out)
	}
}

// seedPeerBatch writes a batch stamped with the given origin and returns the
// directory holding it.
func seedPeerBatch(t *testing.T, tmp, origin string, n int) string {
	t.Helper()
	batch := filepath.Join(tmp, "batch-"+origin)
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 0, n)
	for i := range n {
		rec := map[string]any{
			"harness": "claude", "session_id": origin + string(rune('a'+i)), "project": "proj",
			"role": "user", "text": "retry loop", "time": "2026-08-20T10:00:0" + string(rune('0'+i)) + "Z",
			"origin": origin,
		}
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return batch
}

// The peers row holds the ssh alias someone typed; an imported session is
// stamped with what the sending machine calls itself. #1876 made the comparison
// case-insensitive, which does nothing when the two names differ outright —
// the normal case for anyone with an ssh config. The row then said a machine
// had sent nothing while its sessions were in the index (#1887).
func TestAPullLearnsWhatTheMachineCallsItself(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	if err := os.WriteFile(peersFile, []byte(`{"peers":[{"host":"work","last_pull":"2026-08-20T10:00:00Z"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()

	before := importsByPeerName(dir)
	if _, err := index.Import(dir, seedPeerBatch(t, tmp, "build-box", 3)); err != nil {
		t.Fatal(err)
	}
	learnPeerMachine(dir, "work", before)

	list := peers.Load()
	if len(list) != 1 || list[0].Machine != "build-box" {
		t.Fatalf("the pairing was not learned: %#v", list)
	}

	var text strings.Builder
	doctorPeers(&text, dir, time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC))
	if !strings.Contains(text.String(), "3 sessions from there") {
		t.Errorf("the row still counts none of what arrived:\n%s", text.String())
	}

	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), dir); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Sync struct {
			Peers []struct {
				Sessions int `json:"sessions_from_there"`
			} `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Sync.Peers) != 1 || report.Sync.Peers[0].Sessions != 3 {
		t.Errorf("--json counts %#v, want three", report.Sync.Peers)
	}
}

// Only what actually arrived in this exchange is attributed to the host: a
// batch from a third machine sitting in the index must not rename the peer.
func TestLearningIgnoresWhatWasAlreadyThere(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	if err := os.WriteFile(peersFile, []byte(`{"peers":[{"host":"work"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if _, err := index.Import(dir, seedPeerBatch(t, tmp, "old-desktop", 2)); err != nil {
		t.Fatal(err)
	}

	before := importsByPeerName(dir)
	if _, err := index.Import(dir, seedPeerBatch(t, tmp, "build-box", 3)); err != nil {
		t.Fatal(err)
	}
	learnPeerMachine(dir, "work", before)

	list := peers.Load()
	if len(list) != 1 || list[0].Machine != "build-box" {
		t.Fatalf("the wrong machine was learned: %#v", list)
	}
}

// An exchange that brought nothing new leaves the row alone.
func TestAnEmptyPullLearnsNothing(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	if err := os.WriteFile(peersFile, []byte(`{"peers":[{"host":"work"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	before := importsByPeerName(dir)
	learnPeerMachine(dir, "work", before)
	if list := peers.Load(); len(list) != 1 || list[0].Machine != "" {
		t.Fatalf("a pull that brought nothing wrote a name: %#v", list)
	}
}

// A pull that brings records from two origins teaches nothing: a batch can
// carry a third machine's work, and another process can import while this
// runs, so the safe answer is the count that was there before.
func TestAnAmbiguousPullLearnsNothing(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	if err := os.WriteFile(peersFile, []byte(`{"peers":[{"host":"work"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()

	before := importsByPeerName(dir)
	if _, err := index.Import(dir, seedPeerBatch(t, tmp, "build-box", 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Import(dir, seedPeerBatch(t, tmp, "someone-else", 2)); err != nil {
		t.Fatal(err)
	}
	learnPeerMachine(dir, "work", before)

	if list := peers.Load(); len(list) != 1 || list[0].Machine != "" {
		t.Fatalf("a pull carrying two origins wrote a name: %#v", list)
	}
}

// A machine renamed, or an alias repointed at another host, is corrected by
// the next pull rather than kept forever.
func TestARepointedAliasIsRelearned(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	if err := os.WriteFile(peersFile, []byte(`{"peers":[{"host":"work","machine":"build-box"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()

	before := importsByPeerName(dir)
	if _, err := index.Import(dir, seedPeerBatch(t, tmp, "test-machine", 2)); err != nil {
		t.Fatal(err)
	}
	learnPeerMachine(dir, "work", before)

	if list := peers.Load(); len(list) != 1 || list[0].Machine != "test-machine" {
		t.Fatalf("the stale pairing survived: %#v", list)
	}
}

// A peer's name is a borrowed string: an ssh alias someone typed, or what
// another machine called itself. The report must not hand a terminal what that
// string asks for, and the JSON must hand a tool the name it needs to act on —
// two different answers to one input, and the docs settle which is which.
func TestAPeerNameCannotWriteOnTheReport(t *testing.T) {
	tmp := hermeticEnv(t)
	const nasty = "lap\x1b[31mtop\x07\nSync: peers    everything is fine"
	peersFile := filepath.Join(tmp, "peers.json")
	body, err := json.Marshal(map[string]any{"peers": []map[string]any{
		{"host": nasty, "machine": nasty, "last_pull": time.Now().UTC().Format(time.RFC3339)},
		{"host": strings.Repeat("a", 120), "last_push": time.Now().UTC().Format(time.RFC3339)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(peersFile, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_PEERS_FILE", peersFile)

	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	sync := doctorSection(out, "Sync:")
	if strings.Contains(sync, "\x1b") || strings.Contains(sync, "\x07") {
		t.Errorf("a peer name carried a control byte into the report: %q", sync)
	}
	// One row per peer: a name with a newline in it must not become a second
	// line that reads as deja's own.
	rows := 0
	for line := range strings.SplitSeq(sync, "\n") {
		if strings.Contains(line, "last exchange") {
			rows++
		}
	}
	if rows != 2 {
		t.Errorf("two peers produced %d rows:\n%s", rows, sync)
	}
	// Sanitised, not dropped: the escape becomes a space and the rest of the
	// name is still readable, so the row still names a machine.
	if !strings.Contains(sync, "lap") || !strings.Contains(sync, "top") {
		t.Errorf("the name is gone rather than sanitised:\n%s", sync)
	}

	// The JSON is the other answer: `host` is a name to act on — `deja sync ssh
	// <host>` — so it is reported as the file spells it, and the encoder is
	// what keeps the control bytes out of a terminal.
	raw, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "\x1b") || strings.Contains(raw, "\x07") {
		t.Errorf("doctor --json emitted a raw control byte")
	}
	var report struct {
		Sync struct {
			Peers []struct {
				Host string `json:"host"`
			} `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("doctor --json is not JSON: %v", err)
	}
	found := false
	for _, p := range report.Sync.Peers {
		if p.Host == nasty {
			found = true
		}
	}
	if !found {
		t.Errorf("doctor --json did not report the host as the file spells it: %+v", report.Sync.Peers)
	}
}

// checkPersonLines fails when the lines deja shows the person run past one
// terminal line each or past most lines in all.
func checkPersonLines(t *testing.T, what, msg string, most int) {
	t.Helper()
	lines := strings.Split(msg, "\n")
	if msg == "" {
		lines = nil
	}
	if len(lines) > most {
		t.Errorf("%s is %d lines, want at most %d:\n%s", what, len(lines), most, msg)
	}
	for _, l := range lines {
		if w := termwidth.Columns(l); w > noteWidth {
			t.Errorf("%s has a %d-column line, over %d: %q", what, w, noteWidth, l)
		}
	}
}

// The first session starts on a freshly built machine, where every once-only
// note is due at the same time, were four or five lines in every host:
// receipt, built note, docs, the GitHub line. A session start now says the
// receipt and at most one note, and the notes left over arrive on later starts.
func TestSessionStartSaysAtMostTwoShortLines(t *testing.T) {
	withStatsStores(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	when := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(root, "beta", "drain.jsonl"), "drainpass", []string{
		`{"type":"user","sessionId":"drainpass","timestamp":"` + when + `","message":{"role":"user","content":"how should the zorbit queue drain on deploy"}}`,
		`{"type":"assistant","sessionId":"drainpass","timestamp":"` + when + `","message":{"role":"assistant","content":[{"type":"text","text":"we settled on draining the zorbit queue with --grace 40 before each deploy"}]}}`,
	})
	for i, topic := range []string{"css grid layout for the settings page", "upgrade the postgres driver", "flaky login test on ci", "rename the billing module", "add a healthcheck endpoint", "cache the avatar thumbnails"} {
		id := "bg" + string(rune('a'+i))
		writeClaudeFixture(t, filepath.Join(root, "beta", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + when + `","message":{"role":"user","content":"` + topic + `"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + when + `","message":{"role":"assistant","content":[{"type":"text","text":"done: ` + topic + `"}]}}`,
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	start := func(sid string) string {
		payload, err := json.Marshal(map[string]any{"session_id": sid, "cwd": cwd, "source": "startup"})
		if err != nil {
			t.Fatal(err)
		}
		withHookStdin(t, string(payload))
		out := captureStdout(t, func() { _ = runHookContext(dir, false) })
		var resp sessionStartHookResponse
		if strings.TrimSpace(out) == "" {
			return ""
		}
		if err = json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
			t.Fatalf("not the hook envelope: %v\n%s", err, out)
		}
		return resp.SystemMessage
	}
	first := start("width-1")
	if !strings.Contains(first, "deja recalled") || !strings.Contains(first, "deja indexed") {
		t.Fatalf("the first start should carry the receipt and the built note, or this test proves nothing:\n%s", first)
	}
	checkPersonLines(t, "the first session start", first, 2)
	second := start("width-2")
	if !strings.Contains(second, "github.com/vshulcz/deja-vu") {
		t.Fatalf("the GitHub line the first start held back never came:\n%s", second)
	}
	checkPersonLines(t, "the second session start", second, 2)

	payload, err := json.Marshal(map[string]any{"prompt": "what grace should the zorbit queue drain use before deploy", "cwd": cwd, "session_id": "width-3"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runHookPromptMode(dir, bytes.NewReader(payload), &out, false); err != nil {
		t.Fatal(err)
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("not the hook envelope: %v\n%s", err, out.String())
	}
	if !strings.Contains(resp.SystemMessage, "deja-vu — ") {
		t.Fatalf("no déjà vu line to measure: %q", resp.SystemMessage)
	}
	checkPersonLines(t, "the déjà vu line", resp.SystemMessage, 1)
}

// Every note at the sizes a real machine reaches, from the functions that
// write it: long topics, several targets, large counts, a home-directory path.
func TestEachPersonNoteFitsOneLine(t *testing.T) {
	long := model.Session{ID: "l", Project: "app", Updated: time.Now().Add(-90 * 24 * time.Hour),
		Messages: []model.Message{{Role: "user", Text: "why does the nightly exporter drop the last batch of rows at midnight on the replica"}}}
	imported := long
	imported.Project = "imported:app"
	const home = "/Users/alexandra/Library/Caches/deja"
	notes := map[string]string{
		"rewire":       rewireNote([]string{"claude-auto", "codex-auto", "gemini", "opencode", "copilot"}),
		"stuck":        stuckWiringNote([]string{"claude-auto", "codex-auto", "gemini", "opencode", "copilot"}),
		"déjà vu":      dejaVuLine(long, "exporter", "replica", "nightly"),
		"imported":     dejaVuLine(imported, "exporter", "replica", "nightly"),
		"star":         starText,
		"built":        builtLine(123456, 41, 1234, true),
		"indexing":     indexingNote,
		"progress":     (&warmupStatus{Phase: "sessions", Done: 42, Total: 100}).line(),
		"index cannot": indexCannotAnswerNote,
		"unmounted":    unmountedIndexNote("/Volumes/ext/caches"),
		"unwritable":   unwritableIndexNote(home + "/index.db"),
		"withheld":     withheldNote("local-only+imported:team-laptop", 1234),
		"stale":        staleIndexNote(home),
	}
	for name, note := range notes {
		if note == "" {
			t.Errorf("%s is empty, so this measures nothing", name)
		}
		checkPersonLines(t, name, note, 1)
	}
	if got := fitList([]string{"a.go", "b.go", "c.go"}, 48); got != "a.go, b.go, c.go" {
		t.Errorf("fitList dropped names that fit: %q", got)
	}
	if got := fitList([]string{"internal/index/ingest.go", "cmd/deja/hook_context.go", "README.md"}, 48); got != "internal/index/ingest.go +2" {
		t.Errorf("fitList = %q, want the names that fit and a count of the rest", got)
	}
}

// A warning names its path and its command whole. When a path is so long
// that even the short form passes noteWidth, the line is allowed to run long
// rather than cut either: that is the one exception to the width.
func TestAWarningWithALongPathKeepsThePathAndTheCommand(t *testing.T) {
	const path = "/Volumes/Team Archive Drive 2026/shared/agent-history/caches/deja/index.db"
	for name, c := range map[string]struct{ note, command string }{
		"unmounted":  {unmountedIndexNote(path), "DEJA_INDEX_DIR"},
		"unwritable": {unwritableIndexNote(path), "`deja index`"},
		"stale":      {staleIndexNote(path), ""},
	} {
		if !strings.Contains(c.note, path) {
			t.Errorf("%s cut the path: %q", name, c.note)
		}
		if !strings.Contains(c.note, c.command) {
			t.Errorf("%s lost its command %s: %q", name, c.command, c.note)
		}
		if strings.Contains(c.note, "\n") {
			t.Errorf("%s is more than one line: %q", name, c.note)
		}
	}
	// With a short path the long form is kept, prose and all.
	if got := unmountedIndexNote("/Volumes/ext"); !strings.Contains(got, "reconnect it") {
		t.Errorf("a short path still lost the prose: %q", got)
	}
}

// The re-used session's name is the part of the receipt only it says (#579),
// so on a day with a tally it is the tally that goes, not the name.
func TestReceiptKeepsTheReusedNameOverTheTally(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	for range 3 {
		usage.RecordResult(dir, usage.KindHook, 900, 2, false)
	}
	svc := serviceReceipt(dir)
	if svc == "" {
		t.Fatal("no tally on a day with three injections, so the test proves nothing")
	}
	title := "how should the zorbit queue drain on deploy day"
	got := receiptLine("deja recalled 3 prior sessions from this project", reusedMemory{Title: title, Times: 4}, svc)
	if !strings.Contains(got, "most re-used: \"how should the zorbit") || !strings.HasSuffix(got, " 4×") {
		t.Errorf("the re-used name did not survive: %q", got)
	}
	checkPersonLines(t, "receipt", got, 1)
	// Without a re-used session the tally is what follows the claim.
	if got := receiptLine("deja recalled 3 prior sessions from this project", reusedMemory{}, svc); !strings.HasSuffix(got, svc) {
		t.Errorf("the tally was dropped with room for it: %q", got)
	}
}

// The npm package and the extension `deja install pi-auto` writes are the same
// integration reaching pi two ways, and they drifted: the package had
// session_start and before_agent_start only, so anyone who installed from npm
// got no repair after a failed command, no read-before-edit line, and nothing
// back after a compaction — features that had shipped months earlier (#3180).
func TestThePiPackageWiresTheSameEventsAsTheInstaller(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "extensions", "pi", "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	pkg := string(b)
	installer := piExtensionTS("/usr/local/bin/deja")

	for _, event := range piEventsIn(installer) {
		if !strings.Contains(pkg, `pi.on("`+event+`"`) {
			t.Errorf("the package does not handle %q, which the installer's extension does", event)
		}
	}
	// And nothing the installer does not: a handler only the package has is
	// the same drift facing the other way.
	for _, event := range piEventsIn(pkg) {
		if !strings.Contains(installer, `pi.on("`+event+`"`) {
			t.Errorf("the package handles %q and the installer's extension does not", event)
		}
	}
}

func piEventsIn(src string) []string {
	var out []string
	for _, part := range strings.Split(src, `pi.on("`)[1:] {
		if i := strings.IndexByte(part, '"'); i > 0 {
			out = append(out, part[:i])
		}
	}
	return out
}

// The npm package is what pi users install, and the pi-coding-agent Kimchi and
// Senpi ship has powershell beside bash; the installer's extension took a
// failed PowerShell run and the package still stopped at bash (#4523).
func TestThePiPackageRepairsAFailedPowerShellRunLikeTheInstaller(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "extensions", "pi", "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`event.toolName !== "powershell"`, "tool_name: event.toolName"} {
		if !strings.Contains(piExtensionTS("/bin/deja"), want) {
			t.Fatalf("the installer's extension lost %q", want)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("the package's failure hook lacks %q, which the installer's has", want)
		}
	}
}

// pi stores the recall a prompt was given in the session and sends it to the
// model again on a resume, and a fork copies its source's. Both pi
// extensions asked for a digest once per process, so a resumed session got a
// second copy, and a fork a third that led with its source. Driven the way pi
// drives them: default(pi), then before_agent_start with a session manager
// whose branch and header are a resume's and a fork's, against a stub deja
// that logs what it was asked.
func TestPiResumeAndForkGetNoSecondDigest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	if err = exec.CommandContext(t.Context(), node, "--experimental-strip-types", "-e", "").Run(); err != nil {
		t.Skip("this node cannot strip types")
	}
	pkg, err := os.ReadFile(filepath.Join("..", "..", "extensions", "pi", "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	lib, err := os.ReadFile(filepath.Join("..", "..", "extensions", "pi", "lib.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	stub := filepath.Join(dir, "deja")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"+
		`in=$(cat); printf '%s %s\n' "$1" "$in" >> '`+log+`'; case "$1" in hook-context) echo '{"hookSpecificOutput":{"additionalContext":"<deja-recall>digest</deja-recall>"}}';; hook-prompt) echo '{}';; esac`+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The source's file, named the way pi names it, with its id on the header.
	src := filepath.Join(dir, "2026-10-17T22-49-43-673Z_src-1.jsonl")
	if err := os.WriteFile(src, []byte(`{"type":"session","version":3,"id":"src-1","cwd":"/w"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gen := filepath.Join(dir, "gen", "deja.ts")
	npm := filepath.Join(dir, "npm", "index.ts")
	for path, body := range map[string]string{
		gen:                                  piExtensionTS(stub),
		npm:                                  string(pkg),
		filepath.Join(dir, "npm", "lib.mjs"): string(lib),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	drive := filepath.Join(dir, "drive.mjs")
	if err := os.WriteFile(drive, []byte(`
const mod = await import(process.argv[2]);
const handlers = {};
const pi = { on(n, f) { (handlers[n] ||= []).push(f); }, registerCommand() {} };
mod.default(pi);
const ui = { notify() {}, setStatus() {} };
const held = [{ type: "message" }, { type: "custom_message", customType: "deja-recall", content: "old" }, { type: "message" }];
const sm = (id, branch, header) => ({ getSessionId: () => id, getSessionFile: () => "", getBranch: () => branch, getHeader: () => header });
async function prompt(m) {
  let out = "";
  for (const fn of handlers.before_agent_start || []) {
    const r = await fn({ prompt: "p" }, { ui, sessionManager: m });
    if (r && r.message) out += r.message.content;
  }
  return out;
}
const tag = (s) => (s.includes("digest") ? "digest" : "none");
console.log("new", tag(await prompt(sm("new-1", [], { id: "new-1" }))));
console.log("resume", tag(await prompt(sm("res-1", held, { id: "res-1" }))));
console.log("fork", tag(await prompt(sm("fork-1", held, { id: "fork-1", parentSession: process.argv[3] }))));
console.log("compacted", tag(await prompt(sm("cmp-1", held.concat([{ type: "compaction" }]), { id: "cmp-1" }))));
// pi sends the entries a compaction kept, firstKeptEntryId on.
const kept = [{ type: "message", id: "e1" }, { type: "custom_message", customType: "deja-recall", id: "e2" }, { type: "compaction", firstKeptEntryId: "e1" }];
console.log("kept", tag(await prompt(sm("kept-1", kept, { id: "kept-1" }))));
// A fork at the first message copies nothing: its source is no part of it.
console.log("empty fork", tag(await prompt(sm("efork-1", [], { id: "efork-1", parentSession: process.argv[3] }))));
// A compaction that kept a prompt's recall but not the marked digest: pi
// sends no digest, so the resume needs one (#5314).
const digestKept = [{ type: "custom_message", customType: "deja-recall", id: "a1", details: { digest: true } }, { type: "message", id: "a2" }, { type: "custom_message", customType: "deja-recall", id: "a3" }, { type: "message", id: "a4" }, { type: "compaction", firstKeptEntryId: "a3" }];
console.log("recall kept", tag(await prompt(sm("rk-1", digestKept, { id: "rk-1" }))));
// And the digest the extension sends is marked, so the next resume can tell.
let sent = null;
for (const fn of handlers.before_agent_start || []) {
  const r = await fn({ prompt: "p" }, { ui, sessionManager: sm("mark-1", [], { id: "mark-1" }) });
  if (r && r.message) sent = r.message;
}
console.log("marked", sent && sent.details && sent.details.digest === true ? "yes" : "no");
`), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, ext := range map[string]string{"pi-auto": gen, "pi package": npm} {
		_ = os.Remove(log)
		cmd := exec.CommandContext(t.Context(), node, "--experimental-strip-types", "--no-warnings", drive, ext, src)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "DEJA_BIN="+stub, "HOME="+dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: node: %v\n%s", name, err, out)
		}
		if got, want := strings.TrimSpace(string(out)), "new digest\nresume none\nfork none\ncompacted digest\nkept none\nempty fork digest\nrecall kept digest\nmarked yes"; got != want {
			t.Errorf("%s: digests got:\n%s\nwant:\n%s", name, got, want)
		}
		calls, _ := os.ReadFile(log)
		var forkPrompt string
		for line := range strings.SplitSeq(string(calls), "\n") {
			if strings.HasPrefix(line, "hook-context") && (strings.Contains(line, `"res-1"`) || strings.Contains(line, `"fork-1"`)) {
				t.Errorf("%s: asked for a start digest for a session that holds one: %s", name, line)
			}
			if strings.HasPrefix(line, "hook-prompt") && strings.Contains(line, `"fork-1"`) {
				forkPrompt = line
			}
			if strings.Contains(line, `"efork-1"`) && strings.Contains(line, "src-1") {
				t.Errorf("%s: a fork that copied nothing left its source out: %s", name, line)
			}
		}
		if !strings.Contains(forkPrompt, `"parent_session_id":"src-1"`) {
			t.Errorf("%s: the fork's prompt recall does not name the session it was forked from: %q", name, forkPrompt)
		}
	}
}

// A test that captures output through os.Pipe has to drain it while the code
// under test writes, not after it returns. A pipe holds one buffer — 64 KB on
// Linux, 4 KB on Windows — and a writer that fills it blocks until somebody
// reads, so a capture that reads afterwards deadlocks the moment the output
// grows past that. It did: the windows leg hung when the harness table crossed
// 4096 bytes, and it hung on main rather than on the pull request, because a
// change that grows printed output is not one anybody labels `windows` (#3493,
// #3504, #3505).
//
// The rule is mechanical, so it is checked mechanically: where a test hands the
// write end of a pipe to the code under test, the read of the other end has to
// happen off the calling path — in a goroutine, or through a helper that starts
// one. That is what every capture helper here already does.
func TestNoTestReadsAPipeAfterTheCallThatFillsIt(t *testing.T) {
	files := parseTestFiles(t, ".", filepath.Join("..", "..", "internal"))
	if len(files) == 0 {
		t.Fatal("no test files were checked, so the rule was not applied to anything")
	}
	drainers := drainingHelpers(files)
	for path, parsed := range files {
		checkPipeReads(t, path, parsed.fset, parsed.file, drainers)
	}
}

type parsedFile struct {
	fset *token.FileSet
	file *ast.File
}

func parseTestFiles(t *testing.T, roots ...string) map[string]parsedFile {
	t.Helper()
	out := map[string]parsedFile{}
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			// Other tests build and remove scratch trees while this walks.
			if err != nil {
				return nil //nolint:nilerr // a file that vanished mid-walk is not this test's business
			}
			if info.IsDir() {
				if strings.HasPrefix(info.Name(), ".") && info.Name() != "." && info.Name() != ".." {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Fatalf("%s: %v", path, perr)
			}
			out[path] = parsedFile{fset: fset, file: file}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// drainingHelpers names the functions that take a pipe and read it in a
// goroutine — handing a reader to one of those is the safe shape, not a read on
// the calling path.
func drainingHelpers(files map[string]parsedFile) map[string]bool {
	out := map[string]bool{}
	for _, parsed := range files {
		ast.Inspect(parsed.file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Type.Params == nil {
				return true
			}
			params := map[string]bool{}
			for _, p := range fn.Type.Params.List {
				for _, name := range p.Names {
					params[name.Name] = true
				}
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				goStmt, ok := n.(*ast.GoStmt)
				if !ok {
					return true
				}
				ast.Inspect(goStmt.Call, func(n ast.Node) bool {
					if ident, ok := n.(*ast.Ident); ok && params[ident.Name] {
						out[fn.Name.Name] = true
					}
					return true
				})
				return false
			})
			return true
		})
	}
	return out
}

func checkPipeReads(t *testing.T, _ string, fset *token.FileSet, file *ast.File, drainers map[string]bool) {
	t.Helper()
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		reader, writer := pipeEnds(fn.Body)
		if reader == "" || writer == "" {
			return true
		}
		// A pipe the test fills itself and closes before the call is standing
		// in for a file, not capturing output: it cannot block, because the
		// writing is the test's own and it has already happened. The rule is
		// about the other shape — the write end handed to the code under test.
		if !handsOver(fn.Body, writer) {
			return true
		}
		var offending token.Pos
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.GoStmt:
				// Everything the goroutine does is off the calling path, which
				// is where a read of this pipe belongs.
				return false
			case *ast.CallExpr:
				if ident, ok := node.Fun.(*ast.Ident); ok && drainers[ident.Name] {
					return false
				}
				if readsFrom(node, reader) && offending == 0 {
					offending = node.Pos()
				}
			}
			return true
		})
		if offending != 0 {
			t.Errorf("%s: %s reads %s on the calling path — drain it in a goroutine or hand it to a helper that does, or the write blocks once it fills the buffer",
				fset.Position(offending), fn.Name.Name, reader)
		}
		return true
	})
}

// pipeEnds returns the variables the function assigned the two ends of an
// os.Pipe to, or empty strings when it makes no pipe.
func pipeEnds(body *ast.BlockStmt) (reader, writer string) {
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) < 2 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok || !isSelector(call.Fun, "os", "Pipe") {
			return true
		}
		if ident, ok := assign.Lhs[0].(*ast.Ident); ok && ident.Name != "_" {
			reader = ident.Name
		}
		if ident, ok := assign.Lhs[1].(*ast.Ident); ok && ident.Name != "_" {
			writer = ident.Name
		}
		return false
	})
	return reader, writer
}

// handsOver reports whether the write end leaves the test: put on os.Stdout or
// os.Stderr, or passed to something that will write to it. Writing to it and
// closing it in the test itself is not handing it over.
func handsOver(body *ast.BlockStmt, writer string) bool {
	handed := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if isSelector(lhs, "os", "Stdout") || isSelector(lhs, "os", "Stderr") {
					for _, rhs := range node.Rhs {
						if ident, ok := rhs.(*ast.Ident); ok && ident.Name == writer {
							handed = true
						}
					}
				}
			}
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok {
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == writer {
					// w.WriteString(...) and w.Close() are the test's own use.
					return true
				}
			}
			for _, arg := range node.Args {
				if ident, ok := arg.(*ast.Ident); ok && ident.Name == writer {
					handed = true
				}
			}
		}
		return true
	})
	return handed
}

// readsFrom reports whether the call takes the pipe's reader as what it reads:
// io.ReadAll(r), io.Copy(dst, r), buf.ReadFrom(r), bufio.NewScanner(r).
func readsFrom(call *ast.CallExpr, reader string) bool {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Close" {
		return false
	}
	for _, arg := range call.Args {
		if ident, ok := arg.(*ast.Ident); ok && ident.Name == reader {
			return true
		}
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == reader {
			// r.Read(...) and friends, on the reader itself.
			return sel.Sel.Name != "Close" && sel.Sel.Name != "Fd" && sel.Sel.Name != "Name"
		}
	}
	return false
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == pkg
}

// The bundle and the local installer have to hook the same events. Stating the
// list twice is how they drift: the manifest test named three while the
// installer wired four, so `PreToolUse` could have been dropped from
// plugin.json with every test still green — and plugin users would have lost
// the recall that fires before an edit or a command, silently.
//
// So the expectation is read out of the installer rather than written beside
// it: install into an empty home, see what it wrote, and require the manifest
// to carry the same events with the same matchers.
func TestPluginManifestHooksWhatTheInstallerHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))
	if _, err := installClaudeHook("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	settings, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("the installer wrote no settings: %v", err)
	}
	wired := hookMatchers(t, settings, "deja")
	if len(wired) < 4 {
		t.Fatalf("the installer wired %d events; this test exists because that "+
			"number changes: %v", len(wired), wired)
	}

	manifest := hookMatchers(t, repoFile(t, "claude-plugin/.claude-plugin/plugin.json"),
		"deja.sh")
	// Claude Code loads hooks/hooks.json beside the manifest's own; the events
	// an older Claude rejects live there (#4488).
	maps.Copy(manifest, hookMatchers(t, repoFile(t, "claude-plugin/hooks/hooks.json"), "deja.sh"))

	for _, event := range sortedKeys(wired) {
		got, ok := manifest[event]
		if !ok {
			t.Errorf("the installer hooks %s and the bundle does not, so a plugin "+
				"user gets less than someone who ran `deja install`", event)
			continue
		}
		// A hook with the wrong matcher fires on nothing and looks healthy in
		// every listing the user can see.
		if got != wired[event] {
			t.Errorf("%s matcher: bundle %q, installer %q", event, got, wired[event])
		}
	}
	for _, event := range sortedKeys(manifest) {
		if _, ok := wired[event]; !ok {
			t.Errorf("the bundle hooks %s and the installer does not; one of them "+
				"is wrong", event)
		}
	}
}

// hookMatchers reads event -> matcher for the entries whose command mentions
// want, so the user's own hooks in the same file are ignored.
func hookMatchers(t *testing.T, body []byte, want string) map[string]string {
	t.Helper()
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	out := map[string]string{}
	for event, groups := range doc.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if strings.Contains(h.Command, want) {
					out[event] = g.Matcher
				}
			}
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func repoFile(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	// Git may check these out with CRLF on Windows; the comparison is about
	// content, not about how the working tree stores it.
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// The Claude Code plugin is what `claude plugin install` pulls, so a typo in
// either manifest breaks the one-command install for everyone.
func TestClaudePluginManifestsAreWellFormed(t *testing.T) {
	var market struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(repoFile(t, ".claude-plugin/marketplace.json"), &market); err != nil {
		t.Fatalf("marketplace.json: %v", err)
	}
	if len(market.Plugins) != 1 || market.Plugins[0].Source != "./claude-plugin" {
		t.Fatalf("marketplace does not point at the plugin directory: %+v", market.Plugins)
	}
	var plugin struct {
		Name  string `json:"name"`
		Hooks map[string][]struct {
			Hooks []struct {
				Command       string `json:"command"`
				StatusMessage string `json:"statusMessage"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(repoFile(t, "claude-plugin/.claude-plugin/plugin.json"), &plugin); err != nil {
		t.Fatalf("plugin.json: %v", err)
	}
	if plugin.Name != market.Plugins[0].Name {
		t.Fatalf("plugin name %q does not match the marketplace entry %q", plugin.Name, market.Plugins[0].Name)
	}
	// Which events these are is checked against the installer itself in
	// TestPluginManifestHooksWhatTheInstallerHooks; restating the list here as
	// well is how the two drifted apart. This loop is about the shape of each
	// entry, so it walks whatever the manifest declares.
	for event := range plugin.Hooks {
		groups, ok := plugin.Hooks[event]
		if !ok || len(groups) == 0 || len(groups[0].Hooks) == 0 {
			t.Fatalf("plugin does not hook %s", event)
		}
		h := groups[0].Hooks[0]
		if !strings.HasPrefix(h.Command, "${CLAUDE_PLUGIN_ROOT}/hooks/deja.sh ") {
			t.Fatalf("%s command is not plugin-relative: %q", event, h.Command)
		}
		// Without this the pause before the first prompt is unexplained.
		if h.StatusMessage == "" {
			t.Fatalf("%s hook has no statusMessage", event)
		}
	}
}

// Five registries install this one bundle, and in four of them it is the only
// thing the user runs — so the recall tools have to come with it rather than
// waiting for a separate `deja install`.
func TestPluginBundleShipsTheMCPServer(t *testing.T) {
	var plugin struct {
		MCPServers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(repoFile(t, "claude-plugin/.claude-plugin/plugin.json"), &plugin); err != nil {
		t.Fatalf("plugin.json: %v", err)
	}
	server, ok := plugin.MCPServers["deja"]
	if !ok {
		t.Fatalf("bundle declares no MCP server: %+v", plugin.MCPServers)
	}
	// An absolute path from the author's machine, or a bare `deja` that is not
	// on the client's PATH, both register a server that never starts.
	if !strings.HasPrefix(server.Command, "${CLAUDE_PLUGIN_ROOT}/") {
		t.Fatalf("MCP command is not plugin-relative: %q", server.Command)
	}
	launcher := string(repoFile(t, "claude-plugin/mcp/deja-mcp.sh"))
	if !strings.Contains(launcher, "mcp") {
		t.Fatalf("launcher does not start the MCP server:\n%s", launcher)
	}
	// The hook bridge stands down when deja is installed locally; the MCP
	// launcher must not copy that, or the server exits at handshake.
	if strings.Contains(launcher, "settings.json") {
		t.Fatal("MCP launcher stands down like the hook bridge; clients dedupe by name instead")
	}
	if strings.Contains(launcher, "exit 0") {
		t.Fatal("launcher exits clean without a binary; the client would report a server that answers nothing")
	}
}

// The plugin ships its own copy of the slash command; it must not drift from
// the one `deja install` writes.
func TestPluginCommandMatchesInstaller(t *testing.T) {
	want := claudeCommandMD("deja")
	for _, p := range []string{"claude-plugin/commands/deja.md", "cursor-plugin/commands/deja.md"} {
		if got := string(repoFile(t, p)); got != want {
			t.Fatalf("%s has drifted from claudeCommandMD:\n--- file ---\n%s\n--- installer ---\n%s", p, got, want)
		}
	}
}

// A plugin user may not have the binary yet. The bridge must say so through
// the hook's own channel and still exit clean.
func TestPluginBridgeHandlesMissingBinary(t *testing.T) {
	script := string(repoFile(t, "claude-plugin/hooks/deja.sh"))
	for _, want := range []string{"systemMessage", "$HOME/.local/bin/deja", "exit 0", `exec "$DEJA"`} {
		if !strings.Contains(script, want) {
			t.Fatalf("bridge script missing %q", want)
		}
	}
}

// Users who already ran `deja install claude-auto` and then install the
// plugin must not get the digest twice. Whether it stands down is run, not
// read, in TestPluginBridgesStandDownBesideTheInstaller: a string check here
// held the grep that never matched what the installer writes (#4706).
func TestPluginBridgeStandsDownWhenLocallyInstalled(t *testing.T) {
	script := string(repoFile(t, "claude-plugin/hooks/deja.sh"))
	if !strings.Contains(script, "CLAUDE_CONFIG_DIR") {
		t.Fatal("bridge ignores CLAUDE_CONFIG_DIR when looking for settings.json")
	}
}

// runPluginBridge runs a plugin's hook bridge with a deja on PATH that only
// leaves a mark, and reports whether the bridge called it.
func runPluginBridge(t *testing.T, script string) bool {
	t.Helper()
	bin := t.TempDir()
	mark := filepath.Join(t.TempDir(), "ran")
	fake := "#!/bin/sh\ncat >/dev/null\n: > " + shellQuote(mark) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "deja"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "sh", filepath.Join("..", "..", script), "hook-context")
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	cmd.Stdin = strings.NewReader("{}")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v %s", script, err, out)
	}
	_, err := os.Stat(mark)
	return err == nil
}

// The plugins' bridges stand down when `deja install <h>-auto` already wrote
// the same hooks, or every hook runs twice. They looked for `deja hook-`, and
// the installer writes the launcher, `…/deja-hook hook-context`, so they never
// did (#4706).
func TestPluginBridgesStandDownBesideTheInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bridges are sh scripts")
	}
	for _, c := range []struct {
		script, target string
	}{
		{"codebuddy-plugin/hooks/deja.sh", "codebuddy-auto"},
		{"claude-plugin/hooks/deja.sh", "claude-auto"},
		{"devin-plugin/hooks/deja.sh", "devin-auto"},
		{"cursor-plugin/hooks/deja.sh", "cursor-auto"},
		{"antigravity-plugin/scripts/deja.sh", "antigravity-auto"},
	} {
		t.Run(c.target, func(t *testing.T) {
			hermeticEnv(t)
			if !runPluginBridge(t, c.script) {
				t.Fatal("with nothing installed the bridge did not run deja")
			}
			exe := filepath.Join(t.TempDir(), "deja")
			if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := installTarget(c.target, exe, false); err != nil {
				t.Fatal(err)
			}
			if runPluginBridge(t, c.script) {
				t.Fatalf("%s wired the hooks and the plugin ran them too", c.target)
			}
		})
	}
}

// pluginHooks is the hooks object a plugin bundle carries, in the shape
// Claude Code's settings use.
type pluginHooks map[string][]struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	} `json:"hooks"`
}

type pluginMCPServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// A plugin bundle is what a marketplace install gets instead of
// `deja install <harness>-auto`, so it has to carry the same wiring: the same
// events, in the same order, with the same matchers and hook subcommands; a
// server that starts; files the manifest points at that exist; and a hook
// bridge that stands down when the installer wired the harness already. Its
// version moves with the other plugins.
func TestPluginMatchesTheInstaller(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dir      string // the bundle, under the repo root
		manifest string // under dir
		// hooksFile holds the hooks object, under dir; empty when the
		// manifest carries it under "hooks".
		hooksFile string
		rootVar   string // what the bundle's commands name its root by
		timeout   int
		wiring    []struct{ Event, Sub, Matcher string }
		mcp       func(map[string]pluginMCPServer) bool
		skills    string // the manifest's skills field
		// bridge holds what hooks/deja.sh has to contain.
		bridge []string
		// files have to be regular files in the bundle; exec also executable.
		files, exec []string
	}{
		{
			name:     "codebuddy",
			dir:      "codebuddy-plugin",
			manifest: ".codebuddy-plugin/plugin.json",
			rootVar:  "CODEBUDDY_PLUGIN_ROOT",
			timeout:  codeBuddyHookTimeout,
			wiring:   codeBuddyHookWiring,
			mcp: func(s map[string]pluginMCPServer) bool {
				return s["deja"].Command == "${CODEBUDDY_PLUGIN_ROOT}/mcp/deja-mcp.sh"
			},
			skills: "./skills/",
			// It reads CodeBuddy's settings rather than Claude Code's.
			bridge: []string{`${CODEBUDDY_CONFIG_DIR:-$HOME/.codebuddy}/settings.json`},
			files:  []string{"skills/deja-history/SKILL.md", "hooks/deja.sh", "mcp/deja-mcp.sh"},
			exec:   []string{"hooks/deja.sh", "mcp/deja-mcp.sh"},
		},
		{
			name:      "devin",
			dir:       "devin-plugin",
			manifest:  ".devin-plugin/plugin.json",
			hooksFile: "hooks.json",
			rootVar:   "DEVIN_PLUGIN_ROOT",
			timeout:   60,
			wiring:    devinHookWiring,
			mcp: func(s map[string]pluginMCPServer) bool {
				d, ok := s["deja"]
				return ok && d.Command == "deja" && len(d.Args) == 1 && d.Args[0] == "mcp"
			},
			skills: "./skills/",
			// The guards the inline commands used to carry: stand down when
			// `deja install devin-auto` wired config.json already — every
			// session would otherwise get the digest twice — stay silent
			// without the binary, and say once, through the opening digest,
			// how to get it.
			bridge: []string{"config.json", "command -v deja", "systemMessage", "exec \"$DEJA\" \"$@\""},
			files: []string{
				"skills/deja-history/SKILL.md", "README.md", "LICENSE", "SECURITY.md",
				"hooks.json", "hooks/deja.sh",
			},
			exec: []string{"hooks/deja.sh"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m struct {
				Name       string                     `json:"name"`
				Version    string                     `json:"version"`
				Skills     string                     `json:"skills"`
				Hooks      pluginHooks                `json:"hooks"`
				MCPServers map[string]pluginMCPServer `json:"mcpServers"`
			}
			if err := json.Unmarshal(repoFile(t, tc.dir+"/"+tc.manifest), &m); err != nil {
				t.Fatal(err)
			}
			var claude struct {
				Version string `json:"version"`
			}
			if err := json.Unmarshal(repoFile(t, "claude-plugin/.claude-plugin/plugin.json"), &claude); err != nil {
				t.Fatal(err)
			}
			if m.Name != "deja-vu" || m.Version == "" || m.Version != claude.Version {
				t.Fatalf("name %q version %q, want deja-vu at %q", m.Name, m.Version, claude.Version)
			}
			if !tc.mcp(m.MCPServers) {
				t.Errorf("mcpServers = %+v", m.MCPServers)
			}
			if m.Skills != tc.skills {
				t.Errorf("skills field = %q, want %q", m.Skills, tc.skills)
			}

			hooks := m.Hooks
			if tc.hooksFile != "" {
				if len(hooks) != 0 {
					t.Errorf("manifest carries hooks of its own beside %s", tc.hooksFile)
				}
				if err := json.Unmarshal(repoFile(t, tc.dir+"/"+tc.hooksFile), &hooks); err != nil {
					t.Fatal(err)
				}
			}
			want := map[string][]int{}
			for i, w := range tc.wiring {
				want[w.Event] = append(want[w.Event], i)
			}
			bridge := `"${` + tc.rootVar + `}/hooks/deja.sh" `
			for event, idx := range want {
				entries := hooks[event]
				if len(entries) != len(idx) {
					t.Fatalf("%s: plugin wires %d entries, the installer %d", event, len(entries), len(idx))
				}
				for i, wi := range idx {
					w := tc.wiring[wi]
					e := entries[i]
					if e.Matcher != w.Matcher || len(e.Hooks) != 1 {
						t.Fatalf("%s[%d]: entry %+v, want matcher %q", event, i, e, w.Matcher)
					}
					h := e.Hooks[0]
					if h.Type != "command" || h.Timeout != tc.timeout {
						t.Fatalf("%s[%d]: hook %+v, want a command with timeout %d", event, i, h, tc.timeout)
					}
					if h.Command != bridge+w.Sub {
						t.Fatalf("%s[%d]: command %q does not bridge to deja %s", event, i, h.Command, w.Sub)
					}
				}
			}
			// An event the plugin still wires after the installer dropped it
			// would keep firing for marketplace installs only.
			for event := range hooks {
				if _, ok := want[event]; !ok {
					t.Errorf("plugin wires %s, which the installer never does", event)
				}
			}

			b := string(repoFile(t, tc.dir+"/hooks/deja.sh"))
			for _, s := range tc.bridge {
				if !strings.Contains(b, s) {
					t.Errorf("hooks/deja.sh is missing %q", s)
				}
			}
			root := filepath.Join("..", "..", tc.dir)
			for _, rel := range tc.files {
				info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
				if err != nil || !info.Mode().IsRegular() {
					t.Errorf("%s/%s: %v", tc.dir, rel, err)
				}
			}
			for _, rel := range tc.exec {
				info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
				if err != nil || (runtime.GOOS != "windows" && info.Mode()&0o111 == 0) {
					t.Errorf("%s/%s is not executable: %v", tc.dir, rel, err)
				}
			}
		})
	}
}

// "1 sessions" is the first line anyone sees from deja, and "1 session share"
// was mine from this audit (#737).
func TestPluralHelpers(t *testing.T) {
	for _, c := range []struct {
		n           int
		s, y, share string
	}{
		{0, "s", "ies", "share"},
		{1, "", "y", "shares"},
		{2, "s", "ies", "share"},
		{11, "s", "ies", "share"},
	} {
		if got := pluralS(c.n); got != c.s {
			t.Errorf("pluralS(%d) = %q, want %q", c.n, got, c.s)
		}
		if got := pluralY(c.n); got != c.y {
			t.Errorf("pluralY(%d) = %q, want %q", c.n, got, c.y)
		}
		if got := verbShare(c.n); got != c.share {
			t.Errorf("verbShare(%d) = %q, want %q", c.n, got, c.share)
		}
	}
}

// A policy file that will not parse warns on every command, because loading
// fails open and the rule someone wrote stops holding (#1088). A rule that
// parses and names something deja never consults fails open the same way — the
// project stays in every answer — and only `doctor` said so (#2452).
func TestARuleThatDoesNothingSaysSoWhereRecallHappens(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", path)

	// The shape someone writes when they mean "keep this project out": the
	// keys deja consults are origins, not projects.
	if err := os.WriteFile(path, []byte(`{"activations":{"search":{"work/secretclient":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	warnBrokenPolicy("search", &out)
	got := out.String()
	if !strings.Contains(got, "work/secretclient") {
		t.Errorf("the rule that does nothing is named nowhere but doctor:\n%q", got)
	}
	if !strings.Contains(got, "does nothing") && !strings.Contains(got, "no effect") {
		t.Errorf("the warning does not say the rule is inert:\n%q", got)
	}

	// A policy that says what deja understands says nothing extra.
	if err := os.WriteFile(path, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	warnBrokenPolicy("search", &out)
	if out.Len() != 0 {
		t.Errorf("a policy deja understands produced a warning:\n%q", out.String())
	}

	// And doctor keeps reporting it in full rather than being warned at.
	out.Reset()
	if err := os.WriteFile(path, []byte(`{"activations":{"search":{"work/secretclient":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	warnBrokenPolicy("doctor", &out)
	if out.Len() != 0 {
		t.Errorf("doctor was warned at about the file it prints in full:\n%q", out.String())
	}
}

// policyLeakIndex builds an index holding one imported (peer) session and
// points CLAUDE_PROJECT_DIR at a matching local project.
func policyLeakIndex(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	fresh := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-isoproj", "local.jsonl"), "local1", []string{
		`{"type":"user","sessionId":"local1","timestamp":"` + fresh + `","message":{"role":"user","content":"local work on the parser"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// A peer session arriving over sync.
	batch := t.TempDir()
	line := `{"harness":"claude","session_id":"peer1","project":"isoproj","role":"user","text":"authentication middleware panicked in handler_test.go ZQSECRETMARKER","time":"` + fresh + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(batch, "b.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Import(dir, batch); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "isoproj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)
	return dir
}

// DEJA_RECALL=off is documented as the kill switch; a warm cache must not
// keep injecting after it is set.
func TestHookCacheHonorsRecallOff(t *testing.T) {
	dir := policyLeakIndex(t)
	if d, _, _, _, _, _, _ := cachedHookDigest(dir); d == "" {
		t.Fatal("expected a digest before the kill switch")
	}
	t.Setenv("DEJA_RECALL", "off")
	if d, _, _, _, _, _, _ := cachedHookDigest(dir); d != "" {
		t.Fatalf("cache served a digest with DEJA_RECALL=off:\n%s", d)
	}
}

// A digest cached under one policy must not be served under another: the
// cache hit returns before the policy is ever consulted.
func TestHookCacheRefusesEntryFromAnotherPolicy(t *testing.T) {
	dir := policyLeakIndex(t)
	cwd := os.Getenv("CLAUDE_PROJECT_DIR")
	// Warm the cache under the permissive policy.
	warm, _, _, _, _, _, _ := cachedHookDigest(dir)
	if warm == "" {
		t.Fatal("expected a digest to cache")
	}
	// Plant a recognizable entry as if it had been built then.
	planted := "PLANTED-UNDER-OLD-POLICY"
	writeHookCache(dir, cwd, planted, 1, 10, nil, 0, nil, nil)
	if got, _, _, _, _, _, _ := cachedHookDigest(dir); got != planted {
		t.Fatalf("cache did not serve its own entry back: %q", got)
	}
	t.Setenv("DEJA_AUTORECALL_LOCAL_ONLY", "1")
	if got, _, _, _, _, _, _ := cachedHookDigest(dir); got == planted {
		t.Fatal("cache served an entry built under a policy that no longer applies")
	}
}

// The per-prompt déjà vu hook injects like any other path and must ask the
// policy too.
func TestPromptHookHonorsPolicy(t *testing.T) {
	dir := policyLeakIndex(t)
	t.Setenv("DEJA_AUTORECALL_LOCAL_ONLY", "1")
	in := strings.NewReader(`{"session_id":"s","cwd":"` + os.Getenv("CLAUDE_PROJECT_DIR") + `","prompt":"why did authentication middleware panic in handler_test.go"}`)
	var out strings.Builder
	if err := runHookPrompt(dir, in, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "ZQSECRETMARKER") {
		t.Fatalf("prompt hook injected policy-denied memory:\n%s", out.String())
	}
}

// blame carries whole sessions, so it is the path that most needs the rule.
func TestBlameHonorsPolicy(t *testing.T) {
	dir := policyLeakIndex(t)
	// Deny imported memory on every activation via a policy file.
	pol := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(pol, []byte(`{"activations":{"search":{"imported":false},"mcp":{"imported":false},"auto":{"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)
	got, _, err := blameTextResult(dir, search.BlameOptions{All: true}, "handler_test.go", 10)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "ZQSECRETMARKER") {
		t.Fatalf("blame served policy-denied memory:\n%s", got)
	}
}

// ctx is the command the hook tells an agent to call, and it answered from
// sessions every other reading surface withheld (#1026).
func TestCtxHonorsPolicy(t *testing.T) {
	dir := policyLeakIndex(t)
	pol := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(pol, []byte(`{"activations":{"search":{"imported":false},"mcp":{"imported":false},"auto":{"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)
	out := captureStdout(t, func() {
		if err := cmdCtx(dir, []string{"authentication", "middleware", "panicked"}); err == nil {
			t.Error("ctx answered from a session the policy denies")
		}
	})
	if strings.Contains(out, "ZQSECRETMARKER") {
		t.Fatalf("ctx served policy-denied memory:\n%s", out)
	}
	// The id-prefix branch takes its own path to the same session.
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for _, m := range metas {
		if strings.HasPrefix(m.Project, "imported:") && len(m.ID) >= 6 {
			id = m.ID
		}
	}
	if id == "" {
		t.Fatal("no imported session with an id long enough for the prefix branch")
	}
	out = captureStdout(t, func() {
		if err := cmdCtx(dir, []string{id}); err == nil {
			t.Error("ctx by id answered from a session the policy denies")
		}
	})
	if strings.Contains(out, "ZQSECRETMARKER") {
		t.Fatalf("ctx by id served policy-denied memory:\n%s", out)
	}
}

// With no home directory there is no policy file to point at, and the lines
// that pointed at one ended in "see " and nothing — including doctor, which is
// the screen somebody opens to find out where the file goes (#2785).
func TestTheLinesAboutThePolicyDoNotPointAtNothing(t *testing.T) {
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "DEJA_POLICY_FILE"} {
		t.Setenv(k, "")
	}
	if got := seePolicyFile(); got != "" {
		t.Errorf("a sentence still points at a file: %q", got)
	}
	line := noPolicyFileLine()
	if strings.HasSuffix(strings.TrimSpace(line), "at") || strings.Contains(line, "at  ") {
		t.Errorf("doctor names nowhere as somewhere: %q", line)
	}
	if !strings.Contains(line, "home directory") {
		t.Errorf("doctor does not say why there is no file: %q", line)
	}
}

// With one, both still name it.
func TestTheLinesAboutThePolicyNameItWhenThereIsOne(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_POLICY_FILE", dir+"/policy.json")
	if got := seePolicyFile(); !strings.Contains(got, "policy.json") {
		t.Errorf("the sentence lost the path: %q", got)
	}
	if got := noPolicyFileLine(); !strings.Contains(got, "policy.json") {
		t.Errorf("doctor lost the path: %q", got)
	}
}

// A trust policy that does not load withholds nothing, and the only place that
// said so was doctor: on the paths that actually answer, the guard went off in
// silence (#1088).
func TestSearchWarnsWhenTheTrustPolicyDoesNotLoad(t *testing.T) {
	hermeticEnv(t)
	pol := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("DEJA_POLICY_FILE", pol)
	if err := os.WriteFile(pol, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	errOut, err := captureRunStderr(t, "capstan pawl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, pol) {
		t.Errorf("the warning does not name the policy file:\n%s", errOut)
	}
	if !strings.Contains(errOut, "not valid JSON") {
		t.Errorf("the warning does not name the cause:\n%s", errOut)
	}
	if !strings.Contains(errOut, "every origin activates") {
		t.Errorf("the warning does not say what is in force:\n%s", errOut)
	}
}

func TestSearchIsQuietWhenThePolicyLoadsOrIsAbsent(t *testing.T) {
	hermeticEnv(t)
	pol := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("DEJA_POLICY_FILE", pol)

	errOut, err := captureRunStderr(t, "capstan pawl")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut, "trust policy") {
		t.Errorf("no policy file at all still warned:\n%s", errOut)
	}

	if err = os.WriteFile(pol, []byte(`{"activations":{"search":{"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	errOut, err = captureRunStderr(t, "capstan pawl")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut, "trust policy") {
		t.Errorf("a policy that loads still warned:\n%s", errOut)
	}
}

func TestPolicyWarningNamesPermissionsRatherThanTheSyscall(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny reads here")
	}
	hermeticEnv(t)
	pol := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("DEJA_POLICY_FILE", pol)
	if err := os.WriteFile(pol, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(pol, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(pol, 0o600) })

	errOut, err := captureRunStderr(t, "capstan pawl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "check its permissions") {
		t.Errorf("the warning does not say what to change:\n%s", errOut)
	}
	if strings.Contains(errOut, "open ") {
		t.Errorf("the warning passes the syscall through:\n%s", errOut)
	}
}

// "Nothing matched" and "a rule hides it on this path" are different facts,
// and only the second is something the reader can act on (#680).
func TestPolicyHiddenNoteNamesThePolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", path)
	if err := os.WriteFile(path, []byte(`{"activations":{"search":{"local":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	note := policyHiddenNote(policy.ActivationSearch, 3)
	for _, want := range []string{"3 matching sessions", "search", "deny local", path} {
		if !strings.Contains(note, want) {
			t.Errorf("note %q does not mention %q", note, want)
		}
	}
	// Nothing hidden, nothing to say: a line on every empty result is noise.
	if got := policyHiddenNote(policy.ActivationSearch, 0); got != "" {
		t.Errorf("said %q with nothing hidden", got)
	}
}

// An agent told "no prior sessions matched" concludes the work was never done
// and starts over.
func TestEmptyRecallAnswerNamesThePolicy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(dir, "policy.json"))
	if err := os.WriteFile(filepath.Join(dir, "policy.json"), []byte(`{"activations":{"mcp":{"local":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	hidden := emptyRecallAnswerPolicy(dir, "connection pool", 2)
	for _, want := range []string{"2 prior sessions matched", "trust policy", "not being shown"} {
		if !strings.Contains(hidden, want) {
			t.Errorf("answer %q does not mention %q", hidden, want)
		}
	}
	if plain := emptyRecallAnswerPolicy(dir, "moria", 0); !strings.Contains(plain, "No prior deja sessions matched") {
		t.Errorf("an ordinary miss said %q", plain)
	}
}

func TestPolicyFilterHitsCounts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", path)
	if err := os.WriteFile(path, []byte(`{"activations":{"search":{"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	hits := []search.Hit{
		{Session: model.Session{Project: "local-one"}},
		{Session: model.Session{Project: "imported:peer/p"}},
		{Session: model.Session{Project: "imported:other/p"}},
	}
	kept, hidden := policyFilterHitsCounted(policy.ActivationSearch, hits)
	if len(kept) != 1 || hidden != 2 {
		t.Fatalf("kept %d hidden %d, want 1 and 2", len(kept), hidden)
	}
	if kept[0].Session.Project != "local-one" {
		t.Errorf("kept the wrong one: %q", kept[0].Session.Project)
	}
}

// seedFileHistory gives /work/alpha/config.go six sessions and a decision, so
// the file line has something to say.
func seedFileHistory(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 6 {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"Decision: config.go must load the ARENAGUARD sentinel before any read."},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
}

// The hosts whose only channel at a tool is its result send the file line's
// event after the tool, in their own payloads, recorded on their stands. The
// answer names the event a host checks it against: PostToolUse.
func TestFileLineAfterTheToolInEachHostsPayload(t *testing.T) {
	seedFileHistory(t)
	for _, tc := range []struct{ host, payload string }{
		// gemini-cli 0.60.0, AfterTool on read_file
		{"gemini read_file", `{"session_id":"g1","cwd":"/work/alpha","hook_event_name":"AfterTool","tool_name":"read_file","tool_input":{"file_path":"/work/alpha/config.go"},"tool_response":{"llmContent":"package app\n","returnDisplay":""}}`},
		{"gemini replace", `{"session_id":"g2","cwd":"/work/alpha","hook_event_name":"AfterTool","tool_name":"replace","tool_input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}`},
		// qwen-code 0.20.0, PostToolUse on read_file
		{"qwen read_file", `{"session_id":"q1","cwd":"/work/alpha","hook_event_name":"PostToolUse","permission_mode":"yolo","tool_name":"read_file","tool_input":{"file_path":"/work/alpha/config.go"},"tool_response":{"llmContent":"package app\n"}}`},
		// CodeBuddy's Claude-shaped PostToolUse after Read
		{"codebuddy Read", `{"session_id":"c1","cwd":"/work/alpha","hook_event_name":"PostToolUse","tool_name":"Read","tool_input":{"file_path":"/work/alpha/config.go"}}`},
	} {
		t.Run(tc.host, func(t *testing.T) {
			out := toolHookRun(t, tc.payload)
			var resp sessionStartHookResponse
			if err := json.Unmarshal([]byte(out), &resp); err != nil {
				t.Fatalf("not the hook envelope: %v (%q)", err, out)
			}
			if resp.HookSpecificOutput.HookEventName != "PostToolUse" {
				t.Errorf("event %q, want PostToolUse", resp.HookSpecificOutput.HookEventName)
			}
			if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "ARENAGUARD") {
				t.Errorf("no file line: %q", resp.HookSpecificOutput.AdditionalContext)
			}
		})
	}
	// Claude Code's Read before the tool stays silent: it has the edit to
	// speak at.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"/work/alpha/config.go"},"session_id":"cc","cwd":"/work/alpha"}`); got != "" {
		t.Errorf("Claude Code's Read spoke: %q", got)
	}
	// Grok 1.0.41 hands read_file's PreToolUse context to the model, and names
	// the file under target_file.
	grok := `{"hookEventName":"pre_tool_use","sessionId":"gk","cwd":"/work/alpha","toolName":"read_file","toolInput":{"target_file":"/work/alpha/config.go"},"hook_event_name":"PreToolUse","session_id":"gk","tool_name":"read_file","tool_input":{"target_file":"/work/alpha/config.go"}}`
	if got := toolHookRun(t, grok); !strings.Contains(got, "ARENAGUARD") || !strings.Contains(got, `"PreToolUse"`) {
		t.Errorf("grok's read_file got %q", got)
	}
	// Once per session: the same file read again says nothing.
	if got := toolHookRun(t, `{"session_id":"g1","cwd":"/work/alpha","hook_event_name":"AfterTool","tool_name":"read_file","tool_input":{"file_path":"/work/alpha/config.go"}}`); got != "" {
		t.Errorf("the line repeated in the same session: %q", got)
	}
}

// TRAE CLI fires only PostToolUseFailure for a Bash that exits non-zero, with
// the output under error (traecli 0.207.1).
func TestTraeFailureEventCarriesTheFixPair(t *testing.T) {
	var pre, fail string
	for _, h := range traeHookWiring() {
		switch h.Event {
		case "PreToolUse":
			pre = h.Matcher
		case "PostToolUseFailure":
			fail = h.Sub + " " + h.Matcher
		}
	}
	if fail != "hook-tool-after Bash" {
		t.Fatalf("trae PostToolUseFailure = %q, want hook-tool-after on Bash", fail)
	}
	if pre != "Bash|apply_patch|Edit|Write" {
		t.Fatalf("trae PreToolUse matcher = %q", pre)
	}
	for _, h := range codexHookWiring {
		if h.Event == "PostToolUseFailure" {
			t.Fatal("codex's wiring changed with trae's: every codex user would have to trust the hook again")
		}
	}

	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	payload := `{"session_id":"01a115db-9529-72a0-9de5-a73952a2b106","cwd":"/work/app","hook_event_name":"PostToolUseFailure","model":"stub-model","permission_mode":"bypassPermissions","tool_name":"Bash","tool_input":{"command":"make"},"error":"Exit code 1\ngoroutine 1 [running]:\npanic: sql: database is closed","tool_use_id":"call_27ada812"}`
	var out strings.Builder
	if err := runHookToolAfter(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(out.String()), &resp); err != nil {
		t.Fatalf("no answer for trae's failure payload: %v (%q)", err, out.String())
	}
	if resp.HookSpecificOutput.HookEventName != "PostToolUseFailure" || !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "CGO_ENABLED=0") {
		t.Errorf("answer %+v", resp.HookSpecificOutput)
	}
}

// ZCode takes additionalContext from its tool events, so the file line is
// wired before Edit and Write and the fix pair after Bash, which fires
// PostToolUse with its exitCode when it fails (3.14.4).
func TestZCodeWiresTheToolEvents(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	if _, err := installZCodeHooks("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(zcodeConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks struct {
			Enabled bool `json:"enabled"`
			Events  map[string][]struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"events"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	for event, want := range map[string][2]string{
		"PreToolUse":  {"Bash|Edit|Write", "hook-tool"},
		"PostToolUse": {"Bash", "hook-tool-after"},
	} {
		es := root.Hooks.Events[event]
		if len(es) != 1 || es[0].Matcher != want[0] || !strings.HasSuffix(es[0].Hooks[0].Command, " "+want[1]) {
			t.Errorf("%s = %+v, want %s on %s", event, es, want[1], want[0])
		}
	}
	// A reinstall replaces deja's entries instead of stacking them, and an
	// uninstall takes them out.
	if _, err := installZCodeHooks("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(zcodeConfigPath())
	if !bytes.Equal(b2, b) {
		t.Errorf("a second install changed the file:\n%s", b2)
	}
	if _, err := installZCodeHooks("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(zcodeConfigPath()); strings.Contains(string(after), "hook-tool") {
		t.Errorf("uninstall left the tool hooks:\n%s", after)
	}
}

// An install over one from before the file line keeps qwen's old fix pair off
// PostToolUse and puts the file line there.
func TestQwenRetiresOnlyTheOldFixPair(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	settings := filepath.Join(home, ".qwen", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"hooks":{"PostToolUse":[{"matcher":"run_shell_command","hooks":[{"type":"command","command":"/bin/deja hook-tool-after","timeout":60000}]}]}}`
	if err := os.WriteFile(settings, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installQwenAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(settings)
	var root struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	post := root.Hooks["PostToolUse"]
	if len(post) != 1 || post[0].Matcher != "read_file|edit|write_file|mcp__deja__.*" || !strings.HasSuffix(post[0].Hooks[0].Command, " hook-tool") {
		t.Errorf("PostToolUse = %+v, want only hook-tool", post)
	}
}

// Grok's read_file goes through PreToolUse with the editors.
func TestGrokWiresTheReadBeforeTheEdit(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	t.Setenv("GROK_HOME", filepath.Join(home, ".grok"))
	if _, err := installGrokAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(grokHooksPath())
	var root struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	pre := root.Hooks["PreToolUse"]
	if len(pre) != 1 {
		t.Fatalf("PreToolUse = %+v", pre)
	}
	re := regexp.MustCompile("^(?:" + pre[0].Matcher + ")$")
	for _, tool := range []string{"Bash", "Read", "Edit", "Write", "Agent"} {
		if !re.MatchString(tool) {
			t.Errorf("grok PreToolUse matcher %q misses %s", pre[0].Matcher, tool)
		}
	}
}

// answeringDeja is a stand-in deja that records each run and answers
// hook-tool and hook-tool-after with a line naming the subcommand.
func answeringDeja(t *testing.T) (string, func() []pluginCall) {
	t.Helper()
	stub, calls := recordingDeja(t)
	b, err := os.ReadFile(stub)
	if err != nil {
		t.Fatal(err)
	}
	script := string(b) + "case \"$1\" in hook-tool|hook-tool-after) printf 'DEJA-LINE-%s' \"$1\";; esac\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub, calls
}

// toolCalls is the runs of one subcommand, with the tool it was asked about.
func toolCalls(calls []pluginCall, sub string) []map[string]any {
	var out []map[string]any
	for _, c := range calls {
		if strings.Fields(c.args)[0] == sub {
			out = append(out, c.input)
		}
	}
	return out
}

func inputPath(in map[string]any) string {
	ti, _ := in["tool_input"].(map[string]any)
	p, _ := ti["file_path"].(string)
	return p
}

// pi, Senpi and gjc share an extension; it now answers an edit and a write
// as it did a read. omp's edit names its file in the anchor its input opens
// with.
func TestPiFamilyFileLineAfterEditAndWrite(t *testing.T) {
	stub, calls := answeringDeja(t)
	out := runNodeDriver(t, "deja.ts", piExtensionTS(stub), `
const mod = await import("PLUGIN");
const handlers = {};
mod.default({ on(n, f) { (handlers[n] ||= []).push(f); }, registerCommand() {} });
const r = await handlers.tool_result[0]({ toolName: "edit", toolCallId: "c1", input: { path: "/w/config.go" }, content: [{ type: "text", text: "ok" }], isError: false });
console.log(JSON.stringify(r));
`)
	if !strings.Contains(out, "DEJA-LINE-hook-tool") {
		t.Errorf("pi's edit result was not given the file line: %s", out)
	}
	if got := toolCalls(calls(), "hook-tool"); len(got) != 1 || got[0]["tool_name"] != "edit" || inputPath(got[0]) != "/w/config.go" {
		t.Errorf("hook-tool asked %v", got)
	}

	stub, calls = answeringDeja(t)
	out = runNodeDriver(t, "index.js", ompExtensionJS(stub), `
const mod = await import("PLUGIN");
const handlers = {};
mod.default({ on(n, f) { (handlers[n] ||= []).push(f); }, registerCommand() {} });
const r = await handlers.tool_result[0]({ toolName: "edit", toolCallId: "c1", input: { input: "[/w/config.go#1A2B]\nreplace 3" }, content: [{ type: "text", text: "ok" }] });
console.log(JSON.stringify(r));
`)
	if !strings.Contains(out, "DEJA-LINE-hook-tool") {
		t.Errorf("omp's anchored edit was not given the file line: %s", out)
	}
	if got := toolCalls(calls(), "hook-tool"); len(got) != 1 || inputPath(got[0]) != "/w/config.go" {
		t.Errorf("hook-tool asked %v", got)
	}
}

// prime's model has one tool, ipython; files and commands are in its code
// and the exit code only in the BashResult text (prime-agent 0.9.8).
func TestPrimeFileAndFailureLinesFromTheCell(t *testing.T) {
	stub, calls := answeringDeja(t)
	out := runNodeDriver(t, "deja.ts", primeExtensionTS(stub), `
const mod = await import("PLUGIN");
const handlers = {};
mod.default({ on(n, f) { (handlers[n] ||= []).push(f); }, registerCommand() {} });
const read = await handlers.tool_result[0]({ toolName: "ipython", toolCallId: "c1", input: { code: "print(open('/w/config.go').read())" }, content: [{ type: "text", text: "package app" }], isError: false });
const fail = await handlers.tool_result[0]({ toolName: "ipython", toolCallId: "c2", input: { code: "r = await bash('make')" }, content: [{ type: "text", text: "BashResult(exit_code=3, output='fatal: ZORBLAX widget cache is stale\\n', duration=0.01)\n" }], isError: false });
const ok = await handlers.tool_result[0]({ toolName: "ipython", toolCallId: "c3", input: { code: "r = await bash('true')" }, content: [{ type: "text", text: "BashResult(exit_code=0, output='', duration=0.01)" }], isError: false });
console.log(JSON.stringify({ read, fail, ok }));
`)
	var got struct {
		Read any `json:"read"`
		Fail any `json:"fail"`
		Ok   any `json:"ok"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out[strings.LastIndex(strings.TrimSpace(out), "\n")+1:])), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !strings.Contains(out, "DEJA-LINE-hook-tool\"") || got.Ok != nil {
		t.Errorf("results %s", out)
	}
	after := toolCalls(calls(), "hook-tool-after")
	if len(after) != 1 || after[0]["tool_response"] != "fatal: ZORBLAX widget cache is stale\n\nExit Code: 3" {
		t.Errorf("hook-tool-after asked %v", after)
	}
	if files := toolCalls(calls(), "hook-tool"); len(files) != 1 || inputPath(files[0]) != "/w/config.go" {
		t.Errorf("hook-tool asked %v", files)
	}
}

// Cline CLI 3.0.69 hands beforeTool {toolCall:{toolName}, input} and puts the
// appendContext it returns beside the tool's result.
func TestClineBeforeToolGivesTheFileLine(t *testing.T) {
	stub, calls := answeringDeja(t)
	out := runNodeDriver(t, "index.js", clinePluginJS(stub), `
const mod = await import("PLUGIN");
const p = mod.default;
p.setup({ registerMessageBuilder() {}, registerRule() {}, registerCommand() {} }, { session: { sessionId: "1791367717716_ldkpz" }, workspaceInfo: { rootPath: "/w" } });
const read = await p.hooks.beforeTool({ snapshot: {}, tool: {}, toolCall: { toolName: "read_files" }, input: { files: [{ path: "/w/config.go" }] } });
const edit = await p.hooks.beforeTool({ snapshot: {}, tool: {}, toolCall: { toolName: "editor" }, input: { path: "/w/config.go", old_text: "a", new_text: "b" } });
const cmd = await p.hooks.beforeTool({ snapshot: {}, tool: {}, toolCall: { toolName: "run_commands" }, input: { commands: ["ls"] } });
console.log(JSON.stringify({ read, edit, cmd }));
`)
	if strings.Count(out, `"appendContext":"DEJA-LINE-hook-tool"`) != 2 || strings.Contains(out, `"cmd":{`) {
		t.Errorf("beforeTool answered %s", out)
	}
	got := toolCalls(calls(), "hook-tool")
	if len(got) != 2 || got[0]["tool_name"] != "read" || got[1]["tool_name"] != "edit" || got[0]["session_id"] != "1791367717716_ldkpz" || got[0]["cwd"] != "/w" {
		t.Errorf("hook-tool asked %v", got)
	}
}

// OpenClaw 2026.7's tool result middleware rewrites what the model reads
// back: the file line after read, edit and write, the fix pair after exec.
func TestOpenClawMiddlewareAddsTheLines(t *testing.T) {
	if !strings.Contains(openclawPluginManifest(), `"agentToolResultMiddleware": ["openclaw"]`) {
		t.Fatal("the manifest does not declare the middleware contract OpenClaw requires")
	}
	stub, calls := answeringDeja(t)
	out := runNodeDriver(t, "index.mjs", openclawPluginJS(stub), `
import plugin from "PLUGIN";
let mw, opts;
plugin.register({ on() {}, registerAgentToolResultMiddleware(fn, o) { mw = fn; opts = o; } });
const ctx = { runtime: "openclaw", sessionId: "7efce465", sessionKey: "agent:main:main" };
const read = await mw({ toolCallId: "c1", toolName: "read", args: { path: "/w/config.go" }, result: { content: [{ type: "text", text: "package app" }], details: {} } }, ctx);
const exec = await mw({ toolCallId: "c2", toolName: "exec", args: { command: "make" }, isError: true, result: { content: [{ type: "text", text: "fatal: ZORBLAX widget cache is stale\n\n(Command exited with code 3)" }] } }, ctx);
const other = await mw({ toolCallId: "c3", toolName: "web_fetch", args: { url: "x" }, result: { content: [] } }, ctx);
console.log(JSON.stringify({ read, exec, other, opts }));
`)
	if !strings.Contains(out, `"text":"DEJA-LINE-hook-tool"`) || !strings.Contains(out, `"text":"DEJA-LINE-hook-tool-after"`) || strings.Contains(out, `"other":{`) {
		t.Errorf("middleware answered %s", out)
	}
	if !strings.Contains(out, `"runtimes":["openclaw"]`) {
		t.Errorf("registered for %s", out)
	}
	if got := toolCalls(calls(), "hook-tool"); len(got) != 1 || inputPath(got[0]) != "/w/config.go" || got[0]["session_id"] != "7efce465" {
		t.Errorf("hook-tool asked %v", got)
	}
}

// Amp's tool.result {toolUseID, tool, input, status, output} takes
// {status, output} back.
func TestAmpToolResultGivesTheFileLine(t *testing.T) {
	stub, calls := answeringDeja(t)
	out := runNodeDriver(t, "deja.ts", ampPluginTS(stub), `
const mod = await import("PLUGIN");
const handlers = {};
mod.default({ on(n, f) { handlers[n] = f; }, registerCommand() {}, onDispose() {} });
const read = await handlers["tool.result"]({ toolUseID: "t1", tool: "Read", input: { path: "/w/config.go" }, status: "done", output: "package app", thread: { id: "T-1" } });
const again = await handlers["tool.result"]({ toolUseID: "t1", tool: "Read", input: { path: "/w/config.go" }, status: "done", output: "package app", thread: { id: "T-1" } });
console.log(JSON.stringify({ read, again }));
`)
	if strings.Count(out, `"output":"package app\n\nDEJA-LINE-hook-tool"`) != 2 {
		t.Errorf("tool.result answered %s", out)
	}
	if got := toolCalls(calls(), "hook-tool"); len(got) != 1 || got[0]["session_id"] != "T-1" {
		t.Errorf("hook-tool asked %v, want once for the call", got)
	}
}

// Hermes passes args to transform_tool_result; read_file, write_file and
// patch name the file there, and a patch in patch mode inside its text.
func TestHermesFileLineOnTransformToolResult(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	stub, calls := answeringDeja(t)
	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "plugin.py"), []byte(hermesPluginPy(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := "import importlib.util, json\n" +
		"spec = importlib.util.spec_from_file_location('dejahook', 'plugin.py')\n" +
		"m = importlib.util.module_from_spec(spec)\n" +
		"spec.loader.exec_module(m)\n" +
		"out = [m.repair(tool_name='read_file', args={'path': '/w/config.go'}, result='{\"content\": \"x\"}', session_id='h1'),\n" +
		"       m.repair(tool_name='patch', args={'mode': 'patch', 'patch': '*** Begin Patch\\n*** Update File: /w/config.go\\n@@\\n+x\\n*** End Patch'}, result='ok', session_id='h1'),\n" +
		"       m.repair(tool_name='web_search', args={'query': 'x'}, result='ok', session_id='h1')]\n" +
		"print(json.dumps(out))\n"
	cmd := exec.CommandContext(t.Context(), py, "-c", probe)
	cmd.Dir = root
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("plugin: %v\n%s", err, b)
	}
	var got []any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	var first string
	if len(got) == 3 {
		first, _ = got[0].(string)
	}
	if len(got) != 3 || !strings.HasSuffix(first, "\n\nDEJA-LINE-hook-tool") || got[1] == nil || got[2] != nil {
		t.Errorf("repair returned %v", got)
	}
	asked := toolCalls(calls(), "hook-tool")
	if len(asked) != 2 || asked[0]["tool_name"] != "read" || asked[1]["tool_name"] != "apply_patch" {
		t.Errorf("hook-tool asked %v", asked)
	}
}

// "nothing leaves the machine" is the claim this project is judged on, and the
// documents that make it have four network paths to account for: `deja
// update`, the doctor version check, `deja sync ssh` and `deja embed`. The
// security model has listed all four since it was written; three shorter
// documents said the absolute version instead — SECURITY.md, the two plugin
// security notes, the getting-started page and the MCPB store listing, each
// found on a different day.
//
// The rule is not "never say it": it is that wherever the sentence appears it
// either carries its exception or names what it is about. A privacy claim
// wider than the code is the one kind of wrong sentence that costs more than
// the feature it describes.
var (
	htmlTag   = regexp.MustCompile(`<[^>]*>`)
	whitespce = regexp.MustCompile(`\s+`)
)

// proseOf drops markup and line breaks so a sentence reads as a reader sees
// it: the claim on the front page carries its exception in an <em> on the next
// line, and a claim in a meta description has no neighbours at all.
func proseOf(text string) string {
	return whitespce.ReplaceAllString(htmlTag.ReplaceAllString(text, " "), " ")
}

// sentenceAround returns the sentence holding prose[from:to].
func sentenceAround(prose string, from, to int) string {
	stop := func(c byte) bool { return c == '.' || c == '!' || c == '?' || c == ';' }
	start := from
	for start > 0 && !stop(prose[start-1]) {
		start--
	}
	end := to
	for end < len(prose) && !stop(prose[end]) {
		end++
	}
	// The CJK full stop is three bytes, so it is matched as a string.
	s := prose[start:end]
	if i := strings.LastIndex(s[:from-start], "。"); i >= 0 {
		s = s[i+len("。"):]
	}
	if i := strings.Index(s, "。"); i >= 0 {
		s = s[:i]
	}
	return s
}

func TestEveryNothingLeavesClaimCarriesItsScope(t *testing.T) {
	root := filepath.Join("..", "..")
	// Three ways the same promise gets written. "nothing is sent anywhere" was
	// the one that slipped past a rule written for "leaves the machine" only.
	// The fourth shape is the shortest and outlived the others: "fully local",
	// which said the whole program where the sentence above it said one path.
	// It sat in the page footer on 103 pages and in the Chinese footer on 11,
	// where nobody re-reads anything.
	claim := regexp.MustCompile(`(?i)(nothing|nothing else|no data) (ever )?` +
		`(leaves (your|the|this) (machine|laptop|computer)` +
		`|is (ever )?(sent|uploaded)( anywhere| to anyone)?` +
		`|gets (sent|uploaded))` +
		`|(fully|entirely|completely|100%) local` +
		`|全部在本地|完全本地`)
	// What makes the sentence true: a stated exception, or a subject narrow
	// enough to be accurate on its own.
	scopes := []string{
		"unless you ask", "unless asked", "except", "exception",
		"no network path", "indexing and search", "index and its usage sidecar",
		"without being asked", "the file never leaves", "in your browser",
		"on your disk", "no server", "filereader",
		// Two sentences whose own subject is the scope: the semantic path with
		// no endpoint configured, and the read that parses a store where it
		// lies.
		"the semantic path is off", "in place",
	}
	var checked int
	err := filepath.WalkDir(filepath.Clean(root), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".claude", "fixtures", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".md", ".html", ".txt", ".json":
		default:
			return nil
		}
		if strings.Contains(p, "CHANGELOG.md") {
			return nil // a record of what was said, not a claim being made
		}
		b, err := os.ReadFile(p) //nolint:gosec // G122: test reads the repo's own checked-in files
		if err != nil {
			return err
		}
		text := proseOf(string(b))
		for _, m := range claim.FindAllStringIndex(text, -1) {
			checked++
			// The sentence it sits in, and only that: a neighbouring
			// sentence about something else passed this once — "recall
			// arrives without being asked", two lines below the claim in
			// docs/llms.txt, read as the exception the claim did not have.
			window := strings.ToLower(sentenceAround(text, m[0], m[1]))
			ok := false
			for _, s := range scopes {
				if strings.Contains(window, s) {
					ok = true
					break
				}
			}
			if !ok {
				rel, _ := filepath.Rel(root, p)
				t.Errorf("%s claims nothing leaves the machine with no exception and no subject: %q",
					filepath.ToSlash(rel), strings.TrimSpace(text[m[0]:m[1]]))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Low on purpose: most of these sentences now say what reaches the network
	// instead, so the ones left are the front page's and a handful in prose.
	// The guard is only here to catch the pattern rotting into matching
	// nothing at all.
	if checked < 2 {
		t.Fatalf("found %d such claims — the pattern stopped matching, so this checks nothing", checked)
	}
}

// "Search has no network path" is the scope the claim above leans on, and it
// stopped being true when semantic search arrived: with a sidecar built, every
// search and every MCP recall sends its query to the embedding endpoint. So a
// sentence saying search makes no network calls has to name that exception.
func TestNoNetworkSearchClaimNamesTheSemanticPath(t *testing.T) {
	root := filepath.Join("..", "..")
	claim := regexp.MustCompile(`(?i)(search (have|has) no network path|search make no network calls|no network path in indexing or search|search and every tool above are\s+local and make no network calls)`)
	var checked int
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".claude", "fixtures", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".md", ".html", ".txt", ".json":
		default:
			return nil
		}
		if strings.Contains(p, "CHANGELOG.md") {
			return nil
		}
		b, err := os.ReadFile(p) //nolint:gosec // G122: test reads the repo's own checked-in files
		if err != nil {
			return err
		}
		text := proseOf(string(b))
		for _, m := range claim.FindAllStringIndex(text, -1) {
			checked++
			s := strings.ToLower(sentenceAround(text, m[0], m[1]))
			if !strings.Contains(s, "semantic") && !strings.Contains(s, "embed") {
				rel, _ := filepath.Rel(root, p)
				t.Errorf("%s says search makes no network calls without the semantic exception: %q", filepath.ToSlash(rel), strings.TrimSpace(s))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 3 {
		t.Fatalf("found %d such claims — the pattern stopped matching", checked)
	}
}

// Every value below is synthetic.

func privacyLine(sid, role, ts, text string) string {
	b, err := json.Marshal(map[string]any{
		"type": role, "sessionId": sid, "timestamp": ts, "cwd": "/Users/alicehunt/src/app",
		"message": map[string]any{"role": role, "content": text},
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Colored tool output (grep --color, bat, a colored logger) puts escape
// sequences between a key and its value. Redaction saw no `NAME=value`, and
// every reader stripped the escapes and printed the value whole.
func TestColoredSecretIsMaskedOnEverySurface(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-Users-alicehunt-src-app")
	const val = "Qx7vR2mK9pL4sT8wZ3nB6yH1jF5dG0cA"
	const gh = "ghp_FAKEaaaaBBBBccccDDDDeeeeFFFFgggg1234" //nolint:gosec // G101: synthetic token the test masks
	writeClaudeFixture(t, filepath.Join(store, "ansi1.jsonl"), "ansi1", []string{
		privacyLine("ansi1", "user", "2026-05-01T10:00:00Z", "here is the env:\nDEPLOY_API_TOKEN\x1b[0m=\x1b[33m"+val+"\x1b[0m\nok"),
		privacyLine("ansi1", "assistant", "2026-05-01T10:01:00Z", "the token ghp_\x1b[1m"+gh[4:]+"\x1b[0m is loaded"),
	})
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"show", "ansi1"}, {"recall", "DEPLOY_API_TOKEN"}, {"recall", "token loaded"}} {
		out, err := captureRun(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.Contains(out, val) || strings.Contains(out, gh) {
			t.Errorf("%v printed a secret in the clear:\n%s", args, out)
		}
	}
	exp := filepath.Join(tmp, "exp")
	if _, err := index.ExportFull(dir, exp); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(exp, "*.jsonl"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), val) {
			t.Errorf("sync export carries the value: %s", b)
		}
	}
}

// share is for pasting to someone else, so it runs the second pass recap
// runs: the account name in a home path and an email do not go out.
func TestShareMasksHomePathAndEmail(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-Users-alicehunt-src-app")
	writeClaudeFixture(t, filepath.Join(store, "home1.jsonl"), "home1", []string{
		privacyLine("home1", "user", "2026-05-01T10:00:00Z", "the config is at /Users/alicehunt/src/app/config.yaml, ping alicehunt@example.org"),
		privacyLine("home1", "assistant", "2026-05-01T10:01:00Z", "fixed /Users/alicehunt/src/app/config.yaml, it now loads"),
	})
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "share", "home1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "alicehunt") {
		t.Errorf("share carries the account name / email:\n%s", out)
	}
}

// Profiling is off unless asked for, and asking for it must not be able to
// break the command: a bad path is a reason to say so on stderr and carry on
// indexing, not a reason to fail the run someone is trying to profile.
func TestProfilingWritesWhenAskedAndStaysOutOfTheWayOtherwise(t *testing.T) {
	dir := t.TempDir()

	t.Setenv("DEJA_CPUPROFILE", "")
	t.Setenv("DEJA_MEMPROFILE", "")
	stop := startProfiling()
	stop()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("profiling wrote %d files without being asked", len(entries))
	}

	cpu := filepath.Join(dir, "cpu.out")
	mem := filepath.Join(dir, "mem.out")
	t.Setenv("DEJA_CPUPROFILE", cpu)
	t.Setenv("DEJA_MEMPROFILE", mem)
	stop = startProfiling()
	// Something for the CPU profile to sample; an empty profile is still a
	// valid file, so the assertion below is only that both were written.
	sum := 0
	for i := range 200000 {
		sum += i % 7
	}
	_ = sum
	stop()
	for _, path := range []string{cpu, mem} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(path), err)
		}
		if fi.Size() == 0 {
			t.Fatalf("%s is empty", filepath.Base(path))
		}
	}

	// A directory that does not exist is a typo, not a crash.
	t.Setenv("DEJA_CPUPROFILE", filepath.Join(dir, "no", "such", "cpu.out"))
	t.Setenv("DEJA_MEMPROFILE", filepath.Join(dir, "no", "such", "mem.out"))
	stop = startProfiling()
	stop()
}

// The block can carry two sessions, each with its own quoted lines, and 1024
// bytes cut the second one short — taking the tail, which is where the paths,
// names and numbers live. In real-world stores, by replaying the sweep
// against what the agent actually answered next: blocks carrying words the
// answer went on to use rose from 20% to 25% when the budget grew to 1536, at
// 24 tokens a message, and 2048 bought nothing further.
func TestBudgetHoldsBothSessions(t *testing.T) {
	filler := strings.Repeat("pgbouncer pool ", 12)
	sessions := []model.Session{
		{
			Harness: "claude", ID: "first", Project: "proj",
			Messages: []model.Message{
				{Role: "user", Text: "что там с pgbouncer"},
				{Role: "assistant", Text: filler + "и держим 40 на шард"},
				{Role: "assistant", Text: "в итоге pgbouncer " + filler + "порог 0.7"},
			},
		},
		{
			Harness: "claude", ID: "second", Project: "proj",
			Messages: []model.Message{
				{Role: "user", Text: "pgbouncer снова"},
				{Role: "assistant", Text: filler + "и prepared statements выключены"},
				{Role: "assistant", Text: "в итоге pgbouncer " + filler + "режим transaction"},
			},
		},
	}
	got := search.AutoRecallDigestForAsked(sessions,
		promptHookBudget-recallFrameOverhead, []string{"pgbouncer"}, "что там с pgbouncer")
	if !strings.Contains(got, "second") {
		t.Fatalf("the second session did not fit at all:\n%s", got)
	}
	if !strings.Contains(got, "режим transaction") {
		t.Errorf("the budget cut the tail off the second session's conclusion:\n%s", got)
	}
}

// The README states a lookup latency twice: once in the banner and once in the
// performance table, which cites where each number comes from. They disagreed —
// the banner said "sub-millisecond lookups over 5 GB of history" while the table
// says the benchmark's ~0.4 ms is on a synthetic corpus and a real haystack is
// ~25 ms. In practice, an exact answer is 3.4–6.2 ms
// in process and the relevance tier 215 ms (#2608).
func TestTheReadmeBannerAgreesWithItsOwnTable(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	banner := ""
	for line := range strings.SplitSeq(text, "\n") {
		if strings.Contains(line, "hit@1") && strings.Contains(line, "lookups") {
			banner = line
			break
		}
	}
	if banner == "" {
		t.Fatal("the banner line is gone, so this measures nothing")
	}
	// The table is the sourced claim: whatever the banner says has to be
	// reachable from it.
	if !strings.Contains(text, "`deja bench recall`") {
		t.Fatal("the performance table no longer names its source")
	}
	if regexp.MustCompile(`(?i)sub-millisecond`).MatchString(banner) {
		t.Errorf("the banner promises a latency the table contradicts:\n  %s", strings.TrimSpace(banner))
	}
	// And it still says something about lookups rather than dropping the claim.
	if !strings.Contains(banner, "lookup") {
		t.Errorf("the banner stopped saying anything about lookups:\n  %s", strings.TrimSpace(banner))
	}
}
