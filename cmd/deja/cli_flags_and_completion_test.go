package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestHookPromptToolAfterSessionEndCompletion(t *testing.T) {
	scripts := map[string]string{}
	for _, shell := range completionTestShells {
		scripts[shell] = emittedCompletion(t, shell)
	}
	for _, tc := range []struct {
		cmd   string
		flags []string
	}{
		{"hook-prompt", []string{"--plain", "--copilot", "--strict", "--mcode"}},
		{"hook-tool-after", []string{"--plain", "--copilot", "--mcode"}},
		{"hook-session-end", []string{"--json"}},
	} {
		for _, shell := range completionTestShells {
			block := shellCommandBlock(shell, scripts[shell], tc.cmd)
			for _, flag := range tc.flags {
				if !strings.Contains(block, flag) {
					t.Errorf("%s completion for %q does not offer %s:\n%s", shell, tc.cmd, flag, block)
				}
			}
			// None of these commands should fall through to search's --re flag.
			if strings.Contains(block, "--re") {
				t.Errorf("%s completion for %q fell through to search flags:\n%s", shell, tc.cmd, block)
			}
		}
	}
}

func TestSearchAndStatsRoleAndHarnessCompletion(t *testing.T) {
	roles := strings.Join(knownRoles, " ")
	bash := emittedCompletion(t, "bash")
	for _, cmd := range []string{"stats", "search"} {
		block := shellCommandBlock("bash", bash, cmd)
		if !strings.Contains(block, `"$prev" == "--role"`) || !strings.Contains(block, roles) {
			t.Errorf("bash completion for %q does not complete --role values:\n%s", cmd, block)
		}
	}
	ps := emittedCompletion(t, "powershell")
	psSearch := shellCommandBlock("powershell", ps, "search")
	for _, want := range []string{`$previous -eq '--harness'`, `$harnesses`, `$previous -eq '--role'`, `$roles`} {
		if !strings.Contains(psSearch, want) {
			t.Errorf("powershell completion for search missing %s:\n%s", want, psSearch)
		}
	}
}

func TestEmptyFlagValuesRefused(t *testing.T) {
	tmp := t.TempDir()
	for _, tc := range []struct {
		name string
		run  func() error
		want string
	}{
		{"remember --tag empty", func() error { return runRemember(tmp, []string{"--tag", "", "some note"}) }, "remember: --tag needs value"},
		{"remember --tag spaces", func() error { return runRemember(tmp, []string{"--tag", "   ", "some note"}) }, "remember: --tag needs value"},
		{"promote --tag empty", func() error { return runPromote(tmp, []string{"--tag", "", "abc"}, &bytes.Buffer{}) }, "promote: --tag needs a value"},
		{"promote --tag spaces", func() error { return runPromote(tmp, []string{"--tag", "  ", "abc"}, &bytes.Buffer{}) }, "promote: --tag needs a value"},
		{"promote --note empty", func() error { return runPromote(tmp, []string{"--note", "", "abc"}, &bytes.Buffer{}) }, "promote: --note needs text"},
		{"promote --note spaces", func() error { return runPromote(tmp, []string{"--note", "  ", "abc"}, &bytes.Buffer{}) }, "promote: --note needs text"},
		{"promote --to empty", func() error { return runPromote(tmp, []string{"--to", "", "abc"}, &bytes.Buffer{}) }, "promote: --to needs a path"},
		{"promote --to spaces", func() error { return runPromote(tmp, []string{"--to", "  ", "abc"}, &bytes.Buffer{}) }, "promote: --to needs a path"},
		{"view --out empty", func() error { return runView(tmp, []string{"--out", ""}) }, "view: --out needs a path"},
		{"view --out spaces", func() error { return runView(tmp, []string{"--out", "  "}) }, "view: --out needs a path"},
		{"restore -o empty", func() error { return runRestore(tmp, []string{"-o", "", "file.go"}, &bytes.Buffer{}) }, "restore: -o needs value"},
		{"restore --out spaces", func() error { return runRestore(tmp, []string{"--out", "  ", "file.go"}, &bytes.Buffer{}) }, "restore: -o needs value"},
	} {
		err := tc.run()
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestDashDashTreatsRemainingWordsAsPositional(t *testing.T) {
	tmp := t.TempDir()
	for _, args := range [][]string{
		{"--", "-first", "--bogus"},
		{"--", "first", "--repo"},
	} {
		err := runRemember(tmp, args)
		if err == nil || !strings.Contains(err.Error(), "expected one text argument") {
			t.Errorf("runRemember(%v): got %v, want 'expected one text argument'", args, err)
		}
	}
	for _, args := range [][]string{
		{"--", "file.go", "--bogus"},
		{"--", "file.go", "--json"},
	} {
		_, _, _, err := parseBlame(args)
		if err == nil || err.Error() != "blame accepts one path" {
			t.Errorf("parseBlame(%v): got %v, want 'blame accepts one path'", args, err)
		}
	}
}

func TestNegativeLimitErrorMessageNamesZeroFloor(t *testing.T) {
	tmp := t.TempDir()
	for _, tc := range []struct {
		name string
		run  func() error
		want string
	}{
		{"recap", func() error { return runRecap(tmp, []string{"--limit", "-1"}, &bytes.Buffer{}) }, `recap: --limit wants a number from 0 up, got "-1"`},
		{"tests", func() error { return runTests(tmp, []string{"--limit", "-1"}, &bytes.Buffer{}) }, `tests: --limit wants a number from 0 up, got "-1"`},
		{"secrets", func() error { return runSecrets(tmp, []string{"--limit", "-1"}, &bytes.Buffer{}) }, `secrets: --limit wants a number from 0 up, got "-1"`},
	} {
		err := tc.run()
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestRecapJSONHonorsLimit(t *testing.T) {
	tmp := hermeticEnv(t)
	indexDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", indexDir)

	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-recap-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, id := range []string{"r1", "r2", "r3"} {
		ts := now.Add(-time.Duration(i+1) * time.Hour).Format(time.RFC3339)
		lines := []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/recap-proj","timestamp":"` + ts + `","message":{"role":"user","content":"investigate recap session ` + id + `"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/recap-proj","timestamp":"` + ts + `","message":{"role":"assistant","content":"Decision: keep recap session ` + id + ` deterministic."}}`,
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(indexDir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runRecap(indexDir, []string{"--since", "7d", "--limit", "1", "--json"}, &buf); err != nil {
		t.Fatalf("runRecap: %v", err)
	}
	var out recapJSON
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal recapJSON: %v", err)
	}
	if out.Considered != 3 {
		t.Errorf("sessions_in_window = %d, want 3", out.Considered)
	}
	if len(out.Sessions) != 1 {
		t.Errorf("len(sessions) = %d, want 1", len(out.Sessions))
	}
}
