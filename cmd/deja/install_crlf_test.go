package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Windows user's configs are CRLF, and install rewrote the JSON ones LF-only
// while leaving the TOML and YAML ones half and half — deja's appended block in
// LF inside a CRLF file (#1668).
func TestInstallKeepsCRLFConfigs(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	files := map[string]string{
		".gemini/settings.json":  "{\n  \"theme\": \"dark\"\n}\n",
		".cursor/mcp.json":       "{\n  \"mcpServers\": {}\n}\n",
		".codex/config.toml":     "model = \"o3\"\n",
		".kimi-code/config.toml": "model = \"kimi\"\n",
	}
	for rel, text := range files {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		crlf := strings.ReplaceAll(text, "\n", "\r\n")
		if err := os.WriteFile(p, []byte(crlf), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "install", "--auto"); err != nil {
		t.Fatal(err)
	}
	for rel := range files {
		p := filepath.Join(home, filepath.FromSlash(rel))
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		lf := bytes.Count(b, []byte("\n"))
		crlf := bytes.Count(b, []byte("\r\n"))
		if lf == 0 {
			t.Errorf("%s: no lines at all after install", rel)
			continue
		}
		if crlf != lf {
			t.Errorf("%s: %d of %d line endings are CRLF, the file was written CRLF throughout", rel, crlf, lf)
		}
	}
}

// A file that was LF must not be converted the other way, and a file deja
// creates itself stays LF.
func TestInstallLeavesLFConfigsAlone(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	p := filepath.Join(home, ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{\n  \"theme\": \"dark\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "--auto"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("\r")) {
		t.Errorf("an LF config came back with CR in it:\n%q", string(b))
	}
}

// Idempotence has to survive the conversion: the CRLF file is compared after
// the endings are matched, not before, or every repeat install rewrites it.
func TestInstallCRLFStaysUnchangedOnRepeat(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	seed := map[string]string{
		".gemini/settings.json":  "{\n  \"theme\": \"dark\"\n}\n",
		".cursor/mcp.json":       "{\n  \"mcpServers\": {}\n}\n",
		".codex/config.toml":     "model = \"o3\"\n",
		".kimi-code/config.toml": "model = \"kimi\"\n",
		".grok/config.toml":      "[mcp_servers.mine]\ncommand = \"my-server\"\n",
		".hermes/config.yaml":    "model: hermes-3\n",
		".dsh/cordis.patch.yml":  "# dsh patch\n- insert:\n    - id: mine\n      name: my-plugin\n",
	}
	for rel, text := range seed {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		crlf := strings.ReplaceAll(text, "\n", "\r\n")
		if err := os.WriteFile(p, []byte(crlf), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "install", "--auto"); err != nil {
		t.Fatal(err)
	}
	first := map[string][]byte{}
	for rel := range seed {
		b, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		first[rel] = b
	}
	if _, err := captureRun(t, "install", "--auto"); err != nil {
		t.Fatal(err)
	}
	for rel := range seed {
		b, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first[rel], b) {
			t.Errorf("%s: the second install rewrote a CRLF config:\nfirst:  %q\nsecond: %q", rel, string(first[rel]), string(b))
		}
	}
}
