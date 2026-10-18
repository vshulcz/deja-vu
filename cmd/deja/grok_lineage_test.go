package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// grokSession writes one Grok Build 1.0.41 session under the sandbox's
// ~/.grok: summary.json, updates.jsonl with a user turn and a reply, and
// chat_history.jsonl when chat is given. extra lines go on the end of
// updates.jsonl.
func grokSession(t *testing.T, cwd, id, summaryExtra, text, chat string, extra ...string) string {
	t.Helper()
	dir := filepath.Join(sources.GrokRoot(), "sessions", url.PathEscape(cwd), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	summary := `{"info":{"id":"` + id + `","cwd":"` + cwd + `"},"created_at":"` + now.Add(-time.Minute).Format(time.RFC3339Nano) +
		`","updated_at":"` + now.Format(time.RFC3339Nano) + `"` + summaryExtra + `}`
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := now.Add(-30 * time.Second).Unix()
	lines := `{"timestamp":` + strconv.FormatInt(ts, 10) + `,"method":"session/update","params":{"sessionId":"` + id + `","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"` + text + `"}}}}` + "\n" +
		`{"timestamp":` + strconv.FormatInt(ts+1, 10) + `,"method":"session/update","params":{"sessionId":"` + id + `","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"noted: ` + text + `"}}}}` + "\n"
	for _, l := range extra {
		lines += l + "\n"
	}
	updates := filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(updates, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if chat != "" {
		if err := os.WriteFile(filepath.Join(dir, "chat_history.jsonl"), []byte(chat), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return updates
}

// Measured on Grok Build 1.0.41 through a stub model: the start digest goes out
// in a PreToolUse reminder that chat_history.jsonl keeps, so each `grok -r`
// sent it once more (three in one request after three resumes); a
// `--fork-session` copy got a third digest led by the session it was forked
// from; and a subagent's own session came back to its parent as prior work,
// because its summary names no parent — only the parent's subagent_spawned
// record does.
func TestGrokSessionIsNotRecalledToItsOwnLineage(t *testing.T) {
	withStatsStores(t)
	t.Setenv("DEJA_GROK_ROOT", t.TempDir())
	dir := index.DefaultDir()
	const cwd = "/work/billing"
	// What Grok writes: the reminder as one JSON string, newlines escaped.
	chatLine := func(text string) string {
		var b strings.Builder
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(map[string]any{"type": "user", "content": []any{map[string]any{"type": "text", "text": text}}}); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	digestChat := chatLine("<system-reminder>\nContext from PreToolUse hook 'global/deja:pre_tool_use[0].hooks[0]':\n" + frameRecall(sessionStartLead+"- a session") + "\n</system-reminder>")
	wideChat := chatLine("<system-reminder>\n" + frameRecall(wideRecallLead+"- a session") + "\n</system-reminder>")
	plainChat := chatLine("<user_query>hello</user_query>")
	// A tool's output that quotes the digest, as reading deja's own source
	// does, is encoded once more inside the tool result.
	quoted, _ := json.Marshal(frameRecall(sessionStartLead))
	quotedChat := chatLine("<user_query>read it</user_query>") + `{"type":"tool_result","content":` + strconv.Quote(string(quoted)) + "}\n"

	parent := grokSession(t, cwd, "01a1-parent", `,"session_kind":"headless"`, "invoice rounding is half-even zorblat", digestChat,
		`{"timestamp":1,"method":"_x.ai/session/update","params":{"sessionId":"01a1-parent","update":{"sessionUpdate":"subagent_spawned","subagent_id":"01a1-child","parent_session_id":"01a1-parent","child_session_id":"01a1-child"}}}`)
	grokSession(t, cwd, "01a1-child", `,"session_kind":"subagent","agent_name":"general-purpose"`, "probe the zorblat rounding", "")
	other := grokSession(t, cwd, "01a1-other", `,"session_kind":"headless"`, "an unrelated zorblat note", plainChat)
	// A fork of the parent, sorted first, carries a copy of its spawn record.
	grokSession(t, cwd, "00a0-fork", `,"session_kind":"headless","parent_session_id":"01a1-parent","forked_at":"2026-10-17T23:40:25Z"`,
		"invoice rounding is half-even zorblat", digestChat,
		`{"timestamp":1,"method":"_x.ai/session/update","params":{"sessionId":"00a0-fork","update":{"sessionUpdate":"subagent_spawned","subagent_id":"01a1-child","parent_session_id":"01a1-parent","child_session_id":"01a1-child"}}}`)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The parent asking: its own subagent is its own work.
	if got := askerLineage(dir, "01a1-parent", parent); !got["01a1-child"] {
		t.Errorf("parent's lineage leaves its subagent in its recall: %v", got)
	}
	// The subagent asking, by id only as its prompt hook and MCP call do.
	if got := askerLineage(dir, "01a1-child", ""); !got["01a1-parent"] {
		t.Errorf("subagent's lineage leaves its parent in its recall: %v", got)
	}
	if got := askerLineage(dir, "01a1-other", other); got["01a1-parent"] || got["01a1-child"] {
		t.Errorf("an unrelated session's lineage took in another family: %v", got)
	}

	// A fork made after the build, which is when its first hooks fire.
	fork := grokSession(t, cwd, "01a1-fork", `,"session_kind":"headless","parent_session_id":"01a1-parent","forked_at":"2026-10-17T23:40:25Z"`,
		"invoice rounding is half-even zorblat", digestChat)
	if got := askerLineage(dir, "01a1-fork", fork); !got["01a1-parent"] {
		t.Errorf("fork's lineage leaves its source in its recall: %v", got)
	}
	if got := askerLineage(dir, "01a1-fork", ""); !got["01a1-parent"] {
		t.Errorf("fork's lineage by id leaves its source in its recall: %v", got)
	}

	// The digest the thread already holds is not sent again, on resume or in
	// a fork's copy; a thread that never got one still does.
	if !threadHasDigest(dir, "load", "01a1-parent", parent) {
		t.Error("a resumed grok session that holds its digest gets another")
	}
	if !threadHasDigest(dir, "load", "01a1-fork", fork) {
		t.Error("a grok fork that copied its source's digest gets another")
	}
	if threadHasDigest(dir, "load", "01a1-other", other) {
		t.Error("a resumed grok session that never got a digest is denied one")
	}
	wide := grokSession(t, cwd, "01a1-wide", `,"session_kind":"headless"`, "wide recall", wideChat)
	if !threadHasDigest(dir, "load", "01a1-wide", wide) {
		t.Error("a resumed grok session that holds a wide-recall digest gets another")
	}
	quote := grokSession(t, cwd, "01a1-quote", `,"session_kind":"headless"`, "reading deja", quotedChat)
	if threadHasDigest(dir, "load", "01a1-quote", quote) {
		t.Error("a tool output quoting the digest counted as the digest")
	}
}
