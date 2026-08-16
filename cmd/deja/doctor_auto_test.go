package main

import (
	"bytes"
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

// Codex gates hooks behind a trust store of its own, keyed per hook in
// config.toml. Measured on codex 0.142.4: a home whose hooks.json is perfectly
// wired but which codex has never been shown runs no hook at all under
// `codex exec` — no marker — while the same run with
// --dangerously-bypass-hook-trust runs it. That is the state that looks
// healthiest and works least, and deja used to call it "wired".
//
// The pin itself is not checkable from here. Codex's trusted_hash is not the
// sha256 of the hook file, and deja comparing the two called every working
// install untrusted — on the machine this was written on, doctor said untrusted
// while codex was demonstrably delivering deja's recall to the model.
func TestCodexHookTrustSectionStopsAtTheNextTable(t *testing.T) {
	cfg := `[hooks.state."/h/hooks.json:session_start:0:0"]
trusted_hash = "sha256:abc"

[projects."/some/other"]
enabled = false
`
	got := codexHookTrustSection(cfg)
	if !strings.Contains(got, "trusted_hash") {
		t.Fatalf("the hook's own pin is missing from its section: %q", got)
	}
	if strings.Contains(got, "enabled = false") {
		t.Errorf("the section ran on into an unrelated table, whose enabled flag would decide our status: %q", got)
	}
	if codexHookTrustSection("[projects.\"/x\"]\nenabled = true\n") != "" {
		t.Error("a config that never mentions the hook reported a section for it")
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
