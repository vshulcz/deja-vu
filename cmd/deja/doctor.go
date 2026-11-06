package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
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
		if sources.IsSubagentPath(p) || sources.IsPrimeChildPath(p) || sources.KimiSubagentRun(p) || sources.MuseSubagentFile(p) || sources.AntigravitySubagentFile(p) {
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
	all := false
	offline := os.Getenv("DEJA_OFFLINE") == "1"
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		case "--offline":
			offline = true
		case "--deep":
			deep = true
		case "--all":
			all = true
		default:
			return unknownFlag("doctor", arg, doctorFlags)
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
			return errors.New("doctor --deep cannot find your index — set HOME, or DEJA_INDEX_DIR to an absolute path")
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
	// A terminal gets the screen laid out for reading: a verdict first, agents
	// that are not on this machine folded into one line, columns aligned and
	// wrapped to the width. A pipe keeps every row as it always printed.
	if f, ok := w.(*os.File); ok && briefWanted(f) {
		var buf bytes.Buffer
		printDoctorText(&buf, report, deepReport, dir, all, offline)
		_, err := io.WriteString(w, doctorScreen(buf.String(), report, all, statColorOK(w), briefWidth()))
		if err != nil {
			return err
		}
		return deepDriftErr(deepReport)
	}
	printDoctorText(w, report, deepReport, dir, all, offline)
	return deepDriftErr(deepReport)
}

// printDoctorText is the report as every row prints it.
func printDoctorText(w io.Writer, report doctorReport, deepReport *index.DeepReport, dir string, all, offline bool) {
	doctorHarnessStores(w, dir, all)
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
	doctorOlderBinaries(w)
	doctorDoubleInjections(w, dir)
	fmt.Fprintln(w)
	doctorIndex(w, report.Index, dir)
	// Under the index rows: what the harnesses' own cleanup is about to take,
	// which the index will keep.
	doctorRetentionDue(w, time.Now())
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
	fmt.Fprintf(w, "  kept     %d transcript%s no longer on disk, still searchable — `deja resume <id> --write-back` puts one back, `deja forget --session <id>` drops one for good\n", len(r.Kept), pluralS(len(r.Kept)))
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
		fmt.Fprintf(w, "  %-12s %-11s %s\n", "claude-code", "missing", reportPath(st.path))
		return
	}
	if st.state == "unreadable" {
		fmt.Fprintf(w, "  %-12s %-11s %s\n", "claude-code", "unreadable", reportPath(st.path))
		return
	}
	fmt.Fprintf(w, "  %-12s %-11s %s\n", "claude-code", st.state, reportPath(st.path))
	if off := clientHooksOff("claude-code"); off != "" && st.state == "wired" {
		fmt.Fprintf(w, "  %-12s %s\n", "", off)
	}
	if len(st.missing) > 0 && len(st.missing) < st.want {
		// Named, because the difference is what the machine is missing out on:
		// a settings.json written by an older deja keeps working and quietly
		// lacks everything added since.
		fmt.Fprintf(w, "               %d of %d events wired — no %s; run `deja install claude-auto`\n",
			st.want-len(st.missing), st.want, strings.Join(st.missing, ", "))
	}
	if note := doctorHookRepeats(st.hooks, claudeHookWiring, "claude-auto"); note != "" {
		fmt.Fprintf(w, "  %-12s %s\n", "", note)
	}
	// Only when something here is actually wired: the note is about the binary
	// those entries name, and a file with no deja in it names none. Not when
	// that binary is the launcher, which the note below names (#4245).
	launcher := doctorLauncherNote(st.path, "claude-auto")
	if note := hookExeNote(st.path, "claude-auto"); note != "" && len(st.missing) < st.want &&
		(launcher == "" || !hookExeIsLauncher(st.path)) {
		fmt.Fprintf(w, "  %-12s %s\n", "", note)
	}
	// The entries name the launcher now, and the launcher is always there —
	// what can be gone is everything it resolves to (#3422).
	if note := launcher; note != "" {
		fmt.Fprintf(w, "  %-12s %s\n", "", note)
	} else if st.runNote != "" {
		fmt.Fprintf(w, "  %-12s %s\n", "", st.runNote)
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
	// The configs name the link when the repair wrote one (#4189), and a
	// link that still resolves is wiring that works, whatever became of the
	// binary it led to before.
	named := st.Exe
	if st.Written != "" {
		named = st.Written
	}
	if _, err := os.Stat(named); err == nil {
		return
	}
	fmt.Fprintf(w, "  %-12s %-11s configs name %s, which is not there — `deja install %s` rewrites them for this binary\n",
		"wiring", "stale", named, strings.Join(st.Targets, " "))
}

// doctorCodexHook reports the codex session-start hook state. Codex gates
// hooks behind its own trust store: hooks.json can be perfectly wired while
// codex keeps the hook disabled — memory then silently never arrives.
func doctorCodexHook(w io.Writer) {
	st := codexHookWiringState()
	hooksPath, status, missing, hooks := st.path, st.state, st.missing, st.hooks
	if st.absent || status == "missing" || status == "plugin" {
		// The plugin ships the same hooks under its own root, and codex trusts
		// those the same way. Nothing was installed here, and nothing is
		// missing either.
		if status == "plugin" {
			fmt.Fprintf(w, "  %-12s %-11s %s  (the Codex plugin carries the hooks; codex asks once to trust them)\n",
				"codex-hook", "plugin", reportPath(hooksPath))
			return
		}
		fmt.Fprintf(w, "  %-12s %-11s %s\n", "codex-hook", "missing", reportPath(hooksPath))
		return
	}
	if st.trustUnknown {
		fmt.Fprintf(w, "  %-12s %-11s %s  (cannot read %s, so whether codex trusts the hook is unknown)\n",
			"codex-hook", "wired", reportPath(hooksPath), reportPath(filepath.Join(sources.CodexHome(), "config.toml")))
		return
	}
	line := fmt.Sprintf("  %-12s %-11s %s", "codex-hook", status, reportPath(hooksPath))
	if len(missing) > 0 {
		line += fmt.Sprintf("\n               %d of %d events wired — no %s; run `deja install codex-auto`",
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
	if off := clientHooksOff("codex-hook"); off != "" && status == "wired" {
		line += fmt.Sprintf("\n  %-12s %s", "", off)
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
	for part := range strings.SplitSeq(filepath.Dir(rel), string(filepath.Separator)) {
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
			return nil //nolint:nilerr // unreadable entries are skipped
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
	return slices.Contains(strings.Split(filepath.ToSlash(rel), "/"), "extensions")
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
			//
			// A skip reason here is a count of files deja opened and refused,
			// not a format change: Windsurf trajectories that fail
			// authentication (#308).
			switch {
			case store.Skipped != "":
				fmt.Fprintf(w, "  warning      %s %s; those sessions are missing from recall\n", store.Name, store.Skipped)
			case store.Error != "":
				fmt.Fprintf(w, "  warning      %s store cannot be read — %s; please report it\n",
					store.Name, store.Error)
			default:
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
			// A sqlite3 that is installed and does not answer needs fixing, not
			// installing, and the reader needs to know which binary it is.
			if strings.Contains(store.Skipped, "sqlite3 at ") {
				fmt.Fprintf(w, "  warning      %s %s needs a working sqlite3 CLI: %s; fix or replace it, then run `deja index`\n", store.Name, what, store.Skipped)
				continue
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
	for part := range strings.SplitSeq(location, string(os.PathListSeparator)) {
		for root := range strings.SplitSeq(part, ", ") {
			if root = strings.TrimSpace(root); root != "" {
				roots = append(roots, root)
			}
		}
	}
	return roots
}

func oneStoreDiskGone(path string) bool {
	// A row that names a description rather than a path — roo's "VS Code
	// globalStorage ..." — has no disk to lose. Walked up from the working
	// directory, Cherry Studio's relative placeholder read as unplugged on
	// every machine without the app.
	if !filepath.IsAbs(path) {
		return false
	}
	// Two levels is not enough for every store: `~/.local/share/goose/sessions`
	// and `~/.cline/data/sessions` lose three on a machine that never installed
	// them. A home directory that is there means the disk is there.
	if home := sources.Home(); home != "" && strings.HasPrefix(path, home+string(os.PathSeparator)) && dirExists(home) {
		return false
	}
	dir := filepath.Dir(path)
	for range 2 {
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
		if m.Harness == "deja" && !strings.HasPrefix(m.Project, model.RepoOriginPrefix) && !strings.HasPrefix(m.ID, "deja-repo-") && !strings.HasPrefix(m.ID, model.PromotedNotePrefix) {
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

// onlyAbsentDetail matches a row detail that says nothing beyond "none here":
// "0 files", "0 CLI transcripts", "0 stores".
var onlyAbsentDetail = regexp.MustCompile(`^0 [a-zA-Z ]+$`)

// onlyAbsent reports whether a missing store's detail adds nothing to
// "missing". A detail that does, like aider's note that it writes into each
// project rather than one place, is advice for someone who uses that agent
// and keeps the row (#4625).
func onlyAbsent(detail string) bool {
	return detail == "" || onlyAbsentDetail.MatchString(detail)
}

// storeLabels names stores the way the rows below do: the registry calls
// deja's own notes "deja", and nothing a person reads does.
func storeLabels(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n == "deja" {
			n = "notes"
		}
		out = append(out, n)
	}
	return out
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
	total := 0
	for _, m := range metas {
		if sources.HarnessExcluded(m.Harness) {
			continue
		}
		total++
		for _, activation := range []string{policy.ActivationSearch, policy.ActivationMCP, policy.ActivationAuto} {
			if !pol.Allows(activation, m.Project) {
				out[activation]++
			}
		}
	}
	return out, total
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
		if sources.HarnessExcluded(m.Harness) {
			continue
		}
		if o := policy.Origin(m.Project); model.IsImportedProject(o) {
			present[o] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, rules := range pol.Activations {
		for origin, allowed := range rules {
			if allowed || !model.IsImportedProject(origin) || present[origin] || seen[origin] {
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
	// The alias file names sessions at ingest too, so an edit is in force for
	// new sessions only until a rebuild (#5373).
	if index.ProjectAliasesChanged(dir) {
		fmt.Fprintf(w, "  projects %s changed, sessions already indexed keep their old names — `deja index --rebuild`\n", reportPath(sources.ProjectAliasPath()))
	} else if n := sources.ProjectAliasCount(); n > 0 {
		fmt.Fprintf(w, "  projects %d tree%s named in %s\n", n, pluralS(n), reportPath(sources.ProjectAliasPath()))
	}
	// A precise non-claim: users deciding what to trust deserve to read the
	// boundary in the tool itself, not only in the security docs.
	fmt.Fprintln(w, "  security plaintext on disk — protected by file permissions only, no encryption or access control")
	if n := index.UnsharedFiles(loc); n > 0 {
		fmt.Fprintf(w, "  permissions %d file%s in shared index not group-writable — `chmod -R g+rwX %s`\n", n, pluralS(n), reportPath(loc))
	}
	if idx.State == "missing" || idx.State == "path-is-a-file" {
		// A build already running is not a missing index, and "run `deja
		// warmup`" tells the reader to start what is under way — doctor is
		// the command people run when memory looks absent, so this is the
		// worst place to describe it as absent (#873).
		if st := readWarmupStatus(dir); st != nil {
			fmt.Fprintf(w, "  status   building now (%s) — recall starts when it finishes\n", st.progress())
			return
		}
		// A build requested moments ago has published no progress yet, and
		// "run `deja warmup`" tells the reader to start what is already
		// running — the first build after install is exactly that state
		// (#925).
		if warmupJustRequested(dir) {
			fmt.Fprintln(w, "  status   building now — started moments ago, recall starts when it finishes")
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
			// A new home has no ~/.cache yet, and a build creates it: that
			// is an index not built, not a disk that went away.
			if freshHomePath(parent) {
				fmt.Fprintln(w, notBuiltLine())
				return
			}
			fmt.Fprintf(w, "  status   not reachable — %s is not there; the disk it lives on may have been unmounted\n", reportPath(parent))
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
		fmt.Fprintln(w, notBuiltLine())
		return
	}
	updated := "unknown"
	if fi, err := os.Stat(filepath.Join(dir, "manifest.gob")); err == nil {
		updated = fi.ModTime().Format("2006-01-02 15:04")
	}
	// When the index was last written and when deja last read this machine's
	// stores are different facts, and on a machine that syncs they drift apart:
	// an import rewrites the index without opening a transcript (#3747). Said
	// only when they differ by more than an hour, so an ordinary machine keeps
	// the one-line form.
	read := ""
	if at := index.ManifestSourcesReadAt(dir); at.IsZero() {
		read = ", stores never read"
	} else if fi, err := os.Stat(filepath.Join(dir, "manifest.gob")); err == nil && fi.ModTime().Sub(at) > time.Hour {
		read = ", stores read " + at.Local().Format("2006-01-02 15:04")
	}
	fmt.Fprintf(w, "  status   built (size=%s, updated=%s%s)\n", humanBytes(pathSize(dir)), updated, read)
	if index.Manual() {
		fmt.Fprintln(w, "  updates  automatic updates are off — only `deja index` writes the index")
	}
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
		fmt.Fprintln(w, "  format   written by a newer deja than this one — this build answers from it as it is and leaves it alone; `deja index --rebuild` rebuilds it for this one")
	case index.ReadStateCurrent:
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
			fmt.Fprintln(w, "  freshness 1 store changed since deja last read it — run `deja index`")
		} else {
			fmt.Fprintf(w, "  freshness %d stores changed since deja last read them — run `deja index`\n", idx.StaleStores)
		}
	case "stale-readonly":
		fmt.Fprintf(w, "  freshness %s changed since deja last read it, and the index cannot be written — check the permissions on %s, or point DEJA_INDEX_DIR somewhere writable\n",
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
		fmt.Fprintf(w, "  ingest   %s: %d unusable %s%s skipped, %d path%s unreadable%s — see `deja doctor --json`\n",
			h, e.MalformedLines, sources.SkippedNoun(h), pluralS(e.MalformedLines), e.FailedFiles, pluralS(e.FailedFiles), clipped)
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
	pol := policy.Load()
	// A minute of slack: clocks between machines disagree by seconds, and a
	// session synced from a peer a moment ago is not a finding.
	cutoff := time.Now().Add(time.Minute)
	newest, count := time.Time{}, 0
	var newestID string
	for _, meta := range metas {
		if !metaVisible(pol, policy.ActivationSearch, meta) || !meta.Updated.After(cutoff) {
			continue
		}
		count++
		if meta.Updated.After(newest) {
			newest, newestID = meta.Updated, meta.Key()
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
	// half in ~ and half in /Users/… . Joined back with ", ", the separator
	// the cursor row already used, so every multi-path row reads the same.
	parts := strings.Split(p, string(os.PathListSeparator))
	for i, part := range parts {
		parts[i] = search.SafePath(underHome(part))
	}
	return strings.Join(parts, ", ")
}

// freshHomePath says whether a missing path lies below a directory deja can
// write, so a build would simply create it. A mount point that went away
// leaves its path under a directory nothing here can write, or under an empty
// one outside the home: the mount point itself. A DEJA_INDEX_DIR under /tmp
// or a project directory was read as an unmounted disk before (#5057).
func freshHomePath(p string) bool {
	a := nearestExistingDir(p)
	if a == "" || !dirWritable(a) {
		return false
	}
	if underHome(a) != a {
		return true
	}
	entries, err := os.ReadDir(a)
	return err == nil && len(entries) > 0
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
	return slices.ContainsFunc(paths, doctorExists)
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
	if err != nil {
		return
	}
	var active []index.SessionMeta
	for _, m := range metas {
		if !sources.HarnessExcluded(m.Harness) {
			active = append(active, m)
		}
	}
	if len(active) == 0 {
		return
	}
	for _, pat := range pol.Ignore {
		one := policy.Policy{Ignore: []string{pat}}
		n := 0
		for _, m := range active {
			if one.Ignored(m.Path, m.Project) {
				n++
			}
		}
		if n == 0 {
			fmt.Fprintf(w, "  %-12s %q matches no indexed session — a rule is matched against the project name and the transcript's path, not the directory you ran in\n", "", pat)
			continue
		}
		fmt.Fprintf(w, "  %-12s %q hides %d of %d indexed session%s\n", "", pat, n, len(active), pluralS(len(active)))
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

// notBuiltLine is doctor's line for an index not built yet. With automatic
// indexing off warmup only reports, so it names the command that builds.
func notBuiltLine() string {
	if index.Manual() {
		return "  status   not built, and automatic updates are off (run `deja index`)"
	}
	return "  status   not built (run `deja warmup`)"
}
