package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rooHost lays out one VS Code host the way sources.RooRoots expects it, with
// the given settings file in place.
func rooHost(t *testing.T, dir, settings string) string {
	t.Helper()
	root := filepath.Join(dir, "globalStorage", "rooveterinaryinc.roo-cline")
	if err := os.MkdirAll(filepath.Join(root, "settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	if settings != "" {
		if err := os.WriteFile(filepath.Join(root, "settings", "mcp_settings.json"), []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// roo wires one settings file per host, so a refusal on the second host left
// the first one written with a .bak beside it while the run reported the
// target refused (#2750).
func TestRooWritesNoHostWhenOneOfThemWillRefuse(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	good := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
	bad := rooHost(t, filepath.Join(dir, "b"), "{ this is not json ]\n")
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{good, bad}, string(os.PathListSeparator)))

	first := filepath.Join(good, "settings", "mcp_settings.json")
	before, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "roo", "--no-index"); err == nil {
		t.Fatal("a target that cannot finish reported itself installed")
	}
	after, rerr := os.ReadFile(first)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(after) != string(before) {
		t.Errorf("the first host was written before the refusal:\n%s", after)
	}
	if _, err := os.Stat(first + ".bak"); err == nil {
		t.Errorf("a snapshot was left beside a file that was never changed")
	}
}

// The check has to accept what the writer accepts: installMCPJSON takes a
// settings file with comments in it, so refusing one here would keep somebody
// who annotated their config from installing at all (#1664).
func TestRooTakesACommentedHostTheWriterCanEdit(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	host := rooHost(t, filepath.Join(dir, "a"), "{\n  // mine\n  \"mcpServers\": {}\n}\n")
	t.Setenv("DEJA_ROO_ROOTS", host)

	path := filepath.Join(host, "settings", "mcp_settings.json")
	if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
		t.Fatalf("a commented host was refused: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "// mine") {
		t.Errorf("the comment was dropped:\n%s", b)
	}
	if !strings.Contains(string(b), "\"deja\"") {
		t.Errorf("the entry was not written:\n%s", b)
	}
}

// A settings file whose mcpServers is not an object is what installMCPJSON
// refuses, and it has to be refused before the other host is written.
func TestRooRefusesABlockTheWriterCannotUseBeforeWriting(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	good := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
	bad := rooHost(t, filepath.Join(dir, "b"), "{\"mcpServers\": [1, 2]}\n")
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{good, bad}, string(os.PathListSeparator)))

	first := filepath.Join(good, "settings", "mcp_settings.json")
	before, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "roo", "--no-index"); err == nil {
		t.Fatal("a block the writer cannot use was accepted")
	}
	after, rerr := os.ReadFile(first)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(after) != string(before) {
		t.Errorf("the first host was written before the refusal:\n%s", after)
	}
}

// On the way out there is nothing to refuse over: uninstall takes deja out of
// the hosts it can read and leaves the rest alone, the way #2399 has it for a
// block deja never wrote.
func TestUninstallRooTakesDejaOutOfTheHostsItCanRead(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	good := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
	t.Setenv("DEJA_ROO_ROOTS", good)
	if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
		t.Fatal(err)
	}
	bad := rooHost(t, filepath.Join(dir, "b"), "{ not json ]\n")
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{good, bad}, string(os.PathListSeparator)))

	if _, err := captureRun(t, "uninstall", "roo"); err != nil {
		t.Fatalf("uninstall refused over a host it was not asked to edit: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(good, "settings", "mcp_settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "\"deja\"") {
		t.Errorf("deja was left in a host that could be read:\n%s", b)
	}
}
