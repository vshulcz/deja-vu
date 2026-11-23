package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The moment a command fails is the one an agent never thinks to ask about, and
// tool.execute.after is the only seam opencode gives a plugin for it. The hook
// returns void, so the fix line cannot be handed to the model directly — it is
// appended to the tool's own output, which the next request carries as the tool
// result. Verified in a real opencode run: on an invented error whose repair
// cannot be guessed, the line lands in the tool message of the following
// request.
func TestOpencodePluginCarriesTheFixLineAfterAFailedCommand(t *testing.T) {
	js := opencodePluginJS("/bin/deja")
	compact := strings.Join(strings.Fields(js), "")

	if !strings.Contains(compact, `ctx.tool.hook("execute.after"`) {
		t.Fatal("the after-tool channel is not wired")
	}
	// Only shell: the error signature this reads is a shell one, and an edit
	// or a read carries no command that failed. A failed command still reports
	// "completed" in V2 — the failure lives in the text, not the status.
	if !strings.Contains(compact, `if(event.tool!=="shell"||event.status!=="completed")return`) {
		t.Error("the channel is not scoped to completed shell calls")
	}
	// It calls the failure half of the pair, not the pre-tool half.
	if !strings.Contains(compact, `hook-tool-after`) {
		t.Error("the channel does not call hook-tool-after")
	}
	// The command's output is what carries the error, and it goes to deja under
	// the field the hook reads.
	if !strings.Contains(compact, "tool_response:{output:text}") {
		t.Error("the command output is not passed as the tool response")
	}
	// The line is appended to the output, not pushed as a new message: the
	// hook can only replace the result, so the tool result is the channel.
	if !strings.Contains(compact, `event.result={...result,content:[...content,{type:"text",text:extra}]}`) {
		t.Error("the fix line is not folded into the tool output")
	}
	// It runs in the project, like every other call that ranks by history.
	if !strings.Contains(compact, "cwd,") {
		t.Error("the after-call does not carry the project directory")
	}
}

// Install used to put its entry last, which meant the comma joining it to the
// block went on the reader's own line — and uninstall, which cannot tell that
// comma from one they wrote, left it there (#2617).
func TestOpencodeJSONCGivesTheConfigBackAsItWas(t *testing.T) {
	cases := map[string]string{
		"one neighbour": `{
  // mine
  "mcp": {
    "theirs": { "type": "local" }
  }
}
`,
		"two neighbours": `{
  "mcp": {
    "a": { "type": "local" },
    "b": { "type": "local" }
  }
}
`,
		"their own trailing comma": `{
  "mcp": {
    "theirs": { "type": "local" },
  }
}
`,
		"neighbour over several lines": `{
  "mcp": {
    "theirs": {
      "type": "local"
    }
  }
}
`,
		"a comment above the first entry": `{
  "mcp": {
    // the one I actually use
    "theirs": { "type": "local" }
  }
}
`,
		"comments only": `{
  "mcp": {
    // nothing here yet
  }
}
`,
	}
	for name, before := range cases {
		t.Run(name, func(t *testing.T) {
			installed, _, err := updateOpencodeJSONC([]byte(before), "/usr/local/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(installed), `"deja"`) {
				t.Fatalf("install wrote no entry:\n%s", installed)
			}
			if err = parsesAsJSONC(t, string(installed)); err != nil {
				t.Fatalf("install left a file that does not parse: %v\n%s", err, installed)
			}
			back, _, err := updateOpencodeJSONC(installed, "/usr/local/bin/deja", true)
			if err != nil {
				t.Fatal(err)
			}
			if string(back) != before {
				t.Fatalf("uninstall did not give the config back as it was:\nwant:\n%s\ngot:\n%s", before, back)
			}
		})
	}
}

// A comment the reader put above their first entry belongs to that entry, so
// our own goes above it rather than between the two (#4203).
func TestOpencodeJSONCLeavesALeadingCommentWhereItWas(t *testing.T) {
	before := `{
  "mcp": {
    // the one I actually use
    "theirs": { "type": "local" }
  }
}
`
	out, _, err := updateOpencodeJSONC([]byte(before), "/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(out), "\n")
	comment, deja := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "the one I actually use") {
			comment = i
		}
		if strings.Contains(l, `"deja"`) {
			deja = i
		}
	}
	if comment < 0 || deja < 0 || deja > comment {
		t.Fatalf("our entry went between the reader's comment and its entry:\n%s", out)
	}
}

// A comment directly above the reader's first server is about that server;
// deja's entry went between the two and the comment read as describing deja
// (#4203). The entry goes above the comment, and uninstall still gives the
// file back as it was.
func TestOpencodeEntryGoesAboveTheCommentOfTheFirstServer(t *testing.T) {
	old := `{
  "mcp": {
    // my other server
    // runs nothing
    "other": { "type": "local", "command": ["true"] },
  },
}
`
	next, _, err := updateOpencodeJSONC([]byte(old), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	s := string(next)
	if d, c := strings.Index(s, `"deja"`), strings.Index(s, "// my other server"); d < 0 || d > c {
		t.Fatalf("deja's entry sits under the reader's comment:\n%s", s)
	}
	if !strings.Contains(s, "// runs nothing\n    \"other\"") {
		t.Fatalf("the comment came apart from its server:\n%s", s)
	}
	back, _, err := updateOpencodeJSONC(next, "/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != old {
		t.Fatalf("uninstall did not give the file back:\n%s", back)
	}

	// Inside a block comment a line starting with // is not a comment of its
	// own, and the entry must not land in the middle of it.
	blockOld := "{\n  \"mcp\": {\n    /* servers\n    // old\n    */\n    \"other\": {\"type\":\"local\",\"command\":[\"true\"]}\n  }\n}\n"
	next, _, err = updateOpencodeJSONC([]byte(blockOld), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if i, j := bytes.Index(next, []byte(`"deja"`)), bytes.Index(next, []byte("*/")); i < j {
		t.Fatalf("deja's entry went inside a block comment:\n%s", next)
	}

	// A comment the reader wrote on deja's own entry stays on it when the
	// entry is rewritten, rather than moving to the next server.
	ownOld := "{\n  \"mcp\": {\n    // deja: my memory\n    \"deja\": {\"type\":\"local\",\"command\":[\"/old/deja\",\"mcp\"]},\n    \"a\": {\"type\":\"local\"}\n  }\n}\n"
	next, _, err = updateOpencodeJSONC([]byte(ownOld), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(next), "// deja: my memory\n    \"deja\"") {
		t.Fatalf("the comment on deja's entry moved off it:\n%s", next)
	}

	// The first code line starts inside a block comment that closes on it:
	// the entry goes above the comment, where a parser reads it.
	inOld := "{\n  \"mcp\": {\n    /* start\n    end */ \"a\": {\"type\":\"local\"}\n  }\n}\n"
	next, _, err = updateOpencodeJSONC([]byte(inOld), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(jsoncToJSON(string(next))), &root); err != nil {
		t.Fatalf("%v:\n%s", err, next)
	}
	if mcp, _ := root["mcp"].(map[string]any); mcp["deja"] == nil {
		t.Fatalf("deja's entry went into a comment:\n%s", next)
	}
}

// opencode's config is JSONC whatever it is called: the text writer was picked
// by the file's extension, so the same content in `opencode.json` was decoded
// strictly and the target refused — the reader who annotated their config
// could not install deja at all (#1664, the half #1663 left).
func TestOpencodeReadsTheContentNotTheExtension(t *testing.T) {
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		t.Run(name, func(t *testing.T) {
			hermeticEnv(t)
			dir := filepath.Join(opencodeConfigHome(), "opencode")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, name)
			before := "{\n  // the theme is deliberate\n  \"theme\": \"opencode\",\n  \"mcp\": {}\n}\n"
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := captureRun(t, "install", "opencode", "--no-index"); err != nil {
				t.Fatalf("a commented config was refused: %v", err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), "// the theme is deliberate") {
				t.Errorf("the comment was dropped:\n%s", b)
			}
			if !strings.Contains(string(b), `"deja"`) {
				t.Errorf("the entry was not written:\n%s", b)
			}

			// And out again, leaving the reader's file as it was.
			if _, err = captureRun(t, "uninstall", "opencode"); err != nil {
				t.Fatal(err)
			}
			b, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), `"deja"`) {
				t.Errorf("the entry survived the uninstall:\n%s", b)
			}
			if !strings.Contains(string(b), "// the theme is deliberate") {
				t.Errorf("the comment was dropped on the way out:\n%s", b)
			}
		})
	}
}

// A block written inline has no end line to find, so the entry landed after
// its closing brace and the config stopped parsing — with install reporting
// success (#2777). An empty inline block is what a config nobody has added a
// server to looks like.
func TestOpencodeOpensAnInlineBlockRatherThanWritingPastIt(t *testing.T) {
	for _, before := range []string{
		"{\n  \"theme\": \"opencode\",\n  \"mcp\": {}\n}\n",
		"{\n  \"mcp\": {\"other\": {\"type\":\"local\",\"command\":[\"x\"]}}\n}\n",
		// The block spans lines, but its first server sits on the opening one.
		"{\n  \"mcp\": { \"other\": {\"type\":\"local\",\"command\":[\"x\"]}\n  }\n}\n",
		// A comment after the opening brace is not a server on that line.
		"{\n  \"mcp\": { // my servers\n    \"other\": {\"type\":\"local\",\"command\":[\"x\"]}\n  }\n}\n",
		"{\n  // annotated\n  \"mcp\": {}\n}\n",
	} {
		t.Run(strings.TrimSpace(strings.SplitN(before, "\n", 3)[1]), func(t *testing.T) {
			hermeticEnv(t)
			dir := filepath.Join(opencodeConfigHome(), "opencode")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "opencode.jsonc")
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", "opencode", "--no-index"); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var probe map[string]any
			if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &probe); err != nil {
				t.Fatalf("the config no longer parses: %v\n%s", err, b)
			}
			mcp, _ := probe["mcp"].(map[string]any)
			if _, ok := mcp["deja"]; !ok {
				t.Errorf("the entry is not in the block:\n%s", b)
			}
			if strings.Contains(before, "other") {
				if _, ok := mcp["other"]; !ok {
					t.Errorf("the reader's own server was dropped:\n%s", b)
				}
			}
		})
	}
}

// The shapes the writer cannot edit are refused, and the file is left exactly
// as it was. Writing past a block it could not open is what made a config
// unreadable while install reported success (#2777).
func TestOpencodeLeavesAConfigItCannotEditAlone(t *testing.T) {
	for _, before := range []string{
		// A comment on the line the block opens and closes on: there is no
		// side of it the entry can go without changing what the reader wrote.
		"{\n  \"mcp\": {} // mine\n}\n",
		"{\n  \"mcp\": /* mine */ {}\n}\n",
		// A whole config on one line: the block would be spliced above the
		// object rather than inside it.
		"{\"theme\": \"x\"} // mine\n",
		"{} // mine\n",
	} {
		t.Run(strings.TrimSpace(before), func(t *testing.T) {
			hermeticEnv(t)
			dir := filepath.Join(opencodeConfigHome(), "opencode")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "opencode.json")
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", "opencode", "--no-index"); err == nil {
				t.Error("a config deja cannot edit was reported as installed")
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != before {
				t.Errorf("the config was written to anyway:\n%s", b)
			}
		})
	}
}

// An inline block with servers in it keeps its shape: the split has to fall on
// the brace that closes the block, not on the first one in the line. The
// comment is what sends it to the text writer: plain JSON is decoded.
func TestOpencodeSplitsAnInlineBlockAtItsOwnBrace(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(opencodeConfigHome(), "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "opencode.jsonc")
	before := "{\n  // servers\n  \"mcp\": {\"a\": {\"type\":\"local\",\"command\":[\"x\"]}}\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "opencode", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &probe); err != nil {
		t.Fatalf("the config no longer parses: %v\n%s", err, b)
	}
	if strings.Contains(string(b), "}}") {
		t.Errorf("the split fell inside the reader's own server:\n%s", b)
	}
}

// A config deja declines to edit is not written to on the way out either: the
// uninstall path rejoined the lines it had split while deciding, so a block
// commented out with /* … */ came back reshaped by a run that refused to
// touch it.
func TestUninstallLeavesAConfigItCannotEditByteIdentical(t *testing.T) {
	hermeticEnv(t)
	dir := filepath.Join(opencodeConfigHome(), "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "opencode.jsonc")
	before := "{\n  /* off\n  \"mcp\": {},\n  */\n  \"theme\": \"x\"\n}"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "opencode"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != before {
		t.Errorf("a config the uninstall did not edit was rewritten:\n%q\nwant\n%q", b, before)
	}
}

// deja ranks by project, and the project is a directory. The plugin took the
// server process's cwd, which is where opencode was launched rather than where
// the work is — In practice, the same question asked from the
// repository, from the home directory and from /tmp returned that project's
// sessions, another project's sessions, and nothing at all.
//
// opencode hands the plugin the session's own directory. Every call that ranks
// by project has to say so.
func TestOpencodePluginRunsInTheProjectDirectory(t *testing.T) {
	js := opencodePluginJS("/bin/deja")
	compact := strings.Join(strings.Fields(js), "")

	if !strings.Contains(compact, "ctx.location?.directory") {
		t.Error("the plugin does not read the instance's location")
	}
	if !strings.Contains(compact, "constcwd=ctx.location?.directory||process.cwd()") {
		t.Error("no fallback for a host that hands over no directory")
	}
	// Every call says where it is inside its payload, and hook-context is
	// also run from there.
	if !strings.Contains(compact, `runHook(["hook-context","--strict"],JSON.stringify({session_id:event.sessionID||"",parent_session_id:awaitparentOf(event.sessionID),cwd}),cwd)`) {
		t.Error("hook-context does not run in the project")
	}
	if !strings.Contains(compact, `session_id:sid,parent_session_id:parent,cwd}`) {
		t.Error("the per-prompt payload does not carry the project")
	}
	if !strings.Contains(compact, `session_id:event.sessionID||"",cwd,`) {
		t.Error("the spawn payload does not carry the project")
	}
}

// The opencode plugin caches what the session-start hook answered so it does
// not shell out on every turn. An empty answer was cached the same way, and its
// reasons are not alike: no history is permanent, while a locked index, a call
// that did not get through, or an upgrade replacing the binary are over by the
// next turn.
//
// Driven against the installed plugin with a deja whose first call failed and
// no build running, turns two and three stayed silent as well — the session
// lost its memory for good over one bad moment.
func TestOpencodePluginAsksAgainAfterAnEmptyAnswer(t *testing.T) {
	js := opencodePluginJS("/bin/deja")
	compact := strings.Join(strings.Fields(js), "")

	if !strings.Contains(compact, "constemptyRetries=") {
		t.Error("no bound on how often the session asks again")
	}
	if !strings.Contains(compact, "if(asks<emptyRetries)cache.delete(key)") {
		t.Error("the cached emptiness is never dropped, so the session cannot recover")
	}
	// Per session: one session's bad moment must not spend another's retries.
	if !strings.Contains(compact, "empties.set(key,asks)") {
		t.Error("the count is not kept per session")
	}
}

// stripJSONC removes what a .jsonc reader ignores — line comments, block
// comments and trailing commas — respecting string literals. Written out here
// rather than reusing the production helper: a test that shares the code it is
// checking would hide a bug in it.
func stripJSONC(text string) string {
	var b strings.Builder
	inString, escaped, inLine, inBlock := false, false, false, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				b.WriteByte(c)
			}
		case inBlock:
			if c == '*' && i+1 < len(text) && text[i+1] == '/' {
				inBlock, i = false, i+1
			}
		case inString:
			b.WriteByte(c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
		case c == '"':
			inString = true
			b.WriteByte(c)
		case c == '/' && i+1 < len(text) && text[i+1] == '/':
			inLine, i = true, i+1
		case c == '/' && i+1 < len(text) && text[i+1] == '*':
			inBlock, i = true, i+1
		default:
			b.WriteByte(c)
		}
	}
	return regexp.MustCompile(`,(\s*[}\]])`).ReplaceAllString(b.String(), "$1")
}

// parsesAsJSONC reports whether text is JSON once a .jsonc reader's liberties
// are taken away.
func parsesAsJSONC(t *testing.T, text string) error {
	t.Helper()
	var v any
	return json.Unmarshal([]byte(stripJSONC(text)), &v)
}

// The comma that joins deja's entry to the previous one was placed by walking
// backwards to the first line not already ending in a comma. In a file that
// uses trailing commas — which is what .jsonc is for — that scan runs past the
// last entry and lands on the line opening it, producing `"mine": {,` (#1695).
func TestOpencodeJSONCKeepsTrailingCommasValid(t *testing.T) {
	cases := map[string]string{
		"trailing commas": `{
  // my opencode config
  "theme": "system",
  "mcp": {
    "mine": {
      "type": "local",
      "command": ["my-server"],
    },
  },
}`,
		"no trailing comma": `{
  "mcp": {
    "mine": {
      "type": "local",
      "command": ["my-server"]
    }
  }
}`,
		"empty mcp block": `{
  "mcp": {
  }
}`,
		"block comment last in the block": `{
  "mcp": {
    "mine": {"type": "local", "command": ["my-server"]},
    /* a note
       over several lines
    */
  }
}`,
		"trailing comment on the last entry": `{
  "mcp": {
    "mine": {"type": "local", "command": ["my-server"]} // my server
  }
}`,
		"a // inside a string": `{
  "mcp": {
    "mine": {"type": "remote", "url": "https://example.com/my-server"}
  }
}`,
		"comment last in the block": `{
  "mcp": {
    "mine": {"type": "local", "command": ["my-server"]}
    // more to come
  }
}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			out, _, err := updateOpencodeJSONC([]byte(in+"\n"), "/usr/local/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			got := string(out)
			if err := parsesAsJSONC(t, got); err != nil {
				t.Errorf("install left a file that does not parse: %v\n%s", err, got)
			}
			if !strings.Contains(got, `"deja"`) {
				t.Errorf("deja's entry is missing:\n%s", got)
			}
			if strings.Contains(got, "{,") {
				t.Errorf("a comma landed on an opening brace:\n%s", got)
			}
			if strings.Contains(got, "*/,") || strings.Contains(got, "spanning,") {
				t.Errorf("a comma landed inside a block comment:\n%s", got)
			}
			// A comma written after a trailing comment goes with the comment,
			// and the two entries lose their separator.
			for l := range strings.SplitSeq(got, "\n") {
				if before, _, ok := strings.Cut(l, "//"); ok && !strings.Contains(before, `"`) && strings.HasSuffix(strings.TrimSpace(l), ",") {
					t.Errorf("a comma landed inside a line comment: %q\n%s", l, got)
				}
			}
			// Only where the fixture had one: the empty-block case has none.
			if strings.Contains(in, "my-server") && !strings.Contains(got, "my-server") {
				t.Errorf("the user's own server is gone:\n%s", got)
			}
		})
	}
}

// The per-prompt line never reached a person in opencode: the plugins kept the
// model's half of hook-prompt's reply and dropped systemMessage, while deja
// counted the line shown and held the next one back for twenty minutes. 1.x
// has a toast and shows it there; a 2.x server plugin has none and asks deja
// --strict, so the line is not spent. In both, a task sub-agent's turn, which
// the parent wrote, is not searched again.
func TestOpencodePluginsShowOrKeepThePersonsLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	const line = `deja-vu — from Codex CLI, 3 weeks ago: \"drain with --grace 40\" (via zorbit)`
	for _, shape := range []string{"1.x", "2.x"} {
		t.Run(shape, func(t *testing.T) {
			dir := t.TempDir()
			bin, calls, reply := filepath.Join(dir, "deja"), filepath.Join(dir, "calls"), filepath.Join(dir, "reply")
			if err := os.WriteFile(reply, []byte(`{"systemMessage":"`+line+`","hookSpecificOutput":{"additionalContext":"RECALL-ZORBIT"}}`+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\nin=$(cat)\nprintf '%s\\t%s\\n' \"$*\" \"$in\" >> " + calls + "\n" +
				"case \"$1:$in\" in hook-prompt:*zorbit*) cat " + reply + " ;; esac\n"
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			plugin := filepath.Join(dir, "deja.mjs")
			src, driver := opencodeLegacyPluginJS(bin), `
import { execSync } from "node:child_process";
import { DejaRecall } from "`+plugin+`";
const q = (v) => "'" + String(v).replace(/'/g, "'\\''") + "'";
const $ = (strings, ...values) => {
  const line = strings.reduce((acc, s, i) => acc + s + (i < values.length ? q(values[i]) : ""), "");
  const run = () => { try { return execSync(line, { shell: "/bin/sh" }).toString() } catch { return "" } };
  return { text: async () => run(), quiet: async () => { run() } };
};
const toasts = [];
const client = {
  tui: { showToast: async ({ body }) => { toasts.push(body.message) } },
  session: { get: async ({ path }) => ({ data: { parentID: path.id === "ses_C" ? "ses_P" : undefined } }) },
};
const h = await DejaRecall({ $, client, directory: "`+dir+`" });
const turn = (sid, text) => [{ info: { id: sid + "1", role: "user", sessionID: sid }, parts: [{ type: "text", text }] }];
const main = turn("ses_P", "how should the zorbit queue drain?");
await h["experimental.chat.messages.transform"]({ sessionID: "ses_P" }, { messages: main });
const child = turn("ses_C", "find how the zorbit queue drains");
await h["experimental.chat.messages.transform"]({ sessionID: "ses_C" }, { messages: child });
console.log(JSON.stringify({ toasts, main: main[0].parts[0].text, child: child[0].parts[0].text }));
`
			if shape == "2.x" {
				src, driver = opencodePluginJS(bin), `
import plugin from "`+plugin+`";
const hooks = { session: {}, tool: {} };
const domain = (name) => ({ hook: async (event, fn) => { (hooks[name][event] ||= []).push(fn) } });
const session = { ...domain("session"), get: async ({ sessionID }) => ({ parentID: sessionID === "ses_C" ? "ses_P" : undefined }) };
await plugin.setup({ location: { directory: "`+dir+`" }, session, tool: domain("tool") });
const turn = (sid, text) => [{ id: sid + "1", role: "user", content: [{ type: "text", text }] }];
const main = turn("ses_P", "how should the zorbit queue drain?");
await hooks.session.context[1]({ sessionID: "ses_P", system: [], messages: main });
const child = turn("ses_C", "find how the zorbit queue drains");
await hooks.session.context[1]({ sessionID: "ses_C", system: [], messages: child });
console.log(JSON.stringify({ toasts: [], main: main[0].content[0].text, child: child[0].content[0].text }));
`
			}
			if err := os.WriteFile(plugin, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			out := runNode(t, dir, driver)
			if !strings.Contains(out, `"main":"how should the zorbit queue drain?\n\nRECALL-ZORBIT"`) {
				t.Errorf("the session a person types in lost its recall:\n%s", out)
			}
			if !strings.Contains(out, `"child":"find how the zorbit queue drains"`) {
				t.Errorf("a sub-agent's turn was searched again:\n%s", out)
			}
			b, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			log := string(b)
			if !strings.Contains(log, `"prompt":"","session_id":"ses_C","parent_session_id":"ses_P"`) {
				t.Errorf("the sub-agent was not stamped live with an empty prompt:\n%s", log)
			}
			switch shape {
			case "1.x":
				if !strings.Contains(out, `"toasts":["deja-vu — from Codex CLI`) || strings.Count(out, "deja-vu —") != 1 {
					t.Errorf("the person's line was not toasted exactly once:\n%s", out)
				}
			case "2.x":
				if !strings.Contains(log, "hook-prompt --strict\t") {
					t.Errorf("hook-prompt was not told the line has no reader:\n%s", log)
				}
			}
		})
	}
}

// TestOpencodePluginShapeFollowsTheInstalledVersion pins the one thing both
// opencode majors agree on: each refuses the other's file. 1.18.32 rejects a
// default export that carries no server() ("Plugin .../deja.js must default
// export an object with server()"), and 2.0.12 rejects a module with no
// default export at all ("Missing key at [\"default\"]"). A machine still on
// 1.x therefore has to be written the named-export plugin.
func TestOpencodePluginShapeFollowsTheInstalledVersion(t *testing.T) {
	t.Cleanup(func() { opencodeVersionMajor = opencodeVersionMajorReal })

	opencodeVersionMajor = func() int { return 1 }
	v1 := opencodePluginJSFor("/bin/deja")
	if !strings.Contains(v1, "export const DejaRecall") {
		t.Errorf("opencode 1.x was written a plugin it cannot load:\n%s", v1)
	}
	if strings.Contains(v1, "export default") {
		t.Error("the 1.x plugin carries a default export, which 1.18.32 rejects unless it has server()")
	}

	opencodeVersionMajor = func() int { return 2 }
	v2 := opencodePluginJSFor("/bin/deja")
	if !strings.Contains(v2, `export default {`) || !strings.Contains(v2, `id: "deja-recall"`) {
		t.Errorf("opencode 2.x was written a plugin it cannot load:\n%s", v2)
	}
}

// TestOpencodeLegacyPluginKeepsItsHooks checks the 1.x template still wires
// what it used to, since it is now reachable only through the version fork and
// nothing else exercises it.
func TestOpencodeLegacyPluginKeepsItsHooks(t *testing.T) {
	js := opencodeLegacyPluginJS("/bin/deja")
	for _, want := range []string{
		"experimental.chat.system.transform",
		"experimental.chat.messages.transform",
		"experimental.session.compacting",
		"tool.execute.before",
		"tool.execute.after",
		"hook-context",
		"hook-prompt",
		"hook-tool-after",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("the 1.x plugin no longer wires %q", want)
		}
	}
}

// TestDoctorCallsTheWrongShapeStale covers the upgrade: the file stays where it
// was, names the hook it always named, and stops being loaded the day opencode
// crosses a major. Reported as wired it hides exactly the machine that just
// lost its memory.
func TestDoctorCallsTheWrongShapeStale(t *testing.T) {
	t.Cleanup(func() { opencodeVersionMajor = opencodeVersionMajorReal })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_OPENCODE_MAJOR", "1")

	opencodeVersionMajor = func() int { return 1 }
	if _, err := installOpencodePlugin("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	row := autoWirings()[0]
	if row.name != "opencode" {
		t.Fatalf("first auto-recall row is %q, not opencode", row.name)
	}
	if got, _ := autoWiringState(row); got != "wired" {
		t.Fatalf("a 1.x plugin on a 1.x machine reads %q, want wired", got)
	}

	opencodeVersionMajor = func() int { return 2 }
	if got, _ := autoWiringState(row); got != "stale" {
		t.Errorf("the 1.x plugin on a 2.x machine reads %q, want stale", got)
	}
}

// Both shapes have to carry the file line, and they are two separate templates:
// the 1.x hook table and the 2.x ctx.tool domain. The seam was scoped to a tool
// name in both, so on opencode a read produced nothing and everything deja
// knows at the point of an action reached every harness except this one.
func TestBothOpencodePluginsCarryTheFileLineBackFromARead(t *testing.T) {
	t.Cleanup(func() { opencodeVersionMajor = opencodeVersionMajorReal })
	for name, js := range map[string]string{
		"1.x": opencodeLegacyPluginJS("/bin/deja"),
		"2.x": opencodePluginJS("/bin/deja"),
	} {
		at := strings.Index(js, "execute.after")
		if at < 0 {
			t.Fatalf("%s: the plugin has no after-seam", name)
		}
		after := js[at:]
		if !strings.Contains(after, "filePath") {
			t.Errorf("%s: the after-seam does not read the path the tool was given", name)
		}
		if !strings.Contains(after, "hook-tool") {
			t.Errorf("%s: the after-seam never calls the point-of-action hook", name)
		}
		// Ahead of the gate that keeps the rest of the seam for the shell: the
		// whole failure of this channel was that a file action never got past it.
		shell := strings.Index(after, `!== "bash") return`)
		if shell < 0 {
			shell = strings.Index(after, `!== "shell" ||`)
		}
		if at := strings.Index(after, "fpath"); at < 0 || (shell >= 0 && at > shell) {
			t.Errorf("%s: the file branch sits behind the command gate", name)
		}
	}
}

// A 2.x plugin written before the hook deadline loads and calls deja, so it
// read as wired, while a stuck hook could still hold the turn (#4951). Doctor
// sends it back to install.
func TestDoctorCallsA2xPluginWithoutTheDeadlineStale(t *testing.T) {
	t.Cleanup(func() { opencodeVersionMajor = opencodeVersionMajorReal })
	opencodeVersionMajor = func() int { return 2 }
	js := opencodePluginJS("/bin/deja")
	if opencodePluginShapeStale(js) {
		t.Fatal("the plugin install writes today reads as stale")
	}
	old := strings.NewReplacer("HOOK_DEADLINE", "LIMIT").Replace(js)
	if !opencodePluginShapeStale(old) {
		t.Error("a 2.x plugin with no hook deadline reads as current")
	}
}

// A developer with opencode installed ran the suite and three tests went red:
// install asked the real `opencode --version`, which wrote its log under the
// test's home, and doctor judged the hand-written plugin by the installed major
// (#4155). The suite answers as a machine with no opencode unless a test says
// which one it wants.
func TestTheSuiteNeverAsksTheOpencodeOnPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in is a shell script")
	}
	dir := t.TempDir()
	asked := filepath.Join(dir, "asked")
	stub := "#!/bin/sh\ntouch '" + asked + "'\necho 2.0.18\n"
	if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if got := opencodeVersionMajor(); got != 0 {
		t.Errorf("the suite read opencode %d from PATH, want 0", got)
	}
	if _, err := os.Stat(asked); err == nil {
		t.Error("the suite ran the opencode on PATH")
	}
}

// With the suite pinned to "no opencode", nothing else runs the probe itself:
// the PATH lookup and the version it parses are checked here, against a
// stand-in, so the pin does not hide a broken probe.
func TestTheOpencodeMajorIsReadFromTheBinaryOnPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in is a shell script")
	}
	t.Setenv("DEJA_OPENCODE_MAJOR", "")
	for out, want := range map[string]int{"2.0.18": 2, "opencode 1.14.3": 1, "dev": 0} {
		dir := t.TempDir()
		stub := "#!/bin/sh\necho '" + out + "'\n"
		if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir)
		if got := opencodeVersionMajorReal(); got != want {
			t.Errorf("opencode printing %q reads as major %d, want %d", out, got, want)
		}
	}
	t.Setenv("PATH", t.TempDir())
	if got := opencodeVersionMajorReal(); got != 0 {
		t.Errorf("no opencode on PATH reads as major %d, want 0", got)
	}
	t.Setenv("DEJA_OPENCODE_MAJOR", "1")
	if got := opencodeVersionMajorReal(); got != 1 {
		t.Errorf("DEJA_OPENCODE_MAJOR=1 reads as major %d", got)
	}
}

// opencode builds every model call afresh from its store, so recall added to
// the user's message lasted one call: after the agent's first tool call it was
// gone, and deja, having shown it once, would not send it again (#4786). Each
// plugin is driven here through three calls the way opencode makes them: the
// turn's first, the one after a tool ran, and the next turn's.
func TestOpencodePromptRecallOutlivesTheFirstCall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	for _, shape := range []string{"1.x", "2.x"} {
		t.Run(shape, func(t *testing.T) {
			dir := t.TempDir()
			bin, calls := filepath.Join(dir, "deja"), filepath.Join(dir, "calls")
			// Like deja: a block it has shown in this session is not sent again.
			shown := filepath.Join(dir, "shown")
			script := "#!/bin/sh\nin=$(cat)\nprintf '%s %s\\n' \"$1\" \"$in\" >> " + calls + "\n" +
				"if [ \"$1\" = hook-prompt ] && [ ! -e " + shown + " ]; then touch " + shown +
				"; echo '{\"hookSpecificOutput\":{\"additionalContext\":\"RECALL-DANA\"}}'; fi\n"
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			plugin := filepath.Join(dir, "deja.mjs")
			src, driver := opencodeLegacyPluginJS(bin), `
import { execSync } from "node:child_process";
import { DejaRecall } from "`+plugin+`";
const q = (v) => "'" + String(v).replace(/'/g, "'\\''") + "'";
const $ = (strings, ...values) => {
  const line = strings.reduce((acc, s, i) => acc + s + (i < values.length ? q(values[i]) : ""), "");
  const run = () => { try { return execSync(line, { shell: "/bin/sh" }).toString() } catch { return "" } };
  return { text: async () => run(), quiet: async () => { run() } };
};
const h = await DejaRecall({ $, client: { tui: { showToast: async () => {} } }, directory: "`+dir+`" });
const user = (id, text) => ({ info: { id, role: "user", sessionID: "ses_P" }, parts: [{ type: "text", text }] });
const reply = { info: { id: "m2", role: "assistant", sessionID: "ses_P" }, parts: [{ type: "text", text: "reading go.mod" }] };
const call = async (msgs) => { await h["experimental.chat.messages.transform"]({ sessionID: "ses_P" }, { messages: msgs }); return msgs };
const a = await call([user("m1", "who reviews the pgx PR?")]);
const b = await call([user("m1", "who reviews the pgx PR?"), reply]);
const c = await call([user("m1", "who reviews the pgx PR?"), reply, user("m3", "and the bulk import?")]);
console.log(JSON.stringify([a[0].parts[0].text, b[0].parts[0].text, c[0].parts[0].text, c[2].parts[0].text]));
`
			if shape == "2.x" {
				src, driver = opencodePluginJS(bin), `
import plugin from "`+plugin+`";
const hooks = { session: {}, tool: {} };
const domain = (name) => ({ hook: async (event, fn) => { (hooks[name][event] ||= []).push(fn) } });
await plugin.setup({ location: { directory: "`+dir+`" }, session: domain("session"), tool: domain("tool") });
const user = (id, text) => ({ id, role: "user", content: [{ type: "text", text }] });
const reply = { id: "m2", role: "assistant", content: [{ type: "text", text: "reading go.mod" }] };
const call = async (msgs) => { await hooks.session.context[1]({ sessionID: "ses_P", system: [], messages: msgs }); return msgs };
const a = await call([user("m1", "who reviews the pgx PR?")]);
const b = await call([user("m1", "who reviews the pgx PR?"), reply]);
const c = await call([user("m1", "who reviews the pgx PR?"), reply, user("m3", "and the bulk import?")]);
console.log(JSON.stringify([a[0].content[0].text, b[0].content[0].text, c[0].content[0].text, c[2].content[0].text]));
`
			}
			if err := os.WriteFile(plugin, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			out := runNode(t, dir, driver)
			for i, when := range []string{"on the turn's first call", "after the agent ran a tool", "on the next turn"} {
				if strings.Count(strings.SplitN(out, `","`, 4)[i], "RECALL-DANA") != 1 {
					t.Errorf("%s the question does not carry its recall exactly once:\n%s", when, out)
				}
			}
			if strings.Contains(strings.SplitN(out, `","`, 4)[3], "RECALL-DANA") {
				t.Errorf("the next question was handed the first one's recall:\n%s", out)
			}
			if n := len(stubCalls(calls, "hook-prompt")); n != 2 {
				t.Errorf("hook-prompt ran %d times for two questions over three calls", n)
			}
		})
	}
}

// A user message carried the per-prompt block six times over a multi-step
// turn, alternating between two pointers (#5682). The plugins appended on
// every model call and only checked the text did not already end with their
// own block. Driven here the way that happens: a host that keeps the message
// between calls, a message with no id, two copies of the plugin, and a
// message that already carries a pile. The stub answers a different pointer
// on every call, as deja does once it has shown the first one.
func TestOpencodePromptRecallDoesNotPileUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	for _, shape := range []string{"1.x", "2.x"} {
		t.Run(shape, func(t *testing.T) {
			dir := t.TempDir()
			bin, calls := filepath.Join(dir, "deja"), filepath.Join(dir, "calls")
			script := "#!/bin/sh\nin=$(cat)\nprintf '%s %s\\n' \"$1\" \"$in\" >> " + calls + "\n" +
				"if [ \"$1\" = hook-prompt ]; then n=$(grep -c '^hook-prompt' " + calls + "); " +
				"if [ $((n % 2)) = 1 ]; then p=POINTER-A; else p=POINTER-B; fi; " +
				"printf '%s\\n' '{\"hookSpecificOutput\":{\"additionalContext\":\"<deja-recall>\\nRecalled history.\\n'$p'\\n</deja-recall>\"}}'; fi\n"
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			plugin := filepath.Join(dir, "deja.mjs")
			src, driver := opencodeLegacyPluginJS(bin), `
import { execSync } from "node:child_process";
import { DejaRecall } from "`+plugin+`";
const q = (v) => "'" + String(v).replace(/'/g, "'\\''") + "'";
const $ = (strings, ...values) => {
  const line = strings.reduce((acc, s, i) => acc + s + (i < values.length ? q(values[i]) : ""), "");
  const run = () => { try { return execSync(line, { shell: "/bin/sh" }).toString() } catch { return "" } };
  return { text: async () => run(), quiet: async () => { run() } };
};
const make = async () => {
  const h = await DejaRecall({ $, client: { tui: { showToast: async () => {} } }, directory: "`+dir+`" });
  return (msgs) => h["experimental.chat.messages.transform"]({ sessionID: "ses_P" }, { messages: msgs });
};
const user = (id, text) => ({ info: { ...(id ? { id } : {}), role: "user", sessionID: "ses_P" }, parts: [{ type: "text", text }] });
const text = (m) => m.parts[0].text;
`
			if shape == "2.x" {
				src, driver = opencodePluginJS(bin), `
import plugin from "`+plugin+`";
const make = async () => {
  const hooks = { session: {}, tool: {} };
  const domain = (name) => ({ hook: async (event, fn) => { (hooks[name][event] ||= []).push(fn) } });
  await plugin.setup({ location: { directory: "`+dir+`" }, session: domain("session"), tool: domain("tool") });
  return (msgs) => hooks.session.context[1]({ sessionID: "ses_P", system: [], messages: msgs });
};
const user = (id, text) => ({ ...(id ? { id } : {}), role: "user", content: [{ type: "text", text }] });
const text = (m) => m.content[0].text;
`
			}
			driver += `
const out = {};
// One copy, a message with no id, kept by the host across six calls.
{
  const call = await make();
  const msgs = [user("", "why does the uploader retry so fast?")];
  for (let i = 0; i < 6; i++) await call(msgs);
  out.kept = text(msgs[0]);
}
// Two copies of the plugin on the same kept message.
{
  const a = await make(), b = await make();
  const msgs = [user("m7", "what did we settle on the importer?")];
  for (let i = 0; i < 3; i++) { await a(msgs); await b(msgs) }
  out.twice = text(msgs[0]);
}
// A message that already carries a pile from before.
{
  const call = await make();
  let pile = "how is the cache keyed?";
  for (let i = 0; i < 6; i++) pile += "\n\n<deja-recall>\nRecalled history.\nPOINTER-" + (i % 2 ? "B" : "A") + "\n</deja-recall>";
  const msgs = [user("m9", pile)];
  await call(msgs);
  out.pile = text(msgs[0]);
}
console.log(JSON.stringify(out));
`
			if err := os.WriteFile(plugin, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			var got map[string]string
			if err := json.Unmarshal([]byte(runNode(t, dir, driver)), &got); err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"kept", "twice", "pile"} {
				if n := strings.Count(got[k], "<deja-recall>"); n != 1 {
					t.Errorf("%s: the message carries %d recall blocks, want one:\n%s", k, n, got[k])
				}
			}
			if !strings.HasPrefix(got["pile"], "how is the cache keyed?\n\n<deja-recall>") || !strings.Contains(got["pile"], "POINTER-A") {
				t.Errorf("the pile did not collapse to its first block:\n%s", got["pile"])
			}
			// One ask per question: the kept one and the twice-loaded one. The
			// pile is answered by what it already carries.
			if n := len(stubCalls(calls, "hook-prompt")); n != 2 {
				t.Errorf("hook-prompt ran %d times for two new questions", n)
			}
		})
	}
}

// The opencode plugin stamps every session live through hook-context and
// hook-prompt, and nothing ever cleared the stamp: a session that had just
// ended stayed out of the next one's MCP recall for twenty minutes (#4546).
// opencode 1.18.33 publishes session.idle to a plugin's event hook when a turn
// is over, and awaits a plugin's dispose before `opencode run` exits, which an
// event handler it does not wait for cannot be trusted to finish.
func TestOpencodePluginEndsTheSessionsItStamped(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin opencode would run")
	}
	dir := t.TempDir()
	plugin := filepath.Join(dir, "deja.mjs")
	if err = os.WriteFile(plugin, []byte(opencodeLegacyPluginJS("/usr/local/bin/deja")), 0o644); err != nil {
		t.Fatal(err)
	}
	// Bun's $ stood in for by a tag that writes down each command line.
	driver := `
import { DejaRecall } from "./deja.mjs";
const ran = [];
const $ = (strings, ...values) => {
  ran.push(strings.reduce((acc, s, i) => acc + s + (i < values.length ? String(values[i]) : ""), ""));
  return { text: async () => "", quiet: async () => {} };
};
const client = { tui: { showToast: async () => {} } };
const hooks = await DejaRecall({ $, client, directory: "/w/p" });
await hooks["experimental.chat.system.transform"]({ sessionID: "ses_F" }, { system: [] });
await hooks["experimental.chat.messages.transform"]({ sessionID: "ses_F" },
  { messages: [{ info: { role: "user", sessionID: "ses_F" }, parts: [{ type: "text", text: "the retry loop" }] }] });
ran.length = 0;
if (typeof hooks.event !== "function") { console.log("NOEVENT"); process.exit(0) }
await hooks.event({ event: { type: "session.status", properties: { sessionID: "ses_F", status: { type: "busy" } } } });
console.log("busy:" + ran.filter((c) => c.includes("hook-session-end")).length);
await hooks.event({ event: { type: "session.idle", properties: { sessionID: "ses_F" } } });
console.log("idle:" + JSON.stringify(ran.filter((c) => c.includes("hook-session-end"))));
await hooks["experimental.chat.messages.transform"]({ sessionID: "ses_G" },
  { messages: [{ info: { role: "user", sessionID: "ses_G" }, parts: [{ type: "text", text: "the flaky test" }] }] });
ran.length = 0;
if (typeof hooks.dispose !== "function") { console.log("NODISPOSE"); process.exit(0) }
await hooks.dispose();
console.log("dispose:" + JSON.stringify(ran.filter((c) => c.includes("hook-session-end"))));
`
	run := filepath.Join(dir, "drive.mjs")
	if err = os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), node, run)
	if os.Getenv("DEJA_PROMPT_CACHE_FILE") == "" {
		cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+filepath.Join(dir, ".xdg-cache"))
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	got := string(out)
	if strings.Contains(got, "NOEVENT") || strings.Contains(got, "NODISPOSE") {
		t.Fatalf("the plugin has no event or dispose hook, so a session it stamped live is never ended:\n%s", got)
	}
	// A busy status is the turn starting, not ending.
	if !strings.Contains(got, "busy:0") {
		t.Errorf("a busy status ended the session:\n%s", got)
	}
	// idle ends ses_F; dispose ends only what is still live, ses_G, and does
	// not spawn deja for ses_F a second time.
	for _, c := range []struct{ phase, ends, not string }{{"idle:", "ses_F", ""}, {"dispose:", "ses_G", "ses_F"}} {
		line := ""
		for l := range strings.SplitSeq(got, "\n") {
			if strings.HasPrefix(l, c.phase) {
				line = l
			}
		}
		if !strings.Contains(line, `\"session_id\":\"`+c.ends+`\"`) || !strings.Contains(line, "/usr/local/bin/deja") {
			t.Errorf("%s did not end %s through hook-session-end: %q\n%s", c.phase, c.ends, line, got)
		}
		if c.not != "" && strings.Contains(line, c.not) {
			t.Errorf("%s ended %s again: %q", c.phase, c.not, line)
		}
	}
}

// The spawn branch built its payload as a JSON string and stringified it again
// on the way into the shell, so deja was handed a quoted string where it
// expects an object. It reads nothing out of that and answers with silence — a
// spawned agent in opencode started with no memory at all, and nothing said so.
//
// Driven against the installed plugin with a deja that answers only for an
// object: before, the subagent's prompt came back unchanged; after, it carries
// the block.
func TestOpencodeSpawnPayloadIsEncodedOnce(t *testing.T) {
	js := opencodePluginJS("/bin/deja")
	compact := strings.Join(strings.Fields(js), "")

	if strings.Contains(compact, "JSON.stringify(JSON.stringify(") {
		t.Error("the payload is encoded twice")
	}
	if !strings.Contains(compact, "constpayload={") {
		t.Error("the payload is not built as an object")
	}
	if !strings.Contains(compact, `runHook("hook-tool",JSON.stringify(payload))`) {
		t.Error("the payload is not encoded on the way into the hook's stdin")
	}
	// What deja is handed has to parse as an object with the fields the hook
	// reads. The generated source is not JSON, so this checks the shape the
	// plugin assembles rather than the literal.
	for _, field := range []string{`hook_event_name:"PreToolUse"`, `tool_name:"Task"`, "tool_input:{prompt:args.prompt}"} {
		if !strings.Contains(compact, field) {
			t.Errorf("the payload lost %s", field)
		}
	}
	// And the hook's own contract, so a rename on either side is caught here.
	var probe struct {
		HookEventName string `json:"hook_event_name"`
		ToolName      string `json:"tool_name"`
	}
	if err := json.Unmarshal([]byte(`{"hook_event_name":"PreToolUse","tool_name":"Task"}`), &probe); err != nil {
		t.Fatal(err)
	}
	if probe.HookEventName != "PreToolUse" || probe.ToolName != "Task" {
		t.Fatal("the field names this test pins are not the ones deja reads")
	}
}

// The opencode plugin must fold its recall into the first system block. An
// OpenAI-compatible endpoint that requires the system message to come first
// rejects a request carrying a second one, and installing deja then made every
// turn fail: "Not Found: System message must be at the beginning." Reproduced
// against a local model, and fixed by merging rather than appending. The
// recall goes after opencode's own prompt: that prompt is the same in every
// session, and putting anything in front of it costs the provider's prompt
// cache on every session's first request.
func TestOpencodePluginDoesNotAppendASecondSystemBlock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// The shape is picked from the installed opencode, and the test is about
	// the 2.x one.
	t.Setenv("DEJA_OPENCODE_MAJOR", "2")

	if _, err := installOpencodePlugin("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config", "opencode", "plugins", "deja.js")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	compact := strings.Join(strings.Fields(src), "")
	if !strings.Contains(compact, `if(event.system.length)event.system[0].text=event.system[0].text+"\n\n"+digest`) {
		t.Fatal("plugin no longer folds the recall into the first system block")
	}
	// The push only runs when there is no system block at all, so a second
	// block never lands behind an existing one.
	if !strings.Contains(compact, `elseevent.system.push({type:"text",text:digest})`) {
		t.Fatal("the push is not guarded by emptiness")
	}
}

// The 1.x plugin, which Kilo CLI loads too (#4398), keeps the same order:
// opencode's own prompt first, the digest after it.
func TestLegacyOpencodePluginAppendsAfterTheSystemPrompt(t *testing.T) {
	for _, target := range []string{"opencode-auto", "kilo-auto"} {
		compact := strings.Join(strings.Fields(legacyPluginJSFor(target, "/usr/local/bin/deja")), "")
		if !strings.Contains(compact, `if(output.system.length)output.system[0]=output.system[0]+"\n\n"+ctx`) {
			t.Fatalf("%s: the digest no longer goes after opencode's own system prompt", target)
		}
	}
}

// The title request runs the system transform as well, and the 1.x plugin put
// the session digest into the title generator's prompt (#4795). Kilo sends it
// under "title-<session>"; opencode 1.18 under the session's own id, with the
// title agent's prompt first. Both shapes are driven here next to a session
// request that has to keep its digest.
func TestTheTitleRequestGetsNoDigest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	for _, target := range []string{"opencode-auto", "kilocode-auto"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "deja")
			script := "#!/bin/sh\ncat >/dev/null\n" +
				"if [ \"$1\" = hook-context ]; then echo '{\"hookSpecificOutput\":{\"additionalContext\":\"DIGEST-MARK\"}}'; fi\n"
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			plugin := filepath.Join(dir, "deja.mjs")
			if err := os.WriteFile(plugin, []byte(legacyPluginJSFor(target, bin)), 0o644); err != nil {
				t.Fatal(err)
			}
			driver := `
import { execSync } from "node:child_process";
import { DejaRecall } from "` + plugin + `";
const q = (v) => "'" + String(v).replace(/'/g, "'\\''") + "'";
const $ = (strings, ...values) => {
  const line = strings.reduce((acc, s, i) => acc + s + (i < values.length ? q(values[i]) : ""), "");
  const run = () => { try { return execSync(line, { shell: "/bin/sh" }).toString() } catch { return "" } };
  return { text: async () => run(), quiet: async () => { run() } };
};
const h = await DejaRecall({ $, client: { tui: { showToast: async () => {} } }, directory: "` + dir + `" });
const call = async (sessionID, first) => { const o = { system: [first] }; await h["experimental.chat.system.transform"]({ sessionID }, o); return o.system.join("|") };
console.log(JSON.stringify([
  await call("title-ses_K", "You are Kilo."),
  await call("ses_O", "You are a title generator. You output ONLY a thread title. Nothing else."),
  await call("ses_O", "You are opencode, an interactive CLI tool."),
]));
`
			out := runNode(t, dir, driver)
			parts := strings.SplitN(out, `","`, 3)
			if len(parts) != 3 {
				t.Fatalf("driver printed %q", out)
			}
			for i, what := range []string{"Kilo's title request", "opencode's title request"} {
				if strings.Contains(parts[i], "DIGEST-MARK") {
					t.Errorf("%s carried the session digest: %s", what, parts[i])
				}
			}
			if !strings.Contains(parts[2], "DIGEST-MARK") {
				t.Errorf("the session's own request lost its digest: %s", out)
			}
		})
	}
}

// opencodeStubDeja is a deja that writes down each subcommand and its stdin.
func opencodeStubDeja(t *testing.T, dir string) (bin, calls string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	bin, calls = filepath.Join(dir, "deja"), filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s %s\\n' \"$1\" \"$(cat)\" >> " + calls + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

func runNode(t *testing.T, dir, driver string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin opencode would run")
	}
	run := filepath.Join(dir, "drive.mjs")
	if err = os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), node, run)
	if os.Getenv("DEJA_PROMPT_CACHE_FILE") == "" {
		cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+filepath.Join(dir, ".xdg-cache"))
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	return string(out)
}

func stubCalls(calls, sub string) []string {
	b, _ := os.ReadFile(calls)
	var out []string
	for l := range strings.SplitSeq(string(b), "\n") {
		if strings.HasPrefix(l, sub+" ") {
			out = append(out, l)
		}
	}
	return out
}

// The 2.x plugin registered context, compaction and tool hooks and nothing
// else, so the #4546 session end never ran there (#4571). 2.x hands a plugin
// its events through ctx.event.subscribe and runs the cleanup setup returns;
// opencode 2.0.22 ends a turn with session.execution.succeeded, .failed or
// .interrupted, and `opencode run` awaits the cleanup before it exits.
func TestOpencode2PluginEndsTheSessionsItStamped(t *testing.T) {
	dir := t.TempDir()
	bin, calls := opencodeStubDeja(t, dir)
	plugin := filepath.Join(dir, "deja.mjs")
	if err := os.WriteFile(plugin, []byte(opencodePluginJS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runNode(t, dir, `
import plugin from "`+plugin+`";
const hooks = { session: {}, tool: {} };
const domain = (name) => ({ hook: async (event, fn) => { (hooks[name][event] ||= []).push(fn) } });
let give = null, open = false;
const subscribe = (options) => ({
  async *[Symbol.asyncIterator]() {
    open = true;
    try {
      while (!options?.signal?.aborted) {
        const next = await new Promise((resolve) => { give = resolve; options?.signal?.addEventListener("abort", () => resolve(null), { once: true }) });
        if (!next) return;
        yield next;
      }
    } finally { open = false }
  },
});
const push = async (e) => { for (let i = 0; !give && i < 200; i++) await new Promise((r) => setTimeout(r, 5)); if (!give) return; const g = give; give = null; g(e); await new Promise((r) => setTimeout(r, 300)) };
const ctx = { location: { directory: "`+dir+`" }, session: domain("session"), tool: domain("tool"), event: { subscribe } };
const cleanup = await plugin.setup(ctx);
for (const fn of hooks.session.context) {
  await fn({ sessionID: "ses_F", system: [], messages: [{ role: "user", content: [{ type: "text", text: "the retry loop" }] }] });
}
await push({ type: "session.execution.started", data: { sessionID: "ses_F" } });
console.log("started");
await push({ type: "session.execution.succeeded", data: { sessionID: "ses_F" } });
console.log("succeeded");
console.log("cleanup:" + typeof cleanup);
if (typeof cleanup === "function") await cleanup();
console.log("open:" + open);
`)
	ended := stubCalls(calls, "hook-session-end")
	if !strings.Contains(out, "cleanup:function") {
		t.Errorf("setup returned no cleanup, so `opencode run` exits with the session stamped:\n%s", out)
	}
	if len(ended) == 0 || !strings.Contains(ended[0], `"session_id":"ses_F"`) {
		t.Fatalf("the end of the turn did not end ses_F through hook-session-end: %q\n%s", ended, out)
	}
	if strings.Contains(out, "open:true") {
		t.Errorf("the event subscription outlived the plugin:\n%s", out)
	}
}

// The plugins end a session at the end of its turn and count on the next
// prompt's hook-prompt to stamp it live again. A turn that is only an image
// returned before that call, so its MCP recall could hand the session back to
// itself (#4573).
func TestOpencodePluginsStampAnImageOnlyTurn(t *testing.T) {
	dir := t.TempDir()
	bin, calls := opencodeStubDeja(t, dir)
	v1 := filepath.Join(dir, "deja1.mjs")
	if err := os.WriteFile(v1, []byte(opencodeLegacyPluginJS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}
	v2 := filepath.Join(dir, "deja2.mjs")
	if err := os.WriteFile(v2, []byte(opencodePluginJS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}
	runNode(t, dir, `
import { execSync } from "node:child_process";
import { DejaRecall } from "`+v1+`";
import plugin from "`+v2+`";
// Bun's $ stood in for by a tag that runs the line through sh, each value
// one quoted word as Bun passes it.
const q = (v) => "'" + String(v).replace(/'/g, "'\\''") + "'";
const $ = (strings, ...values) => {
  const line = strings.reduce((acc, s, i) => acc + s + (i < values.length ? q(values[i]) : ""), "");
  const run = () => { try { return execSync(line, { shell: "/bin/sh" }).toString() } catch { return "" } };
  return { text: async () => run(), quiet: async () => { run() } };
};
const image = { type: "file", mime: "image/png", url: "data:image/png;base64,AAAA" };
const h1 = await DejaRecall({ $, client: { tui: { showToast: async () => {} } }, directory: "`+dir+`" });
await h1["experimental.chat.messages.transform"]({ sessionID: "ses_v1" }, { messages: [{ info: { role: "user", sessionID: "ses_v1" }, parts: [image] }] });
const hooks = { session: {}, tool: {} };
const domain = (name) => ({ hook: async (event, fn) => { (hooks[name][event] ||= []).push(fn) } });
await plugin.setup({ location: { directory: "`+dir+`" }, session: domain("session"), tool: domain("tool") });
await hooks.session.context[1]({ sessionID: "ses_v2", system: [], messages: [{ role: "user", content: [image] }] });
`)
	got := strings.Join(stubCalls(calls, "hook-prompt"), "\n")
	for _, id := range []string{"ses_v1", "ses_v2"} {
		if !strings.Contains(got, `"session_id":"`+id+`"`) {
			t.Errorf("an image-only turn in %s never reached hook-prompt, so it was not stamped live:\n%s", id, got)
		}
	}
}

// OpenCode V2 reads only the module's default export, and that export must
// carry an id and a setup (or effect) function. The V1 named-export shape —
// `export const DejaRecall = async ({ $, client, directory }) => ...` — left
// the loader with no default at all, and every load failed with
// "Plugin must export a default definition with an id and an effect or setup
// function. (cause: SchemaError(Missing key at ["default"]))" (ref
// err_bb76d60a). The plugin was installed and silently did nothing.
func TestOpencodePluginExportsV2Definition(t *testing.T) {
	js := opencodePluginJS("/bin/deja")
	compact := strings.Join(strings.Fields(js), "")

	for _, want := range []string{
		"exportdefault{",
		`id:"deja-recall"`,
		"asyncsetup(ctx)",
	} {
		if !strings.Contains(compact, want) {
			t.Errorf("generated plugin missing %q — the V2 loader rejects the module without it:\n%s", want, js)
		}
	}

	// The plugin must not import @opencode/plugin: a local plugin resolves
	// bare specifiers from its own directory, where the package is not
	// installed ("Cannot find package '@opencode/plugin' imported from
	// .../plugins/deja.js"). Plugin.define is the identity function, so the
	// plain { id, setup } object is the same contract with no dependency.
	if strings.Contains(compact, `from"@opencode/plugin"`) {
		t.Error("the plugin imports @opencode/plugin, which a local plugin cannot resolve")
	}

	// The V1 named export is exactly what the V2 loader cannot see. If it
	// rides along, a future reader may think the plugin still speaks V1.
	if strings.Contains(compact, "exportconstDejaRecall") {
		t.Error("the V1 named export is still there; the V2 loader only reads the default export")
	}

	// The digest still arrives in the recall envelope, so a rename on either
	// side is caught here rather than in a silently empty context.
	if !strings.Contains(compact, "hookSpecificOutput") {
		t.Error("the plugin no longer reads the recall envelope")
	}
}

func TestOpencodePluginAfterHookHandlesPatchInputAndStringResult(t *testing.T) {
	t.Cleanup(func() { opencodeVersionMajor = opencodeVersionMajorReal })

	opencodeVersionMajor = func() int { return 2 }
	v2 := opencodePluginJS("/bin/deja")
	if opencodePluginShapeStale(v2) {
		t.Fatal("generated 2.x plugin should not read as stale")
	}
	v2AfterAt := strings.Index(v2, "execute.after")
	if v2AfterAt < 0 {
		t.Fatal("2.x plugin missing execute.after")
	}
	v2After := v2[v2AfterAt:]
	for _, want := range []string{
		`includes("patch") ? event.input?.input || ""`,
		`typeof fres.content === "string"`,
		`typeof fres.output === "string"`,
		`typeof event.result === "string"`,
	} {
		if !strings.Contains(v2After, want) {
			t.Errorf("2.x execute.after missing %q", want)
		}
	}

	opencodeVersionMajor = func() int { return 1 }
	v1 := opencodeLegacyPluginJS("/bin/deja")
	v1AfterAt := strings.Index(v1, "tool.execute.after")
	if v1AfterAt < 0 {
		t.Fatal("1.x plugin missing tool.execute.after")
	}
	if !strings.Contains(v1[v1AfterAt:], `includes("patch") ? input?.args?.input || ""`) {
		t.Error("1.x tool.execute.after missing patch input extraction")
	}
}
