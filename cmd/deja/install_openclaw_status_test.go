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

// OpenClaw's TUI footer takes nothing from plugins, so deja's status line goes
// in a Control UI sidebar tab: a gateway-auth route the tab descriptor points
// at, which the Control UI renders without the Custom plugin UI lab (stand on
// 2026.9.8). The route answers with `deja statusline`, escaped.
func TestOpenClawPluginServesTheStatusLineToItsTab(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin openclaw would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	script := "#!/bin/sh\ncat >/dev/null\n[ \"$1\" = statusline ] && printf 'deja · 3 recalls · <b>today</b>'\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, "index.mjs")
	if err := os.WriteFile(plugin, []byte(openclawPluginJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
import plugin from "` + plugin + `";
let route, tab;
plugin.register({
  on: () => {},
  registerHttpRoute: (r) => { route = r },
  session: { controls: { registerControlUiDescriptor: (d) => { tab = d } } },
});
const res = { headers: {}, setHeader(k, v) { this.headers[k] = v }, end(b) { this.body = b } };
const handled = await route.handler({}, res);
console.log(JSON.stringify({ route: { path: route.path, auth: route.auth }, tab, handled, status: res.statusCode, body: res.body }));
`
	run := filepath.Join(home, "drive.mjs")
	if err := os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	var got struct {
		Route   struct{ Path, Auth string }
		Tab     struct{ Surface, Path, Label string }
		Handled bool
		Status  int
		Body    string
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("driver said %s", out)
	}
	if got.Route.Auth != "gateway" {
		t.Errorf("the status route is open to anyone who reaches the gateway: auth %q", got.Route.Auth)
	}
	if got.Tab.Surface != "tab" || got.Tab.Path != got.Route.Path {
		t.Errorf("the tab does not point at the route: %+v vs %s", got.Tab, got.Route.Path)
	}
	if !got.Handled || got.Status != 200 {
		t.Errorf("handled %v, status %d", got.Handled, got.Status)
	}
	if !strings.Contains(got.Body, "deja · 3 recalls · &lt;b&gt;today&lt;/b&gt;") {
		t.Errorf("the page lacks the escaped status line:\n%s", got.Body)
	}
}
