package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

func TestRecallFlagParsingAndDashDashOrder(t *testing.T) {
	dir, _ := forkStore(t, false)
	// #5907: words before and after -- keep their order.
	var out bytes.Buffer
	if err := runRecall(dir, []string{"retry", "--", "--500"}, &out); err != nil {
		t.Fatalf("runRecall(retry -- --500): %v", err)
	}
	if !strings.Contains(out.String(), `for "retry --500"`) && !strings.Contains(out.String(), "retry") {
		t.Fatalf("unexpected recall output for 'retry -- --500':\n%s", out.String())
	}

	// #5907: single-dash unknown flags before -- are rejected with unknownFlag.
	err := runRecall(dir, []string{"-n", "5", "foo"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), `recall: unknown flag "-n"`) {
		t.Fatalf("runRecall(-n 5 foo) err = %v, want unknown flag -n", err)
	}
}

func TestFilesHowFrictionFlagAndDashDashValidation(t *testing.T) {
	// #5908: deja files -- "" and deja files -- "   " must fail usage check.
	for _, arg := range []string{"", "   "} {
		if err := runFiles(t.TempDir(), []string{"--", arg}, io.Discard); err == nil {
			t.Errorf("runFiles([--, %q]) succeeded, want usage error", arg)
		}
	}

	// #5909: deja how rejects unknown flags with suggestions and rejects blank args after --.
	if err := runHow(t.TempDir(), []string{"--limti", "5", "test"}, io.Discard); err == nil ||
		!strings.Contains(err.Error(), `how: unknown flag "--limti" — did you mean --limit?`) {
		t.Errorf("runHow(--limti) err = %v, want did you mean --limit", err)
	}
	for _, arg := range []string{"", "   "} {
		if err := runHow(t.TempDir(), []string{"--", arg}, io.Discard); err == nil {
			t.Errorf("runHow([--, %q]) succeeded, want usage error", arg)
		}
	}

	// #5910: deja friction rejects unknown flags with suggestions.
	if err := runFriction(t.TempDir(), []string{"--settlee", "foo"}, io.Discard); err == nil ||
		!strings.Contains(err.Error(), `friction: unknown flag "--settlee" — did you mean --settle?`) {
		t.Errorf("runFriction(--settlee) err = %v, want did you mean --settle", err)
	}
	if err := runFriction(t.TempDir(), []string{"-n", "5"}, io.Discard); err == nil ||
		!strings.Contains(err.Error(), `friction: unknown flag "-n"`) {
		t.Errorf("runFriction(-n 5) err = %v, want unknown flag -n", err)
	}
}

func TestStartDetachedPIDReapsExitedChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX zombie reaping test")
	}
	cmd := exec.CommandContext(t.Context(), "true")
	pid, err := startDetachedPID(cmd)
	if err != nil {
		t.Fatalf("startDetachedPID: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child pid %d still present after 2s (not reaped)", pid)
}

func TestInstallCodexAutoNoMCP(t *testing.T) {
	home := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "idx"))

	if err := run([]string{"install", "codex-auto", "--no-mcp", "--no-index"}); err != nil {
		t.Fatalf("install codex-auto --no-mcp: %v", err)
	}

	// hooks.json and skills must exist, but config.toml must NOT have [mcp_servers.deja].
	codexHome := sources.CodexHome()
	hooksPath := filepath.Join(codexHome, "hooks.json")
	if _, err := os.Stat(hooksPath); err != nil {
		t.Fatalf("hooks.json missing at %s: %v", hooksPath, err)
	}
	cfgPath := filepath.Join(codexHome, "config.toml")
	if b, err := os.ReadFile(cfgPath); err == nil && strings.Contains(string(b), "mcp_servers.deja") {
		t.Fatalf("config.toml unexpectedly contains mcp_servers.deja:\n%s", b)
	}
	st := readWiringState()
	if !st.NoMCP {
		t.Fatalf("wiring state = %+v, want NoMCP=true", st)
	}

	// Upgrade rewire preserves NoMCP and does not inject [mcp_servers.deja].
	oldVersion := version
	version = "v9.9.9-upgrade-test"
	defer func() { version = oldVersion }()
	_ = refreshWiringAfterUpgrade()
	if st = readWiringState(); st.Version != "v9.9.9-upgrade-test" || !st.NoMCP {
		t.Fatalf("wiring state after upgrade = %+v, want Version=v9.9.9-upgrade-test and NoMCP=true", st)
	}
	if b, err := os.ReadFile(cfgPath); err == nil && strings.Contains(string(b), "mcp_servers.deja") {
		t.Fatalf("config.toml gained mcp_servers.deja after upgrade rewire:\n%s", b)
	}

	// Uninstall removes hooks cleanly.
	if err := run([]string{"uninstall", "codex-auto"}); err != nil {
		t.Fatalf("uninstall codex-auto: %v", err)
	}
	if b, err := os.ReadFile(hooksPath); err == nil && strings.Contains(string(b), "deja") {
		t.Fatalf("hooks.json still mentions deja after uninstall:\n%s", b)
	}
}

func TestCodexDuplicateHookContextAndPromptDedupe(t *testing.T) {
	dir, _ := forkStore(t, true)
	codexDir := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "11", "04")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sid := "019a4df2-c533-7eb2-9a51-eb6c4e821242"
	rollout := filepath.Join(codexDir, "rollout-2026-11-04T10-49-54-"+sid+".jsonl")
	// At SessionStart on a fresh Codex thread, only session_meta exists on disk.
	meta := `{"timestamp":"2026-11-04T10:49:54Z","type":"session_meta","payload":{"id":"` + sid + `","cwd":"/w/q","source":"cli"}}` + "\n"
	if err := os.WriteFile(rollout, []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}

	runStart := func() string {
		payload, err := json.Marshal(map[string]string{
			"session_id":      sid,
			"source":          "startup",
			"transcript_path": rollout,
			"cwd":             "/w/q",
		})
		if err != nil {
			t.Fatal(err)
		}
		withHookStdin(t, string(payload))
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}

	firstStart := runStart()
	if !strings.Contains(firstStart, "deja-recall") {
		t.Fatalf("first SessionStart produced no digest:\n%s", firstStart)
	}
	secondStart := runStart()
	if strings.TrimSpace(secondStart) != "" {
		t.Fatalf("duplicate SessionStart for the same Codex session emitted a second digest:\n%s", secondStart)
	}

	// Concurrent UserPromptSubmit (hook-prompt) calls for the same session and prompt:
	// only one must emit and record a dejavu injection.
	promptPayload, err := json.Marshal(map[string]string{
		"session_id":      sid,
		"transcript_path": rollout,
		"cwd":             "/w/p",
		"prompt":          forkQuestion,
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	outs := make([]string, 2)
	startGate := make(chan struct{})
	for i := range 2 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startGate
			var buf bytes.Buffer
			if promptErr := runHookPrompt(dir, bytes.NewReader(promptPayload), &buf); promptErr != nil {
				t.Errorf("runHookPrompt[%d]: %v", idx, promptErr)
			}
			outs[idx] = strings.TrimSpace(buf.String())
		}(i)
	}
	close(startGate)
	wg.Wait()

	nonEmpty := 0
	for _, o := range outs {
		if o != "" {
			nonEmpty++
		}
	}
	if nonEmpty != 1 {
		t.Fatalf("concurrent runHookPrompt emitted %d non-empty blocks, want 1:\n[0]=%q\n[1]=%q", nonEmpty, outs[0], outs[1])
	}

	rawLog, err := os.ReadFile(usage.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	var hookCount, dejavuCount int
	for line := range strings.SplitSeq(string(rawLog), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e usage.Event
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e.Into == sid {
			switch e.Kind {
			case usage.KindHook:
				hookCount++
			case usage.KindDejaVu:
				dejavuCount++
			}
		}
	}
	if hookCount != 1 || dejavuCount != 1 {
		t.Fatalf("usage events for %s: hook=%d dejavu=%d, want 1 of each", sid, hookCount, dejavuCount)
	}
}

func TestAntigravityEmptyDigestAnswersOnFirstInvocation(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	// Seed a session in project "netgateway" plus filler sessions so an unscoped
	// home workspace ($HOME) has an empty project digest while unscoped/named
	// recall still finds the answer.
	seedClaude(t, claude, "netgateway", "sess-net",
		"why did netgateway listener fail on startup",
		"netgateway listener failed on startup because ZORBWAX_MTU was unset; exporting ZORBWAX_MTU=1420 fixed it")
	seedClaude(t, claude, "other", "sess-f1", "routine cron cleanup", "cleaned old logs")
	seedClaude(t, claude, "other", "sess-f2", "routine backup check", "verified snapshot")
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	transcript := filepath.Join(tmp, "transcript.jsonl")
	line := `{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nwhy did netgateway listener fail on startup\n</USER_REQUEST>\n"}`
	if err := os.WriteFile(transcript, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	home := os.Getenv("HOME")
	call := func(n int) string {
		payload, err := json.Marshal(map[string]any{
			"invocationNum":  n,
			"conversationId": "conv-empty-digest",
			"transcriptPath": transcript,
			"workspacePaths": []string{home},
		})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := runHookAntigravity(dir, bytes.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		var resp antigravityHookResponse
		if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
			t.Fatalf("invalid JSON: %q", out.String())
		}
		if len(resp.InjectSteps) == 0 {
			return ""
		}
		return resp.InjectSteps[0].EphemeralMessage
	}

	// invocationNum 0 on a workspace with an empty digest must answer the user's
	// prompt immediately rather than waiting until invocationNum 1 (#5933).
	first := call(0)
	if !strings.Contains(first, "ZORBWAX_MTU") {
		t.Fatalf("invocationNum 0 with empty digest did not answer the prompt: %q", first)
	}
	if !digestAlreadyInjected(dir, "conv-empty-digest") {
		t.Fatal("empty digest on invocationNum 0 was not recorded in the ledger")
	}
	// And invocationNum 1 inside the same turn must stay silent (deduped).
	if second := call(1); second != "" {
		t.Fatalf("invocationNum 1 repeated the injection mid-turn: %q", second)
	}
}
