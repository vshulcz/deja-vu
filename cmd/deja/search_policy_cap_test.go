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

// A rule that hides imported memory used to be applied after the result cap,
// so denied sessions still took the limited slots and the local sessions that
// matched never reached the page — an empty answer over a store that had ten
// of them (#1060).
func TestPolicyFilterRunsBeforeTheCap(t *testing.T) {
	tmp := hermeticEnv(t)

	// Imported sessions are newer, so they take the whole top of the ranking.
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	for i := 0; i < 20; i++ {
		rec := index.SyncRecord{
			Harness:   "claude",
			SessionID: fmt.Sprintf("peer-%02d", i),
			Project:   "work/api",
			Role:      "user",
			Text:      "the bearing seal on the peer machine",
			Time:      time.Date(2026, 6, i+1, 10, 0, 0, 0, time.UTC),
		}
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-peer.jsonl"), []byte(buf.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("local-%02d", i)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/tmp/local","timestamp":"2026-05-0%dT10:00:00Z","message":{"role":"user","content":"the bearing seal here at home"}}`, id, i+1)
		writeClaudeFixture(t, filepath.Join(root, "projects", "-tmp-local", id+".jsonl"), id, []string{line})
	}

	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out, err := captureRun(t, "search", "--json", "--limit", "5", "bearing seal")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Total    int  `json:"total"`
		Withheld int  `json:"policy_withheld"`
		Capped   bool `json:"capped"`
		Hits     []struct {
			Session struct {
				ID      string `json:"id"`
				Project string `json:"project"`
			} `json:"session"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(got.Hits) != 3 {
		t.Errorf("the rule ate the result slots: %d local sessions returned, want 3\n%s", len(got.Hits), out)
	}
	for _, h := range got.Hits {
		if strings.HasPrefix(h.Session.Project, "imported") {
			t.Errorf("a denied session was returned: %s (%s)", h.Session.ID, h.Session.Project)
		}
	}
	if got.Total != 3 {
		t.Errorf("total = %d, want 3 — it counts sessions the rule removed", got.Total)
	}
	if got.Withheld != 20 {
		t.Errorf("policy_withheld = %d, want 20 — it counts what fit in the cap, not what the rule denies", got.Withheld)
	}
}
