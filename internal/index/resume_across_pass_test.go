package index

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func kiroPrompt(id string, ts int64, text string) string {
	return fmt.Sprintf(`{"version":"v1","kind":"Prompt","data":{"message_id":%q,"content":[{"kind":"text","data":%q}],"meta":{"timestamp":%d}}}`, id, text, ts) + "\n"
}

// kiro-cli 2.22 stamps only the Prompt.
func kiroAnswer(id, text string) string {
	return fmt.Sprintf(`{"version":"v1","kind":"AssistantMessage","data":{"message_id":%q,"content":[{"kind":"text","data":%q}]}}`, id, text) + "\n"
}

func kiroCLISession(t *testing.T) (dir, transcript string) {
	t.Helper()
	tmp := t.TempDir()
	isolateStores(t, tmp)
	root := filepath.Join(tmp, "kiro")
	t.Setenv("DEJA_KIRO_ROOT", root)
	t.Setenv("DEJA_KIRO_DB", filepath.Join(tmp, "absent.sqlite3"))
	transcript = filepath.Join(root, "cli", "s-retry.jsonl")
	writeAt(t, filepath.Join(root, "cli", "s-retry.json"), `{"session_id":"s-retry","cwd":"/tmp/proj"}`, time.Now())
	return filepath.Join(tmp, "index.db"), transcript
}

// A reply or tool call takes the time of the Prompt before it. On a read
// resumed after a pass that Prompt was behind the offset, and what was
// appended landed at 0001-01-01 (#4444).
func TestKiroCLIRecordAppendedAfterAPassKeepsThePromptsTime(t *testing.T) {
	dir, transcript := kiroCLISession(t)
	writeAt(t, transcript, kiroPrompt("p1", 1785600100, "fix the retry loop"), time.Now())
	indexPass(t, dir)

	appendTo(t, transcript, `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m4","content":[{"kind":"text","data":"vetting it"},{"kind":"toolUse","data":{"toolUseId":"t3","name":"shell","input":{"command":"go vet ./retry"}}}]}}`+"\n")
	indexPass(t, dir)
	matchesRebuild(t, dir, "kiro", "s-retry")
	s, _, _ := FindByIdentity(dir, "kiro", "s-retry")
	for _, m := range s.Messages {
		if m.Time.IsZero() {
			t.Errorf("%s %q has no time", m.Role, m.Text)
		}
	}
}

// A reply streams in as records sharing a message id. A pass between two of
// them stored the halves as two replies; a rebuild joins them (#4445).
func TestKiroCLIReplyStreamedAcrossAPassIsOneMessage(t *testing.T) {
	dir, transcript := kiroCLISession(t)
	writeAt(t, transcript, kiroPrompt("p3", 1785600200, "and the backoff?")+kiroAnswer("a3", "The backoff "), time.Now())
	indexPass(t, dir)
	appendTo(t, transcript, kiroAnswer("a3", "doubles each attempt."))
	indexPass(t, dir)
	matchesRebuild(t, dir, "kiro", "s-retry")
}

// The next reply has its own id and is appended as before.
func TestKiroCLINextReplyAfterAPassIsItsOwn(t *testing.T) {
	dir, transcript := kiroCLISession(t)
	writeAt(t, transcript, kiroPrompt("p3", 1785600200, "and the backoff?")+kiroAnswer("a3", "The backoff doubles."), time.Now())
	indexPass(t, dir)
	appendTo(t, transcript, kiroPrompt("p4", 1785600300, "and the cap?")+kiroAnswer("a4", "Three attempts."))
	indexPass(t, dir)
	matchesRebuild(t, dir, "kiro", "s-retry")
	if s, _, _ := FindByIdentity(dir, "kiro", "s-retry"); len(s.Messages) != 4 {
		t.Errorf("%d messages, want 4", len(s.Messages))
	}
}

func grokChunk(ts int, update, meta string) string {
	return fmt.Sprintf(`{"timestamp":%d,"method":"session/update","params":{"update":%s,"_meta":%s}}`, 1784300000+ts, update, meta) + "\n"
}

func grokUser(ts, idx int, text string) string {
	return grokChunk(ts, fmt.Sprintf(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":%q},"_meta":{"promptIndex":%d}}`, text, idx), "{}")
}

func grokAgent(ts int, prompt, text string) string {
	return grokChunk(ts, fmt.Sprintf(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":%q}}`, text), fmt.Sprintf(`{"promptId":%q}`, prompt))
}

// Grok joins agent chunks that share a promptId, across the tool records
// between them. A pass mid-reply stored two replies (#4445).
func TestGrokReplyStreamedAcrossAPassIsOneMessage(t *testing.T) {
	tmp := t.TempDir()
	isolateStores(t, tmp)
	root := filepath.Join(tmp, "grok")
	t.Setenv("DEJA_GROK_ROOT", root)
	t.Setenv("DEJA_GROK_DB", filepath.Join(tmp, "absent.db"))
	updates := filepath.Join(root, "sessions", "%2Ftmp%2Fproj", "s-retry", "updates.jsonl")
	writeAt(t, updates, grokUser(30, 2, "and the backoff?")+grokAgent(31, "p2", "the backoff "), time.Now())
	dir := filepath.Join(tmp, "index.db")
	indexPass(t, dir)
	appendTo(t, updates, grokChunk(32, `{"sessionUpdate":"tool_call","toolCallId":"call-3","title":"read_file","rawInput":{"target_file":"/tmp/proj/retry.go"},"_meta":{"x.ai/tool":{"name":"read_file"}}}`, `{"promptId":"p2"}`)+
		grokAgent(33, "p2", "doubles each attempt"))
	indexPass(t, dir)
	matchesRebuild(t, dir, "grok", "s-retry")

	// The next prompt's reply is its own.
	appendTo(t, updates, grokUser(40, 3, "and the cap?")+grokAgent(41, "p3", "three attempts"))
	indexPass(t, dir)
	matchesRebuild(t, dir, "grok", "s-retry")
}

// Kimi streams a reply as content.part events and joins them at step.end. A
// pass before the step ended flushed what had arrived, and the rest became a
// second reply (#4445).
func TestKimiReplyStreamedAcrossAPassIsOneMessage(t *testing.T) {
	tmp := t.TempDir()
	isolateStores(t, tmp)
	root := filepath.Join(tmp, "kimi")
	t.Setenv("DEJA_KIMI_ROOT", root)
	session := filepath.Join(root, "sessions", "wd_proj_0123456789ab", "session_retry01")
	wire := filepath.Join(session, "agents", "main", "wire.jsonl")
	at := func(sec int) int64 { return 1788253200000 + int64(sec)*1000 }
	ev := func(sec int, e string) string {
		return fmt.Sprintf(`{"type":"context.append_loop_event","event":%s,"time":%d}`, e, at(sec)) + "\n"
	}
	part := func(sec int, text string) string {
		return ev(sec, fmt.Sprintf(`{"type":"content.part","part":{"type":"text","text":%q}}`, text))
	}
	writeAt(t, filepath.Join(session, "state.json"), `{"title":"why does the retry loop spin","workDir":"/tmp/proj"}`, time.Now())
	writeAt(t, wire, `{"type":"metadata","protocol_version":"1.4"}`+"\n"+
		fmt.Sprintf(`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"why does the retry loop spin"}],"toolCalls":[]},"time":%d}`, at(0))+"\n"+
		ev(1, `{"type":"step.begin"}`)+part(2, "The loop never sleeps; "), time.Now())
	dir := filepath.Join(tmp, "index.db")
	indexPass(t, dir)
	appendTo(t, wire, part(3, "add a backoff before the next attempt.")+ev(4, `{"type":"step.end"}`))
	indexPass(t, dir)
	matchesRebuild(t, dir, "kimi", "session_retry01")

	// A step that begins after a pass is its own reply.
	appendTo(t, wire, ev(5, `{"type":"step.begin"}`)+part(6, "Done."))
	indexPass(t, dir)
	matchesRebuild(t, dir, "kimi", "session_retry01")

	// A user turn that lands mid-stream goes ahead of the unfinished reply.
	appendTo(t, wire, fmt.Sprintf(`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"and the cap?"}],"toolCalls":[]},"time":%d}`, at(7))+"\n")
	indexPass(t, dir)
	matchesRebuild(t, dir, "kimi", "session_retry01")
}
