package sources

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// zedHome isolates a test from a real Zed install. Both the store location and
// the home it is derived from are pinned, so a contributor with threads on disk
// runs the same test as CI.
func zedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("FLATPAK_XDG_DATA_HOME", "")
	t.Setenv("DEJA_ZED_ROOT", filepath.Join(home, "zed"))
	t.Setenv("DEJA_ZED_DB", "")
	return home
}

func zedTestDB(t *testing.T, sql string) string {
	t.Helper()
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "threads.db")
	// The script goes in on stdin, not as an argument: the fixture opens with a
	// `--` comment and the sqlite3 CLI reads a leading `--` as an option.
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sqlite fixture: %v: %s", err, out)
	}
	return db
}

// zedZstdHex compresses a thread body the way Zed does, so a test row is a real
// frame rather than a recorded one.
func zedZstdHex(t *testing.T, plain string) string {
	t.Helper()
	if !ZstdAvailable() {
		t.Skip("zstd not installed")
	}
	cmd := exec.Command("zstd", "-q", "-3", "-c")
	cmd.Stdin = strings.NewReader(plain)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("compress fixture body: %v", err)
	}
	return hex.EncodeToString(out.Bytes())
}

const zedSchema = `create table threads (
    id text primary key,
    summary text not null,
    updated_at text not null,
    data_type text not null,
    data blob not null,
    parent_id text,
    folder_paths text,
    folder_paths_order text,
    created_at text
);`

// zedSchemaOriginal is the table as Zed first created it, before the ALTER TABLE
// statements that added parent_id, the folder columns and created_at.
const zedSchemaOriginal = `create table threads (
    id text primary key,
    summary text not null,
    updated_at text not null,
    data_type text not null,
    data blob not null
);`

const zedModernBody = `{"version":"0.3.0","title":"modern thread","updated_at":"2026-07-19T09:00:02Z","messages":[{"User":{"id":"u1","content":[{"Text":"first question"}]}},{"Agent":{"content":[{"Text":"first answer"}],"tool_results":{},"reasoning_details":null}}]}`

func TestParseZedDBReadsBothStorageEncodings(t *testing.T) {
	zedHome(t)
	sql, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "registry", "zed", "zed.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db := zedTestDB(t, string(sql))
	if !ZstdAvailable() {
		t.Skip("zstd not installed")
	}
	sessions, err := ParseZedDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2: %#v", len(sessions), sessions)
	}
	// Ordered by updated_at, so the pre-compression thread comes first.
	want := []struct {
		id, title string
		texts     []string
	}{
		{"registry-zed-legacy", "legacy zed thread", []string{"does the pre-compression thread shape still load?", "agent-1 threads still parse"}},
		{"registry-zed-modern", "read the zed thread store", []string{"where does zed keep its agent threads?", "in threads.db under the data dir"}},
	}
	for i, w := range want {
		got := sessions[i]
		if got.ID != w.id || got.Title != w.title {
			t.Fatalf("session %d = %q/%q, want %q/%q", i, got.ID, got.Title, w.id, w.title)
		}
		if got.Harness != "zed" || got.Project != "registry-demo" || got.Path != db {
			t.Fatalf("session %d identity = %#v", i, got)
		}
		if got.Started.IsZero() || got.Updated.IsZero() || !got.Started.Before(got.Updated) {
			t.Fatalf("session %d window = %v..%v", i, got.Started, got.Updated)
		}
		if len(got.Messages) != len(w.texts) {
			t.Fatalf("session %d messages = %#v", i, got.Messages)
		}
		for j, text := range w.texts {
			role := "user"
			if j%2 == 1 {
				role = "assistant"
			}
			if got.Messages[j].Role != role || got.Messages[j].Text != text {
				t.Fatalf("session %d message %d = %#v, want %s/%q", i, j, got.Messages[j], role, text)
			}
			if !got.Messages[j].Time.Equal(got.Started) {
				t.Fatalf("session %d message %d time = %v, want the thread start %v", i, j, got.Messages[j].Time, got.Started)
			}
		}
	}
}

// A thread whose body will not decode costs that thread, not the store. The
// opposite — returning an error — would drop every other thread a user has
// because one row is truncated.
func TestParseZedDBSkipsUnreadableThreadWithoutLosingTheStore(t *testing.T) {
	zedHome(t)
	good := zedZstdHex(t, zedModernBody)
	sql := zedSchema + `
insert into threads (id,summary,updated_at,data_type,data,folder_paths,created_at) values
 ('broken-frame','truncated','2026-07-19T09:00:00+00:00','zstd',x'28b52ffd00',  '/w/p','2026-07-19T09:00:00+00:00'),
 ('unknown-type','future encoding','2026-07-19T09:00:01+00:00','brotli',x'00',   '/w/p','2026-07-19T09:00:01+00:00'),
 ('not-json','json but not a thread','2026-07-19T09:00:02+00:00','json','not json at all','/w/p','2026-07-19T09:00:02+00:00'),
 ('no-messages','empty thread','2026-07-19T09:00:03+00:00','json','{"version":"0.3.0","title":"t","messages":[]}','/w/p','2026-07-19T09:00:03+00:00'),
 ('ok','fine','2026-07-19T09:00:04+00:00','zstd',x'` + good + `','/w/p','2026-07-19T09:00:04+00:00');`
	sessions, err := ParseZedDB(zedTestDB(t, sql))
	if err != nil {
		t.Fatalf("one unreadable row must not fail the store: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "ok" {
		t.Fatalf("sessions = %#v, want only the readable thread", sessions)
	}
}

// The regression this guards is silent: comparing Zed's "+00:00" timestamps
// against a Go watermark as text drops rows that are newer by a fraction of a
// second, because '.' sorts below 'Z'. An incremental index would then never
// see them again.
func TestParseZedDBSinceKeepsARowNewerByAFractionOfASecond(t *testing.T) {
	zedHome(t)
	body := zedZstdHex(t, zedModernBody)
	sql := zedSchema + `
insert into threads (id,summary,updated_at,data_type,data,folder_paths,created_at) values
 ('older','older','2026-07-19T08:59:59.900+00:00','zstd',x'` + body + `','/w/p','2026-07-19T08:59:00+00:00'),
 ('fraction-newer','fraction newer','2026-07-19T09:00:00.500+00:00','zstd',x'` + body + `','/w/p','2026-07-19T09:00:00+00:00'),
 ('clearly-newer','clearly newer','2026-07-19T09:00:05+00:00','zstd',x'` + body + `','/w/p','2026-07-19T09:00:05+00:00');`
	db := zedTestDB(t, sql)

	watermark := time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
	sessions, err := ParseZedDBSince(db, watermark)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "fraction-newer,clearly-newer" {
		t.Fatalf("since %v returned %v, want the two newer rows", watermark, ids)
	}

	// A zero watermark is a full read, not a filter.
	all, err := ParseZedDBSince(db, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("zero watermark returned %d sessions, want 3", len(all))
	}
}

// A timestamp SQLite cannot parse must be re-read rather than dropped: a row
// deja cannot place is not a row it has already seen.
func TestParseZedDBSinceKeepsRowsWithAnUnparseableTimestamp(t *testing.T) {
	zedHome(t)
	body := zedZstdHex(t, zedModernBody)
	sql := zedSchema + `
insert into threads (id,summary,updated_at,data_type,data,folder_paths,created_at) values
 ('no-timestamp','unparseable','not a timestamp','zstd',x'` + body + `','/w/p','2026-07-19T09:00:00+00:00');`
	sessions, err := ParseZedDBSince(zedTestDB(t, sql), time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "no-timestamp" {
		t.Fatalf("sessions = %#v, want the unplaceable row kept", sessions)
	}
	// updated_at is unusable, so the thread document's own field carries the window.
	if got := sessions[0].Updated.Format(time.RFC3339); got != "2026-07-19T09:00:02Z" {
		t.Fatalf("updated = %s, want the value from the thread body", got)
	}
}

// Zed adds parent_id, the folder columns and created_at with ALTER TABLE at
// startup, so a store an older Zed wrote and a newer one never opened has only
// the original five columns.
func TestParseZedDBFallsBackToTheOriginalSchema(t *testing.T) {
	zedHome(t)
	body := zedZstdHex(t, zedModernBody)
	sql := zedSchemaOriginal + `
insert into threads (id,summary,updated_at,data_type,data) values
 ('pre-alter','pre alter','2026-07-19T09:00:02+00:00','zstd',x'` + body + `');`
	sessions, err := ParseZedDB(zedTestDB(t, sql))
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %#v, want one", sessions)
	}
	got := sessions[0]
	if got.ID != "pre-alter" || len(got.Messages) != 2 {
		t.Fatalf("session = %#v", got)
	}
	// No folder column and no snapshot in the document either, which is the only
	// case left with nothing to name the project by. With no created_at the
	// window collapses onto updated_at rather than starting at the epoch.
	if got.Project != "-" {
		t.Fatalf("project = %q, want the unknown-project marker", got.Project)
	}
	if !got.Started.Equal(got.Updated) || got.Started.IsZero() {
		t.Fatalf("window = %v..%v, want a single instant", got.Started, got.Updated)
	}
}

// A real thread carries the path it was opened in, whatever the schema. The
// folder_paths column arrives by ALTER TABLE, so a store an older Zed wrote and
// a newer one never opened has only the original five columns — and on such a
// store every thread was landing in the unknown-project marker.
//
// That is worse than a missing label: a session with no project is invisible to
// auto-recall, which ranks within the project the user is working in. Measured
// on a real 30-thread store, all thirty were "-", and all thirty carried their
// own worktree path in the document.
func TestParseZedDBTakesTheProjectFromTheDocumentWhenTheColumnIsGone(t *testing.T) {
	zedHome(t)
	const withSnapshot = `{"version":"0.3.0","title":"snapshot thread","updated_at":"2026-07-19T09:00:02Z",` +
		`"initial_project_snapshot":{"worktree_snapshots":[{"worktree_path":"/Users/x/code/marketplace-price-tracker"}]},` +
		`"messages":[{"User":{"id":"u1","content":[{"Text":"first question"}]}}]}`
	body := zedZstdHex(t, withSnapshot)
	sql := zedSchemaOriginal + `
insert into threads (id,summary,updated_at,data_type,data) values
 ('snap','snap','2026-07-19T09:00:02+00:00','zstd',x'` + body + `');`
	sessions, err := ParseZedDB(zedTestDB(t, sql))
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %#v, want one", sessions)
	}
	if got := sessions[0].Project; got != "marketplace-price-tracker" {
		t.Errorf("project = %q, want it read off the document's worktree path", got)
	}
}

func TestParseZedDBOnAMissingOrEmptyStoreReturnsNothing(t *testing.T) {
	zedHome(t)
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.db")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "absent.db"), empty} {
		sessions, err := ParseZedDB(path)
		if err != nil || sessions != nil {
			t.Fatalf("%s: sessions = %#v, err = %v", filepath.Base(path), sessions, err)
		}
		// The sqlite3 CLI creates a database it is asked to open; nothing here may.
		if path != empty {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("%s was created by the parser", path)
			}
		}
	}
	// LoadZed goes through the same guard and reports nothing rather than failing.
	t.Setenv("DEJA_ZED_DB", filepath.Join(dir, "absent.db"))
	if got := LoadZed(); got != nil {
		t.Fatalf("LoadZed = %#v, want nil", got)
	}
}

func TestZedMessageIgnoresControlRecordsAndNonTextBlocks(t *testing.T) {
	cases := []struct {
		name, raw, role, text string
	}{
		{"user text", `{"User":{"id":"u","content":[{"Text":"hello"}]}}`, "user", "hello"},
		{"user text joined", `{"User":{"id":"u","content":[{"Text":"one"},{"Text":"two"}]}}`, "user", "one\ntwo"},
		{"mention keeps the inlined content", `{"User":{"id":"u","content":[{"Mention":{"uri":"file:///a.go","content":"package a"}}]}}`, "user", "package a"},
		{"image is skipped", `{"User":{"id":"u","content":[{"Image":{"source":"..."}},{"Text":"look"}]}}`, "user", "look"},
		{"agent text", `{"Agent":{"content":[{"Text":"answer"}],"tool_results":{}}}`, "assistant", "answer"},
		{"thinking is skipped", `{"Agent":{"content":[{"Thinking":{"text":"private","signature":null}},{"Text":"answer"}],"tool_results":{}}}`, "assistant", "answer"},
		{"redacted thinking is skipped", `{"Agent":{"content":[{"RedactedThinking":"opaque"},{"Text":"answer"}],"tool_results":{}}}`, "assistant", "answer"},
		{"tool use is skipped", `{"Agent":{"content":[{"ToolUse":{"name":"grep","input":{}}}],"tool_results":{}}}`, "assistant", ""},
		{"resume marker is not speech", `"Resume"`, "", ""},
		{"legacy text segments", `{"id":1,"role":"assistant","segments":[{"type":"text","text":"old"}]}`, "assistant", "old"},
		{"legacy thinking segment is skipped", `{"id":1,"role":"assistant","segments":[{"type":"thinking","text":"private"},{"type":"text","text":"old"}]}`, "assistant", "old"},
		{"legacy system role is still reported", `{"id":1,"role":"system","segments":[{"type":"text","text":"preamble"}]}`, "system", "preamble"},
		{"content is not an array", `{"User":{"id":"u","content":"nope"}}`, "user", ""},
		{"mention without content", `{"User":{"id":"u","content":[{"Mention":{"uri":"file:///a.go"}}]}}`, "user", ""},
		{"legacy role is not a string", `{"role":42,"segments":[{"type":"text","text":"x"}]}`, "", ""},
		{"legacy without segments", `{"role":"user"}`, "", ""},
		{"legacy segments are not an array", `{"role":"user","segments":"nope"}`, "", ""},
		{"legacy empty segments", `{"role":"user","segments":[]}`, "user", ""},
		{"unknown shape", `{"Something":{"content":[]}}`, "", ""},
		{"not an object", `42`, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			role, text := zedMessage([]byte(tc.raw))
			if role != tc.role || text != tc.text {
				t.Fatalf("zedMessage = %q/%q, want %q/%q", role, text, tc.role, tc.text)
			}
		})
	}
}

// A system-role message is reported by zedMessage and dropped by the session
// builder, which is where every harness's authored-preamble rule lives.
func TestParseZedDBDropsHarnessAuthoredRoles(t *testing.T) {
	zedHome(t)
	body := `{"version":"0.2.0","summary":"s","updated_at":"2026-07-19T09:00:02Z","messages":[` +
		`{"id":1,"role":"system","segments":[{"type":"text","text":"preamble"}]},` +
		`{"id":2,"role":"user","segments":[{"type":"text","text":"real turn"}]}]}`
	sql := zedSchema + `
insert into threads (id,summary,updated_at,data_type,data,folder_paths,created_at) values
 ('roles','roles','2026-07-19T09:00:02+00:00','json','` + body + `','/w/p','2026-07-19T09:00:00+00:00');`
	sessions, err := ParseZedDB(zedTestDB(t, sql))
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || len(sessions[0].Messages) != 1 || sessions[0].Messages[0].Text != "real turn" {
		t.Fatalf("sessions = %#v, want only the user turn", sessions)
	}
}

func TestZedFolderTakesTheFirstPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"/a/one", "/a/one"},
		{"/a/one\n/b/two", "/a/one"},
		{"\n\n/b/two", "/b/two"},
		{"   \n", ""},
	}
	for _, tc := range cases {
		if got := zedFolder(tc.in); got != tc.want {
			t.Fatalf("zedFolder(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestZedTimeAcceptsTheFormsAStoreHolds(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"2026-07-19T09:00:02+00:00", "2026-07-19T09:00:02Z"},
		{"2026-07-19T09:00:02.5Z", "2026-07-19T09:00:02Z"},
		{"2026-07-19 09:00:02", "2026-07-19T09:00:02Z"},
		{"not a time", ""},
	}
	for _, tc := range cases {
		got := zedTime(tc.in)
		if tc.want == "" {
			if !got.IsZero() {
				t.Fatalf("zedTime(%q) = %v, want zero", tc.in, got)
			}
			continue
		}
		if got.Format(time.RFC3339) != tc.want {
			t.Fatalf("zedTime(%q) = %v, want %s", tc.in, got, tc.want)
		}
	}
}

func TestZedRootFollowsZedsDataDirAndItsOverride(t *testing.T) {
	home := zedHome(t)
	// The override wins everywhere, which is what the fixtures and the doctor
	// store check rely on.
	if got := ZedRoot(); got != filepath.Join(home, "zed") {
		t.Fatalf("DEJA_ZED_ROOT ignored: %q", got)
	}
	if got := ZedDB(); got != filepath.Join(home, "zed", "threads", "threads.db") {
		t.Fatalf("ZedDB = %q", got)
	}
	t.Setenv("DEJA_ZED_DB", filepath.Join(home, "elsewhere.db"))
	if got := ZedDB(); got != filepath.Join(home, "elsewhere.db") {
		t.Fatalf("DEJA_ZED_DB ignored: %q", got)
	}

	// Without the override the default is Zed's own data dir, which is not the
	// config dir this parser was first reported against.
	t.Setenv("DEJA_ZED_ROOT", "")
	got := ZedRoot()
	var want string
	switch runtime.GOOS {
	case "darwin":
		want = filepath.Join(home, "Library", "Application Support", "Zed")
	case "windows":
		want = filepath.Join(home, "AppData", "Local", "Zed")
	default:
		want = filepath.Join(home, ".local", "share", "zed")
	}
	if got != want {
		t.Fatalf("ZedRoot = %q, want %q", got, want)
	}
	if strings.Contains(got, filepath.Join(".config", "zed")) {
		t.Fatalf("ZedRoot points at the config dir: %q", got)
	}
}

// Every platform layout is asserted on every platform. The macOS and Windows
// paths are the two a contributor is least able to check by hand, and a
// Linux-only CI job would never reach them through runtime.GOOS.
func TestZedDataDirCoversEveryPlatformLayout(t *testing.T) {
	home := zedHome(t)
	t.Run("darwin", func(t *testing.T) {
		want := filepath.Join(home, "Library", "Application Support", "Zed")
		if got := zedDataDir("darwin"); got != want {
			t.Fatalf("zedDataDir(darwin) = %q, want %q", got, want)
		}
	})
	t.Run("windows local app data", func(t *testing.T) {
		want := filepath.Join(home, "AppData", "Local", "Zed")
		if got := zedDataDir("windows"); got != want {
			t.Fatalf("zedDataDir(windows) = %q, want %q", got, want)
		}
	})
	t.Run("windows without LOCALAPPDATA", func(t *testing.T) {
		t.Setenv("LOCALAPPDATA", "")
		want := filepath.Join(home, "AppData", "Local", "Zed")
		if got := zedDataDir("windows"); got != want {
			t.Fatalf("zedDataDir(windows) = %q, want %q", got, want)
		}
	})
	for _, goos := range []string{"linux", "freebsd"} {
		t.Run(goos, func(t *testing.T) {
			want := filepath.Join(home, ".local", "share", "zed")
			if got := zedDataDir(goos); got != want {
				t.Fatalf("zedDataDir(%s) = %q, want %q", goos, got, want)
			}
		})
	}
	t.Run("XDG_DATA_HOME", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg"))
		if got := zedDataDir("linux"); got != filepath.Join(home, "xdg", "zed") {
			t.Fatalf("XDG_DATA_HOME ignored: %q", got)
		}
		t.Setenv("FLATPAK_XDG_DATA_HOME", filepath.Join(home, "flatpak"))
		if got := zedDataDir("linux"); got != filepath.Join(home, "flatpak", "zed") {
			t.Fatalf("FLATPAK_XDG_DATA_HOME must win: %q", got)
		}
	})
}

// A store whose table is gone is not a store with no threads. Both projections
// fail, and the error has to say the schema may have moved — reporting it as an
// empty harness is what makes a whole history disappear while doctor stays green.
func TestParseZedDBReportsAStoreItCannotQuery(t *testing.T) {
	zedHome(t)
	db := zedTestDB(t, `create table not_threads (id text);`)
	_, err := ParseZedDB(db)
	if err == nil {
		t.Fatal("a store with no threads table must be an error, not an empty read")
	}
	if !strings.Contains(err.Error(), "schema") {
		t.Fatalf("error = %v, want it to name the schema", err)
	}
	// LoadZed swallows the error by contract — it is the cold-load path — but it
	// must not panic or invent sessions.
	t.Setenv("DEJA_ZED_DB", db)
	if got := LoadZed(); got != nil {
		t.Fatalf("LoadZed = %#v, want nil", got)
	}
}

// The store is read through a tool deja does not ship, so the shapes it can
// hand back are not all valid JSON arrays of rows. A stub on PATH is the only
// way to reach those branches; nothing else can make the real sqlite3 lie.
func TestParseZedDBSurvivesASqlite3ThatDoesNotAnswerInRows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	zedHome(t)
	real := zedTestDB(t, zedSchema)
	const oneRow = `[{"id":"x","summary":"s","updated_at":"","data_type":"json","data":""}`
	cases := []struct {
		name, stdout string
		exit         int
		wantErr      bool
	}{
		{name: "not json at all", stdout: "not json\n", wantErr: true},
		{name: "a json object rather than an array", stdout: `{"id":"x"}`, wantErr: true},
		// Stopping mid-row is the same cut-short answer as stopping between
		// rows, and with a clean exit it is read the same way: whatever rows
		// arrived whole. Go 1.25 told the two apart by which step failed, Go
		// 1.27 does not, and the exit status was always the better signal.
		{name: "an array that stops mid-row", stdout: `[{"id":"x",`, wantErr: false},
		// A truncated array that still exits 0 is read as the rows it did
		// deliver: the exit status is what says the query was cut short, and a
		// real sqlite3 that dies mid-stream returns one. Asserting an error
		// here would be asserting something the tool cannot tell us.
		{name: "a truncated array that exited cleanly", stdout: oneRow, wantErr: false},
		{name: "a truncated array whose sqlite3 failed", stdout: oneRow, exit: 1, wantErr: true},
		{name: "no output and a failed exit", stdout: "", exit: 1, wantErr: true},
		{name: "no output and a clean exit", stdout: "", exit: 0, wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' %s\nexit %d\n", shellQuote(tc.stdout), tc.exit)
			if err := os.WriteFile(filepath.Join(bin, "sqlite3"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			sessions, err := ParseZedDB(real)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			// Nothing here decodes to a thread, so no case may invent a session.
			if len(sessions) != 0 {
				t.Fatalf("sessions = %#v, want none", sessions)
			}
		})
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// A thread document with no title of its own falls back to the row's summary
// column, which is the copy Zed keeps outside the compressed body.
func TestParseZedDBFallsBackToTheRowSummaryForATitle(t *testing.T) {
	zedHome(t)
	body := `{"version":"0.3.0","updated_at":"2026-07-19T09:00:02Z","messages":[` +
		`{"User":{"id":"u","content":[{"Text":"untitled turn"}]}}]}`
	sql := zedSchema + `
insert into threads (id,summary,updated_at,data_type,data,folder_paths,created_at) values
 ('untitled','the row summary','2026-07-19T09:00:02+00:00','json','` + body + `','/w/p','2026-07-19T09:00:00+00:00');`
	sessions, err := ParseZedDB(zedTestDB(t, sql))
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Title != "the row summary" {
		t.Fatalf("sessions = %#v, want the row's summary as the title", sessions)
	}
}

func TestZedBodyRejectsWhatItCannotDecode(t *testing.T) {
	if _, err := zedBody("json", "zz"); err == nil {
		t.Fatal("non-hex data must be an error")
	}
	if _, err := zedBody("brotli", "00"); err == nil {
		t.Fatal("an unknown data_type must be an error rather than a guess")
	}
	body, err := zedBody("", hex.EncodeToString([]byte(`{"title":"t"}`)))
	if err != nil || string(body) != `{"title":"t"}` {
		t.Fatalf("empty data_type = %q, %v; want the bytes unchanged", body, err)
	}
	if _, err := zstdDecode(nil); err == nil {
		t.Fatal("an empty frame must be an error")
	}
}

// The registry fixture's zstd row is a real frame, so its bytes are opaque in
// review. The SQL states the plaintext it decompresses to; this keeps the two
// from drifting apart, which is the failure a hex literal invites.
func TestRegistryFixtureZstdBlobMatchesItsDocumentedPlaintext(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd not installed")
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "registry", "zed", "zed.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sql := string(b)

	var documented string
	for _, line := range strings.Split(sql, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "-- {"); ok {
			documented = "{" + rest
			break
		}
	}
	if documented == "" {
		t.Fatal("the fixture no longer documents the blob's plaintext")
	}

	start := strings.Index(sql, "x'")
	if start < 0 {
		t.Fatal("no blob literal in the fixture")
	}
	end := strings.Index(sql[start+2:], "'")
	if end < 0 {
		t.Fatal("unterminated blob literal in the fixture")
	}
	got, err := zedBody("zstd", sql[start+2:start+2+end])
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(documented) {
		t.Fatalf("blob decompresses to\n%s\nbut the fixture documents\n%s", got, documented)
	}
}

func TestZstdAvailableReportsThePath(t *testing.T) {
	// The gate is a PATH lookup, so an empty PATH is the one case that must
	// report false on every machine, installed or not.
	t.Setenv("PATH", "")
	if ZstdAvailable() {
		t.Fatal("ZstdAvailable must be false with an empty PATH")
	}
	if SQLite3Available() {
		t.Fatal("SQLite3Available must be false with an empty PATH")
	}
}
