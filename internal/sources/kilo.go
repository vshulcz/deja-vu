package sources

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Kilo Code (github.com/Kilo-Org/kilocode) keeps two stores, and both are
// formats deja already reads.
//
// The extension (kilocode.kilo-code, confirmed from packages/kilo-vscode/
// package.json) writes Roo's task shape under the host's globalStorage:
//
//	tasks/<taskId>/api_conversation_history.json  (transcript, Cline shape)
//	tasks/<taskId>/history_item.json              (id, ts, task, workspace)
//
// The CLI writes OpenCode's message schema to a SQLite database. Kilo vendors
// OpenCode — packages/opencode is 1,780 files inside the Kilo repository — and
// packages/kilo-vscode/src/legacy-migration reads the task directory to import
// it into that database. So the task files are the history of anyone who used
// Kilo before the migration and the database is where it went afterwards;
// both are worth reading (#3643).
//
// Everything here is the path and the name. The transcript parsing is Roo's
// (parseRooShapedTask) and the database parsing is OpenCode's
// (parseOpencodeSchemaDB).

// KiloExtensionID is the publisher and name of the VS Code extension.
const KiloExtensionID = "kilocode.kilo-code"

// KiloRoots are the globalStorage directories the extension may write to, in
// the same host order as RooRoots. DEJA_KILO_ROOTS replaces the list.
func KiloRoots() []string {
	if list := os.Getenv("DEJA_KILO_ROOTS"); list != "" {
		var out []string
		for _, p := range filepath.SplitList(list) {
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	var bases []string
	for _, host := range vsCodeHosts() {
		bases = append(bases, filepath.Join(vsCodeUserDir(host), "globalStorage", KiloExtensionID))
	}
	var out []string
	for _, b := range bases {
		if fi, err := os.Stat(b); err == nil && fi.IsDir() {
			out = append(out, b)
		}
	}
	return out
}

// KiloTaskFiles lists the transcripts of the extension store.
func KiloTaskFiles() []string {
	var files []string
	for _, root := range KiloRoots() {
		files = append(files, walkFiles(filepath.Join(root, "tasks"), func(p string) bool {
			return filepath.Base(p) == "api_conversation_history.json"
		})...)
	}
	return files
}

// ParseKiloTask reads one task of the extension store.
func ParseKiloTask(path string) ([]model.Session, error) {
	return parseRooShapedTask(path, "kilocode")
}

// KiloDB is the CLI's SQLite store. XDG_DATA_HOME moves it the way it moves
// OpenCode's; DEJA_KILO_DB overrides the path outright.
func KiloDB() string {
	if p := os.Getenv("DEJA_KILO_DB"); p != "" {
		return p
	}
	base := filepath.Join(Home(), ".local", "share")
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		base = v
	}
	return filepath.Join(base, "kilo", "kilo.db")
}

// ParseKiloDB reads the CLI store through OpenCode's schema.
func ParseKiloDB(db string) ([]model.Session, error) {
	return parseOpencodeSchemaDB("kilocode", db, "", 0)
}

// ParseKiloDBSince is ParseKiloDB bounded by the incremental watermark.
func ParseKiloDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return ParseKiloDB(db)
	}
	return parseOpencodeSchemaDB("kilocode", db, opencodeSinceWhere(t), 0)
}

// KiloSessionFiles lists what a Kilo install has on disk: the task transcripts
// and the database when it holds anything.
func KiloSessionFiles() []string {
	out := KiloTaskFiles()
	if fi, err := os.Stat(KiloDB()); err == nil && fi.Size() > 0 {
		out = append(out, KiloDB())
	}
	return out
}

func LoadKilo() []model.Session {
	ss := parseFiles(KiloTaskFiles(), ParseKiloTask)
	dbSS, _ := ParseKiloDB(KiloDB())
	return append(ss, dbSS...)
}

// vsCodeHosts are the editors that load a VS Code extension, in the order the
// Roo reader has used since it was written.
func vsCodeHosts() []string {
	return []string{"Code", "Code - Insiders", "VSCodium", "Cursor", "Windsurf"}
}

// vsCodeUserDir is a host's User directory on this platform.
func vsCodeUserDir(host string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(Home(), "Library", "Application Support", host, "User")
	case "windows":
		app := os.Getenv("APPDATA")
		if app == "" {
			app = filepath.Join(Home(), "AppData", "Roaming")
		}
		return filepath.Join(app, host, "User")
	default:
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(Home(), ".config")
		}
		return filepath.Join(cfg, host, "User")
	}
}

// kiloUnderTasks reports whether a path is inside one of the extension roots,
// so the registry can claim it for incremental ingest.
func kiloUnderTasks(p string) bool {
	for _, root := range KiloRoots() {
		if strings.HasPrefix(p, filepath.Join(root, "tasks")) {
			return true
		}
	}
	return false
}
