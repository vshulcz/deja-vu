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
func driveDSHAuto(t *testing.T, web, withLib, card bool) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin dsh would run")
	}
	home := dshStatusHome(t)
	if _, ierr := installDeepSeekAuto("/usr/local/bin/deja", false); ierr != nil {
		t.Fatal(ierr)
	}
	if !withLib {
		if rerr := os.Remove(dshLibPath()); rerr != nil {
			t.Fatal(rerr)
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
	if werr := os.WriteFile(stub, []byte(script), 0o755); werr != nil {
		t.Fatal(werr)
	}
	if werr := os.WriteFile(dshAutoPath(), []byte(dshAutoJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	loadCard := ""
	if card {
		loadCard = `(await import(pathToFileURL(process.argv[3]).href)).default({ effect: (f) => f() });`
	}
	services := "{}"
	if web {
		services = `{ connection: { fetch: { register: (r) => { routes.push(r) } } }, webServer: {} }`
	}
	driver := `
import { pathToFileURL } from "node:url";
const plugin = (await import(pathToFileURL(process.argv[2]).href)).default;
` + loadCard + `
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
	if werr := os.WriteFile(run, []byte(driver), 0o644); werr != nil {
		t.Fatal(werr)
	}
	out, err := exec.CommandContext(t.Context(), node, run, dshAutoPath(), dshNotesPath()).CombinedOutput()
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
	out, asked := driveDSHAuto(t, true, true, true)
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
		name               string
		web, withLib, card bool
		routes             string
	}{
		{"headless", false, true, true, "routes=0\n"},
		{"no lib.js", true, false, true, "routes=0\n"},
		// The person switched the deja-notes row off: no card, so the route
		// may stand but nothing is asked for it.
		{"card row off", true, true, false, "routes=1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, asked := driveDSHAuto(t, tc.web, tc.withLib, tc.card)
			if !strings.Contains(out, "project=MODEL TEXT hook-context\n") || !strings.Contains(out, "recall=MODEL TEXT hook-prompt\n") || !strings.Contains(out, tc.routes) {
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
		Name    string            `json:"name"`
		Version string            `json:"version"`
		Exports map[string]string `json:"exports"`
		Dsh     struct {
			Client struct {
				Platform string `json:"platform"`
			} `json:"client"`
		} `json:"dsh"`
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

// A plugins/deja-notes or plugins/deja-status of somebody else's is left as it
// is by install, by the MCP-only target and by uninstall, and the layer names
// no deja row for it.
func TestDeepSeekLeavesSomeoneElsesPluginPackages(t *testing.T) {
	home := dshStatusHome(t)
	theirs := map[string]string{}
	for _, name := range []string{"deja-notes", "deja-status"} {
		dir := filepath.Join(home, ".dsh", "plugins", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "export default function apply() { /* mine: " + name + " */ }\n"
		if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		theirs[dir] = body
	}
	check := func(step string) {
		t.Helper()
		for dir, body := range theirs {
			if b, err := os.ReadFile(filepath.Join(dir, "index.js")); err != nil || string(b) != body {
				t.Errorf("%s: %s/index.js = %q, %v", step, dir, b, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "client.js")); !os.IsNotExist(err) {
				t.Errorf("%s: deja wrote into %s", step, dir)
			}
		}
	}
	res, err := installDeepSeekAuto("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	check("install")
	if !strings.Contains(res.Note, "alone") || !strings.Contains(res.Note, "deja-notes") {
		t.Errorf("install did not say it left them: %+v", res)
	}
	layer, _ := os.ReadFile(filepath.Join(home, ".dsh", "cordis.patch.yml"))
	if strings.Contains(string(layer), "deja-notes") || strings.Contains(string(layer), "deja-status") {
		t.Errorf("the layer names a package deja did not write:\n%s", layer)
	}
	if _, err := installDeepSeekMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	check("MCP-only install")
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	check("uninstall")
}

// A machine wired with `deja install dsh-auto` before the alias was recorded
// has deja's auto.js on disk and no deepseek-auto in the record. install --all
// has to keep it, or it takes auto.js and the card away.
func TestInstallAllKeepsDshAutoWiredBeforeItWasRecorded(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DSH_HOME", filepath.Join(t.TempDir(), ".dsh"))
	if got := withWiredAutoTargets([]string{"deepseek"}); strings.Join(got, ",") != "deepseek" {
		t.Fatalf("premise: nothing on disk, got %v", got)
	}
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got := withWiredAutoTargets([]string{"deepseek"}); strings.Join(got, ",") != "deepseek,deepseek-auto" {
		t.Errorf("withWiredAutoTargets = %v", got)
	}
}
