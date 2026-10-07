package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The bootstrap hook recalls once, against the session, and only in gateway
// mode. OpenClaw's plugin runtime has the other half: before_prompt_build is
// handed the prompt the user just typed and returns context that goes in front
// of the model, and it fires under `openclaw agent --local` too.
//
// Learned by running it against OpenClaw 2026.7.1-2:
//   - A plugin needs both package.json with openclaw.extensions and
//     openclaw.plugin.json. Either one alone fails the install with a
//     validation error rather than being ignored.
//   - plugins.allow is the user's trust list, and writing to it would be deja
//     deciding what the user trusts. Found only by scanning extensions/, the
//     plugin cost two warnings on every start: the open allow list, and no
//     install or load-path provenance. Named in plugins.load.paths it is
//     origin "config", which both checks accept (#4579).
//   - The entry under plugins.entries is what enables the plugin; extensions/
//     is a discovery root, so no `openclaw plugins install` step is needed.
const openclawPluginID = "deja"

func installOpenClawPlugin(exe string, uninstall bool) (installResult, error) {
	// The launcher, not this binary: a generated plugin is as much a
	// config as a hooks.json, and one that names the build it was
	// installed from stops working the day that build moves (#3682).
	exe = hookExeFor(exe, uninstall)
	dir := filepath.Join(sources.OpenClawStateDir(), "extensions", openclawPluginID)
	if uninstall {
		// Same as the hook pack beside it: say what happened, and take the
		// directories deja made above this one (#3698).
		had := isRealDir(dir)
		if err := os.RemoveAll(dir); err != nil {
			return installResult{}, err
		}
		if _, err := setOpenClawPluginEnabled(false); err != nil {
			return installResult{}, err
		}
		if _, err := setOpenClawPluginLoadPath(dir, false); err != nil {
			return installResult{}, err
		}
		if !had {
			return installResult{Path: dir, Action: "unchanged"}, nil
		}
		pruneCreatedDir(filepath.Dir(dir))
		return installResult{Path: dir, Action: "removed"}, nil
	}
	noteCreatedDirs(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return installResult{}, err
	}
	for _, f := range []struct{ name, body string }{
		{"package.json", openclawPluginPackage()},
		{"openclaw.plugin.json", openclawPluginManifest()},
	} {
		path := filepath.Join(dir, f.name)
		old, err := readConfig(path)
		if err != nil {
			return installResult{}, err
		}
		if _, err := writeIfChanged(path, old, []byte(f.body)); err != nil {
			return installResult{}, err
		}
	}
	entry := filepath.Join(dir, "index.mjs")
	old, err := readConfig(entry)
	if err != nil {
		return installResult{}, err
	}
	a, err := writeIfChanged(entry, old, []byte(openclawPluginJS(exe)))
	if err != nil {
		return installResult{}, err
	}
	// `openclaw plugins disable deja` writes enabled: false on the entry, and
	// install wrote it back on while reporting "unchanged" (#4472).
	var note string
	if openclawEntrySwitchedOff("plugins.entries", openclawPluginID) {
		note = "left deja's plugin switched off, the way it was — `openclaw plugins enable deja` turns it back on"
	} else if _, err := setOpenClawPluginEnabled(true); err != nil {
		return installResult{}, err
	}
	if _, err := setOpenClawPluginLoadPath(dir, true); err != nil {
		return installResult{}, err
	}
	// The manifest and package.json beside it are deja's own and went unnamed
	// on the screen whose job is saying what was touched, so the directory
	// rides along (#3254).
	return wroteAll(installResult{Path: entry, Action: a, Note: note},
		installResult{Path: dir, Action: a}), nil
}

// setOpenClawPluginEnabled adds or removes our entry under plugins.entries,
// leaving every other plugin — and the user's allow list — untouched.
func setOpenClawPluginEnabled(on bool) (string, error) {
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	old, err := readConfig(path)
	if err != nil {
		return "", err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		if !on {
			return "unchanged", nil
		}
		root = map[string]any{}
	} else if configIsJSONC(old) {
		// The same file the hook and the MCP entry are written into, and the
		// same reason not to refuse it over a comment (#2811).
		return setOpenClawEntryJSONC(path, old, "plugins.entries", openclawPluginID, "", on)
	} else if json.Unmarshal(old, &root) != nil {
		return "", openclawParseError(path, old)
	}
	plugins, _ := root["plugins"].(map[string]any)
	entries, _ := mapAt(plugins, "entries")
	if !on {
		if entries == nil {
			return "unchanged", nil
		}
		delete(entries, openclawPluginID)
		if len(entries) == 0 {
			delete(plugins, "entries")
		}
		if len(plugins) == 0 {
			delete(root, "plugins")
		}
	} else {
		if plugins == nil {
			plugins = map[string]any{}
			root["plugins"] = plugins
		}
		if entries == nil {
			entries = map[string]any{}
			plugins["entries"] = entries
		}
		entries[openclawPluginID] = map[string]any{"enabled": true}
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return "", err
	}
	next = append(next, '\n')
	return writeIfChanged(path, old, next)
}

// setOpenClawPluginLoadPath adds the plugin's directory to plugins.load.paths,
// or takes it out, beside whatever paths the user lists there (#4579).
func setOpenClawPluginLoadPath(dir string, on bool) (string, error) {
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	old, err := readConfig(path)
	if err != nil {
		return "", err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		if !on {
			return "unchanged", nil
		}
		root = map[string]any{}
	} else if err := json.Unmarshal([]byte(jsoncToJSON(string(old))), &root); err != nil {
		return "", configParseError(path, err)
	}
	plugins, _ := root["plugins"].(map[string]any)
	load, _ := mapAt(plugins, "load")
	// Something other than an object or a list there is a config deja does
	// not understand. The plugin loads from extensions/ without it, so the
	// cost of leaving it is the warning, not the recall.
	if _, ok := plugins["load"]; ok && load == nil {
		return "unchanged", nil
	}
	paths, isList := load["paths"].([]any)
	if _, ok := load["paths"]; ok && !isList {
		return "unchanged", nil
	}
	kept := make([]any, 0, len(paths)+1)
	for _, p := range paths {
		if s, _ := p.(string); openclawSamePath(s, dir) {
			continue
		}
		kept = append(kept, p)
	}
	had := len(kept) != len(paths)
	if on == had {
		return "unchanged", nil
	}
	if on {
		kept = append(kept, dir)
	}
	if configIsJSONC(old) {
		text, err := openclawLoadPathsJSONC(string(old), dir, on, len(kept), len(load), len(plugins), paths != nil)
		if err != nil {
			return "", configParseError(path, err)
		}
		return writeIfChanged(path, old, []byte(text))
	}
	switch {
	case len(kept) > 0:
		if plugins == nil {
			plugins = map[string]any{}
			root["plugins"] = plugins
		}
		if load == nil {
			load = map[string]any{}
			plugins["load"] = load
		}
		load["paths"] = kept
	case load != nil:
		delete(load, "paths")
		if len(load) == 0 {
			delete(plugins, "load")
		}
		if len(plugins) == 0 {
			delete(root, "plugins")
		}
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return "", err
	}
	return writeIfChanged(path, old, append(next, '\n'))
}

// openclawLoadPathsJSONC adds dir to plugins.load.paths in a config carrying
// comments, or takes it out. Only deja's element is written or cut, so the
// user's own entries keep their comments and commas, and an uninstall gives
// back the bytes the install found. A list left empty goes, with the blocks it
// was the only thing in.
func openclawLoadPathsJSONC(text, dir string, on bool, kept, loadKeys, pluginKeys int, haveKey bool) (string, error) {
	if !on && kept == 0 {
		dropFrom := 2
		if loadKeys == 1 {
			dropFrom = 1
			if pluginKeys == 1 {
				dropFrom = 0
			}
		}
		return jsoncRemoveKey(text, "plugins.load", "paths", dropFrom)
	}
	elem, err := json.Marshal(dir)
	if err != nil {
		return "", err
	}
	if !haveKey {
		return jsoncSetEntry(text, "plugins.load", "paths", "["+string(elem)+"]", false, 2)
	}
	open := zedTopLevelOpen(text)
	if open < 0 {
		return "", fmt.Errorf("does not look like a settings object")
	}
	block, have := walkJSONCKeys(text, open, []string{"plugins", "load"})
	if block == nil || have < 2 {
		return "", fmt.Errorf("plugins.load is not where it parsed")
	}
	at := jsoncListValue(text, block, "paths")
	if at == nil {
		return "", fmt.Errorf("plugins.load.paths is not where it parsed")
	}
	blank := stripJSONComments(text)
	// The last thing in the list that is not space or a comment.
	prevSig := func(i int) int {
		for i--; i > at[0]; i-- {
			if c := blank[i]; c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				break
			}
		}
		return i
	}
	if on {
		last := prevSig(at[1] - 1)
		switch blank[last] {
		case '[':
			return text[:last+1] + string(elem) + text[last+1:], nil
		case ',':
			// A trailing comma stays trailing.
			return text[:last+1] + " " + string(elem) + "," + text[last+1:], nil
		}
		return text[:last+1] + ", " + string(elem) + text[last+1:], nil
	}
	// The strings at the list's own level that name dir, last first so the
	// offsets of the ones before stay good.
	var hits [][2]int
	depth := 0
	for i := at[0] + 1; i < at[1]-1; i++ {
		switch blank[i] {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		case '"':
			end := zedStringEnd(text, i)
			if end < 0 {
				return "", fmt.Errorf("plugins.load.paths has an open string")
			}
			var s string
			if depth == 0 && json.Unmarshal([]byte(text[i:end]), &s) == nil && openclawSamePath(s, dir) {
				hits = append(hits, [2]int{i, end})
			}
			i = end - 1
		}
	}
	for k := len(hits) - 1; k >= 0; k-- {
		from, to := hits[k][0], hits[k][1]
		if p := prevSig(from); blank[p] == ',' {
			// The comma before it, and the space between.
			from = p
		} else {
			// The first element: the comma after it, and the space up to the
			// next one.
			j := to
			for j < at[1]-1 && (blank[j] == ' ' || blank[j] == '\t' || blank[j] == '\n' || blank[j] == '\r') {
				j++
			}
			if blank[j] == ',' {
				to = j + 1
				for to < at[1]-1 && (blank[to] == ' ' || blank[to] == '\t') {
					to++
				}
			}
		}
		text = text[:from] + text[to:]
		blank = stripJSONComments(text)
		at[1] -= to - from
	}
	return text, nil
}

// openclawSamePath reports whether a load path names dir, in the spellings
// OpenClaw resolves: absolute, or from the home directory.
func openclawSamePath(p, dir string) bool {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		p = filepath.Join(sources.Home(), rest)
	}
	return p != "" && filepath.Clean(p) == filepath.Clean(dir)
}

func openclawPluginPackage() string {
	return `{
  "name": "openclaw-deja",
  "version": "1.0.0",
  "type": "module",
  "private": true,
  "openclaw": {
    "extensions": ["./index.mjs"]
  }
}
`
}

func openclawPluginManifest() string {
	return `{
  "id": "` + openclawPluginID + `",
  "name": "deja recall",
  "description": "Recall the user's own past sessions for the question they just asked",
  "activation": {
    "onStartup": true
  },
  "contracts": {
    "agentToolResultMiddleware": ["openclaw"]
  },
  "configSchema": {
    "type": "object",
    "additionalProperties": false
  }
}
`
}

func openclawPluginJS(exe string) string {
	return fmt.Sprintf(`// generated by deja install — safe to delete; regenerate with: deja install openclaw-auto
import { execFileSync } from "node:child_process";

const DEJA = %q;

// Memory is optional: a hook that throws must never take the turn down with it.
function ask(args, payload) {
  try {
    return execFileSync(DEJA, args, {
      input: JSON.stringify(payload),
      encoding: "utf8",
      timeout: 10000,
      maxBuffer: 4 * 1024 * 1024,
      stdio: ["pipe", "pipe", "ignore"],
    }).trim();
  } catch {
    return "";
  }
}

// The agent's workspace is the directory its tools run in and the cwd its
// session header records; the gateway's own cwd is wherever it was started.
// The hooks name the workspace, so a compaction captured against the header is
// found again by the next prompt. The automatic compaction inside a run hands
// its hook only the session key, so what each key last ran as is kept here.
const seen = new Map();
function where(ctx) {
  const key = ctx?.sessionKey || "";
  const last = (key && seen.get(key)) || {};
  const id = ctx?.sessionId || last.id || "";
  const dir = ctx?.workspaceDir || last.dir || "";
  if (key && (id || dir)) seen.set(key, { id, dir });
  return { id: id || key, cwd: dir || process.cwd() };
}

function resultText(result) {
  const content = Array.isArray(result?.content) ? result.content : [];
  return content.filter((c) => c && c.type === "text" && typeof c.text === "string").map((c) => c.text).join("\n");
}

// The line deja has for a finished tool call: a file's history after a read
// or an edit, and after a failed command what this machine ran after the same
// error before.
function toolLine(event, ctx) {
  const name = event?.toolName || "";
  const args = event?.args || {};
  const session_id = ctx?.sessionId || ctx?.sessionKey || "";
  const cwd = event?.cwd || process.cwd();
  if (name === "read" || name === "edit" || name === "write") {
    const path = typeof args.path === "string" ? args.path : typeof args.file_path === "string" ? args.file_path : "";
    if (!path) return "";
    return ask(["hook-tool", "--plain"], { tool_name: name === "read" ? "read" : "edit", tool_input: { file_path: path }, session_id, cwd });
  }
  if (name === "apply_patch" && typeof args.input === "string") {
    return ask(["hook-tool", "--plain"], { tool_name: "apply_patch", tool_input: { command: args.input }, session_id, cwd });
  }
  if (name === "exec" || name === "bash") {
    const text = resultText(event?.result);
    if (!text.trim()) return "";
    return ask(["hook-tool-after", "--plain"], { tool_name: "bash", tool_input: { command: String(args.command || "") }, tool_response: text, session_id, cwd });
  }
  return "";
}

// The status page behind deja's Control UI tab: deja's status line, refreshed
// while the tab is open.
const STATUS_PATH = "/plugins/deja/status";
function statusPage(line) {
  const text = line.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
  return '<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="15"><title>deja</title>' +
    '<style>body{margin:16px;font:14px ui-monospace,Menlo,Consolas,monospace;color:#888}@media(prefers-color-scheme:light){body{color:#444}}</style>' +
    "<p>" + text + "</p>";
}

export default {
  id: %q,
  name: "deja recall",
  register(api) {
    // The TUI footer takes nothing from plugins. The Control UI does take a
    // sidebar tab rendering a gateway-auth route in a sandboxed frame, and
    // unlike native plugin views that needs no Labs switch, so the tab shows
    // deja's status line. An OpenClaw without these APIs goes without it.
    if (typeof api.registerHttpRoute === "function") {
      api.registerHttpRoute({
        path: STATUS_PATH,
        auth: "gateway",
        match: "exact",
        handler: async (_req, res) => {
          res.statusCode = 200;
          res.setHeader("content-type", "text/html; charset=utf-8");
          res.setHeader("cache-control", "no-store");
          res.end(statusPage(ask(["statusline"], {}) || "deja"));
          return true;
        },
      });
      const controls = api.session?.controls?.registerControlUiDescriptor ? api.session.controls : api;
      if (typeof controls.registerControlUiDescriptor === "function") {
        controls.registerControlUiDescriptor({
          id: "status",
          surface: "tab",
          label: "deja",
          description: "What deja recalled for your agents today",
          path: STATUS_PATH,
          group: "agent",
        });
      }
    }
    // A tool result middleware rewrites what the model reads back from a
    // tool, which is the one place a line can arrive beside the result it is
    // about. It needs contracts.agentToolResultMiddleware in the manifest and
    // the plugin enabled, both of which install does; an OpenClaw without the
    // API just goes without it.
    if (typeof api.registerAgentToolResultMiddleware === "function") {
      api.registerAgentToolResultMiddleware(async (event, ctx) => {
        try {
          const line = toolLine(event, ctx);
          if (!line) return;
          const content = Array.isArray(event?.result?.content) ? event.result.content : [];
          return { result: { ...event.result, content: [...content, { type: "text", text: line }] } };
        } catch {}
      }, { runtimes: ["openclaw"] });
    }
    // What this project settled, at the start of the session. The bootstrap
    // hook does this in gateway mode and does not run under the local agent,
    // where a session had no memory of the project at all until it happened to
    // ask a question the store answered.
    //
    // agent_turn_prepare is the phase hook OpenClaw asks new plugins to use —
    // before_agent_start is kept only for compatibility — and it is also where
    // queued next-turn injections are drained, so this sits in the right place
    // if that seam ever starts delivering. It fires once per agent run rather
    // than once per session, so deja_once is what keeps the digest to the first
    // of them.
    api.on(
      "agent_turn_prepare",
      async (_event, ctx) => {
        // The transcript id, not the session key: the live stamp hook-context
        // writes keeps this session out of its own MCP recall by the id the
        // index knows it by, and agent:main:main names no transcript (#4582).
        const at = where(ctx);
        const digest = ask(["hook-context", "--plain"], {
          session_id: at.id,
          cwd: at.cwd,
          source: "startup",
          deja_once: true,
        });
        if (!digest) return;
        return { prependContext: digest };
      },
      { timeoutMs: 15000 },
    );
    api.on(
      "before_prompt_build",
      async (event, ctx) => {
        const prompt = typeof event?.prompt === "string" ? event.prompt.trim() : "";
        if (!prompt) return;
        // Recall skips what it already showed this agent session, keyed by the
        // session id. A payload without one turns that off: measured on a real
        // store, half of all injections were then a word-for-word repeat. The
        // event is {prompt, messages}; the session is on ctx (#4581).
        const at = where(ctx);
        const recall = ask(["hook-prompt", "--plain"], {
          prompt,
          session_id: at.id,
          cwd: at.cwd,
        });
        // Silence is the common case — the hook speaks only when the user's
        // own history answers what they just asked.
        if (!recall) return;
        return { prependContext: recall };
      },
      { timeoutMs: 15000 },
    );
    // Compaction throws away the blocks this session was shown while the list
    // that stops them repeating outlives it, so without this the memory the
    // session just lost is the memory recall refuses to send again. The event
    // names the session file, which still holds the turns about to be
    // summarised: deja reads them from it, and the next prompt's recall
    // carries what the agent was in the middle of.
    api.on(
      "before_compaction",
      async (event, ctx) => {
        // The gateway's compaction names no file and the run's own names no
        // session id; deja finds the session by whichever it is given.
        const at = where(ctx);
        ask(["hook-precompact"], {
          session_id: at.id,
          transcript_path: event?.sessionFile || "",
          cwd: at.cwd,
          harness: "openclaw",
        });
      },
      { timeoutMs: 15000 },
    );
    // A session ends on /new and /reset, on idle expiry, when a compaction
    // rotates it to a new id, and when the gateway shuts down. Its live stamp
    // goes with it, so the next session's MCP recall can answer with it now
    // rather than twenty minutes from now (#4210). The event names the session
    // that ended, by the id the hooks above stamped it under.
    api.on(
      "session_end",
      async (event, ctx) => {
        const id = event?.sessionId || ctx?.sessionId || event?.sessionKey || ctx?.sessionKey || "";
        if (id) ask(["hook-session-end"], { session_id: id });
      },
      { timeoutMs: 5000 },
    );
  },
};
`, exe, openclawPluginID)
}
