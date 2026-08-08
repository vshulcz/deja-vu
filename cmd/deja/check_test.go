package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCheckUsesTheHookPlanCore(t *testing.T) {
	withPlanLookup(t, samplePlanMatches(`C:\work\repo`))
	dir := filepath.Join(t.TempDir(), "index.db")
	plan := "1. Run timeout around deploy_probe"

	var hookOut bytes.Buffer
	hookInput := `{
		"hook_event_name":"PreToolUse",
		"tool_name":"ExitPlanMode",
		"tool_input":{"plan":"1. Run timeout around deploy_probe"},
		"session_id":""
	}`
	if err := runHookPlan(dir, strings.NewReader(hookInput), &hookOut); err != nil {
		t.Fatal(err)
	}

	var resp struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(hookOut.Bytes(), &resp); err != nil {
		t.Fatalf("hook JSON = %q: %v", hookOut.String(), err)
	}
	hookPlain := strings.TrimPrefix(resp.HookSpecificOutput.AdditionalContext, recallFrameHeader)
	hookPlain = strings.TrimSuffix(hookPlain, recallFrameFooter)

	var checkOut bytes.Buffer
	if err := runCheck(dir, []string{"-"}, strings.NewReader(plan), &checkOut); err != nil {
		t.Fatal(err)
	}
	checkPlain := strings.TrimSuffix(checkOut.String(), "\n")
	if checkPlain != hookPlain {
		t.Fatalf("check and hook differ:\ncheck: %q\nhook:  %q", checkPlain, hookPlain)
	}
	if strings.Contains(checkOut.String(), "<deja-recall>") {
		t.Fatalf("check output contains a recall frame: %q", checkOut.String())
	}
	if strings.Count(strings.TrimSpace(checkOut.String()), "\n") != 0 {
		t.Fatalf("one finding was not printed as one line: %q", checkOut.String())
	}
}

func TestRunCheckSilentMiss(t *testing.T) {
	withPlanLookup(t, nil)
	var out bytes.Buffer
	if err := runCheck(
		filepath.Join(t.TempDir(), "index.db"),
		[]string{"-"},
		strings.NewReader("1. Run timeout around deploy_probe"),
		&out,
	); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("miss produced %q", out.String())
	}
}

func TestRunCheckRequiresDash(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"PLAN.md"},
		{"-", "extra"},
	} {
		var out bytes.Buffer
		err := runCheck(
			filepath.Join(t.TempDir(), "index.db"),
			args,
			strings.NewReader("1. Run timeout around deploy_probe"),
			&out,
		)
		if err == nil {
			t.Fatalf("args %v returned no usage error", args)
		}
		if !strings.Contains(err.Error(), "check needs '-'") {
			t.Fatalf("args %v error = %q", args, err)
		}
		if out.Len() != 0 {
			t.Fatalf("args %v produced output %q", args, out.String())
		}
	}
}
