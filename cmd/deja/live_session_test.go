package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// liveAndPriorStore is the shape both reports describe: a finished session that
// answers the question, and the session asking it — indexed, because the harness
// writes each turn to disk as it happens.
func liveAndPriorStore(t *testing.T) (dir, liveID string) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-w-p")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	prior := `{"type":"user","sessionId":"prior-1","cwd":"/w/p","timestamp":"2026-08-04T09:00:00Z","message":{"role":"user","content":"the golden test in internal/store never runs, make test just skips it"}}` + "\n" +
		`{"type":"assistant","sessionId":"prior-1","cwd":"/w/p","timestamp":"2026-08-04T09:02:00Z","message":{"role":"assistant","content":"Run SVC_FIXTURES=$PWD/fixtures make test — that runs the golden test in internal/store and it passes. The plain make test skips it because the fixture directory is unset."}}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "prior-1.jsonl"), []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}
	liveID = "live-9"
	now := time.Now().UTC().Format(time.RFC3339)
	live := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/w/p","timestamp":%q,"message":{"role":"user","content":"The golden test in internal/store never runs here. Find the exact command that runs it and proof that it passes."}}`, liveID, now) + "\n"
	if err := os.WriteFile(filepath.Join(store, liveID+".jsonl"), []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	dir = index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir, liveID
}

// An agent asked recall mid-session and was handed its own question: the live
// transcript is indexed, the best lexical match for the question is the
// question, and the session that holds the answer was off the page entirely
// (#3945, #3965). The hooks know whose session it is; recall now reads that.
func TestRecallDoesNotAnswerWithTheSessionThatIsAsking(t *testing.T) {
	dir, liveID := liveAndPriorStore(t)
	q := `{"query":"golden test internal/store never runs"}`

	// Control first: with nothing marked live the caller's own session is served,
	// which is the bug and also proof this fixture can produce it.
	before, err := callMCPTool(dir, "recall", json.RawMessage(q))
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(before, liveID) {
		t.Fatalf("the fixture never served the live session, so this proves nothing:\n%s", before)
	}

	markSessionLive(dir, liveID)
	after, err := callMCPTool(dir, "recall", json.RawMessage(q))
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if strings.Contains(after, liveID) {
		t.Errorf("the session being written is still on the page:\n%s", after)
	}
	if !strings.Contains(after, "SVC_FIXTURES") {
		t.Errorf("the session that holds the answer did not take its place:\n%s", after)
	}
}

// recall_context reads the same index through its own path, and an agent that
// followed the digest link would otherwise get its own session back.
func TestRecallContextAlsoLeavesOutTheLiveSession(t *testing.T) {
	dir, liveID := liveAndPriorStore(t)
	markSessionLive(dir, liveID)

	got, err := callMCPTool(dir, "recall_context", json.RawMessage(`{"query":"golden test internal/store never runs"}`))
	if err != nil {
		t.Fatalf("recall_context: %v", err)
	}
	if strings.Contains(got, liveID) {
		t.Errorf("the digest was built from the session being written:\n%s", got)
	}
}

// The mark is what the hooks write, so a hook call has to leave one: this is the
// join between the surface that knows the session id and the surface that does
// not.
func TestTheToolHookMarksTheSessionItWasCalledFor(t *testing.T) {
	dir, _ := liveAndPriorStore(t)
	payload := `{"hook_event_name":"PreToolUse","session_id":"hooked-7","cwd":"/w/p","tool_name":"Read","tool_input":{"file_path":"/w/p/internal/store/store.go"}}`
	var out strings.Builder
	if err := runHookTool(dir, strings.NewReader(payload), &out); err != nil {
		t.Fatalf("hook-tool: %v", err)
	}
	if !liveSessionIDs(dir)["hooked-7"] {
		t.Errorf("the hook left no mark: %v", readLiveSessions(dir))
	}
}

// A stamp expires. When it is wrong it hides a session that could have
// answered, so it stops counting the moment the window closes rather than
// waiting for something to clean it up.
func TestAStaleMarkStopsHidingTheSession(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().UTC().Add(-liveSessionWindow - time.Minute).Format(time.RFC3339)
	if err := os.WriteFile(liveSessionsPath(dir), []byte("gone-1 "+old+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if liveSessionIDs(dir)["gone-1"] {
		t.Error("a mark older than the window still counts as live")
	}
	// And the file does not grow without bound: the next write drops it.
	markSessionLive(dir, "fresh-1")
	rows := readLiveSessions(dir)
	if _, ok := rows["gone-1"]; ok {
		t.Errorf("the stale row survived a write: %v", rows)
	}
	if !liveSessionIDs(dir)["fresh-1"] {
		t.Errorf("the fresh row was not written: %v", rows)
	}
}

// The cap holds on a machine running many agents at once, and it keeps the ones
// that spoke most recently.
func TestTheLiveFileIsCapped(t *testing.T) {
	dir := t.TempDir()
	for i := range liveSessionsMax + 6 {
		markSessionLive(dir, fmt.Sprintf("s%02d", i))
	}
	if got := len(readLiveSessions(dir)); got > liveSessionsMax {
		t.Errorf("the file holds %d rows, over the %d cap", got, liveSessionsMax)
	}
}

// A machine with no hooks wired marks nothing, and every surface answers as it
// did before — including with the session being written, which is the honest
// state: deja was never told whose it is.
func TestWithNoMarksNothingIsDropped(t *testing.T) {
	dir, liveID := liveAndPriorStore(t)
	got, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"golden test internal/store never runs"}`))
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(got, liveID) {
		t.Errorf("a session nothing marked live was dropped anyway:\n%s", got)
	}
}

// Asked for a session by its id, recall still answers with it — including a
// live one. The id is the caller naming what it wants, where a query is the
// caller describing it, and the two are not the same ask: #3945 is about a
// question whose best lexical match is itself.
func TestAnIDStillResolvesToTheSessionBeingWritten(t *testing.T) {
	dir, liveID := liveAndPriorStore(t)
	markSessionLive(dir, liveID)

	got, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"`+liveID+`"}`))
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(got, liveID) {
		t.Errorf("an id the agent asked for by name answered with something else:\n%s", got)
	}
}

// A file half-written, hand-edited or left by an older build must not take a
// surface down with it, and must not hide a session on the strength of a line
// nothing can read.
func TestAnUnreadableMarkFileHidesNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(liveSessionsPath(dir), []byte("live-9\nlive-8 not-a-time\n\x00\x00 \nlive-7 "), 0o600); err != nil {
		t.Fatal(err)
	}
	if ids := liveSessionIDs(dir); len(ids) != 0 {
		t.Errorf("garbage read as live sessions: %v", ids)
	}
}

// Forgetting drops the marks with everything else: after a forget there is
// nothing left for them to hide.
func TestForgettingDropsTheMarks(t *testing.T) {
	dir := t.TempDir()
	markSessionLive(dir, "live-9")
	dropHookCaches(dir)
	if ids := liveSessionIDs(dir); len(ids) != 0 {
		t.Errorf("the marks survived a forget: %v", ids)
	}
}
