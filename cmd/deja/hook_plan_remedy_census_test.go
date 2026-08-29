package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// #2458 gave the plan hook the remedy, and only when the census had no command
// of its own — so the better the match, the less deja said. A plan about to run
// the command four sessions ran into a wall with was told the wall and the
// command, while the confirmed pair that got past it was dropped (#2485).
func TestThePlanFindingCarriesTheRemedyBesideTheCensusCommand(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := func(m int) string { return now.Add(-time.Duration(m) * time.Minute).Format(time.RFC3339) }
	write := func(sid string, msgs [][2]any, base int) {
		var lines []string
		for i, m := range msgs {
			rec := map[string]any{"type": m[0], "sessionId": sid, "timestamp": at(base - i), "cwd": "/work/app",
				"message": map[string]any{"role": m[0], "content": m[1]}}
			b, _ := json.Marshal(rec)
			lines = append(lines, string(b))
		}
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wall := `psql: error: connection to the orders migration database failed: Connection refused`
	topics := []string{"the sidebar layout", "the invoice pdf renderer", "the webpack config", "the login throttle",
		"the avatar uploader", "the search autocomplete", "the cron scheduler", "the email templates",
		"the feature flags", "the toast component", "the graphql schema", "the retry budget",
		"the docker base image", "the css variables", "the storybook snapshots", "the i18n strings"}
	for i, top := range topics {
		write(fmt.Sprintf("f%d", i), [][2]any{
			{"user", "work on " + top + " today, it has been slow to load and the team wants it done"},
			{"assistant", "looked at " + top + ", changed what was needed and the tests pass"},
		}, 900-10*i)
	}
	for k := 0; k < 4; k++ {
		write(fmt.Sprintf("m%d", k), [][2]any{
			{"user", fmt.Sprintf("run the orders migration (%d) against the production database", k)},
			{"assistant", []any{map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "make migrate-orders"}}}},
			{"user", []any{map[string]any{"type": "tool_result", "is_error": true, "content": wall}}},
			{"assistant", "the orders migration needs the database reachable first"},
			{"assistant", []any{map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "docker compose up -d db"}}}},
			{"user", []any{map[string]any{"type": "tool_result", "content": "db started"}}},
		}, 300-30*k)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	plan := "1. run the orders migration against the production database\n2. verify the row counts"
	findings := planFindings(dir, plan, "")
	if len(findings) != 1 {
		t.Fatalf("the fixture is meant to produce one finding, got %d: %v", len(findings), findings)
	}
	got := findings[0]
	if !strings.Contains(got, "make migrate-orders") {
		t.Errorf("the census command the plan matched is missing:\n  %s", got)
	}
	if !strings.Contains(got, "docker compose up -d db") {
		t.Errorf("deja knows what got past this wall and did not say it:\n  %s", got)
	}
}
