package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
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
	doctorPeers(w, dir, time.Now())
	fmt.Fprintln(w)
	doctorHooks(w)
	// After doctorHooks, not inside it: that function returns early on a
	// machine without claude settings, which is exactly a machine whose other
	// harnesses may still be wired to a binary that moved.
	doctorWiringExe(w)
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
	if r.Clean() {
		fmt.Fprintln(w, "  status   index matches sources — no memory lost")
		return
	}
	for _, f := range r.Findings {
		fmt.Fprintf(w, "  drift    [%s] %s\n", f.Kind, f.Detail)
	}
	fmt.Fprintf(w, "  status   %s — run `deja index --rebuild`\n", doctorCount(len(r.Findings), "finding"))
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
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(w, "  %-12s missing      %s\n", "claude-code", path)
		return
	}
	var root map[string]any
	if json.Unmarshal(b, &root) != nil {
		fmt.Fprintf(w, "  %-12s unreadable   %s\n", "claude-code", path)
		return
	}
	hooks, _ := root["hooks"].(map[string]any)
	precompact := hookEventWired(hooks, "PreCompact", "hook-precompact")
	status := "missing"
	if precompact {
		status = "wired"
	}
	fmt.Fprintf(w, "  %-12s %-11s %s\n", "precompact", status, path)
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
	hooksPath := filepath.Join(sources.CodexHome(), "hooks.json")
	if _, err := os.Stat(hooksPath); err != nil {
		fmt.Fprintf(w, "  %-12s missing      %s\n", "codex-hook", hooksPath)
		return
	}
	cfgPath := filepath.Join(sources.CodexHome(), "config.toml")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		// Codex's trust store is its config. Without it there is nothing to
		// read, and guessing either way is worse than saying so.
		fmt.Fprintf(w, "  %-12s %-11s %s  (cannot read %s, so whether codex trusts the hook is unknown)\n",
			"codex-hook", "wired", hooksPath, cfgPath)
		return
	}
	// Untrusted until the config says otherwise. An entry that codex has never
	// been shown is the state where it silently runs nothing: measured on codex
	// 0.142.4, a home with hooks.json and no trust entry produced no hook at
	// all under `codex exec`, and the same run with
	// --dangerously-bypass-hook-trust ran it. That state used to read "wired".
	status := "untrusted"
	if section := codexHookTrustSection(string(cfg)); section != "" {
		off, on := strings.Index(section, "enabled = false"), strings.Index(section, "enabled = true")
		switch {
		case off >= 0 && (on == -1 || on > off):
			status = "disabled"
		case strings.Contains(section, "trusted_hash = \"sha256:"):
			status = "wired"
		}
	}
	line := fmt.Sprintf("  %-12s %-11s %s", "codex-hook", status, hooksPath)
	if status == "untrusted" {
		line += "  (codex has not been shown it — open codex once and approve it, or run /hooks; until then `codex exec` runs no hook at all)"
	}
	if status == "disabled" {
		line += "  (codex trusts but disabled it — re-enable in codex settings or hooks.state)"
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
	if r.Model == "" {
		fmt.Fprintf(w, "  endpoint   %s\n", r.State)
		return
	}
	fmt.Fprintf(w, "  endpoint   %s/model=%s/dim=%d\n", r.State, r.Model, r.Dim)
	fmt.Fprintf(w, "  sidecar    coverage=%.1f%%\n", r.Coverage)
}

// unplacedFiles counts transcripts under root that the harness's own filter
// did not pick up. A harness that changes its layout in a new version presents
// exactly this way: quietly fewer sessions, no error, and a directory size that
// still looks right (#701).
func unplacedFiles(root string, seen []string) int {
	have := make(map[string]bool, len(seen))
	for _, p := range seen {
		have[filepath.Clean(p)] = true
	}
	extra := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".jsonl", ".json":
		default:
			return nil
		}
		if !have[filepath.Clean(p)] {
			extra++
		}
		return nil
	})
	return extra
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
			fmt.Fprintf(w, "  warning      %s store cannot be read — its format may have changed; please report it\n", store.Name)
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
		case "needs-sqlite3":
			// Not a format change: the parser could not run at all. Saying so
			// points at installing one package instead of at a bug report
			// against the harness (#792).
			fmt.Fprintf(w, "  warning      %s store needs the sqlite3 CLI — install it, then run `deja index`\n", store.Name)
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
		if present {
			status = "found"
			// A directory deja cannot open loses its sessions from recall
			// without a word — the failure #802/#816 closed, but only in
			// `doctor --json`, which has said `denied` all along while this
			// row, the one people read, said `found` (#993).
			switch inspected[name] {
			case "denied", "unreadable", "parsed-zero", "needs-sqlite3":
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
			case n:
				detail += doctorCount(n, "indexed session") + " from elsewhere"
			default:
				detail += doctorCount(n-imported, "indexed session") + fmt.Sprintf(", %d more from elsewhere", imported)
			}
		}
		// A store path can come from the environment (DEJA_NOTES_FILE) or from
		// disk. On a fixed-width row a newline in it prints a line of its own
		// that reads as one of doctor's.
		line := fmt.Sprintf("  %-12s %-9s %s", name, status, search.SafeLine(path))
		if detail != "" {
			line += "  (" + detail + ")"
		}
		fmt.Fprintln(w, line)
	}

	// printFiles is printRow for the harnesses that answer with a file list.
	// The count comes from the same filter the parser uses, so a store whose
	// layout differs slightly loses those files from every number deja prints
	// — the one thing `doctor` exists to rule out (#701).
	printFiles := func(name, path string, present bool, seen []string) {
		detail := doctorCount(len(seen), "file")
		if extra := unplacedFiles(path, seen); extra > 0 {
			detail += fmt.Sprintf(", %d not recognised here", extra)
		}
		printRow(name, path, present, detail)
	}

	claudeRoot := sources.ClaudeRoot()
	printFiles("claude", claudeRoot, doctorExists(claudeRoot), sources.ClaudeFiles())

	codexRoot := sources.CodexRoot()
	printFiles("codex", codexRoot, doctorExists(codexRoot), sources.CodexFiles())

	ocDB := sources.OpencodeDB()
	printRow("opencode", ocDB, doctorFilePresent(ocDB), doctorSQLiteDetail(ocDB, sqlite))

	printRow("aider", doctorAiderLocation(), len(sources.AiderFiles()) > 0, doctorCount(len(sources.AiderFiles()), "file"))

	geminiRoot := sources.GeminiRoot()
	printFiles("gemini", geminiRoot, doctorExists(geminiRoot), sources.GeminiChatFiles())

	printRow("cursor", doctorCursorLocation(), doctorCursorPresent(), doctorCursorDetail(sqlite))

	printRow("antigravity", doctorAntigravityLocation(), len(sources.AntigravityRoots()) > 0, doctorCount(len(sources.AntigravityTranscripts()), "file"))

	grokRoot := sources.GrokRoot()
	printFiles("grok", grokRoot, doctorExists(grokRoot), sources.GrokSessionFiles())

	qwenRoot := filepath.Join(sources.QwenRoot(), "projects")
	printFiles("qwen", qwenRoot, doctorExists(qwenRoot), sources.QwenSessionFiles())

	kimiRoot := filepath.Join(sources.KimiRoot(), "sessions")
	printFiles("kimi", kimiRoot, doctorExists(kimiRoot), sources.KimiSessionFiles())

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
	printRow("cline", clineLoc, clineFiles > 0 || doctorExists(clineModern), doctorCount(clineFiles, "file"))

	rooFiles := len(sources.RooTaskFiles())
	rooLoc := "VS Code globalStorage rooveterinaryinc.roo-cline"
	if roots := sources.RooRoots(); len(roots) > 0 {
		rooLoc = strings.Join(roots, string(os.PathListSeparator))
	}
	printRow("roo", rooLoc, rooFiles > 0, doctorCount(rooFiles, "file"))

	piRoot := sources.PiRoot()
	printFiles("pi", piRoot, doctorExists(piRoot), sources.PiSessionFiles())
	openclawRoot := sources.OpenClawRoot()
	printRow("openclaw", openclawRoot, doctorExists(openclawRoot), doctorCount(len(sources.OpenClawSessionFiles()), "file"))
	copilotRoot := sources.CopilotRoot()
	printFiles("copilot", copilotRoot, doctorExists(copilotRoot), sources.CopilotSessionFiles())
	printRow("deja", sources.NotesFile(), doctorFilePresent(sources.NotesFile()), "notes")
	if n := noteBucketsRegrouped(dir); n > 0 {
		fmt.Fprintf(w, "  warning      %s of notes in the index %s not what this machine would build now — the zone changed, so the days regrouped; `deja index` renames them\n",
			doctorCount(n, "day"), verbIs(n))
	}
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
	return strings.Join([]string{sources.CursorUserRoot(), sources.CursorCLIRoot()}, ", ")
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
			fmt.Fprintf(w, "  %-12s no file at %s\n", "default", policy.Path())
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
		fmt.Fprintf(w, "  %-12s no file at %s — every origin activates everywhere\n", "default", policy.Path())
		return
	}
	if err != nil {
		// The permissive default is what is actually in force, and that is the
		// part worth saying out loud: the file reads like a restriction.
		fmt.Fprintf(w, "  %-12s %s: %v\n", "unreadable", policy.Path(), err)
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
		fmt.Fprintf(w, "  %-12s %-14s guidance %-11s %s\n", c.name, status, guidanceStatus(guidanceHarness(c.name)), c.path)
		if note := doctorWiringNote(c.name); note != "" && status == "wired" {
			fmt.Fprintf(w, "  %-12s %s\n", "", note)
		}
	}
}

// doctorWiringNote adds what "wired" cannot promise for a given harness. Three
// CLIs share ~/.grok and read different files; one of them — @vibe-kit/grok-cli
// — has no user-level MCP config at all, so `grok mcp list` reports nothing no
// matter what an installer writes to the home directory. Saying "wired" without
// that caveat is how someone concludes deja is broken.
func doctorWiringNote(name string) string {
	if name != "grok" {
		return ""
	}
	return "@vibe-kit/grok-cli reads MCP only from <cwd>/.grok/settings.json — run `grok mcp add deja -c deja -a mcp` in a project to wire that one"
}

type doctorMCPConfig struct {
	name  string
	path  string
	wired func(string) bool
}

func doctorMCPConfigs() []doctorMCPConfig {
	return []doctorMCPConfig{
		{"claude-code", sources.ClaudeJSONPath(), doctorJSONWired("mcpServers")},
		{"codex", filepath.Join(sources.CodexHome(), "config.toml"), doctorTOMLWired},
		{"opencode", doctorOpencodeConfigPath(), doctorJSONWired("mcp")},
		{"cursor", filepath.Join(sources.CursorCLIHome(), "mcp.json"), doctorJSONWired("mcpServers")},
		{"gemini", filepath.Join(sources.GeminiHome(), "settings.json"), doctorJSONWired("mcpServers")},
		{"antigravity", filepath.Join(antigravityConfigHome(), "mcp_config.json"), doctorJSONWired("mcpServers")},
		{"grok", filepath.Join(sources.GrokHome(), "config.toml"), doctorTOMLWired},
		{"qwen", filepath.Join(sources.QwenConfigDir(), "settings.json"), doctorJSONWired("mcpServers")},
		{"kimi", filepath.Join(sources.KimiConfigDir(), "mcp.json"), doctorJSONWired("mcpServers")},
		{"cline", sources.ClineMCPSettingsPath(), doctorJSONWired("mcpServers")},
		{"pi", filepath.Join(sources.PiConfigDir(), "mcp.json"), doctorJSONWired("mcpServers")},
		{"omp", filepath.Join(sources.OmpConfigDir(), "mcp.json"), doctorJSONWired("mcpServers")},
		{"openclaw", filepath.Join(sources.OpenClawStateDir(), "openclaw.json"), doctorOpenClawWired},
		{"copilot", guidancePath("copilot"), doctorFileWired},
		{"hermes", filepath.Join(sources.HermesHome(), "config.yaml"), doctorHermesWired},
		{"goose", filepath.Join(gooseConfigDir(), "config.yaml"), doctorGooseWired},
		{"zed", sources.ZedSettingsPath(), doctorZedWired},
	}
}

// doctorZedWired reads the same JSONC the installer writes, with the same
// scanner. The generic probe falls back to looking for "deja" anywhere in an
// unparseable file, which in a settings file full of comments answers a
// different question than "is the server wired".
func doctorZedWired(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	text := string(b)
	open := zedTopLevelOpen(text)
	if open < 0 {
		return false
	}
	block := zedFindKey(text, open+1, zedServerKey)
	if block == nil {
		return false
	}
	return zedFindKey(text, block.valueOpen+1, "deja") != nil
}

func doctorFileWired(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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

// mcpEntryRunsDeja reports whether an MCP server entry launches deja, in any
// of the shapes clients accept: a bare command, a command plus args, or a
// nested transport object.
func mcpEntryRunsDeja(v any) bool {
	m, _ := v.(map[string]any)
	if m == nil {
		return false
	}
	if t, ok := m["transport"].(map[string]any); ok {
		if mcpEntryRunsDeja(t) {
			return true
		}
	}
	cmd, _ := m["command"].(string)
	if commandIsDeja(cmd) {
		return true
	}
	// Windows and npx-style wiring puts the binary in args instead.
	args, _ := m["args"].([]any)
	for _, a := range args {
		if s, ok := a.(string); ok && commandIsDeja(s) {
			return true
		}
	}
	return false
}

func commandIsDeja(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	// A windows path read on a unix build and vice versa: filepath.Base only
	// knows the separator it was compiled for, and these configs travel.
	cmd = strings.ReplaceAll(cmd, `\`, "/")
	base := strings.ToLower(path.Base(cmd))
	base = strings.TrimSuffix(base, ".exe")
	return base == "deja"
}

func doctorTOMLWired(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if strings.Contains(string(b), "[mcp_servers.deja]") {
		return true
	}
	// Same reasoning as the JSON probe: a hand-wired server under another
	// name still runs deja. The TOML here is small and flat enough that
	// finding a command line naming the binary is enough.
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "command" {
			continue
		}
		if commandIsDeja(strings.Trim(strings.TrimSpace(value), `"`)) {
			return true
		}
	}
	return false
}

// indexFormatDirection is a variable so a test can put doctor in front of an
// index this build cannot read without shipping a manifest writer.
var indexFormatDirection = index.FormatDirection

func doctorIndex(w io.Writer, idx doctorComponent, dir string) {
	fmt.Fprintln(w, "Index:")
	loc := idx.Path
	if loc == "" {
		loc = dir
	}
	fmt.Fprintf(w, "  location %s\n", loc)
	fmt.Fprintf(w, "  exclusions %d active patterns\n", len(sources.ExclusionPatterns()))
	// A precise non-claim: users deciding what to trust deserve to read the
	// boundary in the tool itself, not only in the security docs.
	fmt.Fprintln(w, "  security plaintext on disk — protected by file permissions only, no encryption or access control")
	if idx.State == "missing" {
		// A build already running is not a missing index, and "run `deja
		// warmup`" tells the reader to start what is under way — doctor is
		// the command people run when memory looks absent, so this is the
		// worst place to describe it as absent (#873).
		if st := readWarmupStatus(dir); st != nil {
			fmt.Fprintf(w, "  status   building now (%s) — recall comes online in a few seconds\n", st.progress())
			return
		}
		// A build requested moments ago has published no progress yet, and
		// "run `deja warmup`" tells the reader to start what is already
		// running — the first build after install is exactly that state
		// (#925).
		if warmupJustRequested(dir) {
			fmt.Fprintln(w, "  status   building now — started moments ago, recall comes online in a few seconds")
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
	switch indexFormatDirection(dir) {
	case -1:
		fmt.Fprintln(w, "  format   written by an older deja — this build cannot read it; the next session rebuilds it, or run `deja index` now")
	case 1:
		// The binary was rolled back, not the index. Saying "older" here sent
		// that reader looking in the wrong direction (#890).
		fmt.Fprintln(w, "  format   written by a newer deja than this one — this build rebuilds it in its own format; upgrading again rebuilds it back")
	}
	// A store whose postings vanished or whose record log was truncated cannot
	// answer anything, and said "up to date" until #735. The next search
	// rebuilds it, which is worth saying too — the reader has not lost memory,
	// only this build of the index.
	if index.Damaged(dir) {
		fmt.Fprintln(w, "  integrity damaged — records or postings are missing; the next search rebuilds the index")
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

func doctorFilePresent(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() > 0
}
