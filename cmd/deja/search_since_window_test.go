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

// yearOldStore writes one claude session dated a year back, which is what
// someone returning to deja after a year actually has.
func yearOldStore(t *testing.T) time.Time {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	when := time.Now().AddDate(-1, 0, 0).UTC()
	stamp := when.Format("2006-01-02T15:04:05Z")
	body := fmt.Sprintf(`{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":%q,"message":{"role":"user","content":"the kafka consumer rebalance loops"}}`, stamp) + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	return when
}

// The reader typed `--since 30d`; "since 720h0m0s" is not the flag they passed,
// and activeFilters has taken a raw string for exactly this since #715 — the
// search path was the one that passed "" (#1059).
func TestSearchSinceEchoesWhatWasTyped(t *testing.T) {
	yearOldStore(t)

	out, err := captureRunStderr(t, "kafka", "--since", "30d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "under since 30d") {
		t.Errorf("search did not echo the typed duration: %q", out)
	}
	if strings.Contains(out, "720h") {
		t.Errorf("search printed the parsed duration: %q", out)
	}

	// The --since=30d form goes through splitEqualsForms and must read the same.
	out, err = captureRunStderr(t, "kafka", "--since=30d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "under since 30d") || strings.Contains(out, "720h") {
		t.Errorf("--since=30d form: %q", out)
	}
}

// A store whose every session predates the window cannot answer any --since
// query, so "matched nothing" is a claim about the query when the window is
// what emptied the result.
func TestSinceOlderThanWholeStoreSaysSo(t *testing.T) {
	when := yearOldStore(t)
	want := "every one of the 1 indexed sessions is older than that window — the newest is " +
		when.Local().Format("2006-01-02")

	out, err := captureRunStderr(t, "kafka", "--since", "30d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, want) {
		t.Errorf("search: want %q in %q", want, out)
	}

	out, err = captureRunStderr(t, "last", "--since", "30d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, want) {
		t.Errorf("last: want %q in %q", want, out)
	}
}

// The line is about the window, not about every empty result: a store the
// window does cover must not claim its sessions are older than it.
func TestSinceWithinStoreRangeStaysQuiet(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	stamp := time.Now().Add(-2 * time.Hour).UTC().Format("2006-01-02T15:04:05Z")
	body := fmt.Sprintf(`{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":%q,"message":{"role":"user","content":"the kafka consumer rebalance loops"}}`, stamp) + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRunStderr(t, "nosuchterm", "--since", "30d")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "older than that window") {
		t.Errorf("store inside the window got the older-than-window line: %q", out)
	}
}
