package main

import (
	"os"
	"path/filepath"
	"runtime"
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

// The JSONC writer refuses more than the parsed one does, and the check has to
// refuse the same shapes: a commented host whose block is not an object, or
// whose top level is not an object at all, used to pass the check and refuse
// one host too late.
func TestRooAsksTheJSONCWriterWhatItWouldRefuse(t *testing.T) {
	for _, bad := range []string{
		"{\n  // mine\n  \"mcpServers\": [1, 2]\n}\n",
		"{\n  // mine\n  \"mcpServers\": null\n}\n",
		"// mine\n[1, 2]\n",
		"// mine\n5\n",
		"// mine\nnull\n",
		"null\n",
	} {
		t.Run(strings.TrimSpace(bad), func(t *testing.T) {
			hermeticEnv(t)
			dir := t.TempDir()
			good := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
			other := rooHost(t, filepath.Join(dir, "b"), bad)
			t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{good, other}, string(os.PathListSeparator)))

			first := filepath.Join(good, "settings", "mcp_settings.json")
			before, err := os.ReadFile(first)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", "roo", "--no-index"); err == nil {
				t.Fatal("a host the writer would refuse was accepted")
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
		})
	}
}

// Reading the settings is not the whole question: the snapshot and the write
// both land in the host's settings directory.
func TestRooAsksWhetherItCanWriteTheHostAtAll(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop a write here")
	}
	hermeticEnv(t)
	dir := t.TempDir()
	good := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
	other := rooHost(t, filepath.Join(dir, "b"), "{\n  \"mcpServers\": {}\n}\n")
	closed := filepath.Join(other, "settings")
	if err := os.Chmod(closed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(closed, 0o755) })
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{good, other}, string(os.PathListSeparator)))

	first := filepath.Join(good, "settings", "mcp_settings.json")
	before, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "roo", "--no-index"); err == nil {
		t.Fatal("a host deja cannot write was accepted")
	}
	after, rerr := os.ReadFile(first)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(after) != string(before) {
		t.Errorf("the first host was written before the refusal:\n%s", after)
	}
}

// A host that is there and cannot be read still has deja in it, so the run
// says so rather than reporting no host at all.
func TestUninstallRooNamesTheHostItCouldNotRead(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	good := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
	t.Setenv("DEJA_ROO_ROOTS", good)
	if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
		t.Fatal(err)
	}
	bad := rooHost(t, filepath.Join(dir, "b"), "{ not json ]\n")
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{good, bad}, string(os.PathListSeparator)))

	out, err := captureRun(t, "uninstall", "roo")
	if err != nil {
		t.Fatalf("uninstall refused over a host it was not asked to edit: %v", err)
	}
	if strings.Contains(out, "no Roo host found") {
		t.Errorf("a host that is there was reported as no host at all:\n%s", out)
	}
	if !strings.Contains(out, "could not read") {
		t.Errorf("the host deja could not read is not named:\n%s", out)
	}
}

// The write follows a symlinked settings directory, so the question of whether
// the host takes a write has to follow it too: a Lstat walked past the link,
// answered about the host root, and refused a host that installs fine.
func TestRooFollowsASymlinkedSettingsDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop a write here")
	}
	hermeticEnv(t)
	dir := t.TempDir()
	host := rooHost(t, filepath.Join(dir, "a"), "")
	elsewhere := filepath.Join(dir, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(host, "settings")
	if err := os.RemoveAll(settings); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, settings); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(host, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(host, 0o755) })
	t.Setenv("DEJA_ROO_ROOTS", host)

	if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
		t.Fatalf("a host whose settings directory is a writable symlink was refused: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(elsewhere, "mcp_settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\"deja\"") {
		t.Errorf("the entry was not written:\n%s", b)
	}
}

// A host deja cannot write is skipped on the way out rather than aborting the
// run: the loop used to stop at the first one and leave every later host wired.
func TestUninstallRooCleansTheHostsItCanAndNamesTheRest(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop a write here")
	}
	hermeticEnv(t)
	dir := t.TempDir()
	a := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
	b := rooHost(t, filepath.Join(dir, "b"), "{\n  \"mcpServers\": {}\n}\n")
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{a, b}, string(os.PathListSeparator)))
	if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
		t.Fatal(err)
	}
	closed := filepath.Join(a, "settings")
	if err := os.Chmod(closed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(closed, 0o755) })

	out, err := captureRun(t, "uninstall", "roo")
	if err != nil {
		t.Fatalf("uninstall stopped at the host it could not write: %v", err)
	}
	if !strings.Contains(out, "cannot write") {
		t.Errorf("the host deja could not write is not named:\n%s", out)
	}
	left, rerr := os.ReadFile(filepath.Join(b, "settings", "mcp_settings.json"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if strings.Contains(string(left), "\"deja\"") {
		t.Errorf("a host deja could clean was left wired:\n%s", left)
	}
}

// A block deja never wrote is left alone and reported unchanged (#2399), so it
// is not a host the uninstall gave up on and must not be named as one.
func TestUninstallRooDoesNotNameAHostItNeverTouched(t *testing.T) {
	hermeticEnv(t)
	for _, shape := range []string{
		"{\"mcpServers\": [1, 2]}\n",
		"{\n  // mine\n  \"mcpServers\": [1, 2]\n}\n",
	} {
		t.Run(strings.TrimSpace(shape), func(t *testing.T) {
			dir := t.TempDir()
			a := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
			t.Setenv("DEJA_ROO_ROOTS", a)
			if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
				t.Fatal(err)
			}
			b := rooHost(t, filepath.Join(dir, "b"), shape)
			t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{a, b}, string(os.PathListSeparator)))

			out, err := captureRun(t, "uninstall", "roo")
			if err != nil {
				t.Fatalf("uninstall refused over a block it never wrote: %v", err)
			}
			if strings.Contains(out, "could not read") || strings.Contains(out, "cannot write") {
				t.Errorf("a host deja never touched was named as one it gave up on:\n%s", out)
			}
		})
	}
}

// The write reason is for a host there is something to take out of. A host
// with no deja in its settings — or none at all — is not written on the way
// out either, so a directory that refuses the write changes nothing there.
func TestUninstallRooDoesNotNameAHostWithNothingOfDejaInIt(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop a write here")
	}
	for _, other := range []string{"", "{\n  \"mcpServers\": {\"mine\": {\"command\": \"x\"}}\n}\n"} {
		name := "no settings file"
		if other != "" {
			name = "someone else's server"
		}
		t.Run(name, func(t *testing.T) {
			hermeticEnv(t)
			dir := t.TempDir()
			a := rooHost(t, filepath.Join(dir, "a"), "{\n  \"mcpServers\": {}\n}\n")
			t.Setenv("DEJA_ROO_ROOTS", a)
			if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
				t.Fatal(err)
			}
			b := rooHost(t, filepath.Join(dir, "b"), other)
			closed := filepath.Join(b, "settings")
			if err := os.Chmod(closed, 0o555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(closed, 0o755) })
			t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{a, b}, string(os.PathListSeparator)))

			out, err := captureRun(t, "uninstall", "roo")
			if err != nil {
				t.Fatalf("uninstall refused over a host with nothing of deja's in it: %v", err)
			}
			if strings.Contains(out, "cannot write") {
				t.Errorf("a host with nothing to remove was named as one deja gave up on:\n%s", out)
			}
		})
	}
}
