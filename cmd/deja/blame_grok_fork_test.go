package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// Grok 1.0.41 `--fork-session` copies the source's records into the fork's
// updates.jsonl under the fork's sessionId and outer timestamp, keeping the
// source's eventId (shape read off a live fork). The fork read them as its own
// turns, stamped later than the source, so blame gave the source's line to a
// fork that only said hi, and search listed the source's prompt twice.
func TestGrokForkKeepsOnlyItsOwnRecords(t *testing.T) {
	const (
		src  = "01a15000-0000-7000-8000-00000000000a"
		fork = "01a15000-0000-7000-8000-00000000000f"
		line = "func roundHalfEven(cents int64) int64 { return bankers(cents) }"
	)
	root := t.TempDir()
	t.Setenv("DEJA_GROK_ROOT", root)
	proj := filepath.Join(root, "work", "ledger")
	file := filepath.Join(proj, "ledger.go")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package ledger\n\n"+line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 18, 2, 20, 0, 0, time.UTC)
	tf := t0.Add(100 * time.Second)
	event := func(sid, eid string, at time.Time, update map[string]any) string {
		b := mustMarshal(t, map[string]any{"timestamp": at.Unix(), "method": "session/update", "params": map[string]any{
			"sessionId": sid, "update": update,
			"_meta": map[string]any{"eventId": eid, "agentTimestampMs": at.UnixMilli(), "promptId": eid}}})
		return string(b) + "\n"
	}
	turn := func(sid, owner string, at time.Time, user string, write bool) []string {
		out := []string{event(sid, owner+"-3", at, map[string]any{"sessionUpdate": "user_message_chunk",
			"content": map[string]any{"type": "text", "text": user}, "_meta": map[string]any{"promptIndex": 0}})}
		if write {
			out = append(out, event(sid, owner+"-8", at, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call_w1", "title": "write",
				"rawInput": map[string]any{"file_path": file, "content": "package ledger\n\n" + line + "\n"},
				"_meta":    map[string]any{"x.ai/tool": map[string]any{"version": 1, "name": "write", "kind": "edit"}}}))
		}
		return append(out, event(sid, owner+"-14", at.Add(2*time.Second), map[string]any{"sessionUpdate": "agent_message_chunk",
			"content": map[string]any{"type": "text", "text": "done"}}))
	}
	// A drive colon is no use in a directory name on Windows.
	group := filepath.Join(root, "sessions", strings.ReplaceAll(url.PathEscape(proj), ":", "%3A"))
	write := func(id string, summary map[string]any, lines []string) string {
		dir := filepath.Join(group, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		b := mustMarshal(t, summary)
		if err := os.WriteFile(filepath.Join(dir, "summary.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "updates.jsonl")
		if err := os.WriteFile(p, []byte(strings.Join(lines, "")), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	source := turn(src, src, t0, "Add banker rounding, codeword quillfen.", true)
	var copied []string
	for _, l := range source {
		var m map[string]any
		_ = json.Unmarshal([]byte(l), &m)
		m["timestamp"] = tf.Unix()
		params, ok := m["params"].(map[string]any)
		if !ok {
			t.Fatalf("no params in %s", l)
		}
		params["sessionId"] = fork
		b := mustMarshal(t, m)
		copied = append(copied, string(b)+"\n")
	}
	srcPath := write(src, map[string]any{"info": map[string]any{"id": src, "cwd": proj}, "session_kind": "headless",
		"created_at": t0.Format(time.RFC3339Nano)}, source)
	forkPath := write(fork, map[string]any{"info": map[string]any{"id": fork, "cwd": proj}, "session_kind": "headless",
		"created_at": tf.Format(time.RFC3339Nano), "parent_session_id": src, "forked_at": tf.Format(time.RFC3339Nano)},
		append(copied, turn(fork, fork, tf.Add(time.Second), "hi", false)...))

	parse := func(p string) model.Session {
		ss, err := sources.ParseGrokFile(p)
		if err != nil || len(ss) != 1 {
			t.Fatalf("parse %s: %v %d", p, err, len(ss))
		}
		return ss[0]
	}
	s, f := parse(srcPath), parse(forkPath)
	for _, m := range f.Messages {
		if strings.Contains(m.Text, "quillfen") || m.Role == sources.RoleWrote {
			t.Errorf("fork kept its source's record: %s %q", m.Role, m.Text)
		}
	}
	if len(f.Messages) == 0 || f.Messages[0].Text != "hi" {
		t.Errorf("fork lost its own turn: %+v", f.Messages)
	}

	target := search.BlameTarget{FullPath: file, Base: "ledger.go", Line: 3}
	commit := lineCommit{SHA: "d65793c9", When: tf.Add(time.Minute)}
	for _, ss := range [][]model.Session{{f, s}, {s, f}} {
		if got, ok := attributeLine(ss, target, commit, nil); !ok || got.Session.ID != src {
			t.Errorf("blame credited %q, want the source %s", got.Session.ID, src)
		}
	}

	// Control: what the fork writes itself is the fork's.
	forkPath = write(fork, map[string]any{"info": map[string]any{"id": fork, "cwd": proj}, "session_kind": "headless",
		"created_at": tf.Format(time.RFC3339Nano), "parent_session_id": src, "forked_at": tf.Format(time.RFC3339Nano)},
		append(copied, turn(fork, fork, tf.Add(time.Second), "write it again", true)...))
	if got, _ := attributeLine([]model.Session{s, parse(forkPath)}, target, commit, nil); got.Session.ID != fork {
		t.Errorf("the fork's own later write went to %q", got.Session.ID)
	}
	// Control: a session that is not a fork keeps a record under another
	// eventId; only a fork's copies are dropped.
	plain := write("01a15000-0000-7000-8000-0000000000b0", map[string]any{"info": map[string]any{"id": "01a15000-0000-7000-8000-0000000000b0", "cwd": proj},
		"session_kind": "headless"}, source)
	if got := parse(plain); len(got.Messages) != len(s.Messages) {
		t.Errorf("a non-fork lost records: %d, want %d", len(got.Messages), len(s.Messages))
	}
}
