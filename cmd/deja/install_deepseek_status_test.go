package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func dshStatusHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	return home
}

// deepseek-auto adds the status plugin in a package of its own, with the
// dsh.client declaration and "./client" export the web client's module scan
// looks for, and a row naming it; the MCP-only target and uninstall take both
// back out. Every package.json carries a version: dsh 0.2 refuses one without,
// and the refusal failed every model request.
func TestDeepSeekAutoAddsTheWebStatusPlugin(t *testing.T) {
	home := dshStatusHome(t)
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".dsh", "plugins", "deja-status")
	var pkg struct {
		Name, Version string
		Exports       map[string]string
		Dsh           struct {
			Client struct{ Platform string }
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "deja-status" || pkg.Version == "" || pkg.Exports["./client"] != "./client.js" || pkg.Dsh.Client.Platform != "web" {
		t.Errorf("status package.json = %+v", pkg)
	}
	client, err := os.ReadFile(filepath.Join(dir, "client.js"))
	if err != nil || !strings.Contains(string(client), `id: "deja-status"`) || !strings.Contains(string(client), `"conversation.composer.dock"`) {
		t.Errorf("client.js does not register the slot item (%v):\n%s", err, client)
	}
	var cmdPkg struct{ Name, Version string }
	b, _ = os.ReadFile(filepath.Join(home, ".dsh", "plugins", "deja", "package.json"))
	if err := json.Unmarshal(b, &cmdPkg); err != nil || cmdPkg.Version == "" {
		t.Errorf("the command plugin's package.json has no version (%v):\n%s", err, b)
	}
	layer := filepath.Join(home, ".dsh", "cordis.patch.yml")
	if l, _ := os.ReadFile(layer); !strings.Contains(string(l), "- id: deja-status\n") || !strings.Contains(string(l), yamlQuote(filepath.Join(dir, "index.js"))) {
		t.Errorf("the layer does not name the status plugin:\n%s", l)
	}
	if res, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil || res.Action != "unchanged" {
		t.Errorf("a second install: %+v, %v", res, err)
	}

	if _, err := installDeepSeekMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the MCP-only target left the status plugin: %v", err)
	}
	if l, _ := os.ReadFile(layer); strings.Contains(string(l), "deja-status") {
		t.Errorf("the MCP-only layer still names the status plugin:\n%s", l)
	}

	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("uninstall left the status plugin: %v", err)
	}
}

// The node half answers /deja/statusline with deja's line on dsh's web server,
// only once that service exists, and only for a page on this machine.
func TestDeepSeekStatusPluginServesTheLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	home := dshStatusHome(t)
	fake := filepath.Join(home, "deja")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n[ \"$1\" = statusline ] && echo 'deja · 3 recalls · 2 KB ctx today'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installDeepSeekAuto(fake, false); err != nil {
		t.Fatal(err)
	}
	const drive = `
const { default: apply } = await import(process.argv[1]);
let route, waited;
apply({
  inject(deps, cb) { waited = deps; cb({ webServer: { register(r) { route = r; return () => {}; } }, effect() {} }); },
});
const ask = (host) => new Promise((resolve) => {
  const res = { code: 0, body: "", writeHead(c) { this.code = c; return this; }, end(b) { this.body = b || ""; resolve(this); } };
  route.handler({ headers: { host } }, res);
});
const ok = await ask("127.0.0.1:3080");
const far = await ask("attacker.example");
console.log(JSON.stringify({ waited, path: route.path, ok: [ok.code, ok.body], far: far.code }));
`
	cmd := exec.Command(node, "--input-type=module", "-e", drive, "file://"+dshStatusPath())
	cmd.Dir = home
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node could not run the plugin: %v\n%s", err, out)
	}
	want := `{"waited":["webServer"],"path":"/deja/statusline","ok":[200,"deja · 3 recalls · 2 KB ctx today"],"far":403}`
	if got := strings.TrimSpace(string(out)); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
