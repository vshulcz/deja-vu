package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// YAML lets a mapping be indented by any consistent amount, and the writer
// used to assume two spaces: a four-space block took our entry as a sibling at
// two, which made the user's own extension a key inside ours, and the uninstall
// that followed removed both (#2614).
func TestInstallGooseFollowsTheIndentTheBlockUses(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	dir := filepath.Join(cfg, "goose")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "GOOSE_MODEL: gpt-5\nextensions:\n    mine:\n        enabled: true\n"
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGoose("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	conf := gooseConf(t, cfg)
	if !strings.Contains(conf, "\n    deja:\n") {
		t.Fatalf("our entry is not at the indent the block uses, so the extension after it is nested in ours:\n%s", conf)
	}
	if !strings.Contains(conf, "\n    mine:\n        enabled: true\n") {
		t.Fatalf("the user's extension is no longer an extension:\n%s", conf)
	}
	if _, err := installGoose("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	conf = gooseConf(t, cfg)
	if strings.Contains(conf, "deja") {
		t.Fatalf("uninstall left our entry:\n%s", conf)
	}
	if conf != existing {
		t.Fatalf("uninstall did not give the config back as it was:\nwant:\n%s\ngot:\n%s", existing, conf)
	}
}
