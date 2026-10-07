package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// vscode-auto puts deja's status item into VS Code as a local extension that
// runs `deja statusline`, named in extensions.json beside the reader's own
// extensions, and uninstall takes both out again.
func TestVSCodeAutoAddsTheStatusExtension(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("VSCODE_EXTENSIONS", "")
	extDir := filepath.Join(sources.Home(), ".vscode", "extensions")
	theirs := `[{"identifier":{"id":"someone.theirs"},"version":"2.0.0","relativeLocation":"someone.theirs-2.0.0"}]`
	writeRulesTestFile(t, filepath.Join(extDir, "extensions.json"), theirs)

	if _, err := installTarget("vscode-auto", "/opt/deja/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(extDir, "vshulcz.deja-status-1.0.0")
	js := readRulesTestFile(t, filepath.Join(dir, "extension.js"))
	if !strings.Contains(js, `["statusline"]`) || !strings.Contains(js, "createStatusBarItem") {
		t.Fatalf("extension.js does not run deja statusline into a status item:\n%s", js)
	}
	var pkg struct {
		Name, Publisher, Main string
		Activation            []string `json:"activationEvents"`
	}
	if err := json.Unmarshal([]byte(readRulesTestFile(t, filepath.Join(dir, "package.json"))), &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Publisher+"."+pkg.Name != vsCodeStatusID || pkg.Main != "./extension.js" || len(pkg.Activation) == 0 {
		t.Fatalf("package.json = %+v", pkg)
	}
	list := readRulesTestFile(t, filepath.Join(extDir, "extensions.json"))
	if !strings.Contains(list, `{"identifier":{"id":"someone.theirs"},"version":"2.0.0","relativeLocation":"someone.theirs-2.0.0"}`) ||
		!strings.Contains(list, `"id":"vshulcz.deja-status"`) || !strings.Contains(list, `"relativeLocation":"vshulcz.deja-status-1.0.0"`) {
		t.Fatalf("extensions.json = %s", list)
	}

	if r, err := installVSCodeStatusItem("/opt/deja/bin/deja", false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second install: %+v, %v", r, err)
	}

	if _, err := installTarget("vscode-auto", "/opt/deja/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the extension: %v", err)
	}
	if got := readRulesTestFile(t, filepath.Join(extDir, "extensions.json")); got != theirs {
		t.Fatalf("extensions.json after uninstall = %s, want the reader's list back", got)
	}
}

func TestVSCodeURIPath(t *testing.T) {
	if got := vsCodeURIPath("windows", `C:\Users\a b\.vscode\extensions\x`); got != "/c:/Users/a b/.vscode/extensions/x" {
		t.Errorf("windows: %q", got)
	}
	if got := vsCodeURIPath("linux", "/home/a/.vscode/extensions/x"); got != "/home/a/.vscode/extensions/x" {
		t.Errorf("linux: %q", got)
	}
}
