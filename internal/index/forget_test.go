package index

import (
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/search"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A Claude Code fork opens with its source's turns rewritten under its own
// id. Forgetting the source leaves that copy searchable under the fork, so
// forget names the fork rather than saying nothing.
func TestForgetNamesTheForkThatHoldsACopy(t *testing.T) {
	h := newTwoWayEnv(t)
	opening := hPrompt("app", "s1", 2, 0, "rotate the staging signing key") +
		hSay("app", "s1", 2, 1, "Rotated it and updated the verifier.")
	h.put("app", "s1", opening)
	// The same lines with the fork's id, then a turn of its own.
	fork := hPrompt("app", "s9", 2, 0, "rotate the staging signing key") +
		hSay("app", "s9", 2, 1, "Rotated it and updated the verifier.") +
		hPrompt("app", "s9", 2, 5, "now the production one")
	h.put("app", "s9", fork)
	h.put("svc", "s5", hPrompt("svc", "s5", 3, 0, "unrelated cache work"))
	dir := filepath.Join(h.tmp, "a.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	res, err := Forget(dir, ForgetOptions{Session: "s1", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"claude:s9"}; !reflect.DeepEqual(res.Copies, want) {
		t.Fatalf("copies = %v, want %v", res.Copies, want)
	}
	// And the other way: forgetting the fork names the source it copied.
	res, err = Forget(dir, ForgetOptions{Session: "s9", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"claude:s1"}; !reflect.DeepEqual(res.Copies, want) {
		t.Fatalf("copies of the fork = %v, want %v", res.Copies, want)
	}
}

// A Copilot CLI sub-agent's records sit in its parent's events.jsonl and are
// read as a session of their own. Forgetting the parent left them searchable
// (#5216).
func TestForgetCopilotParentTakesItsInlineSubagent(t *testing.T) {
	h := newTwoWayEnv(t)
	root := filepath.Join(h.tmp, "copilot")
	t.Setenv("DEJA_COPILOT_ROOT", root)
	const parent, child, other = "f67048ad-042c-42f4-ac33-5db51c4d48b0", "717532a2-bf69-4164-be09-03daacd1762f", "aaaa1111-0000-4000-8000-000000000000"
	write := func(id string, lines ...string) {
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	start := func(id string) string {
		return `{"type":"session.start","data":{"sessionId":"` + id + `","startTime":"2026-10-18T05:31:46.707Z","context":{"cwd":"/Users/x/work/app"}},"timestamp":"2026-10-18T05:31:46.707Z"}`
	}
	write(parent, start(parent),
		`{"type":"user.message","data":{"content":"write the notes with a helper"},"timestamp":"2026-10-18T05:31:47.305Z"}`,
		`{"type":"subagent.started","agentId":"`+child+`","data":{"agentName":"general-purpose"},"timestamp":"2026-10-18T05:31:47.356Z"}`,
		`{"type":"user.message","agentId":"`+child+`","data":{"content":"draft the zanzibarquill notes"},"timestamp":"2026-10-18T05:31:47.399Z"}`,
		`{"type":"assistant.message","agentId":"`+child+`","data":{"content":"drafted the zanzibarquill notes"},"timestamp":"2026-10-18T05:31:49.063Z"}`,
	)
	write(other, start(other), `{"type":"user.message","data":{"content":"unrelated cache work"},"timestamp":"2026-10-18T06:00:00.000Z"}`)
	dir := filepath.Join(h.tmp, "a.db")
	if err := Ensure(dir, "copilot", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := FindByID(dir, child); !ok {
		t.Fatalf("the sub-agent is not indexed as its own session")
	}
	res, err := Forget(dir, ForgetOptions{Session: parent})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sessions != 2 {
		t.Errorf("forgot %d sessions (%v), want the parent and its sub-agent", res.Sessions, res.Keys)
	}
	if res.Attached != 1 {
		t.Errorf("attached = %d, want the one sub-agent run, which the selector did not name", res.Attached)
	}
	if _, ok, _ := FindByID(dir, child); ok {
		t.Errorf("the sub-agent's run is still indexed after its parent was forgotten")
	}
	if _, ok, _ := FindByID(dir, other); !ok {
		t.Errorf("an unrelated session went too")
	}
	if res.Tombstones != 1 {
		t.Errorf("tombstones = %d, want only the parent's, which covers its runs", res.Tombstones)
	}

	// A run the forgotten session starts later stays out with it.
	const later = "abcdef01-0000-4000-8000-000000000000"
	write(parent, start(parent),
		`{"type":"user.message","data":{"content":"write the notes with a helper"},"timestamp":"2026-10-18T05:31:47.305Z"}`,
		`{"type":"subagent.started","agentId":"`+child+`","data":{"agentName":"general-purpose"},"timestamp":"2026-10-18T05:31:47.356Z"}`,
		`{"type":"user.message","agentId":"`+child+`","data":{"content":"draft the zanzibarquill notes"},"timestamp":"2026-10-18T05:31:47.399Z"}`,
		`{"type":"assistant.message","agentId":"`+child+`","data":{"content":"drafted the zanzibarquill notes"},"timestamp":"2026-10-18T05:31:49.063Z"}`,
		`{"type":"subagent.started","agentId":"`+later+`","data":{"agentName":"explore"},"timestamp":"2026-10-18T07:00:00.000Z"}`,
		`{"type":"user.message","agentId":"`+later+`","data":{"content":"zebrafinch lookup"},"timestamp":"2026-10-18T07:00:01.000Z"}`,
	)
	if err := Ensure(dir, "copilot", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := FindByID(dir, later); ok {
		t.Errorf("a run the forgotten session started later was indexed")
	}

	// Unforgetting the parent brings its runs back.
	if _, err := Unforget(dir, "copilot:"+parent, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{parent, child, later} {
		if _, ok, _ := FindByID(dir, id); !ok {
			t.Errorf("%s is still gone after its parent was unforgotten", id)
		}
	}
}

// Matched by --project along with its parent, the run still takes no
// tombstone of its own, so unforgetting the parent brings it back.
func TestForgetCopilotByProjectLeavesTheRunToItsParent(t *testing.T) {
	h := newTwoWayEnv(t)
	root := filepath.Join(h.tmp, "copilot")
	t.Setenv("DEJA_COPILOT_ROOT", root)
	const parent, child = "f67048ad-042c-42f4-ac33-5db51c4d48b0", "717532a2-bf69-4164-be09-03daacd1762f"
	dir0 := filepath.Join(root, parent)
	if err := os.MkdirAll(dir0, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"type":"session.start","data":{"sessionId":"` + parent + `","startTime":"2026-10-18T05:31:46.707Z","context":{"cwd":"/Users/x/work/app"}},"timestamp":"2026-10-18T05:31:46.707Z"}`,
		`{"type":"user.message","data":{"content":"write the notes with a helper"},"timestamp":"2026-10-18T05:31:47.305Z"}`,
		`{"type":"user.message","agentId":"` + child + `","data":{"content":"draft the notes"},"timestamp":"2026-10-18T05:31:47.399Z"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir0, "events.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.tmp, "a.db")
	if err := Ensure(dir, "copilot", false, nil); err != nil {
		t.Fatal(err)
	}
	res, err := Forget(dir, ForgetOptions{Project: "work/app"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sessions != 2 || res.Tombstones != 1 || res.Attached != 1 {
		t.Errorf("sessions/tombstones/attached = %d/%d/%d, want 2/1/1", res.Sessions, res.Tombstones, res.Attached)
	}
	if _, err := Unforget(dir, "copilot:"+parent, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := FindByID(dir, child); !ok {
		t.Errorf("the run stayed forgotten after its parent came back")
	}
}

// A session with a copy in another project that was deleted, and so kept,
// then forgotten: the pass kept the deleted copy's file marked kept, a
// rebuild of the same index did not (#5521).
func TestForgetDropsTheKeptFileOfADeletedCopy(t *testing.T) {
	h := newTwoWayEnv(t)
	a := filepath.Join(h.tmp, "a.db")
	// Tombstones live under XDG_CONFIG_HOME, which the suite shares: an
	// earlier test that forgot an s3 would leave this one nothing to forget.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h.tmp, "config"))
	pass := func() {
		t.Helper()
		if err := Ensure(a, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	h.put("app", "s1", hPrompt("app", "s1", 2, 0, "why does the pool fail"))
	h.put("app", "s3", hPrompt("app", "s3", 3, 0, "why does the queue fail"))
	pass()
	h.put("fork", "s3", hPrompt("fork", "s3", 3, 0, "copy: why does the queue fail")+
		hBash("fork", "s3", 3, 1, "c1", "make migrate", "ok", false))
	pass()
	if err := os.Remove(h.path("fork", "s3")); err != nil {
		t.Fatal(err)
	}
	pass()
	if _, err := Forget(a, ForgetOptions{Session: "s3"}); err != nil {
		t.Fatal(err)
	}
	pass()
	b := filepath.Join(h.tmp, "b.db")
	copyDir(t, a, b)
	if err := Ensure(b, "", true, nil); err != nil {
		t.Fatal(err)
	}
	diffSnaps(t, "forget after a deleted copy", takeSnap(t, a), takeSnap(t, b))
}

// Every command reads the elided id a result line prints (#853) — forget was
// the last one that did not, answering "nothing was changed" about a session
// that is there (#855). Of all the commands to silently match nothing, this is
// the worst one.
func TestForgetAndUnforgetTakeAnElidedId(t *testing.T) {
	root, dir := allHarnessEnv(t)
	long := "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0"
	write(t, filepath.Join(root, "claude", "-tmp-p", "s.jsonl"),
		claudeLine(long, "2026-01-02T03:04:05Z", "manneedle here"))
	if err := EnsureForSearch(dir, search.Options{Query: "manneedle", All: true}, false, nil); err != nil {
		t.Fatal(err)
	}

	// Exactly what short() prints for a 40-character id.
	elided := "a1b2c3d4e…d6e7f8a9b0"
	dry, err := Forget(dir, ForgetOptions{Session: elided, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if dry.Sessions != 1 {
		t.Errorf("dry run matched %d sessions with the id from the screen, want 1", dry.Sessions)
	}

	// An elision that belongs to no session still matches nothing.
	if miss, forgetErr := Forget(dir, ForgetOptions{Session: "zzzz…zzzz", DryRun: true}); forgetErr != nil {
		t.Fatal(forgetErr)
	} else if miss.Sessions != 0 {
		t.Errorf("an elision matching nothing dropped %d sessions", miss.Sessions)
	}

	// And the round trip: forget by the printed id, restore by the same string.
	if got, forgetErr := Forget(dir, ForgetOptions{Session: elided}); forgetErr != nil {
		t.Fatal(forgetErr)
	} else if got.Sessions != 1 {
		t.Fatalf("forget matched %d sessions", got.Sessions)
	}
	lifted, err := Unforget(dir, elided, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lifted != 1 {
		t.Errorf("unforget lifted %d tombstones with the id from the screen, want 1", lifted)
	}
	// Other tests in this package share the tombstone file; what matters is
	// that this session is not among what is left.
	for _, key := range Tombstones() {
		if key == "claude:"+long {
			t.Errorf("the restored session is still tombstoned: %v", Tombstones())
		}
	}
}

// A note has no id that survives the crossing: buckets are the reader's local
// day (#911), so the peer sends the same line under an id this machine has
// never seen and the tombstone missed it — deja announced back the line that
// was just deleted (#985).
func TestAForgottenNoteStaysGoneWhenAPeerSendsTheirOwnBucket(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	when := time.Date(2026, 8, 4, 10, 32, 12, 0, time.UTC)
	body := `{"ts":"` + when.Format(time.RFC3339Nano) + `","project":"x29","text":"the ticker window stays at 30s"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	local := ""
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if strings.HasPrefix(m.ID, "deja-20") {
			local = m.ID
		}
	}
	if local == "" {
		t.Fatal("the note did not land in a day bucket")
	}
	if _, err = Forget(dir, ForgetOptions{Session: "deja:" + local}); err != nil {
		t.Fatal(err)
	}

	// The peer's copy: same text, same instant, their rendering of the day,
	// which is the other side of midnight from ours. A fixed 08-03 was this
	// machine's own bucket west of UTC-10, and the test then sent no peer id.
	peer := "deja-2026-08-03-x29"
	if local == peer {
		peer = "deja-2026-08-04-x29"
	}
	exp := filepath.Join(tmp, "transfer")
	if err = os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(SyncRecord{Harness: "deja", SessionID: peer,
		Project: "x29", Role: "user", Text: "the ticker window stays at 30s", Time: when})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(exp, "deja-sync.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := Import(dir, exp)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("the forgotten note came back under the peer's bucket: %d records", n)
	}
	if ImportSkippedForgotten() != 1 {
		t.Errorf("the drop was not reported as a forgotten one: %d", ImportSkippedForgotten())
	}

	// The list someone reads names the note once, not once per record.
	for _, k := range Tombstones() {
		if strings.Contains(k, "\x00") {
			t.Errorf("a content key reached the list people read: %q", k)
		}
	}
	if got := TombstoneMatches("deja:" + local); got != 1 {
		t.Errorf("one forgotten note counted as %d sessions", got)
	}

	// Bringing it back has to bring back what the peer still sends, or the
	// next import would drop it in silence.
	if lifted, err := Unforget(dir, "deja:"+local, nil); err != nil || lifted != 1 {
		t.Fatalf("unforget lifted %d: %v", lifted, err)
	}
	if n, err := Import(dir, exp); err != nil || n != 1 {
		t.Errorf("after unforget the peer's copy still could not land: %d records, %v", n, err)
	}
}

// Forget counts the user's own writing apart from transcripts, and the two
// shapes deja files under its own harness are not the same thing: a day bucket
// comes from `remember`, a deja-note- session from `promote`. Calling a day of
// notes a promoted note names something the reader may never have done (#957).
func TestForgetCountsPromotedNotesApartFromDayBuckets(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)

	store := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"s1","timestamp":"2026-07-11T10:00:00Z","cwd":"/p","message":{"role":"user","content":"a session about the ticker"}}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"ts":"2026-07-12T09:00:00Z","project":"p","text":"a note of my own"}` + "\n" +
		`{"ts":"2026-07-12T10:00:00Z","project":"p","text":"the decision that held","kind":"promoted","session":"claude:s1","state":"accepted","title":"decision"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	got, err := Forget(dir, ForgetOptions{Project: "p", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Notes != 2 {
		t.Fatalf("counted %d of the user's own sessions, want 2 (a day bucket and a promoted note)", got.Notes)
	}
	if got.Promoted != 1 {
		t.Errorf("counted %d promoted notes, want 1 — the day bucket is not one", got.Promoted)
	}
}

// The undo has to be able to refuse the way forget does, so the count and the
// action share one selector (#961).
func TestTombstoneMatchesCountsWhatUnforgetWouldLift(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	store := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"ab-one", "ab-two", "zz-other"} {
		rec := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-07-1` + string(rune('1'+i)) + `T10:00:00Z","cwd":"/p","message":{"role":"user","content":"session ` + id + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Forget(dir, ForgetOptions{Session: "ab"}); err != nil {
		t.Fatal(err)
	}

	if n := TombstoneMatches("ab"); n != 2 {
		t.Errorf("TombstoneMatches(\"ab\") = %d, want 2", n)
	}
	if n := TombstoneMatches("claude:"); n != 2 {
		t.Errorf("a harness-scoped prefix counted %d, want 2", n)
	}
	if n := TombstoneMatches("claude:ab-one"); n != 1 {
		t.Errorf("one exact key counted %d, want 1", n)
	}
	if n := TombstoneMatches("nothing-like-this"); n != 0 {
		t.Errorf("a selector matching nothing counted %d", n)
	}
	// And what it counted is what Unforget lifts.
	lifted, err := Unforget(dir, "ab", nil)
	if err != nil {
		t.Fatal(err)
	}
	if lifted != 2 {
		t.Errorf("Unforget lifted %d where the count said 2", lifted)
	}
}

// Forgetting removes the local copy and keeps the session out of later pushes,
// but a machine that already has it keeps it. Saying nothing read as "it is
// gone everywhere" to someone forgetting a customer name (#788).
func TestForgetReportsWhereItWasAlreadyPushed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.jsonl")
	started := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	m := Manifest{Sessions: map[string]SessionMeta{
		"claude:s1": {ID: "s1", Harness: "claude", Path: path, Started: started, Updated: started},
	}}
	matched := map[string]bool{"claude:s1": true}

	// Nothing pushed anywhere.
	if peers, exported := pushedTo(m, matched); len(peers) != 0 || exported {
		t.Errorf("with no watermarks: peers=%v exported=%v", peers, exported)
	}

	// A directory export carries no peer name.
	m.ExportWatermarks = map[string]int64{path: started.UnixNano()}
	if peers, exported := pushedTo(m, matched); len(peers) != 0 || !exported {
		t.Errorf("after a directory export: peers=%v exported=%v", peers, exported)
	}

	// Named peers are named, and a watermark older than the session is not a
	// push of it.
	m.ExportWatermarks = map[string]int64{
		"mini\x00" + path:  started.UnixNano(),
		"work\x00" + path:  started.Add(time.Hour).UnixNano(),
		"older\x00" + path: started.Add(-time.Hour).UnixNano(),
	}
	peers, exported := pushedTo(m, matched)
	if exported {
		t.Error("named peers were reported as an unnamed export")
	}
	if len(peers) != 2 || peers[0] != "mini" || peers[1] != "work" {
		t.Errorf("peers = %v, want [mini work]", peers)
	}
}
