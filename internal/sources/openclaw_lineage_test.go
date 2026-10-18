package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Shapes taken from an OpenClaw 2026.10.4 store after one sessions_spawn with
// context "isolated" and one with context "fork": the child's session_nodes
// entry names the requester (spawnedBySessionId, parent_session_key), and a
// fork child's header names the source before the source's copied entries.
const openclawSpawnSQL = `
CREATE TABLE session_nodes (session_key TEXT PRIMARY KEY, current_session_id TEXT NOT NULL, entry_json TEXT NOT NULL,
  parent_session_key TEXT, spawned_by TEXT);
CREATE TABLE session_windows (session_id TEXT PRIMARY KEY, session_key TEXT NOT NULL, created_at INTEGER NOT NULL,
  parent_session_key TEXT, spawned_by TEXT);
CREATE TABLE transcript_events (session_id TEXT NOT NULL, seq INTEGER NOT NULL, event_json TEXT, created_at INTEGER NOT NULL,
  PRIMARY KEY (session_id, seq));
INSERT INTO session_nodes VALUES('agent:main:explicit:p','p','{"sessionId":"p"}',NULL,NULL);
INSERT INTO session_nodes VALUES('agent:main:subagent:1','iso','{"sessionId":"iso","spawnedBy":"agent:main:explicit:p","spawnedBySessionId":"p"}','agent:main:explicit:p','agent:main:explicit:p');
INSERT INTO session_nodes VALUES('agent:main:subagent:2','frk','{"sessionId":"frk","spawnedBySessionId":"p","forkedFromParent":true}','agent:main:explicit:p','agent:main:explicit:p');
INSERT INTO session_nodes VALUES('agent:main:subagent:3','old','not json','agent:main:explicit:p',NULL);
INSERT INTO session_windows VALUES('p','agent:main:explicit:p',1000,NULL,NULL);
INSERT INTO session_windows VALUES('iso','agent:main:subagent:1',2000,'agent:main:explicit:p','agent:main:explicit:p');
INSERT INTO session_windows VALUES('frk','agent:main:subagent:2',3000,'agent:main:explicit:p','agent:main:explicit:p');
INSERT INTO session_windows VALUES('old','agent:main:subagent:3',4000,'agent:main:explicit:p',NULL);
INSERT INTO transcript_events VALUES('p',0,'{"type":"session","version":4,"id":"p","timestamp":"2026-10-18T05:37:20.000Z","cwd":"/work/app"}',1);
INSERT INTO transcript_events VALUES('p',1,'{"type":"message","id":"a","timestamp":"2026-10-18T05:37:25.000Z","message":{"role":"user","content":"remember the codeword FROSTBYTE"}}',2);
INSERT INTO transcript_events VALUES('p',2,'{"type":"message","id":"b","parentId":"a","timestamp":"2026-10-18T05:37:27.000Z","message":{"role":"assistant","content":[{"type":"text","text":"remembered"}]}}',3);
INSERT INTO transcript_events VALUES('iso',0,'{"type":"session","version":4,"id":"iso","timestamp":"2026-10-18T05:35:53.000Z","cwd":"/work/app"}',4);
INSERT INTO transcript_events VALUES('iso',1,'{"type":"message","id":"c","timestamp":"2026-10-18T05:35:54.000Z","message":{"role":"user","content":"<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>>\n\n[Subagent Context] You are running as a subagent (depth 1/5).\n\n[Subagent Task]\n\n<<<END_OPENCLAW_INTERNAL_CONTEXT>>>\n\nwrite the notes file\n\n<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>>\n\nBegin.\n\n<<<END_OPENCLAW_INTERNAL_CONTEXT>>>"}}',5);
INSERT INTO transcript_events VALUES('iso',2,'{"type":"message","id":"d","parentId":"c","timestamp":"2026-10-18T05:35:55.000Z","message":{"role":"assistant","content":[{"type":"text","text":"wrote notes"}]}}',6);
INSERT INTO transcript_events VALUES('frk',0,'{"type":"session","version":4,"id":"frk","timestamp":"2026-10-18T05:37:30.749Z","cwd":"/work/app","parentSession":"sqlite:main:p:/home/u/.openclaw/agents/main/agent/openclaw-agent.sqlite"}',7);
INSERT INTO transcript_events VALUES('frk',1,'{"type":"message","id":"a","timestamp":"2026-10-18T05:37:25.000Z","message":{"role":"user","content":"remember the codeword FROSTBYTE"}}',8);
INSERT INTO transcript_events VALUES('frk',2,'{"type":"message","id":"b","parentId":"a","timestamp":"2026-10-18T05:37:27.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"t1","name":"write","arguments":{"path":"parent.txt","content":"the parent wrote this before the fork happened"}}]}}',9);
INSERT INTO transcript_events VALUES('frk',3,'{"type":"message","id":"e","parentId":"b","timestamp":"2026-10-18T05:37:31.653Z","message":{"role":"user","content":"write the fork file"}}',10);
INSERT INTO transcript_events VALUES('frk',4,'{"type":"message","id":"f","parentId":"e","timestamp":"2026-10-18T05:37:33.000Z","message":{"role":"assistant","content":[{"type":"text","text":"wrote fork"}]}}',11);
INSERT INTO transcript_events VALUES('old',0,'{"type":"message","id":"g","timestamp":"2026-10-18T05:40:00.000Z","message":{"role":"user","content":"a child whose entry is unreadable"}}',12);
`

func openclawByID(ss []model.Session) map[string]model.Session {
	out := map[string]model.Session{}
	for _, s := range ss {
		out[s.ID] = s
	}
	return out
}

func TestOpenClawSubagentsHangUnderTheirParent(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	root := t.TempDir()
	db := filepath.Join(root, "main", "agent", "openclaw-agent.sqlite")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = strings.NewReader(openclawSpawnSQL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create store: %v: %s", err, out)
	}
	ss, err := ParseOpenClawDB(db)
	if err != nil {
		t.Fatal(err)
	}
	by := openclawByID(ss)
	if p := by["p"]; p.Kind != "" || p.Parent != "" {
		t.Errorf("the requester is nobody's child: kind=%q parent=%q", p.Kind, p.Parent)
	}
	for _, id := range []string{"iso", "frk", "old"} {
		if s := by[id]; s.Kind != "subagent" || s.Parent != "p" {
			t.Errorf("%s: kind=%q parent=%q, want a subagent of p", id, s.Kind, s.Parent)
		}
	}
	iso := by["iso"]
	if len(iso.Messages) == 0 || iso.Messages[0].Text != "write the notes file" {
		t.Errorf("iso first turn = %q, want the task without OpenClaw's runtime blocks", iso.Messages[0].Text)
	}
	frk := by["frk"]
	for _, m := range frk.Messages {
		if strings.Contains(m.Text, "FROSTBYTE") || strings.Contains(m.Text, "parent.txt") {
			t.Errorf("fork child holds its parent's copied entry: %+v", m)
		}
	}
	if len(frk.Messages) != 2 || frk.Messages[0].Text != "write the fork file" {
		t.Errorf("fork child messages = %+v, want only its own two turns", frk.Messages)
	}
}

// A JSONL store keeps the same entry fields in sessions.json, by key. A branch
// names its source in the header and nothing spawned it.
func TestOpenClawJSONLSubagentAndBranch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("sessions.json", `{"agent:main:main":{"sessionId":"p"},"agent:main:subagent:1":{"sessionId":"kid","spawnedBy":"agent:main:main"}}`)
	kid := write("kid.jsonl", `{"type":"session","version":3,"id":"kid","timestamp":"2026-07-20T09:00:00Z","cwd":"/work/app"}
{"type":"message","id":"a","timestamp":"2026-07-20T09:00:01Z","message":{"role":"user","content":"child task"}}
`)
	br := write("br.jsonl", `{"type":"session","version":3,"id":"br","timestamp":"2026-07-20T10:00:00Z","cwd":"/work/app","parentSession":"`+filepath.ToSlash(filepath.Join(dir, "p.jsonl"))+`"}
{"type":"message","id":"a","timestamp":"2026-07-20T09:30:00Z","message":{"role":"user","content":"copied from the source"}}
{"type":"message","id":"b","parentId":"a","timestamp":"2026-07-20T10:00:01Z","message":{"role":"user","content":"after the branch"}}
`)
	got, err := ParseOpenClawFile(kid)
	if err != nil || len(got) != 1 {
		t.Fatalf("kid: %v %v", got, err)
	}
	if got[0].Kind != "subagent" || got[0].Parent != "p" {
		t.Errorf("kid: kind=%q parent=%q, want a subagent of p", got[0].Kind, got[0].Parent)
	}
	got, err = ParseOpenClawFile(br)
	if err != nil || len(got) != 1 {
		t.Fatalf("br: %v %v", got, err)
	}
	if got[0].Kind != "fork" || got[0].Parent != "p" {
		t.Errorf("br: kind=%q parent=%q, want a fork of p", got[0].Kind, got[0].Parent)
	}
	if len(got[0].Messages) != 1 || got[0].Messages[0].Text != "after the branch" {
		t.Errorf("br messages = %+v, want only what came after the branch", got[0].Messages)
	}
}

func TestOpenClawParentSessionForms(t *testing.T) {
	for in, want := range map[string]string{
		"sqlite:main:q-parent2:/home/u/.openclaw/agents/main/agent/openclaw-agent.sqlite": "q-parent2",
		"/home/u/.openclaw/agents/main/sessions/abc.jsonl":                                "abc",
		"/home/u/.openclaw/agents/main/sessions/abc.jsonl.reset.2026-07-20T09-00-00.000Z": "abc",
		"prev-id": "prev-id",
		"  ":      "",
	} {
		if got := openclawParentSession(in); got != want {
			t.Errorf("openclawParentSession(%q) = %q, want %q", in, got, want)
		}
	}
	if got := openclawUserText("<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>> unclosed"); got != "<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>> unclosed" {
		t.Errorf("an unclosed block is kept, got %q", got)
	}
}
