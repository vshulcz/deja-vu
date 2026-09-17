package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `deja install --all` wires the server layer of every harness it finds. For
// DeepSeek Harness both layers are entries in one patch list, and the plain
// target takes the auto row out — so running --all after --auto ended
// auto-recall there, reporting it as "updated" (#3687).
func TestInstallAllKeepsTheAutoLayerItFinds(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".dsh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "deepseek-auto", "--no-index"); err != nil {
		t.Fatalf("install deepseek-auto: %v", err)
	}
	plugin := filepath.Join(home, ".dsh", "plugins", "deja", "auto.js")
	if _, err := os.Stat(plugin); err != nil {
		t.Fatalf("install deepseek-auto wrote no plugin: %v", err)
	}
	if _, err := captureRun(t, "install", "--all", "--no-index"); err != nil {
		t.Fatalf("install --all: %v", err)
	}
	if _, err := os.Stat(plugin); err != nil {
		t.Errorf("install --all deleted the auto-recall plugin: %v", err)
	}
	layer, err := os.ReadFile(filepath.Join(home, ".dsh", "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(layer), yamlQuote(plugin)) {
		t.Errorf("install --all left the patch list without the auto row:\n%s", layer)
	}
}

// The shared skills directory holds one file that several harnesses read, so
// there is one text in it. pi had its own from before the current one existed
// and wrote it to the shared path, so `deja install pi` replaced what codex,
// omp, Senpi, Kimchi and gjc read, and the next install of any of those put it
// back — whichever ran last decided what every agent was told (#3688).
func TestTheSharedSkillHasOneTextWhoeverInstalls(t *testing.T) {
	hermeticEnv(t)
	want := guidanceText("codex")
	for _, harness := range []string{"pi", "omp", "senpi", "cursor", "zed"} {
		if !sharedSkillHarnesses[harness] {
			t.Fatalf("%s no longer shares the skills directory — pick another", harness)
		}
		if got := guidanceText(harness); got != want {
			t.Errorf("%s writes a different text into the shared skill file:\n%s", harness, firstDifference(got, want))
		}
	}
}

func firstDifference(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range al {
		if i >= len(bl) {
			return fmt.Sprintf("line %d is extra: %s", i+1, al[i])
		}
		if al[i] != bl[i] {
			return fmt.Sprintf("line %d:\n got  %s\n want %s", i+1, al[i], bl[i])
		}
	}
	return fmt.Sprintf("shorter by %d lines", len(bl)-len(al))
}
