package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Zed on Windows keeps settings.json with CRLF endings; the insert wrote LF
// lines into it and the uninstall left a stray CR behind (#3231).
func TestInstallZedKeepsTheFilesLineEndings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	seed := "// hdr\r\n{\r\n  \"theme\": \"x\",\r\n}\r\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installZedMCP(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for i, line := range strings.SplitAfter(string(b), "\n") {
		if line != "" && strings.HasSuffix(line, "\n") && !strings.HasSuffix(line, "\r\n") {
			t.Fatalf("line %d ends with a bare LF in a CRLF file:\n%q", i+1, b)
		}
	}
	if _, err := installZedMCP(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != seed {
		t.Fatalf("uninstall did not give the file back:\n%q\nwant\n%q", got, seed)
	}
}
