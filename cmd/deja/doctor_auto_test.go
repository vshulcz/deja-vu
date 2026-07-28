package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doctor is where someone looks when recall goes quiet. A harness deja can
// wire for auto-recall but doctor does not report is a hole exactly there —
// this pins the two lists together so a new harness cannot open one.
func TestDoctorCoversEveryAutoTarget(t *testing.T) {
	covered := map[string]bool{
		// The two that predate the table and print their own lines.
		"claude": true, "codex": true,
	}
	for _, a := range autoWirings() {
		covered[a.name] = true
	}
	for _, target := range installTargetNames() {
		name, ok := strings.CutSuffix(target, "-auto")
		if !ok {
			continue
		}
		if !covered[name] {
			t.Fatalf("install target %q has no doctor row: add it to autoWirings()", target)
		}
	}
}

// A file that exists but no longer calls deja is the shape of every dead
// integration: the install looks done and nothing ever fires.
func TestDoctorReportsStaleWiring(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	path := filepath.Join(home, ".pi", "agent", "extensions", "deja.ts")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("// an old extension that calls nothing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	doctorAutoRecall(&buf)
	out := buf.String()
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, " pi ") {
			line = l
		}
	}
	if !strings.Contains(line, "stale") {
		t.Fatalf("an extension with no deja call is not reported stale:\n%s", out)
	}
	// Missing and stale must not read the same: one is "never installed",
	// the other "installed and broken", and they need different fixes.
	if !strings.Contains(out, "missing") {
		t.Fatalf("nothing reported missing in an otherwise empty home:\n%s", out)
	}
}

func TestDoctorReportsWiredWhenTheMarkerIsThere(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	if _, err := installPiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	var buf bytes.Buffer
	doctorAutoRecall(&buf)
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, " pi ") && !strings.Contains(l, "wired") {
			t.Fatalf("a fresh install is not reported wired: %q", l)
		}
	}
}

// Every target the installer accepts must be reachable from the shell too:
// the fish script had fallen seven harnesses behind before this was shared.
func TestCompletionListsEveryInstallTarget(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		out := captureStdout(t, func() {
			if err := runCompletion([]string{shell}); err != nil {
				t.Fatalf("%s: %v", shell, err)
			}
		})
		if strings.Contains(out, "%INSTALL_TARGETS%") {
			t.Fatalf("%s script still has the placeholder", shell)
		}
		for _, target := range []string{"cline-auto", "goose-auto", "hermes-auto", "roo", "aider", "openclaw-auto"} {
			if !strings.Contains(out, target) {
				t.Fatalf("%s completion never offers %q", shell, target)
			}
		}
	}
}

// Codex pins the hook file's hash when the user trusts it, and any reinstall
// that rewrites hooks.json invalidates the pin. The hook then stops running
// while `enabled = true` stays in the config — the state that looks healthiest
// and works least, and the one doctor used to call "wired".
func TestCodexHookTrustDetectsARewrittenFile(t *testing.T) {
	dir := t.TempDir()
	hooks := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(hooks, []byte(`{"hooks":{"session_start":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(`{"hooks":{"session_start":[]}}`))
	matching := "\ntrusted_hash = \"sha256:" + hex.EncodeToString(sum[:]) + "\"\nenabled = true\n"
	if !codexHookTrusted(hooks, matching) {
		t.Fatal("an untouched hook file is reported untrusted")
	}
	stale := "\ntrusted_hash = \"sha256:0000000000000000000000000000000000000000000000000000000000000000\"\nenabled = true\n"
	if codexHookTrusted(hooks, stale) {
		t.Fatal("a rewritten hook file is reported trusted")
	}
	// A config with no pin at all is a codex that never asked; saying
	// "untrusted" there would be a false alarm on every fresh install.
	if !codexHookTrusted(hooks, "\nenabled = true\n") {
		t.Fatal("absence of a pin reported as untrusted")
	}
}

// Goose and Hermes keep MCP servers in YAML and nest ours under a key, so a
// plain "deja appears in the file" check passes on a disabled or unrelated
// entry — these two probes exist for that and had no test.
func TestYAMLWiringProbes(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name  string
		body  string
		probe func(string) bool
		want  bool
	}{
		{"goose wired", "extensions:\n  deja:\n    enabled: true\n", doctorGooseWired, true},
		{"goose absent", "extensions:\n  memory:\n    enabled: true\n", doctorGooseWired, false},
		{"goose mentions deja elsewhere", "# installed by deja\nextensions:\n  memory:\n    enabled: true\n", doctorGooseWired, false},
		{"hermes wired", "mcp_servers:\n  deja:\n    command: /bin/deja\n", doctorHermesWired, true},
		{"hermes absent", "mcp_servers:\n  other:\n    command: /bin/other\n", doctorHermesWired, false},
		{"hermes plugin only", "plugins:\n  enabled:\n    - deja\n", doctorHermesWired, false},
	}
	for _, tc := range cases {
		path := filepath.Join(dir, tc.name+".yaml")
		if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := tc.probe(path); got != tc.want {
			t.Fatalf("%s: probe = %v, want %v", tc.name, got, tc.want)
		}
	}
	// A missing file is not wired, and must not panic.
	if doctorGooseWired(filepath.Join(dir, "absent.yaml")) || doctorHermesWired(filepath.Join(dir, "absent.yaml")) {
		t.Fatal("a missing config reported as wired")
	}
}
