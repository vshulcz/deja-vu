package sources

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Cherry Studio (github.com/CherryHQ/cherry-studio) runs Claude Code sessions
// from a desktop app and writes them as ordinary Claude Code transcripts under
// its own app data:
//
//	<app data>/CherryStudio/Data/Agents/.claude/<projects>/<workspace>/<session>.jsonl
//
// and, before the upgrade that added `Data/Agents`, directly under
// `<app data>/CherryStudio/.claude/<projects>/…`. Both are read so a store that
// predates it is not lost.
//
// One difference from a stock store, and it matters for text rather than for
// tokens: Cherry Studio appends the same API call three or four times as the
// stream progresses — a new uuid each time, the same requestId and message id,
// the text growing. Read plainly that makes one reply into three messages, each
// a prefix of the next, so a recall can quote half a sentence and `deja show`
// prints the answer twice before finishing it. The reader collapses a run by
// its request id and keeps the longest (#3644).
//
// Sessions are their own harness rather than extra Claude roots: a reader
// should see which app the work happened in, and `deja sources` should say
// cherrystudio when Cherry Studio is what is on the machine.

// The fixture for this harness cannot carry the real path: the repository
// excludes `.claude/`, so a fixture under `…/Data/Agents/.claude/projects` is
// never committed and the suite passes only where it was written — which is
// how it reached CI red once (#3644).

// CherryStudioRoots are the transcript roots of a Cherry Studio install.
// DEJA_CHERRYSTUDIO_ROOTS replaces the list.
func CherryStudioRoots() []string {
	if list := os.Getenv("DEJA_CHERRYSTUDIO_ROOTS"); list != "" {
		var out []string
		for _, p := range filepath.SplitList(list) {
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	projects := claudeProjectsDirName()
	var out []string
	for _, base := range cherryStudioAppDirs() {
		out = append(out,
			filepath.Join(base, "Data", "Agents", ".claude", projects),
			filepath.Join(base, ".claude", projects),
		)
	}
	return liveDirs(out)
}

// Cherry Studio runs agents on three runtimes (AGENT_TYPES in its main bundle:
// claude-code, pi, dsh), and the other two keep their own stores beside the
// Claude one: pi writes flat <ts>_<id>.jsonl files into Data/Agents/.pi/sessions,
// and dsh runs with DSH_HOME=Data/Agents/.dsh. The formats are the stock ones,
// so those readers parse them; only the harness name says Cherry Studio (#4342).
// DEJA_CHERRYSTUDIO_ROOTS replaces these too: the override names every root.

// CherryStudioAllRoots is every store root of the install, for the rows that
// say where deja looked.
func CherryStudioAllRoots() []string {
	out := CherryStudioRoots()
	out = append(out, cherryStudioPiRoots()...)
	return append(out, cherryStudioDshRoots()...)
}

func cherryStudioPiRoots() []string { return cherryStudioAgentRoots(".pi") }

func cherryStudioDshRoots() []string { return cherryStudioAgentRoots(".dsh") }

func cherryStudioAgentRoots(agent string) []string {
	if os.Getenv("DEJA_CHERRYSTUDIO_ROOTS") != "" {
		return nil
	}
	var out []string
	for _, base := range cherryStudioAppDirs() {
		out = append(out, filepath.Join(base, "Data", "Agents", agent, "sessions"))
	}
	return liveDirs(out)
}

func liveDirs(paths []string) []string {
	var live []string
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			live = append(live, p)
		}
	}
	return live
}

// cherryStudioAppDirs is every directory the user moved the app's data to in
// its settings, then where the app keeps it by default. The move is kept in
// ~/.cherrystudio/boot-config.json under app.user_data_path, a map from the
// executable to the directory, which the app reads at startup to set userData;
// without it a moved store reads as an empty one (#4347). The moved ones come
// first: a move can leave the old directory behind, and a reader that wants
// one file of the app's, such as its database, has to find the live one.
func cherryStudioAppDirs() []string {
	dirs := cherryStudioMovedDirs()
	if def := cherryStudioDefaultDir(); !slices.Contains(dirs, def) {
		dirs = append(dirs, def)
	}
	return dirs
}

func cherryStudioDefaultDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(Home(), "Library", "Application Support", "CherryStudio")
	case "windows":
		app := os.Getenv("APPDATA")
		if app == "" {
			app = filepath.Join(Home(), "AppData", "Roaming")
		}
		return filepath.Join(app, "CherryStudio")
	default:
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(Home(), ".config")
		}
		return filepath.Join(cfg, "CherryStudio")
	}
}

func cherryStudioMovedDirs() []string {
	b, err := os.ReadFile(filepath.Join(Home(), ".cherrystudio", "boot-config.json"))
	if err != nil {
		return nil
	}
	var cfg struct {
		UserDataPath map[string]any `json:"app.user_data_path"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return nil
	}
	var out []string
	for _, v := range cfg.UserDataPath {
		d, ok := v.(string)
		if !ok || !filepath.IsAbs(d) {
			continue
		}
		d = filepath.Clean(d)
		// Moved to the home directory, the legacy <dir>/.claude/projects root
		// is Claude Code's own store, and every Claude session would be listed
		// as Cherry Studio's.
		if slices.Contains(ClaudeRoots(), filepath.Join(d, ".claude", claudeProjectsDirName())) {
			continue
		}
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// CherryStudioDatabases are where the app keeps its own store, MCP servers
// included: `<app data>/Data/cherrystudio.sqlite` (`app.database.file` in
// 2.0.14's out/main/main.js).
func CherryStudioDatabases() []string {
	var out []string
	for _, base := range cherryStudioAppDirs() {
		out = append(out, filepath.Join(base, "Data", "cherrystudio.sqlite"))
	}
	return out
}

// CherryStudioMCPServer is one row of the app's `mcp_server` table.
type CherryStudioMCPServer struct {
	Name    string
	Command string
	Args    []string
	// Active is the switch beside each server in Settings → MCP. The app
	// starts only the servers that have it on, and is_active defaults to off.
	Active bool
}

// CherryStudioMCPServers reads the MCP servers the app itself has, from the
// first of its databases that exists. ok is false when there is none, when
// sqlite3 is missing, or when the read fails: then nothing is known about the
// app, which is different from the app having no deja server (#4344).
//
// Read-only, through the same sqlite3 call the transcript stores use: a
// running app owns this file.
func CherryStudioMCPServers() (db string, servers []CherryStudioMCPServer, ok bool) {
	for _, p := range CherryStudioDatabases() {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			db = p
			break
		}
	}
	if db == "" || !SQLite3Available() {
		return db, nil, false
	}
	out, err := sqliteOutput(db,
		"select json_object('name', name, 'command', coalesce(command, ''), 'args', coalesce(args, ''), 'active', coalesce(is_active, 0)) from mcp_server;")
	if err != nil {
		return db, nil, false
	}
	rows, err := sqliteObjects[struct {
		Name    string `json:"name"`
		Command string `json:"command"`
		Args    string `json:"args"`
		Active  int    `json:"active"`
	}](out)
	if err != nil {
		return db, nil, false
	}
	for _, r := range rows {
		s := CherryStudioMCPServer{Name: r.Name, Command: r.Command, Active: r.Active != 0}
		// args is a JSON array in a text column; a row that does not parse
		// keeps its command, which is still enough to recognise deja.
		_ = json.Unmarshal([]byte(r.Args), &s.Args)
		servers = append(servers, s)
	}
	return db, servers, true
}

// CherryStudioSessionFiles lists the transcripts on disk, from all three
// agent runtimes.
func CherryStudioSessionFiles() []string {
	var out []string
	for _, root := range CherryStudioRoots() {
		out = append(out, walkFiles(root, func(p string) bool {
			return strings.HasSuffix(p, ".jsonl")
		})...)
	}
	for _, root := range cherryStudioPiRoots() {
		out = append(out, walkFiles(root, func(p string) bool {
			return strings.HasSuffix(p, ".jsonl")
		})...)
	}
	for _, root := range cherryStudioDshRoots() {
		out = append(out, walkFiles(root, isDeepSeekLog)...)
	}
	return out
}

// ParseCherryStudioFile reads one transcript: Claude Code's format, with the
// snapshot run collapsed.
func ParseCherryStudioFile(path string) ([]model.Session, error) {
	switch {
	case cherryStudioPiFile(path):
		return ParseCherryStudioPiFileFromOffset(path, 0)
	case cherryStudioDshFile(path):
		return ParseCherryStudioDshFile(path)
	}
	return ParseCherryStudioFileFromOffset(path, 0)
}

// ParseCherryStudioPiFileFromOffset reads a session of Cherry's pi runtime.
// The store is flat, so the project comes from the header's cwd.
func ParseCherryStudioPiFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parsePiShaped(path, offset, "cherrystudio", "-", true)
}

// ParseCherryStudioDshFile reads a session of Cherry's dsh runtime.
func ParseCherryStudioDshFile(path string) ([]model.Session, error) {
	ss, err := ParseDeepSeekFile(path)
	for i := range ss {
		ss[i].Harness = "cherrystudio"
	}
	return ss, err
}

// The kinds match every path the index classifies, and resolving the roots
// reads boot-config.json and stats each candidate: a no-op pass over 3000 pi
// files went from 0.13 s to 0.8 s. The store's own segment rules a path out
// first.
func cherryStudioPiFile(p string) bool {
	return strings.HasSuffix(p, ".jsonl") && strings.Contains(p, cherryStudioAgentSegment(".pi")) &&
		underAnyRoot(p, cherryStudioPiRoots())
}

func cherryStudioAgentSegment(agent string) string {
	sep := string(filepath.Separator)
	return sep + filepath.Join("Data", "Agents", agent, "sessions") + sep
}

func cherryStudioDshFile(p string) bool {
	return isDeepSeekLog(p) && strings.Contains(p, cherryStudioAgentSegment(".dsh")) &&
		underAnyRoot(p, cherryStudioDshRoots())
}

func underAnyRoot(p string, roots []string) bool {
	for _, root := range roots {
		if strings.HasPrefix(p, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ParseCherryStudioFileFromOffset is the incremental read. A collapse spanning
// the watermark cannot see the earlier snapshots, so it is only used when
// CherryStudioResumes says the tail starts a new call.
func ParseCherryStudioFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseClaudeTypedWithOptions(path, func(fn func([]byte)) error {
		return scanJSONLBytes(path, offset, fn)
	}, claudeParseOptions{Harness: "cherrystudio", CollapseSnapshots: true})
}

// CherryStudioUnderRoot reports whether a path belongs to this store, so the
// registry can claim it without stealing a stock Claude transcript.
func CherryStudioUnderRoot(p string) bool {
	sep := string(filepath.Separator)
	if os.Getenv("DEJA_CHERRYSTUDIO_ROOTS") == "" && !strings.Contains(p, sep+".claude"+sep) {
		return false
	}
	for _, root := range CherryStudioRoots() {
		if strings.HasPrefix(p, root) {
			return true
		}
	}
	return false
}

func LoadCherryStudio() []model.Session {
	return parseFiles(CherryStudioSessionFiles(), ParseCherryStudioFile)
}

// CherryStudioResumes reports whether the tail from offset can be appended to
// what is stored. An index that ran mid-stream stored the reply as far as it
// had got; when the tail carries more snapshots of that same call, appending
// kept the half reply next to the full one, and nothing de-duplicates a prefix
// against its completion (#4346). Then the file is read whole and the run
// collapses as it does on a first read.
func CherryStudioResumes(path string, offset int64) bool {
	if offset <= 0 {
		return true
	}
	key := cherrySnapshotKey(lastLineBefore(path, offset))
	if key == "" {
		return true
	}
	// Only as far as the first line that names a call: a stream's snapshots
	// run back to back, so if that line starts another call the stored one is
	// finished. Scanning the whole tail cost a search the read the inline
	// append cap exists to spare it.
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(io.NewSectionReader(f, offset, 1<<62))
	for {
		line, err := r.ReadBytes('\n')
		if k := cherrySnapshotKey(trimJSONSpace(line)); k != "" {
			return k != key
		}
		if err != nil {
			return true
		}
	}
}

// cherrySnapshotKey is the call a line reports on, by requestId or message id
// only: a uuid changes with every snapshot, so it never joins a run.
func cherrySnapshotKey(line []byte) string {
	var v struct {
		RequestID string `json:"requestId"`
		Message   *struct {
			ID string `json:"id"`
		} `json:"message"`
	}
	if len(line) == 0 || json.Unmarshal(line, &v) != nil {
		return ""
	}
	if v.RequestID != "" {
		return "req:" + v.RequestID
	}
	if v.Message != nil && v.Message.ID != "" {
		return "msg:" + v.Message.ID
	}
	return ""
}

// lastLineBefore returns the last complete line ending at end, read backwards
// so a long transcript is not read from its first byte.
func lastLineBefore(path string, end int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var line []byte
	pos := end - 1 // the newline that ends the line
	for pos > 0 {
		n := int64(64 << 10)
		if n > pos {
			n = pos
		}
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, pos-n); err != nil && err != io.EOF {
			return nil
		}
		for i := len(buf) - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				return append(buf[i+1:], line...)
			}
		}
		line = append(buf, line...)
		pos -= n
	}
	return line
}
