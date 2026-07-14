package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

func setupLocalIndex(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-sshsync")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"type":"user","sessionId":"ssh1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"sshneedle question"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "ssh1.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	return tmp
}

func TestSyncSSHPush(t *testing.T) {
	setupLocalIndex(t)
	var calls [][]string
	old := sshRunner
	defer func() { sshRunner = old }()
	sshRunner = func(name string, args ...string) (string, error) {
		calls = append(calls, append([]string{name}, args...))
		if name == "ssh" && args[1] == "mktemp -d" {
			return "/tmp/remote-batch\n", nil
		}
		if name == "ssh" {
			return "deja: imported 1 records", nil
		}
		return "", nil
	}
	if err := runSyncSSH([]string{"minihost"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want mktemp+scp+import: %v", len(calls), calls)
	}
	if calls[1][0] != "scp" || !strings.HasSuffix(calls[1][len(calls[1])-1], ":/tmp/remote-batch/") {
		t.Fatalf("bad scp call: %v", calls[1])
	}
	if !strings.Contains(calls[2][2], "sync import") || !strings.Contains(calls[2][2], "/tmp/remote-batch") {
		t.Fatalf("bad remote import call: %v", calls[2])
	}
}

func TestSyncSSHPushNothingNew(t *testing.T) {
	setupLocalIndex(t)
	var calls int
	old := sshRunner
	defer func() { sshRunner = old }()
	sshRunner = func(name string, args ...string) (string, error) {
		calls++
		return "/tmp/x", nil
	}
	// First push exports the one record; run it with a runner that accepts it.
	full := sshRunner
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && args[1] == "mktemp -d" {
			return "/tmp/remote-batch", nil
		}
		return "deja: imported 1 records", nil
	}
	if err := runSyncSSH([]string{"minihost"}); err != nil {
		t.Fatal(err)
	}
	// Second push has nothing new: no ssh/scp calls at all.
	sshRunner = full
	calls = 0
	if err := runSyncSSH([]string{"minihost"}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("expected no remote calls on empty export, got %d", calls)
	}
}

func TestSyncSSHPull(t *testing.T) {
	setupLocalIndex(t)
	base := time.Date(2026, 1, 3, 4, 5, 6, 0, time.UTC)
	old := sshRunner
	defer func() { sshRunner = old }()
	sshRunner = func(name string, args ...string) (string, error) {
		if name == "ssh" && args[1] == "mktemp -d" {
			return "/tmp/remote-out", nil
		}
		if name == "ssh" && strings.Contains(args[1], "sync export") {
			return "deja: exported 1 records", nil
		}
		if name == "scp" {
			dest := strings.TrimSuffix(args[len(args)-1], "/")
			f, err := os.Create(filepath.Join(dest, "deja-sync-remote-1.jsonl"))
			if err != nil {
				return "", err
			}
			enc := json.NewEncoder(f)
			_ = enc.Encode(index.SyncRecord{Harness: "claude", SessionID: "remote-ssh", Project: "otherbox", Role: "user", Text: "pullneedle from remote", Time: base})
			return "", f.Close()
		}
		return "", nil
	}
	if err := runSyncSSH([]string{"minihost", "--pull"}); err != nil {
		t.Fatal(err)
	}
	ss, err := index.Search(os.Getenv("DEJA_INDEX_DIR"), search.Options{Query: "pullneedle", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || !strings.HasPrefix(ss[0].Project, "imported:") {
		t.Fatalf("pulled session not searchable: %#v", ss)
	}
}

func TestSyncSSHBadArgs(t *testing.T) {
	if err := runSyncSSH(nil); err == nil {
		t.Fatal("expected error for missing host")
	}
	if err := runSyncSSH([]string{"--evil"}); err == nil {
		t.Fatal("expected error for flag-looking host")
	}
	if err := runSyncSSH([]string{"a", "b"}); err == nil {
		t.Fatal("expected error for two hosts")
	}
}
