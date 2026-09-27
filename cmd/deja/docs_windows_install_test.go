package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// install.sh takes darwin and linux and fails everything else, so a Windows
// reader following the front page gets "unsupported OS" and no next step. The
// release zip is that next step and neither document said so (#1320).
func TestDocsNameTheWindowsInstall(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, name := range []string{"README.md", filepath.Join("docs", "guide", "getting-started.html")} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := strings.ToLower(string(b))
		if !strings.Contains(text, "windows_amd64.zip") {
			t.Errorf("%s does not name the windows download", name)
		}
		if !strings.Contains(text, "unsupported os") {
			t.Errorf("%s does not say what the install script does on windows", name)
		}
		// deja-vu is in ScoopInstaller/Main, so `scoop install deja-vu` needs no
		// bucket added first. A page that names only the zip sends a quarter of
		// our downloads through manual PATH editing for no reason.
		if !strings.Contains(text, "scoop install deja-vu") {
			t.Errorf("%s does not name the scoop install, which is the short path on windows", name)
		}
		// And winget, which ships with Windows: the package has been in
		// microsoft/winget-pkgs since 0.21.0 (#428125), and a reader without
		// Scoop should not be sent to the zip.
		if !strings.Contains(text, "winget install vshulcz.deja-vu") {
			t.Errorf("%s does not name the winget install", name)
		}
	}
}

// The translations carry their own Windows paragraph, so each one has to name
// both package managers too.
func TestTranslatedReadmesNameBothWindowsInstalls(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, l := range readmeLanguages {
		b, err := os.ReadFile(filepath.Join(root, l.file))
		if err != nil {
			t.Fatal(err)
		}
		for _, cmd := range []string{"scoop install deja-vu", "winget install vshulcz.deja-vu"} {
			if !strings.Contains(string(b), cmd) {
				t.Errorf("%s does not name %q", l.file, cmd)
			}
		}
	}
}

// And that the binary alone is a finished install for searching: the reporter's
// setup is a zip, no `deja install`, and every MCP target reading `not-wired`,
// which the docs described nowhere.
func TestDocsSayInstallIsOptional(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, name := range []string{"README.md", filepath.Join("docs", "guide", "getting-started.html")} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := strings.ToLower(string(b))
		if !strings.Contains(text, "not-wired") {
			t.Errorf("%s does not mention what doctor reports without deja install", name)
		}
		if !strings.Contains(text, "optional") {
			t.Errorf("%s does not say deja install is optional", name)
		}
	}
}

// The claim itself, checked against the script rather than trusted: if
// install.sh ever grows a windows branch, these paragraphs are wrong.
func TestInstallScriptStillRefusesWindows(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)
	if !strings.Contains(script, "darwin|linux") {
		t.Error("install.sh no longer takes only darwin and linux; the docs say it does")
	}
	if strings.Contains(script, "mingw") || strings.Contains(script, "msys") {
		t.Error("install.sh looks like it handles windows now; the docs say it does not")
	}
}
