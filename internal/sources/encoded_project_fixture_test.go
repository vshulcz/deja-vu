package sources

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// encodedFixtureSuffix is a path segment nobody can have on disk, for fixtures
// that mean to exercise the dash fallback in decodeProjectBase.
//
// The decoder resolves an encoded project name against the filesystem on
// purpose — that is what recovers `deja-vu` from `-Users-me-deja-vu` instead of
// calling it `deja/vu`. So a fixture that encodes a plausible path is asserting
// the fallback only for as long as nobody creates that path: two tests encoded
// `/tmp/deja-vu` and failed on a contributor's machine twice, on unmodified
// main, because they had a checkout there (#3512).
func encodedFixtureSuffix(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	// Hex, so the segment survives the split on "-" as one piece.
	return "fx" + hex.EncodeToString(b[:])
}

// encodeProjectPath spells a path the way a harness stores it as a directory
// name. On unix that is every separator replaced by a dash. On windows the
// drive colon goes the same way — `C:\Users\me\app` is stored `C--Users-me-app`
// — and a fixture that replaced separators only left `C:-Users-…`, which
// resolveEncodedPath cannot resolve and the decoder then read through its dash
// fallback. The test below asserts the resolving branch, so on windows it was
// asserting the wrong one, and main went red there.
func encodeProjectPath(path string) string {
	encoded := strings.ReplaceAll(path, string(filepath.Separator), "-")
	if vol := filepath.VolumeName(path); strings.HasSuffix(vol, ":") {
		encoded = strings.Replace(encoded, vol, strings.TrimSuffix(vol, ":")+"-", 1)
	}
	return encoded
}

// The other side of the same rule, pinned on a directory the test owns rather
// than on the absence of one: an encoded name that does resolve keeps the
// hyphen the encoding lost.
func TestAnEncodedProjectThatResolvesKeepsItsHyphen(t *testing.T) {
	root := t.TempDir()
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(real, "deja-vu")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	encoded := encodeProjectPath(project)
	if got := decodeProjectBase(encoded); got != filepath.Base(real)+"/deja-vu" {
		t.Errorf("decodeProjectBase(%q) = %q, want the hyphen kept under %q",
			encoded, got, filepath.Base(real))
	}

	// And with the directory gone, the same name falls back to the dash rule —
	// which is the branch the fixtures elsewhere are asserting.
	if err := os.RemoveAll(project); err != nil {
		t.Fatal(err)
	}
	if got := decodeProjectBase(encoded); got != "deja/vu" {
		t.Errorf("with the directory gone, decodeProjectBase(%q) = %q, want deja/vu", encoded, got)
	}
}
