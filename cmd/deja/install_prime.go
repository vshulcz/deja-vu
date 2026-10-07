package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// prime-agent is pi's lineage — same JSONL envelope, same extension API — so
// the wiring is pi's with the parts prime-agent does not deliver left out.
//
// Measured on prime-agent 0.9.1 with a probe extension and a recording
// endpoint:
//   - before_agent_start fires and the message it returns arrives in the
//     provider request, which is the channel the digest and per-prompt recall
//     ride.
//   - session_start fires, so the footer can say the first index is building.
//   - tool_result fires, in --print too (0.9.8). An earlier probe called a
//     bash tool the model does not have: prime hands it one tool, ipython,
//     and shell commands and edits run inside its cell.
//   - session_before_compact and session_compact fire, in --print too (0.9.8
//     stand). session_compact comes after the compaction entry is written and
//     the session file still holds the turns before it, so the capture reads
//     that file.
//   - MCP servers are read from ~/.prime/agent/settings.json, and the shared
//     ~/.agents/skills directory is loaded — `deja install` writes the skill
//     there already.
func installPrimeMCP(exe string, uninstall bool) (installResult, error) {
	return installPrimeMCPAt(primeSettingsPath(), exe, uninstall)
}

func primeSettingsPath() string {
	return filepath.Join(sources.PrimeConfigDir(), "settings.json")
}

func installPrimeMCPAt(path, exe string, uninstall bool) (installResult, error) {
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	// A JSONC file goes through the text writer, so a comment the reader
	// kept stays; re-marshalling the object dropped it for good (#3243).
	if len(bytes.TrimSpace(old)) > 0 && configIsJSONC(old) {
		command, args := mcpCommandArgs(exe)
		return writeJSONCEntry(path, old, "mcpServers",
			map[string]any{"type": "stdio", "command": command, "args": args}, uninstall)
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		root = map[string]any{}
	} else if err := json.Unmarshal([]byte(jsoncToJSON(string(old))), &root); err != nil {
		return installResult{}, configParseError(path, err)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil {
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		servers = map[string]any{}
		root["mcpServers"] = servers
	}
	var note string
	if uninstall {
		delete(servers, "deja")
		removeAdoptedDejaEntries(path, "mcpServers", servers)
		note = leftDejaEntriesNote(servers)
		if len(servers) == 0 {
			delete(root, "mcpServers")
		}
	} else {
		// deja under another name is adopted, not doubled (#4556).
		key := dejaEntryKey(servers)
		if key != "deja" {
			noteBlockAdded(path, "mcpServers."+key)
		}
		command, args := mcpCommandArgs(exe)
		// type: "stdio" explicitly: prime-agent's settings carry both stdio and
		// http servers under the same key, and its docs write the type out.
		entry := map[string]any{"type": "stdio", "command": command, "args": args}
		note = keepSwitch(servers[key], entry)
		servers[key] = entry
		note = withOtherDejaEntries(note, servers, key)
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	noteCreatedDirs(filepath.Dir(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a, Note: note}, err
}

// installPrimeAuto writes the extension and the MCP server both, for the same
// reason pi does: a digest with no tool to follow it up with is half an install.
func installPrimeAuto(exe string, uninstall bool) (installResult, error) {
	mcp, err := installPrimeMCP(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	ext, err := installPrimeExtension(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(mcp, ext), nil
}

func primeExtensionPath() string {
	return filepath.Join(sources.PrimeConfigDir(), "extensions", "deja.ts")
}

func installPrimeExtension(exe string, uninstall bool) (installResult, error) {
	// The launcher, not this binary: a generated plugin is as much a
	// config as a hooks.json, and one that names the build it was
	// installed from stops working the day that build moves (#3682).
	exe = hookExeFor(exe, uninstall)
	path := primeExtensionPath()
	if uninstall {
		if _, err := os.Stat(path); err != nil {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		if err := os.Remove(path); err != nil {
			return installResult{}, err
		}
		// And the directory deja made for it (#3698).
		pruneCreatedDir(filepath.Dir(path))
		return installResult{Path: path, Action: "removed"}, nil
	}
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	noteCreatedDirs(filepath.Dir(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	a, err := writeIfChanged(path, old, []byte(primeExtensionTS(exe)))
	return installResult{Path: path, Action: a}, err
}

func primeExtensionTS(exe string) string {
	return fmt.Sprintf(`// generated by deja install — safe to delete; regenerate with: deja install prime-auto
import { execFileSync } from "node:child_process";

const DEJA = %q;

function run(args: string[], input: string, timeout = 10000): string {
  try {
    return execFileSync(DEJA, args, {
      input,
      encoding: "utf8",
      timeout,
      maxBuffer: 4 * 1024 * 1024,
      stdio: ["pipe", "pipe", "ignore"],
    }).trim();
  } catch {
    return "";
  }
}

export default function (pi: any) {
  // Once per session, not once per process: /new, a resume and a fork keep the
  // process and switch the session, and a flag set on the first one left every
  // later session with no digest.
  const injected = new Set<string>();
  let toldBuilding = false;
  // No event carries a session id; the session manager does, and every handler
  // reaches it through ctx. Recall dedupes per session, so without one the same
  // block goes out on every message.
  let session = "";
  let sessionFile = "";
  const remember = (ctx: any) => {
    try {
      const m = ctx && ctx.sessionManager;
      const id = m && (m.getSessionId ? m.getSessionId() : m.sessionId);
      if (id) session = String(id);
      const file = m && (m.getSessionFile ? m.getSessionFile() : m.sessionFile);
      if (file) sessionFile = String(file);
    } catch {}
  };
  const sessionID = () => session;

  // prime-agent keeps a footer status line: while the first index builds, that
  // is where the user can see it happening instead of wondering why recall is
  // quiet.
  const showBuild = (ctx: any) => {
    const status = run(["warmup-status"], "");
    if (status) {
      ctx.ui.setStatus("deja", status);
      return true;
    }
    ctx.ui.setStatus("deja", "");
    return false;
  };

  pi.on("session_start", async (_event: any, ctx: any) => {
    try {
      remember(ctx);
      showBuild(ctx);
    } catch {
      // memory is optional: never break the session over it
    }
  });

  // Registered commands show up in the prompt box, which is how someone who
  // never read the docs finds this at all.
  pi.registerCommand("deja", {
    description: "Search your own past coding sessions",
    handler: async (args: string, ctx: any) => {
      const query = (args || "").trim();
      if (!query) {
        ctx.ui.notify("Usage: /deja <what you are looking for>", "info");
        return;
      }
      // The user is waiting on this one, so it gets a longer budget than a
      // hook: a first search can rebuild the index. "--" keeps a query that
      // starts with a dash out of deja's own flag parsing.
      const commandArgs = query.startsWith("-") ? ["search", "--", query] : ["search", query];
      const found = run(commandArgs, "", 120000);
      ctx.ui.notify(found || "Nothing in your history matches " + query, "info");
    },
  });

  // The one channel that reaches the model here: what it returns is stored in
  // the session and sent with the turn. The first run carries the project
  // digest, every one after it the recall for the question just asked.
  pi.on("before_agent_start", async (event: any, ctx: any) => {
    try {
      remember(ctx);
      if (!injected.has(sessionID())) {
        // The session goes with it: hook-context marks the one starting as
        // live, which keeps it out of its own MCP recall on this first turn
        // (#4394, as #4246 and #4273 did for Hermes and opencode).
        const raw = run(["hook-context"], JSON.stringify({ session_id: sessionID(), cwd: process.cwd() }));
        let digest = "";
        let receipt = "";
        try {
          const parsed = JSON.parse(raw);
          digest = parsed?.hookSpecificOutput?.additionalContext || "";
          receipt = parsed?.systemMessage || "";
        } catch {
          digest = raw;
        }
        if (digest) {
          injected.add(sessionID());
          ctx.ui.setStatus("deja", "");
          // The receipt is what tells the user memory arrived; without it the
          // recall is invisible and reads as the model guessing.
          if (receipt) ctx.ui.notify(receipt, "info");
          const stats = run(["statusline"], "");
          if (stats) ctx.ui.setStatus("deja", stats);
          return { message: { customType: "deja-recall", content: digest, display: false } };
        }
        // Nothing to recall: either there is no history yet, or the first
        // index is still building. Only the second is worth saying, once.
        if (!toldBuilding && showBuild(ctx)) {
          toldBuilding = true;
          ctx.ui.notify(run(["warmup-status"], ""), "info");
        }
        return;
      }
      const raw = run(["hook-prompt"], JSON.stringify({ prompt: event.prompt || "", session_id: sessionID() }));
      if (!raw) return;
      const resp = JSON.parse(raw);
      if (resp && resp.systemMessage) ctx.ui.notify(resp.systemMessage, "info");
      const extra = resp && resp.hookSpecificOutput && resp.hookSpecificOutput.additionalContext;
      if (!extra) return;
      return { message: { customType: "deja-recall", content: extra, display: false } };
    } catch {
      // memory is optional: never break the session over it
    }
  });

  // Compaction throws away the blocks this session was shown, and the list that
  // stops them repeating outlives them. The session file still holds the turns
  // the summary replaced: deja reads them from it, and the next prompt's recall
  // carries what the agent was in the middle of.
  pi.on("session_compact", async (_event: any, ctx: any) => {
    try {
      remember(ctx);
      run(["hook-precompact"], JSON.stringify({ session_id: sessionID(), transcript_path: sessionFile, cwd: process.cwd(), harness: "prime" }));
    } catch {}
  });

  // The model has one tool, ipython, and reads, edits and shell commands all
  // run inside its cell: edit(path=...) is the pre-imported editor and
  // bash('...') the shell. So the file line is asked for each file the cell
  // names, and the repair when its output carries a failed bash. tool_result's
  // content is what the model reads, and the handler's return replaces it.
  const answered: Record<string, string> = {};
  const fileRe = /\b(?:edit|open|Path)\s*\(\s*(?:path\s*=\s*)?[rbuf]?(["'])([^"'\n]+)\1/g;
  pi.on("tool_result", async (event: any) => {
    try {
      if (!event || event.toolName !== "ipython") return;
      const code = String((event.input && event.input.code) || "");
      const parts = Array.isArray(event.content) ? event.content : [];
      const id = String(event.toolCallId || "");
      if (!(id in answered)) {
        let line = "";
        const output = parts
          .filter((p: any) => p && p.type === "text" && typeof p.text === "string")
          .map((p: any) => p.text)
          .join("\n");
        // bash() inside a cell does not fail the cell: the exit code is only
        // in the text, as BashResult(exit_code=N, ...).
        // The output is the repr of a Python string there, so its escapes
        // are undone before the error in it is looked up.
        const exit = /exit_code=(\d+)/.exec(output);
        if (event.isError || (exit && exit[1] !== "0")) {
          const repr = /output=(["'])((?:\\.|(?!\1)[^\\])*)\1/.exec(output);
          const text = repr
            ? repr[2].replace(/\\n/g, "\n").replace(/\\t/g, "\t").replace(/\\(["'\\])/g, "$1") + "\nExit Code: " + (exit ? exit[1] : "1")
            : output;
          line = run(["hook-tool-after", "--plain"], JSON.stringify({
            tool_name: "bash",
            tool_response: text,
            session_id: sessionID(),
            cwd: process.cwd(),
          }), 5000);
        }
        for (const m of code.matchAll(fileRe)) {
          if (line) break;
          line = run(["hook-tool", "--plain"], JSON.stringify({
            tool_name: "edit",
            tool_input: { file_path: m[2] },
            session_id: sessionID(),
            cwd: process.cwd(),
          }), 5000);
        }
        answered[id] = line;
      }
      const note = answered[id];
      if (!note) return;
      return { content: parts.concat([{ type: "text", text: note }]) };
    } catch {}
  });

  // The session is over, so its live stamp goes: the next session's MCP
  // recall can answer with it now rather than twenty minutes from now (#4210).
  pi.on("session_shutdown", async (_event: any) => {
    try {
      const id = sessionID();
      if (id) run(["hook-session-end"], JSON.stringify({ session_id: id }), 2000);
    } catch {}
  });
}
`, exe)
}
