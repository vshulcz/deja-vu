package sources

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Before the current runtime ZCode kept each conversation as one snapshot:
//
//	~/.zcode/v2/sessions/<workspaceHash>/<taskId>.json
//	{meta: {taskId, acpSessionId, workspacePath, title, createdAt, updatedAt},
//	 messages: [{role, content, timestamp}]}
//
// That is the shape the 3.14.4 runtime's own restore-legacy-sessions skill
// scans (scan-legacy-sessions.mjs), skipping `.deleted.json`, and the files
// stay there until the user restores them by hand. A restored one lands in the
// CLI database under acpSessionId || taskId, the id read here, and is read from
// there instead (#4432).

// ZCodeLegacyRoot is the snapshot root. DEJA_ZCODE_LEGACY_ROOT replaces it.
func ZCodeLegacyRoot() string {
	return EnvPath("DEJA_ZCODE_LEGACY_ROOT", filepath.Join(ZCodeConfigDir(), "v2", "sessions"))
}

// ZCodeLegacyFiles lists the snapshots ZCode itself would offer to restore.
func ZCodeLegacyFiles() []string {
	return walkFiles(ZCodeLegacyRoot(), zcodeLegacyFile)
}

// ZCodeLegacyUnderRoot lets the registry claim a path for ingest.
func ZCodeLegacyUnderRoot(p string) bool {
	return underRoot(p, ZCodeLegacyRoot(), ".json") && zcodeLegacyFile(p)
}

func zcodeLegacyFile(p string) bool {
	return strings.HasSuffix(p, ".json") && !strings.HasSuffix(p, ".deleted.json")
}

// ParseZCodeLegacyFile reads one snapshot, unless the CLI database already
// holds the session it was restored as.
func ParseZCodeLegacyFile(path string) ([]model.Session, error) {
	ss, err := parseZCodeLegacy(path)
	if len(ss) == 0 || zcodeRestoredIDs(ZCodeDB())[ss[0].ID] {
		return nil, err
	}
	return ss, err
}

func parseZCodeLegacy(path string) ([]model.Session, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snap struct {
		Meta struct {
			TaskID        string `json:"taskId"`
			ACPSessionID  string `json:"acpSessionId"`
			WorkspacePath string `json:"workspacePath"`
			Title         string `json:"title"`
			CreatedAt     any    `json:"createdAt"`
			UpdatedAt     any    `json:"updatedAt"`
		} `json:"meta"`
		Messages []struct {
			Role      string `json:"role"`
			Content   any    `json:"content"`
			Timestamp any    `json:"timestamp"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, err
	}
	m := snap.Meta
	s := model.Session{
		Harness: "zcode",
		ID:      firstNonEmpty(m.ACPSessionID, firstNonEmpty(m.TaskID, strings.TrimSuffix(filepath.Base(path), ".json"))),
		Project: cwdProjectName(m.WorkspacePath),
		Title:   strings.TrimSpace(m.Title),
		Path:    path,
	}
	s.Touch(parseTimeAny(m.CreatedAt))
	s.Touch(parseTimeAny(m.UpdatedAt))
	for _, msg := range snap.Messages {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		// The restore skill reads content as a string and nothing else.
		txt, _ := msg.Content.(string)
		if strings.TrimSpace(txt) == "" {
			continue
		}
		t := parseTimeAny(msg.Timestamp)
		s.Touch(t)
		s.Messages = append(s.Messages, model.Message{Role: msg.Role, Text: capParsedMessage(txt), Time: t})
	}
	if len(s.Messages) == 0 {
		return nil, nil
	}
	return []model.Session{s}, nil
}

// zcodeLegacySidecar fingerprints whether the CLI database holds the session
// a snapshot was restored as. The reader skips such a snapshot, but a restore
// leaves the file as it was, so no pass re-read it and its turns stayed beside
// the database's (#4448).
func zcodeLegacySidecar(p string) (int64, int64) {
	ids := zcodeRestoredIDs(ZCodeDB())
	if len(ids) == 0 {
		return 0, 0
	}
	if id := zcodeLegacyID(p); id != "" && ids[id] {
		return 1, 1
	}
	return 0, 0
}

// zcodeLegacyID is the id a snapshot is read under, kept per file state so a
// pass does not decode every snapshot again.
func zcodeLegacyID(p string) string {
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	stamp := fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
	zcodeIDsMu.Lock()
	c, ok := zcodeLegacyIDs[p]
	zcodeIDsMu.Unlock()
	if ok && c[0] == stamp {
		return c[1]
	}
	id := ""
	if f, err := os.Open(p); err == nil {
		if meta, ok := zcodeSnapshotMeta(f); ok {
			id = firstNonEmpty(meta.ACPSessionID, firstNonEmpty(meta.TaskID, strings.TrimSuffix(filepath.Base(p), ".json")))
		}
		_ = f.Close()
	}
	zcodeIDsMu.Lock()
	zcodeLegacyIDs[p] = [2]string{stamp, id}
	zcodeIDsMu.Unlock()
	return id
}

var zcodeLegacyIDs = map[string][2]string{}

type zcodeSnapshotIDs struct {
	TaskID       string `json:"taskId"`
	ACPSessionID string `json:"acpSessionId"`
}

// zcodeSnapshotMeta decodes a snapshot's meta and stops there. The cache above
// lives as long as the process, and each pass is a process, so this runs for
// every snapshot on every pass: decoding the messages too cost ~120 ms a pass
// on 100 MB of them.
func zcodeSnapshotMeta(r io.Reader) (zcodeSnapshotIDs, bool) {
	var meta zcodeSnapshotIDs
	d := json.NewDecoder(bufio.NewReader(r))
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		return meta, false
	}
	for d.More() {
		tok, err := d.Token()
		if err != nil {
			return meta, false
		}
		if key, _ := tok.(string); strings.EqualFold(key, "meta") {
			return meta, d.Decode(&meta) == nil
		}
		var skip json.RawMessage
		if d.Decode(&skip) != nil {
			return meta, false
		}
	}
	return meta, true
}

// zcodeRestoredIDs is the session ids in the CLI database, read once for each
// state of the file and its WAL rather than once for each snapshot.
func zcodeRestoredIDs(db string) map[string]bool {
	key := zcodeDBStamp(db)
	if key == "" || !SQLite3Available() {
		return nil
	}
	zcodeIDsMu.Lock()
	defer zcodeIDsMu.Unlock()
	if zcodeIDsKey == key {
		return zcodeIDs
	}
	cmd, stop := sqliteReadCmd(db, `select json_object('id',id) from session`)
	out, err := cmd.Output()
	stop()
	ids := map[string]bool{}
	if err == nil && len(out) > 0 {
		if rows, err := sqliteObjects[struct {
			ID string `json:"id"`
		}](out); err == nil {
			for _, r := range rows {
				ids[r.ID] = true
			}
		}
	}
	zcodeIDsKey, zcodeIDs = key, ids
	return ids
}

var (
	zcodeIDsMu  sync.Mutex
	zcodeIDsKey string
	zcodeIDs    map[string]bool
)

// zcodeDBStamp names a state of the database: its path, and the size and
// time of it and its WAL, where a write lands before a checkpoint.
func zcodeDBStamp(db string) string {
	fi, err := os.Stat(db)
	if err != nil || fi.Size() == 0 {
		return ""
	}
	stamp := fmt.Sprintf("%s|%d|%d", db, fi.ModTime().UnixNano(), fi.Size())
	if wal, err := os.Stat(db + "-wal"); err == nil {
		stamp += fmt.Sprintf("|%d|%d", wal.ModTime().UnixNano(), wal.Size())
	}
	return stamp
}
