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

// driveDSHAuto installs deepseek-auto, points its auto.js at a stub deja, and
// runs one turn through it the way dsh would: two contexts assembled for a
// session, and, when web is true, the connection and web-server services the
// web profile injects. It returns what the driver printed and the stub's calls.
func driveDSHAuto(t *testing.T, web, withLib bool) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin dsh would run")
	}
	home := dshStatusHome(t)
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if !withLib {
		if err := os.Remove(dshLibPath()); err != nil {
			t.Fatal(err)
		}
	}
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	// JSON unless asked for --plain, as deja answers: the model's text in
	// additionalContext, the person's lines in systemMessage.
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' \"$*\" >> " + calls + "\n" +
		"case \"$*\" in\n" +
		"  *--plain*) printf 'MODEL TEXT %s' \"$1\" ;;\n" +
		"  hook-prompt) printf '{\"hookSpecificOutput\":{\"hookEventName\":\"UserPromptSubmit\",\"additionalContext\":\"MODEL TEXT hook-prompt\"},\"systemMessage\":\"deja-vu — you have been here: \\\\\"zorbit drain\\\\\" (4d ago · via: drain)\"}' ;;\n" +
		"  hook-context) printf '{\"hookSpecificOutput\":{\"hookEventName\":\"SessionStart\",\"additionalContext\":\"MODEL TEXT hook-context\"},\"systemMessage\":\"deja: first build done\"}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dshAutoPath(), []byte(dshAutoJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	services := "{}"
	if web {
		services = `{ connection: { fetch: { register: (r) => { routes.push(r) } } }, webServer: {} }`
	}
	driver := `
import { pathToFileURL } from "node:url";
const plugin = (await import(pathToFileURL(process.argv[2]).href)).default;
const routes = [];
const contexts = {};
const services = ` + services + `;
plugin({
  systemPrompt: { context: (c) => { contexts[c.name] = c.text } },
  on: () => {},
  inject: (names, cb) => { if (names.every((n) => n in services)) cb(services) },
});
await new Promise((r) => setTimeout(r, 50));
const log = [{ type: "user/message", data: { role: "user", content: [{ type: "text", text: "how do we drain zorbit" }] } }];
const agent = { sessionId: "session-s1", session: { id: "session-s1", header: { cwd: "/w" }, events: log } };
console.log("project=" + contexts["deja:project"]({ agent }));
console.log("recall=" + contexts["deja:recall"]({ agent }));
console.log("routes=" + routes.length);
for (const r of routes) {
  const take = async () => (await (await r.fetch(new Request("http://x/api/deja-notes?session=session-s1"))).json()).notes;
  console.log("path=" + r.path);
  console.log("notes=" + JSON.stringify(await take()));
  console.log("again=" + JSON.stringify(await take()));
}
`
	run := filepath.Join(home, "drive.mjs")
	if err := os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, run, dshAutoPath()).CombinedOutput()
	if err != nil {
		t.Fatalf("driving auto.js: %v\n%s", err, out)
	}
	asked, _ := os.ReadFile(calls)
	return string(out), string(asked)
}

// In dsh web the person sees deja's lines: auto.js asks each hook once for its
// JSON reply, hands the model exactly the text --plain prints, and keeps the
// receipt and notes for the card behind /api/deja-notes, once (#4983).
func TestDeepSeekAutoShowsThePersonDejasLinesInDshWeb(t *testing.T) {
	out, asked := driveDSHAuto(t, true, true)
	for _, want := range []string{
		"project=MODEL TEXT hook-context\n",
		"recall=MODEL TEXT hook-prompt\n",
		"routes=1\n",
		"path=/api/deja-notes\n",
		`notes=["deja: first build done","deja-vu — you have been here: \"zorbit drain\" (4d ago · via: drain)"]` + "\n",
		"again=[]\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(asked, "--plain") || strings.Count(asked, "\n") != 2 {
		t.Errorf("want one JSON call per hook, got:\n%s", asked)
	}
}

// Without a page — tui, headless, sdk — nothing reads the board, so the hooks
// stay --plain and deja does not mark notes shown that nobody saw. Without
// lib.js the plugin still recalls and registers no route.
func TestDeepSeekAutoKeepsPlainWhereNoPageReads(t *testing.T) {
	for _, tc := range []struct {
		name         string
		web, withLib bool
	}{
		{"headless", false, true},
		{"no lib.js", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, asked := driveDSHAuto(t, tc.web, tc.withLib)
			if !strings.Contains(out, "project=MODEL TEXT hook-context\n") || !strings.Contains(out, "recall=MODEL TEXT hook-prompt\n") || !strings.Contains(out, "routes=0\n") {
				t.Errorf("driver:\n%s", out)
			}
			if strings.Count(asked, "--plain") != 2 {
				t.Errorf("want both hooks asked --plain, got:\n%s", asked)
			}
		})
	}
}

// The card is the npm plugin's client.js in a package of its own: dsh serves
// a browser half only from a package declaring dsh.client, and refuses two
// rows resolving to one package. The MCP-only target and uninstall take it
// back out, with lib.js.
func TestDeepSeekAutoWritesTheNotesCard(t *testing.T) {
	home := dshStatusHome(t)
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".dsh", "plugins", "deja-notes")
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
	if pkg.Name != "deja-notes" || pkg.Version == "" || pkg.Exports["./client"] != "./client.js" || pkg.Dsh.Client.Platform != "web" {
		t.Errorf("notes package.json = %+v", pkg)
	}
	client, _ := os.ReadFile(filepath.Join(dir, "client.js"))
	if !strings.Contains(string(client), `id: "deja-notes",`) || strings.Contains(string(client), `id: "dsh-deja"`) || !strings.Contains(string(client), `"conversation.input.dock"`) {
		t.Errorf("client.js does not register as deja-notes:\n%.400s", client)
	}
	if _, err := os.Stat(filepath.Join(home, ".dsh", "plugins", "deja", "lib.js")); err != nil {
		t.Errorf("lib.js is not beside auto.js: %v", err)
	}
	layer := filepath.Join(home, ".dsh", "cordis.patch.yml")
	if l, _ := os.ReadFile(layer); !strings.Contains(string(l), "- id: deja-notes\n") || !strings.Contains(string(l), yamlQuote(filepath.Join(dir, "index.js"))) {
		t.Errorf("the layer does not name the notes package:\n%s", l)
	}
	if res, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil || res.Action != "unchanged" {
		t.Errorf("a second install: %+v, %v", res, err)
	}

	if _, err := installDeepSeekMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the MCP-only target left the notes package: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".dsh", "plugins", "deja", "lib.js")); !os.IsNotExist(err) {
		t.Errorf("the MCP-only target left lib.js: %v", err)
	}

	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("uninstall left the notes package: %v", err)
	}
}

// `deja install dsh-auto` is what the dsh guide prints. Recorded under the
// alias it was dropped as unknown, and `deja uninstall dsh-auto` was refused.
func TestDshAliasIsRecordedAndUninstalls(t *testing.T) {
	got := withAutoTargets([]string{"dsh-auto"})
	if strings.Join(got, ",") != "deepseek,deepseek-auto" {
		t.Errorf("withAutoTargets(dsh-auto) = %v", got)
	}
	hermeticEnv(t)
	recordWiring([]string{"dsh-auto"}, false)
	if st := readWiringState(); strings.Join(st.Targets, ",") != "deepseek-auto" {
		t.Errorf("recorded targets = %v", st.Targets)
	}
}
