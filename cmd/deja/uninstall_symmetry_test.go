package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUninstallTakesBackEveryFileItWrote installs every target in one home,
// uninstalls every target, and walks what is left.
//
// TestUninstallLeavesNoFileOrDirItCreated asks the same question of a handful
// of paths named in the test. Naming them is what hid #3684: a snapshot of
// deja's own statusline stayed beside ~/.claude/settings.json, naming the
// binary the uninstall had just orphaned, and no test looked there.
func TestUninstallTakesBackEveryFileItWrote(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if home == "" || !strings.Contains(home, "TestUninstallTakesBack") {
		t.Fatalf("refusing to install into %q", home)
	}
	targets := []string{}
	for _, name := range installTargetNames() {
		// The sync timer is the one target that reaches outside the home
		// directory — a launchd or systemd unit the platform then owns.
		if name == "sync-timer" {
			continue
		}
		targets = append(targets, name)
	}
	for _, target := range targets {
		if _, err := captureRun(t, "install", target, "--no-index"); err != nil {
			t.Fatalf("install %s: %v", target, err)
		}
	}
	// While every target is installed, the other sweeps over the same home:
	// every skill declares the name of its directory (#3700). Installing all
	// of them is the most expensive thing in this suite, so it is done once.
	skillNamesMatchTheirDirectories(t, home)
	// What deja says it created, before the uninstall wipes the record.
	made := append([]string(nil), readWiringState().Dirs...)
	if len(made) == 0 {
		t.Fatal("the record names no directory deja created, so there is nothing to check")
	}
	for _, target := range targets {
		if _, err := captureRun(t, "uninstall", target); err != nil {
			t.Fatalf("uninstall %s: %v", target, err)
		}
	}
	// A directory deja made goes when the last thing in it does. Thirty-three
	// were left on a bare home, from the harness roots down to a plugin
	// directory four levels deep (#3698).
	for _, dir := range made {
		if strings.Contains(dir, filepath.Join(".config", "deja")) {
			continue
		}
		if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
			t.Errorf("uninstall left the empty directory it created: %s", strings.TrimPrefix(dir, home))
		}
	}
	_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		// deja's own directory is not a harness config: the wiring record
		// lives there and is the binary's, not the harness's.
		if strings.Contains(p, filepath.Join(".config", "deja")) {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		if mentionsDeja(b) {
			t.Errorf("uninstall left %s, which still names deja: %s", p,
				strings.Join(strings.Fields(string(b)), " "))
		}
		return nil
	})
}

// TestInstallStatuslineRewritesOneWrittenByAnotherBuild: the status bar names
// the binary directly rather than the launcher, so a deja that moved leaves an
// entry running a file that is not there. The install used to call that entry
// the reader's and refuse it, printing a combine line that piped the missing
// binary into this one (#3684).
func TestInstallStatuslineRewritesOneWrittenByAnotherBuild(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := map[string]any{"statusLine": map[string]any{
		"type": "command", "command": filepath.Join(home, "gone", "deja") + " statusline", "refreshInterval": 1000,
	}}
	b, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, b, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "statusline", "--no-index")
	if err != nil {
		t.Fatalf("install statusline: %v", err)
	}
	if !strings.Contains(out, "replaced the deja statusline") {
		t.Errorf("install said nothing about the entry it replaced:\n%s", out)
	}
	cmd := statuslineCommand(t, settings)
	if strings.Contains(cmd, filepath.Join(home, "gone")) {
		t.Errorf("statusline still runs the binary that moved: %q", cmd)
	}
	if !strings.HasSuffix(cmd, " statusline") {
		t.Errorf("statusline command is not deja's: %q", cmd)
	}
	// And it comes out again, which the exact-match test could not do either.
	if _, err := captureRun(t, "uninstall", "statusline"); err != nil {
		t.Fatalf("uninstall statusline: %v", err)
	}
	if got := statuslineCommand(t, settings); got != "" {
		t.Errorf("uninstall left a statusline behind: %q", got)
	}
}

// A statusline the reader combined with their own is theirs: deja's half runs
// inside a line they wrote, and rewriting the entry would throw the other half
// away. `deja install statusline` used to answer such a line with the same
// error it answers a stranger's — offering to combine what was already
// combined, and naming both halves a second time.
func TestInstallStatuslineKeepsACombinedLine(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	combined := combinedStatusline("ccstatusline", filepath.Join(home, "bin", "deja"))
	b, err := json.Marshal(map[string]any{"statusLine": map[string]any{"type": "command", "command": combined}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "statusline", "--no-index"); err != nil {
		t.Fatalf("install statusline: %v", err)
	}
	if got := statuslineCommand(t, settings); got != combined {
		t.Errorf("install rewrote a line the reader built:\n got  %q\n want %q", got, combined)
	}
	// Not ours to remove either.
	if _, err := captureRun(t, "uninstall", "statusline"); err != nil {
		t.Fatalf("uninstall statusline: %v", err)
	}
	if got := statuslineCommand(t, settings); got != combined {
		t.Errorf("uninstall took out a line the reader built: %q", got)
	}
}

// Installing only the statusline used to record nothing at all — the record
// skipped the target, and with no target left it was never written — so the
// uninstall did not know it had created the file and left an empty one behind,
// beside a snapshot of deja's own wiring it reported as the reader's (#3684).
func TestUninstallStatuslineRemovesTheSettingsItCreated(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "statusline", "--no-index"); err != nil {
		t.Fatalf("install statusline: %v", err)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("install did not write %s: %v", settings, err)
	}
	out, err := captureRun(t, "uninstall", "statusline")
	if err != nil {
		t.Fatalf("uninstall statusline: %v", err)
	}
	if _, err := os.Stat(settings); err == nil {
		b, _ := os.ReadFile(settings)
		t.Errorf("uninstall left %s behind: %s", settings, strings.TrimSpace(string(b)))
	}
	if _, err := os.Stat(settings + ".bak"); err == nil {
		t.Errorf("uninstall left a snapshot of deja's own config at %s.bak", settings)
	}
	if strings.Contains(out, "kept 1 snapshot of configs you already had") {
		t.Errorf("uninstall called its own snapshot the reader's:\n%s", out)
	}
}

// The commands directory deja had to create goes with the last command in it.
// The writer made its own directory without recording it, so an empty
// ~/.claude/commands survived every uninstall on a machine that had no
// commands of its own (#3685).
func TestUninstallRemovesACommandsDirectoryItCreated(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-auto", "--no-index"); err != nil {
		t.Fatalf("install claude-auto: %v", err)
	}
	commands := filepath.Join(home, ".claude", "commands")
	if _, err := os.Stat(filepath.Join(commands, "deja.md")); err != nil {
		t.Fatalf("install did not write the command file: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "claude-auto"); err != nil {
		t.Fatalf("uninstall claude-auto: %v", err)
	}
	if _, err := os.Stat(commands); err == nil {
		t.Errorf("uninstall left %s behind", commands)
	}
}

// The prune walks upward through directories deja made (#3698), so the other
// half of the rule has to hold too: a directory the reader has since put
// something in stays, whoever created it.
func TestUninstallKeepsADirectoryTheReaderHasFilled(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"pi-auto", "claude-auto"} {
		if _, err := captureRun(t, "install", target, "--no-index"); err != nil {
			t.Fatalf("install %s: %v", target, err)
		}
	}
	theirs := map[string]string{
		filepath.Join(home, ".pi", "agent", "extensions", "theirs.ts"): "export default {}\n",
		filepath.Join(home, ".claude", "commands", "theirs.md"):        "# their command\n",
	}
	for path, body := range theirs {
		if _, err := os.Stat(filepath.Dir(path)); err != nil {
			t.Fatalf("install did not create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"pi-auto", "claude-auto"} {
		if _, err := captureRun(t, "uninstall", target); err != nil {
			t.Fatalf("uninstall %s: %v", target, err)
		}
	}
	for path := range theirs {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("uninstall took a file of the reader's with the directory: %s", strings.TrimPrefix(path, home))
		}
	}
}

// A commands directory the reader already had stays, empty or not: the record
// is what tells the two apart.
func TestUninstallKeepsACommandsDirectoryTheReaderHad(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	commands := filepath.Join(home, ".claude", "commands")
	if err := os.MkdirAll(commands, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-auto", "--no-index"); err != nil {
		t.Fatalf("install claude-auto: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "claude-auto"); err != nil {
		t.Fatalf("uninstall claude-auto: %v", err)
	}
	if _, err := os.Stat(commands); err != nil {
		t.Errorf("uninstall removed a directory the reader already had: %v", err)
	}
}

// `uninstall --all` walks the harnesses it finds, and the status bar is not a
// harness: it stayed wired to the binary the reader was removing (#3684).
func TestUninstallAllTakesTheStatuslineToo(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"claude-auto", "statusline"} {
		if _, err := captureRun(t, "install", target, "--no-index"); err != nil {
			t.Fatalf("install %s: %v", target, err)
		}
	}
	if _, err := captureRun(t, "uninstall", "--all"); err != nil {
		t.Fatalf("uninstall --all: %v", err)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if got := statuslineCommand(t, settings); got != "" {
		t.Errorf("uninstall --all left the status bar running %q", got)
	}
}

// A statusline of someone else's is not reached by `uninstall --all` either.
func TestUninstallAllKeepsAStatuslineThatIsNotDejas(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{"statusLine": map[string]any{"type": "command", "command": "ccstatusline"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-auto", "--no-index"); err != nil {
		t.Fatalf("install claude-auto: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "--all"); err != nil {
		t.Fatalf("uninstall --all: %v", err)
	}
	if got := statuslineCommand(t, settings); got != "ccstatusline" {
		t.Errorf("uninstall --all touched a statusline that is not deja's: %q", got)
	}
}

// doctor prints `deja install ` and every target it recorded when the binary
// has moved, so the command has to accept more than one of them: with two
// recorded targets that line answered "install needs a target" (#3686).
func TestInstallTakesSeveralTargetsAtOnce(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "claude-auto", "statusline", "--no-index")
	if err != nil {
		t.Fatalf("install claude-auto statusline: %v", err)
	}
	if statuslineCommand(t, filepath.Join(home, ".claude", "settings.json")) == "" {
		t.Errorf("the second target was not installed:\n%s", out)
	}
	if !strings.Contains(out, "claude-auto") {
		t.Errorf("the first target was not installed:\n%s", out)
	}
	if _, err := captureRun(t, "install", "claude-auto", "--all"); err == nil {
		t.Error("--all beside a target was accepted")
	}
}

func statuslineCommand(t *testing.T, settings string) string {
	t.Helper()
	b, err := os.ReadFile(settings)
	if err != nil {
		return ""
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("%s: %v", settings, err)
	}
	entry, _ := root["statusLine"].(map[string]any)
	cmd, _ := entry["command"].(string)
	return cmd
}
