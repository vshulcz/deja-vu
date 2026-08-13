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

// sharedSkillHarnesses read the cross-agent skills directory defined by the
// Agent Skills standard. Measured, not assumed, for gemini, openclaw and qwen:
// each finds a skill placed only there. The rest are from their own docs.
//
// Writing one file instead of one per harness is not only tidier. Gemini prints
// "Skill conflict detected" when the same skill exists in both its own
// directory and the shared one, so having both is a visible fault rather than
// harmless duplication.
var sharedSkillHarnesses = map[string]bool{
	"cursor": true, "gemini": true, "kimi": true, "qwen": true,
	"roo": true, "codex": true, "goose": true, "openclaw": true,
}

// sharedSkillPath is the one file all of them read. Claude Code is deliberately
// not among them: its docs list every directory it scans and this is not one.
func sharedSkillPath() string {
	return filepath.Join(homeDir(), ".agents", "skills", "deja-history", "SKILL.md")
}

func guidancePath(harness string) string {
	if sharedSkillHarnesses[harness] {
		return sharedSkillPath()
	}
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
	case "hermes":
		// Top-level, not inside the plugin deja generates: a plugin-bundled
		// skill in Hermes is opt-in, absent from skills_list and never in the
		// system prompt, so the agent would never find it on its own.
		return filepath.Join(sources.HermesHome(), "skills", "deja-history", "SKILL.md")
	case "grok":
		// grok reads <cwd>/.grok/GROK.md first and only falls back to the
		// home copy, so this never shadows a project's own instructions.
		return filepath.Join(sources.GrokHome(), "GROK.md")
	case "opencode":
		// opencode reads skills from its config directory, which is the cheaper
		// channel: a block in AGENTS.md is in context for the whole session
		// whether or not anyone asks about past work.
		return filepath.Join(opencodeConfigHome(), "opencode", "skills", "deja-history", "SKILL.md")
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

// skillFile wraps a body in the SKILL.md frontmatter every harness expects.
// The name has to match the directory, and the description is the only part
// loaded before the skill is used, so it carries the trigger phrases.
func skillFile(body string) string {
	return "---\nname: deja-history\ndescription: Search the user's past AI coding sessions. Use when they say things like 'didn't we fix this before', 'what did we decide about X', or before re-debugging an error that may already be solved.\n---\n\n" + body + "\n"
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

		return skillFile(body)
	}
	return guidanceStart + "\n" + guidanceBody + "\n" + guidanceEnd + "\n"
}

// retiredGuidancePaths are where previous versions of deja wrote guidance for a
// harness that has since moved. Install strips deja's block from it, because a
// harness that reads both would otherwise carry the old copy in every session
// forever — the file belongs to the user and nothing else would ever clean it.
func retiredGuidancePaths(harness string) []string {
	switch harness {
	case "opencode":
		return []string{filepath.Join(opencodeConfigHome(), "opencode", "AGENTS.md")}
	case "gemini":
		return []string{
			filepath.Join(sources.GeminiHome(), "GEMINI.md"),
			filepath.Join(sources.GeminiHome(), "skills", "deja-history", "SKILL.md"),
		}
	case "qwen":
		return []string{
			filepath.Join(sources.QwenConfigDir(), "QWEN.md"),
			filepath.Join(sources.QwenConfigDir(), "skills", "deja-history", "SKILL.md"),
		}
	case "kimi":
		return []string{
			filepath.Join(sources.KimiConfigDir(), "AGENTS.md"),
			filepath.Join(sources.KimiConfigDir(), "skills", "deja-history", "SKILL.md"),
		}
	case "codex":
		return []string{filepath.Join(sources.CodexHome(), "AGENTS.md")}
	case "cursor":
		return []string{filepath.Join(sources.CursorCLIHome(), "skills", "deja-history", "SKILL.md")}
	case "roo":
		return []string{rooRulesPath(), filepath.Join(homeDir(), ".roo", "skills", "deja-history", "SKILL.md")}
	}
	return nil
}

// dropRetiredGuidance removes deja's marked block from a file it no longer
// writes, leaving every other line in it alone.
func dropRetiredGuidance(harness string) error {
	keep := guidancePath(harness)
	for _, path := range retiredGuidancePaths(harness) {
		// A relocation variable can point a harness's own directory at the
		// shared one. Retiring what we are about to write — or what seven
		// other harnesses read — would delete the live skill.
		if path == keep || path == sharedSkillPath() {
			continue
		}
		old, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if start, end := guidanceMarkerLines(string(old)); start < 0 || end < 0 {
			// A skill file has frontmatter rather than markers. Ours is
			// identifiable without guessing: we generate the directory name and
			// the name field, and both have to match before anything is
			// deleted, so a skill someone wrote themselves is never touched.
			if filepath.Base(path) == "SKILL.md" &&
				filepath.Base(filepath.Dir(path)) == "deja-history" &&
				strings.Contains(string(old), "name: deja-history") {
				if err := os.Remove(path); err != nil {
					return err
				}
				// Take the directory too, but only if we emptied it.
				_ = os.Remove(filepath.Dir(path))
			}
			continue
		}
		next := updateGuidanceBlock(string(old), true)
		// A file that held nothing but our block was ours to begin with, so
		// leaving it behind empty would be litter rather than someone's content.
		if strings.TrimSpace(next) == "" {
			if err := os.Remove(path); err != nil {
				return err
			}
			continue
		}
		if _, err := writeIfChanged(path, old, []byte(next)); err != nil {
			return err
		}
	}
	return nil
}

// sharedSkillStillWanted reports whether a harness other than the one being
// removed still reads the shared skill, according to what install recorded.
// Without this, uninstalling one of eight would silently blind the other seven.
func sharedSkillStillWanted(leaving string) bool {
	targets := readWiringState().Targets
	// No record means we cannot show we are the last reader. Keeping a file
	// nobody needs costs a few hundred bytes; removing one seven harnesses
	// still read costs them their memory, and nothing would report it.
	if len(targets) == 0 {
		return true
	}
	for _, t := range targets {
		other := guidanceHarness(t)
		if other != leaving && sharedSkillHarnesses[other] {
			return true
		}
	}
	return false
}

// writeSharedSkill puts the cross-agent skill in place for a harness whose own
// guidance lives elsewhere. Removal goes through the same guard the shared
// readers use: one harness leaving must not take the file from the rest.
func writeSharedSkill(harness string, uninstall bool) error {
	path := sharedSkillPath()
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if uninstall {
		if len(old) == 0 || sharedSkillStillWanted(harness) {
			return nil
		}
		return os.Remove(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err = writeIfChanged(path, old, []byte(skillFile(skillBody)))
	return err
}

func installGuidance(harness string, uninstall bool) (installResult, error) {
	path := guidancePath(harness)
	if path == "" {
		return installResult{}, nil
	}
	if err := dropRetiredGuidance(harness); err != nil {
		return installResult{}, err
	}
	// Grok Build reads the cross-agent skills directory, so it gets the shared
	// skill on top of its own guidance rather than instead of it: a second
	// product, @vibe-kit/grok-cli, shares ~/.grok and reads GROK.md, and
	// retiring that block would take deja away from whoever runs it.
	if harness == "grok" {
		if err := writeSharedSkill(harness, uninstall); err != nil {
			return installResult{}, err
		}
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
			// One file serves several harnesses, so removing it because one of
			// them was uninstalled would take the memory away from the rest.
			if sharedSkillHarnesses[harness] && sharedSkillStillWanted(harness) {
				return installResult{Path: path, Action: "kept"}, nil
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

// guidanceHarness maps an install target to the harness whose guidance file it
// shares. An -auto target is the same harness with hooks on top, so it gets the
// same guidance — stripped generally rather than listed, because the three that
// were listed were the only three that ever got it (#1199).
func guidanceHarness(harness string) string {
	if harness == "claude-auto" {
		return "claude-code"
	}
	return strings.TrimSuffix(harness, "-auto")
}

// guidanceOwnsWholeFile reports whether install writes the whole file rather
// than a marked block inside someone else's. A skill file lives in deja's own
// directory and has no marker in it; AGENTS.md and its siblings belong to the
// user, and deja only ever appends a marked block there.
func guidanceOwnsWholeFile(harness string) bool {
	// A shared-skill harness owns a file too — the same one as its neighbours,
	// which is what the uninstall guard is for.
	if sharedSkillHarnesses[harness] {
		return true
	}
	switch harness {
	case "claude-code", "claude", "antigravity", "copilot", "pi", "opencode", "hermes":
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
