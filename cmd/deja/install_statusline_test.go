package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallStatuslineErrorAndUninstallIdempotent(t *testing.T) {
	t.Run("malformed settings", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		path := filepath.Join(home, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"statusLine":`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installStatusline("/bin/deja", false); err == nil {
			t.Fatal("expected malformed settings.json error")
		}
	})

	t.Run("uninstall preserves foreign statusline", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		path := filepath.Join(home, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		old := `{"statusLine":{"type":"command","command":"/bin/other status"}}`
		if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
			t.Fatal(err)
		}
		r, err := installStatusline("/bin/deja", true)
		if err != nil {
			t.Fatal(err)
		}
		if r.Action != "unchanged" {
			t.Fatalf("action = %q", r.Action)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "/bin/other status") || strings.Contains(string(b), "/bin/deja statusline") {
			t.Fatalf("settings changed: %s", b)
		}
	})
}

func TestInstallStatuslineInstallConflictAndRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	r, err := installStatusline("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "created" {
		t.Fatalf("install result = %#v", r)
	}
	r, err = installStatusline("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "unchanged" {
		t.Fatalf("second install = %#v", r)
	}
	r, err = installStatusline("/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "updated" {
		t.Fatalf("uninstall = %#v", r)
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "statusLine") {
		t.Fatalf("statusline left in settings: %s", b)
	}

	if _, err := installStatusline("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(path, []byte(`{"statusLine":{"type":"command","command":"/bin/other status"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installStatusline("/bin/deja", false); err == nil || !strings.Contains(err.Error(), "already configured") {
		t.Fatalf("conflict err = %v", err)
	}
}

func TestInstallTargetErrorsAndAliases(t *testing.T) {
	if _, err := installTarget("missing", "/bin/deja", false); err == nil || !strings.Contains(err.Error(), "unknown target") {
		t.Fatalf("unknown target err = %v", err)
	}
	for _, args := range [][]string{nil, {"a", "b"}} {
		if err := runInstall(args, false); err == nil || !strings.Contains(err.Error(), "install needs target") {
			t.Fatalf("install args %v err = %v", args, err)
		}
	}
	if err := runInstall(nil, true); err == nil || !strings.Contains(err.Error(), "uninstall needs target") {
		t.Fatalf("uninstall err = %v", err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installTarget("claude", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
}
