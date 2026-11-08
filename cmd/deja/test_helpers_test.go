package main

import (
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func indexSearch(dir string, o search.Options) ([]model.Session, error) {
	r, err := index.SearchDetailed(dir, o)
	return r.Sessions, err
}

// trimBriefTitle bounds a title for the brief and strips what a terminal would
// act on rather than print. Titles are user-typed text arriving verbatim: a
// carriage return rewinds the line, an escape recolours the rest of the
// screen, a bell rings on every refresh. The `recent` lines have printed them
// raw since they existed; this is one place for all of them (#634 set the same
// rule for the status bar).
func trimBriefTitle(t string) string { return trimBriefTitleTo(t, briefTitleMax) }

// helpHidden names the commands deliberately kept out of `deja help`. They are
// wired by `deja install` and read by a harness, not typed by a person: the
// session-start and compaction hooks, goose's two, the refresh worker and the
// warmup probe. The list exists so the omission is a decision with a reason
// rather than drift — help listed five hook commands and hid five, including
// hook-context, because the text is hand-maintained and nothing compared it
// with the dispatch table (#1654). TestHelpCoversEveryCommand is that
// comparison.
var helpHidden = map[string]bool{
	"help":              true,
	"hook-codewhale":    true,
	"hook-context":      true,
	"hook-goose":        true,
	"hook-goose-prompt": true,
	"hook-mcp-call":     true,
	"hook-precompact":   true,
	"hook-refresh":      true,
	"hook-session-end":  true,
	"hook-stop":         true,
	"reasonix-ext":      true,
	"warmup-status":     true,
}

// completionHiddenCommands are the ones deja runs for itself. Leaving a command
// out is a decision recorded here rather than a name missing from three scripts.
var completionHiddenCommands = map[string]bool{
	"warmup-status": true,
	// Reasonix starts it as the plugin's runtime and talks JSON-RPC to it.
	"reasonix-ext": true,
}

func hookDigest(dir string) string {
	digest, _, _, _, _, _, _ := hookDigestResult(dir)
	return digest
}

func hookDigestResult(dir string) (string, int, int64, []string, int, []string, []string) {
	return hookDigestResultFor(dir, "", "")
}

// unplacedFiles counts transcripts under root that the harness's own filter did
// not pick up. A harness that changes its layout in a new version presents
// exactly this way: quietly fewer sessions, no error, and a directory size that
// still looks right (#701).
//
// unplacedFiles counts the transcripts under a root that deja did not read,
// split by whether it declined them on purpose. Everything was one number
// before, and on a machine that spawns subagents most of it is the deliberate
// skip: this store reported "1192 not recognised here" of which 596 were
// subagent transcripts deja is written to leave alone (#1384). A number that
// large reads as the tool failing to understand the user's own history, which
// is the one thing doctor exists to rule out.
func unplacedFiles(root string, seen []string, skipped func(string) bool) (unread, byRule int) {
	return unplacedFilesIn(root, seen, skipped, false)
}

func skillIsTheCommand(harness string) bool {
	return slices.Contains(skillIsTheCommandHarnesses(), harness)
}

// doctorHarnesses lists every store, the form `deja doctor --all` prints.
func doctorHarnesses(w io.Writer, dir string) {
	doctorHarnessStores(w, dir, true)
}

// resetEnvironmentOnce lets a test ask for a session's first block again. The
// marker file is per store and a test's store is its own; the process flag is
// shared, so it is reset under the same lock the server takes.
func resetEnvironmentOnce() {
	environmentMu.Lock()
	environmentSpent = false
	environmentMu.Unlock()
}

// printFixPairs writes the pairs in the prose form, one remedy per pair.
func printFixPairs(stdout io.Writer, pairs []index.FixPair) error {
	return printFixPairsFrom("", stdout, pairs)
}

// copySources maps each session in ss that is a fork or copy of another in ss
// to that other's position. A fork that names its source is believed; an
// unmarked one (claude --fork-session, opencode --fork) is read whole and
// judged by what it holds, which costs a read only for the pairs maybe says
// look alike (#5320).
func copySources(dir string, ss []model.Session, maybe func(src, c int, sameShape bool) bool) map[int]int {
	return copySourcesAmong(dir, ss, maybe, nil, nil)
}

// latestUserRequest returns the newest user turn in an antigravity transcript,
// or "" when the file holds none it can read.
func latestUserRequest(path string) string {
	return latestUserRequestFromSteps(transcriptTailSteps(path))
}

// latestToolFailure is the output of the newest tool step that ended badly, or
// "" when the last thing the agent did was not a failure. PostToolUse gets the
// error but its contract allows no answer at all (checked on antigravity-cli
// 1.1.13: it must return an empty object), so the moment a command fails is
// reachable only from the invocation that follows it.
//
// Only the newest tool step is read: a failure two steps back has already been
// answered or worked around, and injecting it again would be talking about
// something the agent has moved on from.
func latestToolFailure(path string) string {
	return latestToolFailureFromSteps(transcriptTailSteps(path))
}

// builtNote is what the first session start after a build says about what
// the build found: the counts, and on a second line one finding when the
// history holds one. Someone who installed deja from a marketplace
// never runs `deja install` and never sees the proof the CLI prints; for
// them the index was built in silence and recall stayed invisible until it
// happened to fire (#3073). Once per index.
func builtNote(dir string) string { return builtNoteFor(dir, true) }

// runHookPrecompact is deliberately best effort: Claude must be able to
// compact even when the input is incomplete or the index cannot start.
func runHookPrecompact(dir string) { runHookPrecompactFor(dir, "") }

// runHookContext prints session-start context. plain=false emits the Claude
// Code / Codex hook JSON envelope; plain=true prints the bare digest for
// hosts that inject raw text (the opencode plugin).
func runHookContext(dir string, plain bool) error {
	return runHookContextMode(dir, plain, false)
}

// runHookPrompt is the UserPromptSubmit hook: search the user's own prompt
// against the index (relevance, not recency) and inject a compact hint only
// when something genuinely matches. Empty output means stay silent — a hook
// that talks every turn is wallpaper. It never builds or refreshes the index:
// this path runs on every prompt and must stay ~milliseconds.
func runHookPrompt(dir string, stdin io.Reader, stdout io.Writer) error {
	return runHookPromptMode(dir, stdin, stdout, false)
}

// askedRecently reports whether this reader already had an answer to the same
// question inside the window. Scanned from the end, like the cooldowns: the
// file is append-only and the newest rows are the ones that matter.
func askedRecently(dir, sid, key string, window time.Duration) bool {
	return loadSeen(dir).asked(sid, key, window)
}

// toolHookLine is what the agent is told, or "" for the silence that is the
// default answer. It gates on the tool: a command line is worth saying only for
// the tool that runs a command, and a file's history only before the file is
// changed — Read, Glob and NotebookRead carry a file_path too, and a hook wired
// with a wide matcher would otherwise fire on every one of them.
// toolHookLine is the line for an action when nothing has been said yet.
func toolHookLine(dir, cwd string, input toolHookInput) string {
	return toolHookLineSkipping(dir, cwd, input, func(string) bool { return false })
}

// commandHookLine is the line for a command when nothing has been said yet.
func commandHookLine(dir, cwd, cmd string) string {
	return commandHookLineSkipping(dir, cwd, cmd, "", func(string) bool { return false })
}

func fileHookLine(dir, cwd, path string) string {
	return fileHookLineOutsideWithEdit(dir, cwd, path, "", nil)
}

// fileHookLineOutside leaves out the sessions self reports: the one the hook
// fires in and what counts as it. It is in the index once it has touched the
// file, and as the newest it was counted and its own last reply quoted back as
// the file's history (#4380).
func fileHookLineOutside(dir, cwd, path string, self func(index.SessionMeta, string) bool) string {
	return fileHookLineOutsideWithEdit(dir, cwd, path, "", self)
}

// promotedDecisionFor returns the newest accepted promoted note belonging to one
// of these sessions. Accepted only, for the reason projectConventions gives: a
// decision later reversed carries a non-accepted state, and LoadPromotedNotes
// keeps the latest state per source.
func promotedDecisionFor(metas []index.SessionMeta) string {
	return promotedDecisionForFile("", metas)
}

func promotedDecisionForFile(path string, metas []index.SessionMeta) string {
	return promotedDecisionForFileIn(index.DefaultDir(), path, metas)
}

// fileHookRanLine is the command the sessions that touched this file ran and
// the transcript saw finish cleanly, or "" when there is no such pattern.
//
// Reads one sidecar and the manifest rows the caller already has: this is on
// the per-action path, where the file line's own history cost 133 ms before
// #3605 moved it into the manifest.
func fileHookRanLine(dir string, metas []index.SessionMeta) string {
	return fileHookRanLineFor(dir, "", metas)
}

// weekNoteAt is that one line, or "". It goes out on the JSON path only — the
// plain path is the model's context, where a receipt to the person is noise.
//
// The first session after install starts the clock rather than reporting:
// "deja this week: 1 recall" a minute after the install would be the hook
// counting itself.
func weekNoteAt(dir string, now time.Time) string { return weekNoteFor(dir, now, true) }

// refreshAiderContext regenerates the read-only file from the same digest the
// hooks inject elsewhere, so aider users get what every other harness gets.
func refreshAiderContext(dir string) error {
	return writeAiderContext(aiderContextBody(dir))
}

func updateClaudeSessionStartHook(root map[string]any, exe string, uninstall bool) map[string]any {
	root = updateClaudeHook(root, "SessionStart", hookRun(exe, "hook-context"), "", uninstall)
	return root
}

// hermesCatalogEdits are where the copy in extensions/hermes differs from the
// one deja install writes: it comes from the Hermes plugin catalog rather than
// from deja, and finds deja on PATH.
var hermesCatalogEdits = [][2]string{
	{"Generated by deja install — safe to delete; regenerate with: deja install hermes-auto.",
		"From github.com/vshulcz/deja-vu, extensions/hermes. Installed by hermes plugins install."},
	{"the deja binary is not at %s — install it with:", "the deja binary is not on PATH — install it with:"},
	{", then run \"\n            \"deja install hermes-auto\" % DEJA", "\""},
}

// hermesCatalogPy is the provider as extensions/hermes ships it: the same
// code with deja taken from PATH, since nothing wrote the path in.
func hermesCatalogPy() string {
	py := hermesMemoryPy("deja")
	for _, r := range hermesCatalogEdits {
		if strings.Count(py, r[0]) != 1 {
			panic("hermesCatalogPy: the provider no longer holds " + r[0])
		}
		py = strings.Replace(py, r[0], r[1], 1)
	}
	return py
}

func traeIDEHooksPath() string { return traeIDEPresent()[0].hooksPath() }

func installZCodeHooks(exe string, uninstall bool) (installResult, error) {
	return installZCodeHooksWith(exe, uninstall, !uninstall && zcodeConfigIsLiveRuntimeConfig())
}

func recent(dir string, n int) ([]model.Session, error) {
	return recentMatching(dir, n, search.Options{})
}

// logoLines lays the info column beside the mark, vertically centred, and
// returns the composed lines. The live build display repaints these, so the
// layout has to be shared with printLogo rather than reimplemented.
func logoLines(info []string) []string { return logoLinesArt(catArt, info) }

// printLogo lays the info column beside the mark, vertically centred.
func printLogo(w io.Writer, info []string) { printLogoMood(w, info, moodReady) }

func loadAll(h string) []model.Session {
	var ss []model.Session
	for _, store := range sources.StoresToRead() {
		if (h == "" || store.Name == h) && store.Load != nil {
			ss = append(ss, store.Load()...)
		}
	}
	return ss
}

func recallContext(dir, q string) (string, error) {
	text, _, _, _, err := recallContextResult(dir, q, "")
	return text, err
}

// recallContextResult keeps the four-value shape its callers and tests read.
func recallContextResult(dir, q, harness string) (string, int, int64, []string, error) { //nolint:unparam // same shape as recallTextResult; changes_block_test.go calls it
	text, sessions, raw, ids, _, _, err := recallContextResultFrom(dir, q, harness)
	return text, sessions, raw, ids, err
}

// mcpContextFlag reports whether `deja mcp` was asked for the profile surface.
func mcpContextFlag(args []string) bool {
	for _, a := range args {
		if a == "--context-search" || a == "-context-search" {
			return true
		}
	}
	return false
}

func recallText(dir, q, harness string, limit, budget int) (string, error) {
	text, _, _, _, err := recallTextResult(dir, q, harness, limit, 0, budget)
	return text, err
}

// policyFilterHits drops search hits the trust policy blocks on this
// activation path. The default policy blocks nothing.
func policyFilterHits(activation string, hits []search.Hit) []search.Hit {
	kept, _ := policyFilterHitsCounted(activation, hits)
	return kept
}

// printNoMatches says how big the store actually is, not how many sessions the
// query happened to load.
//
// On the index-backed path the loaded set is empty by definition when nothing
// matched, so this printed "no matches in 0 indexed sessions" for a perfectly
// healthy index — and internal/index/manifest.go reserves that exact sentence
// as the signature of a corrupt store, on the grounds that a reader told the
// index holds zero sessions concludes the tool is broken rather than that
// their query missed. It fired on every ordinary miss, so the signature could
// not be used to recognise the failure it was written for (#637).
func printNoMatches(w io.Writer, dir, q string, regex bool) (mistypedCommand bool) {
	return printNoMatchesHinted(w, dir, q, regex, false)
}

// scrubBackupsIn is every copy this command left in one directory, for the tests
// and for the line that tells a reader where to look.
func scrubBackupsIn(dir string) []string {
	matches, err := filepath.Glob(filepath.Join(dir, "*"+scrubBackupExt+"*"))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	return matches
}

// cliSkillMeta is what a skill registry reads to know what the skill needs:
// ClawHub serves skills/deja-search as vshulcz/deja-search and checks the
// declared binary and install specs against what the skill does. It is
// OpenClaw's nested shape, which the Agent Skills spec does not allow: there
// metadata maps strings to strings, and Crush drops a skill whose metadata
// does not decode that way (#4378). So only the registry copy carries it; the
// file deja installs into ~/.agents/skills, which Crush reads, goes without.
// OpenClaw reads that directory too and loads a skill with no metadata.openclaw
// block as always eligible, which is right on a machine deja was installed on.
const cliSkillMeta = `metadata:
  openclaw:
    homepage: https://vshulcz.github.io/deja-vu/guide/memory-for-openclaw.html
    requires:
      bins:
        - deja
    install:
      - kind: brew
        formula: deja-vu
        bins: [deja]
      - kind: node
        package: "@vshulcz/deja-vu"
        bins: [deja]`

// cliSkillRegistryFile is the copy in the repository, the one ClawHub publishes.
func cliSkillRegistryFile() string {
	return "---\nname: " + cliSkillName + "\ndescription: " + cliSkillDesc + "\n" + cliSkillMeta + "\n---\n\n" + cliSkillBody + "\n"
}

// statuslineMemoryLine renders it. A count is a statistic; what the earlier
// session was about is a memory, so the title wins when there is one.
func statuslineMemoryLine(m fileMemory) string {
	return statuslineMemoryLineTo(m, statuslineMaxTitle)
}

// statuslineMemoryLineTo bounds the title to maxTitle columns — the unit
// safeForStatusline works in, since the bound exists to keep the bar inside the
// terminal. maxTitle <= 0 drops the title and falls through to the count form,
// which is what a bar too narrow to hold any of it gets.
func statuslineMemoryLineTo(m fileMemory, maxTitle int) string {
	return statuslineMemoryLineWithin(m, maxTitle, statuslineMaxName)
}

// trimStatuslineTitle makes a session title safe for a status bar. The title
// is whatever the user typed first, so it can carry a carriage return that
// rewrites the line, an ANSI escape that recolours the whole bar, or a bell.
func trimStatuslineTitle(t string) string { return safeForStatusline(t, statuslineMaxTitle) }

// emptyRecallAnswer distinguishes "there is nothing" from "there is nothing
// YET". A harness with no auto-recall wiring reaches deja over MCP and nothing
// else, so a tool result is the one place it can learn that the first index is
// still being built. Without this the agent reads a confident negative and
// tells the user they have no history.
func emptyRecallAnswer(dir, q string) string { return emptyRecallAnswerPolicy(dir, q, 0) }
