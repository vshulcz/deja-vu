package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Feed every registry fixture through its own parser after three kinds of
// damage a real store shows: a tail cut mid-line, bytes that are not UTF-8, and
// a NUL inside a value. What comes back has to be text — the display layer
// sanitises, but a consumer that forwards Text without it (export, sync, a new
// surface) inherits whatever the parser kept (#1740).
func TestCorruptFixturesAcrossParsers(t *testing.T) {
	root := filepath.Join("..", "..", "fixtures", "registry")
	var files []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(p, ".jsonl") || strings.HasSuffix(p, ".json") || strings.HasSuffix(p, ".md")) {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		t.Fatal("no fixtures")
	}
	damage := map[string]func([]byte) []byte{
		"truncated": func(b []byte) []byte { return b[:len(b)*6/10] },
		"badutf8":   func(b []byte) []byte { return append(append([]byte{}, b...), 0xff, 0xfe, '\n') },
		"nul":       func(b []byte) []byte { return []byte(strings.Replace(string(b), "a", "\x00", 1)) },
	}
	tmp := t.TempDir()
	checked := 0
	// Which harnesses this sweep actually reached: a fixture layout that stops
	// matching would otherwise turn the whole sweep into a silent pass.
	covered := map[string]bool{}
	for _, src := range files {
		orig, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		for name, f := range damage {
			dst := filepath.Join(tmp, name+"-"+filepath.Base(src))
			if err := os.WriteFile(dst, f(orig), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, h := range Registry() {
				for _, k := range h.Kinds {
					if k.Parse == nil {
						continue
					}
					// Match is path-shaped; call the parser directly on the
					// damaged copy of a fixture the same harness owns.
					if !strings.Contains(src, string(filepath.Separator)+harnessDir(h.Name)+string(filepath.Separator)) {
						continue
					}
					checked++
					covered[h.Name] = true
					ss, err := k.Parse(dst, 0)
					_ = err
					for _, s := range ss {
						// Metadata reaches a reader the same way the body does:
						// a project name is printed on every row.
						fields := []struct{ what, text string }{
							{"project", s.Project}, {"id", s.ID}, {"title", s.Title},
						}
						for _, m := range s.Messages {
							fields = append(fields, struct{ what, text string }{"text", m.Text})
						}
						for _, f := range fields {
							if !utf8.ValidString(f.text) {
								t.Errorf("%s %s: %s parsed invalid utf-8 into %s out of %s", h.Name, k.Name, name, f.what, filepath.Base(src))
							}
							if strings.ContainsRune(f.text, 0) {
								t.Errorf("%s %s: %s parsed a NUL into %s out of %s", h.Name, k.Name, name, f.what, filepath.Base(src))
							}
						}
					}
				}
			}
		}
	}
	t.Logf("parsed %d damaged fixture/kind pairs across %d harnesses", checked, len(covered))
	var missed []string
	for _, h := range Registry() {
		switch h.Name {
		case "deja", "opencode", "cursor", "zed", "hermes":
			// Database-backed or synthesised stores: their fixture is a SQL
			// script or nothing at all, so there is no transcript to damage
			// here. Their read paths are covered by their own tests.
			continue
		}
		if !covered[h.Name] {
			missed = append(missed, h.Name)
		}
	}
	if len(missed) > 0 {
		t.Errorf("the sweep reached no fixture for %v — it is passing without testing them", missed)
	}
}

func harnessDir(name string) string {
	switch name {
	case "claude":
		return "claude-code"
	case "deepseek":
		return "deepseek"
	}
	return name
}
