package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// doctorHarnesses lists every store, the form `deja doctor --all` prints.
func doctorHarnesses(w io.Writer, dir string) {
	doctorHarnessStores(w, dir, true)
}

// doctorHarnessStores prints the store rows. Without all, a store that is
// simply absent folds into one closing line: doctor lists every agent deja
// can read, so on a machine with two of them the rows that matter hid among
// thirty-odd "missing" ones (#4625). Only a row with nothing to say beyond
// "not here" folds; anything found, unreadable, excluded, unplugged, holding
// indexed sessions or carrying advice keeps its row.
func doctorHarnessStores(w io.Writer, dir string, all bool) {
	fmt.Fprintln(w, "Harness stores:")
	// Say the selection out loud. Without this line a narrowed run looks like
	// a machine that has thirty-four stores missing, and the variable is set by
	// whoever started the process — not necessarily by whoever is reading.
	if only, ok := sources.StoresSelected(); ok {
		fmt.Fprintf(w, "  reading only %s (%s)\n", strings.Join(storeLabels(only), ", "), sources.StoresEnv)
	}
	sqlite := sources.SQLite3Available()

	// Files are what deja found; sessions are what they became. The two differ
	// whenever ids collide — a resumed transcript, a copied one, a harness that
	// reuses a thread id — and the difference is invisible in a row that only
	// counts files (#861).
	indexed := index.HarnessSessionCounts(dir)
	fromElsewhere := index.ImportedSessionCounts(dir)
	sharedRows := index.HarnessSharedCounts(dir)
	keptRows := index.HarnessKeptCounts(dir)
	// Transcripts the index has no state for at all. Audited on a real store,
	// ten of them had never been read — five written eight weeks earlier — and
	// nothing on any surface said so, because a store's session count reads as
	// "nothing written yet" rather than "files never opened" (#3747).
	neverRead := index.HarnessUnreadCounts(dir)

	// The same inspection the JSON form reports, so one command does not give
	// two answers about one store: `found` here and `unreadable` there (#999).
	rows := &doctorStoreRows{
		w: w, all: all,
		indexed: indexed, fromElsewhere: fromElsewhere, sharedRows: sharedRows, keptRows: keptRows, neverRead: neverRead,
		inspected: map[string]string{}, unchecked: map[string]bool{}, partly: map[string]bool{},
		counted: map[string]bool{},
	}
	for _, check := range doctorStoreChecks() {
		store, _ := inspectDoctorStore(check)
		rows.inspected[check.name] = store.State
		rows.unchecked[check.name] = store.Unchecked
		rows.partly[check.name] = store.Partial
	}

	claudeRoots := sources.ClaudeRoots()
	claudeLocation := strings.Join(claudeRoots, string(os.PathListSeparator))
	claudePresent := false
	for _, root := range claudeRoots {
		claudePresent = claudePresent || doctorExists(root)
	}
	rows.filesSkippingIn("claude", claudeLocation, claudeRoots, claudePresent, sources.ClaudeFiles(),
		func(p string) bool { return !sources.ClaudeFileWanted(p) }, sources.ClaudeSidecarFiles()...)

	codexRoots := sources.CodexRoots()
	codexLocation := strings.Join(codexRoots, string(os.PathListSeparator))
	codexPresent := false
	for _, root := range codexRoots {
		codexPresent = codexPresent || doctorExists(root)
	}
	rows.filesBesideIn("codex", codexLocation, codexRoots, codexPresent, sources.CodexFiles(), sources.CodexSidecarFiles()...)
	// TRAE CLI keeps its state database, config and skills beside the codex-rs
	// store, so only the session directories are walked for unread files.
	rows.filesBesideIn("trae", sources.TraeRoot(), sources.TraeSessionDirs(), doctorExists(sources.TraeRoot()), sources.TraeFiles())

	ocDB := sources.OpencodeDB()
	rows.row("opencode", ocDB, doctorFilePresent(ocDB), doctorSQLiteDetail(ocDB, sqlite))

	rows.row("aider", doctorAiderLocation(), len(sources.AiderFiles()) > 0, doctorAiderDetail())

	// The row names the store and counts what is under `tmp`, where the chats
	// are: Antigravity keeps its own store in a sibling directory of the same
	// root and has its own row, so walking the whole of ~/.gemini reported its
	// files — 55 of 77 on a real machine — as chats gemini failed to read
	// (#3397). The settings and the extensions beside them are not chats
	// either.
	geminiRoot := sources.GeminiRoot()
	rows.filesBesideIn("gemini", geminiRoot, []string{filepath.Join(geminiRoot, "tmp")},
		doctorExists(geminiRoot), sources.GeminiChatFiles(), sources.GeminiSidecarFiles()...)

	rows.row("cursor", doctorCursorLocation(), doctorCursorPresent(), doctorCursorDetail(sqlite))

	// Present means a store that is there, not a path deja was told about:
	// AntigravityRoots hands back DEJA_ANTIGRAVITY_ROOT as given, so a machine
	// with the variable set to a directory that does not exist — a typo, a
	// removed install — read as `found` with nothing in it, which is the shape
	// every other row reports as `missing`.
	agyRoots := sources.AntigravityRoots()
	rows.filesSkippingIn("antigravity", doctorAntigravityLocation(), agyRoots, doctorAnyExists(agyRoots),
		sources.AntigravityTranscripts(), sources.AntigravitySubagentFile, sources.AntigravitySidecarFiles()...)

	// The store root also holds Grok's settings, credentials and caches, which
	// are not transcripts and never will be; the sessions directory is what the
	// count is about (#3319).
	grokRoot := filepath.Join(sources.GrokRoot(), "sessions")
	rows.filesBeside("grok", grokRoot, doctorExists(grokRoot), sources.GrokSessionFiles(), sources.GrokSidecarFiles()...)

	qwenRoot := filepath.Join(sources.QwenRoot(), "projects")
	// Beside, not unread: `<id>.runtime.json`, `meta.json` and
	// `extract-cursor.json` are qwen's own bookkeeping (#3676).
	rows.filesSkippingIn("qwen", qwenRoot, []string{qwenRoot}, doctorExists(qwenRoot),
		sources.QwenSessionFiles(), sources.QwenSubagentFile, sources.QwenSidecarFiles()...)

	// CodeBuddy's tree is Claude Code's: sub-agents under the session that
	// spawned them, a meta file per session and a memory/ directory beside.
	cbRoots := sources.CodeBuddyRoots()
	rows.filesSkippingIn("codebuddy", strings.Join(cbRoots, string(os.PathListSeparator)), cbRoots, doctorAnyExists(cbRoots),
		sources.CodeBuddySessionFiles(), sources.CodeBuddySubagentFile, sources.CodeBuddySidecarFiles()...)

	kimiRoot := filepath.Join(sources.KimiRoot(), "sessions")
	rows.filesSkippingIn("kimi", kimiRoot, []string{kimiRoot}, doctorExists(kimiRoot),
		sources.KimiSessionFiles(), sources.KimiSubagentFile, sources.KimiSidecarFiles()...)

	gooseRoot := filepath.Join(sources.GooseRoot(), "sessions")
	rows.row("goose", gooseRoot, doctorExists(gooseRoot) || doctorFilePresent(sources.GooseDB()), doctorGooseDetail(sqlite))

	// Hermes 0.17 keeps one state.db at the root and no profiles/ directory,
	// which HermesDBs reads first; the row looked only at profiles/ and said
	// missing about an indexed store (#4244).
	// The directory alone is not a store: it holds hermes' config, and
	// `deja install hermes-auto` writes there on a machine where it never ran.
	hermesRoot := sources.HermesProfilesRoot()
	rootStore := doctorFilePresent(filepath.Join(sources.HermesHome(), "state.db"))
	hermesPresent := rootStore || doctorExists(hermesRoot)
	if rootStore || !doctorExists(hermesRoot) {
		hermesRoot = sources.HermesHome()
	}
	rows.row("hermes", hermesRoot, hermesPresent, doctorCount(len(sources.HermesSessionFiles()), "store"))

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
	rows.filesBesideIn("cline", clineLoc, clineWalks, clineFiles > 0 || doctorExists(clineModern),
		sources.ClineSessionFiles(), sources.ClineSidecarFiles()...)

	rooFiles := len(sources.RooTaskFiles())
	rooLoc := "VS Code globalStorage rooveterinaryinc.roo-cline"
	if roots := sources.RooRoots(); len(roots) > 0 {
		rooLoc = strings.Join(roots, string(os.PathListSeparator))
	}
	rows.row("roo", rooLoc, rooFiles > 0, doctorCount(rooFiles, "file"))

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
	rows.row("kilocode", kiloLoc, kiloTasks > 0 || kiloHasDB, kiloDetail)

	rows.laterStores(sqlite)
	rows.row("deja", sources.NotesFile(), doctorFilePresent(sources.NotesFile()), "notes")
	if len(rows.folded) > 0 {
		fmt.Fprintf(w, "  %s not found on this machine — `deja doctor --all` lists them\n",
			doctorCount(len(rows.folded), "more store"))
	}
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
	return " but the sqlite3 CLI is " + sqliteGap() + " — those sessions are unavailable"
}

// sqliteGap words what is wrong with sqlite3 for a row that has no room for the
// whole problem; the Tools section names the binary and what it did.
func sqliteGap() string {
	if sources.SQLite3Broken() {
		return "not working"
	}
	return "missing"
}

func doctorSQLiteDetail(db string, sqlite bool) string {
	fi, err := os.Stat(db)
	if err != nil || fi.Size() == 0 {
		return ""
	}
	d := humanBytes(fi.Size())
	if !sqlite {
		d += ", sqlite3 CLI " + sqliteGap() + " — sessions unavailable"
	}
	return d
}

func doctorGooseDetail(sqlite bool) string {
	parts := []string{doctorCount(len(sources.GooseJSONLFiles()), "legacy file")}
	if fi, err := os.Stat(sources.GooseDB()); err == nil && fi.Size() > 0 {
		seg := humanBytes(fi.Size()) + " SQLite"
		if !sqlite {
			seg += ", sqlite3 CLI " + sqliteGap() + " — modern sessions unavailable"
		}
		parts = append(parts, seg)
	}
	return strings.Join(parts, ", ")
}

func doctorCursorDetail(sqlite bool) string {
	transcripts := sources.CursorTranscripts()
	cli := doctorCount(len(transcripts), "CLI transcript")
	// By the reader's own test, which matches the directory in any case.
	short := 0
	for _, p := range transcripts {
		if sources.CursorSubagentParent(p) != "" {
			short++
		}
	}
	if short > 0 && !sources.SubagentsWhole() {
		cli += fmt.Sprintf(", %d subagent transcripts read as task, answer and what they changed — set DEJA_INCLUDE_SUBAGENTS=1 for the whole run", short)
	}
	parts := []string{cli}
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
			seg += ", sqlite3 CLI " + sqliteGap() + " — IDE sessions unavailable"
		}
		parts = append(parts, seg)
	}
	// Cursor CLI writes a second, content-addressed store per chat and nothing
	// reads it. While a transcript sits beside every chat that has one, that
	// costs nothing and this stays quiet; if a release stops writing
	// transcripts, this is the only thing that would say so (#3772).
	if n := sources.CursorChatsWithoutTranscript(); n > 0 {
		parts = append(parts, doctorCount(n, "CLI chat")+" with no transcript — contents not readable yet")
	}
	return strings.Join(parts, ", ")
}

func doctorCursorPresent() bool {
	// Chats count as present even though nothing parses them: a machine whose
	// Cursor writes only the new store would otherwise drop the row, and the
	// row is where the warning above lives.
	return len(sources.CursorTranscripts()) > 0 || len(sources.CursorDBs()) > 0 ||
		len(sources.CursorChatStores()) > 0
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

// doctorAiderDetail says where the history is when none was found: aider
// writes it at the git root of each project, and $HOME holds one only for a
// launch from $HOME (#4326).
func doctorAiderDetail() string {
	n := len(sources.AiderFiles())
	if n > 0 {
		return doctorCount(n, "file")
	}
	return doctorCount(0, "file") + " — " + aiderNoHistoryHint
}

const aiderNoHistoryHint = "aider writes .aider.chat.history.md in each project; start it there as `deja aider` or list project dirs in DEJA_AIDER_ROOTS"

func doctorAntigravityLocation() string {
	if roots := sources.AntigravityRoots(); len(roots) > 0 {
		return strings.Join(roots, string(os.PathListSeparator))
	}
	return filepath.Join(sources.Home(), ".gemini", "antigravity*")
}

func doctorTools(w io.Writer) {
	fmt.Fprintln(w, "Tools:")
	// Found and working are not the same thing: a wrapper that drops its
	// arguments or a stub that prints nothing reads every store as empty, and
	// this line said "found" beside a row of parsed-zero stores.
	status := "not found"
	problem := sources.SQLite3Problem()
	switch {
	case problem == "":
		status = "found"
	case sources.SQLite3Broken():
		status = "found but not working"
	}
	fmt.Fprintf(w, "  %-12s %s (needed for opencode and Cursor IDE stores)\n", "sqlite3", status)
	if sources.SQLite3Broken() {
		fmt.Fprintf(w, "  warning      %s; every database store reads as empty until it is fixed\n", problem)
	}
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
