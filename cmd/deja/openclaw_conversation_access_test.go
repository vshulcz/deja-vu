package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

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
