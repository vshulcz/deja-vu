package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// pi has no hooks; it has an extension API. Extensions in
// ~/.pi/agent/extensions/*.ts are auto-discovered, so installing is one file
// and no settings edit.
//
// before_agent_start is the injection point: it fires after the user submits a
// prompt, carries the prompt text, and can return a message that is stored in
// the session and sent to the model. That gives us both halves of what Claude
// Code splits across SessionStart and UserPromptSubmit.
//
// pi also has a real channel to the human — ctx.ui.notify — so the recall line
// and the "index still building" note go there instead of into the prompt.
func installPiExtension(exe string, uninstall bool) (installResult, error) {
	return installPiShapedExtension(sources.PiConfigDir(), exe, uninstall)
}

// installPiShapedExtension writes the same extension into a pi descendant's
// agent directory. Senpi keeps pi's loader — measured on a live install of
// @code-yeongyu/senpi: with this file in `<agent>/extensions` its start screen
// lists `deja.ts`, its `/` palette carries the extension's own `deja` command,
// and a session started with it records
// `{"type":"custom_message","customType":"deja-recall"}` holding what
// `deja hook-context` returned (#3670).
func installPiShapedExtension(agentDir, exe string, uninstall bool) (installResult, error) {
	// The launcher, not this binary: a generated plugin is as much a
	// config as a hooks.json, and one that names the build it was
	// installed from stops working the day that build moves (#3682).
	exe = hookExeFor(exe, uninstall)
	dir := filepath.Join(agentDir, "extensions")
	path := filepath.Join(dir, "deja.ts")
	if uninstall {
		if _, err := os.Stat(path); err != nil {
			return installResult{Path: path, Action: "unchanged"}, nil //nolint:nilerr // nothing to remove
		}
		if err := os.Remove(path); err != nil {
			return installResult{}, err
		}
		// And the extensions directory deja made for it (#3698).
		pruneCreatedDir(dir)
		return installResult{Path: path, Action: "removed"}, nil
	}
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	next := []byte(piExtensionTS(exe))
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

// installPiMCP writes the server into ~/.pi/agent/mcp.json. pi 0.99 reads
// that file itself (its built-in mcp extension); older pi only through the
// pi-mcp-adapter package. Install says so when nothing in the pi here reads
// it (#4583).
func installPiMCP(exe string, uninstall bool) (installResult, error) {
	r, err := installMCPJSON(filepath.Join(sources.PiConfigDir(), "mcp.json"), exe, uninstall)
	if err != nil || uninstall {
		return r, err
	}
	note := piMCPUnreadNote()
	if note == "" {
		return r, nil
	}
	if r.Note != "" {
		note = r.Note + "; " + note
	}
	r.Note = note
	return r, nil
}

const piNoAdapterNote = "pi reads this file only through the pi-mcp-adapter package — `pi install npm:pi-mcp-adapter`"

// piBuiltinMCPSince is the first pi with MCP of its own: 0.99.0 added the
// built-in mcp extension, which reads mcp.json from the agent directory.
const piBuiltinMCPSince = "0.99.0"

// piMCPUnreadNote is why nothing in pi will read mcp.json, and "" when
// something does: pi-mcp-adapter, or pi's built-in mcp extension on a pi that
// has one and has not switched it off. A pi whose version cannot be read
// counts as current, which has it.
func piMCPUnreadNote() string {
	if piMCPAdapterInstalled() {
		return ""
	}
	if v := piPackageVersion(); v != "" && !openclawVersionAtLeast(v, piBuiltinMCPSince) {
		return piNoAdapterNote
	}
	if off, at := piBuiltinMCPOff(); off {
		return "pi's built-in MCP is switched off by `" + at + "`, and pi-mcp-adapter is not installed — nothing in pi reads this file"
	}
	return ""
}

// piBuiltinMCPOff reads pi's `extensions` setting for builtin:mcp, the way
// pi's package manager does: an entry for it in a trusted project's
// .pi/settings.json wins, the last one there deciding; otherwise in the user
// settings.json `-builtin:mcp` beats `+builtin:mcp`, which beats a `!` glob.
// It returns the entry and the file that switched it off.
func piBuiltinMCPOff() (bool, string) {
	const name = "builtin:mcp"
	matches := func(e string) (mark byte, ok bool) {
		if e == "" || !strings.ContainsRune("+-!", rune(e[0])) {
			return 0, false
		}
		mark, rule := e[0], e[1:]
		if mark == '!' {
			return mark, piGlobMatch(rule, name)
		}
		return mark, strings.TrimPrefix(strings.TrimPrefix(rule, "./"), ".\\") == name
	}
	where := func(e, p string) string { return e + "` in `" + reportPath(p) }
	if cwd, err := os.Getwd(); err == nil && piProjectTrusted(cwd) {
		p := filepath.Join(cwd, ".pi", "settings.json")
		last := ""
		for _, e := range jsonStrings(jsonAt(readJSONConfig(p), "extensions")) {
			if _, ok := matches(e); ok {
				last = e
			}
		}
		if last != "" {
			return last[0] != '+', where(last, p)
		}
	}
	p := filepath.Join(sources.PiConfigDir(), "settings.json")
	var minus, plus, bang string
	for _, e := range jsonStrings(jsonAt(readJSONConfig(p), "extensions")) {
		switch mark, ok := matches(e); {
		case !ok:
		case mark == '-':
			minus = e
		case mark == '+':
			plus = e
		case bang == "":
			bang = e
		}
	}
	switch {
	case minus != "":
		return true, where(minus, p)
	case plus != "":
		return false, ""
	case bang != "":
		return true, where(bang, p)
	}
	return false, ""
}

// piProjectTrusted is pi's project trust for dir: the nearest decision for it
// or a parent in the agent directory's trust.json. pi reads a project's
// .pi/settings.json only once the project is trusted, and asks otherwise.
func piProjectTrusted(dir string) bool {
	var data map[string]any
	if b, err := readConfig(filepath.Join(sources.PiConfigDir(), "trust.json")); err == nil {
		_ = json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), &data)
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if v, ok := data[d].(bool); ok {
			return v
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	// No decision: the user settings' defaultProjectTrust, "ask" by default,
	// which a doctor with no UI answers the way pi without one does.
	return jsonAt(readJSONConfig(filepath.Join(sources.PiConfigDir(), "settings.json")), "defaultProjectTrust") == "always"
}

// piGlobMatch is the part of minimatch pi's `!` entries use on a name with no
// slash: `*`, `?`, `**`, `[...]` and `{a,b}`. A brace pair without a comma
// stays literal, as in minimatch; extglobs (`@(a)`) are not read.
func piGlobMatch(glob, name string) bool {
	// braceGroup reports whether the `{` at i opens an alternation: it closes,
	// and has a comma at its own level.
	braceGroup := func(i int) bool {
		depth := 0
		comma := false
		for j := i; j < len(glob); j++ {
			switch glob[j] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return comma
				}
			case ',':
				if depth == 1 {
					comma = true
				}
			}
		}
		return false
	}
	var re strings.Builder
	re.WriteString("^")
	var groups []bool // per open `{`: an alternation, or literal
	open := 0
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; c {
		case '*':
			switch {
			case strings.HasPrefix(glob[i:], "**/"):
				re.WriteString("(?:.*/)?")
				i += 2
			case strings.HasPrefix(glob[i:], "**"):
				re.WriteString(".*")
				i++
			default:
				re.WriteString("[^/]*")
			}
		case '?':
			re.WriteString("[^/]")
		case '[':
			j := strings.IndexByte(glob[i+1:], ']')
			if j < 0 {
				re.WriteString(`\[`)
				continue
			}
			body := glob[i+1 : i+1+j]
			re.WriteString("[")
			if strings.HasPrefix(body, "!") || strings.HasPrefix(body, "^") {
				re.WriteString("^")
				body = body[1:]
			}
			for _, r := range body {
				if r == '-' {
					re.WriteRune(r)
				} else {
					re.WriteString(regexp.QuoteMeta(string(r)))
				}
			}
			re.WriteString("]")
			i += j + 1
		case '{':
			g := braceGroup(i)
			groups = append(groups, g)
			if g {
				open++
				re.WriteString("(?:")
			} else {
				re.WriteString(`\{`)
			}
		case '}':
			if n := len(groups); n > 0 {
				g := groups[n-1]
				groups = groups[:n-1]
				if g {
					open--
					re.WriteString(")")
					continue
				}
			}
			re.WriteString(`\}`)
		case ',':
			if n := len(groups); n > 0 && groups[n-1] {
				re.WriteString("|")
			} else {
				re.WriteString(",")
			}
		default:
			re.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	re.WriteString("$")
	if open != 0 {
		return false
	}
	m, err := regexp.MatchString(re.String(), name)
	return err == nil && m
}

// piPackages are the names pi has shipped under.
var piPackages = []string{"@earendil-works/pi-coding-agent", "@mariozechner/pi-coding-agent"}

// piPackageVersion is the version of the pi on PATH, read from the package
// its command starts (npm and Homebrew link bin/pi into <package>/dist, npm
// on Windows leaves pi.cmd beside node_modules), or else the first x.y.z that
// `pi --version` prints, for a shim that hides the package (pnpm, Volta,
// Nix); "" when there is no pi or it will not say. Asked once per command.
func piPackageVersion() string {
	bin, err := exec.LookPath("pi")
	if err != nil {
		return ""
	}
	piVersionMu.Lock()
	defer piVersionMu.Unlock()
	if v, ok := piVersionCache[bin]; ok {
		return v
	}
	v := piVersionAt(bin)
	piVersionCache[bin] = v
	return v
}

var (
	piVersionMu    sync.Mutex
	piVersionCache = map[string]string{}
	piVersionToken = regexp.MustCompile(`\d+\.\d+\.\d+`)
)

func piVersionAt(bin string) string {
	var dirs []string
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		for d := filepath.Dir(resolved); len(dirs) < 4; d = filepath.Dir(d) {
			dirs = append(dirs, d)
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	for _, name := range piPackages {
		dirs = append(dirs, filepath.Join(filepath.Dir(bin), "node_modules", filepath.FromSlash(name)))
	}
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(d, "package.json"))
		if err != nil {
			continue
		}
		var pkg struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if json.Unmarshal(b, &pkg) == nil && slices.Contains(piPackages, pkg.Name) {
			return pkg.Version
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	// A wrapper that starts node without exec keeps stdout open after the
	// kill; WaitDelay stops waiting for it.
	cmd.WaitDelay = 200 * time.Millisecond
	out, _ := cmd.Output()
	return piVersionToken.FindString(string(out))
}

// piMCPAdapterInstalled reports whether pi loads pi-mcp-adapter: a package
// source naming it in the user's settings.json or the project's. pi takes a
// source as a string or as {source: …}, from npm, git or a local path. It
// also loads an extension listed under "extensions" or sitting in an
// extensions/ directory beside settings.json.
func piMCPAdapterInstalled() bool {
	dirs := []string{sources.PiConfigDir()}
	if cwd, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Join(cwd, ".pi"))
	}
	for _, dir := range dirs {
		cfg := readJSONConfig(filepath.Join(dir, "settings.json"))
		pkgs, _ := jsonAt(cfg, "packages").([]any)
		exts, _ := jsonAt(cfg, "extensions").([]any)
		for _, pkg := range append(pkgs, exts...) {
			src, _ := pkg.(string)
			if m, ok := pkg.(map[string]any); ok {
				src, _ = m["source"].(string)
			}
			if strings.Contains(src, "pi-mcp-adapter") {
				return true
			}
		}
		entries, _ := os.ReadDir(filepath.Join(dir, "extensions"))
		for _, e := range entries {
			if strings.Contains(e.Name(), "pi-mcp-adapter") {
				return true
			}
		}
	}
	return false
}

func piExtensionTS(exe string) string { //nolint:funlen // the body is the plugin's source text
	return fmt.Sprintf(`// generated by deja install — safe to delete; regenerate with: deja install pi-auto
import { execFileSync } from "node:child_process";
import { closeSync, openSync, readSync } from "node:fs";
import { basename } from "node:path";

const DEJA = %q;

function run(args: string[], input: string, timeout = 10000): string {
  try {
    return execFileSync(DEJA, args, {
      windowsHide: true,
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
  // The tool and compaction events carry no session id, and recall dedupes per
  // session: without one it repeats itself and forgets nothing. pi keeps the id
  // on the session manager, which every handler gets through ctx.
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

  // A resumed session holds the digest it opened with, and pi sends it to the
  // model again with the rest of the session; a fork holds a copy of its
  // source's. Asking for another put a second copy in front of the model, and
  // in a fork one that led with the source itself.
  // What counts is the digest, not any recall: a prompt's recall goes under
  // the same customType, and a compaction that kept one but not the digest
  // left a resumed session with none (#5314). The digest carries
  // details.digest; a branch with no marked digest was written before the
  // mark, and any block of ours still counts there.
  const isDigest = (e: any) =>
    !!e && e.type === "custom_message" && e.customType === "deja-recall" && !!(e.details && e.details.digest);
  const holdsRecall = (ctx: any) => {
    try {
      const branch = ctx.sessionManager.getBranch() || [];
      const marked = branch.some(isDigest);
      // pi sends what follows a compaction and the entries it kept from
      // before it, firstKeptEntryId on.
      let keep = "";
      for (let i = branch.length - 1; i >= 0; i--) {
        const e = branch[i] || {};
        if (keep === "" && e.type === "compaction") {
          keep = String(e.firstKeptEntryId || "");
          if (!keep) return false;
          continue;
        }
        if (marked ? isDigest(e) : e.type === "custom_message" && e.customType === "deja-recall") return true;
        if (keep && e.id === keep) return false;
      }
    } catch {}
    return false;
  };
  // The session a fork was made from, named on its header by path, when the
  // fork holds a copy of its turns: a fork at the first message copies
  // nothing, and its source is no part of it. The id is on that file's own
  // header; pi names the file <time>_<id>.jsonl.
  const parents: Record<string, string> = {};
  const parentID = (ctx: any) => {
    const key = sessionID();
    if (key in parents) return parents[key];
    let id = "";
    try {
      const m = ctx.sessionManager;
      const path = String((m.getHeader() || {}).parentSession || "");
      const copied = (m.getBranch() || []).some((e: any) => e && e.type === "message");
      if (path && copied) {
        try {
          const fd = openSync(path, "r");
          const buf = Buffer.alloc(64 * 1024);
          const n = readSync(fd, buf, 0, buf.length, 0);
          closeSync(fd);
          id = String(JSON.parse(buf.subarray(0, n).toString("utf8").split("\n", 1)[0]).id || "");
        } catch {}
        if (!id) {
          const name = basename(path).replace(/\.jsonl$/, "");
          id = name.slice(name.lastIndexOf("_") + 1);
        }
      }
    } catch {}
    if (key) parents[key] = id;
    return id;
  };

  // pi keeps a footer status line: while the first index builds, that is
  // where the user can see it happening instead of wondering why recall is
  // quiet. Cleared as soon as the build finishes.
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

  // pi surfaces registered commands in the prompt box, which is how someone
  // who never read the docs finds this at all.
  pi.registerCommand("deja", {
    description: "Search your own past coding sessions",
    handler: async (args: string, ctx: any) => {
      const query = (args || "").trim();
      if (!query) {
        ctx.ui.notify("Usage: /deja <what you are looking for>", "info");
        return;
      }
      // The user is waiting on this one, so it may outlive the hook budget:
      // a first search can rebuild the index.
      //
      // "--" when the query names one of deja's own flags: searching for
      // "--json" or "--all-matches" otherwise dies in flag parsing and comes
      // back as an empty history.
      const commandArgs = query.startsWith("-") ? ["search", "--", query] : ["search", query];
      const found = run(commandArgs, "", 120000);
      ctx.ui.notify(found || "Nothing in your history matches " + query, "info");
    },
  });

  pi.on("before_agent_start", async (event: any, ctx: any) => {
    try {
      remember(ctx);
      const parent = parentID(ctx);
      if (!injected.has(sessionID()) && holdsRecall(ctx)) {
        injected.add(sessionID());
        const stats = run(["statusline"], "");
        if (stats) ctx.ui.setStatus("deja", stats);
      }
      if (!injected.has(sessionID())) {
        // The session goes with it: hook-context marks the one starting as
        // live, which keeps it out of its own MCP recall on this first turn
        // (#4394, as #4246 and #4273 did for Hermes and opencode). pi has no
        // MCP of its own, so the lead names the shell command, not the tool
        // (#4584).
        const raw = run(["hook-context"], JSON.stringify({ session_id: sessionID(), parent_session_id: parent, cwd: process.cwd(), deja_shell: true }));
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
          // pi keeps the footer until it is cleared, so the session carries a
          // quiet reminder that memory is on rather than a one-off toast.
          const stats = run(["statusline"], "");
          if (stats) ctx.ui.setStatus("deja", stats);
          // Marked, so a resume can tell the digest from a prompt's recall
          // (#5314).
          return { message: { customType: "deja-recall", content: digest, display: false, details: { digest: true } } };
        }
        // Nothing to recall: either there is no history yet, or the first
        // index is still building. Only the second is worth saying, once.
        if (!toldBuilding && showBuild(ctx)) {
          toldBuilding = true;
          ctx.ui.notify(run(["warmup-status"], ""), "info");
        }
        return;
      }
      const raw = run(["hook-prompt"], JSON.stringify({ prompt: event.prompt || "", session_id: sessionID(), parent_session_id: parent }));
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

  // The other half of the point of action: a command has just failed, and this
  // machine has fixed that same error before. pi hands tool_result the content
  // the model is about to read and uses what the handler returns, so the repair
  // goes in beside the error in the same turn. tool_execution_start and
  // tool_execution_end are observe-only — their return reaches nothing — which
  // is why this is the one that carries it.
  const repaired: Record<string, string> = {};
  pi.on("tool_result", async (event: any) => {
    try {
      if (!event) return;
      // A file the agent just opened is the step before it changes that file,
      // and pi has no handler that runs earlier whose return the model reads.
      // So the file's own history goes out here: what was decided about it,
      // from the sessions that decided it. deja answers once per session per
      // fact, so re-reading the same file stays quiet. An edit or a write
      // without a read before it gets the same line, for the edits after it.
      if (event.toolName === "read" || event.toolName === "edit" || event.toolName === "write") {
        const path = String((event.input && (event.input.path || event.input.file_path)) || "");
        if (!path) return;
        const parts = Array.isArray(event.content) ? event.content : [];
        const id = "file:" + String(event.toolCallId || "");
        if (!(id in repaired)) {
          repaired[id] = run(["hook-tool", "--plain"], JSON.stringify({
            tool_name: event.toolName,
            tool_input: { file_path: path },
            session_id: sessionID(),
            cwd: process.cwd(),
          }));
        }
        const note = repaired[id];
        if (!note) return;
        return { content: parts.concat([{ type: "text", text: note }]) };
      }
      if (!event.isError) return;
      // Senpi and Kimchi's pi have a powershell tool beside bash (#4523).
      if (event.toolName !== "bash" && event.toolName !== "powershell") return;
      const parts = Array.isArray(event.content) ? event.content : [];
      const output = parts
        .filter((p: any) => p && p.type === "text" && typeof p.text === "string")
        .map((p: any) => p.text)
        .join("\n");
      if (!output.trim()) return;
      const id = String(event.toolCallId || "");
      if (!(id in repaired)) {
        repaired[id] = run(["hook-tool-after", "--plain"], JSON.stringify({
          tool_name: event.toolName,
          tool_response: output,
          session_id: sessionID(),
          cwd: process.cwd(),
        }));
      }
      const line = repaired[id];
      if (!line) return;
      return { content: parts.concat([{ type: "text", text: line }]) };
    } catch {
      // never turn a failed command into a failed session
    }
  });

  // Compaction throws away the blocks this session was shown, and the list that
  // stops them repeating outlives them. session_compact fires once the summary
  // has replaced the history, which is exactly when the session should be
  // allowed to see those blocks again. The session file keeps the turns the
  // summary replaced, so deja reads them from it and the next prompt carries
  // what the agent was in the middle of.
  pi.on("session_compact", async (_event: any, ctx: any) => {
    try {
      remember(ctx);
      run(["hook-precompact"], JSON.stringify({ session_id: sessionID(), transcript_path: sessionFile, cwd: process.cwd() }));
    } catch {}
  });

  // The session is over: quit, /new, /resume, /fork or a reload. Its live
  // stamp goes with it, so the next session's MCP recall can answer with it
  // now rather than twenty minutes from now (#4210). The id is the one this
  // extension saw last; on a switch ctx may already name the next session.
  pi.on("session_shutdown", async (_event: any) => {
    try {
      const id = sessionID();
      if (id) run(["hook-session-end"], JSON.stringify({ session_id: id, cwd: process.cwd() }), 2000);
    } catch {}
  });
}
`, exe)
}
