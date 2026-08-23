package sources

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Zed's built-in agent keeps every thread in one SQLite store, and unlike the
// other db-backed harnesses the thread body is not JSON in a text column: it
// is a zstd frame in a BLOB. Two things follow from that, and both are easy to
// get wrong.
//
// The store lives under Zed's *data* directory, not its config directory.
// ~/.config/zed exists on every platform and holds settings, keymaps and
// prompts — it holds no threads at all. On macOS the threads are under
// ~/Library/Application Support/Zed; ZedRoot mirrors Zed's own paths::data_dir.
//
// Reading a body needs a zstd decoder. deja has no runtime Go dependencies and
// the standard library has no zstd, so the frame goes through the `zstd` CLI,
// gated by ZstdAvailable the way the five sqlite3-backed stores are gated by
// SQLite3Available. Without it a Zed store is reported as skipped rather than
// as empty (see SkipReason).
//
// Verified against Zed 0.61 (crates/agent/src/db.rs, crates/agent/src/thread.rs,
// crates/paths/src/paths.rs at 968a13d2).

// ZedRoot is Zed's data directory, following paths::data_dir() upstream.
// DEJA_ZED_ROOT relocates it for tests and for a relocated install.
func ZedRoot() string {
	if p := os.Getenv("DEJA_ZED_ROOT"); p != "" {
		return p
	}
	return zedDataDir(runtime.GOOS)
}

// zedDataDir takes the platform as an argument rather than reading runtime.GOOS
// so all three layouts stay reachable from a test on any machine. A Linux-only
// CI job would otherwise never execute the macOS and Windows branches, which
// are the two a contributor is least able to check by hand.
func zedDataDir(goos string) string {
	switch goos {
	case "darwin":
		return filepath.Join(Home(), "Library", "Application Support", "Zed")
	case "windows":
		if p := os.Getenv("LOCALAPPDATA"); p != "" {
			return filepath.Join(p, "Zed")
		}
		return filepath.Join(Home(), "AppData", "Local", "Zed")
	default:
		// Linux and FreeBSD go through dirs::data_local_dir(), which is
		// XDG_DATA_HOME when set. A Flatpak install overrides it outright.
		if p := os.Getenv("FLATPAK_XDG_DATA_HOME"); p != "" {
			return filepath.Join(p, "zed")
		}
		if p := os.Getenv("XDG_DATA_HOME"); p != "" {
			return filepath.Join(p, "zed")
		}
		return filepath.Join(Home(), ".local", "share", "zed")
	}
}

// ZedDB is the single thread store. DEJA_ZED_DB points at one directly, which
// is how the conformance fixture is read without a Zed install.
func ZedDB() string {
	return EnvPath("DEJA_ZED_DB", filepath.Join(ZedRoot(), "threads", "threads.db"))
}

// ZedSettingsPath is the settings file Zed's agent reads MCP servers from. It
// is the *config* directory, not the data one ZedRoot points at: on this
// machine, measured, `~/.config/zed/settings.json` beside a data store under
// `~/Library/Application Support/Zed`.
//
// macOS and Linux are the same path because Zed asks for a config dir rather
// than following the platform convention; Windows is APPDATA. DEJA_ZED_CONFIG
// overrides the lot, which is how this is tested without a Zed install and how
// someone whose layout differs can point deja at it.
func ZedSettingsPath() string {
	if p := os.Getenv("DEJA_ZED_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(zedConfigDir(runtime.GOOS), "settings.json")
}

func zedConfigDir(goos string) string {
	if goos == "windows" {
		if p := os.Getenv("APPDATA"); p != "" {
			return filepath.Join(p, "Zed")
		}
		return filepath.Join(Home(), "AppData", "Roaming", "Zed")
	}
	if p := os.Getenv("XDG_CONFIG_HOME"); p != "" {
		return filepath.Join(p, "zed")
	}
	return filepath.Join(Home(), ".config", "zed")
}

// ZstdAvailable reports whether the zstd CLI the zed parser shells out to is
// on PATH. Zed compresses every thread it writes, so without this the store is
// readable but its contents are not.
func ZstdAvailable() bool {
	_, err := exec.LookPath("zstd")
	return err == nil
}

func LoadZed() []model.Session {
	ss, err := ParseZedDBSince(ZedDB(), time.Time{})
	if err != nil {
		return nil
	}
	return ss
}

func ParseZedDB(db string) ([]model.Session, error) {
	return ParseZedDBSince(db, time.Time{})
}

// zedCols keeps one row shape across both schema generations. parent_id,
// folder_paths, folder_paths_order and created_at were added by ALTER TABLE
// statements Zed runs at startup, so a store written by an older Zed that a
// newer one has never opened still has only the original five columns.
const (
	zedFullCols = `id,summary,updated_at,created_at,folder_paths,data_type,hex(data) as data`
	zedBaseCols = `id,summary,updated_at,null as created_at,null as folder_paths,data_type,hex(data) as data`
)

type zedRow struct {
	ID        string `json:"id"`
	Summary   string `json:"summary"`
	UpdatedAt string `json:"updated_at"`
	CreatedAt string `json:"created_at"`
	Folders   string `json:"folder_paths"`
	DataType  string `json:"data_type"`
	Data      string `json:"data"`
}

// ParseZedDBSince reads threads updated after t. Every thread is one session:
// Zed has no notion of a session spanning threads, and the thread id is the
// stable identifier its own UI resumes by.
func ParseZedDBSince(db string, t time.Time) ([]model.Session, error) {
	// The sqlite3 CLI creates a missing database on open — never let it.
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	where := zedSinceWhere(t)
	rows, err := zedRows(db, zedFullCols, where)
	if err != nil {
		// Retry against the original schema rather than reporting a whole
		// harness as broken because two columns are missing.
		var baseErr error
		rows, baseErr = zedRows(db, zedBaseCols, where)
		if baseErr != nil {
			return nil, err
		}
	}
	out := make([]model.Session, 0, len(rows))
	for _, r := range rows {
		s, ok := zedSession(db, r)
		if !ok {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// zedSinceWhere filters on updated_at without comparing it as text. The column
// is Rust's to_rfc3339 output and carries a "+00:00" offset, while a Go
// watermark formatted with RFC3339Nano ends in "Z" and has its trailing
// fractional zeros stripped. Comparing those as strings is wrong in both
// directions: '.' sorts below 'Z', so a row a fraction of a second *newer*
// than a whole-second watermark compares as older and would be dropped from
// every incremental run. strftime normalises both sides to the same UTC shape
// first. A timestamp SQLite cannot parse yields NULL and is kept, because a
// row deja cannot place is a row to re-read, not one to discard.
func zedSinceWhere(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	const norm = `strftime('%Y-%m-%dT%H:%M:%f',updated_at)`
	w := sqlEscape(t.UTC().Format("2006-01-02T15:04:05.000"))
	return " where " + norm + " is null or " + norm + " > '" + w + "'"
}

func zedRows(db, cols, where string) ([]zedRow, error) {
	q := "select " + cols + " from threads" + where + " order by updated_at"
	cmd := exec.Command("sqlite3", "-readonly", "-json", db, ".timeout 5000", q)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(stdout)
	tok, err := dec.Token()
	if err != nil {
		waitErr := cmd.Wait()
		if err == io.EOF {
			// Empty stdout means either a query that matched nothing or one
			// sqlite3 refused to run. Reporting the second as "no sessions"
			// makes a harness disappear from recall while doctor still calls
			// the store healthy.
			if waitErr != nil {
				return nil, fmt.Errorf("zed: query failed, the store schema may have changed: %w", waitErr)
			}
			return nil, nil
		}
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		_ = cmd.Wait()
		return nil, fmt.Errorf("zed: bad sqlite json")
	}
	// An answer that stops before its closing bracket is a query that was cut
	// short, and the exit status is the only thing that says so: a real sqlite3
	// dying mid-stream returns one. So a stream that simply ends is read as the
	// rows it did deliver, and a non-zero exit is the error. Which of the two
	// steps below notices the missing bytes depends on the toolchain — Go 1.25
	// ended the loop and reported io.EOF from the closing token, Go 1.27 stays
	// in the loop and fails the decode — so neither decides on its own.
	var rows []zedRow
	var cut error
	for dec.More() {
		var r zedRow
		if err := dec.Decode(&r); err != nil {
			cut = err
			break
		}
		rows = append(rows, r)
	}
	if cut == nil {
		if _, err := dec.Token(); err != nil && err != io.EOF {
			cut = err
		}
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	// Anything that is not a truncation — output that was never JSON, a row
	// whose shape is wrong — still fails: the store is then unreadable rather
	// than short.
	var syntax *json.SyntaxError
	if cut != nil && !errors.As(cut, &syntax) && !errors.Is(cut, io.ErrUnexpectedEOF) {
		return nil, cut
	}
	return rows, nil
}

// zedSession turns one row into a session, or reports false when the row
// carries nothing worth indexing. A body that will not decode is skipped
// rather than failing the store: one unreadable thread should not cost a user
// every other thread they have.
func zedSession(db string, r zedRow) (model.Session, bool) {
	if r.ID == "" {
		return model.Session{}, false
	}
	doc, err := zedBody(r.DataType, r.Data)
	if err != nil {
		return model.Session{}, false
	}
	var th zedThread
	if err := json.Unmarshal(doc, &th); err != nil {
		return model.Session{}, false
	}
	updated := zedTime(r.UpdatedAt)
	if updated.IsZero() {
		updated = zedTime(th.UpdatedAt)
	}
	// created_at is set from updated_at when Zed first writes a thread, so on a
	// store predating the column the window collapses to a point rather than
	// starting at the epoch.
	started := zedTime(r.CreatedAt)
	if started.IsZero() {
		started = updated
	}
	title := th.Title
	if title == "" {
		title = th.Summary // legacy agent-1 threads name the field differently
	}
	if title == "" {
		title = r.Summary
	}
	s := model.Session{
		ID:      r.ID,
		Harness: "zed",
		Project: projectName(zedProject(r.Folders, th)),
		Path:    db,
		Title:   title,
		Started: started,
		Updated: updated,
	}
	// Zed stores no per-message timestamp in either thread format, so every
	// message inherits the thread's start the way aider's do. Order is carried
	// by the array itself, which is what recall actually reads.
	for _, raw := range th.Messages {
		role, text := zedMessage(raw)
		if text != "" && !HarnessAuthored(role) {
			s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: started})
		}
		s.Messages = append(s.Messages, zedWork(raw, started)...)
	}
	if len(s.Messages) == 0 {
		return model.Session{}, false
	}
	return s, true
}

// zedBody undoes the storage encoding. data_type is "zstd" for everything Zed
// writes today and "json" for rows written before compression landed; an
// unknown value is an error rather than a guess, because feeding a compressed
// frame to the JSON parser would report a corrupt thread instead of a format
// deja does not know yet.
func zedBody(dataType, hexData string) ([]byte, error) {
	raw, err := hex.DecodeString(hexData)
	if err != nil {
		return nil, err
	}
	switch dataType {
	case "json", "":
		return raw, nil
	case "zstd":
		return zstdDecode(raw)
	default:
		return nil, fmt.Errorf("zed: unknown data_type %q", dataType)
	}
}

// zstdDecode shells out per frame. Concatenating every frame into one `zstd -d`
// would cost one process instead of N, but a single corrupt frame would then
// take the whole store with it, and a store deja can half-read is worth more
// than one it refuses. Incremental runs decompress only the threads that moved.
func zstdDecode(frame []byte) ([]byte, error) {
	if len(frame) == 0 {
		return nil, fmt.Errorf("zed: empty thread body")
	}
	cmd := exec.Command("zstd", "-d", "-c", "-q")
	cmd.Stdin = bytes.NewReader(frame)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("zed: zstd -d: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return out.Bytes(), nil
}

// zedThread is the flattened thread document. Zed serialises
// SerializedThread{#[serde(flatten)] thread, version}, so the version sits
// beside the thread's own fields rather than wrapping them. `title` is the
// current name for what agent-1 threads called `summary`.
type zedThread struct {
	Version   string            `json:"version"`
	Title     string            `json:"title"`
	Summary   string            `json:"summary"`
	UpdatedAt string            `json:"updated_at"`
	Messages  []json.RawMessage `json:"messages"`
	// Snapshot is where the thread was opened. The folder_paths column is the
	// first place to look and is not always there: it arrives by ALTER TABLE,
	// so a store an older Zed wrote and a newer one never opened has only the
	// original five columns. Every thread document carries the path anyway.
	Snapshot zedSnapshot `json:"initial_project_snapshot"`
}

type zedSnapshot struct {
	Worktrees []struct {
		Path string `json:"worktree_path"`
	} `json:"worktree_snapshots"`
}

// zedMessage reads one message in either of the two shapes a live store mixes.
//
// Current threads (version 0.3.0) hold Rust's externally tagged Message enum:
// {"User":{...}}, {"Agent":{...}} or the bare string "Resume". Threads written
// by agent-1 and not re-saved since hold {"role":"user","segments":[...]}.
// Both matter to deja specifically, whose pitch is the history from before it
// was installed: Zed rewrites a thread in the new shape only when that thread
// is next saved.
//
// Only user and assistant text is indexed. Thinking, tool payloads, images and
// mentions' inlined file bodies are skipped by design, as they are for cline.
func zedMessage(raw json.RawMessage) (role, text string) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == `"Resume"` {
		// A resume marker is a control record, not something anyone said.
		return "", ""
	}
	var tagged map[string]json.RawMessage
	if err := json.Unmarshal(raw, &tagged); err != nil {
		return "", ""
	}
	if body, ok := tagged["User"]; ok {
		return "user", zedContentText(body, "Text", "Mention")
	}
	if body, ok := tagged["Agent"]; ok {
		return "assistant", zedContentText(body, "Text")
	}
	return zedLegacyMessage(tagged)
}

// zedWork is what the agent did rather than what it said: the commands it ran
// and the files it named. Zed keeps both inside the same content array as the
// prose, tagged ToolUse, and dropping them left `deja how`, `deja blame`, the
// files-touched line and the worked-on-most ranking blind on a Zed store —
// measured there: 797 ToolUse blocks against 69 Agent.Text ones, so the parser
// was indexing the talk and none of the work.
//
// Tool results are not here to be indexed: Zed stores the call and not what
// came back, so a Zed session cannot pair an error with the command that
// followed it the way `deja fix` does elsewhere.
//
// Modern threads only. An agent-1 message carries `segments` rather than a
// tagged content array, and whether tool calls appear there is not something
// this can be written against: every thread on the store measured for this was
// version 0.3.0, and the shape of a legacy tool segment is unknown. Guessing
// at it would be the mistake zedBody's data_type refuses to make. A legacy
// thread keeps the behaviour it had, which is its prose.
//
// One record per message, as claude's parser emits: a thread that touches the
// same file in three exchanges says so three times, and the ranking reads how
// often a file was worked on.
func zedWork(raw json.RawMessage, t time.Time) []model.Message {
	var tagged map[string]json.RawMessage
	if json.Unmarshal(raw, &tagged) != nil {
		return nil
	}
	body, ok := tagged["Agent"]
	if !ok {
		return nil
	}
	var msg struct {
		Content []struct {
			ToolUse *struct {
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"ToolUse"`
		} `json:"content"`
	}
	if json.Unmarshal(body, &msg) != nil {
		return nil
	}
	var out []model.Message
	var paths []string
	for _, block := range msg.Content {
		if block.ToolUse == nil {
			continue
		}
		if cmd := zedCommand(block.ToolUse.Name, block.ToolUse.Input); cmd != "" {
			if IndexCommands() && worthIndexing(cmd) {
				out = append(out, model.Message{Role: RoleCommand, Text: "$ " + cmd, Time: t})
			}
			continue
		}
		paths = append(paths, zedToolPaths(block.ToolUse.Name, block.ToolUse.Input)...)
	}
	if len(paths) > 0 && IndexToolPaths() {
		out = append(out, model.Message{Role: RoleFiles, Text: strings.Join(dedupeStrings(paths), "\n"), Time: t})
	}
	return out
}

// zedCommand is the shell line a terminal call ran, or "" when the call is not
// one. The name is Zed's own and stable across both thread formats.
func zedCommand(name string, input json.RawMessage) string {
	if name != "terminal" {
		return ""
	}
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	return strings.TrimSpace(in.Command)
}

// zedPathTools are the calls whose input names a file. `terminal` is absent for
// the reason claude's list gives: a path inside a shell command is guesswork.
// `grep` and `find_path` are absent because they name a pattern and a scope,
// not a file the session worked on.
// `list_directory` is absent for the same reason: it names a directory being
// looked around in, not a file the work touched.
var zedPathTools = map[string][]string{
	"edit_file":              {"path"},
	"read_file":              {"path"},
	"create_directory":       {"path"},
	"delete_path":            {"path"},
	"move_path":              {"source_path", "destination_path"},
	"copy_path":              {"source_path", "destination_path"},
	"open":                   {"path"},
	"diagnostics":            {"path"},
	"save_file":              {"paths"},
	"restore_file_from_disk": {"paths"},
}

func zedToolPaths(name string, input json.RawMessage) []string {
	fields, ok := zedPathTools[name]
	if !ok {
		return nil
	}
	var in map[string]json.RawMessage
	if json.Unmarshal(input, &in) != nil {
		return nil
	}
	var out []string
	for _, f := range fields {
		raw, ok := in[f]
		if !ok {
			continue
		}
		// Two shapes for the same thing: most tools take one path, and the
		// ones that act on a selection take a list under `paths`.
		var p string
		if json.Unmarshal(raw, &p) == nil {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
			continue
		}
		var ps []string
		if json.Unmarshal(raw, &ps) != nil {
			continue
		}
		for _, p := range ps {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// dedupeStrings keeps the first of each path: a thread reads the same file
// many times over, and the record is about which files the work touched.
func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// zedContentText joins the wanted variants of a content block array. A Text
// block is a bare string under its tag; a Mention is an object whose `content`
// is the text Zed inlined for the model, which is the only part of it a reader
// can index.
func zedContentText(body json.RawMessage, keep ...string) string {
	var msg struct {
		Content []map[string]json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return ""
	}
	var b strings.Builder
	for _, block := range msg.Content {
		for _, tag := range keep {
			raw, ok := block[tag]
			if !ok {
				continue
			}
			var part string
			if err := json.Unmarshal(raw, &part); err != nil {
				// Mention is a struct, not a string.
				var m struct {
					Content string `json:"content"`
				}
				if err := json.Unmarshal(raw, &m); err != nil {
					continue
				}
				part = m.Content
			}
			if part == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(part)
		}
	}
	return b.String()
}

// zedLegacyMessage reads an agent-1 message. Segments are internally tagged
// ({"type":"text","text":...}), and only text segments are indexed; a thinking
// segment carries the same tag shape and is dropped here.
func zedLegacyMessage(m map[string]json.RawMessage) (role, text string) {
	rawRole, ok := m["role"]
	if !ok {
		return "", ""
	}
	if err := json.Unmarshal(rawRole, &role); err != nil {
		return "", ""
	}
	rawSegments, ok := m["segments"]
	if !ok {
		return "", ""
	}
	var segments []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(rawSegments, &segments); err != nil {
		return "", ""
	}
	var b strings.Builder
	for _, seg := range segments {
		if seg.Type != "text" || seg.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(seg.Text)
	}
	return role, b.String()
}

// zedProject is where a thread belongs: the folder_paths column when the store
// has it, and the path the thread document recorded when it does not.
//
// This matters more than a missing label. A session with no project is
// invisible to auto-recall, which ranks within the project the user is working
// in — so on a store without that column, every Zed thread was indexed and
// then unreachable by the thing that recalls without being asked. Measured on a
// real store: thirty threads, thirty of them in project "-", and every one of
// them carrying its own worktree path in the document.
func zedProject(folders string, th zedThread) string {
	if p := zedFolder(folders); p != "" {
		return p
	}
	for _, w := range th.Snapshot.Worktrees {
		if p := strings.TrimSpace(w.Path); p != "" {
			return p
		}
	}
	return ""
}

// zedFolder picks the project directory out of a serialized PathList. Zed
// writes the paths newline-joined in lexicographic order with a separate
// comma-joined index for display order; deja wants one project name, so the
// first path wins and multi-root threads are named after it.
func zedFolder(folders string) string {
	for _, p := range strings.Split(folders, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			return p
		}
	}
	return ""
}

// zedTime accepts the offset form Rust's to_rfc3339 writes as well as the
// plain forms a hand-edited or migrated row can hold.
func zedTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
