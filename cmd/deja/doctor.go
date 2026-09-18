package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// doctorVersionLookup fetches the latest released version. It is injected so
// tests can stub it — the real lookup talks to GitHub with a short budget.
type doctorVersionLookup func() (latest string, ok bool)

// doctorLookup is the dispatcher's lookup; overridable in tests so they never
// touch the network.
var doctorLookup = defaultDoctorVersionLookup()

func defaultDoctorVersionLookup() doctorVersionLookup {
	return func() (string, bool) {
		download := newHTTPUpdateDownloader(&http.Client{Timeout: 2 * time.Second})
		body, err := download(latestReleaseURL, maxReleaseJSON, "latest release")
		if err != nil {
			return "", false
		}
		var release updateRelease
		if err := json.Unmarshal(body, &release); err != nil {
			return "", false
		}
		latest := normalizeUpdateVersion(release.TagName)
		return latest, latest != ""
	}
}

// countSubagentFiles counts the child transcripts among the files a harness
// offered. They are read as their task and their answer rather than in full, so
// the row says how much of them is searchable.
func countSubagentFiles(seen []string) int {
	n := 0
	for _, p := range seen {
		if sources.IsSubagentPath(p) {
			n++
		}
	}
	return n
}

// runDoctor prints a self-diagnosis report. Diagnosis itself never fails, so
// both human and JSON reports keep exit status 0.
func runDoctor(w io.Writer, args []string, lookup doctorVersionLookup, dir string) error {
	jsonOutput := false
	deep := false
	offline := os.Getenv("DEJA_OFFLINE") == "1"
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		case "--offline":
			offline = true
		case "--deep":
			deep = true
		default:
			return fmt.Errorf("doctor: unknown flag %q", arg)
		}
	}
	if offline {
		lookup = nil
	}
	report := collectDoctorReport(lookup, dir)
	var deepReport *index.DeepReport
	if deep {
		// doctor is what the no-home refusal sends the reader to, so it runs
		// without one — but --deep takes the index lock before it reads, and
		// with a relative index dir that left `.cache/deja/index.db.lock` in
		// whatever directory they were standing in (#1692). There is no
		// database at a path like that to verify anyway.
		if !filepath.IsAbs(dir) {
			return fmt.Errorf("doctor --deep cannot find your index — set HOME, or DEJA_INDEX_DIR to an absolute path")
		}
		dr, err := index.DeepVerify(dir)
		if err != nil {
			return fmt.Errorf("doctor: deep verify: %w", err)
		}
		deepReport = &dr
		report.Deep = deepReport
	}
	if jsonOutput {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
		return deepDriftErr(deepReport)
	}
	doctorHarnesses(w, dir)
	printDoctorStoreWarnings(w, report.Stores)
	// The third cause of a files-to-sessions gap, after a parse failure (#861)
	// and an id collision (#1101): the reader forgot them. `last` and `stats`
	// have said so all along; the screen someone opens to check that a forget
	// took did not (#1108).
	if n := len(index.Tombstones()); n > 0 {
		fmt.Fprintf(w, "  %-12s %s forgotten here and kept out of the index (`deja forget --list`)\n", "forgotten", doctorCount(n, "session"))
	}
	fmt.Fprintln(w)
	doctorTools(w)
	fmt.Fprintln(w)
	doctorPolicy(w, dir)
	fmt.Fprintln(w)
	doctorMCP(w)
	fmt.Fprintln(w)
	doctorCommands(w)
	fmt.Fprintln(w)
	doctorPeers(w, dir, time.Now())
	fmt.Fprintln(w)
	doctorHooks(w)
	// After doctorHooks, not inside it: that function returns early on a
	// machine without claude settings, which is exactly a machine whose other
	// harnesses may still be wired to a binary that moved.
	doctorWiringExe(w)
	doctorDoubleInjections(w, dir)
	fmt.Fprintln(w)
	doctorIndex(w, report.Index, dir)
	fmt.Fprintln(w)
	if report.Embed != nil {
		doctorEmbed(w, *report.Embed)
	} else {
		doctorEmbed(w, doctorEmbedReport{State: "unavailable"})
	}
	fmt.Fprintln(w)
	if offline {
		fmt.Fprintln(w, "version: check skipped (offline)")
	} else {
		doctorVersion(w, func() (string, bool) { return report.Version.Latest, report.Version.Latest != "" })
	}
	if deepReport != nil {
		fmt.Fprintln(w)
		doctorDeep(w, *deepReport)
	}
	return deepDriftErr(deepReport)
}

// doctorDeep prints the source-vs-index proof. Everything above it is deja
// trusting its own bookkeeping; this section is the recount.
func doctorDeep(w io.Writer, r index.DeepReport) {
	fmt.Fprintln(w, "Deep verification:")
	fmt.Fprintf(w, "  checked  %s, %s, %s re-parsed, %s resolved\n",
		doctorCount(r.FilesChecked, "source file"),
		doctorCount(r.SessionsIndexed, "indexed session"),
		doctorCount(r.SampledFiles, "sampled file"),
		doctorCount(r.SampledPostings, "posting"))
	if len(r.Stale) > 0 {
		fmt.Fprintf(w, "  stale    %s changed since last pass — `deja index` will absorb them\n", doctorCount(len(r.Stale), "source"))
	}
	doctorKept(w, &r)
	if r.Clean() {
		// What this pass compares is message counts per session, plus the
		// structure around them: sizes, magic numbers, postings that resolve.
		// It cannot see a same-length edit inside a record, which leaves the
		// count identical and the session unreachable — so the line says what
		// was checked rather than promising nothing was lost (#1712).
		//
		// And only what was actually checked: nothing is sampled when every
		// source is stale, or when the sampled tokens carry no postings, and a
		// clean report then means "found nothing wrong", not "compared and
		// agreed".
		switch {
		case r.SampledFiles == 0 && r.SampledPostings == 0:
			fmt.Fprintln(w, "  status   nothing to compare — no source was in sync to re-parse and no sampled token carried postings")
		case r.SampledFiles == 0:
			fmt.Fprintln(w, "  status   the sampled postings resolve; no source was in sync to re-parse, so no message count was compared")
		case r.SampledPostings == 0:
			fmt.Fprintln(w, "  status   every sampled session's message count matches its source; no sampled token carried postings")
		default:
			fmt.Fprintln(w, "  status   every sampled session's message count matches its source, and the sampled postings resolve")
		}
		return
	}
	for _, f := range r.Findings {
		fmt.Fprintf(w, "  drift    [%s] %s\n", f.Kind, f.Detail)
	}
	fmt.Fprintf(w, "  status   %s — run `deja index --rebuild`\n", doctorCount(len(r.Findings), "finding"))
}

// doctorKept says which indexed transcripts are no longer on disk and are kept
// on purpose — the client's cleanup, not drift (#2970). Printed before the
// verdict so a reader sees it is not what any finding is about.
func doctorKept(w io.Writer, r *index.DeepReport) {
	if r == nil || len(r.Kept) == 0 {
		return
	}
	fmt.Fprintf(w, "  kept     %d transcript%s no longer on disk, still searchable — `deja forget <id>` drops one for good\n", len(r.Kept), pluralS(len(r.Kept)))
}

func deepDriftErr(r *index.DeepReport) error {
	if r == nil || r.Clean() {
		return nil
	}
	return fmt.Errorf("doctor: index drift detected (%s) — run `deja index --rebuild`", doctorCount(len(r.Findings), "finding"))
}

func doctorHooks(w io.Writer) {
	fmt.Fprintln(w, "Hooks:")
	// Every harness's row is printed whatever Claude Code's file says. This
	// used to return early when that one file was missing or unreadable, so a
	// machine without Claude Code — which is most of them — saw nothing at all
	// about the twelve other harnesses deja can wire. The one command someone
	// runs when memory is not working told them least exactly when they had
	// the most to check.
	defer doctorAutoRecall(w)
	defer doctorCodexHook(w)
	st := claudeHookWiringState()
	if st.absent {
		fmt.Fprintf(w, "  %-12s missing      %s\n", "claude-code", reportPath(st.path))
		return
	}
	if st.state == "unreadable" {
		fmt.Fprintf(w, "  %-12s unreadable   %s\n", "claude-code", reportPath(st.path))
		return
	}
	fmt.Fprintf(w, "  %-12s %-11s %s\n", "claude-code", st.state, reportPath(st.path))
	if len(st.missing) > 0 && len(st.missing) < len(claudeHookWiring) {
		// Named, because the difference is what the machine is missing out on:
		// a settings.json written by an older deja keeps working and quietly
		// lacks everything added since.
		fmt.Fprintf(w, "               %d of %d events wired — no %s; run `deja install`\n",
			len(claudeHookWiring)-len(st.missing), len(claudeHookWiring), strings.Join(st.missing, ", "))
	}
	if note := doctorHookRepeats(st.hooks, claudeHookWiring, "claude-auto"); note != "" {
		fmt.Fprintf(w, "  %-12s %s\n", "", note)
	}
	// Only when something here is actually wired: the note is about the binary
	// those entries name, and a file with no deja in it names none.
	if note := hookExeNote(st.path, "claude-auto"); note != "" && len(st.missing) < len(claudeHookWiring) {
		fmt.Fprintf(w, "  %-12s %s\n", "", note)
	}
	// The entries name the launcher now, and the launcher is always there —
	// what can be gone is everything it resolves to (#3422).
	if note := doctorLauncherNote(st.path, "claude-auto"); note != "" {
		fmt.Fprintf(w, "  %-12s %s\n", "", note)
	}
}

// doctorWiringExe reports configs that name a binary which is no longer there.
//
// Every hook and MCP entry deja writes holds an absolute path. Move the binary
// and those configs keep naming the old one: the harness fails to start deja
// on every session, and doctor happily printed "wired" for a hook that cannot
// run. The repair from #773 exists but runs from the hook path — the one path
// a dead binary cannot reach — so doctor is where a person finds out (#876).
func doctorWiringExe(w io.Writer) {
	st := readWiringState()
	if st.Exe == "" || len(st.Targets) == 0 {
		return
	}
	if _, err := os.Stat(st.Exe); err == nil {
		return
	}
	fmt.Fprintf(w, "  %-12s %-11s configs name %s, which is not there — `deja install %s` rewrites them for this binary\n",
		"wiring", "stale", st.Exe, strings.Join(st.Targets, " "))
}

// doctorCodexHook reports the codex session-start hook state. Codex gates
// hooks behind its own trust store: hooks.json can be perfectly wired while
// codex keeps the hook disabled — memory then silently never arrives.
func doctorCodexHook(w io.Writer) {
	st := codexHookWiringState()
	hooksPath, status, missing, hooks := st.path, st.state, st.missing, st.hooks
	if st.absent {
		// The plugin ships the same hooks under its own root, and codex trusts
		// those the same way. Nothing was installed here, and nothing is
		// missing either.
		if status == "plugin" {
			fmt.Fprintf(w, "  %-12s %-11s %s  (the Codex plugin carries the hooks; codex asks once to trust them)\n",
				"codex-hook", "plugin", hooksPath)
			return
		}
		fmt.Fprintf(w, "  %-12s missing      %s\n", "codex-hook", reportPath(hooksPath))
		return
	}
	if st.trustUnknown {
		fmt.Fprintf(w, "  %-12s %-11s %s  (cannot read %s, so whether codex trusts the hook is unknown)\n",
			"codex-hook", "wired", hooksPath, filepath.Join(sources.CodexHome(), "config.toml"))
		return
	}
	line := fmt.Sprintf("  %-12s %-11s %s", "codex-hook", status, hooksPath)
	if len(missing) > 0 {
		line += fmt.Sprintf("\n               %d of %d events wired — no %s; run `deja install`",
			len(codexHookWiring)-len(missing), len(codexHookWiring), strings.Join(missing, ", "))
	}
	if status == "untrusted" {
		line += "  (codex has not been shown it — open codex once and approve it, or run /hooks; until then `codex exec` runs no hook at all)"
	}
	// Trust is per hook there, so a machine can have one approved hook and
	// four that codex refuses to run — which it says on its own first screen
	// and this row used to call `wired` (#3654).
	if status == "wired" && st.pinned > 0 && st.approved < st.pinned {
		line += fmt.Sprintf("\n               %d of %d hooks approved — codex runs only those; open codex once and approve the rest (/hooks)",
			st.approved, st.pinned)
	}
	if status == "disabled" {
		line += "  (codex trusts but disabled it — re-enable in codex settings or hooks.state)"
	}
	if note := doctorHookRepeats(hooks, codexHookWiring, "codex-auto"); note != "" {
		line += fmt.Sprintf("\n  %-12s %s", "", note)
	}
	// Whatever codex thinks of the entry, it can still name a binary that is
	// gone — and an untrusted row said only that codex had not been shown it,
	// which is the state an upgraded machine sits in (#3502).
	if exe := hookExeNote(hooksPath, "codex-auto"); exe != "" {
		line += fmt.Sprintf("\n  %-12s %s", "", exe)
	}
	fmt.Fprintln(w, line)
}

func hookEventWired(hooks map[string]any, event, command string) bool {
	entries, _ := hooks[event].([]any)
	for _, entryAny := range entries {
		entry, _ := entryAny.(map[string]any)
		if entry == nil {
			continue
		}
		// Substring match: installs write the absolute binary path ahead of
		// the subcommand, so an exact compare would report every real
		// installation as missing.
		hs, _ := entry["hooks"].([]any)
		for _, hAny := range hs {
			h, _ := hAny.(map[string]any)
			if h == nil || h["type"] != "command" {
				continue
			}
			if cmd, _ := h["command"].(string); strings.Contains(cmd, command) {
				return true
			}
		}
	}
	return false
}

func doctorEmbed(w io.Writer, r doctorEmbedReport) {
	fmt.Fprintln(w, "Embedding:")
	// The sidecar's own line, not the endpoint's: the endpoint may be fine and
	// the file still unparseable, and saying "endpoint unreadable" would send
	// the reader after the wrong thing. Both lines print, because whether an
	// endpoint is configured is what decides if re-running `deja embed` fixes
	// it (#1960).
	if r.Sidecar == "unreadable" {
		fmt.Fprintf(w, "  sidecar    unreadable — %s\n", r.Error)
	}
	if r.Model == "" {
		fmt.Fprintf(w, "  endpoint   %s\n", r.State)
		return
	}
	fmt.Fprintf(w, "  endpoint   %s/model=%s/dim=%d\n", r.State, r.Model, r.Dim)
	if r.Sidecar == "unreadable" {
		return
	}
	fmt.Fprintf(w, "  sidecar    coverage=%.1f%%\n", r.Coverage)
}

// inDotDir reports whether the path sits inside a dot-directory below the
// root — a cache, a temp dir, anything a tool keeps for itself.
func inDotDir(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	for _, part := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			return true
		}
	}
	return false
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

// unplacedFilesIn is unplacedFiles with the one decision a caller can make:
// whether a dot directory under this root is the store itself.
func unplacedFilesIn(root string, seen []string, skipped func(string) bool, dotDirsAreTheStore bool) (unread, byRule int) {
	have := make(map[string]bool, len(seen))
	for _, p := range seen {
		have[filepath.Clean(p)] = true
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".jsonl", ".json":
		default:
			return nil
		}
		if have[filepath.Clean(p)] {
			return nil
		}
		// A store's own scratch is not a transcript deja failed to read: 452
		// of the 482 this machine reported for codex sat in `.tmp`, and a
		// count that size reads as a parser that cannot cope with the store.
		//
		//
		// Unless the store keeps its transcripts there: Antigravity files
		// everything under `.system_generated`, so the rule silenced its row
		// completely rather than trimming its noise. Codex is the opposite —
		// it writes in-progress rollouts under `.tmp` — which is why this is
		// the caller's decision and not something inferred from the files
		// (#3377).
		if !dotDirsAreTheStore && inDotDir(root, p) {
			return nil
		}
		// Nor is an extension's own state. The pi family keeps it beside the
		// transcripts, at `sessions/<project>/extensions/<name>/<id>.json` —
		// senpi's terminal extension writes one per session — and counting
		// those said "2 not recognised here" about a store whose every
		// transcript had just been indexed, while `doctor --json` for the same
		// store said ok (#3669).
		if inExtensionState(root, p) {
			return nil
		}
		if skipped != nil && skipped(p) {
			byRule++
			return nil
		}
		unread++
		return nil
	})
	return unread, byRule
}

// inExtensionState reports whether a path sits under an `extensions` directory
// inside the store. A transcript never does: the directory belongs to whatever
// extension the harness is running, and what it keeps there is state.
func inExtensionState(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == "extensions" {
			return true
		}
	}
	return false
}

// printDoctorStoreWarnings says what deja could not read and why.
func printDoctorStoreWarnings(w io.Writer, stores []doctorStore) {
	for _, store := range stores {
		switch store.State {
		case "parsed-zero":
			fmt.Fprintf(w, "  warning      %s files found but newest parsed to zero\n", store.Name)
		case "unreadable":
			// The store is there and deja cannot read it — usually a harness
			// that changed its format. Silence here reads as "you have no
			// history with that agent".
			// With the reason. "Please report it" and nothing to report is how
			// #1642 arrived: a store deja refused, and no way for its owner or
			// for us to tell which refusal it was.
			if store.Error != "" {
				fmt.Fprintf(w, "  warning      %s store cannot be read — %s; please report it\n",
					store.Name, store.Error)
			} else {
				fmt.Fprintf(w, "  warning      %s store cannot be read — its format may have changed; please report it\n", store.Name)
			}
		case "denied":
			// Not a format change and not an empty history: deja is not
			// allowed to read the files. On macOS this is usually Full Disk
			// Access rather than the file mode (#802). A store that is only
			// partly unreadable loses sessions from recall while looking whole
			// everywhere else, so it says which half it is (#816).
			what := "store cannot be read"
			if store.Partial {
				what = "store is only partly readable — some sessions are missing from recall"
			}
			fmt.Fprintf(w, "  warning      %s %s — permission denied on %s; check its permissions (on macOS, also Full Disk Access for your terminal)\n", store.Name, what, store.Denied)
		case "needs-sqlite3", "needs-zstd":
			// Not a format change: the parser could not run at all. Saying so
			// points at installing one package instead of at a bug report
			// against the harness (#792). Which package depends on the store —
			// zed and deepseek need zstd (#1758) — and a partly readable one
			// says so rather than reading as a store that is entirely gone.
			what := "store"
			if store.Partial {
				what = "store is only partly readable — part of it"
			}
			fmt.Fprintf(w, "  warning      %s %s needs %s — install it, then run `deja index`\n", store.Name, what, toolFromSkip(store.Skipped))
		}
	}
}

// storeDiskGone distinguishes a store whose disk went away from a harness that
// was never installed. Both leave the path missing; what differs is how much of
// the way there is missing. `~/.kimi-code/sessions` on a machine without kimi
// loses one level and its home is right there; a store on an ejected volume
// loses the whole chain (#933).
// Cursor and aider hand doctor their roots joined for display, and a joined
// string is no path to walk up from: it lost the whole chain by construction
// and cursor's row said `unplugged` on every machine.
func storeDiskGone(location string) bool {
	roots := doctorLocationRoots(location)
	for _, root := range roots {
		if !oneStoreDiskGone(root) {
			return false
		}
	}
	return len(roots) > 0
}

func doctorLocationRoots(location string) []string {
	var roots []string
	for _, part := range strings.Split(location, string(os.PathListSeparator)) {
		for _, root := range strings.Split(part, ", ") {
			if root = strings.TrimSpace(root); root != "" {
				roots = append(roots, root)
			}
		}
	}
	return roots
}

func oneStoreDiskGone(path string) bool {
	// Two levels is not enough for every store: `~/.local/share/goose/sessions`
	// and `~/.cline/data/sessions` lose three on a machine that never installed
	// them. A home directory that is there means the disk is there.
	if home := sources.Home(); home != "" && strings.HasPrefix(path, home+string(os.PathSeparator)) && dirExists(home) {
		return false
	}
	dir := filepath.Dir(path)
	for i := 0; i < 2; i++ {
		if dirExists(dir) {
			return false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return true
}

// noteBucketsRegrouped counts the day buckets in the index that this machine
// would not build now. Notes are grouped by
// the reader's day (#911), so a laptop that changed zones regroups them on the
// next rebuild: nothing is lost, but an id that was shared or pasted somewhere
// keeps resolving and starts naming a different note (#935).
func noteBucketsRegrouped(dir string) int {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return 0
	}
	type span struct{ started, updated time.Time }
	indexed := map[string]span{}
	for _, m := range metas {
		if m.Harness == "deja" {
			indexed[m.ID] = span{m.Started, m.Updated}
		}
	}
	if len(indexed) == 0 {
		return 0
	}
	here := map[string]span{}
	for _, s := range sources.LoadNotes() {
		here[s.ID] = span{s.Started, s.Updated}
	}
	if len(here) == 0 {
		return 0
	}
	// Notes written since the build only add buckets or extend them forward; a
	// bucket that vanished, or that now starts at a different note, means the
	// days were cut somewhere else — which is what a changed zone does.
	moved := 0
	for id, in := range indexed {
		if h, ok := here[id]; !ok || !h.started.Equal(in.started) {
			moved++
		}
	}
	return moved
}

func doctorHarnesses(w io.Writer, dir string) {
	fmt.Fprintln(w, "Harness stores:")
	sqlite := sources.SQLite3Available()

	// Files are what deja found; sessions are what they became. The two differ
	// whenever ids collide — a resumed transcript, a copied one, a harness that
	// reuses a thread id — and the difference is invisible in a row that only
	// counts files (#861).
	indexed := index.HarnessSessionCounts(dir)
	fromElsewhere := index.ImportedSessionCounts(dir)
	sharedRows := index.HarnessSharedCounts(dir)
	keptRows := index.HarnessKeptCounts(dir)

	// The same inspection the JSON form reports, so one command does not give
	// two answers about one store: `found` here and `unreadable` there (#999).
	inspected := map[string]string{}
	unchecked := map[string]bool{}
	partly := map[string]bool{}
	for _, check := range doctorStoreChecks() {
		store, _ := inspectDoctorStore(check)
		inspected[check.name] = store.State
		unchecked[check.name] = store.Unchecked
		partly[check.name] = store.Partial
	}

	printRow := func(name, path string, present bool, detail string) {
		status := "missing"
		// A store the reader excluded says so whether or not it is on disk:
		// "missing" would read as deja not finding it, and "found" as deja
		// about to read it. Neither is true (#3499).
		if inspected[name] == "excluded" {
			fmt.Fprintf(w, "  %-12s %-9s %s\n", name, "excluded",
				"not read — `harness:"+name+"` is in "+reportPath(sources.ExcludePath()))
			return
		}
		if present {
			status = "found"
			// A directory deja cannot open loses its sessions from recall
			// without a word — the failure #802/#816 closed, but only in
			// `doctor --json`, which has said `denied` all along while this
			// row, the one people read, said `found` (#993).
			switch inspected[name] {
			case "denied", "unreadable", "parsed-zero", "needs-sqlite3", "needs-zstd":
				status = inspected[name]
				if status == "denied" {
					if detail != "" {
						detail += ", "
					}
					// The warning block below names the path and what to do.
					// Whole or in part: the row folded both into "cannot be
					// read" while the warning under it and the search above
					// said the store still answers (#1034, #816).
					if partly[name] {
						detail += "partly unreadable"
					} else {
						detail += "cannot be read"
					}
				}
			}
			// A walk that stopped at its budget looked at part of the store,
			// and `found` on its own claims the whole of it (#1025).
			if unchecked[name] {
				if detail != "" {
					detail += ", "
				}
				detail += "permissions not fully checked"
			}
		} else if storeDiskGone(path) {
			// A store whose whole disk is gone is not a store that was
			// deleted, and "missing" on a row of transcripts reads as the
			// second thing — the failure #906 fixed on every write path, and
			// #931 on the index row one screen below this one (#933).
			status = "unplugged"
		}
		// Also when the store is missing: a machine whose history arrived by
		// `sync import` has no files at all, and doctor said nothing about the
		// sessions it does hold — the only surface that names them was stats
		// (#892).
		if n, ok := indexed[name]; ok {
			if detail != "" {
				detail += ", "
			}
			// Local and imported counted apart: on a store with both, "3
			// files, 8 indexed sessions" reads as a miscount, and the reader
			// who learned from #861 that files-against-sessions shows
			// collapsing has no way to read the other direction (#894).
			imported := fromElsewhere[name]
			switch imported {
			case 0:
				detail += doctorCount(n, "indexed session")
				// The gap between files and sessions has two causes, and they
				// read the same: a file that failed to parse, or two files
				// sharing an id. The manifest knows which (#1101).
				if sh := sharedRows[name]; sh > 0 {
					detail += fmt.Sprintf(", %d of them shared by two transcripts", sh)
				}
				// And the other way the numbers disagree: a session whose
				// transcript the client cleaned up, kept on purpose (#2970).
				if k := keptRows[name]; k == 1 {
					detail += ", 1 from a transcript no longer on disk"
				} else if k > 1 {
					detail += fmt.Sprintf(", %d from transcripts no longer on disk", k)
				}
			case n:
				detail += doctorCount(n, "indexed session") + " from elsewhere"
			default:
				detail += doctorCount(n-imported, "indexed session") + fmt.Sprintf(", %d more from elsewhere", imported)
			}
		}
		// A store path can come from the environment (DEJA_NOTES_FILE) or from
		// disk. On a fixed-width row a newline in it prints a line of its own
		// that reads as one of doctor's.
		line := fmt.Sprintf("  %-12s %-9s %s", name, status, reportPath(path))
		if detail != "" {
			line += "  (" + detail + ")"
		}
		fmt.Fprintln(w, line)
	}

	// printFiles is printRow for the harnesses that answer with a file list.
	// The count comes from the same filter the parser uses, so a store whose
	// layout differs slightly loses those files from every number deja prints
	// — the one thing `doctor` exists to rule out (#701).
	// printFilesSkippingIn is printFiles for a harness that has more than one
	// transcript root and declines some of its own files by a rule.
	printFilesSkippingIn := func(name, loc string, roots []string, present bool, seen []string, skipped func(string) bool) {
		detail := doctorCount(len(seen), "file")
		unread := 0
		byRule := 0
		for _, root := range roots {
			u, b := unplacedFiles(root, seen, skipped)
			unread += u
			byRule += b
		}
		if byRule > 0 {
			// The variable named the way it was read as the cause of the
			// skip — "skipped (DEJA_INCLUDE_SUBAGENTS=1)" — so somebody who
			// wanted those transcripts indexed set the thing the line said
			// was already set. It is the remedy, and it reads as one now.
			detail += fmt.Sprintf(", %d subagent transcripts skipped — set DEJA_INCLUDE_SUBAGENTS=1 to index them", byRule)
		} else if short := countSubagentFiles(seen); short > 0 && os.Getenv("DEJA_INCLUDE_SUBAGENTS") != "1" {
			// Read, but not in full: what a reader needs to know is which half
			// of those files is searchable, and how to get the rest (#3009).
			detail += fmt.Sprintf(", %d subagent transcripts read as task and answer — set DEJA_INCLUDE_SUBAGENTS=1 for the whole run", short)
		}
		if unread > 0 {
			detail += fmt.Sprintf(", %d not recognised here", unread)
		}
		printRow(name, loc, present, detail)
	}
	// printFilesSkipping is its one-root form.
	printFilesSkipping := func(name, path string, present bool, seen []string, skipped func(string) bool) {
		printFilesSkippingIn(name, path, []string{path}, present, seen, skipped)
	}
	printFiles := func(name, path string, present bool, seen []string) {
		printFilesSkipping(name, path, present, seen, nil)
	}
	// printFilesBeside is printFiles for a harness whose store keeps files
	// beside the transcripts that are not transcripts — Continue's
	// sessions.json (the list, which deja reads), Copilot's vscode.metadata.json
	// (the IDE's bookkeeping, #3303), Kimi's per-session state.json (the title
	// and working directory, #3309). Counted with the files, it made `doctor`
	// disagree with `deja sources` by one; counted as unread, it made every
	// store report a file deja could not read (#3297). So it is neither: the
	// count is the transcripts, and the note leaves the list alone.
	// printFilesBesideIn is printFilesBeside for a row whose printed location is
	// not one directory: cline names its modern store and its legacy roots on
	// the same line, and that string cannot be walked (#3360).
	printFilesBesideIn := func(name, loc string, walks []string, dotDirsAreTheStore, present bool, seen []string, beside ...string) {
		detail := doctorCount(len(seen), "file")
		placed := append(append([]string{}, seen...), beside...)
		unread := 0
		for _, walk := range walks {
			u, _ := unplacedFilesIn(walk, placed, nil, dotDirsAreTheStore)
			unread += u
		}
		if unread > 0 {
			detail += fmt.Sprintf(", %d not recognised here", unread)
		}
		printRow(name, loc, present, detail)
	}
	printFilesBeside := func(name, path string, present bool, seen []string, beside ...string) {
		printFilesBesideIn(name, path, []string{path}, false, present, seen, beside...)
	}

	claudeRoots := sources.ClaudeRoots()
	claudeLocation := strings.Join(claudeRoots, string(os.PathListSeparator))
	claudePresent := false
	for _, root := range claudeRoots {
		claudePresent = claudePresent || doctorExists(root)
	}
	printFilesSkippingIn("claude", claudeLocation, claudeRoots, claudePresent, sources.ClaudeFiles(),
		func(p string) bool { return !sources.ClaudeFileWanted(p) })

	codexRoots := sources.CodexRoots()
	codexLocation := strings.Join(codexRoots, string(os.PathListSeparator))
	codexPresent := false
	for _, root := range codexRoots {
		codexPresent = codexPresent || doctorExists(root)
	}
	printFilesBesideIn("codex", codexLocation, codexRoots, false, codexPresent, sources.CodexFiles(), sources.CodexSidecarFiles()...)

	ocDB := sources.OpencodeDB()
	printRow("opencode", ocDB, doctorFilePresent(ocDB), doctorSQLiteDetail(ocDB, sqlite))

	printRow("aider", doctorAiderLocation(), len(sources.AiderFiles()) > 0, doctorCount(len(sources.AiderFiles()), "file"))

	// The row names the store and counts what is under `tmp`, where the chats
	// are: Antigravity keeps its own store in a sibling directory of the same
	// root and has its own row, so walking the whole of ~/.gemini reported its
	// files — 55 of 77 on a real machine — as chats gemini failed to read
	// (#3397). The settings and the extensions beside them are not chats
	// either.
	geminiRoot := sources.GeminiRoot()
	printFilesBesideIn("gemini", geminiRoot, []string{filepath.Join(geminiRoot, "tmp")}, false,
		doctorExists(geminiRoot), sources.GeminiChatFiles(), sources.GeminiSidecarFiles()...)

	printRow("cursor", doctorCursorLocation(), doctorCursorPresent(), doctorCursorDetail(sqlite))

	// Present means a store that is there, not a path deja was told about:
	// AntigravityRoots hands back DEJA_ANTIGRAVITY_ROOT as given, so a machine
	// with the variable set to a directory that does not exist — a typo, a
	// removed install — read as `found` with nothing in it, which is the shape
	// every other row reports as `missing`.
	agyRoots := sources.AntigravityRoots()
	printFilesBesideIn("antigravity", doctorAntigravityLocation(), agyRoots, true, doctorAnyExists(agyRoots),
		sources.AntigravityTranscripts(), sources.AntigravitySidecarFiles()...)

	// The store root also holds Grok's settings, credentials and caches, which
	// are not transcripts and never will be; the sessions directory is what the
	// count is about (#3319).
	grokRoot := filepath.Join(sources.GrokRoot(), "sessions")
	printFilesBeside("grok", grokRoot, doctorExists(grokRoot), sources.GrokSessionFiles(), sources.GrokSidecarFiles()...)

	qwenRoot := filepath.Join(sources.QwenRoot(), "projects")
	// Beside, not unread: `<id>.runtime.json`, `meta.json` and
	// `extract-cursor.json` are qwen's own bookkeeping (#3676).
	printFilesBeside("qwen", qwenRoot, doctorExists(qwenRoot),
		sources.QwenSessionFiles(), sources.QwenSidecarFiles()...)

	kimiRoot := filepath.Join(sources.KimiRoot(), "sessions")
	printFilesBeside("kimi", kimiRoot, doctorExists(kimiRoot), sources.KimiSessionFiles(), sources.KimiSidecarFiles()...)

	gooseRoot := filepath.Join(sources.GooseRoot(), "sessions")
	printRow("goose", gooseRoot, doctorExists(gooseRoot) || doctorFilePresent(sources.GooseDB()), doctorGooseDetail(sqlite))

	hermesRoot := sources.HermesProfilesRoot()
	printRow("hermes", hermesRoot, doctorExists(hermesRoot), doctorCount(len(sources.HermesSessionFiles()), "store"))

	clineModern := sources.ClineSessionsDir()
	clineFiles := len(sources.ClineSessionFiles())
	clineLoc := clineModern
	if legacy := sources.ClineLegacyRoots(); len(legacy) > 0 {
		clineLoc += ", " + strings.Join(legacy, string(os.PathListSeparator))
	}
	// Every root the line names is walked, or the count would promise coverage
	// the row does not have: a stray file under a legacy tasks tree was
	// invisible while the line advertised that root (review of #3360).
	clineWalks := sources.ClineStoreRoots()
	printFilesBesideIn("cline", clineLoc, clineWalks, false, clineFiles > 0 || doctorExists(clineModern),
		sources.ClineSessionFiles(), sources.ClineSidecarFiles()...)

	rooFiles := len(sources.RooTaskFiles())
	rooLoc := "VS Code globalStorage rooveterinaryinc.roo-cline"
	if roots := sources.RooRoots(); len(roots) > 0 {
		rooLoc = strings.Join(roots, string(os.PathListSeparator))
	}
	printRow("roo", rooLoc, rooFiles > 0, doctorCount(rooFiles, "file"))

	// Kilo Code keeps the extension's task files and the CLI's database, so the
	// row names both and says which half answered (#3643).
	kiloTasks := len(sources.KiloTaskFiles())
	kiloLoc := "VS Code globalStorage " + sources.KiloExtensionID
	if roots := sources.KiloRoots(); len(roots) > 0 {
		kiloLoc = strings.Join(roots, string(os.PathListSeparator))
	}
	kiloDB := sources.KiloDB()
	kiloHasDB := doctorExists(kiloDB)
	if kiloHasDB {
		kiloLoc = kiloLoc + string(os.PathListSeparator) + kiloDB
	}
	kiloDetail := doctorCount(kiloTasks, "task file")
	if kiloHasDB {
		kiloDetail += ", CLI store present" + doctorDBPrereqNote(sqlite)
	}
	printRow("kilocode", kiloLoc, kiloTasks > 0 || kiloHasDB, kiloDetail)

	// Four stores whose formats deja already had: two pi descendants and two
	// flat-transcript clients (#3647).
	senpiRoot := sources.SenpiRoot()
	printFiles("senpi", senpiRoot, doctorExists(senpiRoot), sources.SenpiSessionFiles())
	kimchiRoot := sources.KimchiRoot()
	printFiles("kimchi", kimchiRoot, doctorExists(kimchiRoot), sources.KimchiSessionFiles())
	commandRoot := sources.CommandCodeRoot()
	// The checkpoint stream beside each transcript is named rather than left to
	// the unread count: it is not a conversation, and "1 not recognised here"
	// on a store deja reads correctly is the line that sends someone looking
	// for drift that is not there.
	printFilesBeside("commandcode", commandRoot, doctorExists(commandRoot),
		sources.CommandCodeSessionFiles(), sources.CommandCodeCheckpointFiles()...)
	// ZCode has two stores, the way Kilo does: the project transcripts and the
	// CLI's SQLite database. The count is the transcripts and the database is
	// named beside them, or the newest "file" is a database the transcript
	// reader cannot read and the row calls the store broken (#3675).
	zcodeRoot := sources.ZCodeRoot()
	zcodeTranscripts := sources.ZCodeTranscriptFiles()
	zcodeLoc := zcodeRoot
	zcodeDB := sources.ZCodeDB()
	zcodeHasDB := doctorExists(zcodeDB)
	if zcodeHasDB {
		zcodeLoc = zcodeLoc + string(os.PathListSeparator) + zcodeDB
	}
	zcodeDetail := doctorCount(len(zcodeTranscripts), "file")
	if zcodeHasDB {
		zcodeDetail += ", CLI store present" + doctorDBPrereqNote(sqlite)
	}
	printRow("zcode", zcodeLoc, doctorExists(zcodeRoot) || zcodeHasDB, zcodeDetail)
	gjcRoot := sources.GjcRoot()
	printFiles("gjc", gjcRoot, doctorExists(gjcRoot), sources.GjcSessionFiles())

	// Kiro's two clients write different files under one root, and the row says
	// which of them answered: a CLI user and an IDE user have nothing in common
	// but the directory (#3103).
	kiroCLI := len(sources.KiroCLIFiles())
	kiroIDE := len(sources.KiroIDEFiles())
	kiroRoot := sources.KiroRoot()
	kiroDetail := doctorCount(kiroCLI, "CLI file")
	if kiroIDE > 0 {
		kiroDetail += ", " + doctorCount(kiroIDE, "IDE file")
	}
	printRow("kiro", kiroRoot, kiroCLI+kiroIDE > 0, kiroDetail)

	// Cherry Studio writes Claude Code transcripts under its own app data, so
	// the row names the roots it found rather than the app directory (#3644).
	cherryFiles := len(sources.CherryStudioSessionFiles())
	cherryLoc := "CherryStudio/Data/Agents/.claude"
	if roots := sources.CherryStudioRoots(); len(roots) > 0 {
		cherryLoc = strings.Join(roots, string(os.PathListSeparator))
	}
	printRow("cherrystudio", cherryLoc, cherryFiles > 0, doctorCount(cherryFiles, "file"))

	continueDir := filepath.Join(sources.ContinueRoot(), "sessions")
	printFilesBeside("continue", continueDir, doctorExists(continueDir), sources.ContinueSessionFiles(),
		filepath.Join(continueDir, "sessions.json"))

	piRoot := sources.PiRoot()
	printFiles("pi", piRoot, doctorExists(piRoot), sources.PiSessionFiles())
	openclawRoot := sources.OpenClawRoot()
	printFilesBeside("openclaw", openclawRoot, doctorExists(openclawRoot), sources.OpenClawSessionFiles(), sources.OpenClawSidecarFiles()...)
	for _, db := range sources.OpenClawAgentDBs() {
		printRow("openclaw", db, doctorFilePresent(db), doctorSQLiteDetail(db, sqlite))
	}
	copilotRoot := sources.CopilotRoot()
	printFilesBeside("copilot", copilotRoot, doctorExists(copilotRoot), sources.CopilotSessionFiles(), sources.CopilotSidecarFiles()...)
	chatFiles := len(sources.CopilotChatSessionFiles())
	chatLoc := "VS Code Copilot Chat"
	if roots := sources.CopilotChatRoots(); len(roots) > 0 {
		chatLoc = strings.Join(roots, string(os.PathListSeparator))
	}
	printRow("copilot-chat", chatLoc, chatFiles > 0, doctorCount(chatFiles, "file"))
	// omp, deepseek and zed are read by the indexer and were named by nothing
	// here — a user of one of them had no row to check when their sessions did
	// not come back (#1738). The registry decides what is indexed, and a test
	// now holds these rows to it.
	ompRoot := sources.OmpRoot()
	printFiles("omp", ompRoot, doctorExists(ompRoot), sources.OmpSessionFiles())
	primeRoot := sources.PrimeRoot()
	printFiles("prime", primeRoot, doctorExists(primeRoot), sources.PrimeSessionFiles())
	ampRoot := sources.AmpRoot()
	printFiles("amp", ampRoot, doctorExists(ampRoot), sources.AmpThreadFiles())
	dshRoot := sources.DeepSeekRoot()
	printFiles("deepseek", dshRoot, doctorExists(dshRoot), sources.DeepSeekSessionFiles())
	zedDB := sources.ZedDB()
	printRow("zed", zedDB, doctorFilePresent(zedDB), doctorSQLiteDetail(zedDB, sqlite))
	// One store per project rather than one per machine, so the registry is
	// what makes them findable at all. Name it even when it lists nothing:
	// "the registry is empty" is the answer for someone whose crush sessions
	// did not come back.
	crushRegistry := filepath.Join(sources.CrushDataHome(), "projects.json")
	printRow("crush", crushRegistry, doctorFilePresent(crushRegistry), doctorCount(len(sources.CrushDBs()), "store"))
	for _, db := range sources.CrushDBs() {
		printRow("crush", db, doctorFilePresent(db), doctorSQLiteDetail(db, sqlite))
	}
	printRow("deja", sources.NotesFile(), doctorFilePresent(sources.NotesFile()), "notes")
	if n := noteBucketsRegrouped(dir); n > 0 {
		fmt.Fprintf(w, "  warning      %s of notes in the index %s not what this machine would build now — the zone changed, so the days regrouped; `deja index` renames them\n",
			doctorCount(n, "day"), verbIs(n))
	}
}

// toolFromSkip turns a skip reason into the package to install.
func toolFromSkip(reason string) string {
	switch {
	case strings.Contains(reason, "sqlite3") && strings.Contains(reason, "zstd"):
		return "the sqlite3 and zstd CLIs"
	case strings.Contains(reason, "zstd"):
		return "the zstd CLI"
	default:
		return "the sqlite3 CLI"
	}
}

// doctorDBPrereqNote is what a row has to add about the half of a store that
// needs the sqlite3 CLI. Kilo's and ZCode's rows named their database and said
// nothing about the tool that reads it, so on a machine without sqlite3 those
// sessions were missing from recall with the row reporting the store present
// (#3679). The rows for the stores that are only a database say it through
// doctorSQLiteDetail; these two have transcripts as well, so the note rides
// beside the count.
func doctorDBPrereqNote(sqlite bool) string {
	if sqlite {
		return ""
	}
	return " but the sqlite3 CLI is missing — those sessions are unavailable"
}

func doctorSQLiteDetail(db string, sqlite bool) string {
	fi, err := os.Stat(db)
	if err != nil || fi.Size() == 0 {
		return ""
	}
	d := humanBytes(fi.Size())
	if !sqlite {
		d += ", sqlite3 CLI missing — sessions unavailable"
	}
	return d
}

func doctorGooseDetail(sqlite bool) string {
	parts := []string{doctorCount(len(sources.GooseJSONLFiles()), "legacy file")}
	if fi, err := os.Stat(sources.GooseDB()); err == nil && fi.Size() > 0 {
		seg := humanBytes(fi.Size()) + " SQLite"
		if !sqlite {
			seg += ", sqlite3 CLI missing — modern sessions unavailable"
		}
		parts = append(parts, seg)
	}
	return strings.Join(parts, ", ")
}

func doctorCursorDetail(sqlite bool) string {
	parts := []string{doctorCount(len(sources.CursorTranscripts()), "CLI transcript")}
	dbs := sources.CursorDBs()
	if len(dbs) > 0 {
		var size int64
		for _, db := range dbs {
			if fi, err := os.Stat(db); err == nil {
				size += fi.Size()
			}
		}
		seg := fmt.Sprintf("%s IDE %s", doctorCount(len(dbs), "store"), humanBytes(size))
		if !sqlite {
			seg += ", sqlite3 CLI missing — IDE sessions unavailable"
		}
		parts = append(parts, seg)
	}
	return strings.Join(parts, ", ")
}

func doctorCursorPresent() bool {
	return len(sources.CursorTranscripts()) > 0 || len(sources.CursorDBs()) > 0
}

func doctorCursorLocation() string {
	// Contracted here rather than by the row: this location is two paths in one
	// string, and the row's own contraction only reaches the first (#2360).
	return strings.Join([]string{reportPath(sources.CursorUserRoot()), reportPath(sources.CursorCLIRoot())}, ", ")
}

func doctorAiderLocation() string {
	loc := filepath.Join(sources.Home(), ".aider.chat.history.md")
	if roots := os.Getenv("DEJA_AIDER_ROOTS"); roots != "" {
		loc += string(os.PathListSeparator) + roots
	}
	return loc
}

func doctorAntigravityLocation() string {
	if roots := sources.AntigravityRoots(); len(roots) > 0 {
		return strings.Join(roots, string(os.PathListSeparator))
	}
	return filepath.Join(sources.Home(), ".gemini", "antigravity*")
}

func doctorTools(w io.Writer) {
	fmt.Fprintln(w, "Tools:")
	status := "not found"
	if sources.SQLite3Available() {
		status = "found"
	}
	fmt.Fprintf(w, "  %-12s %s (needed for opencode and Cursor IDE stores)\n", "sqlite3", status)
	// git is not optional decoration: without it a hit loses the line saying
	// its files have changed since, project names lose worktree identity, and
	// the session-start hook loses the task signal. All three degrade in
	// silence, which is fine on a hit and not fine with nowhere to ask (#796).
	gitStatus := "not found"
	if _, err := exec.LookPath("git"); err == nil {
		gitStatus = "found"
	}
	fmt.Fprintf(w, "  %-12s %s (needed for changed-file notes, worktree names and the task signal)\n", "git", gitStatus)
}

// policyWithheldCounts reports, per activation, how many indexed sessions the
// policy in force keeps off that path, and how many are indexed in all.
func policyWithheldCounts(dir string) (map[string]int, int) {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return nil, 0
	}
	pol := policy.Load()
	out := map[string]int{}
	for _, activation := range []string{policy.ActivationSearch, policy.ActivationMCP, policy.ActivationAuto} {
		for _, m := range metas {
			if !pol.Allows(activation, m.Project) {
				out[activation]++
			}
		}
	}
	return out, len(metas)
}

// unmatchedImportGroups lists imported:<group> rules that no session in the
// index answers to.
func unmatchedImportGroups(dir string) []string {
	pol := policy.Load()
	present := map[string]bool{}
	metas, err := index.AllMeta(dir)
	if err != nil {
		return nil
	}
	for _, m := range metas {
		if o := policy.Origin(m.Project); strings.HasPrefix(o, "imported:") {
			present[o] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, rules := range pol.Activations {
		for origin, allowed := range rules {
			if allowed || !strings.HasPrefix(origin, "imported:") || present[origin] || seen[origin] {
				continue
			}
			seen[origin] = true
			out = append(out, origin)
		}
	}
	sort.Strings(out)
	return out
}

// doctorPolicy reports the one mechanism that separates local memory from
// imported. Load falls back to the permissive default on any error, so a
// malformed file changed nothing and said nothing, and a working one was
// invisible — leaving no place at all to find out what the rules are (#661).
func doctorPolicy(w io.Writer, dir string) {
	fmt.Fprintln(w, "Trust policy:")
	exists, unknown, err := policy.Diagnose()
	if !exists {
		// …unless the environment restricts it anyway. Deciding on the file's
		// absence said "every origin activates everywhere" while the auto path
		// was local-only, on the one screen someone opens to find out what is
		// allowed (#939).
		if pol := policy.Load(); pol.Describe(policy.ActivationAuto) != "local+imported" {
			fmt.Fprintf(w, "  %-12s %s\n", "default", noPolicyFileLine())
			withheld, total := policyWithheldCounts(dir)
			for _, activation := range []string{policy.ActivationSearch, policy.ActivationMCP, policy.ActivationAuto} {
				line := pol.Describe(activation)
				if n := withheld[activation]; n > 0 {
					line += fmt.Sprintf(" — withholds %d of %d indexed session%s", n, total, pluralS(total))
				}
				fmt.Fprintf(w, "  %-12s %s\n", activation, line)
			}
			fmt.Fprintf(w, "  %-12s DEJA_AUTORECALL_LOCAL_ONLY is set in this environment\n", "from env")
			return
		}
		fmt.Fprintf(w, "  %-12s %s — every origin activates everywhere\n", "default", noPolicyFileLine())
		// Except one thing, which is in force with or without a file and is
		// the reason a directory can be missing from recall (#2050).
		printIgnored(w, policy.Load(), dir)
		return
	}
	if err != nil {
		// The permissive default is what is actually in force, and that is the
		// part worth saying out loud: the file reads like a restriction.
		fmt.Fprintf(w, "  %-12s %s: %v\n", "unreadable", reportPath(policy.Path()), err)
		fmt.Fprintf(w, "  %-12s every origin activates everywhere until it parses\n", "in force")
		return
	}
	pol := policy.Load()
	withheld, total := policyWithheldCounts(dir)
	for _, activation := range []string{policy.ActivationSearch, policy.ActivationMCP, policy.ActivationAuto} {
		line := pol.Describe(activation)
		// The rule's text is not its effect. `search local-only` reads the
		// same whether it withholds nothing or the whole index, and doctor is
		// where someone checks that the rule does what they meant (#978).
		if n := withheld[activation]; n > 0 {
			line += fmt.Sprintf(" — withholds %d of %d indexed session%s", n, total, pluralS(total))
		}
		fmt.Fprintf(w, "  %-12s %s\n", activation, line)
	}
	printIgnored(w, pol, dir)
	for _, u := range unknown {
		fmt.Fprintf(w, "  %-12s %q is not an activation or origin deja consults — this rule does nothing\n", "ignored", u)
	}
	// An `imported:x` rule has the right shape and still matches nothing when
	// no session came from a project starting with x — the group is a project
	// prefix from the exporting machine, not a machine name, and a rule
	// written for a machine reads as in force forever (#955).
	for _, g := range unmatchedImportGroups(dir) {
		fmt.Fprintf(w, "  %-12s %q matches nothing in this index — the part after `imported:` is the first path component of the project on the machine it came from, not that machine's name\n", "inert", g)
	}
}

func doctorMCP(w io.Writer) {
	fmt.Fprintln(w, "MCP wiring:")
	for _, c := range doctorMCPConfigs() {
		status := "config missing"
		if doctorExists(c.path) {
			if c.wired(c.path) {
				status = "wired"
			} else {
				status = "not wired"
			}
		}
		// The Kimi Code plugin declares the same server, and stands down when
		// this file already has it. Reporting a machine that installed the
		// plugin as "not wired" sends someone to run an install that only
		// pushes the plugin aside.
		if status != "wired" && c.name == "kimi" && kimiPluginInstalled() {
			status = "plugin"
		}
		// And the Grok Build plugin, which declares the same server and stands
		// down when config.toml already has [mcp_servers.deja] (#1828).
		if status != "wired" && c.name == "grok" && grokPluginInstalled() {
			status = "plugin"
		}
		// Same for Codex: `codex plugin add deja-vu@deja-vu` brings the server
		// and the hooks with it.
		if status != "wired" && c.name == "codex" && codexPluginInstalled() {
			status = "plugin"
		}
		fmt.Fprintf(w, "  %-12s %-14s guidance %-11s %s\n", c.name, status, guidanceStatus(guidanceHarness(c.name)), reportPath(c.path))
		// One "wired" can be two registrations: a hand add under another name
		// — the project is called deja-vu, after all — plus the `deja` a later
		// install wrote beside it. Each session then starts the server twice
		// and carries the tool schema twice, and the boolean above cannot say
		// so (#2269).
		if status == "wired" && c.dupes != nil {
			if keys := c.dupes(c.path); len(keys) >= 2 {
				fmt.Fprintf(w, "  %-12s %s\n", "", doctorMCPDuplicateNote(keys))
			}
		}
		// "Wired" says the server is declared, not that it can start. A config
		// naming a binary that is gone — a restored backup, a hand edit, a
		// machine where deja moved and one file was fixed by hand — read as
		// healthy while no memory arrived (#2216).
		if status == "wired" {
			if missing := dejaCommandMissing(c.path); missing != "" {
				fmt.Fprintf(w, "  %-12s %s\n", "",
					"points at "+missing+", which is not there — `deja install "+c.name+"` rewrites it for this binary")
			} else if other := otherBinaryNote(c.path, c.name); other != "" {
				// The quieter half: the binary is there and is neither this one
				// nor the deja on PATH. Two harnesses on the machine this was
				// found on pointed at builds left behind by probe runs, and
				// both rows read `wired` (#3656).
				fmt.Fprintf(w, "  %-12s %s\n", "", other)
			}
		}
		// Zed's entry can defer to an extension instead of naming a binary,
		// and then "wired" is a fact about an id rather than about anything
		// runnable (#3660).
		if status == "wired" && c.name == "zed" {
			if note := zedUnreachableNote(c.path); note != "" {
				fmt.Fprintf(w, "  %-12s %s\n", "", note)
			}
		}
		if note := doctorWiringNote(c.name); note != "" && status == "wired" {
			fmt.Fprintf(w, "  %-12s %s\n", "", note)
		}
	}
}

// doctorMCPDuplicateNote is the line a duplicated setup never got to read: it
// names every key so the reader can decide which one is theirs. Which one to
// remove is not deja's call — one of them may be a hand add carrying fields
// deja does not know about (#2269).
func doctorMCPDuplicateNote(keys []string) string {
	quoted := make([]string, len(keys))
	for i, key := range keys {
		quoted[i] = "`" + strings.ReplaceAll(key, "`", "'") + "`"
	}
	names := strings.Join(quoted, ", ")
	if len(keys) == 2 {
		return fmt.Sprintf("two entries in this config run deja (%s) — every session starts the server twice", names)
	}
	return fmt.Sprintf("%d entries in this config run deja (%s) — every session starts the server %d times", len(keys), names, len(keys))
}

// dejaCommandMissing returns the deja binary a config names when that file is
// not there, and "" when the config names one that is, names none, or names it
// by bare name for the PATH to resolve. deja writes these commands itself, so
// reading them back is the difference between "declared" and "can start".
func dejaCommandMissing(path string) string {
	cmd := dejaCommandIn(path)
	// A bare name is the PATH's business, and this check would answer for a
	// lookup it does not do. A relative path or a `~` is worse than that: it
	// resolves against wherever the reader is standing, or against nothing,
	// so a stat here would report a working setup broken.
	if cmd == "" || !filepath.IsAbs(cmd) {
		return ""
	}
	if _, err := os.Stat(cmd); err == nil {
		return ""
	}
	return cmd
}

// dejaCommandIn finds the command a config runs deja with. JSON configs are
// parsed the way the wiring check parses them; the rest are read a line at a
// time, because a `command` key with a path whose name is deja means the same
// thing in TOML, YAML and JSONC, and adding a parser per format to answer one
// question is not worth the surface.
func dejaCommandIn(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var root map[string]any
	if json.Unmarshal(b, &root) == nil {
		for _, m := range mcpServerMaps(root) {
			for _, v := range m {
				if cmd := mcpEntryDejaCommand(v); cmd != "" {
					return cmd
				}
			}
			// Nothing recognised by name. An entry under the key deja writes
			// is deja's own whatever binary it happens to run — a build called
			// `deja-probe` or `deja-cont` is what a `go build -o` leaves, and
			// an entry naming one was invisible to every check here while the
			// row still said wired (#3659).
			if v, ok := m["deja"]; ok {
				if cmd := mcpEntryCommand(v); cmd != "" {
					return cmd
				}
			}
			// And the keys deja writes that are not just `deja`: Zed's entry is
			// `deja-context-server`, the id its extension owns. With a build
			// under another name in it — `deja-arm` from a probe run — neither
			// the name test nor the `deja` key matched, so the row said `wired`
			// about a server pointing into a scratch directory (#3683).
			for key, v := range m {
				if !strings.HasPrefix(strings.ToLower(key), "deja") {
					continue
				}
				if cmd := mcpEntryCommand(v); cmd != "" {
					return cmd
				}
			}
		}
		// Parsed, and nothing of deja's under any container this knows. The
		// text reader looks for the key by name instead, which is how a
		// harness with a container nobody has added here still gets an answer
		// — and it can only find what this walk missed, since both require a
		// deja-named key or binary (#3683).
		return dejaKeyedCommand(string(b))
	}
	// The attributed read first. The scan below takes any `command` in the file
	// whose value looks like deja, and goose keeps a `slash_commands` list at
	// the bottom of the same config with `- command: "deja"` in it — so the
	// scan answered with the name of a slash command and the MCP entry three
	// lines from the top, pointing at a build in a scratch directory, was never
	// looked at (#3662).
	if cmd := dejaKeyedCommand(string(b)); cmd != "" {
		return cmd
	}
	for _, m := range commandValue.FindAllStringSubmatch(string(b), -1) {
		// One group per quoting, so a quote inside a value cannot end it: a
		// path under C:\Users\O'Brien is a path, not a delimiter.
		//
		// Only the double-quoted form carries escapes. TOML's literal string
		// and YAML's single-quoted scalar keep their backslashes, which is how
		// a Windows path is normally written there, and reading an escaped one
		// raw named a path that exists nowhere — doctor then called a working
		// install broken (#2216).
		var value string
		switch {
		case m[1] != "":
			value = quotedPathUnescape.Replace(m[1])
		case m[2] != "":
			value = m[2]
		default:
			value = m[3]
		}
		if value = strings.TrimSpace(value); commandIsDeja(value) {
			return value
		}
	}
	return ""
}

// mcpServerMaps returns every map of servers a config might keep them in. The
// three top-level spellings, and `mcp.servers` one level deeper — which is
// OpenClaw's and ZCode's shape, and was read as an entry rather than as a map
// of them, so no check here could see the binary either of those two runs
// (#3663).
func mcpServerMaps(root map[string]any) []map[string]any {
	var out []map[string]any
	// context_servers is Zed's spelling of the same map (#3683).
	for _, key := range []string{"mcpServers", "mcp", "servers", "context_servers"} {
		m, _ := root[key].(map[string]any)
		if m == nil {
			continue
		}
		out = append(out, m)
		if nested, ok := m["servers"].(map[string]any); ok {
			out = append(out, nested)
		}
	}
	return out
}

// quotedPathUnescape undoes what a quoted string does to a Windows path. Only
// the two escapes a path can carry: anything else in a command line is not
// something this check should be interpreting.
var quotedPathUnescape = strings.NewReplacer(`\\`, `\`, `\"`, `"`)

// commandValue matches a `command` or `cmd` key and the value after it, in the
// three shapes these configs come in: JSON and JSONC quote the key, TOML uses
// `=`, YAML uses `:` and quotes nothing. A JSONC file that will not parse as
// JSON — zed's settings, which carry comments — reaches this too, so the whole
// text is scanned rather than a line at a time.
var commandValue = regexp.MustCompile(`"?(?:command|cmd)"?\s*[:=]\s*(?:"([^"\n]*)"|'([^'\n]*)'|([^",\n}]+))`)

// mcpEntryDejaCommand is mcpEntryRunsDeja's answer to "which one": the same
// walk, returning the command or argument that named deja.
func mcpEntryDejaCommand(v any) string {
	m, _ := v.(map[string]any)
	if m == nil {
		return ""
	}
	// The top level first: when an entry carries both, that is the one deja
	// wrote, and naming the nested one sent the reader after a path deja had
	// already replaced (#2716).
	switch c := m["command"].(type) {
	case string:
		if commandIsDeja(c) {
			return strings.TrimSpace(c)
		}
	case []any:
		// opencode writes the command as a list. The recogniser reads that
		// shape since #2713, and a check that says "wired" without being able
		// to name the binary cannot report one that is gone.
		for _, v := range c {
			if s, ok := v.(string); ok && commandIsDeja(s) {
				return strings.TrimSpace(s)
			}
		}
	}
	args, _ := m["args"].([]any)
	for _, a := range args {
		if s, ok := a.(string); ok && commandIsDeja(s) {
			return strings.TrimSpace(s)
		}
	}
	if t, ok := m["transport"].(map[string]any); ok {
		if cmd := mcpEntryDejaCommand(t); cmd != "" {
			return cmd
		}
	}
	return ""
}

// mcpEntryCommand is mcpEntryDejaCommand without the name test: the command an
// entry runs, whatever it is called. Only callers that already know the entry
// is deja's — because it sits under the key deja writes — may use it.
func mcpEntryCommand(v any) string {
	m, _ := v.(map[string]any)
	if m == nil {
		return ""
	}
	switch c := m["command"].(type) {
	case string:
		return strings.TrimSpace(c)
	case []any:
		for _, item := range c {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	if t, ok := m["transport"].(map[string]any); ok {
		return mcpEntryCommand(t)
	}
	return ""
}

// dejaKeyedCommand is the same claim for the formats read as text: the line that
// names deja, then the first `command` inside the block it opens. Hermes keeps
// its servers in YAML and named one `deja-cont`, which nothing here could see
// (#3659).
//
// Two kinds of anchor, because the block is shaped differently under each. A
// mapping key (`deja:`) has its fields indented below it. A TOML table header
// and a `serverName:` field both sit *beside* the command instead — TOML puts
// every key of a table at the header's own indent, and dsh names the server in
// a field of the row it belongs to — so for those the block is the run of
// siblings until the next table header or a line further out.
func dejaKeyedCommand(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		opens, beside := dejaBlockOpens(trimmed)
		if !opens {
			continue
		}
		indent := yamlIndentWidth(line)
		for _, next := range lines[i+1:] {
			nextTrimmed := strings.TrimSpace(next)
			if nextTrimmed == "" {
				continue
			}
			if outOfDejasBlock(next, nextTrimmed, indent, beside) {
				break
			}
			if m := commandValue.FindStringSubmatch(next); m != nil {
				for _, group := range m[1:] {
					if v := strings.TrimSpace(group); v != "" {
						return quotedPathUnescape.Replace(v)
					}
				}
			}
		}
	}
	return ""
}

// outOfDejasBlock reports whether a line has left the block the anchor opened.
//
// For an anchor whose fields sit beside it, only a new table header or a line
// further out ends the block: the old rule stopped at the first sibling that
// was not `command`, which for codex's `[mcp_servers.deja]` is `type = "stdio"`
// on the very next line — so a codex config whose command is not the first key
// read as though it named no binary at all, and the row said `wired` about a
// build in a scratch directory (#3668).
func outOfDejasBlock(line, trimmed string, indent int, beside bool) bool {
	if beside {
		if strings.HasPrefix(trimmed, "[") {
			return true
		}
		return yamlIndentWidth(line) < indent
	}
	if yamlIndentWidth(line) > indent || strings.HasPrefix(trimmed, "-") {
		return false
	}
	if strings.HasPrefix(trimmed, "[") || strings.Contains(trimmed, ":") || strings.Contains(trimmed, "=") {
		return !strings.HasPrefix(trimmed, "command")
	}
	return false
}

// dejaBlockOpens reports whether a line starts the block that belongs to deja,
// and whether that block's keys sit beside the anchor rather than under it. A
// mapping key indents its fields below; a TOML table header does not, and dsh
// has no server key at all — the name is a field inside a patch-list row,
// beside the command rather than above it.
func dejaBlockOpens(trimmed string) (opens, beside bool) {
	switch trimmed {
	case "deja:":
		return true, false
	case "[mcp_servers.deja]", "[mcp.servers.deja]",
		"serverName: deja", `serverName: "deja"`, "serverName: 'deja'":
		return true, true
	}
	// A quoted JSON key, for the files that do not parse as JSON: Zed's
	// settings carry comments, so the whole text is read a line at a time, and
	// its server key is `deja-context-server` rather than `deja` (#3683).
	if key, ok := jsonKeyOpening(trimmed); ok && strings.HasPrefix(strings.ToLower(key), "deja") {
		return true, false
	}
	return false, false
}

// jsonKeyOpening reads `"name": {` — the line that opens an object under a
// key — and returns the key.
func jsonKeyOpening(trimmed string) (string, bool) {
	if !strings.HasPrefix(trimmed, `"`) {
		return "", false
	}
	end := strings.Index(trimmed[1:], `"`)
	if end < 0 {
		return "", false
	}
	key := trimmed[1 : 1+end]
	rest := strings.TrimSpace(trimmed[1+end+1:])
	if !strings.HasPrefix(rest, ":") {
		return "", false
	}
	if strings.TrimSpace(strings.TrimPrefix(rest, ":")) != "{" {
		return "", false
	}
	return key, true
}

// doctorWiringNote adds what "wired" cannot promise for a given harness. Three
// CLIs share ~/.grok and read different files; one of them — @vibe-kit/grok-cli
// — has no user-level MCP config at all, so `grok mcp list` reports nothing no
// matter what an installer writes to the home directory. Saying "wired" without
// that caveat is how someone concludes deja is broken.
func doctorWiringNote(name string) string {
	switch name {
	case "grok":
		return "@vibe-kit/grok-cli reads MCP only from <cwd>/.grok/settings.json — run `grok mcp add deja -c deja -a mcp` in a project to wire that one"
	case "cherrystudio":
		// The only target whose "wired" is about a file the app has not read
		// yet: Cherry Studio keeps its servers in an app database with no
		// config file to write, so the install writes the JSON its importer
		// takes and the row must not be read as "the app has it".
		return "Cherry Studio has no config file to write — this is the JSON to import in Settings → MCP → Import from JSON"
	case "roo", "kilocode":
		// One settings file per VS Code-compatible host, and the path above is
		// whichever one exists. `deja install` writes every host that has the
		// extension; the row can only speak for one.
		return "one settings file per editor — `deja install " + name + "` writes every host that has the extension"
	}
	return ""
}

type doctorMCPConfig struct {
	name  string
	path  string
	wired func(string) bool
	// dupes lists every key in the config that runs deja, so doctorMCP can say
	// when "wired" is really two registrations (#2269). Nil where the format
	// has no counting probe — the boolean is then all doctor can promise.
	dupes func(string) []string
}

func doctorMCPConfigs() []doctorMCPConfig {
	return []doctorMCPConfig{
		{"claude-code", sources.ClaudeJSONPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"codex", filepath.Join(sources.CodexHome(), "config.toml"), doctorTOMLWired, doctorTOMLDejaKeys},
		{"opencode", doctorOpencodeConfigPath(), doctorJSONWired("mcp"), doctorJSONDejaKeys("mcp")},
		{"cursor", filepath.Join(sources.CursorCLIHome(), "mcp.json"), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"gemini", filepath.Join(sources.GeminiHome(), "settings.json"), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"antigravity", filepath.Join(antigravityConfigHome(), "mcp_config.json"), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"grok", filepath.Join(sources.GrokHome(), "config.toml"), doctorTOMLWired, doctorTOMLDejaKeys},
		{"qwen", filepath.Join(sources.QwenConfigDir(), "settings.json"), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"kimi", filepath.Join(sources.KimiConfigDir(), "mcp.json"), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"cline", sources.ClineMCPSettingsPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"pi", filepath.Join(sources.PiConfigDir(), "mcp.json"), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"omp", filepath.Join(sources.OmpConfigDir(), "mcp.json"), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"amp", sources.AmpSettingsFile(), doctorJSONWired(ampServersKey), doctorJSONDejaKeys(ampServersKey)},
		{"prime", primeSettingsPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"openclaw", filepath.Join(sources.OpenClawStateDir(), "openclaw.json"), doctorOpenClawWired, nil},
		{"copilot", guidancePath("copilot"), doctorFileWired, nil},
		{"vscode", doctorVSCodeMCPPath(), doctorJSONWired("servers"), doctorJSONDejaKeys("servers")},
		{"hermes", filepath.Join(sources.HermesHome(), "config.yaml"), doctorHermesWired, nil},
		{"goose", filepath.Join(gooseConfigDir(), "config.yaml"), doctorGooseWired, nil},
		{"continue", continueConfigPath(), doctorContinueWired, nil},
		{"crush", crushConfigPath(), doctorJSONWired("mcp"), doctorJSONDejaKeys("mcp")},
		{"zed", sources.ZedSettingsPath(), doctorZedWired, nil},
		// The nine targets `deja install` has always had and this table never
		// named. A row here is the only place a machine says whether the
		// server is declared and which binary it runs, so for these the report
		// said nothing at all — deepseek's entry on the author's machine still
		// pointed at a throwaway build with every other row repaired.
		{"deepseek", dshPatchPath(), doctorDSHWired, nil},
		{"roo", doctorFirstExisting(rooMCPSettingsPaths(), vsCodeExtensionMCPPath(sources.RooExtensionID)), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"kilocode", kilocodeDoctorPath(), doctorKilocodeWired, nil},
		{"kiro", kiroMCPSettingsPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"senpi", senpiMCPPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"kimchi", kimchiMCPPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"gjc", gjcMCPPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"zcode", zcodeConfigPath(), doctorZCodeWired, nil},
		{"commandcode", commandCodeMCPPath(), doctorJSONWired("mcpServers"), doctorJSONDejaKeys("mcpServers")},
		{"cherrystudio", cherryStudioImportPath(), doctorFileWired, nil},
	}
}

// dshPatchPath is the home-level patch layer installDeepSeek writes.
func dshPatchPath() string {
	return filepath.Join(sources.DSHHome(), "cordis.patch.yml")
}

// doctorDSHWired reads the layer for deja's own block rather than for a server
// key: dsh has no MCP config of its own, it has an ordered list of patch
// entries, and deja's is `mcp-deja`.
func doctorDSHWired(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "id: mcp-deja")
}

// doctorZCodeWired reads `mcp.servers`, one level deeper than the `mcpServers`
// the rest of this table uses.
func doctorZCodeWired(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var root struct {
		MCP struct {
			Servers map[string]any `json:"servers"`
		} `json:"mcp"`
	}
	if json.Unmarshal(b, &root) != nil {
		return false
	}
	for _, v := range root.MCP.Servers {
		if mcpEntryDejaCommand(v) != "" {
			return true
		}
	}
	_, ok := root.MCP.Servers["deja"]
	return ok
}

// doctorFirstExisting names the host a report should talk about when a harness
// has one settings file per VS Code-compatible editor: the one that is there.
// A row with no path at all says less than nothing, so fallback takes the first
// candidate, and then the one passed in — Kilo Code's reader lists only
// directories that exist, which on a machine without the extension is none.
func doctorFirstExisting(paths []string, fallback string) string {
	for _, p := range paths {
		if doctorExists(p) {
			return p
		}
	}
	if len(paths) > 0 {
		return paths[0]
	}
	return fallback
}

// kilocodeDoctorPath names the config that is actually there. Kilo has two: the
// extension's settings under a VS Code host, and the CLI's own
// `<config>/kilo/kilo.jsonc`, which is OpenCode-shaped because the CLI is
// OpenCode vendored. A machine with only the CLI had its row pointing at an
// editor path that does not exist (#3672).
func kilocodeDoctorPath() string {
	for _, p := range kilocodeMCPSettingsPaths() {
		if doctorExists(p) {
			return p
		}
	}
	if cli := kilocodeCLIConfigPath(); doctorExists(cli) {
		return cli
	}
	if paths := kilocodeMCPSettingsPaths(); len(paths) > 0 {
		return paths[0]
	}
	return vsCodeExtensionMCPPath(sources.KiloExtensionID)
}

// doctorKilocodeWired reads whichever of the two shapes the named file is in:
// `mcpServers` in the extension's settings, `mcp` in the CLI's config.
func doctorKilocodeWired(path string) bool {
	return doctorJSONWired("mcpServers")(path) || doctorJSONWired("mcp")(path)
}

// vsCodeExtensionMCPPath is where an extension would keep its MCP settings in
// plain VS Code, for a report on a machine that has neither the editor nor the
// extension: both readers list only directories that exist, so on such a
// machine the row had no path to print at all.
func vsCodeExtensionMCPPath(extension string) string {
	return filepath.Join(vsCodeDefaultUserDir(), "globalStorage", extension, "settings", "mcp_settings.json")
}

// doctorZedWired reads the same JSONC the installer writes, with the same
// scanner. The generic probe falls back to looking for "deja" anywhere in an
// unparseable file, which in a settings file full of comments answers a
// different question than "is the server wired".
//
// Either id counts as wired: the current one, and the one deja wrote before
// the two halves were given the same id. A machine that has not reinstalled
// since is still wired, and should not be told otherwise.
func doctorZedWired(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	text := string(b)
	return zedLocate(text, zedServerID) != nil || zedLocate(text, zedLegacyServerID) != nil
}

func doctorFileWired(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// doctorVSCodeMCPPath is the mcp.json in the first VS Code User folder present.
// VS Code Copilot Chat is wired through MCP alone — no hook, no plugin — and
// the config key is `servers`, not the common `mcpServers`.
func doctorVSCodeMCPPath() string {
	dirs := vsCodeUserDirs()
	if len(dirs) == 0 {
		return filepath.Join(vsCodeDefaultUserDir(), "mcp.json")
	}
	return filepath.Join(dirs[0], "mcp.json")
}

func doctorOpencodeConfigPath() string {
	dir := filepath.Join(opencodeConfigHome(), "opencode")
	path := filepath.Join(dir, "opencode.json")
	if !doctorExists(path) {
		if jsonc := filepath.Join(dir, "opencode.jsonc"); doctorExists(jsonc) {
			return jsonc
		}
	}
	return path
}

// doctorOpenClawWired checks openclaw.json's nested mcp.servers map.
func doctorOpenClawWired(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var root map[string]any
	if json.Unmarshal(b, &root) != nil {
		return strings.Contains(string(b), `"deja"`)
	}
	mcp, _ := root["mcp"].(map[string]any)
	servers, _ := mcp["servers"].(map[string]any)
	_, ok := servers["deja"]
	return ok
}

func doctorJSONWired(key string) func(string) bool {
	return func(path string) bool {
		b, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		var root map[string]any
		if json.Unmarshal(b, &root) != nil {
			// jsonc or otherwise unparseable — fall back to a substring probe.
			return strings.Contains(string(b), `"deja"`)
		}
		m, _ := root[key].(map[string]any)
		if _, ok := m["deja"]; ok {
			return true
		}
		// Someone who wired the server by hand may have called it anything —
		// "deja-vu" is the obvious other choice. What identifies it is the
		// command it runs, not the key it was filed under, and telling a
		// working setup it is not wired sends the debugging the wrong way.
		for _, v := range m {
			if mcpEntryRunsDeja(v) {
				return true
			}
		}
		return false
	}
}

// doctorJSONDejaKeys lists every key in the config's server block that runs
// deja: the literal `deja` first, then the hand-named rest in sorted order, so
// the duplicate line reads the same on every run (#2269). Nil on a file that
// will not parse — the substring fallback above can say "wired", but it
// cannot count.
func doctorJSONDejaKeys(key string) func(string) []string {
	return func(path string) []string {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var root map[string]any
		// The file may be JSONC — the install keeps a reader's comments and
		// trailing commas (#3243) — so it is read the way install reads it.
		if json.Unmarshal([]byte(jsoncToJSON(string(b))), &root) != nil {
			return nil
		}
		m, _ := root[key].(map[string]any)
		if m == nil {
			return nil
		}
		var out []string
		if _, ok := m["deja"]; ok {
			out = append(out, "deja")
		}
		var rest []string
		for name, entry := range m {
			if name != "deja" && mcpEntryRunsDeja(entry) {
				rest = append(rest, name)
			}
		}
		sort.Strings(rest)
		return append(out, rest...)
	}
}

// mcpEntryRunsDeja reports whether an MCP server entry launches deja, in any
// of the shapes clients accept: a bare command, a command plus args, a command
// written as a list, or a nested transport object.
//
// One function rather than two. install had grown its own answer, and they
// disagreed in both directions — a nested transport and a capitalised path
// were deja here and not there, a list command the other way round (#2713).
func mcpEntryRunsDeja(v any) bool {
	m, _ := v.(map[string]any)
	if m == nil {
		return false
	}
	return entryRunsDeja(m)
}

// commandIsDeja is the token test, under the name doctor's readers use. One
// implementation, because two answers to "is this the binary" disagreed on a
// quoted Windows path: the entry read as wired while nothing could name the
// command in it, so the missing-binary check had nothing to report (#2716).
func commandIsDeja(cmd string) bool {
	return isDejaBinaryToken(cmd)
}

func doctorTOMLWired(path string) bool {
	return len(doctorTOMLDejaKeys(path)) > 0
}

// doctorTOMLDejaKeys is the TOML side of the same count: every
// `[mcp_servers.X]` block that runs deja, the literal `deja` first (its header
// alone is enough — deja wrote it), then the hand-named rest sorted (#2269).
// Same reasoning as the JSON probe for the names: a hand-wired server under
// another name still runs deja. Attribution is per block rather than the old
// whole-file scan, because a `command` line elsewhere in this config — a hook,
// say — is not MCP wiring; and args count as well as command, since Windows
// wiring runs deja behind a `cmd /c` shim.
func doctorTOMLDejaKeys(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	found := map[string]bool{}
	current := ""
	for _, line := range strings.Split(string(b), "\n") {
		if key, ok := tomlMCPHeader(line); ok {
			current = key
			if key == "deja" {
				found[key] = true
			}
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			current = ""
			continue
		}
		if current == "" || found[current] {
			continue
		}
		key, value, ok := tomlLineKeyValue(line)
		if !ok || (key != "command" && key != "args") {
			continue
		}
		for _, value := range tomlStringValues(value) {
			if commandIsDeja(value) {
				found[current] = true
				break
			}
		}
	}
	var out []string
	if found["deja"] {
		out = append(out, "deja")
		delete(found, "deja")
	}
	rest := make([]string, 0, len(found))
	for key := range found {
		rest = append(rest, key)
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// indexFormatDirection is a variable so a test can put doctor in front of an
// index this build cannot read without shipping a manifest writer.
var indexFormatDirection = index.FormatDirection

// indexReadState is a variable for the same reason, and it is the one the row
// below asks: how old the index is and what this build may do with it are
// different questions (#3597).
var indexReadState = index.ReadStateOf

func doctorIndex(w io.Writer, idx doctorIndexReport, dir string) {
	fmt.Fprintln(w, "Index:")
	loc := idx.Path
	if loc == "" {
		loc = dir
	}
	fmt.Fprintf(w, "  location %s\n", reportPath(loc))
	// "Active" is what a reader checks this screen for, and it was false in the
	// one state they check it in: the pattern is written, the index is not
	// rebuilt, and the next search still serves the project they meant to hide.
	// The list applies at ingest, so it covers nothing already indexed until a
	// rebuild — the sentence `deja index` prints for the same reason (#1307,
	// #2664).
	if n := len(sources.ExclusionPatterns()); n > 0 && index.ExclusionsChanged(dir) {
		fmt.Fprintf(w, "  exclusions %d pattern%s, not applied to sessions already indexed — `deja index --rebuild`\n", n, pluralS(n))
	} else {
		fmt.Fprintf(w, "  exclusions %d active patterns\n", n)
	}
	// A precise non-claim: users deciding what to trust deserve to read the
	// boundary in the tool itself, not only in the security docs.
	fmt.Fprintln(w, "  security plaintext on disk — protected by file permissions only, no encryption or access control")
	if idx.State == "missing" || idx.State == "path-is-a-file" {
		// A build already running is not a missing index, and "run `deja
		// warmup`" tells the reader to start what is under way — doctor is
		// the command people run when memory looks absent, so this is the
		// worst place to describe it as absent (#873).
		if st := readWarmupStatus(dir); st != nil {
			fmt.Fprintf(w, "  status   building now (%s) — recall comes online when it finishes\n", st.progress())
			return
		}
		// A build requested moments ago has published no progress yet, and
		// "run `deja warmup`" tells the reader to start what is already
		// running — the first build after install is exactly that state
		// (#925).
		if warmupJustRequested(dir) {
			fmt.Fprintln(w, "  status   building now — started moments ago, recall comes online when it finishes")
			return
		}
		// A file where the directory belongs: a build refuses rather than
		// deleting it, so "run `deja warmup`" would send the reader to a
		// command that will not run either (#3610).
		if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
			fmt.Fprintf(w, "  status   not built — %s is a file, not a directory; move it aside, or point DEJA_INDEX_DIR at a directory\n", reportPath(dir))
			return
		}
		// An index whose disk was unplugged is not a missing index, and
		// "run `deja warmup`" points at a path that is not there. doctor is
		// what someone runs when memory looks broken (#931).
		if parent := filepath.Dir(dir); !dirExists(parent) {
			fmt.Fprintf(w, "  status   not reachable — %s is not there; the disk it lives on may have been unmounted\n", parent)
			return
		}
		// The index directory is there but cannot be read — a permissions
		// problem or a restricted mount, not a missing build. "run `deja
		// warmup`" would send the reader to a command that cannot read it
		// either, and the index may well be built behind the closed door
		// (#1116).
		if dirExists(dir) {
			if _, err := os.ReadDir(dir); os.IsPermission(err) {
				fmt.Fprintf(w, "  status   unreadable — %s cannot be read (permission denied); fix its permissions or point DEJA_INDEX_DIR somewhere readable\n", dir)
				return
			}
		}
		// "run `deja warmup`" on a location that cannot be written sends the
		// reader to a command that fails the same way. doctor is where someone
		// looks to learn why memory is absent, so it has to name the reason.
		if !indexDirWritable(dir) {
			fmt.Fprintf(w, "  status   not built — %s is not writable, so no build can run there; point DEJA_INDEX_DIR somewhere writable\n", filepath.Dir(dir))
			return
		}
		fmt.Fprintln(w, "  status   not built (run `deja warmup`)")
		return
	}
	updated := "unknown"
	if fi, err := os.Stat(filepath.Join(dir, "manifest.gob")); err == nil {
		updated = fi.ModTime().Format("2006-01-02 15:04")
	}
	fmt.Fprintf(w, "  status   built (size=%s, updated=%s)\n", humanBytes(pathSize(dir)), updated)
	// An index written by an older format is unreadable to this binary: the
	// hook paths refuse it and ask for a rebuild, which is why memory goes
	// quiet after an upgrade. doctor called that "up to date" — the one
	// command someone runs to find out why nothing is recalled (#877).
	switch indexReadState(dir) {
	case index.ReadStateUnreadable:
		fmt.Fprintln(w, "  format   written by an older deja — this build cannot read it; the next session rebuilds it, or run `deja index` now")
	case index.ReadStateWithheld:
		// It reads. What it holds is text written before deja knew how to
		// redact something, so it answers nothing until the re-read is done —
		// which is a different sentence from "cannot read", and the only one of
		// the three that stops recall.
		fmt.Fprintln(w, "  format   written before deja learned to mask something it now masks — it answers once the sources are re-read; `deja index` does it now")
	case index.ReadStateOlderRules:
		// And the common upgrade: the store reads, answers, and re-derives
		// behind the answer. Calling that unreadable told someone looking for
		// why nothing is recalled that their index was gone (#3597, #3562).
		fmt.Fprintln(w, "  format   written by an older deja — it still answers; the next session re-reads the sources, or run `deja index` now")
	case index.ReadStateNewer:
		// The binary was rolled back, not the index. Saying "older" here sent
		// that reader looking in the wrong direction (#890).
		fmt.Fprintln(w, "  format   written by a newer deja than this one — this build rebuilds it in its own format; upgrading again rebuilds it back")
	}
	// A store whose postings vanished or whose record log was truncated cannot
	// answer anything, and said "up to date" until #735. The next search
	// rebuilds it, which is worth saying too — the reader has not lost memory,
	// only this build of the index.
	if reason := indexDamageReason(dir); reason != "" {
		// What broke, not a summary of the four ways it can: a manifest that
		// will not decode is not missing records, and the sentence sent the
		// reader — and whoever reads the doctor output they paste into an
		// issue — after the wrong file (#2695).
		fmt.Fprintf(w, "  integrity damaged — %s; the next search rebuilds the index\n", reason)
		return
	}
	switch idx.State {
	case "stale":
		if idx.StaleStores == 1 {
			fmt.Fprintln(w, "  freshness 1 store changed since last build — run `deja index`")
		} else {
			fmt.Fprintf(w, "  freshness %d stores changed since last build — run `deja index`\n", idx.StaleStores)
		}
	case "stale-readonly":
		fmt.Fprintf(w, "  freshness %s changed since last build, and the index cannot be written — check the permissions on %s, or point DEJA_INDEX_DIR somewhere writable\n",
			doctorCount(idx.StaleStores, "store"), filepath.Dir(idx.Path))
	default:
		fmt.Fprintln(w, "  freshness up to date")
	}
	// A stamp the clock cannot account for puts a session at the top of every
	// surface ordered by date, and leaves it there. The first screen has said
	// so since #696 and the listing since #2105; this is where a reader looks
	// when a store reads wrong (#2106).
	if idx.SessionsAhead > 0 {
		// pluralThatThose fits "— that one is at the top of this list", the
		// sentence `deja last` and `deja log` print. Spliced here it read
		// "leads with that one is".
		lead := "it"
		if idx.SessionsAhead > 1 {
			lead = "one of them"
		}
		fmt.Fprintf(w, "  clock    %s stamped later than this machine's clock — `deja last` leads with %s\n",
			doctorCount(idx.SessionsAhead, "session"), lead)
	}
	// What the last sync did with this machine's own rules: records that were
	// dropped because they belong to sessions forgotten here are invisible
	// afterwards, and a peer who keeps sending them drops them every time
	// (#1016).
	if li, ok := readLastImport(dir); ok && (li.Forgot > 0 || li.Own > 0) {
		parts := []string{fmt.Sprintf("%d record%s in", li.Records, pluralS(li.Records))}
		if li.Forgot > 0 {
			parts = append(parts, fmt.Sprintf("%d left out as forgotten here", li.Forgot))
		}
		if li.Own > 0 {
			parts = append(parts, fmt.Sprintf("%d already here word for word", li.Own))
		}
		fmt.Fprintf(w, "  last sync %s (%s)\n", strings.Join(parts, ", "), li.At.Local().Format("2006-01-02 15:04"))
	}
	health := index.IngestHealth(dir)
	names := make([]string, 0, len(health))
	for h, e := range health {
		if e.MalformedLines > 0 || e.FailedFiles > 0 || e.ClippedMessages > 0 {
			names = append(names, h)
		}
	}
	sort.Strings(names)
	for _, h := range names {
		e := health[h]
		// "malformed" covered only unparseable lines; valid JSON deja cannot
		// use is skipped just as invisibly, and the reader needs the same
		// warning either way (#814).
		clipped := ""
		if e.ClippedMessages > 0 {
			// Named separately from the skipped lines: the session is here and
			// searchable, it is the tail of one message that is not, and a
			// search over that tail answers "no matches" (#1093).
			clipped = fmt.Sprintf(", %d message%s stored short of the transcript (over 64 KB)",
				e.ClippedMessages, pluralS(e.ClippedMessages))
		}
		fmt.Fprintf(w, "  ingest   %s: %d unusable line%s skipped, %d path%s unreadable%s — see `deja doctor --json`\n",
			h, e.MalformedLines, pluralS(e.MalformedLines), e.FailedFiles, pluralS(e.FailedFiles), clipped)
	}
	reportFutureDated(w, dir)
}

// reportFutureDated names the sessions stamped ahead of the clock.
//
// One note dated next year sits at the top of `deja last` and stays there. deja
// cannot tell a skewed clock from a deliberate date, so the ordering is left
// alone — but saying nothing leaves the reader with a store that looks wrong
// for no visible reason, and the usual causes are a typo'd year in a
// hand-edited note file or a millisecond stamp read as seconds (#2063).
func reportFutureDated(w io.Writer, dir string) {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return
	}
	// A minute of slack: clocks between machines disagree by seconds, and a
	// session synced from a peer a moment ago is not a finding.
	cutoff := time.Now().Add(time.Minute)
	newest, count := time.Time{}, 0
	var newestID string
	for _, meta := range metas {
		if !meta.Updated.After(cutoff) {
			continue
		}
		count++
		if meta.Updated.After(newest) {
			newest, newestID = meta.Updated, meta.Harness+":"+meta.ID
		}
	}
	if count == 0 {
		return
	}
	fmt.Fprintf(w, "  clock    %d session%s stamped in the future, newest %s (%s) — it sorts above real work in `deja last` until the date is corrected\n",
		count, pluralS(count), newest.Local().Format("2006-01-02"), safeForStatusline(newestID, 80))
}

// strandedUpdateStagings counts the staging files an interrupted update left
// beside this binary. The name is deja's own (`.deja-update-*`), so this
// recognises only its own litter (#1109).
func strandedUpdateStagings() (int, string) {
	exe, err := os.Executable()
	if err != nil {
		return 0, ""
	}
	dir := filepath.Dir(exe)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, ""
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), ".deja-update-") {
			n++
		}
	}
	return n, dir
}

func doctorVersion(w io.Writer, lookup doctorVersionLookup) {
	fmt.Fprintln(w, "Version:")
	fmt.Fprintf(w, "  current  %s\n", version)
	// An update killed between staging and rename strands a whole binary's
	// worth of bytes beside the real one, under a prefix deja itself wrote —
	// and every later run walked past it (#1109).
	if n, dir := strandedUpdateStagings(); n > 0 {
		fmt.Fprintf(w, "  %-8s %s left in %s by an interrupted update — safe to delete\n",
			"leftover", doctorCount(n, "staged file"), dir)
	}
	latest, ok := lookup()
	if !ok {
		fmt.Fprintln(w, "  latest   unable to check")
		return
	}
	fmt.Fprintf(w, "  latest   v%s\n", latest)
	current := normalizeUpdateVersion(version)
	if order, ok := compareUpdateVersions(current, latest); ok {
		switch {
		case order < 0:
			fmt.Fprintln(w, "  status   update available (run `deja update`)")
		case order == 0:
			fmt.Fprintln(w, "  status   up to date")
		default:
			fmt.Fprintln(w, "  status   ahead of latest release")
		}
		return
	}
	if current == "dev" || current == "" {
		fmt.Fprintln(w, "  status   dev build")
	}
}

// reportPath is how the human report names a file: control characters
// sanitised, and a home-prefixed path contracted to ~. The issue template asks
// a reporter to "run deja doctor and redact local paths before pasting", which
// is work this report can do for them — ~/.claude/projects is as actionable as
// the absolute form for the person who ran it (#2360). --json keeps the real
// path: a tool reading it may need one, and nobody pastes JSON by hand.
func reportPath(p string) string {
	if p == "" {
		return p
	}
	// Some rows carry several paths in one string — a store deja looks for in
	// two places, or a root list from the environment. Contracting the whole
	// string would only reach the first, which is how the cursor row came out
	// half in ~ and half in /Users/… .
	parts := strings.Split(p, string(os.PathListSeparator))
	for i, part := range parts {
		parts[i] = search.SafePath(underHome(part))
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

// underHome contracts a home-prefixed path to ~, and leaves everything else
// alone. The boundary check keeps /home/alicia out of alice's tilde.
func underHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(p, home) {
		return p
	}
	rest := strings.TrimPrefix(p, home)
	if rest == "" || rest[0] == '/' || rest[0] == '\\' {
		return "~" + rest
	}
	return p
}

func doctorCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func doctorExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// doctorAnyExists reports whether at least one of the paths is on disk.
func doctorAnyExists(paths []string) bool {
	for _, p := range paths {
		if doctorExists(p) {
			return true
		}
	}
	return false
}

func doctorFilePresent(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() > 0
}

// printIgnored says which directories deja does not recall from. A rule that
// silently drops history is indistinguishable from history that was never
// there, so it is printed whether it came from the policy file or from the
// built-in default (#2050).
//
// And what it costs, the way the activation rows say what they withhold: the
// rule's text is not its effect. A rule is matched against the project name
// and the transcript's own path, so the natural thing to write — the
// directory's absolute path — matches neither, and the row above reported it
// as in force while it hid nothing (#3584).
func printIgnored(w io.Writer, pol policy.Policy, dir string) {
	pats := pol.IgnorePatterns()
	if len(pats) == 0 {
		return
	}
	what := "default"
	if len(pol.Ignore) > 0 {
		what = "from the file"
	}
	fmt.Fprintf(w, "  %-12s %s (%s)\n", "not recalled", strings.Join(pats, ", "), what)
	// Only for a rule somebody wrote. The default is deja's own and a machine
	// that has never met an agent runtime is not being told about it.
	if len(pol.Ignore) == 0 {
		return
	}
	metas, err := index.AllMeta(dir)
	if err != nil || len(metas) == 0 {
		return
	}
	for _, pat := range pol.Ignore {
		one := policy.Policy{Ignore: []string{pat}}
		n := 0
		for _, m := range metas {
			if one.Ignored(m.Path, m.Project) {
				n++
			}
		}
		if n == 0 {
			fmt.Fprintf(w, "  %-12s %q matches no indexed session — a rule is matched against the project name and the transcript's path, not the directory you ran in\n", "", pat)
			continue
		}
		fmt.Fprintf(w, "  %-12s %q hides %d of %d indexed session%s\n", "", pat, n, len(metas), pluralS(len(metas)))
	}
}

// noPolicyFileLine says where the policy would be, or that there is nowhere
// for it: with no home directory doctor printed "no file at " and stopped
// (#2785), on the screen somebody opens to find out where the file goes.
func noPolicyFileLine() string {
	if p := policy.Path(); p != "" {
		return "no file at " + reportPath(p)
	}
	return "no policy file — deja cannot find a home directory to look in"
}
