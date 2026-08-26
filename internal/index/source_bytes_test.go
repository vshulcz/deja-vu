package index

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A control byte in tracked text is invisible in an editor and invisible in a
// diff, so it survives review by being unreadable. Two reached comments about
// control bytes — one in `internal/usage/snapshots.go` and one in a comment
// claiming the bytes on the wire carry no raw escape (#1983) — and the second
// was found only because someone ran `od -c` over a diff.
//
// Tab, newline and carriage return are the three a text file is made of. A file
// holding a NUL is binary and not this test's business: the testdata fixtures
// deja parses are exactly that.
func TestNoControlBytesInTrackedText(t *testing.T) {
	root := repoRoot(t)
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil || bytes.IndexByte(b, 0) >= 0 {
			return nil
		}
		for i, c := range b {
			if c == '\t' || c == '\n' || c == '\r' {
				continue
			}
			if c < 0x20 || c == 0x7f {
				rel, _ := filepath.Rel(root, path)
				found = append(found, formatByteHit(rel, b, i, c))
				return nil
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Errorf("control bytes in tracked text, which no editor and no diff will show:\n%s",
			strings.Join(found, "\n"))
	}
}

// formatByteHit names the file, the line and the byte, since the byte itself
// prints as nothing.
func formatByteHit(rel string, b []byte, at int, c byte) string {
	line := 1 + bytes.Count(b[:at], []byte("\n"))
	return "  " + rel + ":" + itoa(line) + ": byte 0x" + hexByte(c)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func hexByte(c byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[c>>4], digits[c&0xf]})
}

// repoRoot walks up to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}
