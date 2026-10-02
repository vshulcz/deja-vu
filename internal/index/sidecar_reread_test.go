package index

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// onlySession names the one session the index holds.
func onlySession(t *testing.T, dir string) (harness, id string) {
	t.Helper()
	metas, err := AllMeta(dir)
	if err != nil || len(metas) != 1 {
		t.Fatalf("want one session in the index, have %d (%v)", len(metas), err)
	}
	return metas[0].Harness, metas[0].ID
}

// Five readers take a session's title, workspace or start from a file beside
// the transcript, which the agent writes on its own: late, after the
// transcript, and again on a rename. A change to that file alone never
// reached the incremental index, only a rebuild (#4446, after #4319 for
// cline-sdk).
func TestASidecarChangeAloneIsReread(t *testing.T) {
	rooTask := `[{"role":"user","content":[{"type":"text","text":"<task>\nfix the retry loop\n</task>"}],"ts":1790762400000},{"role":"assistant","content":[{"type":"text","text":"looking at the retry loop"}],"ts":1790762410000}]`
	rooItem := func(id string) func(string) string {
		return func(title string) string {
			return fmt.Sprintf(`{"id":%q,"number":1,"ts":1790762460000,"task":%q,"workspace":"/tmp/proj"}`, id, title)
		}
	}
	cases := []struct {
		name       string
		env        map[string]string // values are under the store root
		transcript string            // relative to the store root
		body       string
		sidecar    string // relative to the store root
		meta       func(title string) string
	}{{
		name:       "kilocode-task",
		env:        map[string]string{"DEJA_KILO_ROOTS": "kilo", "DEJA_KILO_DB": "absent-kilo.db"},
		transcript: "kilo/tasks/0b7c1f9e-2d3a-4e5f-8a9b-1c2d3e4f5a6b/api_conversation_history.json",
		body:       rooTask,
		sidecar:    "kilo/tasks/0b7c1f9e-2d3a-4e5f-8a9b-1c2d3e4f5a6b/history_item.json",
		meta:       rooItem("0b7c1f9e-2d3a-4e5f-8a9b-1c2d3e4f5a6b"),
	}, {
		name:       "roo",
		env:        map[string]string{"DEJA_ROO_ROOTS": "roo", "DEJA_ROO_CLI_ROOT": "roocli"},
		transcript: "roo/tasks/1790762400000/api_conversation_history.json",
		body:       rooTask,
		sidecar:    "roo/tasks/1790762400000/history_item.json",
		meta:       rooItem("1790762400000"),
	}, {
		name:       "cline-vscode",
		env:        map[string]string{"DEJA_CLINE_ROOT": "none", "DEJA_CLINE_ROOTS": "cline-ext"},
		transcript: "cline-ext/tasks/1767300000000/api_conversation_history.json",
		body:       `[{"role":"user","content":[{"type":"text","text":"<task>\nfix the retry loop\n</task>"}]},{"role":"assistant","content":[{"type":"text","text":"looking at the retry loop"}]}]`,
		sidecar:    "cline-ext/state/taskHistory.json",
		meta: func(title string) string {
			return fmt.Sprintf(`[{"id":"1767999999999","ts":1767999999999,"task":"another task"},{"id":"1767300000000","ts":1767300000000,"task":%q,"cwdOnTaskInitialization":"/tmp/proj","tokensIn":12}]`, title)
		},
	}, {
		name:       "reasonix",
		env:        map[string]string{"DEJA_REASONIX_ROOT": "rx"},
		transcript: "rx/projects/-tmp-proj/sessions/20260930-100000.000000000-deepseek-chat.jsonl",
		body: `{"role":"user","content":"fix the retry loop","createdAt":1790762410000}` + "\n" +
			`{"role":"assistant","content":"looking at the retry loop","createdAt":1790762420000}` + "\n",
		sidecar: "rx/projects/-tmp-proj/sessions/20260930-100000.000000000-deepseek-chat.jsonl.meta",
		meta: func(title string) string {
			return fmt.Sprintf(`{"id":"20260930-100000.000000000-deepseek-chat","created_at":"2026-09-30T10:00:00Z","updated_at":"2026-09-30T10:00:20Z","workspace_root":"/tmp/proj","custom_title":%q}`, title)
		},
	}, {
		name:       "kimi",
		env:        map[string]string{"DEJA_KIMI_ROOT": "kimi"},
		transcript: "kimi/sessions/wd_proj_0123456789ab/session_retry01/agents/main/wire.jsonl",
		body: `{"type":"metadata","protocol_version":"1.4"}` + "\n" +
			`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}],"toolCalls":[]},"time":1788253201000}` + "\n",
		sidecar: "kimi/sessions/wd_proj_0123456789ab/session_retry01/state.json",
		meta: func(title string) string {
			return fmt.Sprintf(`{"createdAt":"2026-09-01T09:00:00.000Z","updatedAt":"2026-09-01T09:00:02.000Z","title":%q,"workDir":"/tmp/proj"}`, title)
		},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			isolateStores(t, tmp)
			root := filepath.Join(tmp, "store")
			for k, v := range tc.env {
				t.Setenv(k, filepath.Join(root, v))
			}
			at := time.Now().Add(-time.Hour)
			writeAt(t, filepath.Join(root, tc.transcript), tc.body, at)
			dir := filepath.Join(tmp, "index.db")
			indexPass(t, dir)
			harness, id := onlySession(t, dir)

			// The sidecar lands after the transcript, the order Roo and Kilo
			// write a new task in.
			sidecar := filepath.Join(root, tc.sidecar)
			writeAt(t, sidecar, tc.meta("fix the retry loop"), at.Add(time.Minute))
			indexPass(t, dir)
			matchesRebuild(t, dir, harness, id)

			// A rename rewrites it alone, same size.
			writeAt(t, sidecar, tc.meta("cap the retry at three"), at.Add(2*time.Minute))
			indexPass(t, dir)
			matchesRebuild(t, dir, harness, id)
		})
	}
}
