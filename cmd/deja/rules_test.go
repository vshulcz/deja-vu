package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rulesHome is a hermetic home where deja is recorded as installed for the
// given targets, so nothing outside the temp directory can become a target
// whatever the developer has exported.
func rulesHome(t *testing.T, targets ...string) string {
	t.Helper()
	hermeticEnv(t)
	t.Setenv("GROK_HOME", "")
	t.Setenv("KIMI_CODE_HOME", "")
	t.Setenv("GOOSE_PATH_ROOT", "")
	home := os.Getenv("HOME")
	b, err := json.Marshal(wiringState{Targets: targets})
	if err != nil {
		t.Fatal(err)
	}
	writeRulesTestFile(t, wiringStatePath(), string(b))
	return home
}

func writeRulesTestFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readRulesTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func rulesOut(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := runRulesTo(&buf, args)
	return buf.String(), err
}

// The status names every installed agent and what its copy is: in sync, stale,
// missing, or an agent whose global rules file deja does not know.
func TestRulesStatusNamesEveryState(t *testing.T) {
	home := rulesHome(t, "claude-auto", "codex", "opencode", "cursor", "statusline")
	for _, d := range []string{".claude", ".codex", filepath.Join(".config", "opencode")} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeRulesTestFile(t, rulesSourcePath(), "- commits are one line\n")
	claude := filepath.Join(home, ".claude", "CLAUDE.md")
	codex := filepath.Join(home, ".codex", "AGENTS.md")
	writeRulesTestFile(t, claude, "mine\n\n"+rulesBlock("- commits are one line"))
	writeRulesTestFile(t, codex, rulesBlock("- an older rule"))

	out, err := rulesOut(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"claude-code  in sync",
		"codex        stale",
		"opencode     missing",
		"cursor       no global rules file known",
		"run `deja rules sync`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "statusline") {
		t.Errorf("statusline is not an agent and has no rules to read:\n%s", out)
	}
}

// Sync writes one block per file, keeps every byte of the reader's own text,
// and a second sync changes nothing — CRLF endings and a byte order mark
// included, which is where a rewrite would show up as "updated" every time.
func TestRulesSyncKeepsTheReadersTextAndIsIdempotent(t *testing.T) {
	home := rulesHome(t, "claude-code", "grok")
	writeRulesTestFile(t, rulesSourcePath(), "- write like a person\r\n- no push without an ok\r\n\r\n")
	claude := filepath.Join(home, ".claude", "CLAUDE.md")
	theirs := "\xef\xbb\xbf# Mine\r\n\r\nKeep this.\r\n"
	writeRulesTestFile(t, claude, theirs)
	grok := filepath.Join(home, ".grok", "AGENTS.md")
	writeRulesTestFile(t, grok, "own text\n\n"+guidanceText("append"))

	if _, err := rulesOut(t, "sync"); err != nil {
		t.Fatal(err)
	}
	got := readRulesTestFile(t, claude)
	want := theirs + "\r\n" + strings.ReplaceAll(rulesBlock("- write like a person\n- no push without an ok"), "\n", "\r\n")
	if got != want {
		t.Fatalf("CLAUDE.md after sync:\n%q\nwant\n%q", got, want)
	}
	g := readRulesTestFile(t, grok)
	if !strings.Contains(g, guidanceStart) || !strings.Contains(g, rulesStart) || !strings.HasPrefix(g, "own text\n") {
		t.Fatalf("grok's file lost something:\n%s", g)
	}

	out, err := rulesOut(t, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "rules unchanged") != 2 {
		t.Fatalf("second sync rewrote a file:\n%s", out)
	}
	if st, _ := rulesState(claude, "- write like a person\n- no push without an ok"); st != "in sync" {
		t.Fatalf("state after sync = %q", st)
	}
}

// Emptying the rules takes the copies back out; a file that held nothing but
// the block is removed rather than left empty.
func TestRulesSyncWithNoRulesRemovesTheBlocks(t *testing.T) {
	home := rulesHome(t, "claude-code", "codex")
	claude := filepath.Join(home, ".claude", "CLAUDE.md")
	codex := filepath.Join(home, ".codex", "AGENTS.md")
	writeRulesTestFile(t, claude, "mine\n\n"+rulesBlock("- old"))
	writeRulesTestFile(t, codex, rulesBlock("- old"))
	writeRulesTestFile(t, rulesSourcePath(), "\n")

	if _, err := rulesOut(t, "sync"); err != nil {
		t.Fatal(err)
	}
	if got := readRulesTestFile(t, claude); got != "mine\n" {
		t.Fatalf("CLAUDE.md = %q, want the reader's text alone", got)
	}
	if _, err := os.Stat(codex); !os.IsNotExist(err) {
		t.Fatalf("an AGENTS.md that was only deja's block should be gone, stat err = %v", err)
	}
}

// Nothing is created in a directory the agent never made: a file there is one
// nothing reads.
func TestRulesSyncSkipsAnAgentWithNoDirectory(t *testing.T) {
	home := rulesHome(t, "codex")
	writeRulesTestFile(t, rulesSourcePath(), "- a rule\n")
	if _, err := rulesOut(t, "sync"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); !os.IsNotExist(err) {
		t.Fatalf("sync created codex's directory: %v", err)
	}
}

func TestRulesRefusesAnOversizedOrMarkedSource(t *testing.T) {
	rulesHome(t, "claude-code")
	writeRulesTestFile(t, rulesSourcePath(), strings.Repeat("x", rulesMaxBytes+1))
	if _, err := rulesOut(t, "sync"); err == nil || !strings.Contains(err.Error(), "the limit is 8192") {
		t.Fatalf("oversized rules: err = %v", err)
	}
	writeRulesTestFile(t, rulesSourcePath(), "- a rule\n"+rulesEnd+"\n")
	if _, err := rulesOut(t, "sync"); err == nil || !strings.Contains(err.Error(), "block markers") {
		t.Fatalf("rules holding a marker: err = %v", err)
	}
}

// A block whose end marker is gone cannot be bounded. That file is named and
// left alone; the others are still written.
func TestRulesSyncNamesAnUnboundedFileAndWritesTheRest(t *testing.T) {
	home := rulesHome(t, "claude-code", "codex")
	claude := filepath.Join(home, ".claude", "CLAUDE.md")
	codex := filepath.Join(home, ".codex", "AGENTS.md")
	broken := "mine\n" + rulesStart + "\nhalf a block\n"
	writeRulesTestFile(t, claude, broken)
	writeRulesTestFile(t, codex, "theirs\n")
	writeRulesTestFile(t, rulesSourcePath(), "- a rule\n")

	out, err := rulesOut(t)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "marker without its pair") {
		t.Fatalf("status did not say the block is broken:\n%s", out)
	}
	_, err = rulesOut(t, "sync")
	if err == nil || !strings.Contains(err.Error(), claude) {
		t.Fatalf("sync err = %v, want one naming %s", err, claude)
	}
	if got := readRulesTestFile(t, claude); got != broken {
		t.Fatalf("the unbounded file was touched: %q", got)
	}
	if !strings.Contains(readRulesTestFile(t, codex), rulesStart) {
		t.Fatal("codex was not written after claude's refusal")
	}
}

// Uninstall takes the copy with it and leaves the reader's text.
func TestUninstallDropsTheRulesBlock(t *testing.T) {
	home := rulesHome(t, "codex")
	codex := filepath.Join(home, ".codex", "AGENTS.md")
	writeRulesTestFile(t, codex, "theirs\n\n"+rulesBlock("- a rule"))
	if _, err := captureRun(t, "uninstall", "codex"); err != nil {
		t.Fatalf("uninstall codex: %v", err)
	}
	if got := readRulesTestFile(t, codex); got != "theirs\n" {
		t.Fatalf("AGENTS.md after uninstall = %q", got)
	}
}

// doctor says which copies are behind, and nothing when there are no rules.
func TestDoctorNamesAgentsBehindTheRules(t *testing.T) {
	home := rulesHome(t, "claude-code", "codex")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeRulesTestFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), rulesBlock("- a rule"))
	if note := rulesDoctorNote(); note != "" {
		t.Fatalf("no rules.md, yet doctor says %q", note)
	}
	writeRulesTestFile(t, rulesSourcePath(), "- a rule\n")
	var buf bytes.Buffer
	doctorCommands(&buf)
	if !strings.Contains(buf.String(), "rules        codex behind") {
		t.Fatalf("doctor lacks the rules line:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "claude-code behind") {
		t.Fatalf("an agent in sync was named:\n%s", buf.String())
	}
}
