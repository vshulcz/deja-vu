package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// presentFiles is one path if it exists, nothing otherwise: a store deja names
// but does not have should weigh nothing rather than the directory around it.
func presentFiles(paths ...string) []string {
	var out []string
	for _, p := range paths {
		if p == "" {
			continue
		}
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

func cursorReadFiles() []string {
	return append(sources.CursorTranscripts(), sources.CursorDBs()...)
}

// grokReadFiles is both of Grok's stores: the session files Grok Build writes,
// and the SQLite store the maintained CLI writes instead. The row listed only
// the first, so the size column read 0 B on a machine whose whole history is in
// the database (#3225).
func grokReadFiles() []string {
	files := sources.GrokSessionFiles()
	if db := sources.GrokDB(); db != "" {
		files = append(files, db)
	}
	return files
}

// filesSize sums what the parser would open, counting each path once: cursor
// and hermes list a file under more than one discovery rule.
func filesSize(paths []string) int64 {
	seen := map[string]bool{}
	var total int64
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			total += fi.Size()
		}
	}
	return total
}

// commonDir is the deepest directory holding every path, which is the answer to
// "where is my history" — the configured root can be a parent of it, or, when a
// harness keeps its store somewhere else entirely, not contain it at all.
func commonDir(paths []string) string {
	dir := ""
	for _, p := range paths {
		if p == "" {
			continue
		}
		d := filepath.Dir(p)
		if dir == "" {
			dir = d
			continue
		}
		for dir != d && !strings.HasPrefix(d+string(filepath.Separator), dir+string(filepath.Separator)) {
			parent := filepath.Dir(dir)
			if parent == dir {
				return dir
			}
			dir = parent
		}
	}
	return dir
}

// projectExcludePatterns counts project rules from the file and environment.
// Count them directly: excluded stores can also come from the environment,
// and duplicate harness rules do not each add a store (#5401).
func projectExcludePatterns() int {
	n := 0
	for _, pattern := range sources.ExclusionPatterns() {
		if !strings.HasPrefix(pattern, "harness:") {
			n++
		}
	}
	return n
}

// An environment exclusion must not be attributed to the exclude file.
// Prefer the environment when both settings exclude the same store.
func harnessExclusionSource(name string) string {
	for excluded := range strings.SplitSeq(os.Getenv("DEJA_EXCLUDE_HARNESSES"), ",") {
		if strings.EqualFold(strings.TrimSpace(excluded), name) {
			return "DEJA_EXCLUDE_HARNESSES"
		}
	}
	return sources.ExcludePath()
}

// harnessExclusionSources names where a set of store exclusions come from:
// the exclude file, DEJA_EXCLUDE_HARNESSES, or both.
func harnessExclusionSources(names map[string]bool) string {
	env, file := false, false
	for name := range names {
		if harnessExclusionSource(name) == "DEJA_EXCLUDE_HARNESSES" {
			env = true
		} else {
			file = true
		}
	}
	switch {
	case env && file:
		return reportPath(sources.ExcludePath()) + " and DEJA_EXCLUDE_HARNESSES"
	case env:
		return "DEJA_EXCLUDE_HARNESSES"
	}
	return reportPath(sources.ExcludePath())
}

func harnessExclusionReason(name string) string {
	setting := harnessExclusionSource(name)
	if setting == "DEJA_EXCLUDE_HARNESSES" {
		return "excluded — " + setting
	}
	return fmt.Sprintf("excluded — `harness:%s` is in %s", name, setting)
}

func printSources(dir string) {
	// A terminal gets a table; a pipe gets the tab-separated rows scripts
	// read, exactly as before.
	if briefWanted(os.Stdout) {
		var buf bytes.Buffer
		printSourcesTo(&buf, dir)
		fmt.Print(sourcesScreen(buf.String(), statColorOK(os.Stdout), briefWidth()))
		return
	}
	printSourcesTo(os.Stdout, dir)
}

// rootsOrDefault names the roots deja found, or the default one when none
// exists yet, so a row never has an empty location: someone who installs the
// harness later can see where its history should appear (#5638).
func rootsOrDefault(roots []string, def string) string {
	if len(roots) == 0 {
		return def
	}
	return strings.Join(roots, string(os.PathListSeparator))
}

func printSourcesTo(w io.Writer, dir string) {
	redactions := map[string]int{}
	if red, err := index.Redactions(dir); err == nil {
		redactions = red.Files
	}
	// Transcripts a store holds that the index has never read. doctor has said
	// so since #3747, but `deja sources` is the command people run first, and
	// its session count reads as "nothing written yet" rather than "files never
	// opened" — which is exactly how ten unread transcripts, five of them eight
	// weeks old, went unnoticed on a real store (#3752). Nil before the first
	// index, and then there is nothing to say.
	neverRead := index.HarnessUnreadCounts(dir)
	unreadNote := func(name string) string {
		if u := neverRead[name]; u > 0 {
			return "\t(" + doctorCount(u, "transcript") + " never read — `deja index`)"
		}
		return ""
	}
	antigravityRoots := sources.AntigravityRoots()
	antigravityLocation := strings.Join(antigravityRoots, string(os.PathListSeparator))
	if antigravityLocation == "" {
		antigravityLocation = filepath.Join(sources.Home(), ".gemini", "antigravity*")
	}
	// files names what the parser opens. The screen answers "where is my history
	// and how much of it is there", and roots are where deja looks rather than
	// what it reads: a codex root reported 108 MB of plugins, caches and sqlite
	// WALs around 1.3 MB of transcripts, and hermes named a directory holding
	// one crash log while the store it reads sat elsewhere (#654).
	items := []struct {
		name, location string
		roots          []string
		files          func() []string
		load           func() []model.Session
	}{
		{"claude", strings.Join(sources.ClaudeRoots(), string(os.PathListSeparator)), sources.ClaudeRoots(), sources.ClaudeFiles, sources.LoadClaude},
		{"codex", strings.Join(sources.CodexRoots(), string(os.PathListSeparator)), sources.CodexRoots(), sources.CodexFiles, sources.LoadCodex},
		{"trae", sources.TraeRoot(), []string{sources.TraeRoot()}, sources.TraeFiles, sources.LoadTrae},
		{"gemini", sources.GeminiRoot(), []string{filepath.Join(sources.GeminiRoot(), "tmp")}, sources.GeminiChatFiles, sources.LoadGemini},
		{"cursor", strings.Join([]string{sources.CursorUserRoot(), sources.CursorCLIRoot()}, string(os.PathListSeparator)), []string{sources.CursorUserRoot(), sources.CursorCLIRoot()}, cursorReadFiles, sources.LoadCursor},
		{"antigravity", antigravityLocation, antigravityRoots, sources.AntigravityTranscripts, sources.LoadAntigravity},
		// Both halves, the way the registry's grok entry loads them: the
		// maintained CLI writes one SQLite store beside the config and no
		// session files at all, so a machine on that build read as zero
		// sessions here while doctor and search both found them (#3225).
		{"grok", sources.GrokRoot(), []string{sources.GrokRoot()}, grokReadFiles, func() []model.Session {
			return append(sources.LoadGrok(), sources.LoadGrokDB()...)
		}},
		{"qwen", filepath.Join(sources.QwenRoot(), "projects"), []string{filepath.Join(sources.QwenRoot(), "projects")}, sources.QwenSessionFiles, sources.LoadQwen},
		{"codebuddy", strings.Join(sources.CodeBuddyRoots(), string(os.PathListSeparator)), sources.CodeBuddyRoots(), sources.CodeBuddySessionFiles, sources.LoadCodeBuddy},
		{"kimi", filepath.Join(sources.KimiRoot(), "sessions"), []string{filepath.Join(sources.KimiRoot(), "sessions")}, sources.KimiSessionFiles, sources.LoadKimi},
		{"goose", filepath.Join(sources.GooseRoot(), "sessions"), sources.GooseSessionsDirs(), sources.GooseSessionFiles, sources.LoadGoose},
		{"hermes", sources.HermesProfilesRoot(), []string{sources.HermesProfilesRoot()}, sources.HermesSessionFiles, sources.LoadHermes},
		{"copilot", sources.CopilotRoot(), []string{sources.CopilotRoot()}, sources.CopilotSessionFiles, sources.LoadCopilot},
		{"copilot-chat", rootsOrDefault(sources.CopilotChatRoots(), sources.CopilotChatDefaultRoot()), sources.CopilotChatRoots(), sources.CopilotChatSessionFiles, sources.LoadCopilotChat},
		{"cline", sources.ClineSessionsDir(), append([]string{sources.ClineSessionsDir()}, sources.ClineLegacyRoots()...), sources.ClineSessionFiles, sources.LoadCline},
		{"roo", rootsOrDefault(sources.RooRoots(), sources.RooDefaultRoot()), sources.RooRoots(), sources.RooTaskFiles, sources.LoadRoo},
		{"kilocode", rootsOrDefault(sources.KiloRoots(), sources.KiloDefaultRoot()), sources.KiloRoots(), sources.KiloSessionFiles, sources.LoadKilo},
		{"cherrystudio", rootsOrDefault(sources.CherryStudioAllRoots(), sources.CherryStudioDefaultRoot()), sources.CherryStudioAllRoots(), sources.CherryStudioSessionFiles, sources.LoadCherryStudio},
		{"kiro", sources.KiroRoot(), []string{sources.KiroRoot()}, sources.KiroSessionFiles, sources.LoadKiro},
		{"senpi", sources.SenpiRoot(), []string{sources.SenpiRoot()}, sources.SenpiSessionFiles, sources.LoadSenpi},
		{"kimchi", sources.KimchiRoot(), []string{sources.KimchiRoot()}, sources.KimchiSessionFiles, sources.LoadKimchi},
		{"commandcode", sources.CommandCodeRoot(), []string{sources.CommandCodeRoot()}, sources.CommandCodeSessionFiles, sources.LoadCommandCode},
		{"zcode", sources.ZCodeRoot(), []string{sources.ZCodeRoot()}, sources.ZCodeSessionFiles, sources.LoadZCode},
		{"gjc", sources.GjcRoot(), []string{sources.GjcRoot()}, sources.GjcSessionFiles, sources.LoadGjc},
		{"continue", filepath.Join(sources.ContinueRoot(), "sessions"), []string{filepath.Join(sources.ContinueRoot(), "sessions")}, sources.ContinueSessionFiles, sources.LoadContinue},
		{"pi", sources.PiRoot(), []string{sources.PiRoot()}, sources.PiSessionFiles, sources.LoadPi},
		{"omp", sources.OmpRoot(), []string{sources.OmpRoot()}, sources.OmpSessionFiles, sources.LoadOmp},
		{"prime", sources.PrimeRoot(), sources.PrimeRoots(), sources.PrimeSessionFiles, sources.LoadPrime},
		{"amp", sources.AmpRoot(), []string{sources.AmpRoot()}, sources.AmpThreadFiles, sources.LoadAmp},
		{"openclaw", sources.OpenClawRoot(), []string{sources.OpenClawRoot()}, sources.OpenClawStoreFiles, sources.LoadOpenClaw},
		{"codewhale", sources.CodeWhaleRoot(), sources.CodeWhaleRoots(), sources.CodeWhaleSessionFiles, sources.LoadCodeWhale},
		{"junie", sources.JunieRoot(), []string{sources.JunieRoot()}, sources.JunieSessionFiles, sources.LoadJunie},
		{"jetbrains", sources.JetBrainsRoot(), []string{sources.JetBrainsRoot()}, sources.JetBrainsSessionFiles, sources.LoadJetBrains},
		{"windsurf", sources.WindsurfRoot(), []string{sources.WindsurfRoot()}, sources.WindsurfSessionFiles, sources.LoadWindsurf},
		{"reasonix", sources.ReasonixRoot(), sources.ReasonixRoots(), sources.ReasonixSessionFiles, sources.LoadReasonix},
		{"muse", sources.MuseRoot(), sources.MuseRoots(), sources.MuseSessionFiles, sources.LoadMuse},
		{"mcode", sources.McodeRoot(), []string{sources.McodeRoot()}, sources.McodeSessionFiles, sources.LoadMcode},
		{"deepseek", sources.DeepSeekRoot(), []string{sources.DeepSeekRoot()}, sources.DeepSeekSessionFiles, sources.LoadDeepSeek},
		{"zed", sources.ZedDB(), []string{sources.ZedDB()}, func() []string { return presentFiles(sources.ZedDB()) }, sources.LoadZed},
		{"devin", filepath.Dir(sources.DevinSessionsDB()), []string{filepath.Dir(sources.DevinSessionsDB())}, sources.DevinFiles, sources.LoadDevin},
		// The location is the registry, not a store: Crush keeps one store per
		// project, under the project, and the registry is the only thing that
		// says where they are.
		{"crush", sources.CrushDataHome(), sources.CrushDBs(), sources.CrushDBs, sources.LoadCrush},
		{"deja", sources.NotesFile(), []string{sources.NotesFile()}, func() []string { return presentFiles(sources.NotesFile()) }, sources.LoadNotes},
	}
	skipStore := sources.ExcludedHarnesses()
	for _, it := range items {
		// A store DEJA_STORES leaves out has no row, as in doctor: it is not
		// read, so it has no count to report.
		if sources.StoreSilenced(it.name) {
			continue
		}
		// A store the reader excluded is named and not read. Walking it here
		// reported three sessions and eighteen messages for a harness deja had
		// just been told never to open, which is the opposite of what this
		// screen is for (#3499).
		if skipStore[it.name] {
			fmt.Fprintf(w, "%s\t%s\t%s\n", it.name, it.location, harnessExclusionReason(it.name))
			continue
		}
		redacted := 0
		for _, root := range it.roots {
			redacted += redactionsUnder(redactions, root)
		}
		// Only paths that are files on this machine: a discovery rule can name
		// something that is not one — hermes returns a token for a Postgres
		// store — and neither a size nor a directory can be read off that.
		read := presentFiles(it.files()...)
		size := filesSize(read)
		location := it.location
		if where := commonDir(read); where != "" {
			location = where
		}
		raw := it.load()
		ss := sources.FilterSessions(raw)
		excluded := len(raw) - len(ss)
		msg := sources.CountMessages(ss)
		note := ""
		// `deja sources` is where the empty-machine advice sends people, and a
		// store deja is not allowed to read looked exactly like one nobody has
		// used: `sessions=0 messages=0 size=0 B` (#1000).
		if denied, whole := firstDeniedDir(it.roots); denied != "" {
			note = "\t(cannot be read — permission denied on " + denied + ")"
		} else if !whole {
			note = "\t(permissions not fully checked — too many directories to walk)"
		}
		if it.name == "cursor" && len(sources.CursorDBs()) > 0 && !sources.SQLite3Available() {
			note = "\t(" + sources.SQLite3Problem() + " — Cursor IDE sessions unavailable)"
		}
		// Zed needs zstd as well as sqlite3: sqlite3 alone opens the store and
		// reads nothing out of it, since every thread body is a compressed
		// frame. SkipReason already words which of the two is missing.
		if it.name == "zed" {
			if reason := sources.SkipReason("zed"); reason != "" {
				note = "\t(" + reason + " — Zed threads unavailable)"
			}
		}
		if it.name == "windsurf" {
			if reason := sources.SkipReason("windsurf"); reason != "" {
				note = "\t(" + reason + " — those conversations unavailable)"
			}
		}
		if n := projectExcludePatterns(); n > 0 {
			note += fmt.Sprintf("\texcluded-patterns=%d", n)
		}
		if excluded > 0 {
			note += fmt.Sprintf("\texcluded-sessions=%d", excluded)
		}
		note += unreadNote(it.name)
		fmt.Fprintf(w, "%s\t%s\tsessions=%d messages=%d size=%s redacted=%d%s\n", it.name, location, sources.CountSessions(ss), msg, humanBytes(size), redacted, note)
	}
	// The two rows below are written by hand rather than driven by the table
	// above, and the exclusion has to reach them too: the store #3499 named is
	// one of them, and an excluded opencode kept being opened and kept printing
	// the sqlite3 error the exclusion exists to silence.
	excludedRow := func(name, location string) bool {
		if sources.StoreSilenced(name) {
			return true
		}
		if !skipStore[name] {
			return false
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", name, location, harnessExclusionReason(name))
		return true
	}
	aiderFiles := sources.AiderFiles()
	var aiderSize int64
	aiderRedactions := 0
	for _, p := range aiderFiles {
		if fi, err := os.Stat(p); err == nil {
			aiderSize += fi.Size()
		}
		aiderRedactions += redactions[p]
	}
	aiderLocationForSkip := filepath.Join(sources.Home(), ".aider.chat.history.md")
	skipAider := excludedRow("aider", aiderLocationForSkip)
	var rawAiderSessions, aiderSessions []model.Session
	if !skipAider {
		rawAiderSessions = sources.LoadAider()
		aiderSessions = sources.FilterSessions(rawAiderSessions)
	}
	aiderMessages := 0
	for _, s := range aiderSessions {
		aiderMessages += sources.CountMessages([]model.Session{s})
	}
	aiderLocation := filepath.Join(sources.Home(), ".aider.chat.history.md")
	if roots := os.Getenv("DEJA_AIDER_ROOTS"); roots != "" {
		aiderLocation += string(os.PathListSeparator) + roots
	}
	note := ""
	if n := projectExcludePatterns(); n > 0 {
		note = fmt.Sprintf("\texcluded-patterns=%d", n)
	}
	if excluded := len(rawAiderSessions) - len(aiderSessions); excluded > 0 {
		note += fmt.Sprintf("\texcluded-sessions=%d", excluded)
	}
	note += unreadNote("aider")
	if len(rawAiderSessions) == 0 && !skipAider {
		note += "\tnote=" + aiderNoHistoryHint
	}
	if !skipAider {
		fmt.Fprintf(w, "aider\t%s\tsessions=%d messages=%d size=%s redacted=%d%s\n", aiderLocation, sources.CountSessions(aiderSessions), aiderMessages, humanBytes(aiderSize), aiderRedactions, note)
	}
	if excludedRow("opencode", sources.OpencodeDB()) {
		return
	}
	var size int64
	if fi, err := os.Stat(sources.OpencodeDB()); err == nil {
		size = fi.Size()
	}
	s, m, countErr := sources.OpencodeCounts()
	// The counts come out of sqlite, which knows nothing about the exclude
	// list, so with a pattern in force this row kept reporting sessions that
	// are not indexed, not searchable and not exported while every other row
	// subtracted them (#2247). Only then is the store loaded: counting by SQL
	// is why this row is cheap on a large database.
	opencodeExcluded := 0
	if len(sources.ExclusionPatterns()) > 0 {
		raw := sources.LoadOpencode()
		kept := sources.FilterSessions(raw)
		opencodeExcluded = len(raw) - len(kept)
		// Subtracted from the SQL numbers rather than recounted from what
		// loaded: the loader drops a session holding no text at all, which the
		// row has always counted, so recounting would move the numbers for a
		// reason that has nothing to do with the exclude list.
		dropped := 0
		for _, x := range raw {
			if sources.ExcludedProject(x.Project) {
				dropped += len(x.Messages)
			}
		}
		s, m = max(0, s-opencodeExcluded), max(0, m-dropped)
	}
	note = ""
	if size > 0 && !sources.SQLite3Available() {
		note = "\t(" + sources.SQLite3Problem() + " — opencode sessions unavailable)"
	}
	if n := projectExcludePatterns(); n > 0 {
		note += fmt.Sprintf("\texcluded-patterns=%d", n)
	}
	if opencodeExcluded > 0 {
		note += fmt.Sprintf("\texcluded-sessions=%d", opencodeExcluded)
	}
	// A store sqlite could not open — locked past the timeout, or a file the
	// reader may not open — looked like one nobody had used, the shape #1000
	// fixed for the file stores (#3190).
	if countErr != nil && sources.SQLite3Available() {
		reason := "sqlite3: " + countErr.Error()
		if line := sources.ExitStderrLine(countErr); line != "" {
			reason = "sqlite3: " + line
		}
		if f, err := os.Open(sources.OpencodeDB()); err != nil {
			reason = err.Error()
		} else {
			_ = f.Close()
		}
		note = "\t(cannot be read — " + reason + ")" + note
	}
	note += unreadNote("opencode")
	fmt.Fprintf(w, "opencode\t%s\tsessions=%d messages=%d size=%s redacted=%d%s\n", sources.OpencodeDB(), s, m, humanBytes(size), redactions[sources.OpencodeDB()], note)
}

func redactionsUnder(files map[string]int, root string) int {
	total := 0
	for p, n := range files {
		if p == root || strings.HasPrefix(p, root+string(os.PathSeparator)) {
			total += n
		}
	}
	return total
}

func pathSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err == nil && d.Type()&os.ModeSymlink == 0 && !d.IsDir() {
			if fi, e := d.Info(); e == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total
}
