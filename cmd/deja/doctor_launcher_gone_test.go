package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The entry names the launcher now, and someone can delete it — it sits in a
// config directory people tidy. The check that reports a hook running a binary
// that is gone read the file's name, so it stopped covering the file every
// hook runs on the day the launcher landed (#3422).
func TestDoctorNamesALauncherThatIsGone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no launcher on windows")
	}
	hermeticEnv(t)
	launcher := dejaLauncherPath()

	claude := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": launcher + " hook-context"},
		}}},
	}}
	b, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, b, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorHooks(&out)
	if got := out.String(); !strings.Contains(got, "runs "+launcher+", which is not there") {
		t.Errorf("doctor said nothing about the missing launcher:\n%s", got)
	}

	// And once it is back, nothing extra.
	if _, err := writeDejaLauncher("/usr/local/bin/deja"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorHooks(&out)
	if got := out.String(); strings.Contains(got, "which is not there") {
		t.Errorf("doctor reported a launcher that is there:\n%s", got)
	}
}
