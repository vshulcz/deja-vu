package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// deja.exe is a console program. Started from a host that has no console of
// its own, such as the DSH desktop app, it gets a fresh console window unless
// the spawn asks for windowsHide, and that window flashes on every hook
// (#5635). Every JavaScript or Python file we ship or write into a client's
// config has to ask for it: the plugins under extensions/, the npm launcher,
// and the plugins install generates from this package. In Python the ask is
// creationflags, CREATE_NO_WINDOW on Windows.
func TestShippedSpawnsHideTheirWindow(t *testing.T) {
	root := filepath.Join("..", "..")
	var files []string
	_ = filepath.WalkDir(filepath.Join(root, "extensions"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "test" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		switch filepath.Ext(path) {
		case ".js", ".mjs", ".cjs", ".ts", ".py":
			files = append(files, path)
		}
		return nil
	})
	files = append(files, filepath.Join(root, "npm", "bin", "deja"))
	generated, _ := filepath.Glob("*.go")
	for _, path := range generated {
		if !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
	}

	spawners := regexp.MustCompile(`(^|[^.\w])(spawn|spawnSync|execFile|execFileSync|execSync|exec|fork)\(`)
	promisified := regexp.MustCompile(`(\w+)\s*=\s*promisify\((?:execFile|exec)\)`)
	pyCalls := regexp.MustCompile(`(^|[^.\w])(subprocess\.(?:run|Popen|call|check_call|check_output))\(`)
	checked, pyChecked := 0, 0
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		if strings.Contains(src, "import subprocess") {
			for _, loc := range pyCalls.FindAllStringSubmatchIndex(src, -1) {
				pyChecked++
				if !strings.Contains(callArgs(src, loc[1]), "creationflags=") {
					line := strings.Count(src[:loc[4]], "\n") + 1
					t.Errorf("%s:%d: %s( runs without creationflags", path, line, src[loc[4]:loc[5]])
				}
			}
		}
		if !strings.Contains(src, "child_process") {
			continue
		}
		calls := spawners
		if m := promisified.FindStringSubmatch(src); m != nil {
			calls = regexp.MustCompile(`(^|[^.\w])(spawn|spawnSync|execFile|execFileSync|execSync|exec|fork|` + regexp.QuoteMeta(m[1]) + `)\(`)
		}
		for _, loc := range calls.FindAllStringSubmatchIndex(src, -1) {
			args := callArgs(src, loc[1])
			// The options come before the callback, and a callback body
			// mentioning windowsHide hides nothing.
			for _, cb := range []string{"=>", "function"} {
				if i := strings.Index(args, cb); i >= 0 {
					args = args[:i]
				}
			}
			line := strings.Count(src[:loc[4]], "\n") + 1
			checked++
			if !strings.Contains(args, "windowsHide: true") {
				t.Errorf("%s:%d: %s( spawns without windowsHide: true", path, line, src[loc[4]:loc[5]])
			}
		}
	}
	if checked < 20 {
		t.Fatalf("checked %d spawn sites, want at least 20: the scan stopped finding them", checked)
	}
	if pyChecked < 3 {
		t.Fatalf("checked %d Python subprocess calls, want at least 3: the scan stopped finding them", pyChecked)
	}
}

// callArgs returns the text from just after a call's opening paren to its
// matching close, or to the end of src when the parens never balance.
func callArgs(src string, open int) string {
	depth := 1
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return src[open:i]
			}
		}
	}
	return src[open:]
}
