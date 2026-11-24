package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestHookAntigravityUsesLastUserInputAndCWDFallback(t *testing.T) {
	setup := func(t *testing.T) (string, string, string) {
		t.Helper()
		tmp := hermeticEnv(t)
		claude := filepath.Join(tmp, "claude")
		t.Setenv("DEJA_CLAUDE_ROOT", claude)

		repoDir := filepath.Join(tmp, "tmp", "agyapp")
		otherDir := filepath.Join(tmp, "tmp", "unrelatedapp")
		for _, d := range []string{repoDir, otherDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}

		seedClaude(t, claude, "agyapp", "sess-agy-1510",
			"why does the frobnicator stall on startup",
			"the frobnicator stalled on startup because ZORBWAX_LIMIT was unset; exporting ZORBWAX_LIMIT=4 cleared it")

		dir := filepath.Join(tmp, "index.db")
		t.Setenv("DEJA_INDEX_DIR", dir)
		if err := index.Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
		return dir, repoDir, otherDir
	}

	invoke := func(t *testing.T, dir string, payload map[string]any) antigravityHookResponse {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := runHookAntigravity(dir, bytes.NewReader(raw), &out); err != nil {
			t.Fatal(err)
		}
		var resp antigravityHookResponse
		if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
			t.Fatalf("invalid hook JSON output: %q (%v)", out.String(), err)
		}
		return resp
	}

	t.Run("invocation_0_cwd_fallback_when_workspacePaths_and_transcriptPath_empty", func(t *testing.T) {
		dir, repoDir, _ := setup(t)
		resp0 := invoke(t, dir, map[string]any{
			"invocationNum":  0,
			"conversationId": "conv-1510-0",
			"workspacePaths": []string{},
			"transcriptPath": "",
			"cwd":            repoDir,
			"lastUserInput":  "why does the frobnicator stall on startup",
		})
		if len(resp0.InjectSteps) == 0 || !strings.Contains(resp0.InjectSteps[0].EphemeralMessage, antigravityLead) {
			t.Fatalf("invocation 0 missing digest with cwd fallback: %+v", resp0)
		}
		if !strings.Contains(resp0.InjectSteps[0].EphemeralMessage, "frobnicator") {
			t.Fatalf("invocation 0 missing recalled content: %q", resp0.InjectSteps[0].EphemeralMessage)
		}
	})

	t.Run("invocation_1_lastUserInput_and_cwd_fallback_without_transcript", func(t *testing.T) {
		dir, repoDir, _ := setup(t)
		resp1 := invoke(t, dir, map[string]any{
			"invocationNum":  1,
			"conversationId": "conv-1510-1",
			"workspacePaths": []string{},
			"transcriptPath": "",
			"cwd":            repoDir,
			"lastUserInput":  "<USER_REQUEST>\nwhy does the frobnicator stall on startup\n</USER_REQUEST>",
		})
		if len(resp1.InjectSteps) == 0 || !strings.Contains(resp1.InjectSteps[0].EphemeralMessage, "frobnicator") {
			t.Fatalf("invocation 1 did not recall using lastUserInput + cwd: %+v", resp1)
		}
		if strings.Contains(resp1.InjectSteps[0].EphemeralMessage, antigravityLead) {
			t.Fatalf("invocation 1 unexpectedly repeated digest lead: %q", resp1.InjectSteps[0].EphemeralMessage)
		}
	})

	t.Run("workspacePaths_wins_over_cwd", func(t *testing.T) {
		dir, repoDir, otherDir := setup(t)
		resp := invoke(t, dir, map[string]any{
			"invocationNum":  0,
			"conversationId": "conv-1510-b",
			"workspacePaths": []string{repoDir},
			"transcriptPath": "",
			"cwd":            otherDir,
			"lastUserInput":  "why does the frobnicator stall on startup",
		})
		if len(resp.InjectSteps) == 0 || !strings.Contains(resp.InjectSteps[0].EphemeralMessage, "frobnicator") {
			t.Fatalf("workspacePaths did not take precedence over cwd: %+v", resp)
		}
	})

	t.Run("malformed_json_outputs_empty_object", func(t *testing.T) {
		tmp := hermeticEnv(t)
		dir := filepath.Join(tmp, "index.db")
		var out bytes.Buffer
		if err := runHookAntigravity(dir, strings.NewReader("{malformed"), &out); err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(out.String()) != "{}" {
			t.Fatalf("got %q, want {}", out.String())
		}
	})
}
