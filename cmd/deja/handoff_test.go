package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestHandoffFlagValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--to"},
		{"--to", "notepad"},
		{"--frobnicate"},
	} {
		if err := runHandoff(args, discardWriter{}); err == nil {
			t.Fatalf("runHandoff(%#v) returned nil", args)
		}
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestHandoffCommandTable(t *testing.T) {
	cases := map[string][]string{
		"claude":   {"claude", "P"},
		"codex":    {"codex", "P"},
		"opencode": {"opencode", "--prompt", "P"},
		"gemini":   {"gemini", "-i", "P"},
		"qwen":     {"qwen", "-i", "P"},
		"aider":    {"aider", "--message", "P"},
		"pi":       {"pi", "P"},
		"grok":     {"grok", "P"},
		"cursor":   {"cursor-agent", "chat", "P"},
		"copilot":  {"copilot", "-p", "P"},
	}
	for target, want := range cases {
		argv, ok := handoffCommand(target, "P")
		if !ok || strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
			t.Fatalf("handoffCommand(%s) = %v, %v", target, argv, ok)
		}
	}
	if _, ok := handoffCommand("antigravity", "P"); ok {
		t.Fatal("antigravity has no CLI prompt entry point, must stay paste-only")
	}
	if len(handoffTargets()) != len(cases) {
		t.Fatalf("handoffTargets() = %v out of sync with command table", handoffTargets())
	}
}

func TestHandoffDigestShape(t *testing.T) {
	s := model.Session{
		ID: "abc123", Harness: "claude", Project: "gateway", Updated: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
		Messages: []model.Message{
			{Role: "user", Text: "pool exhausted under load"},
			{Role: "assistant", Text: "raised MaxIdleConns, real leak was rows.Close"},
			{Role: "user", Text: "still failing on staging"},
			{Role: "assistant", Text: "staging pgbouncer caps at 20, bump pool_size"},
		},
	}
	d := handoffDigest(s, handoffBudget)
	for _, want := range []string{
		"picking up work handed off from a claude session",
		"project gateway",
		"## User problem statement(s)",
		"## Where it stopped",
		"**assistant:** staging pgbouncer caps at 20",
	} {
		if !strings.Contains(d, want) {
			t.Fatalf("digest missing %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "# deja share:") {
		t.Fatalf("share header must be replaced by handoff framing:\n%s", d)
	}
	if len(d) > handoffBudget+256 {
		t.Fatalf("digest exceeds budget: %d", len(d))
	}
}

func TestHandoffTailEmptyAndBudget(t *testing.T) {
	if got := handoffTail(model.Session{}, 100); got != "" {
		t.Fatalf("empty session tail = %q", got)
	}
	s := model.Session{Messages: []model.Message{
		{Role: "assistant", Text: strings.Repeat("x", 500)},
	}}
	if got := handoffTail(s, 40); len(got) > 40 {
		t.Fatalf("tail ignored budget: %d bytes", len(got))
	}
	if got := handoffTail(s, 0); got != "" {
		t.Fatalf("zero budget tail = %q", got)
	}
}

func TestHandoffPrintsComposableDigest(t *testing.T) {
	withStatsStores(t)
	out, err := captureRun(t, "handoff", "--to", "codex", "c3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "picking up work handed off from a claude session") ||
		!strings.Contains(out, "long beta session") {
		t.Fatalf("handoff output = %q", out)
	}
}

func TestHandoffPasteModes(t *testing.T) {
	withStatsStores(t)
	// no --to: universal paste digest
	out, err := captureRun(t, "handoff", "c3")
	if err != nil || !strings.Contains(out, "picking up work handed off") {
		t.Fatalf("bare handoff = %q, %v", out, err)
	}
	// GUI-only target prints the digest too
	out, err = captureRun(t, "handoff", "--to", "antigravity", "c3")
	if err != nil || !strings.Contains(out, "picking up work handed off") {
		t.Fatalf("antigravity handoff = %q, %v", out, err)
	}
	// but cannot --exec
	if err := runHandoff([]string{"--to", "antigravity", "c3", "--exec"}, discardWriter{}); err == nil || !strings.Contains(err.Error(), "no CLI prompt entry") {
		t.Fatalf("antigravity exec error = %v", err)
	}
	if err := runHandoff([]string{"c3", "--exec"}, discardWriter{}); err == nil || !strings.Contains(err.Error(), "--exec needs --to") {
		t.Fatalf("bare exec error = %v", err)
	}
}

func TestHandoffCleanDropsPreamblesAndRepeats(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "<environment_context><cwd>/x</cwd></environment_context>"},
		{Role: "user", Text: "hi"},
		{Role: "user", Text: "hi"},
		{Role: "user", Text: "Comments on artifact URI: file:///brain/plan.md approved"},
		{Role: "user", Text: "real question about retries"},
	}}
	got := handoffClean(s)
	if len(got.Messages) != 2 || got.Messages[0].Text != "hi" || got.Messages[1].Text != "real question about retries" {
		t.Fatalf("cleaned = %#v", got.Messages)
	}
}

func TestHandoffSourceErrors(t *testing.T) {
	withStatsStores(t)
	if _, err := handoffSource("nope-prefix"); err == nil || !strings.Contains(err.Error(), "no session matches") {
		t.Fatalf("bad prefix error = %v", err)
	}
	t.Chdir(t.TempDir())
	if _, err := handoffSource(""); err == nil || !strings.Contains(err.Error(), "no indexed sessions for this project") {
		t.Fatalf("empty project error = %v", err)
	}
}

func TestHandoffSourcePicksNewestProjectSession(t *testing.T) {
	withStatsStores(t)
	cwd := filepath.Join(t.TempDir(), "tmp", "alpha")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	s, err := handoffSource("")
	if err != nil {
		t.Fatal(err)
	}
	// c2 (2026-03) is newer than c1 (2026-01) in project tmp/alpha.
	if s.ID != "c2" {
		t.Fatalf("picked session %s, want c2", s.ID)
	}
}

func TestHandoffExecMissingBinary(t *testing.T) {
	withStatsStores(t)
	t.Setenv("PATH", t.TempDir())
	err := runHandoff([]string{"--to", "grok", "c3", "--exec"}, discardWriter{})
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("exec missing binary error = %v", err)
	}
}

func TestPrefixArgAndProjectCandidates(t *testing.T) {
	if prefixArg("") != "" || prefixArg("abc") != " abc" {
		t.Fatal("prefixArg formatting")
	}
	names := projectNameCandidates("/tmp/alpha")
	if len(names) == 0 || names[0] == "" {
		t.Fatalf("candidates = %v", names)
	}
}
