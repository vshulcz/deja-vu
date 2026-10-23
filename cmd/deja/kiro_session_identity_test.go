package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// seedKiroCLI writes a kiro-cli session the way 2.32 does: the header beside
// the transcript, a prompt and a reply. parent is "" for a top-level session.
func seedKiroCLI(t *testing.T, id, cwd, parent, prompt, reply string, at time.Time) {
	t.Helper()
	root := filepath.Join(os.Getenv("HOME"), ".kiro", "sessions", "cli")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	header := map[string]any{
		"session_id": id, "cwd": cwd, "title": prompt,
		"created_at": at.UTC().Format(time.RFC3339Nano), "updated_at": at.UTC().Format(time.RFC3339Nano),
		// 2.32 writes this on top-level sessions too; only the parent id says
		// a sub-agent ran it.
		"session_created_reason": "subagent",
	}
	if parent != "" {
		header["parent_session_id"] = parent
	}
	b, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, id+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	ts := at.Unix()
	lines := fmt.Sprintf(`{"version":"v1","kind":"Prompt","data":{"message_id":"%[1]s-p","content":[{"kind":"text","data":%[2]q}],"meta":{"timestamp":%[4]d}}}
{"version":"v1","kind":"AssistantMessage","data":{"message_id":"%[1]s-a","content":[{"kind":"text","data":%[3]q}]}}
`, id, prompt, reply, ts)
	if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A kiro-cli sub-agent runs as a session of its own, whose hooks name only
// that session; the session that spawned it is in the header beside its
// transcript as parent_session_id. Its digest led with that parent, which is
// live and is the one asking through it (#5153).
func TestAKiroSubAgentsDigestLeavesOutItsParent(t *testing.T) {
	tmp := hermeticEnv(t)
	now := time.Now()
	const parent = "136cf72a-585d-4047-997f-d37a0d0f194a"
	const child = "e012b5f2-0f80-45a3-a1f6-eccc522bb35e"
	cwd := filepath.Join(tmp, "app")
	seedKiroCLI(t, "0a1b2c3d-0000-4000-8000-000000000001", cwd, "", "the ledger export drops the last row", "the writer missed a final flush", now.Add(-2*time.Hour))
	seedKiroCLI(t, parent, cwd, "", "cap the pelicanquill jitter", "Decided: pelicanquill jitter capped at 250ms in jitter.go.", now.Add(-time.Minute))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	run := func(sid string) string {
		withHookStdin(t, `{"hook_event_name":"agentSpawn","cwd":"`+filepath.ToSlash(cwd)+`","session_id":"`+sid+`"}`)
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}
	if out := run("7f7f7f7f-0000-4000-8000-000000000002"); !strings.Contains(out, "pelicanquill") {
		t.Fatalf("the control did not recall the parent's session:\n%s", out)
	}
	// The sub-agent starts after the build: its agentSpawn is its first record.
	seedKiroCLI(t, child, cwd, parent, "review the pelicanquill jitter cap", "", now)
	out := run(child)
	if strings.Contains(out, "pelicanquill") {
		t.Errorf("the sub-agent's digest led with the session that spawned it:\n%s", out)
	}
	if !strings.Contains(out, "ledger export") {
		t.Errorf("leaving the parent out took the rest of the digest with it:\n%s", out)
	}
}

// The V2 engine has no session-end hook; stop fires after every turn with the
// session id. Unwired, a finished kiro-cli session kept its live stamp, and the
// next session's MCP recall left it out for twenty minutes (#5152).
func TestKiroAgentEndsItsSessionAtStop(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "kiro-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	var agent struct {
		Hooks map[string][]struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(kiroTestDir(t), "agents", "deja.json"))), &agent); err != nil {
		t.Fatal(err)
	}
	stop := agent.Hooks["stop"]
	if len(stop) != 1 || !strings.HasSuffix(stop[0].Command, " hook-session-end") {
		t.Fatalf("stop does not run hook-session-end: %+v", stop)
	}

	// The payload kiro-cli 2.32 sends to stop ends the stamp.
	dir := t.TempDir() + string(filepath.Separator)
	const sid = "8fdbff3f-1125-4ba4-a5ec-0b3e56b8bc11"
	markSessionLive(dir, sid)
	runHookSessionEnd(dir, strings.NewReader(`{"hook_event_name":"stop","cwd":"/tmp/app","session_id":"`+sid+`","assistant_response":"done"}`))
	if liveSessionIDs(dir)[sid] {
		t.Error("the stop payload left the session stamped live")
	}
}
