package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
	"github.com/vshulcz/deja-vu/internal/vault"
)

func TestLogJSONLOutput(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()

	// Empty store emits zero bytes for both --jsonl and --last --jsonl.
	var buf bytes.Buffer
	if err := runLogTo(&buf, dir, []string{"--jsonl"}); err != nil {
		t.Fatalf("empty log --jsonl: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("empty log --jsonl = %q, want empty output", buf.String())
	}

	buf.Reset()
	if err := runLogTo(&buf, dir, []string{"--last", "--jsonl"}); err != nil {
		t.Fatalf("empty log --last --jsonl: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("empty log --last --jsonl = %q, want empty output", buf.String())
	}

	if err := runLogTo(&buf, dir, []string{"--json", "--jsonl"}); err == nil {
		t.Fatal("expected error when both --json and --jsonl are passed")
	}

	usage.Record(dir, "search", 128)
	usage.RecordDigest(dir, "recall", "secret-digest-body", 1, 64)

	// Populated store emits one compact JSON object per line.
	buf.Reset()
	if err := runLogTo(&buf, dir, []string{"--jsonl", "10"}); err != nil {
		t.Fatalf("populated log --jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("log --jsonl emitted %d lines, want 2: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		var ev usage.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line %d invalid JSON %q: %v", i, line, err)
		}
		if ev.Kind == "" || ev.Time.IsZero() {
			t.Fatalf("line %d missing kind/time: %+v", i, ev)
		}
	}

	// --last --jsonl emits one compact Snapshot line.
	buf.Reset()
	if err := runLogTo(&buf, dir, []string{"--last", "--jsonl"}); err != nil {
		t.Fatalf("populated log --last --jsonl: %v", err)
	}
	lastLines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lastLines) != 1 {
		t.Fatalf("log --last --jsonl emitted %d lines, want 1: %q", len(lastLines), buf.String())
	}
	var snap usage.Snapshot
	if err := json.Unmarshal([]byte(lastLines[0]), &snap); err != nil {
		t.Fatalf("snapshot line invalid JSON %q: %v", lastLines[0], err)
	}
	if !strings.Contains(snap.Digest, "secret-digest-body") {
		t.Fatalf("snapshot digest = %q, want secret-digest-body", snap.Digest)
	}
}

func TestLogLastFailsClosedWhenVaultLocked(t *testing.T) {
	_, dir := withSecurityVaultEnv(t)
	if _, err := vault.Init(dir, "log-lock-passphrase", 0); err != nil {
		t.Fatalf("vault.Init: %v", err)
	}
	usage.RecordDigest(dir, "recall", "vault-protected-digest", 1, 90)
	// Seal the vault and evict key material.
	if err := vault.Lock(dir, "log-lock-passphrase"); err != nil {
		t.Fatalf("vault.Lock: %v", err)
	}
	t.Setenv("DEJA_VAULT_PASSPHRASE", "")

	var buf bytes.Buffer
	lockErr := runLogTo(&buf, dir, []string{"--last", "--jsonl"})
	if !errors.Is(lockErr, vault.ErrLocked) {
		t.Fatalf("locked log --last --jsonl = %v, want vault.ErrLocked", lockErr)
	}
	if buf.Len() != 0 {
		t.Fatalf("locked log --last --jsonl wrote %q", buf.String())
	}

	// Non---last audit trail remains readable while locked and contains the locked access event.
	if err := runLogTo(&buf, dir, []string{"--jsonl", "20"}); err != nil {
		t.Fatalf("locked log --jsonl: %v", err)
	}
	var sawLocked bool
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var ev usage.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode locked event %q: %v", line, err)
		}
		if ev.Kind == usage.KindAccess && ev.Decision == "locked" {
			sawLocked = true
		}
	}
	if !sawLocked {
		t.Fatalf("expected locked access event in log --jsonl output: %s", buf.String())
	}
}
