package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// doctorStoreRows prints the rows of doctor's store table and keeps what the
// rows share: the inspection, the index counts and the folded names.
type doctorStoreRows struct {
	w   io.Writer
	all bool

	indexed, fromElsewhere, sharedRows, keptRows, neverRead map[string]int

	inspected         map[string]string
	unchecked, partly map[string]bool

	// Crush and OpenClaw print a row per database under one name, and the
	// counts below are the harness's, not the row's: repeated on every row, an
	// empty crush.db claimed another project's sessions (#4379). The first row
	// of a harness carries them.
	counted map[string]bool
	folded  []string
}

// row prints one store row, or folds it into the closing line.
func (r *doctorStoreRows) row(name, path string, present bool, detail string) {
	// A store DEJA_STORES silences has no row at all. The line above says
	// which stores are being read; a row saying "missing" about one of the
	// others would be answering a question nobody asked.
	if sources.StoreSilenced(name) {
		return
	}
	// A store the reader excluded says so whether or not it is on disk:
	// "missing" would read as deja not finding it, and "found" as deja
	// about to read it. Neither is true (#3499).
	if r.inspected[name] == "excluded" {
		fmt.Fprintf(r.w, "  %-12s %-9s %s\n", name, "excluded",
			"not read — `harness:"+name+"` is in "+reportPath(sources.ExcludePath()))
		return
	}
	status, detail := r.status(name, path, present, detail)
	detail = r.countsDetail(name, detail)
	// A store path can come from the environment (DEJA_NOTES_FILE) or from
	// disk. On a fixed-width row a newline in it prints a line of its own
	// that reads as one of doctor's.
	if !r.all && status == "missing" && onlyAbsent(detail) {
		r.folded = append(r.folded, name)
		return
	}
	line := fmt.Sprintf("  %-12s %-9s %s", name, status, reportPath(path))
	if detail != "" {
		line += "  (" + detail + ")"
	}
	fmt.Fprintln(r.w, line)
}

// status is the row's state word, with what the inspection adds to detail.
func (r *doctorStoreRows) status(name, path string, present bool, detail string) (string, string) {
	status := "missing"
	if present {
		status = "found"
		// A directory deja cannot open loses its sessions from recall
		// without a word — the failure #802/#816 closed, but only in
		// `doctor --json`, which has said `denied` all along while this
		// row, the one people read, said `found` (#993).
		switch r.inspected[name] {
		case "denied", "unreadable", "parsed-zero", "needs-sqlite3", "needs-zstd":
			status = r.inspected[name]
			if status == "denied" {
				if detail != "" {
					detail += ", "
				}
				// The warning block below names the path and what to do.
				// Whole or in part: the row folded both into "cannot be
				// read" while the warning under it and the search above
				// said the store still answers (#1034, #816).
				if r.partly[name] {
					detail += "partly unreadable"
				} else {
					detail += "cannot be read"
				}
			}
		}
		// A walk that stopped at its budget looked at part of the store,
		// and `found` on its own claims the whole of it (#1025).
		if r.unchecked[name] {
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
	return status, detail
}

// countsDetail adds the index's session counts to the first row of a harness.
func (r *doctorStoreRows) countsDetail(name, detail string) string {
	// Also when the store is missing: a machine whose history arrived by
	// `sync import` has no files at all, and doctor said nothing about the
	// sessions it does hold — the only surface that names them was stats
	// (#892).
	first := !r.counted[name]
	r.counted[name] = true
	if n, ok := r.indexed[name]; ok && first {
		if detail != "" {
			detail += ", "
		}
		// Local and imported counted apart: on a store with both, "3
		// files, 8 indexed sessions" reads as a miscount, and the reader
		// who learned from #861 that files-against-sessions shows
		// collapsing has no way to read the other direction (#894).
		imported := r.fromElsewhere[name]
		switch imported {
		case 0:
			detail += doctorCount(n, "indexed session")
			// The gap between files and sessions has two causes, and they
			// read the same: a file that failed to parse, or two files
			// sharing an id. The manifest knows which (#1101).
			if sh := r.sharedRows[name]; sh > 0 {
				detail += fmt.Sprintf(", %d of them shared by two transcripts", sh)
			}
			// And the other way the numbers disagree: a session whose
			// transcript the client cleaned up, kept on purpose (#2970).
			if k := r.keptRows[name]; k == 1 {
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
	// The gap in the other direction: transcripts the index has never
	// read. Outside the block above on purpose — a store with no indexed
	// session at all has no entry there, and that is exactly the store
	// this is about (#3747).
	if u := r.neverRead[name]; u > 0 && first {
		if detail != "" {
			detail += ", "
		}
		detail += doctorCount(u, "transcript") + " never read — `deja index`"
	}
	return detail
}

// filesSkippingIn is row for the harnesses that answer with a file list.
// The count comes from the same filter the parser uses, so a store whose
// layout differs slightly loses those files from every number deja prints
// — the one thing `doctor` exists to rule out (#701).
// This form is for a harness that has more than one transcript root and
// declines some of its own files by a rule.
// Files named in beside are the store's own bookkeeping, as for
// filesBeside below. Kimi's and Qwen's sub-agent logs were left out
// whatever DEJA_INCLUDE_SUBAGENTS said, so their rows did not name it
// (#4473, #4475); the switch takes them now and every row names it (#4483).
func (r *doctorStoreRows) filesSkippingIn(name, loc string, roots []string, present bool, seen []string, skipped func(string) bool, beside ...string) {
	detail := doctorCount(len(seen), "file")
	placed := append(append([]string{}, seen...), beside...)
	unread := 0
	byRule := 0
	for _, root := range roots {
		u, b := unplacedFiles(root, placed, skipped)
		unread += u
		byRule += b
	}
	if byRule > 0 {
		// The variable named the way it was read as the cause of the
		// skip — "skipped (DEJA_INCLUDE_SUBAGENTS=1)" — so somebody who
		// wanted those transcripts indexed set the thing the line said
		// was already set. It is the remedy, and it reads as one now.
		detail += fmt.Sprintf(", %d subagent transcripts skipped — set DEJA_INCLUDE_SUBAGENTS=1 to index them", byRule)
	} else if short := countSubagentFiles(seen); short > 0 && !sources.SubagentsWhole() {
		// Read, but not in full: what a reader needs to know is which half
		// of those files is searchable, and how to get the rest (#3009).
		detail += fmt.Sprintf(", %d subagent transcripts read as task, answer and what they changed — set DEJA_INCLUDE_SUBAGENTS=1 for the whole run", short)
	}
	if unread > 0 {
		detail += fmt.Sprintf(", %d not recognised here", unread)
	}
	r.row(name, loc, present, detail)
}

// filesSkipping is its one-root form.
func (r *doctorStoreRows) filesSkipping(name, path string, present bool, seen []string, skipped func(string) bool, beside ...string) {
	r.filesSkippingIn(name, path, []string{path}, present, seen, skipped, beside...)
}

// files is filesSkipping with no rule.
func (r *doctorStoreRows) files(name, path string, present bool, seen []string) {
	r.filesSkipping(name, path, present, seen, nil)
}

// filesBesideIn is files for a harness whose store keeps files
// beside the transcripts that are not transcripts — Continue's
// sessions.json (the list, which deja reads), Copilot's vscode.metadata.json
// (the IDE's bookkeeping, #3303), Kimi's per-session state.json (the title
// and working directory, #3309). Counted with the files, it made `doctor`
// disagree with `deja sources` by one; counted as unread, it made every
// store report a file deja could not read (#3297). So it is neither: the
// count is the transcripts, and the note leaves the list alone.
// The In form is for a row whose printed location is
// not one directory: cline names its modern store and its legacy roots on
// the same line, and that string cannot be walked (#3360).
func (r *doctorStoreRows) filesBesideIn(name, loc string, walks []string, dotDirsAreTheStore, present bool, seen []string, beside ...string) {
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
	r.row(name, loc, present, detail)
}

// filesBeside is filesBesideIn for one directory.
func (r *doctorStoreRows) filesBeside(name, path string, present bool, seen []string, beside ...string) {
	r.filesBesideIn(name, path, []string{path}, false, present, seen, beside...)
}

// laterStores prints the rows from senpi on, in the table's order.
func (r *doctorStoreRows) laterStores(sqlite bool) {
	// Four stores whose formats deja already had: two pi descendants and two
	// flat-transcript clients (#3647).
	senpiRoot := sources.SenpiRoot()
	r.files("senpi", senpiRoot, doctorExists(senpiRoot), sources.SenpiSessionFiles())
	kimchiRoot := sources.KimchiRoot()
	// Its sub-agent runs are skipped on purpose and named as such (#4401).
	r.filesSkipping("kimchi", kimchiRoot, doctorExists(kimchiRoot), sources.KimchiSessionFiles(), sources.KimchiSubagentFile)
	commandRoot := sources.CommandCodeRoot()
	// The checkpoint stream beside each transcript is named rather than left to
	// the unread count: it is not a conversation, and "1 not recognised here"
	// on a store deja reads correctly is the line that sends someone looking
	// for drift that is not there.
	r.filesBeside("commandcode", commandRoot, doctorExists(commandRoot),
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
	// And the snapshots an older ZCode left, which are read too (#4432).
	zcodeLegacy := sources.ZCodeLegacyFiles()
	if len(zcodeLegacy) > 0 {
		zcodeLoc += string(os.PathListSeparator) + sources.ZCodeLegacyRoot()
		zcodeDetail += ", " + doctorCount(len(zcodeLegacy), "legacy snapshot")
	}
	if zcodeHasDB {
		zcodeDetail += ", CLI store present" + doctorDBPrereqNote(sqlite)
	}
	r.row("zcode", zcodeLoc, doctorExists(zcodeRoot) || zcodeHasDB || len(zcodeLegacy) > 0, zcodeDetail)
	// gjc's scope file sits in every project directory and its sub-agent
	// passes are skipped on purpose; counted as unread, the row reported one
	// file per project that deja had no reason to read (#4393).
	gjcRoot := sources.GjcRoot()
	r.filesSkipping("gjc", gjcRoot, doctorExists(gjcRoot), sources.GjcSessionFiles(),
		sources.GjcSubagentPath, sources.GjcScopeFiles()...)

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
	// `kiro-cli chat --no-interactive` writes only to its database (#4300).
	kiroLoc := kiroRoot
	kiroDB := sources.KiroDB()
	kiroHasDB := doctorExists(kiroDB)
	if kiroHasDB {
		kiroLoc += string(os.PathListSeparator) + kiroDB
		kiroDetail += ", CLI store present" + doctorDBPrereqNote(sqlite)
	}
	r.row("kiro", kiroLoc, kiroCLI+kiroIDE > 0 || kiroHasDB, kiroDetail)

	// Cherry Studio writes Claude Code transcripts under its own app data, so
	// the row names the roots it found rather than the app directory (#3644).
	cherryFiles := len(sources.CherryStudioSessionFiles())
	cherryLoc := sources.CherryStudioDefaultRoot()
	if roots := sources.CherryStudioAllRoots(); len(roots) > 0 {
		cherryLoc = strings.Join(roots, string(os.PathListSeparator))
	}
	r.row("cherrystudio", cherryLoc, cherryFiles > 0, doctorCount(cherryFiles, "file"))

	continueDir := filepath.Join(sources.ContinueRoot(), "sessions")
	r.filesBeside("continue", continueDir, doctorExists(continueDir), sources.ContinueSessionFiles(),
		filepath.Join(continueDir, "sessions.json"))

	piRoot := sources.PiRoot()
	r.files("pi", piRoot, doctorExists(piRoot), sources.PiSessionFiles())
	openclawRoot := sources.OpenClawRoot()
	r.filesBeside("openclaw", openclawRoot, doctorExists(openclawRoot), sources.OpenClawSessionFiles(), sources.OpenClawSidecarFiles()...)
	for _, db := range sources.OpenClawAgentDBs() {
		r.row("openclaw", db, doctorFilePresent(db), doctorSQLiteDetail(db, sqlite))
	}
	copilotRoot := sources.CopilotRoot()
	r.filesBeside("copilot", copilotRoot, doctorExists(copilotRoot), sources.CopilotSessionFiles(), sources.CopilotSidecarFiles()...)
	chatFiles := len(sources.CopilotChatSessionFiles())
	chatLoc := "VS Code Copilot Chat"
	if roots := sources.CopilotChatRoots(); len(roots) > 0 {
		chatLoc = strings.Join(roots, string(os.PathListSeparator))
	}
	r.row("copilot-chat", chatLoc, chatFiles > 0, doctorCount(chatFiles, "file"))
	// omp, deepseek and zed are read by the indexer and were named by nothing
	// here — a user of one of them had no row to check when their sessions did
	// not come back (#1738). The registry decides what is indexed, and a test
	// now holds these rows to it.
	ompRoot := sources.OmpRoot()
	r.files("omp", ompRoot, doctorExists(ompRoot), sources.OmpSessionFiles())
	primeRoot := sources.PrimeRoot()
	r.files("prime", primeRoot, doctorExists(primeRoot), sources.PrimeSessionFiles())
	ampRoot := sources.AmpRoot()
	if sources.AmpThreadsServerSide() {
		r.row("amp", ampRoot, doctorExists(ampRoot), doctorCount(0, "file")+", "+ampServerSideNote)
	} else {
		r.files("amp", ampRoot, doctorExists(ampRoot), sources.AmpThreadFiles())
	}
	// CodeWhale keeps its transcripts beside its own bookkeeping — the offline
	// queue, the ownership ledger, the checkpoint slot — so those are placed
	// rather than counted as transcripts deja could not read.
	cwRoots := sources.CodeWhaleRoots()
	r.filesBesideIn("codewhale", sources.CodeWhaleRoot(), cwRoots, false, doctorExists(sources.CodeWhaleRoot()),
		sources.CodeWhaleSessionFiles(), sources.CodeWhaleSidecarFiles()...)
	// Junie keeps an index and two renderings beside each session's log.
	r.filesBeside("junie", sources.JunieRoot(), doctorExists(sources.JunieRoot()),
		sources.JunieSessionFiles(), sources.JunieSidecarFiles()...)
	// AI Assistant's chats are inside each IDE's workspace files, most of
	// which hold none; the count is the workspace files.
	r.row("jetbrains", sources.JetBrainsRoot(), doctorExists(sources.JetBrainsRoot()),
		doctorCount(len(sources.JetBrainsSessionFiles()), "workspace file"))
	// Every Cascade trajectory is sealed, and one that fails authentication
	// is out of recall; the row counts those apart (#308).
	wsDetail := doctorCount(len(sources.WindsurfSessionFiles()), "file")
	if reason := sources.SkipReason("windsurf"); reason != "" {
		wsDetail += ", " + reason
	}
	r.row("windsurf", sources.WindsurfRoot(), doctorExists(sources.WindsurfRoot()), wsDetail)
	// Reasonix keeps a dozen sidecars beside each transcript — metadata, event
	// logs, locks, subagent logs — so those are placed, not counted as unread.
	rxRoot := sources.ReasonixRoot()
	r.filesBesideIn("reasonix", rxRoot, sources.ReasonixSessionDirsAll(), false, doctorExists(rxRoot),
		sources.ReasonixSessionFiles(), sources.ReasonixSidecarFiles()...)
	// Muse keeps cron.db, goals.db and spilled tool output beside each log, and
	// its subagents' logs one level down, left out with DEJA_INCLUDE_SUBAGENTS=0.
	museRoots := sources.MuseRoots()
	r.filesSkippingIn("muse", strings.Join(museRoots, string(os.PathListSeparator)), museRoots, doctorAnyExists(museRoots),
		sources.MuseSessionFiles(), sources.MuseSubagentFile, sources.MuseSidecarFiles()...)
	dshRoot := sources.DeepSeekRoot()
	r.files("deepseek", dshRoot, doctorExists(dshRoot), sources.DeepSeekSessionFiles())
	// MiniMax Code keeps one transcript per session under its own data
	// directory, sharded by the day the conversation started. The runtime's
	// own bookkeeping sits beside each transcript and is named rather than
	// counted as a file deja could not read.
	mcodeRoot := sources.McodeRoot()
	r.filesBeside("mcode", mcodeRoot, doctorExists(mcodeRoot), sources.McodeSessionFiles(), sources.McodeSidecarFiles()...)
	zedDB := sources.ZedDB()
	r.row("zed", zedDB, doctorFilePresent(zedDB), doctorSQLiteDetail(zedDB, sqlite))
	// One store for every local session, as Zed has — but the file the row
	// names may be either of the two Devin keeps side by side.
	devinDB := sources.DevinSessionsDB()
	devinLoc := devinDB
	devinDetail := doctorSQLiteDetail(devinDB, sqlite)
	if legacy := sources.DevinLegacySessionsDB(); !doctorFilePresent(devinDB) && doctorFilePresent(legacy) {
		devinLoc = legacy
		devinDetail = doctorSQLiteDetail(legacy, sqlite)
	}
	r.row("devin", devinLoc, doctorFilePresent(devinDB) || doctorFilePresent(sources.DevinLegacySessionsDB()), devinDetail)
	// One store per project rather than one per machine, so the registry is
	// what makes them findable at all. Name it even when it lists nothing:
	// "the registry is empty" is the answer for someone whose crush sessions
	// did not come back.
	crushRegistry := filepath.Join(sources.CrushDataHome(), "projects.json")
	r.row("crush", crushRegistry, doctorFilePresent(crushRegistry), doctorCount(len(sources.CrushDBs()), "store"))
	for _, db := range sources.CrushDBs() {
		r.row("crush", db, doctorFilePresent(db), doctorSQLiteDetail(db, sqlite))
	}
}
