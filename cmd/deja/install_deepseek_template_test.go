package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// dsh renders a prompt context through a strict {{name}} interpolation and
// throws on a name it has not registered, ending the turn before the model is
// called. A recall quoting {{cwd}} from a past session did exactly that on
// every turn (#5421). The installer's auto.js registers the same two contexts
// as the npm plugin, so it has to hand dsh inert text too.
func TestDeepSeekAutoRecallWithTemplateBracesRenders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin dsh would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	script := "#!/bin/sh\ncat >/dev/null\n" +
		"printf '%s\\n' 'last time: unknown prompt variable \"{{cwd}}\" in context \"deja:project\"; {{cwd b {{a b}}'\n"
	if werr := os.WriteFile(stub, []byte(script), 0o755); werr != nil {
		t.Fatal(werr)
	}
	plugin := filepath.Join(home, "auto.js")
	if werr := os.WriteFile(plugin, []byte(dshAutoJS(stub)), 0o644); werr != nil {
		t.Fatal(werr)
	}
	// render mirrors dsh-system-prompt 0.2.1-rc.2's strict interpolation, with
	// the interpolate flag ignored the way every dsh before 0.2.1-alpha.2 does.
	driver := `
import plugin from ` + strconv.Quote(plugin) + `;
const contexts = [];
plugin({ systemPrompt: { context: (c) => contexts.push(c) } });
function render(c, text) {
  for (let open = text.indexOf("{{"); open >= 0; open = text.indexOf("{{", open + 2)) {
    const group = /^\{\{([^{}]*)\}\}/.exec(text.slice(open));
    if (!group) {
      if (text.indexOf("}}", open + 2) >= 0) throw new Error("malformed prompt variable reference in context " + c.name);
      continue;
    }
    if (!["provider", "model"].includes(group[1])) throw new Error("unknown prompt variable {{" + group[1] + "}} in context " + c.name);
  }
  return text;
}
const agent = { sessionId: "sess-A", session: { events: [
  { type: "user/message", data: { role: "user", content: [{ type: "text", text: "why did the turn fail" }] } },
] } };
for (const c of contexts) {
  try {
    console.log(c.name + " interpolate=" + c.interpolate + " " + JSON.stringify(render(c, c.text({ agent }))));
  } catch (e) {
    console.log(c.name + " THROWS " + e.message);
  }
}
`
	run := filepath.Join(home, "drive.mjs")
	if werr := os.WriteFile(run, []byte(driver), 0o644); werr != nil {
		t.Fatal(werr)
	}
	out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	got := string(out)
	for _, name := range []string{"deja:project", "deja:recall"} {
		line := ""
		for l := range strings.SplitSeq(got, "\n") {
			if strings.HasPrefix(l, name+" ") {
				line = l
			}
		}
		if line == "" {
			t.Fatalf("%s was not registered:\n%s", name, got)
		}
		if strings.Contains(line, "THROWS") {
			t.Errorf("recalled {{cwd}} fails the turn: %s", line)
		}
		if !strings.Contains(line, "interpolate=false") {
			t.Errorf("%s lets dsh read recalled text as a template: %s", name, line)
		}
		if !strings.Contains(line, "{ {cwd} }") {
			t.Errorf("%s did not carry the recalled text: %s", name, line)
		}
	}
}
