package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestRankRecallAndContextResolveLiveLineageOnce(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	proj := filepath.Join(tmp, "workproj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(proj)
	if cwd, err := os.Getwd(); err == nil {
		proj = cwd
	}

	store := filepath.Join(root, "-w-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	prior := fmt.Sprintf(
		`{"type":"user","sessionId":"prior-1","cwd":%q,"timestamp":"2026-08-04T09:00:00Z","message":{"role":"user","content":"the golden test in internal/store never runs, make test just skips it"}}`+"\n"+
			`{"type":"assistant","sessionId":"prior-1","cwd":%q,"timestamp":"2026-08-04T09:02:00Z","message":{"role":"assistant","content":"Run SVC_FIXTURES=$PWD/fixtures make test in the repo root and the golden test passes."}}`+"\n",
		proj, proj,
	)
	if err := os.WriteFile(filepath.Join(store, "prior-1.jsonl"), []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	live := fmt.Sprintf(
		`{"type":"user","sessionId":"live-9","cwd":%q,"timestamp":%q,"message":{"role":"user","content":"The golden test in internal/store never runs here. Find the exact command that runs it."}}`+"\n",
		proj, now,
	)
	if err := os.WriteFile(filepath.Join(store, "live-9.jsonl"), []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	markSessionLive(dir, "live-9")

	const q = "golden test internal/store never runs"
	liveLineageCutCalls.Store(0)
	ranked, err := rankRecall(dir, q, "", "", 5)
	if err != nil {
		t.Fatalf("rankRecall: %v", err)
	}
	if calls := liveLineageCutCalls.Load(); calls != 1 {
		t.Errorf("rankRecall called liveLineageCut %d times, want 1", calls)
	}
	if len(ranked.hits) == 0 || ranked.hits[0].Session.ID != "prior-1" {
		t.Errorf("rankRecall hits = %+v, want prior-1 first without live-9", ranked.hits)
	}
	for _, h := range ranked.hits {
		if h.Session.ID == "live-9" {
			t.Errorf("rankRecall returned live session %q", h.Session.ID)
		}
	}

	liveLineageCutCalls.Store(0)
	text, _, _, sessions, _, _, err := recallContextResultFrom(dir, q, "")
	if err != nil {
		t.Fatalf("recallContextResultFrom: %v", err)
	}
	if calls := liveLineageCutCalls.Load(); calls != 1 {
		t.Errorf("recallContextResultFrom called liveLineageCut %d times, want 1", calls)
	}
	if !strings.Contains(text, "SVC_FIXTURES") || strings.Contains(strings.Join(sessions, ","), "live-9") {
		t.Errorf("unexpected context response sessions=%v text=%q", sessions, text)
	}
}
