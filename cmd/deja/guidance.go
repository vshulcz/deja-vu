package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

const (
	guidanceStart = "<!-- deja guidance:start -->"
	guidanceEnd   = "<!-- deja guidance:end -->"
)

const guidanceBody = `Before re-deriving past work, search deja when the user refers to past work, previous sessions, or what was decided before. Use the deja MCP tools:

- recall: search history with a specific error, function, or decision.
- recall_context: get a concise digest of the best matching session.

Example: for "what did we decide about token refresh?", call recall with query "token refresh decision", then call recall_context if the result needs more detail.

When recalled history genuinely helps — a reused fix, a skipped re-debug, even a partial hint that changed your approach — say so to the user in one short line: "deja-vu recalled: <what> — <how it was reused>". Never credit recalls that did not help.`

// skillBody is the same guidance for harnesses that read a skill file. A skill
// is loaded when its description looks relevant, so the manual costs nothing in
// the sessions that never search history — which is why the detail that would be
// too expensive to keep in an MCP tool description, or in a guidance block that
// sits in context all session, belongs here instead.
const skillBody = `Search deja before re-deriving past work: when the user refers to earlier sessions or decisions, before debugging an error, and before implementing something that may already exist. It searches this machine's own history across every AI coding tool used on it, going back further than deja itself was installed.

## Finding something

- recall: search with the most specific token available — an exact error string, function name, file path, or flag. Several words are ANDed. Not for library docs or general knowledge; only this user's own sessions.
- recall_context: a full digest of the single best-matching session, once a recall hit looks right and the reasoning behind it matters.
- blame: before editing, refactoring or deleting a file, the prior sessions that discussed it, so you know why it is shaped the way it is. Session history, not git authorship.
- fix: paste a failing output verbatim to see the commands that followed that same error before, in sessions where it did not come back.
- how: the real command with the real flags this machine runs for a build, test, deploy or script, ordered by how many sessions ran it. A guessed invocation is plausible and fails on this setup.
- remember: store one durable decision after it is settled, as a single self-contained fact. Not transcripts, not anything already obvious from the code.

## Reading a result

A result may carry a bracketed marker with a date — that is the user's own later judgement on that session, and it is not advisory. Do not repeat a rejected approach. Prefer a replacement over what it replaced. Treat a stale result as needing confirmation before acting on it. A result with no marker carries no judgement in either direction.

## Saying what you used

When recalled history genuinely helps — a reused fix, a skipped re-debug, even a partial hint that changed your approach — tell the user in one short line what was recalled and how you used it: "deja-vu recalled: we hit this JWT skew in March — reusing that fix". Say nothing about recalls that did not help. This is provenance, not advertising; a note on every call would be noise.

## Limits worth respecting

- Result windows are bounded. Do not report corpus-wide counts, or claim a complete audit, from the number of hits you got back.
- If deja is unavailable or the index is empty, say that history search is unavailable. Do not invent what it might have found.
- Vary the wording and try a second query before concluding nothing is there. Exact tokens match best, so an error string beats a paraphrase of it.`

func guidancePath(harness string) string {
	switch harness {
	case "claude-code", "claude":
		return filepath.Join(sources.ClaudeConfigDir(), "skills", "deja-history", "SKILL.md")
	case "antigravity":
		// Inside the plugin, not beside it. Antigravity ingests skills/ from a
		// directory marked by plugin.json — which is what `agy plugin validate`
		// confirms ("skills: 1 processed") — and a SKILL.md written one level up
		// is read by nothing. doctor checked the same wrong path, so it reported
		// guidance missing on a machine where the skill was installed and
		// working.
		return filepath.Join(antigravityConfigHome(), "plugins", antigravityPluginName, "skills", "deja-history", "SKILL.md")
	case "copilot":
		return filepath.Join(homeDir(), ".copilot", "skills", "deja-history", "SKILL.md")
	case "pi":
		return filepath.Join(sources.PiConfigDir(), "skills", "deja-history", "SKILL.md")
	case "cursor":
		// Cursor was the one harness deja wrote nothing for: it has no
		// user-level instructions file to append a block to. It does read
		// user-level skills, which is a place that did not exist when that was
		// decided.
		return filepath.Join(sources.CursorCLIHome(), "skills", "deja-history", "SKILL.md")
	case "qwen":
		return filepath.Join(sources.QwenConfigDir(), "QWEN.md")
	case "codex":
		return filepath.Join(sources.CodexHome(), "AGENTS.md")
	case "kimi":
		return filepath.Join(sources.KimiConfigDir(), "AGENTS.md")
	case "gemini":
		return filepath.Join(sources.GeminiHome(), "GEMINI.md")
	case "grok":
		// grok reads <cwd>/.grok/GROK.md first and only falls back to the
		// home copy, so this never shadows a project's own instructions.
		return filepath.Join(sources.GrokHome(), "GROK.md")
	case "opencode":
		return filepath.Join(opencodeConfigHome(), "opencode", "AGENTS.md")
	case "roo":
		// Global rules are read verbatim into the system prompt for every
		// mode and every task, which is the only always-on channel Roo has.
		return rooRulesPath()
	default:
		return ""
	}
}

func opencodeConfigHome() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg
	}
	return filepath.Join(homeDir(), ".config")
}

func guidanceText(harness string) string {
	if guidanceOwnsWholeFile(harness) {
		body := skillBody
		if harness == "copilot" {
			body = "deja does not index Copilot history. It is a consumer: search the memory the other harnesses on this machine wrote.\n\n" + skillBody
		}
		if harness == "pi" {
			body = `If the deja MCP tools are available (via pi-mcp-adapter), use them:

- recall: search history with a specific error, function, or decision.
- recall_context: get a concise digest of the best matching session.

If MCP is not available, use the deja CLI via bash instead:

- Search: bash("deja 'connection pool exhausted'")
- Context: bash("deja ctx 'connection pool exhausted'")
- Blame: bash("deja blame src/db.go")
- Remember: bash("deja remember 'we use advisory locks because redis lost messages'")

Example: for "what did we decide about token refresh?", try recall first; if unavailable, run bash("deja 'token refresh decision'").

When recalled history genuinely helps, say so to the user in one short line: "deja-vu recalled: <what> — <how it was reused>". Never credit recalls that did not help.`
		}

		return "---\nname: deja-history\ndescription: Search the user's past AI coding sessions. Use when they say things like 'didn't we fix this before', 'what did we decide about X', or before re-debugging an error that may already be solved.\n---\n\n" + body + "\n"
	}
	return guidanceStart + "\n" + guidanceBody + "\n" + guidanceEnd + "\n"
}

func installGuidance(harness string, uninstall bool) (installResult, error) {
	path := guidancePath(harness)
	if path == "" {
		return installResult{}, nil
	}
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return installResult{}, err
	}
	var next []byte
	if guidanceOwnsWholeFile(harness) {
		if uninstall {
			if len(old) == 0 {
				return installResult{Path: path, Action: "unchanged"}, nil
			}
			if err := os.Remove(path); err != nil {
				return installResult{}, err
			}
			return installResult{Path: path, Action: "removed"}, nil
		} else {
			next = []byte(guidanceText(harness))
			// A skill inside an antigravity plugin directory is ingested only
			// when plugin.json marks that directory as a plugin — without it the
			// whole directory is skipped silently, which `agy plugin validate`
			// reports as "missing plugin.json". `install antigravity-auto`
			// writes the marker; plain `install antigravity` did not, so the
			// guidance landed somewhere nothing would read.
			if harness == "antigravity" {
				if err := ensureAntigravityPluginMarker(); err != nil {
					return installResult{}, err
				}
			}
		}
	} else {
		next = []byte(updateGuidanceBlock(string(old), uninstall))
	}
	a, err := writeIfChanged(path, old, next)
	if err != nil {
		return installResult{}, err
	}
	// grok is three CLIs sharing one directory: the one that still reads
	// GROK.md is not the one being maintained, and grok-dev reads AGENTS.md
	// instead. Writing both is the only way to cover a user of either.
	if harness == "grok" {
		alt := filepath.Join(sources.GrokHome(), "AGENTS.md")
		oldAlt, rerr := os.ReadFile(alt)
		if rerr != nil && !os.IsNotExist(rerr) {
			return installResult{}, rerr
		}
		if _, werr := writeIfChanged(alt, oldAlt, []byte(updateGuidanceBlock(string(oldAlt), uninstall))); werr != nil {
			return installResult{}, werr
		}
	}
	return installResult{Path: path, Action: a}, nil
}

func updateGuidanceBlock(old string, uninstall bool) string {
	newline := "\n"
	if strings.Contains(old, "\r\n") {
		newline = "\r\n"
	}
	start, end := guidanceMarkerLines(old)
	if start >= 0 && end >= 0 {
		old = old[:start] + old[end:]
	}
	if uninstall {
		return old
	}
	old = strings.TrimRight(old, "\r\n")
	if old != "" {
		old += newline + newline
	}
	return old + strings.ReplaceAll(guidanceText("append"), "\n", newline)
}

func guidanceMarkerLines(s string) (start, end int) {
	start, end = -1, -1
	offset := 0
	for _, line := range strings.SplitAfter(s, "\n") {
		content := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if start < 0 && content == guidanceStart {
			start = offset
		} else if start >= 0 && content == guidanceEnd {
			end = offset + len(line)
			break
		}
		offset += len(line)
	}
	return start, end
}

func guidanceHarness(harness string) string {
	switch harness {
	case "claude-auto":
		return "claude-code"
	case "codex-auto":
		return "codex"
	case "opencode-auto":
		return "opencode"
	default:
		return harness
	}
}

// guidanceOwnsWholeFile reports whether install writes the whole file rather
// than a marked block inside someone else's. A skill file lives in deja's own
// directory and has no marker in it; AGENTS.md and its siblings belong to the
// user, and deja only ever appends a marked block there.
func guidanceOwnsWholeFile(harness string) bool {
	switch harness {
	case "claude-code", "claude", "antigravity", "copilot", "pi", "cursor":
		return true
	}
	return false
}

// guidanceStatus reports whether deja's guidance is in that file, not merely
// whether the file exists. Anyone who keeps their own AGENTS.md was told deja
// had written guidance there when nothing from deja had ever been installed
// (#637). Install leaves a marked block in a shared file, so the marker is
// what to look for — and "the file is there but ours is not" is a different
// answer from "there is no file", because only the first means someone else
// owns it. A skill file deja writes whole has no marker and does not need one.
func guidanceStatus(harness string) string {
	// One form throughout: the path lookup took the raw name and the
	// whole-file check took the normalised one, so the function contradicted
	// itself about which it accepts.
	harness = guidanceHarness(harness)
	path := guidancePath(harness)
	if path == "" {
		return "unsupported"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "missing"
	}
	// An empty file is an interrupted write, not guidance — the one shape
	// where "the file exists" still fails to mean "we wrote it" even in a
	// directory deja owns.
	if len(strings.TrimSpace(string(b))) == 0 {
		return "absent"
	}
	if guidanceOwnsWholeFile(harness) || strings.Contains(string(b), guidanceStart) {
		return "written"
	}
	return "absent"
}

func guidanceResult(harness string, uninstall bool) (installResult, error) {
	return installGuidance(guidanceHarness(harness), uninstall)
}

func guidanceOutput(harness string, result installResult) string {
	if result.Path == "" {
		return fmt.Sprintf("%s: guidance unsupported", harness)
	}
	return fmt.Sprintf("%s: guidance %s %s", harness, result.Action, result.Path)
}
