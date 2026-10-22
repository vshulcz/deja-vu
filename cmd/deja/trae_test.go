package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// TRAE CLI's rollouts get their own doctor row, apart from codex: live and
// archived sessions counted, and the state database and config TRAE keeps in
// the same directory left out of the unread count.
func TestDoctorReportsTheTraeStore(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "trae")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rollout := `{"timestamp":"2026-10-01T09:00:00Z","type":"session_meta","payload":{"id":"019fbcca-0000-7000-8000-000000000001","cwd":"/w/api","originator":"codex-tui"}}` + "\n" +
		`{"timestamp":"2026-10-01T09:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"why does the retry loop never back off"}}` + "\n"
	write(filepath.Join(root, "sessions", "2026", "10", "01", "rollout-2026-10-01T09-00-00-019fbcca-0000-7000-8000-000000000001.jsonl"), rollout)
	write(filepath.Join(root, "archived_sessions", "rollout-2026-09-30T09-00-00-019fbcca-0000-7000-8000-000000000002.jsonl"), rollout)
	write(filepath.Join(root, "state_5.sqlite"), "sqlite")

	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	var row string
	for l := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(l, "trae") && strings.Contains(l, "file") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatalf("no trae row in the report:\n%s", buf.String())
	}
	if !strings.Contains(row, "2 files") || strings.Contains(row, "not recognised") {
		t.Errorf("row = %q, want 2 files and nothing unrecognised", row)
	}
	for l := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(l, "codex") && strings.Contains(l, "2 files") {
			t.Errorf("codex row counted TRAE's rollouts: %q", l)
		}
	}

	// The JSON report names the store and finds it.
	var found bool
	for _, c := range doctorStoreChecks() {
		if c.name != "trae" {
			continue
		}
		found = true
		b, err := json.Marshal(c.paths)
		if err != nil {
			t.Fatal(err)
		}
		if len(c.files) != 2 || !strings.Contains(string(b), "trae") {
			t.Errorf("trae store check = %d files at %s, want 2 under the trae root", len(c.files), b)
		}
		ss, err := c.parse(c.files[0])
		if err != nil || len(ss) != 1 || ss[0].Harness != "trae" {
			t.Errorf("trae store check parse = %v, %+v", err, ss)
		}
	}
	if !found {
		t.Fatal("doctor has no trae store check")
	}
}

// A TRAE session resumes with traex, not codex, and a history-only entry has
// nothing to reopen.
func TestResumeTraeSession(t *testing.T) {
	_, cmd, err := resumeCommand(model.Session{Harness: "trae", ID: "019fbcca-0000-7000-8000-000000000001", Project: "api"})
	if err != nil || cmd != "traex resume 019fbcca-0000-7000-8000-000000000001" {
		t.Fatalf("resume = %q, %v", cmd, err)
	}
	if _, _, err := resumeCommand(model.Session{Harness: "trae", ID: "x", Project: "history"}); err == nil {
		t.Fatal("a history.jsonl entry offered a resume command")
	}
}
