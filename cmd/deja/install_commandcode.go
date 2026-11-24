package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Command Code keeps every user-scoped surface under `~/.commandcode`, and all
// four of them are shapes deja already writes:
//
//	mcp.json                        the server, `mcpServers`
//	settings.json                   hooks, under a `hooks` key in Claude's shape
//	skills/<name>/SKILL.md          Anthropic-style skills
//	commands/<name>.md              slash commands, name from the basename
//
// The CLI is closed, so none of this was read out of its source. Two
// independent integrations agree on it and both cite the vendor's own docs:
// rulesync's `src/constants/commandcode-paths.ts` and
// `src/types/hooks.ts` (four documented events — PreToolUse, PostToolUse,
// Stop, SessionStart — `command` hooks only, timeout in seconds), and
// akitaonrails/ai-memory's support matrix, which reports the same MCP file and
// says its SessionStart injects. Nothing here is verified on the machine deja
// was written on, and the registry entry says so (#3651).
func commandCodeConfigDir() string {
	return filepath.Join(homeDir(), ".commandcode")
}

func commandCodeMCPPath() string  { return filepath.Join(commandCodeConfigDir(), "mcp.json") }
func commandCodeSettings() string { return filepath.Join(commandCodeConfigDir(), "settings.json") }

func commandCodeSkillPath() string {
	return filepath.Join(commandCodeConfigDir(), "skills", "deja-history", "SKILL.md")
}

// commandCodeHookWiring is what deja has to say at each of the events Command
// Code documents. There is no per-prompt event — the four are PreToolUse,
// PostToolUse, Stop and SessionStart — so the digest rides SessionStart and
// the rest is the tool-time pair.
//
// The matcher is a regex over the tool's display name, and those names are its
// own: SHELL, READ, EDIT, WRITE, SEARCH, GLOB, LIST. A Claude-shaped `Bash`
// matcher never fires here, which is the trap the same adapter warns about.
var commandCodeHookWiring = []struct{ Event, Sub, Matcher string }{
	{"SessionStart", "hook-context", ""},
	{"PreToolUse", "hook-tool", "SHELL|EDIT|WRITE"},
	{"PostToolUse", "hook-tool-after", "SHELL"},
}

func installCommandCode(exe string, uninstall bool) (installResult, error) {
	server, err := installMCPJSON(commandCodeMCPPath(), exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	skill, err := installSkillFile(commandCodeSkillPath(), uninstall)
	if err != nil {
		return installResult{}, err
	}
	command, err := installCommandFile("commandcode", exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(server, skill, command), nil
}

func installCommandCodeAuto(exe string, uninstall bool) (installResult, error) {
	// The server and the rest first: `deja install --auto` installs the -auto
	// target alone, and hooks without a server would leave the tool half-wired.
	base, err := installCommandCode(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	hookExe := hookExeFor(exe, uninstall)
	results := []installResult{base}
	for _, h := range commandCodeHookWiring {
		// Seconds here, not milliseconds: Command Code's timeout unit is
		// seconds (default 30, max 600), so the numbers the other
		// settings.json targets pass would be ten minutes each.
		r, ierr := installSettingsHookCmd(commandCodeSettings(), h.Event, h.Matcher, 30,
			hookRun(hookExe, h.Sub), uninstall)
		if ierr != nil {
			return installResult{}, ierr
		}
		results = append(results, r)
	}
	mod, err := installCommandCodeMod(hookExe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(append(results, mod)...), nil
}

// commandCodeModPath is a personal mod: Command Code loads every file in
// ~/.commandcode/mods at the start of a session, untrusted or not (1.77.0,
// stand).
func commandCodeModPath() string { return filepath.Join(commandCodeConfigDir(), "mods", "deja.ts") }

// installCommandCodeMod writes the mod that carries what the four shell hooks
// cannot: the session's end (onSessionEnd) and the footer line
// (cmd.ui.setStatus, a segment under the input in the TUI).
func installCommandCodeMod(exe string, uninstall bool) (installResult, error) {
	path := commandCodeModPath()
	if uninstall {
		if _, err := os.Stat(path); err != nil {
			return installResult{Path: path, Action: "unchanged"}, nil //nolint:nilerr // nothing to remove
		}
		if err := os.Remove(path); err != nil {
			return installResult{}, err
		}
		pruneCreatedDir(filepath.Dir(path))
		return installResult{Path: path, Action: "removed"}, nil
	}
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	noteCreatedDirs(filepath.Dir(path))
	if merr := os.MkdirAll(filepath.Dir(path), 0o755); merr != nil {
		return installResult{}, merr
	}
	a, err := writeIfChanged(path, old, []byte(commandCodeModTS(exe)))
	return installResult{Path: path, Action: a}, err
}

func commandCodeModTS(exe string) string {
	return fmt.Sprintf(`// generated by deja install — safe to delete; regenerate with: deja install commandcode-auto
%simport { execFileSync } from "node:child_process"

const DEJA = %q
%s
function run(args: string[], input: string, timeout = 5000): string {
  try {
    return execFileSync(DEJA, args, {
      windowsHide: true,
      input,
      encoding: "utf8",
      timeout,
      maxBuffer: 1024 * 1024,
      stdio: ["pipe", "pipe", "ignore"],
    }).trim()
  } catch {
    // Memory is optional: a mod that throws here would only log a mod_error.
    return ""
  }
}

const RECALL_BLOCK_RE = /^\s*<deja-(?:recall|memory)\b[\s\S]*<\/deja-(?:recall|memory)>\s*$/

// cleanParts splits any trailing deja recall/known text parts off a user
// message so keys stay stable across kept-message re-runs and duplicate piles
// collapse to one block.
function cleanParts(m: any, known: Set<string>) {
  if (!m || m.role !== "user" || (m.meta && (m.meta.isMeta || m.meta.isSummary))) {
    return { isUser: false, baseParts: [] as any[], stripped: [] as string[] }
  }
  if (typeof m.content === "string") {
    return { isUser: true, baseParts: [{ type: "text", text: m.content }], stripped: [] as string[] }
  }
  if (!Array.isArray(m.content) || m.content.some((b: any) => b && b.type === "tool_result")) {
    return { isUser: false, baseParts: [] as any[], stripped: [] as string[] }
  }
  const baseParts = m.content.slice()
  const stripped: string[] = []
  while (baseParts.length > 1) {
    const tail = baseParts[baseParts.length - 1]
    if (tail && tail.type === "text" && typeof tail.text === "string" && (RECALL_BLOCK_RE.test(tail.text) || known.has(tail.text))) {
      stripped.unshift(tail.text)
      baseParts.pop()
    } else {
      break
    }
  }
  return { isUser: true, baseParts, stripped }
}

export default function (cmd: any) {
  let session = ""
  // The recall for each question, asked once and stored on disk. transformContext
  // runs on every model call of a turn and never writes back, so re-attaching
  // every historical turn's recall keeps the full prompt prefix byte-identical
  // across multi-turn conversations and restarts.
  const promptCache = createSessionStore("commandcode-cache")
  const scopeOf = () => (cmd?.cwd || "") + "\0" + (session || "default")
  const status = () => {
    try {
      const payload = session ? JSON.stringify({ session_id: session, cwd: cmd.cwd }) : ""
      cmd.ui.setStatus(run(["statusline"], payload, 2000).split(" · ").slice(0, 3).map((p, i) => i === 2 ? p.replace(/^(\d+\s+earlier)\b.*/, "$1") : p).join(" · ") || null)
    } catch {}
  }
  cmd.on("run_start", (event: any) => {
    if (event && event.sessionId) session = String(event.sessionId)
  })
  cmd.hooks({
    onSessionStart: (event: any) => {
      if (event && event.sessionId) session = String(event.sessionId)
      status()
    },
    // The four shell hooks have no end of session. Its live stamp goes here,
    // so another session's recall can answer with it now.
    onSessionEnd: () => {
      if (session) run(["hook-session-end"], JSON.stringify({ session_id: session, cwd: cmd.cwd }), 2000)
      try {
        cmd.ui.setStatus(null)
      } catch {}
    },
    onRunEnd: () => status(),
    // The shell hooks have no per-prompt event, and a PreToolUse reads a
    // transcript the turn is not yet written to. The question is here.
    transformContext: ({ messages }: any) => {
      if (!Array.isArray(messages)) return messages
      const keyOf = (m: any, idx: number, text: string) =>
        String((m.meta && m.meta.messageId) || idx) + "\n" + text
      const scope = scopeOf()
      let snap = promptCache.read(scope)
      const seen = snap.session.recalled || {}
      const known = new Set<string>(Object.values(seen).filter(Boolean) as string[])
      const cleaned = messages.map((m: any) => {
        const info = cleanParts(m, known)
        const text = info.isUser
          ? info.baseParts.filter((b: any) => b && b.type === "text").map((b: any) => b.text).join("\n")
          : ""
        return { ...info, text }
      })
      let lastAt = -1
      for (let i = cleaned.length - 1; i >= 0; i--) {
        if (cleaned[i].text) {
          lastAt = i
          break
        }
      }
      if (lastAt < 0) return messages
      const lastMsg = messages[lastAt]
      const lastInfo = cleaned[lastAt]
      const lastKey = keyOf(lastMsg, lastAt, lastInfo.text)
      if (!Object.hasOwn(seen, lastKey)) {
        const answer = lastInfo.stripped.length > 0
          ? lastInfo.stripped[0]
          : run(["hook-prompt", "--plain"], JSON.stringify({ prompt: lastInfo.text, session_id: session, cwd: cmd.cwd }), 10000)
        const res = promptCache.write(scope, snap.epoch, (s: any) => {
          rememberEntry(s.recalled, lastKey, answer)
        })
        if (res && !res.stale) snap = { epoch: res.epoch, session: res.session }
        else snap = promptCache.read(scope)
      }
      const recalls = snap.session.recalled || {}
      let out: any[] | null = null
      for (let i = 0; i < messages.length; i++) {
        const info = cleaned[i]
        if (!info.text) continue
        const m = messages[i]
        const recall = recalls[keyOf(m, i, info.text)] || info.stripped[0] || ""
        if (recall) {
          if (info.text.includes(recall) && info.stripped.length === 0) continue
          if (Array.isArray(m.content) && m.content.length === info.baseParts.length + 1 && info.stripped.length === 1 && info.stripped[0] === recall) continue
          if (!out) out = messages.slice()
          out[i] = { ...m, content: [...info.baseParts, { type: "text", text: recall }] }
        } else if (info.stripped.length > 0) {
          if (!out) out = messages.slice()
          out[i] = { ...m, content: info.baseParts }
        }
      }
      return out || messages
    },
  })
}
`, promptCacheImportsJS, exe, promptCacheStoreJS)
}

// commandCodeFirstRoot is what says Command Code is on this machine: its own
// project store, which only exists once it has run.
func commandCodeFirstRoot() string { return sources.CommandCodeRoot() }
