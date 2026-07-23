package main

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

type doctorStore struct {
	Name  string   `json:"name"`
	State string   `json:"state"`
	Paths []string `json:"paths"`
	Files int      `json:"files"`
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
	Ingest        map[string]index.HarnessIngest `json:"ingest_health,omitempty"`
	Deep          *index.DeepReport              `json:"deep,omitempty"`
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
	for _, check := range stores {
		store, mod := inspectDoctorStore(check)
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
	report.Version = collectDoctorVersion(lookup)
	report.Embed = collectDoctorEmbed(dir)
	return report
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
		{"opencode", []string{sources.OpencodeDB()}, presentDoctorFile(sources.OpencodeDB()), sources.ParseOpencodeDB},
		{"aider", aiderPaths, sources.AiderFiles(), sources.ParseAiderFile},
		{"gemini", []string{sources.GeminiRoot()}, sources.GeminiChatFiles(), sources.ParseGeminiFile},
		{"cursor", []string{sources.CursorUserRoot(), sources.CursorCLIRoot()}, cursorFiles, parseDoctorCursor},
		{"antigravity", sources.AntigravityRoots(), sources.AntigravityTranscripts(), sources.ParseAntigravityFile},
		{"grok", []string{sources.GrokRoot()}, sources.GrokSessionFiles(), sources.ParseGrokFile},
		{"qwen", []string{filepath.Join(sources.QwenRoot(), "projects")}, sources.QwenSessionFiles(), sources.ParseQwenFile},
		{"kimi", []string{filepath.Join(sources.KimiRoot(), "sessions")}, sources.KimiSessionFiles(), sources.ParseKimiFile},
		{"pi", []string{sources.PiRoot()}, sources.PiSessionFiles(), sources.ParsePiFile},
		{"openclaw", []string{sources.OpenClawRoot()}, sources.OpenClawSessionFiles(), sources.ParseOpenClawFile},
		{"copilot", []string{sources.CopilotRoot()}, sources.CopilotSessionFiles(), sources.ParseCopilotFile},
		{"deja", []string{sources.NotesFile()}, presentDoctorFile(sources.NotesFile()), sources.ParseNotesFile},
	}
}

func presentDoctorFile(path string) []string {
	if doctorExists(path) {
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

func inspectDoctorStore(check doctorStoreCheck) (doctorStore, time.Time) {
	store := doctorStore{Name: check.name, State: "missing", Paths: check.paths, Files: len(check.files)}
	for _, path := range check.paths {
		if path == "" {
			continue
		}
		fi, err := os.Stat(path)
		if err != nil {
			if os.IsPermission(err) {
				store.State = "unreadable"
				return store, time.Time{}
			}
			continue
		}
		if fi.IsDir() {
			f, err := os.Open(path)
			if err != nil {
				if os.IsPermission(err) {
					store.State = "unreadable"
					return store, time.Time{}
				}
				continue
			}
			_, err = f.Readdirnames(1)
			_ = f.Close()
			if err != nil && err != io.EOF && os.IsPermission(err) {
				store.State = "unreadable"
				return store, time.Time{}
			}
		}
	}
	if len(check.files) == 0 {
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
	sessions, _ := check.parse(newest)
	store.State = "ok"
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
