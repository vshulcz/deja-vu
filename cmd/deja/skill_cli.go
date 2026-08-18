package main

import (
	"os"
	"path/filepath"
)

// The CLI-only skill. deja's other skill names the six MCP tools, which exist
// only after `deja install` wires a harness. A machine that has the binary and
// nothing else can search its whole history through the shell — and the agent on
// it had no way to learn that, because the only skill deja shipped described an
// API that was not there (#1320). The two live side by side and point at each
// other; the same index answers both.
const (
	cliSkillName = "deja-search"
	cliSkillDesc = "Search the user's past AI coding sessions with the deja CLI. Use when they say things like 'didn't we fix this before', 'what did we decide about X', or before re-debugging an error that may already be solved."
)

const cliSkillBody = `Search deja before re-deriving past work: when the user refers to earlier sessions or decisions, before debugging an error, and before implementing something that may already exist. It searches this machine's own history across every AI coding tool used on it, going back further than deja itself was installed.

This skill drives the ` + "`deja`" + ` binary through the shell. If the deja MCP tools (recall, recall_context, blame, fix, how, remember) are available in this session, use those instead — same index, one less hop. They appear only when ` + "`deja install`" + ` has wired this harness.

## Finding something

- ` + "`deja search --json \"<query>\"`" + `: the most specific token available — an exact error string, function name, file path, or flag. Several words are ANDed. Only this user's own sessions, never library docs or general knowledge.
- ` + "`deja ctx <query|id-prefix>`" + `: a full digest of the single best-matching session, once a hit looks right and the reasoning behind it matters. Takes no flags.
- ` + "`deja show <id-prefix> --harness <name> --json`" + `: the turns themselves, paged with ` + "`--offset`" + ` and ` + "`--limit`" + `. Use the id and harness a hit printed.
- ` + "`deja blame <path> --json`" + `: before editing, refactoring or deleting a file, the prior sessions that discussed it, so you know why it is shaped the way it is. Session history, not git authorship.
- ` + "`deja fix \"<pasted error>\"`" + `: the commands that followed that same error before, in sessions where it did not come back. Paste the failing output verbatim.
- ` + "`deja how <what>`" + `: the real command with the real flags this machine runs for a build, test, deploy or script, ordered by how many sessions ran it. A guessed invocation is plausible and fails on this setup.
- ` + "`deja remember \"<text>\"`" + `: store one durable decision after it is settled, as a single self-contained fact. Not transcripts, not anything already obvious from the code.

Useful flags on search: ` + "`--harness`" + `, ` + "`--project`" + `, ` + "`--since 30d`" + `, ` + "`--role user|assistant|tool|files|command|edit`" + `, ` + "`--session <id>`" + `, ` + "`--limit 1-100`" + `, ` + "`--all`" + `, ` + "`--re`" + ` for a regular expression.

## Reading a result

` + "`--json`" + ` returns an envelope: ` + "`tier`" + `, ` + "`total`" + `, ` + "`capped`" + `, ` + "`hits`" + `.

- ` + "`tier: \"relevance\"`" + ` means **nothing matched** — those are the nearest sessions deja could find, and counting them as hits overstates what is there. ` + "`tier: \"error\"`" + ` IS a match: the query was an error and those sessions hit it, matched by signature rather than by words.
- ` + "`total`" + ` is how many sessions matched; ` + "`capped`" + ` says a cap hid some of them. Read those two for coverage, never the length of ` + "`hits`" + `.
- ` + "`policy_withheld`" + `, when present, is how many matching sessions this machine's trust policy kept out of the answer — an empty result and a rule are different answers.
- A hit may carry ` + "`superseded`" + ` with a date: the user's own later judgement on that session. Do not repeat a rejected approach, prefer a replacement over what it replaced, and treat a stale result as needing confirmation before acting on it. A hit without it carries no judgement either way.

## Saying what you used

When recalled history genuinely helps — a reused fix, a skipped re-debug, even a partial hint that changed your approach — tell the user in one short line what was recalled and how you used it: "deja-vu recalled: we hit this JWT skew in March — reusing that fix". Say nothing about recalls that did not help. This is provenance, not advertising; a note on every call would be noise.

## Limits worth respecting

- Result windows are bounded. Do not report corpus-wide counts, or claim a complete audit, from the number of hits you got back.
- If ` + "`deja`" + ` is not on PATH or the index is empty, say that history search is unavailable. Do not invent what it might have found.
- Vary the wording and try a second query before concluding nothing is there. Exact tokens match best, so an error string beats a paraphrase of it.`

// cliSkillPath is the cross-agent skills directory, the same one the MCP skill
// uses for the eight harnesses that read it. A CLI-only install has no wired
// harness to key off, so there is nowhere harness-specific to put this.
func cliSkillPath() string {
	return filepath.Join(homeDir(), ".agents", "skills", cliSkillName, "SKILL.md")
}

func cliSkillFile() string {
	return "---\nname: " + cliSkillName + "\ndescription: " + cliSkillDesc + "\n---\n\n" + cliSkillBody + "\n"
}

// writeCLISkill puts the skill in place. Called by warmup, which is the only
// command a CLI-only install runs: the point of #1320 is that this path never
// touches an agent's configuration, and a skill file is deja's own directory
// rather than a harness's config. An edited copy is kept, the same as any other
// skill deja writes.
func writeCLISkill() error {
	path := cliSkillPath()
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// Kept without saying so. writeSkillOfOurs prints "skill: kept your edited
	// …" when it refuses, which is right for an install someone is watching and
	// wrong here: warmup runs on its own, its stdout is otherwise empty, and
	// that line would appear on every run for as long as the edit lives.
	// `deja install --force` still takes deja's version back.
	if !forceGuidance && skillWasEdited(path, old) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeSkillOfOurs(path, old, []byte(cliSkillFile()))
}

// cliSkillStillWanted reports whether any harness deja wired is staying. One
// target leaving out of eight must not take the skill from the rest — every one
// of them can still shell out to the binary. With nothing recorded there is no
// harness to keep it for, and the person asking to uninstall gets what they
// asked for.
func cliSkillStillWanted(leaving []string) bool {
	going := map[string]bool{}
	for _, t := range leaving {
		going[t] = true
	}
	for _, t := range readWiringState().Targets {
		if !going[t] {
			return true
		}
	}
	return false
}

// removeCLISkill takes it back on uninstall, and only when the file on disk is
// the one deja left: a copy the user rewrote is theirs.
func removeCLISkill() error {
	path := cliSkillPath()
	old, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if skillWasEdited(path, old) {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	// Only the directory deja named itself, and only a real one: os.Remove
	// unlinks a symlink whatever stands behind it, so a skills tree someone
	// keeps in their dotfiles would go while the skills it points at stayed
	// (the bound pruneGuidanceDirs already holds).
	if dir := filepath.Dir(path); filepath.Base(dir) == cliSkillName && isRealDir(dir) {
		_ = os.Remove(dir)
	}
	return nil
}
