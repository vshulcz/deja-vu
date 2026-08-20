package main

import (
	"bufio"
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
)

type doctorStore struct {
	Name  string   `json:"name"`
	State string   `json:"state"`
	Paths []string `json:"paths"`
	Files int      `json:"files"`
	// IndexedSessions is the other half of the pair the human line has printed
	// since #861: files against sessions is how collapsing shows, and a reader
	// of --json could see only the files (#1088). Imported is counted apart for
	// the same reason the printed line separates it (#894).
	IndexedSessions int `json:"indexed_sessions"`
	Imported        int `json:"indexed_from_elsewhere,omitempty"`
	// Denied names the path that refused to be read, so the warning can point
	// at the directory to fix rather than at the harness (#802). Partial says
	// the rest of the store was readable: sessions are missing from recall
	// rather than the whole harness (#816).
	// Unchecked says the permission walk stopped at its budget, so no
	// permission problem past that point was looked for (#1025).
	Denied    string `json:"denied,omitempty"`
	Partial   bool   `json:"partial,omitempty"`
	Unchecked bool   `json:"unchecked,omitempty"`
}

type doctorComponent struct {
	State       string `json:"state"`
	Path        string `json:"path,omitempty"`
	StaleStores int    `json:"stale_stores,omitempty"`
}

type doctorVersionReport struct {
	State   string `json:"state"`
	Current string `json:"current"`
	Latest  string `json:"latest,omitempty"`
}

type doctorReport struct {
	SchemaVersion int                            `json:"schema_version"`
	Stores        []doctorStore                  `json:"stores"`
	Index         doctorComponent                `json:"index"`
	MCP           []doctorMCPStatus              `json:"mcp"`
	SQLite3       doctorComponent                `json:"sqlite3"`
	Version       doctorVersionReport            `json:"version"`
	Embed         *doctorEmbedReport             `json:"embed,omitempty"`
	Policy        doctorPolicyReport             `json:"policy"`
	Ingest        map[string]index.HarnessIngest `json:"ingest_health,omitempty"`
	Deep          *index.DeepReport              `json:"deep,omitempty"`
}

// doctorPolicyReport is the trust policy in the machine form. The text report
// has had a block for it since #661 while `--json` had no key at all, so a
// script could not see that recall is switched off on a machine (#1027).
type doctorPolicyReport struct {
	// State is one of default, active, unreadable — what is in force, not what
	// the file says, which is the distinction the text block draws too.
	State       string                      `json:"state"`
	Path        string                      `json:"path"`
	Error       string                      `json:"error,omitempty"`
	Total       int                         `json:"indexed_sessions"`
	Activations map[string]doctorPolicyRule `json:"activations"`
	Ignored     []string                    `json:"ignored,omitempty"`
	Inert       []string                    `json:"inert,omitempty"`
}

type doctorPolicyRule struct {
	Rule     string `json:"rule"`
	Withheld int    `json:"withheld"`
}

func collectDoctorPolicy(dir string) doctorPolicyReport {
	r := doctorPolicyReport{State: "active", Path: policy.Path(), Activations: map[string]doctorPolicyRule{}}
	exists, unknown, err := policy.Diagnose()
	switch {
	case !exists:
		r.State = "default"
	case err != nil:
		r.State = "unreadable"
		r.Error = err.Error()
	}
	pol := policy.Load()
	withheld, total := policyWithheldCounts(dir)
	r.Total = total
	for _, a := range []string{policy.ActivationSearch, policy.ActivationMCP, policy.ActivationAuto} {
		r.Activations[a] = doctorPolicyRule{Rule: pol.Describe(a), Withheld: withheld[a]}
	}
	r.Ignored = unknown
	r.Inert = unmatchedImportGroups(dir)
	return r
}

type doctorEmbedReport struct {
	State    string  `json:"state"`
	Model    string  `json:"model,omitempty"`
	Dim      int     `json:"dim,omitempty"`
	Coverage float64 `json:"coverage"`
}

type doctorMCPStatus struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Path  string `json:"path"`
}

type doctorStoreCheck struct {
	name  string
	paths []string
	files []string
	parse func(string) ([]model.Session, error)
}

func collectDoctorReport(lookup doctorVersionLookup, dir string) doctorReport {
	stores := doctorStoreChecks()
	report := doctorReport{SchemaVersion: jsonout.Version, Stores: make([]doctorStore, 0, len(stores))}
	storeMods := make([]time.Time, 0, len(stores))
	indexed := index.HarnessSessionCounts(dir)
	fromElsewhere := index.ImportedSessionCounts(dir)
	for _, check := range stores {
		store, mod := inspectDoctorStore(check)
		store.IndexedSessions = indexed[check.name]
		store.Imported = fromElsewhere[check.name]
		report.Stores = append(report.Stores, store)
		storeMods = append(storeMods, mod)
	}
	report.Index = inspectDoctorIndex(dir, storeMods)
	report.Ingest = index.IngestHealth(dir)
	report.MCP = collectDoctorMCP()
	report.SQLite3.State = "missing"
	if sources.SQLite3Available() {
		report.SQLite3.State = "ok"
	}
	report.Policy = collectDoctorPolicy(dir)
	report.Version = collectDoctorVersion(lookup)
	report.Embed = collectDoctorEmbed(dir)
	return report
}

// firstDeniedDir walks the store roots until something refuses to be read and
// returns that path. The walk is bounded: doctor is a diagnostic command, but
// a harness root can hold tens of thousands of transcripts. The second result
// is false when the budget cut the walk short, so the caller can avoid calling
// a half-checked store whole (#1025).
func firstDeniedDir(paths []string) (string, bool) {
	// Directories, not entries: a store of 50k transcripts sits in a few
	// hundred of them, and counting files spent the budget in the first
	// project — a locked directory later in the walk was never reached, and
	// doctor reported the store whole (#864). A machine with a few thousand
	// projects hit the same wall at the directory bound, so it is high enough
	// that only a pathological tree reaches it (#1025).
	const budget = 200_000
	visited := 0
	whole := true
	home := sources.Home()
	for _, root := range paths {
		// aider's root is the home directory itself: any locked directory
		// anywhere under $HOME would be blamed on aider, and the walk would
		// cost the whole tree.
		if root == "" || root == home {
			continue
		}
		denied := ""
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsPermission(err) {
					denied = p
					return filepath.SkipAll
				}
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if visited++; visited > budget {
				whole = false
				return filepath.SkipAll
			}
			return nil
		})
		if denied != "" {
			return denied, true
		}
	}
	return "", whole
}

// storeNeedsSQLite3 names the harnesses deja reads through the sqlite3 CLI.
func storeNeedsSQLite3(name string) bool {
	switch name {
	case "opencode", "cursor", "grok", "hermes", "goose":
		return true
	}
	return false
}

func collectDoctorEmbed(dir string) *doctorEmbedReport {
	r := &doctorEmbedReport{State: "unavailable"}
	reachable := false
	if c, err := embed.New(); err == nil {
		r.State, r.Model, reachable = "reachable", c.Model, true
	}
	s, err := embed.Read(dir)
	if err != nil {
		if !reachable {
			return nil
		}
		return r
	}
	r.Model, r.Dim = s.Model, s.Dim
	// Coverage the search will not use is not coverage. Vectors address records
	// by offset, so search refuses a sidecar built for an earlier index (#1355)
	// — and doctor is the screen someone opens to find out why semantic results
	// stopped, which is the worst place to report 50% of a file nothing reads
	// (#1359).
	if embed.Stale(dir, s) {
		return r
	}
	if records, err := index.ReadRecords(dir); err == nil && len(records) > 0 {
		r.Coverage = float64(s.Covered) / float64(len(records)) * 100
	}
	return r
}

func doctorStoreChecks() []doctorStoreCheck {
	aiderPaths := []string{sources.Home()}
	aiderPaths = append(aiderPaths, filepath.SplitList(os.Getenv("DEJA_AIDER_ROOTS"))...)
	cursorFiles := append(sources.CursorTranscripts(), sources.CursorDBs()...)
	return []doctorStoreCheck{
		{"claude", []string{sources.ClaudeRoot()}, sources.ClaudeFiles(), sources.ParseClaudeFile},
		{"codex", []string{sources.CodexRoot()}, sources.CodexFiles(), parseDoctorCodex},
		{"opencode", []string{sources.OpencodeDB()}, presentDoctorFile(sources.OpencodeDB()), doctorProbeOpencode},
		{"aider", aiderPaths, sources.AiderFiles(), sources.ParseAiderFile},
		{"gemini", []string{sources.GeminiRoot()}, sources.GeminiChatFiles(), sources.ParseGeminiFile},
		{"cursor", []string{sources.CursorUserRoot(), sources.CursorCLIRoot()}, cursorFiles, parseDoctorCursor},
		{"antigravity", sources.AntigravityRoots(), sources.AntigravityTranscripts(), sources.ParseAntigravityFile},
		{"grok", []string{sources.GrokRoot()}, sources.GrokSessionFiles(), sources.ParseGrokFile},
		{"hermes", []string{sources.HermesHome(), sources.HermesProfilesRoot()}, sources.HermesSessionFiles(), parseDoctorHermes},
		{"qwen", []string{filepath.Join(sources.QwenRoot(), "projects")}, sources.QwenSessionFiles(), sources.ParseQwenFile},
		{"kimi", []string{filepath.Join(sources.KimiRoot(), "sessions")}, sources.KimiSessionFiles(), sources.ParseKimiFile},
		{"goose", []string{filepath.Join(sources.GooseRoot(), "sessions")}, sources.GooseSessionFiles(), parseDoctorGoose},
		{"pi", []string{sources.PiRoot()}, sources.PiSessionFiles(), sources.ParsePiFile},
		{"omp", []string{sources.OmpRoot()}, sources.OmpSessionFiles(), sources.ParseOmpFile},
		{"openclaw", []string{sources.OpenClawRoot()}, sources.OpenClawSessionFiles(), sources.ParseOpenClawFile},
		{"copilot", []string{sources.CopilotRoot()}, sources.CopilotSessionFiles(), sources.ParseCopilotFile},
		// Both were listed by the text rows and by nothing else: absent here,
		// `doctor --json` never named them and "no agent history was found on
		// this machine" was printed on a machine whose history is theirs
		// (#999).
		{"cline", []string{sources.ClineSessionsDir()}, sources.ClineSessionFiles(), sources.ParseClineFile},
		{"roo", sources.RooRoots(), sources.RooTaskFiles(), sources.ParseRooTask},
		{"deja", []string{sources.NotesFile()}, presentDoctorFile(sources.NotesFile()), sources.ParseNotesFile},
	}
}

func presentDoctorFile(path string) []string {
	// By content: an empty notes file is not a store with something in it, and
	// counting it made the two forms of this command disagree about the same
	// row — `missing` in the text, `ok` in JSON (#999).
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() && fi.Size() > 0 {
		return []string{path}
	}
	return nil
}

func parseDoctorCodex(path string) ([]model.Session, error) {
	if filepath.Base(path) == "history.jsonl" {
		return sources.ParseCodexHistory(path)
	}
	return sources.ParseCodexRollout(path)
}

func parseDoctorCursor(path string) ([]model.Session, error) {
	if filepath.Base(path) == "state.vscdb" {
		return sources.ParseCursorDB(path)
	}
	return sources.ParseCursorTranscript(path)
}

func parseDoctorGoose(path string) ([]model.Session, error) {
	if filepath.Base(path) == "sessions.db" {
		return sources.ParseGooseDB(path)
	}
	return sources.ParseGooseFile(path)
}

// parseDoctorHermes probes the SQLite stores by file and the Postgres store by
// DSN, so a PG-backed Hermes reports its real health instead of the frozen
// pre-cutover state.db (#1018).
func parseDoctorHermes(path string) ([]model.Session, error) {
	if sources.IsHermesPGStore(path) {
		return sources.ParseHermesPG(sources.HermesPGDSN(), 0)
	}
	return sources.ParseHermesDB(path)
}

func inspectDoctorStore(check doctorStoreCheck) (doctorStore, time.Time) {
	store := doctorStore{Name: check.name, State: "missing", Paths: check.paths, Files: len(check.files)}
	for _, path := range check.paths {
		if path == "" {
			continue
		}
		fi, err := os.Stat(path)
		if err != nil {
			if os.IsPermission(err) {
				store.State = "denied"
				store.Denied = path
				return store, time.Time{}
			}
			continue
		}
		if fi.IsDir() {
			f, err := os.Open(path)
			if err != nil {
				if os.IsPermission(err) {
					store.State = "denied"
					store.Denied = path
					return store, time.Time{}
				}
				continue
			}
			_, err = f.Readdirnames(1)
			_ = f.Close()
			if err != nil && err != io.EOF && os.IsPermission(err) {
				store.State = "denied"
				store.Denied = path
				return store, time.Time{}
			}
		}
	}
	// The file collectors swallow EACCES, so a locked directory silently takes
	// its sessions out of recall. With no files at all that read as a harness
	// nobody has used (#802); with some files it read as a complete store
	// missing a few, which is quieter still (#816).
	denied, whole := firstDeniedDir(check.paths)
	if denied != "" {
		store.State = "denied"
		store.Denied = denied
		store.Partial = len(check.files) > 0
		return store, time.Time{}
	}
	// Nothing refused to be read *of what was walked*. Saying "found" for a
	// store whose walk stopped early is the same silence #864 closed, one
	// order of magnitude up (#1025).
	store.Unchecked = !whole
	if len(check.files) == 0 {
		// The text rows have separated a store whose disk went away from one
		// that was deleted since #933; a script reading this could not (#999).
		for _, p := range check.paths {
			if p != "" && storeDiskGone(p) {
				store.State = "unplugged"
				break
			}
		}
		return store, time.Time{}
	}
	newest, mod := newestDoctorFile(check.files)
	f, err := os.Open(newest)
	if err != nil {
		if os.IsPermission(err) {
			store.State = "unreadable"
		} else {
			store.State = "parsed-zero"
		}
		return store, mod
	}
	_ = f.Close()
	sessions, parseErr := check.parse(newest)
	store.State = "ok"
	// A parser that could not run is not a store that could not be understood.
	// Without this, removing the sqlite3 CLI told the user their harness had
	// changed its format and asked them to report it — two lines above deja
	// naming the missing CLI itself (#792).
	if parseErr != nil && storeNeedsSQLite3(check.name) && !sources.SQLite3Available() {
		store.State = "needs-sqlite3"
		return store, mod
	}
	// A parser that refuses to read the store is the loudest thing doctor can
	// learn, and it used to be discarded: a harness that changed its schema
	// showed up here as a healthy store while its recall was empty.
	if parseErr != nil {
		store.State = "unreadable"
		return store, mod
	}
	// A session someone opened and closed without typing parses to nothing,
	// correctly. Calling that a parse failure sends people looking for a bug
	// in deja when the file simply holds no conversation — so only a file
	// with something to parse counts.
	if len(sessions) == 0 && !fileHasConversation(newest) {
		return store, mod
	}
	if len(sessions) == 0 {
		store.State = "parsed-zero"
	}
	return store, mod
}

func newestDoctorFile(files []string) (string, time.Time) {
	files = append([]string(nil), files...)
	sort.Strings(files)
	newest := files[0]
	var newestMod time.Time
	for _, path := range files {
		if fi, err := os.Stat(path); err == nil && fi.ModTime().After(newestMod) {
			newest, newestMod = path, fi.ModTime()
		}
	}
	return newest, newestMod
}

func inspectDoctorIndex(dir string, storeMods []time.Time) doctorComponent {
	result := doctorComponent{State: "missing", Path: dir}
	if !index.HasManifest(dir) {
		return result
	}
	result.State = "ok"
	builtAt := index.ManifestBuiltAt(dir)
	for _, mod := range storeMods {
		if !mod.IsZero() && mod.After(builtAt) {
			result.StaleStores++
		}
	}
	if result.StaleStores > 0 {
		result.State = "stale"
		// "run `deja index`" is the advice attached to `stale`, and it cannot
		// be followed when the index cannot be written: the build fails with
		// the same permission error every time. search says so on this exact
		// state; doctor called it ordinary staleness (#1004).
		if !indexDirWritable(dir) {
			result.State = "stale-readonly"
		}
	}
	return result
}

func collectDoctorMCP() []doctorMCPStatus {
	configs := doctorMCPConfigs()
	out := make([]doctorMCPStatus, 0, len(configs))
	for _, config := range configs {
		state := "config-missing"
		if doctorExists(config.path) {
			state = "not-wired"
			if config.wired(config.path) {
				state = "wired"
			}
		}
		out = append(out, doctorMCPStatus{Name: config.name, State: state, Path: config.path})
	}
	return out
}

func collectDoctorVersion(lookup doctorVersionLookup) doctorVersionReport {
	report := doctorVersionReport{State: "unknown", Current: version}
	if lookup == nil {
		report.State = "offline"
		return report
	}
	latest, ok := lookup()
	if !ok {
		return report
	}
	report.Latest = latest
	current := normalizeUpdateVersion(version)
	order, comparable := compareUpdateVersions(current, latest)
	if !comparable {
		if current == "dev" || current == "" {
			report.State = "dev"
		}
		return report
	}
	switch {
	case order < 0:
		report.State = "update-available"
	case order > 0:
		report.State = "ahead"
	default:
		report.State = "ok"
	}
	return report
}

func doctorParsedZeroWarning() string {
	var names []string
	for _, check := range doctorStoreChecks() {
		store, _ := inspectDoctorStore(check)
		if store.State == "parsed-zero" {
			names = append(names, store.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "warning: " + strings.Join(names, ", ") + " files found but newest parsed to zero"
}

// fileHasConversation reports whether a store file holds anything that should
// have produced a message. Harness files begin with setup records — protocol
// metadata, model config, tool lists — and a session that was opened and never
// used contains only those.
func fileHasConversation(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true // unreadable is a real problem; let the caller flag it
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(io.LimitReader(f, 1<<20))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		for _, marker := range [][]byte{
			[]byte(`"role":"user"`), []byte(`"role": "user"`),
			[]byte(`"role":"assistant"`), []byte(`"role": "assistant"`),
			[]byte(`"type":"user"`), []byte(`"type":"assistant"`),
			[]byte("#### "),
		} {
			if bytes.Contains(line, marker) {
				return true
			}
		}
	}
	return false
}

// doctorProbeOpencode answers the only question the store check asks — does
// this store parse into sessions — without reading all of it. Parsing the
// whole database took 6.5 seconds of doctor's 8 on a 2.8 GB store, and every
// row after the first few adds nothing to the answer.
func doctorProbeOpencode(db string) ([]model.Session, error) {
	// A plain limit does not help: the query orders by session and message
	// time, so sqlite sorts the whole join before it can take the first row.
	// Narrowing to the newest session first is what makes this cheap.
	return sources.ParseOpencodeNewest(db)
}
