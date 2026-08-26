package index

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Two of the five sentences about the import directory sanitised it and three
// did not, and the comment on the sanitised pair says why it matters: the path
// reaches a terminal through main, which prints an error as it is (#1857).
func TestEverySentenceAboutTheDirectoryIsSafeToPrint(t *testing.T) {
	base := t.TempDir()
	// Windows refuses every control character in a name, so the odd name there
	// is the other half of what assertPrintable guards: a bidi override, which
	// the filesystem accepts and a terminal obeys (#2081).
	odd := "batch\x1b[31mHACK\rrewound"
	if runtime.GOOS == "windows" {
		odd = "batch\u202eHACK-rewound"
	}

	t.Run("a file where the directory should be", func(t *testing.T) {
		path := filepath.Join(base, odd)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Skipf("the filesystem refused the name: %v", err)
		}
		_, err := Import(filepath.Join(base, "idx"), path)
		if err == nil {
			t.Fatal("importing a file was accepted")
		}
		assertPrintable(t, err.Error(), "is a file")
	})

	t.Run("a directory deja may not read", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads anything")
		}
		dir := filepath.Join(base, odd+"-dir")
		// The name carries an escape and a carriage return on purpose, and not
		// every filesystem takes one — Windows refuses the whole class. Its
		// siblings above already skip on that; this one failed instead (#2081).
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Skipf("the filesystem refused the name: %v", err)
		}
		if err := os.Chmod(dir, 0o000); err != nil {
			t.Skipf("cannot drop permissions: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		_, err := Import(filepath.Join(base, "idx2"), dir)
		if err == nil {
			t.Skip("this platform read the directory anyway")
		}
		assertPrintable(t, err.Error(), "permission denied")
	})

	t.Run("a file where the export directory should be", func(t *testing.T) {
		path := filepath.Join(base, odd+"-export")
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Skipf("the filesystem refused the name: %v", err)
		}
		_, _, err := ExportDeferred(filepath.Join(base, "idx3"), path, "peer")
		if err == nil {
			t.Skip("the fixture did not reach the sentence")
		}
		if strings.Contains(err.Error(), "is a file") {
			assertPrintable(t, err.Error(), "is a file")
		}
	})
}

// assertPrintable holds one sentence: no escape, no rewind, and still naming
// what went wrong.
func assertPrintable(t *testing.T, msg, want string) {
	t.Helper()
	if strings.ContainsAny(msg, "\x1b\r") {
		t.Errorf("the sentence carries an escape or a rewind: %q", msg)
	}
	if !strings.Contains(msg, want) {
		t.Errorf("the sentence no longer says what happened (%q): %q", want, msg)
	}
	if !strings.Contains(msg, "batch") {
		t.Errorf("the sentence no longer names the directory: %q", msg)
	}
}
