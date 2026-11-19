package sources

import (
	"crypto/sha256"
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

// A transcript whose chat is gone from chats/ re-read all of chats/ on every
// lookup, so a rebuild with 500 of them read it 500 times (#4226). A miss is
// answered from the index while it is fresh, and a chat written after the
// scan, in a new bucket or an existing one, is still found.
func TestCursorChatMissesDoNotRescanChats(t *testing.T) {
	cli := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cli)
	chats := filepath.Join(cli, "chats")
	writeChat := func(cwd, id string) {
		t.Helper()
		dir := filepath.Join(chats, CursorChatBucket(cwd), id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"cwd":`+fmt.Sprintf("%q", cwd)+`}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for b := range 20 {
		for c := range 5 {
			writeChat(fmt.Sprintf("/work/p%d", b), fmt.Sprintf("chat-%d-%d", b, c))
		}
	}
	// Seeded an hour ago, so a chat added below moves an mtime even on a
	// filesystem with a coarse clock.
	old := time.Now().Add(-time.Hour)
	dirs, _ := os.ReadDir(chats)
	for _, d := range dirs {
		_ = os.Chtimes(filepath.Join(chats, d.Name()), old, old)
	}
	_ = os.Chtimes(chats, old, old)

	transcript := func(id string) string {
		return filepath.Join(cli, "projects", "work-x", "agent-transcripts", id, id+".jsonl")
	}
	before := cursorChatIndex.scans
	if cwd, ok := CursorChatCWD(transcript("chat-3-1")); cwd != "/work/p3" || !ok {
		t.Fatalf("present chat = %q, %v", cwd, ok)
	}
	for i := range 50 {
		if cwd, _ := CursorChatCWD(transcript(fmt.Sprintf("gone-%d", i))); cwd != "" {
			t.Fatalf("gone chat %d = %q", i, cwd)
		}
	}
	if n := cursorChatIndex.scans - before; n != 1 {
		t.Fatalf("1 present + 50 gone lookups scanned chats/ %d times, want 1", n)
	}

	// A chat added to a bucket in the clock tick of the scan leaves the
	// bucket's mtime as it was; on HFS+ or FAT that tick is a second or two.
	writeChat("/work/p7", "late-same-bucket")
	_ = os.Chtimes(filepath.Join(chats, CursorChatBucket("/work/p7")), old, old)
	cursorChatIndex.scanned = cursorChatIndex.scanned.Add(-cursorChatFresh)
	if cwd, ok := CursorChatCWD(transcript("late-same-bucket")); cwd != "/work/p7" || !ok {
		t.Fatalf("chat added to an existing bucket, once the index aged out = %q, %v", cwd, ok)
	}
	writeChat("/work/new", "late-new-bucket")
	if cwd, ok := CursorChatCWD(transcript("late-new-bucket")); cwd != "/work/new" || !ok {
		t.Fatalf("chat added in a new bucket = %q, %v", cwd, ok)
	}
}

// A chats/ mtime ahead of the clock — a ~/.cursor copied from a machine whose
// clock ran fast, a network share with skew — read as changed at every miss,
// and each miss rescanned all of chats/ again.
func TestCursorChatMissesTrustAChatsDirFromTheFuture(t *testing.T) {
	cli := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cli)
	chats := filepath.Join(cli, "chats")
	dir := filepath.Join(chats, CursorChatBucket("/work/p"), "chat-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ahead := time.Now().Add(time.Hour)
	_ = os.Chtimes(chats, ahead, ahead)
	before := cursorChatIndex.scans
	for i := range 20 {
		CursorChatCWD(filepath.Join(cli, "projects", "work-p", "agent-transcripts", fmt.Sprintf("gone-%d", i), fmt.Sprintf("gone-%d.jsonl", i)))
	}
	if n := cursorChatIndex.scans - before; n != 1 {
		t.Fatalf("20 misses with chats/ an hour ahead scanned it %d times, want 1", n)
	}
}

// Cursor CLI's second store is unread, which is free while a transcript sits
// beside every chat and blindness the day one stops being written. The count
// has to key on the chat's own uuid: both layouts name a chat the same way,
// and comparing anything else would either cry wolf or stay silent (#3772).
func TestCursorChatsWithoutATranscriptAreCounted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", home)
	covered := "77a811d0-b626-4860-9684-009a16ef334d"
	orphan := "90eb0569-7d34-42df-bfa0-e2a79c249987"
	for _, id := range []string{covered, orphan} {
		dir := filepath.Join(home, "chats", "wshash", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "store.db"), []byte("SQLite format 3\x00"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A chat with no store.db is an empty chat, not a gap: 17 of the 36 here
	// were that, and counting them would report a loss that never happened.
	if err := os.MkdirAll(filepath.Join(home, "chats", "wshash", "empty-chat"), 0o755); err != nil {
		t.Fatal(err)
	}
	tdir := filepath.Join(home, "projects", "-Users-me-work", "agent-transcripts", covered)
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tdir, covered+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := len(CursorChatStores()); got != 2 {
		t.Fatalf("CursorChatStores() found %d stores, want the two with a store.db", got)
	}
	if got := CursorChatsWithoutTranscript(); got != 1 {
		t.Fatalf("CursorChatsWithoutTranscript() = %d, want only the chat with no transcript beside it", got)
	}

	// The quiet case: once the transcript exists, the count says nothing.
	tdir = filepath.Join(home, "projects", "-Users-me-work", "agent-transcripts", orphan)
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tdir, orphan+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := CursorChatsWithoutTranscript(); got != 0 {
		t.Fatalf("CursorChatsWithoutTranscript() = %d with a transcript for every chat, want 0", got)
	}
}

// Nothing to say on a machine without Cursor CLI, and no walk of a root that
// is not there.
func TestCursorChatsAreSilentWithoutTheStore(t *testing.T) {
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(t.TempDir(), "absent"))
	if got := CursorChatStores(); got != nil {
		t.Errorf("CursorChatStores() = %v, want none", got)
	}
	if got := CursorChatsWithoutTranscript(); got != 0 {
		t.Errorf("CursorChatsWithoutTranscript() = %d, want 0", got)
	}
}

// cursor writes a composer's `lastUpdatedAt` when it feels like it, and the
// bubbles keep arriving regardless. The pass filtered both sides and read no
// bubble whose composer had not also moved, so a turn written after the
// composer's own stamp was skipped — and the next pass, carrying a later
// watermark, excluded the bubble on its own stamp too (#2159).
func TestACursorTurnSurvivesAComposerThatStoppedAdvancing(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "state.vscdb")
	schema := `create table cursorDiskKV (key text primary key, value text);
insert into cursorDiskKV values
 ('composerData:comp-1', json('{"composerId":"comp-1","name":"Fix the pager","createdAt":1752600000000,"lastUpdatedAt":1752600100000}')),
 ('bubbleId:comp-1:b1', json('{"type":1,"text":"the first question","timestamp":1752600001000,"workspaceProjectDir":"/Users/me/work/my-app"}')),
 ('bubbleId:comp-1:b2', json('{"type":2,"text":"the later answer","timestamp":1752600900000}'));`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, schema).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	texts := func(ms int64) []string {
		t.Helper()
		ss, err := parseCursorDB(db, time.UnixMilli(ms).UTC())
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range ss {
			for _, m := range s.Messages {
				out = append(out, m.Text)
			}
		}
		return out
	}
	// The premise: a watermark before everything brings both turns.
	if got := texts(1752600000000); len(got) != 2 {
		t.Fatalf("a watermark before the store returned %v, want both turns", got)
	}
	// The watermark a pass would carry after the first turn: past the
	// composer's own stamp, before the later bubble.
	// The composer comes back whole, the turn already indexed with the new
	// one: the index replaces the session with what a pass returns (#4451).
	got := texts(1752600200000)
	if strings.Join(got, " ") != "the first question the later answer" {
		t.Errorf("returned %v, want the composer whole with the turn written after it stopped advancing", got)
	}
	ss, err := parseCursorDB(db, time.UnixMilli(1752600200000).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Errorf("the pass returned %d session(s), want one", len(ss))
	}
	// And it is still a filter: past every bubble, the pass is empty.
	if got := texts(1752601000000); len(got) != 0 {
		t.Errorf("a watermark past every bubble returned %v, want nothing", got)
	}
}

// Past cursorComposerListMax moved composers the pass asks for every composer
// row instead of naming them, and keeps the ones a bubble moved or whose own
// stamp moved: a rename adds no bubble (#4450). The rest are dropped.
func TestACursorPassPastTheKeyListKeepsOnlyMovedComposers(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "state.vscdb")
	var b strings.Builder
	b.WriteString("create table cursorDiskKV (key text primary key, value text);\nbegin;\n")
	moved := cursorComposerListMax + 1
	for i := range moved {
		fmt.Fprintf(&b, "insert into cursorDiskKV values ('composerData:c%d', json('{\"composerId\":\"c%d\",\"name\":\"task %d\",\"createdAt\":1752600000000,\"lastUpdatedAt\":1752600100000}'));\n", i, i, i)
		fmt.Fprintf(&b, "insert into cursorDiskKV values ('bubbleId:c%d:b1', json('{\"type\":1,\"text\":\"turn %d\",\"timestamp\":1752600900000}'));\n", i, i)
	}
	b.WriteString(`insert into cursorDiskKV values ('composerData:renamed', json('{"composerId":"renamed","name":"new name","createdAt":1752600000000,"lastUpdatedAt":1752600950000}'));
insert into cursorDiskKV values ('bubbleId:renamed:b1', json('{"type":1,"text":"an old turn","timestamp":1752600001000}'));
insert into cursorDiskKV values ('composerData:still', json('{"composerId":"still","name":"untouched","createdAt":1752600000000,"lastUpdatedAt":1752600100000}'));
insert into cursorDiskKV values ('bubbleId:still:b1', json('{"type":1,"text":"nothing new","timestamp":1752600001000}'));
commit;`)
	// On stdin: the script is past the 32 KiB a Windows command line holds.
	seed := exec.CommandContext(t.Context(), "sqlite3", db)
	seed.Stdin = strings.NewReader(b.String())
	if out, err := seed.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	ss, err := parseCursorDB(db, time.UnixMilli(1752600200000).UTC())
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, s := range ss {
		ids[s.ID] = true
	}
	if len(ss) != moved+1 || !ids["renamed"] || ids["still"] {
		t.Errorf("pass returned %d sessions (renamed %v, still %v), want %d moved and the renamed one", len(ss), ids["renamed"], ids["still"], moved)
	}
}

// Cursor writes the time of each turn into the turn, and the reader took the
// file's modification time for all of them: one real transcript here holds
// turns from July 26 and August 22 and every message was dated August 22, the
// day the file was last written (#3349).
func TestCursorTakesTheTimeFromTheTurnNotTheFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "Users-w-api", "agent-transcripts", "de875c53")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "de875c53.jsonl")
	lines := `{"role":"user","message":{"role":"user","content":[{"type":"text","text":"<timestamp>Sunday, Jul 26, 2026, 1:06 PM (UTC+3)</timestamp>\n<user_query>\nwhy does the retry loop drop the last attempt\n</user_query>"}]}}
{"role":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"because the counter compares with < instead of <="}]}}
{"role":"user","message":{"role":"user","content":[{"type":"text","text":"<timestamp>Saturday, Aug 22, 2026, 9:35 AM (UTC+3)</timestamp>\n<user_query>\ncap the retries at three\n</user_query>"}]}}
`
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	// The file's own mtime is a third date, and must not be what any turn says.
	mtime := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CURSOR_CLI_ROOT", root)

	ss, err := ParseCursorTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	july := time.Date(2026, 7, 26, 13, 6, 0, 0, time.FixedZone("", 3*3600))
	august := time.Date(2026, 8, 22, 9, 35, 0, 0, time.FixedZone("", 3*3600))
	if !s.Started.Equal(july) {
		t.Errorf("started = %v, want the first turn's own stamp %v", s.Started, july)
	}
	if !s.Updated.Equal(august) {
		t.Errorf("updated = %v, want the last turn's own stamp %v", s.Updated, august)
	}
	if len(s.Messages) < 3 {
		t.Fatalf("messages = %d, want at least 3", len(s.Messages))
	}
	if !s.Messages[0].Time.Equal(july) {
		t.Errorf("first turn = %v, want %v", s.Messages[0].Time, july)
	}
	if !s.Messages[1].Time.Equal(july) {
		t.Errorf("the reply carries the question's time: %v, want %v", s.Messages[1].Time, july)
	}
	if !s.Messages[2].Time.Equal(august) {
		t.Errorf("third turn = %v, want %v", s.Messages[2].Time, august)
	}
}

// The shapes the stamp can take, and the ones it cannot: a zone with minutes,
// no zone at all, a month spelled in full, and three that do not parse — a
// label other than UTC, seconds in the time, and a month name deja does not
// know. Each of those falls back to the file's modification time, which is the
// documented behaviour (#3349).
func TestCursorTurnTimeReadsTheShapesCursorWrites(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
	}{
		{"<timestamp>Sunday, Jul 26, 2026, 1:06 PM (UTC+3)</timestamp>", "2026-07-26T13:06:00+03:00"},
		{"<timestamp>Jul 26, 2026, 1:06 PM (UTC-3:30)</timestamp>", "2026-07-26T13:06:00-03:30"},
		{"<timestamp>January 2, 2026, 9:05 AM</timestamp>", "2026-01-02T09:05:00Z"},
	} {
		got, ok := cursorTurnTime(c.in)
		if !ok {
			t.Errorf("cursorTurnTime(%q) did not parse", c.in)
			continue
		}
		if got.Format(time.RFC3339) != c.want {
			t.Errorf("cursorTurnTime(%q) = %s, want %s", c.in, got.Format(time.RFC3339), c.want)
		}
	}
	for _, in := range []string{
		"<timestamp>Sunday, Jul 26, 2026, 1:06:45 PM (UTC+3)</timestamp>",
		"<timestamp>воскресенье, 26 июля 2026, 13:06</timestamp>",
		"no stamp at all, just a question",
	} {
		if _, ok := cursorTurnTime(in); ok {
			t.Errorf("cursorTurnTime(%q) parsed a shape it should have left to the file time", in)
		}
	}
}

// The encoded directory in the form the shared resolver walks: Cursor writes
// it without the leading separator, so the exported helper adds it back.
func TestCursorTranscriptProjectDirBase(t *testing.T) {
	got := CursorTranscriptProjectDirBase(filepath.Join("root", "projects", "Users-me-work-app", "agent-transcripts", "s1", "s1.jsonl"))
	if got != "-Users-me-work-app" {
		t.Errorf("CursorTranscriptProjectDirBase = %q, want %q", got, "-Users-me-work-app")
	}
	if got := CursorTranscriptProjectDirBase(filepath.Join("agent-transcripts", "s1.jsonl")); got != "" {
		t.Errorf("a path with no projects directory above it = %q, want empty", got)
	}
}

// seedCursorStore writes a chat store the way Cursor CLI does: JSON message
// blobs, a protobuf root listing their digests in field 1, and meta naming the
// root as hex-encoded JSON.
func seedCursorStore(t *testing.T, db string, msgs []string) {
	t.Helper()
	var sql strings.Builder
	sql.WriteString("create table blobs (id text primary key, data blob); create table meta (key text primary key, value text);\n")
	var root []byte
	for _, m := range msgs {
		sum := sha256.Sum256([]byte(m))
		root = append(root, 0x0a, 32)
		root = append(root, sum[:]...)
		fmt.Fprintf(&sql, "insert into blobs values ('%x', X'%x');\n", sum, m)
	}
	// A field the walk must skip: Cursor's root carries more than the list.
	root = append(root, 0x12, 3, 'a', 'b', 'c')
	rootID := sha256.Sum256(root)
	fmt.Fprintf(&sql, "insert into blobs values ('%x', X'%x');\n", rootID, root)
	meta := hex.EncodeToString([]byte(fmt.Sprintf(`{"agentId":"a","latestRootBlobId":"%x"}`, rootID)))
	fmt.Fprintf(&sql, "insert into meta values ('0', '%s');\n", meta)
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql.String()).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

// Cursor's transcript records the calls and none of their results; the chat
// store beside it has them. A failed command read as one that never failed,
// and nothing it printed was searchable (#4187).
func TestCursorToolResultsComeFromTheChatStore(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "")
	home := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", home)
	id := "5b0c1a7e-2f4d-4c1b-9a3e-7d2f1c0b9e8a"

	chat := filepath.Join(home, "chats", "wshash", id)
	if err := os.MkdirAll(chat, 0o755); err != nil {
		t.Fatal(err)
	}
	seedCursorStore(t, filepath.Join(chat, "store.db"), []string{
		`{"role":"user","content":[{"type":"text","text":"build it"}]}`,
		// A call the transcript does not carry: pairing by position would
		// hand its result to the next Shell.
		`{"role":"assistant","content":[{"type":"tool-call","toolCallId":"c0","toolName":"GetDynamicTools","args":{"namespace":"deja"}},{"type":"tool-call","toolCallId":"c1","toolName":"Shell","args":{"command":"go vet ./...","description":"vet"}},{"type":"tool-call","toolCallId":"c2","toolName":"Shell","args":{"command":"make build","description":"build"}}]}`,
		`{"role":"tool","content":[{"type":"tool-result","toolCallId":"c0","toolName":"GetDynamicTools","result":{"description":"selfdescneedle indexes past sessions"}}]}`,
		`{"role":"tool","content":[{"type":"tool-result","toolCallId":"c1","toolName":"Shell","result":"Exit code: 1\n\nCommand output:\n\n` + "```" + `\nstorevetneedle: unreachable code\n` + "```" + `\n\nCommand completed in 55 ms.\n\nShell state (cwd, env vars) persists for subsequent calls."}]}`,
		`{"role":"tool","content":[{"type":"tool-result","toolCallId":"c2","toolName":"Shell","result":"Exit code: 0\n\nCommand output:\n\n` + "```" + `\nok\n` + "```" + `"}]}`,
	})

	tdir := filepath.Join(home, "projects", "Users-me-work", "agent-transcripts", id)
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"role":"user","message":{"content":[{"type":"text","text":"build it"}]}}
{"role":"assistant","message":{"content":[{"type":"tool_use","name":"GetDynamicTools","input":{"namespace":"deja"}},{"type":"tool_use","name":"Shell","input":{"command":"go vet ./...","description":"vet"}},{"type":"tool_use","name":"Shell","input":{"command":"make build","description":"build"}}]}}
`
	p := filepath.Join(tdir, id+".jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseCursorTranscript(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %#v", err, ss)
	}
	var cmds, outs []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleToolOutput:
			outs = append(outs, m.Text)
		}
	}
	if want := []string{"$ go vet ./...  → exit 1", "$ make build  → exit 0"}; strings.Join(cmds, "|") != strings.Join(want, "|") {
		t.Fatalf("commands = %q, want %q", cmds, want)
	}
	if len(outs) != 2 || outs[0] != "storevetneedle: unreachable code" || outs[1] != "ok" {
		t.Fatalf("tool output = %q", outs)
	}

	for _, m := range ss[0].Messages {
		if strings.Contains(m.Text, "selfdescneedle") {
			t.Fatalf("an MCP server's own description was indexed: %q", m.Text)
		}
	}

	// The switch that keeps tool output out of the index still holds.
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	ss, _ = ParseCursorTranscript(p)
	for _, m := range ss[0].Messages {
		if m.Role == RoleToolOutput {
			t.Fatalf("tool output indexed with DEJA_INDEX_TOOL_OUTPUT=0: %q", m.Text)
		}
	}
}

// A transcript with no store beside it parses as it did.
func TestCursorTranscriptWithoutAStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", home)
	tdir := filepath.Join(home, "projects", "Users-me-work", "agent-transcripts", "solo")
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(tdir, "solo.jsonl")
	if err := os.WriteFile(p, []byte(`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"command":"make"}}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCursorTranscript(p)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 || ss[0].Messages[0].Text != "$ make" {
		t.Fatalf("parse: %v %#v", err, ss)
	}
}

func TestProtoChildIDsStopsOnABrokenBlob(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	b := append([]byte{0x0a, 32}, sum[:]...)
	b = append(b, 0x0a, 40, 1, 2) // length runs past the end
	if got := protoChildIDs(b); len(got) != 1 || got[0] != hex.EncodeToString(sum[:]) {
		t.Fatalf("children = %q", got)
	}
}

// The store is the chat's latest branch: a call it lost to a rewind must not
// take the result of a later identical call, and the later one keeps its own.
func TestCursorPairingOnlyMovesForward(t *testing.T) {
	key := func(cmd string) string { return cursorCallKey("Shell", map[string]any{"command": cmd}) }
	rs := &cursorToolResults{calls: []cursorToolResult{
		{key: key("make build"), text: "build-2"},
		{key: key("npm test"), text: "test-2"},
	}}
	args := func(cmd string) map[string]any { return map[string]any{"command": cmd} }
	// Transcript: npm test (rewound away), make build, npm test.
	if got, ok := rs.take("Shell", args("npm test")); !ok || got != "test-2" {
		t.Fatalf("first npm test = %q %v", got, ok)
	}
	if _, ok := rs.take("Shell", args("make build")); ok {
		t.Fatal("make build paired backwards, behind a call already paired")
	}
	if _, ok := rs.take("Shell", args("npm test")); ok {
		t.Fatal("the same store call paired twice")
	}
	var none *cursorToolResults
	if _, ok := none.take("Shell", args("x")); ok {
		t.Fatal("a chat with no store paired a call")
	}
}

func TestCursorShellOutputDropsTheFraming(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Exit code: 0\n\nCommand output:\n\n```\nhi\n\n```\n\nCommand completed in 420 ms.\n\nShell state (cwd, env vars) persists for subsequent calls.", "hi"},
		{"Exit code: 1\n\nCommand output:\n\n````\nprinted ```fence```\n````\n\nCommand completed in 3 ms.", "printed ```fence```"},
		{"Exit code: 2\n\nno such file", "no such file"},
		{"plain text", "plain text"},
	} {
		if got := cursorShellOutput(c.in); got != c.want {
			t.Errorf("cursorShellOutput(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// cursor-agent writes a subagent's run to agent-transcripts/<parent>/subagents/
// <child>.jsonl, and the lines carry no ids, so the path is the only link. The
// child was skipped by default and, under DEJA_INCLUDE_SUBAGENTS=1, read as a
// session of its own with no parent: blame could not name the subagent that
// wrote a file, and show could not hang it under the run that spawned it.
func TestACursorSubagentNamesItsParentAndKeepsWhatItChanged(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", root)
	parent := "11111111-aaaa-4bbb-8ccc-000000000001"
	dir := filepath.Join(root, "projects", "Users-x-app", "agent-transcripts", parent, "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(`{"role":"user","message":{"content":[{"type":"text","text":"Write the release notes into notes.txt"}]}}` + "\n")
	b.WriteString(`{"role":"assistant","message":{"content":[{"type":"text","text":"Writing it."},{"type":"tool_use","name":"Write","input":{"path":"/x/app/notes.txt","contents":"zebrafinch release notes, line one of them\n"}}]}}` + "\n")
	// Enough reading after the write that the tail cut has a middle to drop.
	for i := range 6 {
		fmt.Fprintf(&b, `{"role":"assistant","message":{"content":[{"type":"text","text":"checking step %d"}]}}`+"\n", i)
	}
	b.WriteString(`{"role":"assistant","message":{"content":[{"type":"text","text":"Done: notes.txt holds the notes."}]}}` + "\n")
	path := filepath.Join(dir, "22222222-aaaa-4bbb-8ccc-000000000002.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := CursorTranscripts(); len(got) != 1 || got[0] != path {
		t.Fatalf("the subagent transcript is not read by default: %v", got)
	}
	ss, err := ParseCursorTranscript(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %v", ss, err)
	}
	s := ss[0]
	if s.Kind != "subagent" || s.Parent != parent {
		t.Fatalf("kind %q parent %q, want subagent of %s", s.Kind, s.Parent, parent)
	}
	var wrote, middle bool
	for _, m := range s.Messages {
		wrote = wrote || m.Role == RoleWrote && strings.HasPrefix(m.Text, "/x/app/notes.txt")
		middle = middle || strings.Contains(m.Text, "checking step 1")
	}
	if !wrote {
		t.Errorf("the subagent's write is gone, so blame cannot name it: %+v", s.Messages)
	}
	if middle {
		t.Errorf("the middle of the run is kept by default: %+v", s.Messages)
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	ss, _ = ParseCursorTranscript(path)
	if len(ss) != 1 || ss[0].Parent != parent || len(ss[0].Messages) <= len(s.Messages) {
		t.Fatalf("DEJA_INCLUDE_SUBAGENTS=1 should take the whole run under its parent: %+v", ss)
	}

	// The primary transcript is nobody's child.
	top := filepath.Join(root, "projects", "Users-x-app", "agent-transcripts", parent, parent+".jsonl")
	if err := os.WriteFile(top, []byte(`{"role":"user","message":{"content":[{"type":"text","text":"hi"}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ss, _ := ParseCursorTranscript(top); len(ss) != 1 || ss[0].Kind != "" || ss[0].Parent != "" {
		t.Errorf("primary transcript read as a subagent: %+v", ss)
	}
}

func TestParseCursorDB(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "state.vscdb")
	schema := `create table cursorDiskKV (key text primary key, value text);
insert into cursorDiskKV values
 ('composerData:comp-1', json('{"composerId":"comp-1","name":"Fix the pager","createdAt":1752600000000,"lastUpdatedAt":1752600100000,"fullConversationHeadersOnly":[{"bubbleId":"b1","type":1},{"bubbleId":"b2","type":2}]}')),
 ('bubbleId:comp-1:b1', json('{"type":1,"text":"cursorneedle question","timestamp":1752600001000,"workspaceProjectDir":"/Users/me/work/my-app"}')),
 ('bubbleId:comp-1:b2', json('{"type":2,"text":"cursorneedle answer","rawText":"raw","timestamp":1752600002000}')),
 ('composerData:comp-empty', json('{"composerId":"comp-empty","name":"draft"}')),
 ('composerData:comp-null', null),
 ('agentKv:blob:x', 'not-json-target');`
	cmd := exec.CommandContext(t.Context(), "sqlite3", db, schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v: %s", err, out)
	}
	ss, err := ParseCursorDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1 (empty/null dropped): %#v", len(ss), ss)
	}
	s := ss[0]
	if s.Harness != "cursor" || s.ID != "comp-1" || s.Title != "Fix the pager" {
		t.Fatalf("bad meta: %#v", s)
	}
	if s.Project != "work/my-app" {
		t.Fatalf("project = %q, want work/my-app", s.Project)
	}
	if len(s.Messages) != 2 || s.Messages[0].Role != "user" || s.Messages[1].Role != "assistant" {
		t.Fatalf("messages wrong: %#v", s.Messages)
	}
	if s.Messages[0].Time.UnixMilli() != 1752600001000 {
		t.Fatalf("timestamp wrong: %v", s.Messages[0].Time)
	}
	if newest, err := ParseCursorDBNewest(db); err != nil || len(newest) != 1 {
		t.Fatalf("ParseCursorDBNewest = %v, %#v", err, newest)
	}
}

func TestParseCursorTranscript(t *testing.T) {
	t.Setenv("DEJA_CURSOR_CLI_ROOT", t.TempDir()) // no chats/ of the machine's own
	tmp := t.TempDir()
	encoded := "Users-x-work-my-app"
	wantProject := filepath.Join("my", "app") // fallback splits on every hyphen
	if runtime.GOOS != "windows" {
		resolved := filepath.Join(tmp, "work", "my-app")
		if err := os.MkdirAll(resolved, 0o755); err != nil {
			t.Fatal(err)
		}
		encoded = strings.TrimPrefix(strings.ReplaceAll(resolved, string(filepath.Separator), "-"), "-")
		wantProject = "work/my-app" // resolved against the real directory
	}
	dir := filepath.Join(tmp, "cursorcli", "projects", encoded, "agent-transcripts", "sess-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"role":"user","message":{"content":[{"type":"text","text":"transcriptneedle question"}]}}
{"role":"assistant","message":{"content":[{"type":"text","text":"Running ls."},{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}
{"type":"turn_ended","status":"success"}
`
	p := filepath.Join(dir, "sess-1.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCursorTranscript(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "sess-1" {
		t.Fatalf("bad session: %#v", ss)
	}
	if len(ss[0].Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (control line skipped): %#v", len(ss[0].Messages), ss[0].Messages)
	}
	if ss[0].Project != wantProject {
		t.Fatalf("project = %q, want %q", ss[0].Project, wantProject)
	}
}

func TestCursorTranscriptProjectWalkUp(t *testing.T) {
	t.Setenv("DEJA_CURSOR_CLI_ROOT", t.TempDir()) // no chats/ of the machine's own
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "project directory",
			path: filepath.Join("root", "projects", "Users-me-work-app", "agent-transcripts", "session", "session.jsonl"),
			want: filepath.Join("work", "app"),
		},
		{
			name: "project directory case",
			path: filepath.Join("root", "Projects", "Users-me-work-app", "agent-transcripts", "session", "session.jsonl"),
			want: filepath.Join("work", "app"),
		},
		{
			name: "filesystem root",
			path: filepath.Join(string(filepath.Separator), "session.jsonl"),
			want: "-",
		},
		{
			name: "relative path without projects directory",
			path: filepath.Join("agent-transcripts", "session.jsonl"),
			want: "-",
		},
	}
	if runtime.GOOS == "windows" {
		tests = append(tests, struct {
			name string
			path string
			want string
		}{
			name: "windows drive root",
			path: filepath.Join(`C:\`, "session.jsonl"), //nolint:gocritic // a drive root, joined the way Windows joins it
			want: "-",
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cursorTranscriptProject(tt.path); got != tt.want {
				t.Fatalf("cursorTranscriptProject(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestCursorTranscriptsTakeSubagentsUnlessSwitchedOff(t *testing.T) {
	t.Setenv("DEJA_CURSOR_CLI_ROOT", t.TempDir()) // no chats/ of the machine's own
	root := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", root)
	base := filepath.Join(root, "projects", "Users-x-app", "agent-transcripts", "s1")
	if err := os.MkdirAll(filepath.Join(base, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"role":"user","message":{"content":"hi"}}` + "\n"
	if err := os.WriteFile(filepath.Join(base, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "subagents", "child.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if files := CursorTranscripts(); len(files) != 2 {
		t.Fatalf("discovery wrong, want the run and its subagent: %v", files)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	files := CursorTranscripts()
	if len(files) != 1 || !strings.HasSuffix(files[0], "s1.jsonl") {
		t.Fatalf("DEJA_INCLUDE_SUBAGENTS=0 discovery wrong: %v", files)
	}
}

func TestCursorTranscriptsPathMatchingIsCaseInsensitive(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", root)
	dir := filepath.Join(root, "projects", "Users-x-app", "Agent-Transcripts", "s1", "SubAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "child.JSONL")
	if err := os.WriteFile(path, []byte(`{"role":"user","message":{"content":"hi"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	if got := CursorTranscripts(); len(got) != 0 {
		t.Fatalf("subagent transcript discovered under =0: %v", got)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if got := CursorTranscripts(); len(got) != 1 || got[0] != path {
		t.Fatalf("case-insensitive transcript discovery = %v, want %q", got, path)
	}
}

// The vocabulary here is not invented: it was read off a transcript the Cursor
// CLI wrote while being asked to read, edit, create, delete and run something.
// Every field differs from Claude's — `path` not `file_path`, `Shell` not
// `Bash` — which is why the dialect exists.
func TestCursorTranscriptWorkRecords(t *testing.T) {
	t.Setenv("DEJA_CURSOR_CLI_ROOT", t.TempDir()) // no chats/ of the machine's own
	dir := filepath.Join(t.TempDir(), "projects", "Users-me-app", "agent-transcripts", "s-work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"role":"user","message":{"content":[{"type":"text","text":"fix the greeting"}]}}
{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/w/app/main.go"}}]}}
{"role":"assistant","message":{"content":[{"type":"tool_use","name":"StrReplace","input":{"path":"/w/app/main.go","old_string":"println(\"hello\")","new_string":"println(\"goodbye\")"}}]}}
{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"path":"/w/app/notes.txt","contents":"alpha\n"}},{"type":"tool_use","name":"Delete","input":{"path":"/w/app/notes.txt"}}]}}
{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"command":"go test ./...","description":"tests"}},{"type":"tool_use","name":"Shell","input":{"command":"ls -la"}}]}}
{"type":"turn_ended","status":"success"}
`
	p := filepath.Join(dir, "s-work.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCursorTranscript(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	// A turn that only called tools carries no prose; its records must survive
	// anyway, which is the whole point of the work-record path.
	files := strings.Join(byRole[RoleFiles], "\n")
	for _, want := range []string{"/w/app/main.go", "/w/app/notes.txt"} {
		if !strings.Contains(files, want) {
			t.Errorf("files records missing %q: %q", want, files)
		}
	}
	if len(byRole[RoleEdit]) != 1 || !strings.HasPrefix(byRole[RoleEdit][0], "/w/app/main.go\n") ||
		!strings.Contains(byRole[RoleEdit][0], `println("hello")`) {
		t.Errorf("edit span = %q, want the replaced bytes under their path", byRole[RoleEdit])
	}
	// `ls -la` is navigation and is dropped by the same rule Claude's commands
	// go through.
	if len(byRole[RoleCommand]) != 1 || byRole[RoleCommand][0] != "$ go test ./..." {
		t.Errorf("commands = %q, want only the meaningful one", byRole[RoleCommand])
	}
}

func TestCursorWorkRecordsRespectTheirSwitches(t *testing.T) {
	t.Setenv("DEJA_CURSOR_CLI_ROOT", t.TempDir()) // no chats/ of the machine's own
	dir := filepath.Join(t.TempDir(), "projects", "Users-me-app", "agent-transcripts", "s-off")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/w/app/main.go"}},{"type":"tool_use","name":"StrReplace","input":{"path":"/w/app/main.go","old_string":"a","new_string":"b"}},{"type":"tool_use","name":"Shell","input":{"command":"go build ./..."}}]}}
{"role":"user","message":{"content":[{"type":"text","text":"keep at least one message"}]}}
`
	p := filepath.Join(dir, "s-off.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_PATHS", "0")
	t.Setenv("DEJA_INDEX_EDITS", "0")
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	ss, err := ParseCursorTranscript(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles || m.Role == RoleEdit || m.Role == RoleCommand {
			t.Fatalf("a switched-off record was still written: %s %q", m.Role, m.Text)
		}
	}
}

func seedCursorStamped(t *testing.T, db string, stamp int64) {
	t.Helper()
	schema := fmt.Sprintf(`create table cursorDiskKV (key text primary key, value text);
insert into cursorDiskKV values
 ('composerData:comp-1', json('{"composerId":"comp-1","name":"pool sizing","createdAt":%[1]d,"lastUpdatedAt":%[1]d,"fullConversationHeadersOnly":[{"bubbleId":"b1","type":1}]}')),
 ('bubbleId:comp-1:b1', json('{"type":1,"text":"the pool was exhausted","timestamp":%[1]d,"workspaceProjectDir":"/tmp/app"}'));`, stamp)
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, schema).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

// Every source reads a numeric stamp through unixGuess, which takes either
// unit. Cursor had its own millisecond-only reader, so a seconds-stamped store
// was dated to three weeks after the epoch and sat behind everything deja knows
// (#2094).
func TestACursorStoreStampedInSecondsIsNotDatedTo1970(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	when := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	for _, unit := range []struct {
		name  string
		stamp int64
	}{
		{"milliseconds", when.UnixMilli()},
		{"seconds", when.Unix()},
	} {
		db := filepath.Join(t.TempDir(), "state.vscdb")
		seedCursorStamped(t, db, unit.stamp)
		ss, err := ParseCursorDB(db)
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 || len(ss[0].Messages) != 1 {
			t.Fatalf("%s: the fixture did not come back whole, so this measures nothing: %#v", unit.name, ss)
		}
		for what, got := range map[string]time.Time{
			"started": ss[0].Started, "updated": ss[0].Updated,
			"the message": ss[0].Messages[0].Time,
		} {
			if !got.UTC().Equal(when) {
				t.Errorf("%s: %s is %s, want %s", unit.name, what, got.UTC().Format(time.RFC3339), when.Format(time.RFC3339))
			}
		}
	}
}

// And the clause that decides what a changed store hands back reads the same
// units the reader does, or the two disagree the way opencode's did (#2086).
func TestTheCursorSinceClauseTakesEitherUnit(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	newer := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	watermark := newer.Add(-time.Hour)
	for _, unit := range []struct {
		name  string
		stamp int64
	}{
		{"milliseconds", newer.UnixMilli()},
		{"seconds", newer.Unix()},
	} {
		db := filepath.Join(t.TempDir(), "state.vscdb")
		seedCursorStamped(t, db, unit.stamp)
		if whole, err := ParseCursorDBSince(db, time.Time{}); err != nil || len(whole) != 1 {
			t.Fatalf("%s: the fixture is not readable at all: %d sessions err=%v", unit.name, len(whole), err)
		}
		ss, err := ParseCursorDBSince(db, watermark)
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 {
			t.Errorf("%s: a turn newer than the watermark did not come back: %d sessions", unit.name, len(ss))
		}
	}
}

// The other half of that: a millisecond store must still be filtered, or the
// watermark buys nothing on the store it exists for.
func TestTheCursorSinceClauseStillFilters(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	older := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	db := filepath.Join(t.TempDir(), "state.vscdb")
	seedCursorStamped(t, db, older.UnixMilli())
	ss, err := ParseCursorDBSince(db, older.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 0 {
		t.Errorf("a turn older than the watermark came back anyway: %d sessions", len(ss))
	}
}

// The Windows IDE root is %APPDATA%\Cursor\User. Before this branch existed,
// CursorUserRoot fell through to ~/.config/Cursor/User — a directory Cursor
// never writes on Windows — so a machine with megabytes of chats in
// state.vscdb reported no IDE stores at all. Runs natively on any Windows
// host, the CI leg included.
func TestCursorUserRootWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("resolution under test is the windows branch")
	}
	t.Setenv("DEJA_CURSOR_ROOT", "")

	appdata := filepath.Join(t.TempDir(), "Roaming")
	if err := os.MkdirAll(appdata, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPDATA", appdata)
	if got, want := CursorUserRoot(), filepath.Join(appdata, "Cursor", "User"); got != want {
		t.Fatalf("CursorUserRoot with APPDATA = %q, want %q", got, want)
	}

	// XDG must not leak into the Windows resolution the way it did before:
	// pointing it at an existing dir changes nothing here.
	xdg := filepath.Join(t.TempDir(), "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "Cursor", "User"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, want := CursorUserRoot(), filepath.Join(appdata, "Cursor", "User"); got != want {
		t.Fatalf("CursorUserRoot ignores XDG on windows, got %q want %q", got, want)
	}

	// A stripped environment still lands on the real location.
	t.Setenv("APPDATA", "")
	if got, want := CursorUserRoot(), filepath.Join(Home(), "AppData", "Roaming", "Cursor", "User"); got != want {
		t.Fatalf("CursorUserRoot without APPDATA = %q, want %q", got, want)
	}
}
