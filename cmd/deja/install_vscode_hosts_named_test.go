package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Three hosts written, one named: the result of the last host was the whole
// answer, so Code and Insiders were updated in silence (#3233).
func TestInstallVSCodeNamesEveryHostItWrites(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	t.Setenv("DEJA_VSCODE_USER_DIRS", a+string(os.PathListSeparator)+b)
	res, err := installVSCodeMCP("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	all := res.Path + " " + res.Note
	for _, dir := range []string{a, b} {
		if !strings.Contains(all, filepath.Join(dir, "mcp.json")) && !strings.Contains(all, shortHome(filepath.Join(dir, "mcp.json"))) {
			t.Fatalf("host %s not named: path=%q note=%q", dir, res.Path, res.Note)
		}
	}
}

func TestInstallRooNamesEveryHostItWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{
		rooStorage(t, home, "Code"),
		rooStorage(t, home, "Cursor"),
	}, string(os.PathListSeparator)))
	res, err := installRoo("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	all := res.Path + " " + res.Note
	for _, host := range []string{"Code", "Cursor"} {
		if !strings.Contains(all, host) {
			t.Fatalf("host %s not named: path=%q note=%q", host, res.Path, res.Note)
		}
	}
}
