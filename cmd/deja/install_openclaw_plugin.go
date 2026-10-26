package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The hook pack's digest runs only in gateway mode. OpenClaw's plugin runtime
// has the rest: an agent:bootstrap hook of its own that also runs under
// `openclaw agent --local`, and before_prompt_build, which is handed the prompt
// the user just typed and returns context that goes in front of the model.
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
	dir := openclawPluginDir()
	if uninstall {
		// Same as the hook pack beside it: say what happened, and take the
		// directories deja made above this one (#3698).
		had := isRealDir(dir)
		if err := os.RemoveAll(dir); err != nil {
			return installResult{}, err
		}
		if err := removeOpenClawPluginEntry(); err != nil {
			return installResult{}, err
		}
		if err := setOpenClawPluginLoadPath(dir, false); err != nil {
			return installResult{}, err
		}
		if !had {
			return installResult{Path: dir, Action: "unchanged"}, nil
		}
		pruneCreatedDir(filepath.Dir(dir))
		return installResult{Path: dir, Action: "removed"}, nil
	}
	entry, a, err := writeOpenClawPluginFiles(exe)
	if err != nil {
		return installResult{}, err
	}
	cfg, err := setOpenClawPluginEntry()
	if err != nil {
		return installResult{}, err
	}
	if err := setOpenClawPluginLoadPath(dir, true); err != nil {
		return installResult{}, err
	}
	// The manifest and package.json beside it are deja's own and went unnamed
	// on the screen whose job is saying what was touched, so the directory
	// rides along (#3254). The entry too: an upgrade that only added the grant
	// said "unchanged".
	return wroteAll(installResult{Path: entry, Action: a},
		installResult{Path: dir, Action: a}, cfg), nil
}

// writeOpenClawPluginFiles writes the plugin's directory, which is deja's
// own, and nothing in openclaw.json: the entry point and what was done to it.
func writeOpenClawPluginFiles(exe string) (string, string, error) {
	dir := openclawPluginDir()
	noteCreatedDirs(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	for _, f := range []struct{ name, body string }{
		{"package.json", openclawPluginPackage()},
		{"openclaw.plugin.json", openclawPluginManifest()},
	} {
		path := filepath.Join(dir, f.name)
		old, err := readConfig(path)
		if err != nil {
			return "", "", err
		}
		if _, err := writeIfChanged(path, old, []byte(f.body)); err != nil {
			return "", "", err
		}
	}
	entry := filepath.Join(dir, "index.mjs")
	old, err := readConfig(entry)
	if err != nil {
		return "", "", err
	}
	a, err := writeIfChanged(entry, old, []byte(openclawPluginJS(exe)))
	return entry, a, err
}

// setOpenClawPluginEntry writes our entry under plugins.entries, leaving
// every other plugin — and the user's allow list — untouched.
func setOpenClawPluginEntry() (installResult, error) {
	path := openclawConfigPath()
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) > 0 {
		if uerr := json.Unmarshal([]byte(jsoncToJSON(string(old))), &root); uerr != nil {
			return installResult{}, openclawParseError(path, old)
		}
	}
	if root == nil {
		root = map[string]any{}
	}
	plugins, _ := root["plugins"].(map[string]any)
	entries, _ := mapAt(plugins, "entries")
	have, _ := mapAt(entries, openclawPluginID)
	entry, note := openclawPluginEntry(have, openclawTakesConversationAccess())
	if configIsJSONC(old) {
		// The same file the hook and the MCP entry are written into, and the
		// same reason not to refuse it over a comment (#2811).
		a, serr := setOpenClawEntryJSONC(path, old, "plugins.entries", openclawPluginID, "", true, entry)
		return installResult{Path: path, Action: a, Note: note}, serr
	}
	if plugins == nil {
		plugins = map[string]any{}
		root["plugins"] = plugins
	}
	if entries == nil {
		entries = map[string]any{}
		plugins["entries"] = entries
	}
	entries[openclawPluginID] = entry
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	a, err := writeIfChanged(path, old, append(next, '\n'))
	return installResult{Path: path, Action: a, Note: note}, err
}

// removeOpenClawPluginEntry takes our entry out of plugins.entries, and the
// blocks it leaves empty.
func removeOpenClawPluginEntry() error {
	path := openclawConfigPath()
	old, err := readConfig(path)
	if err != nil || len(bytes.TrimSpace(old)) == 0 {
		return err
	}
	if configIsJSONC(old) {
		_, serr := setOpenClawEntryJSONC(path, old, "plugins.entries", openclawPluginID, "", false, nil)
		return serr
	}
	var root map[string]any
	if json.Unmarshal(old, &root) != nil {
		return openclawParseError(path, old)
	}
	plugins, _ := root["plugins"].(map[string]any)
	entries, _ := mapAt(plugins, "entries")
	if entries == nil || entries[openclawPluginID] == nil {
		return nil
	}
	delete(entries, openclawPluginID)
	if len(entries) == 0 {
		delete(plugins, "entries")
	}
	if len(plugins) == 0 {
		delete(root, "plugins")
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return err
	}
	_, err = writeIfChanged(path, old, append(next, '\n'))
	return err
}

// setOpenClawPluginLoadPath adds the plugin's directory to plugins.load.paths,
// or takes it out, beside whatever paths the user lists there (#4579).
func setOpenClawPluginLoadPath(dir string, on bool) error {
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	old, err := readConfig(path)
	if err != nil {
		return err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		if !on {
			return nil
		}
		root = map[string]any{}
	} else if uerr := json.Unmarshal([]byte(jsoncToJSON(string(old))), &root); uerr != nil {
		return configParseError(path, uerr)
	}
	plugins, _ := root["plugins"].(map[string]any)
	load, _ := mapAt(plugins, "load")
	// Something other than an object or a list there is a config deja does
	// not understand. The plugin loads from extensions/ without it, so the
	// cost of leaving it is the warning, not the recall.
	if _, ok := plugins["load"]; ok && load == nil {
		return nil
	}
	paths, isList := load["paths"].([]any)
	if _, ok := load["paths"]; ok && !isList {
		return nil
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
		return nil
	}
	if on {
		kept = append(kept, dir)
	}
	if configIsJSONC(old) {
		text, oerr := openclawLoadPathsJSONC(string(old), dir, on, len(kept), len(load), len(plugins), paths != nil)
		if oerr != nil {
			return configParseError(path, oerr)
		}
		_, err = writeIfChanged(path, old, []byte(text))
		return err
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
		return err
	}
	_, err = writeIfChanged(path, old, append(next, '\n'))
	return err
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
		return "", errors.New("does not look like a settings object")
	}
	block, have := walkJSONCKeys(text, open, []string{"plugins", "load"})
	if block == nil || have < 2 {
		return "", errors.New("plugins.load is not where it parsed")
	}
	at := jsoncListValue(text, block, "paths")
	if at == nil {
		return "", errors.New("plugins.load.paths is not where it parsed")
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
	next, err := jsoncCutListStrings(text, *at, func(s string) bool { return openclawSamePath(s, dir) })
	if err != nil {
		return "", fmt.Errorf("plugins.load.paths %w", err)
	}
	return next, nil
}

// jsoncCutListStrings takes the strings match picks out of the list spanned by at
// (from its '[' to one past its ']') as text: the other elements keep their
// comments and commas. Its error names no list: the caller says which.
// A list left empty stays as `[]`; dropping the key is
// the caller's call.
func jsoncCutListStrings(text string, at [2]int, match func(string) bool) (string, error) {
	return jsoncCutListElements(text, at, func(elem string) bool {
		var s string
		return json.Unmarshal([]byte(elem), &s) == nil && match(s)
	})
}

// jsoncCutListElements is jsoncCutListStrings for the elements, strings and
// objects, that match picks out by their text as the list has it: comments
// inside an object are still there, and match reads them out. Elements are
// offered in order.
func jsoncCutListElements(text string, at [2]int, match func(elem string) bool) (string, error) {
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
	// The elements at the list's own level that match, last first so the
	// offsets of the ones before stay good.
	var hits [][2]int
	depth := 0
	for i := at[0] + 1; i < at[1]-1; i++ {
		switch blank[i] {
		case '{':
			if depth == 0 {
				end := zedObjectEnd(text, i)
				if end < 0 || end > at[1] {
					return "", errors.New("has an open object")
				}
				if match(text[i:end]) {
					hits = append(hits, [2]int{i, end})
				}
				i = end - 1
				continue
			}
			depth++
		case '[':
			depth++
		case ']', '}':
			depth--
		case '"':
			end := zedStringEnd(text, i)
			if end < 0 {
				return "", errors.New("has an open string")
			}
			if depth == 0 && match(text[i:end]) {
				hits = append(hits, [2]int{i, end})
			}
			i = end - 1
		}
	}
	space := func(c byte) bool { return c == ' ' || c == '\t' }
	for _, hit := range slices.Backward(hits) {
		from, to := hit[0], hit[1]
		comma := -1
		j := to
		for j < at[1]-1 && (space(blank[j]) || blank[j] == '\n' || blank[j] == '\r') {
			j++
		}
		if blank[j] == ',' {
			// The comma after it, and the space up to the next element.
			to = j + 1
			for to < at[1]-1 && space(text[to]) {
				to++
			}
		} else if p := prevSig(from); blank[p] == ',' {
			// The last element: the comma before it goes, and only the
			// comma. A comment the reader put between it and deja's entry
			// is theirs and stays where they wrote it.
			comma = p
		}
		// The entry's line goes whole when nothing else is left on it.
		s, e := from, to
		for s > 0 && space(text[s-1]) {
			s--
		}
		for e < len(text) && space(text[e]) {
			e++
		}
		if s > 0 && text[s-1] == '\n' && e < len(text) && (text[e] == '\n' || text[e] == '\r') {
			if text[e] == '\r' && e+1 < len(text) && text[e+1] == '\n' {
				e++
			}
			from, to = s, e+1
		} else if comma >= 0 {
			from = s
		}
		cut := to - from
		text = text[:from] + text[to:]
		if comma >= 0 {
			text = text[:comma] + text[comma+1:]
			cut++
		}
		blank = stripJSONComments(text)
		at[1] -= cut
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
    "additionalProperties": false,
    "properties": {
      "receipt": {
        "type": "boolean",
        "description": "In a 1:1 chat with the owner on a channel, add deja's one-line receipt under a reply answered from history. Default true."
      }
    }
  }
}
`
}

func openclawPluginJS(exe string) string { //nolint:funlen // the body is the plugin's source text
	return fmt.Sprintf(`// generated by deja install — safe to delete; regenerate with: deja install openclaw-auto
import { execFileSync } from "node:child_process";

const DEJA = %q;

// Memory is optional: a hook that throws must never take the turn down with it.
function ask(args, payload) {
  try {
    return execFileSync(DEJA, args, {
      windowsHide: true,
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

// deja's per-prompt receipt rides under the final reply in a 1:1
// chat with the owner on a channel (Telegram, Slack, Discord ...), never in a
// group, the TUI or the Control UI, and the session-start notes never go to a
// channel at all. The rules below are extensions/openclaw/lib.mjs's, copied as
// they are; TestOpenClawPluginSharesTheReceiptRules keeps the two equal.
function splitHookReply(stdout) {
  const text = String(stdout || "").trim()
  if (!text) return { context: "", notes: "" }
  const lines = text.split("\n")
  for (let i = lines.length - 1; i >= 0; i--) {
    const line = lines[i].trim()
    if (!line.startsWith("{")) continue
    let reply
    try {
      reply = JSON.parse(line)
    } catch {
      continue
    }
    if (!reply || typeof reply !== "object" || Array.isArray(reply)) continue
    if (!("hookSpecificOutput" in reply) && !("systemMessage" in reply)) continue
    const out = reply.hookSpecificOutput
    const context = out && typeof out.additionalContext === "string" ? out.additionalContext.trim() : ""
    const before = lines.slice(0, i).join("\n").trim()
    const after = lines.slice(i + 1).join("\n").trim()
    const notes = typeof reply.systemMessage === "string" ? reply.systemMessage.trim() : ""
    return { context: [before, context, after].filter(Boolean).join("\n"), notes }
  }
  return { context: text, notes: "" }
}
function deliversNotes(channel) {
  const name = String(channel || "").trim().toLowerCase()
  return name !== "" && name !== "webchat"
}
function ownerSender(config, channel, senderId) {
  const ch = String(channel || "").trim().toLowerCase()
  const id = String(senderId ?? "").trim()
  if (!ch || !id) return false
  const owners = config && config.commands && config.commands.ownerAllowFrom
  if (Array.isArray(owners) && owners.length > 0) {
    return owners.some((e) => String(e).trim().toLowerCase() === ch + ":" + id.toLowerCase())
  }
  const allow = config && config.channels && config.channels[ch] && config.channels[ch].allowFrom
  if (!Array.isArray(allow)) return false
  const entries = allow.map((e) => String(e).trim()).filter(Boolean)
  if (entries.length !== 1 || entries[0] === "*") return false
  return entries[0] === id || entries[0].toLowerCase() === ch + ":" + id.toLowerCase()
}
function directSessionKey(key) {
  const parts = String(key || "").trim().toLowerCase().split(":")
  if (!parts[0]) return false
  return !parts.some((p) => p === "group" || p === "channel" || p === "thread" || p === "topic")
}
function conversationOf(id, channel) {
  let s = String(id ?? "").trim().toLowerCase()
  const own = String(channel || "").trim().toLowerCase() + ":"
  if (s.startsWith(own)) s = s.slice(own.length)
  for (const prefix of ["channel:", "chat:", "user:"]) {
    if (s.startsWith(prefix)) return s.slice(prefix.length)
  }
  return s
}
function directTurn(event, ctx) {
  const e = event || {}
  const c = ctx || {}
  const key = e.sessionKey || c.sessionKey || ""
  const channel = String(e.channel || c.channelId || "").trim().toLowerCase()
  const chatType = e.chatType ?? c.chatType
  const conversation = conversationOf(c.conversationId ?? e.conversationId, channel)
  const direct =
    e.isGroup === false &&
    directSessionKey(key) &&
    (chatType === undefined || chatType === "direct") &&
    channel !== "" &&
    conversation !== ""
  return { key, direct, channel, conversation, sender: String(e.senderId ?? c.senderId ?? "").trim() }
}
function wholeReplies(config, channel) {
  const ch = String(channel || "").trim().toLowerCase()
  const section = config && config.channels && config.channels[ch]
  if (!ch || !section) return false
  const off = (s) => s === "off" || s === false || (s && typeof s === "object" && s.mode === "off" && !(s.block && s.block.enabled === true))
  // blockStreaming: true is the older spelling of streaming.block.enabled.
  if (!off(section.streaming) || section.blockStreaming === true) return false
  const accounts = section.accounts && typeof section.accounts === "object" ? Object.values(section.accounts) : []
  if (accounts.some((a) => a && ((a.streaming !== undefined && !off(a.streaming)) || a.blockStreaming === true))) return false
  const defaults = config.agents && config.agents.defaults
  return !(defaults && defaults.blockStreamingDefault === "on")
}
function receiptAllowed(turn, ctx, channel, config) {
  const c = ctx || {}
  if (!turn || !turn.direct) return false
  if (!deliversNotes(channel) || turn.channel !== String(channel).trim().toLowerCase()) return false
  if (!c.sessionKey || turn.key !== c.sessionKey || !directSessionKey(c.sessionKey)) return false
  if (c.chatType !== undefined && c.chatType !== "direct") return false
  if (!c.runId || !turn.sender || String(c.senderId ?? "").trim() !== turn.sender) return false
  if (!wholeReplies(config, channel)) return false
  return ownerSender(config, channel, turn.sender)
}
function replyTakes(held, event, ctx) {
  const e = event || {}
  const c = ctx || {}
  if (!held || e.kind !== "final") return false
  const channel = String(e.channel || c.channelId || "").trim().toLowerCase()
  if (channel !== held.channel) return false
  if (!held.runId || (e.runId || c.runId) !== held.runId) return false
  if (conversationOf(c.conversationId, channel) !== held.conversation) return false
  if (c.senderId !== undefined && String(c.senderId).trim() !== held.sender) return false
  // The delivery's own word that it is a 1:1 chat, required: a session key
  // with no group word in it (session.scope "global") says nothing.
  if (!e.usageState || e.usageState.chatType !== "direct") return false
  const p = e.payload || {}
  if (p.audioAsVoice === true) return false
  return typeof p.text === "string" && p.text.trim() !== ""
}
function escapeMarkdown(text) {
  return String(text).replace(/[\\\x60*_~|\[\]]/g, "\\$&")
}
function receiptLine(text) {
  const shape = /^deja-vu\s+—\s+([^"]+?):\s+("(?:[^"\\]|\\.)*")(?:\s+\((.+)\))?$/
  for (const raw of String(text || "").split("\n")) {
    const m = shape.exec(raw.trim())
    if (!m) continue
    let topic = m[2].slice(1, -1)
    try {
      topic = JSON.parse(m[2])
    } catch {}
    // An older deja named no agent and put the date in the parentheses:
    // "you have been here: "…" (Oct 4 · via zorbit)".
    const old = { "you have been here": "you've been here", "this was done on another machine": "done on another machine" }[m[1]]
    if (old) {
      const when = !m[3] || /^via:?\s/.test(m[3]) ? "" : m[3].split(/\s+·\s+via:?\s+/)[0].trim()
      return "↺ " + old + ': "' + escapeMarkdown(topic) + '"' + (when ? " · " + escapeMarkdown(when) : "") + " · deja"
    }
    return "↺ " + escapeMarkdown(m[1]) + ': "' + escapeMarkdown(topic) + '" · deja'
  }
  return ""
}
function withReceipt(payload, line) {
  if (!payload || !line) return payload
  const presentation = payload.presentation && typeof payload.presentation === "object" ? payload.presentation : {}
  const blocks = Array.isArray(presentation.blocks) ? presentation.blocks : []
  return { ...payload, presentation: { ...presentation, blocks: [...blocks, { type: "context", text: "_" + line + "_" }] } }
}

// A cron or heartbeat run has no person waiting on its reply.
function turnChannel(ctx) {
  if (ctx?.trigger && ctx.trigger !== "user") return "";
  return ctx?.channel || ctx?.messageProvider || "";
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
          if (ctx?.trigger === "heartbeat") return;
          const line = toolLine(event, ctx);
          if (!line) return;
          const content = Array.isArray(event?.result?.content) ? event.result.content : [];
          return { result: { ...event.result, content: [...content, { type: "text", text: line }] } };
        } catch {}
      }, { runtimes: ["openclaw"] });
    }
    // What this project settled, at the start of the session. The digest
    // goes into the Project Context through agent:bootstrap, registered as a
    // plugin hook: unlike agent_turn_prepare it is not behind
    // allowConversationAccess, and unlike the hook pack beside this plugin it
    // runs under --local as well as in the gateway (2026.9.8). The event fires
    // on every agent run, and in the gateway the pack answers it too; both send
    // the transcript id with deja_once, so the session gets one digest.
    //
    // The transcript id, not the session key: the live stamp hook-context
    // writes keeps this session out of its own MCP recall by the id the index
    // knows it by, and agent:main:main names no transcript (#4582).
    const digest = (ctx) => {
      const at = where(ctx);
      return ask(["hook-context", "--plain"], {
        session_id: at.id,
        cwd: at.cwd,
        source: "startup",
        deja_once: true,
        deja_prompt_stamps: true,
      });
    };
    // By default every DM shares agent:main:main, so a receipt is tied to the
    // run, the conversation and the sender it was asked for, and every new
    // message or prompt in the session drops whatever an earlier turn left.
    const showReceipt = api.pluginConfig?.receipt !== false;
    const turns = new Map();
    const board = new Map();
    const keep = (map, key, value) => {
      map.delete(key);
      map.set(key, value);
      while (map.size > 256) map.delete(map.keys().next().value);
    };
    // before_dispatch is the hook told whether the message came in a group.
    api.on("before_dispatch", async (event, ctx) => {
      const turn = directTurn(event, ctx);
      if (!turn.key) return;
      board.delete(turn.key);
      keep(turns, turn.key, turn);
    });
    let bootstrap = false;
    // A plugin hook is wired only while hooks.internal.enabled is not false.
    if (typeof api.registerHook === "function" && api.config?.hooks?.internal?.enabled !== false) {
      try {
        api.registerHook(
          "agent:bootstrap",
          async (event) => {
            const context = event?.context;
            if (!context || !Array.isArray(context.bootstrapFiles)) return;
            const text = digest(context);
            if (!text) return;
            context.bootstrapFiles.push({ name: "DEJA-RECALL.md", path: "deja://recall", content: text, missing: false });
          },
          { name: "deja-digest", description: "deja's digest of this project's past sessions" },
        );
        bootstrap = true;
      } catch {}
    }
    // Without plugin hooks the digest goes in front of the first prompt
    // instead, which 2026.8.1+ runs only with allowConversationAccess.
    if (!bootstrap) {
      api.on(
        "agent_turn_prepare",
        async (_event, ctx) => {
          const text = digest(ctx);
          if (!text) return;
          return { prependContext: text };
        },
        { timeoutMs: 15000 },
      );
    }
    api.on(
      "before_prompt_build",
      async (event, ctx) => {
        const prompt = typeof event?.prompt === "string" ? event.prompt.trim() : "";
        if (!prompt) return;
        // A heartbeat poll is not a turn anyone asked for: recall on its
        // prompt has nothing to answer, and the stamp it wrote hid the
        // session from the user's other sessions for twenty minutes (#5270).
        if (ctx?.trigger === "heartbeat") return;
        // Recall skips what it already showed this agent session, keyed by the
        // session id. A payload without one turns that off: measured on a real
        // store, half of all injections were then a word-for-word repeat. The
        // event is {prompt, messages}; the session is on ctx (#4581).
        const key = ctx?.sessionKey || "";
        // A second prompt hook in the same run keeps the receipt deja already
        // spent on it; one from any other run drops what was left.
        if (key && board.get(key)?.runId !== ctx?.runId) board.delete(key);
        const at = where(ctx);
        const channel = turnChannel(ctx);
        const turn = key ? turns.get(key) : undefined;
        if (key) turns.delete(key);
        const payload = { prompt, session_id: at.id, cwd: at.cwd };
        // One spawn either way: the JSON reply where the receipt can be shown
        // (its context for the model, the receipt kept for the reply), --plain
        // anywhere else, which leaves the receipt unspent.
        let recall;
        if (showReceipt && receiptAllowed(turn, ctx, channel, api.config)) {
          const reply = splitHookReply(ask(["hook-prompt"], payload));
          const line = receiptLine(reply.notes);
          if (line) keep(board, key, { ...turn, runId: ctx.runId, line });
          recall = reply.context;
        } else {
          recall = ask(["hook-prompt", "--plain"], payload);
        }
        // Silence is the common case — the hook speaks only when the user's
        // own history answers what they just asked.
        if (!recall) return;
        return { prependContext: recall };
      },
      { timeoutMs: 15000 },
    );
    // The final reply of the same run takes its receipt, as a context block
    // under the answer. The transcript the model reads is written before this.
    if (showReceipt) {
      api.on("reply_payload_sending", async (event, ctx) => {
        const key = event?.sessionKey || ctx?.sessionKey || "";
        const held = key && board.get(key);
        if (!held || event?.kind !== "final") return;
        // The final reply settles it either way: shown here or dropped.
        board.delete(key);
        if (!replyTakes(held, event, ctx)) return;
        return { payload: withReceipt(event.payload, held.line) };
      });
    }
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
        if (id) ask(["hook-session-end"], { session_id: id, cwd: ctx?.workspaceDir || seen.get(ctx?.sessionKey || "")?.dir || process.cwd() });
      },
      { timeoutMs: 5000 },
    );
  },
};
`, exe, openclawPluginID)
}
