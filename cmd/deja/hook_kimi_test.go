package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

func seedKimiHookWorkspace(t *testing.T) (dir, cwd string) {
	t.Helper()
	withStatsStores(t)
	hostsNoWarmup(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "kimiwork", "k1.jsonl"), "k1", []string{
		`{"type":"user","sessionId":"k1","timestamp":"` + old + `","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
		`{"type":"assistant","sessionId":"k1","timestamp":"` + old + `","message":{"role":"assistant","content":"disabled prepared statements in config.toml and verified Edit target"}}`,
	})
	dir = index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd = filepath.Join(t.TempDir(), "tmp", "kimiwork")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	t.Setenv("KIMI_CODE_HOME", filepath.Join(t.TempDir(), "kimi-home"))
	t.Setenv("DEJA_KIMI_ROOT", "")
	t.Setenv("PATH", t.TempDir())
	return dir, cwd
}

func runHookViaDeferred(t *testing.T, dir, name string, rest []string, payload any) string {
	t.Helper()
	b := mustMarshal(t, payload)
	withHookStdin(t, string(b))
	var err error
	out := captureStdout(t, func() {
		err = runHookDeferred(dir, name, rest, commands[name])
	})
	if err != nil {
		t.Fatalf("%s %v: %v", name, rest, err)
	}
	return out
}

func TestKimiHookContextVersionGateAndOnceDedup(t *testing.T) {
	t.Run("2.9+ emits SessionStart JSON and skips duplicate UserPromptSubmit --once", func(t *testing.T) {
		dir, cwd := seedKimiHookWorkspace(t)
		t.Setenv("DEJA_KIMI_VERSION", "2.9.0-canary.20261127")

		out := runHookViaDeferred(t, dir, "hook-context", []string{"--kimi"}, map[string]any{
			"hook_event_name": "SessionStart",
			"session_id":      "session_k29",
			"cwd":             cwd,
			"source":          "startup",
		})
		var resp sessionStartHookResponse
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
			t.Fatalf("expected JSON from hook-context --kimi on 2.9+, got %q: %v", out, err)
		}
		if resp.HookSpecificOutput.HookEventName != "SessionStart" {
			t.Errorf("hookEventName = %q, want SessionStart", resp.HookSpecificOutput.HookEventName)
		}
		if strings.TrimSpace(resp.HookSpecificOutput.AdditionalContext) == "" {
			t.Fatalf("expected non-empty additionalContext in SessionStart JSON: %q", out)
		}

		// UserPromptSubmit fires right after SessionStart; --once must stay quiet.
		second := runHookViaDeferred(t, dir, "hook-context", []string{"--plain", "--once"}, map[string]any{
			"hook_event_name": "UserPromptSubmit",
			"session_id":      "session_k29",
			"cwd":             cwd,
			"prompt":          "hello",
		})
		if strings.TrimSpace(second) != "" {
			t.Fatalf("expected UserPromptSubmit --once to skip after SessionStart injection, got %q", second)
		}
	})

	for _, tc := range []struct {
		name string
		ver  string
	}{
		{name: "2.8.1 stays silent on SessionStart so UserPromptSubmit delivers", ver: "2.8.1"},
		{name: "unset version with missing kimi binary falls back conservatively", ver: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, cwd := seedKimiHookWorkspace(t)
			t.Setenv("DEJA_KIMI_VERSION", tc.ver)

			out := runHookViaDeferred(t, dir, "hook-context", []string{"--kimi"}, map[string]any{
				"hook_event_name": "SessionStart",
				"session_id":      "session_k28",
				"cwd":             cwd,
				"source":          "startup",
			})
			if strings.TrimSpace(out) != "" {
				t.Fatalf("expected silent SessionStart for %q, got %q", tc.ver, out)
			}

			second := runHookViaDeferred(t, dir, "hook-context", []string{"--plain", "--once"}, map[string]any{
				"hook_event_name": "UserPromptSubmit",
				"session_id":      "session_k28",
				"cwd":             cwd,
				"prompt":          "hello",
			})
			if strings.TrimSpace(second) == "" {
				t.Fatalf("expected UserPromptSubmit --once to deliver plain digest after skipped SessionStart")
			}
		})
	}
}

func TestKimiPostToolUseUpdatedToolOutputAndFallback(t *testing.T) {
	t.Run("2.9+ Edit delivers deferred recall via updatedToolOutput without repeating on prompt", func(t *testing.T) {
		dir := seedEditHistory(t)
		t.Setenv("KIMI_CODE_HOME", filepath.Join(t.TempDir(), "kimi-home"))
		t.Setenv("DEJA_KIMI_ROOT", "")
		t.Setenv("PATH", t.TempDir())
		t.Setenv("DEJA_KIMI_VERSION", "2.9.0")

		if out := runHookViaDeferred(t, dir, "hook-tool", []string{"--defer"}, map[string]any{
			"hook_event_name": "PreToolUse",
			"session_id":      "kimi-edit-29",
			"cwd":             "/work/alpha",
			"tool_name":       "Edit",
			"tool_input":      map[string]any{"path": "config.go", "old_string": "a", "new_string": "b"},
			"tool_call_id":    "call_1",
		}); strings.TrimSpace(out) != "" {
			t.Fatalf("PreToolUse --defer emitted unexpected output: %q", out)
		}

		after := runHookViaDeferred(t, dir, "hook-tool-after", []string{"--kimi"}, map[string]any{
			"hook_event_name": "PostToolUse",
			"session_id":      "kimi-edit-29",
			"cwd":             "/work/alpha",
			"tool_name":       "Edit",
			"tool_input":      map[string]any{"path": "config.go"},
			"tool_output":     "Edited config.go",
			"is_error":        false,
		})
		var resp struct {
			HookSpecificOutput struct {
				HookEventName     string `json:"hookEventName"`
				UpdatedToolOutput string `json:"updatedToolOutput"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(after)), &resp); err != nil {
			t.Fatalf("expected PostToolUse JSON, got %q: %v", after, err)
		}
		if resp.HookSpecificOutput.HookEventName != "PostToolUse" {
			t.Errorf("hookEventName = %q, want PostToolUse", resp.HookSpecificOutput.HookEventName)
		}
		if !strings.HasPrefix(resp.HookSpecificOutput.UpdatedToolOutput, "Edited config.go\n\n") ||
			!strings.Contains(resp.HookSpecificOutput.UpdatedToolOutput, "sentinel") {
			t.Fatalf("updatedToolOutput missing original output + deferred recall: %q", resp.HookSpecificOutput.UpdatedToolOutput)
		}

		prompt := runHookViaDeferred(t, dir, "hook-prompt", []string{"--plain"}, map[string]any{
			"hook_event_name": "UserPromptSubmit",
			"session_id":      "kimi-edit-29",
			"cwd":             "/work/alpha",
			"prompt":          "ok",
		})
		if strings.Contains(prompt, "sentinel") {
			t.Fatalf("deferred recall repeated on UserPromptSubmit after updatedToolOutput: %q", prompt)
		}
	})

	t.Run("2.9+ Edit at 2000-byte slice boundary defers to UserPromptSubmit instead of truncating", func(t *testing.T) {
		dir := seedEditHistory(t)
		t.Setenv("KIMI_CODE_HOME", filepath.Join(t.TempDir(), "kimi-home"))
		t.Setenv("DEJA_KIMI_ROOT", "")
		t.Setenv("PATH", t.TempDir())
		t.Setenv("DEJA_KIMI_VERSION", "2.9.0")

		_ = runHookViaDeferred(t, dir, "hook-tool", []string{"--defer"}, map[string]any{
			"hook_event_name": "PreToolUse",
			"session_id":      "kimi-edit-long",
			"cwd":             "/work/alpha",
			"tool_name":       "Write",
			"tool_input":      map[string]any{"path": "config.go", "content": "x"},
		})
		after := runHookViaDeferred(t, dir, "hook-tool-after", []string{"--kimi"}, map[string]any{
			"hook_event_name": "PostToolUse",
			"session_id":      "kimi-edit-long",
			"cwd":             "/work/alpha",
			"tool_name":       "Write",
			"tool_input":      map[string]any{"path": "config.go"},
			"tool_output":     strings.Repeat("x", 2000),
			"is_error":        false,
		})
		if strings.TrimSpace(after) != "" {
			t.Fatalf("expected silent PostToolUse at 2000-byte cap, got %q", after)
		}
		prompt := runHookViaDeferred(t, dir, "hook-prompt", []string{"--plain"}, map[string]any{
			"hook_event_name": "UserPromptSubmit",
			"session_id":      "kimi-edit-long",
			"cwd":             "/work/alpha",
			"prompt":          "ok",
		})
		if !strings.Contains(prompt, "sentinel") {
			t.Fatalf("expected deferred recall on UserPromptSubmit after 2000-byte fallback, got %q", prompt)
		}
	})

	t.Run("2.8.1 Edit defers to UserPromptSubmit", func(t *testing.T) {
		dir := seedEditHistory(t)
		t.Setenv("KIMI_CODE_HOME", filepath.Join(t.TempDir(), "kimi-home"))
		t.Setenv("DEJA_KIMI_ROOT", "")
		t.Setenv("PATH", t.TempDir())
		t.Setenv("DEJA_KIMI_VERSION", "2.8.1")

		_ = runHookViaDeferred(t, dir, "hook-tool", []string{"--defer"}, map[string]any{
			"hook_event_name": "PreToolUse",
			"session_id":      "kimi-edit-28",
			"cwd":             "/work/alpha",
			"tool_name":       "Edit",
			"tool_input":      map[string]any{"path": "config.go", "old_string": "a", "new_string": "b"},
		})
		after := runHookViaDeferred(t, dir, "hook-tool-after", []string{"--kimi"}, map[string]any{
			"hook_event_name": "PostToolUse",
			"session_id":      "kimi-edit-28",
			"cwd":             "/work/alpha",
			"tool_name":       "Edit",
			"tool_input":      map[string]any{"path": "config.go"},
			"tool_output":     "Edited config.go",
			"is_error":        false,
		})
		if strings.TrimSpace(after) != "" {
			t.Fatalf("expected silent PostToolUse on 2.8.1, got %q", after)
		}
		prompt := runHookViaDeferred(t, dir, "hook-prompt", []string{"--plain"}, map[string]any{
			"hook_event_name": "UserPromptSubmit",
			"session_id":      "kimi-edit-28",
			"cwd":             "/work/alpha",
			"prompt":          "ok",
		})
		if !strings.Contains(prompt, "sentinel") {
			t.Fatalf("expected deferred recall on UserPromptSubmit on 2.8.1, got %q", prompt)
		}
	})

	t.Run("PostToolUseFailure Bash defers fix pair on 2.9+ without inline output", func(t *testing.T) {
		seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
		t.Setenv("KIMI_CODE_HOME", filepath.Join(t.TempDir(), "kimi-home"))
		t.Setenv("DEJA_KIMI_ROOT", "")
		t.Setenv("PATH", t.TempDir())
		t.Setenv("DEJA_KIMI_VERSION", "2.9.0")
		dir := os.Getenv("DEJA_INDEX_DIR")

		failure := map[string]any{
			"hook_event_name": "PostToolUseFailure",
			"session_id":      "kimi-bash-29",
			"cwd":             "/work/app",
			"tool_name":       "Bash",
			"tool_input":      map[string]any{"command": "make"},
			"tool_call_id":    "call_1",
			"is_error":        true,
			"error":           map[string]any{"code": "TOOL_ERROR", "message": "goroutine 1 [running]:\npanic: sql: database is closed"},
		}
		for strike := 1; strike <= 2; strike++ {
			if out := runHookViaDeferred(t, dir, "hook-tool-after", []string{"--kimi"}, failure); strings.TrimSpace(out) != "" {
				t.Fatalf("strike %d printed unexpected output: %q", strike, out)
			}
		}
		prompt := runHookViaDeferred(t, dir, "hook-prompt", []string{"--plain"}, map[string]any{
			"hook_event_name": "UserPromptSubmit",
			"session_id":      "kimi-bash-29",
			"cwd":             "/work/app",
			"prompt":          "ok",
		})
		if !strings.Contains(prompt, "make clean && make CGO_ENABLED=0") {
			t.Fatalf("expected deferred fix pair on UserPromptSubmit, got %q", prompt)
		}
	})
}

func TestKimiAutoInstallUpgradeAndDoctor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("KIMI_CODE_HOME", "")
	t.Setenv("DEJA_KIMI_ROOT", "")
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

	// Create a runnable fake binary on PATH so doctor reports wired rather than missing binary.
	binDir := t.TempDir()
	dejaExe := filepath.Join(binDir, "deja")
	if err := os.WriteFile(dejaExe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfgDir := filepath.Join(home, ".kimi-code")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "config.toml")
	exe := hookExeInConfigs(dejaExe)

	// Pre-populate unmarked legacy tables alongside a user-owned hook and old marked entry.
	initial := strings.Join([]string{
		`[[hooks]]`,
		`event = "Stop"`,
		`command = "user-stop"`,
		``,
		kimiHookMarker,
		`[[hooks]]`,
		`event = "UserPromptSubmit"`,
		`command = "` + exe + ` hook-prompt --plain"`,
		`timeout = 30`,
		``,
		`[[hooks]]`,
		`event = "PostToolUse"`,
		`matcher = "Bash"`,
		`command = "` + exe + ` hook-tool-after --defer"`,
		`timeout = 30`,
		``,
		`[[hooks]]`,
		`event = "PostToolUseFailure"`,
		`matcher = "Bash"`,
		`command = "` + exe + ` hook-tool-after --defer"`,
		`timeout = 30`,
		``,
	}, "\n")
	if err := os.WriteFile(cfgPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installKimiAuto(dejaExe, false); err != nil {
		t.Fatalf("installKimiAuto: %v", err)
	}
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)

	if strings.Contains(cfg, "hook-tool-after --defer") {
		t.Fatalf("upgrade left legacy hook-tool-after --defer behind:\n%s", cfg)
	}
	if !strings.Contains(cfg, `command = "user-stop"`) {
		t.Fatalf("upgrade dropped user-owned hook:\n%s", cfg)
	}
	for _, want := range []string{
		`event = "SessionStart"`,
		exe + ` hook-context --kimi`,
		exe + ` hook-context --plain --once`,
		exe + ` hook-prompt --plain`,
		exe + ` hook-precompact --harness kimi`,
		exe + ` hook-session-end`,
		`matcher = "^mcp__deja__"`,
		exe + ` hook-mcp-call`,
		`matcher = "^(Edit|Write|Bash)$"`,
		exe + ` hook-tool-after --kimi`,
		`matcher = "^(Edit|Write)$"`,
		exe + ` hook-tool --defer`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("upgraded config missing %q:\n%s", want, cfg)
		}
	}
	if got, want := strings.Count(cfg, kimiHookMarker), strings.Count(cfg, "[[hooks]]")-1; got != want {
		t.Errorf("markers=%d, want %d (one per managed hook table):\n%s", got, want, cfg)
	}

	var kimiRow *doctorAutoStatus
	for _, row := range collectDoctorAutoRecall() {
		if row.Name == "kimi" {
			r := row
			kimiRow = &r
			break
		}
	}
	if kimiRow == nil || kimiRow.State != "wired" || kimiRow.BinaryMissing {
		t.Fatalf("doctor auto-recall row for kimi = %+v, want wired and present binary", kimiRow)
	}
}
