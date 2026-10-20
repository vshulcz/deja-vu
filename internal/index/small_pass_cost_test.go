package index

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// smallPassStore writes n Claude Code sessions of four turns each, every turn
// a question, a command and its output, and builds an index over them. It
// returns the index and a function that rewrites session i shorter.
func smallPassStore(t *testing.T, n int) (string, func(i int)) {
	t.Helper()
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	loc := time.Local
	t.Cleanup(func() { time.Local = loc })
	time.Local = time.UTC
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	session := func(i, turns int) {
		id := fmt.Sprintf("s%02d", i)
		var body string
		for turn := 0; turn < turns; turn++ {
			ts := fmt.Sprintf("%02d:%02d", turn, i)
			tid := fmt.Sprintf("t%d", turn)
			body += parityClaudeLine(id, ts+"Z", "user", fmt.Sprintf(`"why does step %d of the migration hang"`, turn)) +
				parityClaudeLine(id, ts+"Z", "assistant", `[{"type":"tool_use","id":"`+tid+`","name":"Bash","input":{"command":"make migrate STEP=`+fmt.Sprint(turn)+`"}}]`) +
				parityClaudeLine(id, ts+"Z", "user", `[{"type":"tool_result","tool_use_id":"`+tid+`","content":"dial tcp 127.0.0.1:5432: connect: connection refused\nmake: *** [migrate] Error 1"}]`)
		}
		p := filepath.Join(proj, id+".jsonl")
		parityWrite(t, p, body, false)
		later := time.Now().Add(time.Duration(i+10*(4-turns)) * time.Second)
		_ = os.Chtimes(p, later, later)
	}
	for i := 0; i < n; i++ {
		session(i, 4)
	}
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	return inc, func(i int) { session(i, 3) }
}

// A pass that rewrites one session of many reads that session's records, not
// the whole log: the rest stay where they are, and the offset table says
// where. Walking the log was a second of a 20-session pass at 40,000.
func TestAReplacingPassReadsOnlyTheSessionsItChanges(t *testing.T) {
	const n = 12
	inc, shorten := smallPassStore(t, n)
	walked := storedRecordsWalked.Load()
	shorten(5)
	parityPass(t, inc, false)
	walked = storedRecordsWalked.Load() - walked
	if walked == 0 {
		t.Fatal("no replacing pass ran, so this measures nothing")
	}
	// The changed session's twelve records, out of n*12.
	if walked > 12 {
		t.Errorf("the pass walked %d stored records, want the changed session's 12", walked)
	}
	sameAsRebuild(t, inc)

	// The control: without the offset table the pass walks the log.
	if err := os.Remove(filepath.Join(inc, recordOffsetsFile)); err != nil {
		t.Fatal(err)
	}
	walked = storedRecordsWalked.Load()
	shorten(6)
	parityPass(t, inc, false)
	if walked = storedRecordsWalked.Load() - walked; walked < (n-1)*12 {
		t.Errorf("with no offset table the pass walked %d records, want them all, so the counter misses the walk", walked)
	}
	sameAsRebuild(t, inc)
}

// The command table and the session facts are mined again only from the
// sessions a pass changed, the first pass after a build included.
func TestAReplacingPassMinesOnlyTheChangedSessionsCommands(t *testing.T) {
	const n = 12
	inc, shorten := smallPassStore(t, n)
	mined := commandRecordsMined.Load()
	shorten(3)
	parityPass(t, inc, false)
	if mined = commandRecordsMined.Load() - mined; mined > 3 {
		t.Errorf("the command tables read %d command records, want the changed session's 3", mined)
	}
	if len(ReadCommands(inc)) == 0 || len(ReadSessionFacts(inc)) != n {
		t.Fatalf("commands %v, facts for %d sessions: this measures nothing", ReadCommands(inc), len(ReadSessionFacts(inc)))
	}
	sameAsRebuild(t, inc)

	// The control: without the state every command is read again.
	if err := os.Remove(commandsStatePath(inc)); err != nil {
		t.Fatal(err)
	}
	mined = commandRecordsMined.Load()
	shorten(4)
	parityPass(t, inc, false)
	if mined = commandRecordsMined.Load() - mined; mined < (n-2)*4 {
		t.Errorf("with no state the tables read %d command records, want them all, so the counter misses the walk", mined)
	}
	sameAsRebuild(t, inc)
}

// Resolving a schema store's database stats files, and a pass met every
// transcript's path for the first time: a second of stats on 40,000 of them.
func TestAReplacingPassResolvesTheSchemaStoresOnce(t *testing.T) {
	const n = 12
	inc, shorten := smallPassStore(t, n)
	var asked atomic.Int64
	saved := opencodeSchemaDBs
	t.Cleanup(func() { opencodeSchemaDBs = saved })
	counting := map[string]func() string{}
	for h, db := range saved {
		counting[h] = func() string { asked.Add(1); return db() }
	}
	opencodeSchemaDBs = counting
	// Walked whole, as a pass that cannot name its sessions walks it, so
	// every transcript's records are judged.
	if err := os.Remove(filepath.Join(inc, recordOffsetsFile)); err != nil {
		t.Fatal(err)
	}
	shorten(2)
	parityPass(t, inc, false)
	if got, most := asked.Load(), int64(4*len(saved)); got > most {
		t.Errorf("the pass resolved the schema databases %d times, want at most %d", got, most)
	}
}

// A word the dead records shared with live ones is known live from the start
// of its postings; reading each one whole made the scrub cost the size of the
// common words' postings.
func TestTheScrubReadsTheStartOfALiveWordsPostings(t *testing.T) {
	const n = 60
	inc, shorten := smallPassStore(t, n)
	// Dead records from earlier passes raise the size below which a word's
	// postings are read at all.
	for i := 0; i < n/6; i++ {
		shorten(i)
	}
	parityPass(t, inc, false)
	probed := postingBytesProbed.Load()
	shorten(n - 1)
	parityPass(t, inc, false)
	probed = postingBytesProbed.Load() - probed
	if probed > 4*liveProbeBytes {
		t.Errorf("the scrub read %d bytes of postings, want no more than the start of each word's", probed)
	}
	sameAsRebuild(t, inc)
}

// The facts are carried with the state they were written beside. A facts
// file that went missing is not read as "no session ran anything".
func TestCarriedCommandFactsMatchTheirState(t *testing.T) {
	inc, shorten := smallPassStore(t, 6)
	if err := os.Remove(sessionFactsPath(inc)); err != nil {
		t.Fatal(err)
	}
	shorten(1)
	parityPass(t, inc, false)
	if got := len(ReadSessionFacts(inc)); got != 6 {
		t.Errorf("facts for %d sessions after the pass, want 6", got)
	}
	sameAsRebuild(t, inc)
}
