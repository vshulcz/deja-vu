package main

import (
	"bytes"
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

If these tools are not available in this session, the same index is reachable through the shell: ` + "`deja search --json \"<query>\"`" + `, ` + "`deja ctx <query>`" + `, ` + "`deja blame <path> --json`" + `.

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
// each finds a skill placed only there. The rest are from their own docs:
// Zed 1.4.2 replaced its rules library with Agent Skills and loads them from
// ~/.agents/skills globally, which is the file deja already writes.
//
// Writing one file instead of one per harness is not only tidier. Gemini prints
// "Skill conflict detected" when the same skill exists in both its own
// directory and the shared one, so having both is a visible fault rather than
// harmless duplication.
var sharedSkillHarnesses = map[string]bool{
	"cursor": true, "gemini": true, "kimi": true, "qwen": true,
	"roo": true, "codex": true, "goose": true, "openclaw": true,
	"omp": true, "deepseek": true, "zed": true,
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
		// This file is for @vibe-kit/grok-cli, which reads <cwd>/.grok/GROK.md
		// first and only falls back to the home copy, so it never shadows a
		// project's own instructions. Grok Build ignores it — its home rules are
		// Agents.md, AGENTS.md, Claude.md and CLAUDE.md, and `grok inspect` on
		// 1.0.5 lists those and not this. What reaches Grok Build is the shared
		// skill written alongside it, which the same command lists.
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
// writes, leaving every other line in it alone. It returns what the caller
// should say about the files it had to leave: a block whose markers do not pair
// cannot be bounded, and guessing its end is what deleted a file in #1705 — but
// leaving deja's instructions in front of an agent after an uninstall, without
// a word, is the other way to get it wrong (#2218).
func dropRetiredGuidance(harness string, uninstall bool) (note string, err error) {
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
			return note, err
		}
		if start, end := guidanceMarkerLines(string(old)); start < 0 || end < 0 {
			// A skill file has frontmatter rather than markers. Ours is
			// identifiable without guessing: we generate the directory name and
			// the name field, and both have to match before anything is
			// deleted, so a skill someone wrote themselves is never touched.
			if filepath.Base(path) == "SKILL.md" &&
				filepath.Base(filepath.Dir(path)) == "deja-history" &&
				!restoredGuidance[path] &&
				strings.Contains(string(old), "name: deja-history") {
				if err := os.Remove(path); err != nil {
					return note, err
				}
				// Put back what install replaced, before the directory is
				// considered empty: install promised the reader's copy was
				// kept, and removing deja's file without restoring it left
				// that copy as a .bak beside nothing (#2581).
				restored, rerr := restoreReplacedGuidance(path, harness)
				if rerr != nil {
					return note, rerr
				}
				if restored {
					continue
				}
				// Take the directory too, but only if we emptied it.
				_ = os.Remove(filepath.Dir(path))
				continue
			}
			// A start with no end: deja's text is still in a file the agent
			// reads, and only the reader can decide where it ends. Said on the
			// way out, where "leave nothing behind" is the promise; on the way
			// in the block is deja's own guidance twice over, and "delete this
			// by hand" is not advice for someone installing.
			if start >= 0 && uninstall {
				note = path + ": deja's guidance block has no end marker, so it was left as it is — delete from " +
					guidanceStart + " to the end of that block by hand"
			}
			continue
		}
		next, uerr := updateGuidanceBlock(string(old), true)
		if uerr != nil {
			return note, markerErrorFor(path, uerr)
		}
		// A file that held nothing but our block was ours to begin with, so
		// leaving it behind empty would be litter rather than someone's content.
		if strings.TrimSpace(next) == "" {
			if err := os.Remove(path); err != nil {
				return note, err
			}
			continue
		}
		if _, err := writeIfChanged(path, old, []byte(next)); err != nil {
			return note, err
		}
	}
	return note, nil
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
		if other == leaving || removingTargets[other] {
			continue
		}
		if sharedSkillHarnesses[other] {
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
	return writeSkillOfOurs(path, old, []byte(skillFile(skillBody)))
}

// writeGuidanceFile writes guidance, refusing to replace a skill someone has
// edited. A block inside the user's own AGENTS.md is edited surgically and is
// not at risk; a SKILL.md is a whole file deja owns and replaces.
func writeGuidanceFile(path string, old, next []byte) (string, error) {
	if !strings.HasSuffix(path, "SKILL.md") {
		return writeIfChanged(path, old, next)
	}
	if !forceGuidance && skillWasEdited(path, old) {
		fmt.Printf("skill: kept your edited %s — `deja install --force` to take deja's version\n", shortHome(path))
		return "kept", nil
	}
	// deja cannot tell its own pre-marks copy from a file someone else wrote at
	// the same path, and treating every unmarked file as a stranger's would
	// freeze the skill on every machine installed before marks existed
	// (skill_marks.go). What it can do is say what it replaced, rather than
	// calling it an update and leaving the backup unmentioned (#1703).
	replacing := len(old) > 0 && !bytes.Equal(old, next) && !skillIsMarked(path)
	if replacing {
		// backupOnce keeps the first .bak it ever made and skips the rest, so
		// the promise below would have named a copy of some older file while
		// the one being destroyed went unsaved. This copy is of the content
		// deja is about to replace, which is the only one the message can
		// honestly point at (#1703).
		if err := os.WriteFile(path+".bak", old, 0o600); err != nil {
			return "", err
		}
	}
	action, err := writeIfChanged(path, old, next)
	if err != nil {
		return action, err
	}
	if replacing && action != "unchanged" {
		fmt.Printf("skill: replaced %s, which deja has no record of writing — your copy is at %s\n",
			shortHome(path), shortHome(path+".bak"))
	}
	rememberSkill(path, next)
	return action, nil
}

// writeSkillOfOurs writes a skill unless the copy on disk is not the one deja
// left there. An edited skill is kept and reported rather than blocking the
// install: the person who tuned the wording keeps it, the rest of the install
// still happens, and --force is how to ask for deja's version back.
func writeSkillOfOurs(path string, old, next []byte) error {
	_, err := writeGuidanceFile(path, old, next)
	return err
}

func installGuidance(harness string, uninstall bool) (installResult, error) {
	path := guidancePath(harness)
	if path == "" {
		return installResult{}, nil
	}
	retiredNote, err := dropRetiredGuidance(harness, uninstall)
	if err != nil {
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
				return installResult{Path: path, Action: "unchanged", Note: retiredNote}, nil
			}
			// One file serves several harnesses, so removing it because one of
			// them was uninstalled would take the memory away from the rest.
			if sharedSkillHarnesses[harness] && sharedSkillStillWanted(harness) {
				return installResult{Path: path, Action: "kept", Note: retiredNote}, nil
			}
			if restoredGuidance[path] {
				return installResult{Path: path, Action: "kept", Note: retiredNote}, nil
			}
			if err := os.Remove(path); err != nil {
				return installResult{}, err
			}
			// And put back what install replaced. It said where the copy went
			// — "your copy is at …SKILL.md.bak" — and removing deja's file
			// without restoring it left the reader's own skill as a backup
			// beside nothing (#2581).
			restored, rerr := restoreReplacedGuidance(path, harness)
			if rerr != nil {
				return installResult{}, rerr
			}
			if restored {
				return installResult{Path: path, Action: "restored", Note: retiredNote}, nil
			}
			return installResult{Path: path, Action: "removed", Note: retiredNote}, nil
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
		// grok writes a twin below; check its markers before touching this
		// file, or a refusal there leaves this one already rewritten (#1705).
		//
		// Only on the way in. Removing is the other direction: the two blocks
		// are bounded independently, and refusing to take out the one that can
		// be taken out left a complete deja block in a file nobody had been
		// told about (#2210).
		if harness == "grok" && !uninstall {
			b, rerr := os.ReadFile(filepath.Join(sources.GrokHome(), "AGENTS.md"))
			if rerr != nil && !os.IsNotExist(rerr) {
				return installResult{}, rerr
			}
			if cerr := checkGuidanceMarkers(string(b)); cerr != nil {
				return installResult{}, markerErrorFor(filepath.Join(sources.GrokHome(), "AGENTS.md"), cerr)
			}
		}
		updated, uerr := updateGuidanceBlock(string(old), uninstall)
		if uerr != nil {
			// The twin still has to be dealt with: this file is the one that
			// cannot be bounded, and the other one's block is nobody's hostage.
			if harness == "grok" && uninstall {
				twin, terr := removeGrokTwinBlock()
				if terr != nil {
					return installResult{}, terr
				}
				// Both unbounded is the case where saying only one path sends
				// the reader to fix half of it and run into the other half.
				if twin != "" {
					return installResult{}, fmt.Errorf("%s: %w (and the same in %s)", path, uerr, twin)
				}
			}
			return installResult{}, markerErrorFor(path, uerr)
		}
		next = []byte(updated)
	}
	a, err := writeGuidanceFile(path, old, next)
	if err != nil {
		return installResult{}, err
	}
	if a == "kept" {
		return installResult{Path: path, Action: a, Note: retiredNote}, nil
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
		updatedAlt, uerr := updateGuidanceBlock(string(oldAlt), uninstall)
		if uerr != nil {
			return installResult{}, markerErrorFor(alt, uerr)
		}
		if _, werr := writeIfChanged(alt, oldAlt, []byte(updatedAlt)); werr != nil {
			return installResult{}, werr
		}
	}
	return installResult{Path: path, Action: a, Note: retiredNote}, nil
}

// markerErrorFor names the file a marker refusal is about. The refusal is the
// instruction to finish the job by hand, and it used to leave the reader
// looking for which of deja's files it meant (#2210).
func markerErrorFor(path string, err error) error {
	return fmt.Errorf("%s: %w", path, err)
}

// removeGrokTwinBlock takes deja's block out of grok's other file when this one
// cannot be bounded. Written for the uninstall direction only: the two blocks
// stand on their own, so one broken file is no reason to keep the other.
//
// The first return value is the twin's path when it is unbounded too, so the
// caller can name both files at once — `grok-auto` runs `grok` first and would
// otherwise fail on the same file twice and never mention this one.
func removeGrokTwinBlock() (unbounded string, err error) {
	alt := filepath.Join(sources.GrokHome(), "AGENTS.md")
	oldAlt, rerr := os.ReadFile(alt)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return "", nil
		}
		return "", rerr
	}
	updated, uerr := updateGuidanceBlock(string(oldAlt), true)
	if uerr != nil {
		return alt, nil
	}
	_, werr := writeIfChanged(alt, oldAlt, []byte(updated))
	return "", werr
}

func updateGuidanceBlock(old string, uninstall bool) (string, error) {
	newline := "\n"
	if strings.Contains(old, "\r\n") {
		newline = "\r\n"
	}
	// A marker without its pair means the block cannot be bounded. Appending a
	// fresh one left the file with two starts and one end, and the uninstall
	// after that cut from the first start to the only end — across the user's
	// own text — and deleted the file, because what was left was empty (#1705).
	if err := checkGuidanceMarkers(old); err != nil {
		return "", err
	}
	// Every complete block, not just the first: a file carrying two kept both
	// for ever, since the install removed one and appended one.
	for {
		start, end := guidanceMarkerLines(old)
		if start < 0 || end < 0 {
			break
		}
		old = old[:start] + old[end:]
	}
	if uninstall {
		return old, nil
	}
	old = strings.TrimRight(old, "\r\n")
	if old != "" {
		old += newline + newline
	}
	return old + strings.ReplaceAll(guidanceText("append"), "\n", newline), nil
}

// checkGuidanceMarkers reports whether every start marker has an end marker
// after it. Only whole-line markers count, which is what guidanceMarkerLines
// pairs — a marker written inline in a sentence is prose, and deja appends its
// own block below such a file rather than claiming that text.
//
// A lone end marker is left alone for the same reason: it is what an inline
// start looks like to a line scanner, and appending below it costs nobody
// anything. A start with no end is different — the block cannot be bounded,
// and cutting from it to the next end takes whatever the user wrote in
// between, which is how an uninstall came to delete the file (#1705).
func checkGuidanceMarkers(doc string) error {
	open := false
	for _, line := range strings.Split(doc, "\n") {
		switch strings.TrimSuffix(line, "\r") {
		case guidanceStart:
			if open {
				return fmt.Errorf("deja's guidance block has a start marker with no end marker after it — put the pair back, or delete the block entirely")
			}
			open = true
		case guidanceEnd:
			open = false
		}
	}
	if open {
		return fmt.Errorf("deja's guidance block has no end marker — put it back, or delete the block entirely")
	}
	return nil
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
	line := fmt.Sprintf("%s: guidance %s %s", harness, result.Action, result.Path)
	// A file deja knows about and could not act on. Printed under the action
	// rather than in place of it: the action is still what happened (#2218).
	if result.Note != "" {
		line += "\n" + strings.Repeat(" ", len(harness)+2) + result.Note
	}
	return line
}

// restoredGuidance is what this run has already put back. `uninstall claude-code`
// runs the auto variant too, and the second pass met the reader's own file,
// matched it on `name: deja-history` — which it must, to be that skill — and
// deleted what the first pass had just restored (#2581).
var restoredGuidance = map[string]bool{}

// restoreReplacedGuidance puts back the file install replaced, and reports
// whether it did. A backup holding deja's own text — an older guidance version,
// snapshotted by a later install — is dropped instead: nobody asked for that
// file, and leaving deja's own words behind after an uninstall is what #2575
// was about.
func restoreReplacedGuidance(path, harness string) (bool, error) {
	return restoreReplacedFile(path, func(b []byte) bool { return isOurGuidance(b, harness) })
}

// restoreReplacedFile is the shared shape: put back the copy install made, or
// drop it when it holds deja's own words rather than the reader's. ours decides
// which, per kind of file — a skill by its frontmatter, a command file by the
// subcommands it names (#2600).
func restoreReplacedFile(path string, ours func([]byte) bool) (bool, error) {
	bak := path + ".bak"
	b, err := os.ReadFile(bak)
	if err != nil {
		return false, nil
	}
	if ours(b) {
		return false, os.Remove(bak)
	}
	// The one write that does not go through writeIfChanged: a file deja
	// cannot read is not one to put the snapshot back over (#2751).
	if _, err := readConfig(path); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return false, err
	}
	if err := os.Remove(bak); err != nil {
		return false, err
	}
	restoredGuidance[path] = true
	fmt.Printf("deja: put back %s, the copy install replaced\n", shortHome(path))
	return true, nil
}

// dejaSkillDescription is the line deja writes into every skill's frontmatter.
// A file carrying it is deja's own — from this build or an older one, which is
// the case that matters: an older skill has no marks, so install treats it as a
// stranger's and backs it up, and putting that back on the way out leaves deja's
// words on a machine deja was just removed from (#2585, the shape of #2575).
//
// What it costs: a reader who rewrote the body and kept deja's description
// verbatim is read as deja rather than as themselves. Their own description —
// one line, the part a person changes first when they make the skill theirs —
// is what separates the two.
const dejaSkillDescription = "description: Search the user's past AI coding sessions."

func isOurGuidance(b []byte, harness string) bool {
	if harness != "" && bytes.Equal(bytes.TrimSpace(b), bytes.TrimSpace([]byte(guidanceText(harness)))) {
		return true
	}
	head, _, _ := strings.Cut(string(b), "\n---")
	// Either skill deja writes: the MCP one and the CLI one carry their own
	// fixed description, and a person making one of them theirs edits that line
	// first (#2585, #2596).
	return strings.Contains(head, dejaSkillDescription) || strings.Contains(head, cliSkillDesc)
}
