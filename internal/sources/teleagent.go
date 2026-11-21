package sources

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// TeleAgent (星辰超级智能体, the China Telecom TeleAI desktop client) keeps one
// OpenCode-schema SQLite store per account under
// ~/.local/share/TeleAgent/users/<account>/teleagent.db ($TELEAGENT_HOME moves
// the whole tree). Its tables — `session`, `message`, and `part`, or
// `session_v2` and `session_message` — use OpenCode's message schema unchanged,
// so the store reads through the same SQLite reader as mimo
// (parseOpencodeSchemaDB with harness "teleagent"); see opencode.go for the
// field-by-field schema description. The on-disk survey is the reference
// converter's: Nwflower/dsh-chat-import, issue #60, commit 7b5453c.

// TeleAgentDataDir is the root the per-account stores live under.
// DEJA_TELEAGENT_ROOT replaces it outright, TELEAGENT_HOME is the harness's
// own override, and XDG_DATA_HOME relocates ~/.local/share as usual.
func TeleAgentDataDir() string {
	if p := strings.TrimSpace(os.Getenv("DEJA_TELEAGENT_ROOT")); p != "" {
		return expandTilde(p)
	}
	if p := strings.TrimSpace(os.Getenv("TELEAGENT_HOME")); p != "" {
		return expandTilde(p)
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		return filepath.Join(expandTilde(xdg), "TeleAgent")
	}
	return filepath.Join(Home(), ".local", "share", "TeleAgent")
}

// TeleAgentUsersDir is the directory holding one subdirectory per account.
func TeleAgentUsersDir() string {
	return filepath.Join(TeleAgentDataDir(), "users")
}

// TeleAgentDBs lists every account's store that exists and is non-empty, most
// recently written first, so doctor names the account a user just used rather
// than the first one on disk. An account directory without a store (created by
// a login that never opened a conversation) is skipped.
func TeleAgentDBs() []string {
	entries, err := os.ReadDir(TeleAgentUsersDir())
	if err != nil {
		return nil
	}
	type account struct {
		db    string
		mtime time.Time
	}
	var found []account
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		db := filepath.Join(TeleAgentUsersDir(), e.Name(), "teleagent.db")
		if fi, err := os.Stat(db); err == nil && fi.Size() > 0 {
			found = append(found, account{db, fi.ModTime()})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].mtime.After(found[j].mtime) })
	out := make([]string, len(found))
	for i, a := range found {
		out[i] = a.db
	}
	return out
}

// ParseTeleagentDB reads one account's store through OpenCode's schema.
func ParseTeleagentDB(db string) ([]model.Session, error) {
	ss, err := parseOpencodeSchemaDB("teleagent", db, "", 0)
	if err == nil {
		for i := range ss {
			ss[i].Path = db
		}
	}
	return ss, err
}

// ParseTeleagentDBSince reads one account's sessions changed after t.
func ParseTeleagentDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return ParseTeleagentDB(db)
	}
	ss, err := parseOpencodeSchemaDBSince("teleagent", db, t)
	if err == nil {
		for i := range ss {
			ss[i].Path = db
		}
	}
	return ss, err
}

// TeleAgentSessionFiles lists every account's store that exists and is
// non-empty. The harness has no single-store shortcut (unlike mimo): one
// account per machine is the common case but not the only one.
func TeleAgentSessionFiles() []string {
	return TeleAgentDBs()
}

// LoadTeleAgent reads every account's sessions on this machine. An account
// whose query fails marks that store unread through dbRead without losing the
// accounts that did open.
func LoadTeleAgent() []model.Session {
	dbs := TeleAgentDBs()
	all := make([]model.Session, 0, len(dbs))
	for _, db := range dbs {
		ss, err := ParseTeleagentDB(db)
		all = append(all, dbRead(db, ss, err)...)
	}
	return all
}
