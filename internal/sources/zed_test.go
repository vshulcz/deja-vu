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

// A thread that reads the same file twice and runs the same command twice —
// ordinary work, and what a real Zed thread does all day. Every message
// carried the thread's start, so the ingest's duplicate check saw one turn
// where there were two: 1385 of 6767 messages from the 30 real threads on
// this machine never reached the index (#3333).
const zedRepeatBody = `{"version":"0.3.0","title":"repeated work","updated_at":"2026-07-19T09:00:02Z","messages":[
{"User":{"id":"u1","content":[{"Text":"the retry queue stalls on staging"}]}},
{"Agent":{"content":[
 {"ToolUse":{"id":"c1","name":"read_file","input":{"path":"/w/app/queue/retry.go","start_line":1,"end_line":80}}},
 {"ToolUse":{"id":"c2","name":"terminal","input":{"command":"go test ./queue/...","cd":"/w/app"}}}
],"tool_results":{},"reasoning_details":null}},
{"Agent":{"content":[
 {"ToolUse":{"id":"c3","name":"read_file","input":{"path":"/w/app/queue/retry.go","start_line":1,"end_line":80}}},
 {"ToolUse":{"id":"c4","name":"terminal","input":{"command":"go test ./queue/...","cd":"/w/app"}}}
],"tool_results":{},"reasoning_details":null}}
]}`

func TestZedTellsTwoIdenticalTurnsApartByTheirPlace(t *testing.T) {
	zedHome(t)
	hex := zedZstdHex(t, zedRepeatBody)
	sql := fmt.Sprintf("%s\ninsert into threads (id, summary, updated_at, data_type, data, folder_paths, created_at) values ('r1', 'repeated work', '2026-07-19T09:00:02Z', 'zstd', x'%s', '[\"/w/app\"]', '2026-07-19T08:00:00Z');", zedSchema, hex)
	db := zedTestDB(t, sql)
	if !ZstdAvailable() {
		t.Skip("zstd not installed")
	}
	ss, err := ParseZedDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	seen := map[string]int{}
	for _, m := range ss[0].Messages {
		seen[m.Role+"\x00"+m.Text+"\x00"+m.Time.Format("2006-01-02T15:04:05.000000000Z07:00")]++
	}
	for k, n := range seen {
		if n > 1 {
			t.Errorf("%d messages share role, text and stamp — the ingest stores one of them: %q", n, k)
		}
	}
	// The stamps stay inside the thread's own span and keep the array's order.
	prev := ss[0].Messages[0].Time
	for _, m := range ss[0].Messages[1:] {
		if m.Time.Before(prev) {
			t.Errorf("message stamped before the one ahead of it: %v < %v", m.Time, prev)
		}
		prev = m.Time
	}
	if last := ss[0].Messages[len(ss[0].Messages)-1].Time; last.After(ss[0].Updated) {
		t.Errorf("last message stamped %v, after the thread's own update %v", last, ss[0].Updated)
	}
}

// Where Zed keeps settings.json differs by platform, and the installer writes
// to whatever this returns. A wrong answer here does not fail loudly: install
// reports success and Zed reads a file deja never touched.
func TestZedConfigDirPerPlatform(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Pointed away from the profile on purpose: a roaming profile is not
	// always under the home directory, and reading APPDATA is the only way to
	// find it. Same answer either way proves nothing.
	roaming := filepath.Join(t.TempDir(), "Roaming")
	t.Setenv("APPDATA", roaming)
	if got, want := zedConfigDir("windows"), filepath.Join(roaming, "Zed"); got != want {
		t.Errorf("windows config dir = %q, want %q", got, want)
	}
	// A windows without APPDATA set still has a profile to fall back on.
	t.Setenv("APPDATA", "")
	if got, want := zedConfigDir("windows"), filepath.Join(home, "AppData", "Roaming", "Zed"); got != want {
		t.Errorf("windows config dir without APPDATA = %q, want %q", got, want)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	if got, want := zedConfigDir("linux"), filepath.Join(home, ".config", "zed"); got != want {
		t.Errorf("posix config dir = %q, want %q", got, want)
	}
	xdg := filepath.Join(home, "elsewhere")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, want := zedConfigDir("linux"), filepath.Join(xdg, "zed"); got != want {
		t.Errorf("XDG_CONFIG_HOME ignored: %q, want %q", got, want)
	}
}

// The override is what lets anyone whose layout differs point deja at the real
// file, and it is how this is tested without a Zed install.
func TestZedSettingsPathHonoursTheOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("DEJA_ZED_CONFIG", want)
	if got := ZedSettingsPath(); got != want {
		t.Errorf("ZedSettingsPath = %q, want %q", got, want)
	}
	t.Setenv("DEJA_ZED_CONFIG", "")
	if got := ZedSettingsPath(); got == want {
		t.Errorf("the override outlived its variable: %q", got)
	}
}

// Zed records both sides of an edit, but on the result rather than on the call:
// the `edit_file` input is a path, a mode and a sentence of intent, and the
// change itself comes back under `output` as a unified diff beside the whole
// file before and after. Counted on a real store, 589 of 684 edit_file results
// carry it and not one call did — so 29 of 30 zed sessions had no recorded edit
// at all (#595). Shape from a real thread.
func TestZedEditsComeFromTheToolResult(t *testing.T) {
	const removed = "cfg.MaxConns = 10 // the number from before this change"
	const added = "cfg.MaxConns = int32(size) // default_pool_size, from the ini"
	msg := `{"Agent":{"content":[{"ToolUse":{"id":"call_1","name":"edit_file","input":{"path":"/w/internal/pool/pool.go","mode":"edit","display_description":"Raise the pool size"}}}],
	 "tool_results":{"call_1":{"tool_use_id":"call_1","tool_name":"edit_file","is_error":false,"content":{"Text":"Edited pool.go"},
	  "output":{"input_path":"/w/internal/pool/pool.go","diff":"@@ -18,7 +18,7 @@\n func open(size int) {\n-` + removed + `\n+` + added + `\n }\n","old_text":"whole file before","new_text":"whole file after"}}}}}`
	at := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)

	var replaced, wrote []string
	for _, m := range zedWork([]byte(msg), at) {
		switch m.Role {
		case RoleEdit:
			replaced = append(replaced, m.Text)
		case RoleWrote:
			wrote = append(wrote, m.Text)
		}
	}
	if len(replaced) != 1 {
		t.Fatalf("replaced spans = %q, want the one the diff removed", replaced)
	}
	path, span, _ := strings.Cut(replaced[0], "\n")
	if path != "/w/internal/pool/pool.go" {
		t.Errorf("the span is filed under %q", path)
	}
	if !strings.Contains(span, removed) {
		t.Errorf("the span is not what the diff removed: %q", span)
	}
	// The whole-file texts beside the diff are 67 KB at their largest on a real
	// store, and they are not what stopped existing at that line.
	if strings.Contains(span, "whole file before") {
		t.Error("the whole file was stored as the replaced span")
	}

	if len(wrote) != 1 {
		t.Fatalf("written records = %q, want one for the diff's added lines", wrote)
	}
	h, ok := HashWrittenLine(added)
	if !ok {
		t.Fatal("the added line is not evidence")
	}
	if _, has := WroteRecordHas(wrote[0], h); !has {
		t.Errorf("the written record does not hold the added line: %q", wrote[0])
	}
	// And not the removed one: that is the other rule's evidence.
	if old, _ := HashWrittenLine(removed); func() bool { _, has := WroteRecordHas(wrote[0], old); return has }() {
		t.Error("a removed line was hashed as written")
	}
}

func TestZedEditsStopWhenSwitchedOff(t *testing.T) {
	msg := `{"Agent":{"content":[],"tool_results":{"call_1":{"tool_use_id":"call_1","tool_name":"edit_file",
	  "output":{"input_path":"/w/pool.go","diff":"@@ -1,1 +1,1 @@\n-cfg.MaxConns = 10 // the number before this change\n+cfg.MaxConns = int32(size) // from the ini\n"}}}}}`
	at := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	t.Setenv("DEJA_INDEX_EDITS", "0")
	t.Setenv("DEJA_INDEX_WRITES", "0")
	for _, m := range zedWork([]byte(msg), at) {
		if m.Role == RoleEdit || m.Role == RoleWrote {
			t.Errorf("a %s record survived with both switches off: %q", m.Role, m.Text)
		}
	}
}

// A result without the diff is not an edit anyone can attribute: 95 of the 684
// on the real store carry no output object at all, and inventing a span from
// the call's own input would file a sentence of intent as the file's old text.
func TestZedSkipsAnEditItCannotRead(t *testing.T) {
	at := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	for name, msg := range map[string]string{
		"the output is a plain string": `{"Agent":{"content":[{"ToolUse":{"id":"call_1","name":"edit_file","input":{"path":"/w/pool.go","mode":"edit","display_description":"Raise the pool size"}}}],
	 "tool_results":{"call_1":{"tool_use_id":"call_1","tool_name":"edit_file","output":"Edited pool.go"}}}}`,
		// Zed writes this one itself: the edit was refused or produced nothing,
		// and the result comes back with the path and an empty diff. A record
		// built from it says a session edited a file and gives nothing to match.
		"the diff is empty": `{"Agent":{"content":[],
	 "tool_results":{"call_1":{"tool_use_id":"call_1","tool_name":"edit_file","output":{"input_path":"/w/pool.go","diff":"","old_text":"whole file","new_text":"whole file"}}}}}`,
	} {
		for _, m := range zedWork([]byte(msg), at) {
			if m.Role == RoleEdit || m.Role == RoleWrote {
				t.Errorf("%s: a %s record was invented: %q", name, m.Role, m.Text)
			}
		}
	}
}

// Only an edit_file result. The filter is on the tool name rather than on the
// shape of the output, so a tool that grows a `diff` field later — zed's own
// `git_state` snapshot already carries one, under another key — does not start
// producing edit records nobody checked.
func TestZedReadsEditsOnlyFromTheEditTool(t *testing.T) {
	msg := `{"Agent":{"content":[],
	 "tool_results":{"call_1":{"tool_use_id":"call_1","tool_name":"terminal",
	  "output":{"input_path":"/w/pool.go","diff":"@@ -1,1 +1,1 @@\n-cfg.MaxConns = 10 // the number before this change\n+cfg.MaxConns = int32(size) // from the ini\n"}}}}}`
	at := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	for _, m := range zedWork([]byte(msg), at) {
		if m.Role == RoleEdit || m.Role == RoleWrote {
			t.Errorf("a %s record came from a %s result: %q", m.Role, "terminal", m.Text)
		}
	}
}

// Zed 1.22's `write_file` creates or overwrites a whole file. Its input is
// {path, content} and its result the same EditSessionOutput edit_file returns,
// so a file the agent created reaches files, edits and blame the same way
// (#4339). Shape from crates/agent/src/tools/write_file_tool.rs, v1.22.0.
func TestZedReadsAFileTheAgentWrote(t *testing.T) {
	const added = "func Retry(n int, f func() error) error { // three tries, then give up"
	msg := `{"Agent":{"content":[{"ToolUse":{"id":"call_1","name":"write_file","input":{"path":"proj/retry.go","content":"package main\n` + added + `\n"}}}],
	 "tool_results":{"call_1":{"tool_use_id":"call_1","tool_name":"write_file","is_error":false,"content":{"Text":"ok"},
	  "output":{"input_path":"proj/retry.go","diff":"@@ -0,0 +1,2 @@\n+package main\n+` + added + `\n","old_text":"","new_text":"package main\n` + added + `\n"}}}}}`
	at := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)

	var files, wrote []string
	for _, m := range zedWork([]byte(msg), at) {
		switch m.Role {
		case RoleFiles:
			files = append(files, m.Text)
		case RoleWrote:
			wrote = append(wrote, m.Text)
		}
	}
	if len(files) != 1 || files[0] != "proj/retry.go" {
		t.Errorf("files records = %q, want the written file", files)
	}
	if len(wrote) != 1 {
		t.Fatalf("written records = %q, want one for the new file", wrote)
	}
	h, ok := HashWrittenLine(added)
	if !ok {
		t.Fatal("the added line is not evidence")
	}
	if _, has := WroteRecordHas(wrote[0], h); !has {
		t.Errorf("the written record does not hold the file's line: %q", wrote[0])
	}
}

// @-mentioning another thread inlines Zed's own generated summary of it into
// the message. deja indexed that as the person's words — 48798 characters
// across the 12 mentions on this machine's store, each standing above the
// sentence someone actually typed (#3336). A selection mention is the other
// kind and is theirs: code they attached on purpose.
const zedMentionBody = `{"version":"0.3.0","title":"mention thread","updated_at":"2026-02-02T12:35:02Z","messages":[
{"User":{"id":"u1","content":[
 {"Mention":{"uri":{"Thread":{"id":"a6c02f18-ab43-4848-97a4-2a84f53ab927","name":"Deep Code Review Advertising Feature Branch"}},"content":"# Conversation Summary: Deep Code Review\n## 1. Overview\n- The user requested a deep review of the advertising branch."}},
 {"Text":"\ncarry on with the errors listed in KNOWN_ISSUES.md"}]}},
{"User":{"id":"u2","content":[
 {"Mention":{"uri":{"Selection":{"abs_path":"/w/app/pool.go","line_range":{"start":12,"end":18}}},"content":"item := pool.Get()\npool.Put(item)"}},
 {"Text":"\nwhy does this leak"}]}}
]}`

func TestZedDoesNotTakeTheSummaryOfAMentionedThreadAsSpeech(t *testing.T) {
	zedHome(t)
	hex := zedZstdHex(t, zedMentionBody)
	sql := fmt.Sprintf("%s\ninsert into threads (id, summary, updated_at, data_type, data, folder_paths, created_at) values ('m1', 'mention thread', '2026-02-02T12:35:02Z', 'zstd', x'%s', '[\"/w/app\"]', '2026-02-02T12:35:00Z');", zedSchema, hex)
	db := zedTestDB(t, sql)
	if !ZstdAvailable() {
		t.Skip("zstd not installed")
	}
	ss, err := ParseZedDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	var users []string
	for _, m := range ss[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	if len(users) != 2 {
		t.Fatalf("user turns = %d, want 2: %q", len(users), users)
	}
	if strings.Contains(users[0], "Conversation Summary") {
		t.Errorf("the editor's summary of another thread is indexed as the person's words: %q", users[0])
	}
	if !strings.Contains(users[0], "carry on with the errors listed in KNOWN_ISSUES.md") {
		t.Errorf("the person's own sentence went with it: %q", users[0])
	}
	if !strings.Contains(users[1], "pool.Put(item)") {
		t.Errorf("a selection the person attached is theirs and must stay: %q", users[1])
	}
}

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
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
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
	cmd := exec.CommandContext(t.Context(), "zstd", "-q", "-3", "-c")
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
		if got.Harness != "zed" || got.Project != "workspace/registry-demo" || got.Path != db {
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
			// The thread has no clock of its own, so the place in the array
			// is one: the start plus a millisecond per record, which is what
			// tells two identical turns apart (#3333).
			if want := got.Started.Add(time.Duration(j) * time.Millisecond); !got.Messages[j].Time.Equal(want) {
				t.Fatalf("session %d message %d time = %v, want %v", i, j, got.Messages[j].Time, want)
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
	if newest, err := ParseZedNewest(zedTestDB(t, sql)); err != nil || len(newest) != 1 {
		t.Fatalf("ParseZedNewest = %v, %#v", err, newest)
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
	ids := make([]string, 0, len(sessions))
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
		`"initial_project_snapshot":{"worktree_snapshots":[{"worktree_path":"/Users/x/code/catalog-service"}]},` +
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
	if got := sessions[0].Project; got != "code/catalog-service" {
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
	resolved := zedTestDB(t, zedSchema)
	const oneRow = `{"id":"x","summary":"s","updated_at":"","data_type":"json","data":""}`
	cases := []struct {
		name, stdout string
		exit         int
		wantErr      bool
	}{
		{name: "not json at all", stdout: "not json\n", wantErr: true},
		// The rows arrive as bare objects, one per line — an array is the
		// shape the shell's own -json mode used to produce and no longer a
		// row the reader can take.
		{name: "a json array rather than objects", stdout: `[{"id":"x"}]`, wantErr: true},
		// Stopping mid-object is a cut-short answer, and with a clean exit it
		// is read the same way as stopping between objects: whatever rows
		// arrived whole. Go 1.25 told the two apart by which step failed, Go
		// 1.27 does not, and the exit status was always the better signal.
		{name: "a stream that stops mid-object", stdout: `{"id":"x",`, wantErr: false},
		// A truncated stream that still exits 0 is read as the rows it did
		// deliver: the exit status is what says the query was cut short, and a
		// real sqlite3 that dies mid-stream returns one. Asserting an error
		// here would be asserting something the tool cannot tell us.
		{name: "a whole row and then nothing, exited cleanly", stdout: oneRow, wantErr: false},
		{name: "a whole row whose sqlite3 failed", stdout: oneRow, exit: 1, wantErr: true},
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
			sessions, err := ParseZedDB(resolved)
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
	for line := range strings.SplitSeq(sql, "\n") {
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

// Zed keeps what a tool returned on the agent message, tool_results keyed by
// tool_use_id; the reader took the ToolUse blocks and not the results, and
// the comment said Zed stores none (#3291). Shape from a real thread.
func TestZedToolResultsAreIndexedAsToolOutput(t *testing.T) {
	msg := `{"Agent":{"content":[{"Text":"Running the tests."},{"ToolUse":{"id":"call_1","name":"terminal","input":{"command":"go test ./...","cd":"."}}}],
	 "tool_results":{"call_1":{"tool_use_id":"call_1","tool_name":"terminal","is_error":false,"content":{"Text":"Command \"go test ./...\" failed with exit code 1.\n\n# app/pkg\npkg/parser.go:42:9: undefined: frobnicateWidget\nFAIL"},"output":"…"},
	  "call_0":{"tool_use_id":"call_0","tool_name":"list_directory","is_error":true,"content":{"Text":"Path .} not found in project"},"output":"Path .} not found in project"}}}}`
	at := time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
	var toolOut []string
	for _, m := range zedWork([]byte(msg), at) {
		if m.Role == RoleToolOutput {
			toolOut = append(toolOut, m.Text)
		}
	}
	if len(toolOut) != 2 {
		t.Fatalf("tool output = %q, want both results", toolOut)
	}
	joined := strings.Join(toolOut, "\n")
	for _, want := range []string{"failed with exit code 1", "undefined: frobnicateWidget", "not found in project"} {
		if !strings.Contains(joined, want) {
			t.Errorf("tool output lacks %q:\n%s", want, joined)
		}
	}
}

// A thread that talks and works: two commands, several files, and the calls
// that name neither. Shaped after a real store — Zed keeps tool calls in the
// same content array as the prose, tagged ToolUse.
const zedWorkBody = `{"version":"0.3.0","title":"worked thread","updated_at":"2026-07-19T09:00:02Z","messages":[
{"User":{"id":"u1","content":[{"Text":"the retry queue stalls on staging"}]}},
{"Agent":{"content":[
 {"Text":"Let me look."},
 {"ToolUse":{"id":"c1","name":"read_file","input":{"path":"/w/app/queue/retry.go","start_line":1,"end_line":80}}},
 {"ToolUse":{"id":"c2","name":"terminal","input":{"command":"go test ./queue/...","cd":"/w/app"}}},
 {"ToolUse":{"id":"c3","name":"grep","input":{"regex":"backoff","include_pattern":"**/*.go"}}}
],"tool_results":{},"reasoning_details":null}},
{"Agent":{"content":[
 {"ToolUse":{"id":"c4","name":"edit_file","input":{"path":"/w/app/queue/retry.go","display_description":"spread the wakeups","mode":"edit"}}},
 {"ToolUse":{"id":"c5","name":"read_file","input":{"path":"/w/app/queue/retry.go","start_line":1,"end_line":80}}},
 {"ToolUse":{"id":"c6","name":"move_path","input":{"source_path":"/w/app/old.go","destination_path":"/w/app/queue/jitter.go"}}},
 {"ToolUse":{"id":"c7","name":"terminal","input":{"command":"go build ./...","cd":"/w/app"}}},
 {"ToolUse":{"id":"c8","name":"copy_path","input":{"source_path":"/w/app/queue/retry.go","destination_path":"/w/app/queue/retry_backup.go"}}},
 {"ToolUse":{"id":"c9","name":"save_file","input":{"paths":["/w/app/queue/jitter.go","/w/app/README.md"]}}},
 {"ToolUse":{"id":"c10","name":"diagnostics","input":{"path":"/w/app/queue/lint.go"}}},
 {"ToolUse":{"id":"c11","name":"list_directory","input":{"path":"/w/app/vendor"}}},
 {"Text":"We spread the wakeups over a second."}
],"tool_results":{},"reasoning_details":null}}
]}`

func zedWorkedStore(t *testing.T) []string {
	t.Helper()
	zedHome(t)
	hex := zedZstdHex(t, zedWorkBody)
	sql := fmt.Sprintf("%s\ninsert into threads (id, summary, updated_at, data_type, data, folder_paths, created_at) values ('w1', 'worked thread', '2026-07-19T09:00:02Z', 'zstd', x'%s', '[\"/w/app\"]', '2026-07-19T08:00:00Z');", zedSchema, hex)
	db := zedTestDB(t, sql)
	if !ZstdAvailable() {
		t.Skip("zstd not installed")
	}
	sessions, err := ParseZedDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	roles := make([]string, 0, len(sessions[0].Messages))
	for _, m := range sessions[0].Messages {
		roles = append(roles, m.Role+"\t"+m.Text)
	}
	return roles
}

// Zed keeps what the agent did in the same place as what it said, and deja read
// only the saying. Measured on a real 30-thread store: 797 ToolUse blocks
// against 69 of agent prose, so `deja how`, `deja blame`, the files-touched
// line and the worked-on-most ranking were all blind on a Zed history.
func TestZedIndexesTheCommandsAThreadRan(t *testing.T) {
	rows := zedWorkedStore(t)

	var commands []string
	for _, r := range rows {
		role, text, _ := strings.Cut(r, "\t")
		if role == RoleCommand {
			commands = append(commands, text)
		}
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %v, want the two terminal calls", commands)
	}
	for i, want := range []string{"$ go test ./queue/...", "$ go build ./..."} {
		if commands[i] != want {
			t.Errorf("command %d = %q, want %q", i, commands[i], want)
		}
	}
	// The prose is still there: the work is added beside it, not instead.
	joined := strings.Join(rows, "\n")
	for _, said := range []string{"the retry queue stalls on staging", "We spread the wakeups over a second."} {
		if !strings.Contains(joined, said) {
			t.Errorf("the conversation lost %q:\n%s", said, joined)
		}
	}
}

func TestZedIndexesTheFilesAThreadTouched(t *testing.T) {
	rows := zedWorkedStore(t)

	var files []string
	for _, r := range rows {
		role, text, _ := strings.Cut(r, "\t")
		if role == RoleFiles {
			files = append(files, strings.Split(text, "\n")...)
		}
	}
	want := []string{
		"/w/app/queue/retry.go",
		"/w/app/old.go",
		"/w/app/queue/jitter.go",
		// A copy names both ends, the same as a move.
		"/w/app/queue/retry_backup.go",
		// save_file and restore_file_from_disk act on a selection, so their
		// paths arrive as a list rather than one string.
		"/w/app/README.md",
		// Asking for a file's diagnostics is the agent looking at that file,
		// the same as reading it.
		"/w/app/queue/lint.go",
	}
	for _, w := range want {
		found := false
		for _, f := range files {
			if f == w {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is missing from %v", w, files)
		}
	}
	// A path is recorded once per message however often the thread reads it:
	// the record is which files the work touched, not how many reads it took.
	for _, r := range rows {
		role, text, _ := strings.Cut(r, "\t")
		if role != RoleFiles {
			continue
		}
		seen := map[string]bool{}
		for p := range strings.SplitSeq(text, "\n") {
			if seen[p] {
				t.Errorf("%s is listed twice in one record: %q", p, text)
			}
			seen[p] = true
		}
	}
}

// A search names a pattern and a scope, not a file the session worked on, and
// a path inside a shell command is guesswork — the same line claude's parser
// draws.
func TestZedLeavesSearchesAndShellPathsOutOfTheFileRecord(t *testing.T) {
	rows := zedWorkedStore(t)

	joined := strings.Join(rows, "\n")
	for _, absent := range []string{"backoff", "**/*.go"} {
		if strings.Contains(joined, absent) {
			t.Errorf("a grep's pattern reached the index: %q in\n%s", absent, joined)
		}
	}
	for _, r := range rows {
		role, text, _ := strings.Cut(r, "\t")
		if role == RoleFiles && strings.Contains(text, "/w/app\n") {
			t.Errorf("a terminal's working directory was recorded as a file: %q", text)
		}
	}
	// Listing a directory is looking around, not working on a file.
	if strings.Contains(joined, "/w/app/vendor") {
		t.Errorf("a directory listing reached the file record:\n%s", joined)
	}
}

// A thread deja cannot decode is skipped, and the skip is counted against the
// store the way an unusable JSONL line is. Dropped in silence, a new data_type
// from Zed would have emptied the harness while doctor still said "found"
// (#4341).
func TestParseZedDBCountsTheThreadsItCannotDecode(t *testing.T) {
	zedHome(t)
	good := zedZstdHex(t, zedModernBody)
	sql := zedSchema + `
insert into threads (id,summary,updated_at,data_type,data,folder_paths,created_at) values
 ('broken-frame','truncated','2026-07-19T09:00:00+00:00','zstd',x'28b52ffd00',  '/w/p','2026-07-19T09:00:00+00:00'),
 ('unknown-type','future encoding','2026-07-19T09:00:01+00:00','brotli',x'00',   '/w/p','2026-07-19T09:00:01+00:00'),
 ('not-json','json but not a thread','2026-07-19T09:00:02+00:00','json','not json at all','/w/p','2026-07-19T09:00:02+00:00'),
 ('no-messages','empty thread','2026-07-19T09:00:03+00:00','json','{"version":"0.3.0","title":"t","messages":[]}','/w/p','2026-07-19T09:00:03+00:00'),
 ('ok','fine','2026-07-19T09:00:04+00:00','zstd',x'` + good + `','/w/p','2026-07-19T09:00:04+00:00');`
	db := zedTestDB(t, sql)
	DiagSnapshot()
	if _, err := ParseZedDB(db); err != nil {
		t.Fatal(err)
	}
	reasons := DiagReasons()
	malformed, _ := DiagSnapshot()
	// An empty thread is a thread with nothing in it, not one deja failed on.
	if malformed[db] != 3 {
		t.Fatalf("unreadable threads counted = %d, want 3: %v", malformed[db], malformed)
	}
	// Rows come back by updated_at, so the last skip is the JSON one; the
	// brotli row has to name its encoding when it is the only one.
	if r := reasons[db]; !strings.Contains(r, "not-json") {
		t.Fatalf("reason = %q, want the last skipped thread named", r)
	}
	brotli := zedTestDB(t, zedSchema+`
insert into threads (id,summary,updated_at,data_type,data) values ('unknown-type','s','2026-07-19T09:00:01+00:00','brotli',x'00');`)
	if _, err := ParseZedDB(brotli); err != nil {
		t.Fatal(err)
	}
	if r := DiagReasons()[brotli]; !strings.Contains(r, `unknown data_type "brotli"`) {
		t.Fatalf("reason = %q, want the unknown data_type named", r)
	}
	DiagSnapshot()
}
