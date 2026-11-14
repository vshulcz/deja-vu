package sources

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// MiMo-Code (github.com/XiaomiMiMo/MiMo-Code, `@xiaomimimo/cli`) is Xiaomi's
// OpenCode-derived terminal agent. Upstream (`packages/cli/src/global/index.ts`,
// `packages/cli/src/storage/db.ts`, `packages/cli/src/config/paths.ts`) keeps
// its SQLite store in OpenCode's schema (`session` / `session_v2`, `message`,
// `part`, `session_message`) under `~/.local/share/mimocode/mimocode.db` (or
// `$MIMOCODE_HOME/data/mimocode.db`) and reads its config from
// `~/.config/mimocode/mimocode.jsonc` (`mimocode.json`).

// MimoConfigDir returns the directory holding `mimocode.jsonc` / `mimocode.json`
// and `plugins/`. `DEJA_MIMO_CONFIG_DIR` or `MIMOCODE_CONFIG_DIR` overrides it;
// `MIMOCODE_HOME` places it under `<MIMOCODE_HOME>/config`.
func MimoConfigDir() string {
	if p := strings.TrimSpace(os.Getenv("DEJA_MIMO_CONFIG_DIR")); p != "" {
		return expandTilde(p)
	}
	if p := strings.TrimSpace(os.Getenv("MIMOCODE_CONFIG_DIR")); p != "" {
		return expandTilde(p)
	}
	if h := strings.TrimSpace(os.Getenv("MIMOCODE_HOME")); h != "" {
		return filepath.Join(expandTilde(h), "config")
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "mimocode")
	}
	return filepath.Join(Home(), ".config", "mimocode")
}

func mimoDataDir() string {
	if h := strings.TrimSpace(os.Getenv("MIMOCODE_HOME")); h != "" {
		return filepath.Join(expandTilde(h), "data")
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		return filepath.Join(xdg, "mimocode")
	}
	return filepath.Join(Home(), ".local", "share", "mimocode")
}

// MimoDB returns the path to MiMo-Code's SQLite store. `DEJA_MIMO_DB` overrides
// the path outright; `MIMOCODE_DB` resolves relative to the data dir unless
// already absolute.
func MimoDB() string {
	if p := strings.TrimSpace(os.Getenv("DEJA_MIMO_DB")); p != "" {
		return p
	}
	if v := strings.TrimSpace(os.Getenv("MIMOCODE_DB")); v != "" && v != ":memory:" {
		if filepath.IsAbs(v) {
			return v
		}
		return filepath.Join(mimoDataDir(), v)
	}
	def := filepath.Join(Home(), ".local", "share", "mimocode", "mimocode.db")
	if os.Getenv("MIMOCODE_HOME") == "" && os.Getenv("XDG_DATA_HOME") != "" {
		xdg := filepath.Join(mimoDataDir(), "mimocode.db")
		if _, err := os.Stat(xdg); err == nil {
			return xdg
		}
		if _, err := os.Stat(def); err == nil {
			return def
		}
		return xdg
	}
	return filepath.Join(mimoDataDir(), "mimocode.db")
}

// ParseMimoDB reads MiMo-Code's SQLite store through OpenCode's schema.
func ParseMimoDB(db string) ([]model.Session, error) {
	return parseOpencodeSchemaDB("mimo", db, "", 0)
}

// ParseMimoDBSince reads sessions in MiMo-Code's SQLite store changed after t.
func ParseMimoDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return ParseMimoDB(db)
	}
	return parseOpencodeSchemaDBSince("mimo", db, t)
}

// MimoSessionFiles lists the MiMo-Code database when it exists and is non-empty.
func MimoSessionFiles() []string {
	if fi, err := os.Stat(MimoDB()); err == nil && fi.Size() > 0 {
		return []string{MimoDB()}
	}
	return nil
}

// LoadMimo reads every MiMo-Code session on this machine.
func LoadMimo() []model.Session {
	db := MimoDB()
	ss, err := ParseMimoDB(db)
	return dbRead(db, ss, err)
}
