package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// The command as it was run carries the way one machine reached the directory,
// which is the longest part of the line and the only part the reader cannot
// use. A list of six of them was mostly the same home directory six times.
func TestOrientShowsTheCommandWithoutTheWayItGotThere(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"$ cd /Users/x/code/svc; go test ./...", "go test ./..."},
		{"cd /Users/x/code/svc && make test", "make test"},
		{"cd a; cd b; go vet ./...", "go vet ./..."},
		{"go test ./...", "go test ./..."},
		{"cdk deploy --all", "cdk deploy --all"}, // not a cd
		{"cd /Users/x/code/svc", "cd /Users/x/code/svc"},
	} {
		if got := orientCommand(tc.in); got != tc.want {
			t.Errorf("orientCommand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	long := "go test ./... -run 'TestSomethingWithAVeryLongNameIndeed|TestAnotherOne' -count=1 -v"
	if got := orientCommand(long); len([]rune(got)) > orientCommandMax+1 {
		t.Errorf("a long command was not cut: %q", got)
	}
}

// A path under the directory the question came from reads as the editor names
// it. One outside it keeps its full name, because a bare "MEMORY.md" the
// reader cannot find is worse than a long path.
func TestOrientNamesPathsFromWhereTheQuestionWasAsked(t *testing.T) {
	// Named the way the reader's editor names it, which on Windows means
	// backslashes: the separator is the platform's, not the transcript's.
	want := filepath.Join("internal", "store", "store.go")
	if got := orientPath("/w/svc/internal/store/store.go", "/w/svc"); got != want {
		t.Errorf("path under the cwd = %q, want %q", got, want)
	}
	if got := orientPath("/other/place/notes.md", "/w/svc"); got != "/other/place/notes.md" {
		t.Errorf("path outside the cwd was rewritten to %q", got)
	}
	if got := orientPath("/w/svc/main.go", ""); got != "/w/svc/main.go" {
		t.Errorf("with no cwd the path changed: %q", got)
	}
}

// orient takes no subject, so an empty store has to say the store is empty
// rather than answer with nothing — an agent told nothing exists invents
// something, which is the failure every empty answer here is written against.
func TestOrientOnAStoreWithNothingInItSaysSo(t *testing.T) {
	hermeticEnv(t)
	out, err := callMCPTool(t.TempDir(), "orient", json.RawMessage(`{"mode":"orient"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No past session") {
		t.Errorf("orient on an empty store answered %q", out)
	}
}

// A project whose sessions ran nothing that repeats still has sessions. orient
// said none had worked there while recall for the same project found them.
func TestOrientCountsSessionsWithNothingToMap(t *testing.T) {
	dir, _ := tuiStore(t)
	out, err := callMCPTool(dir, "orient", json.RawMessage(`{"mode":"orient","project":"payments"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "No past session") || !strings.Contains(out, "2 sessions worked in payments") {
		t.Errorf("orient on a project with two sessions answered %q", out)
	}
}

// The mode is declared, reachable through the one tool, and needs no payload —
// q is what every other mode reads, and orient asking for one would be asking
// the agent a question it called orient to avoid.
func TestOrientIsReachableWithoutAnArgument(t *testing.T) {
	if _, ok := dispatcherModes["orient"]; !ok {
		t.Fatal("orient is not one of the modes the tool dispatches")
	}
	raw := json.RawMessage(`{"mode":"orient","q":"ignored"}`)
	if got := string(spreadQ("orient", raw)); got != string(raw) {
		t.Errorf("q was spread into orient's arguments: %s", got)
	}
	schema, _ := dejaTool()["inputSchema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	mode, _ := props["mode"].(map[string]any)
	modes, _ := mode["enum"].([]string)
	found := false
	for _, m := range modes {
		found = found || m == "orient"
	}
	if !found {
		t.Errorf("the declared modes are %v, without orient", modes)
	}
}

// The map arrives unasked in the session-start digest, so the bar it applies
// has to be outright rather than by ranking: a project with nothing recurring
// gets nothing, because an empty map still costs every prompt of the session.
func TestTheDigestMapSaysNothingWhenThereIsNoPractice(t *testing.T) {
	hermeticEnv(t)
	if got := orientDigestBlock(t.TempDir(), t.TempDir(), []string{"nowhere"}, policy.ActivationAuto); got != "" {
		t.Errorf("an empty store produced a map block:\n%s", got)
	}
}

func TestOrientHelpersFilterEphemeralAndRankSuiteVerify(t *testing.T) {
	if !orientSuiteVerifyCommand("go test ./...") {
		t.Error("expected go test ./... to be a suite verify command")
	}
	if orientSuiteVerifyCommand("go test ./cmd/deja -run TestOrient") {
		t.Error("targeted -run test command must not be ranked as full suite verify")
	}
	if !orientEphemeralCommand("go test /tmp/probe_test.go") {
		t.Error("expected /tmp/ command to be filtered as ephemeral")
	}
	if !isOrientEphemeralFile("/w/svc/cmd/deja/zz_probe_test.go") {
		t.Error("expected zz_probe_test.go to be filtered as ephemeral")
	}
	if got := orientPath("/w/svc/.claude/worktrees/agent-123/cmd/deja/main.go", "/w/svc"); got != filepath.Join("cmd", "deja", "main.go") {
		t.Errorf("orientPath with worktree segment = %q, want cmd/deja/main.go", got)
	}
	if !orientSkipFixError("--- SKIP: TestLiveMCPPerfBreakdown (0.00s)") {
		t.Error("expected --- SKIP: to be skipped in orient fixes")
	}
	if !orientSkipFixError("internal/search/search.go:393:69: undefined: digest") {
		t.Error("expected one-off compiler undefined error to be skipped in orient fixes")
	}
	if !orientDestructiveRemedy("go test ./... && git reset --hard HEAD") {
		t.Error("expected git reset --hard remedy to be flagged as destructive")
	}
	if orientTestRemedyMatchesFail("--- FAIL: TestFoo", "go test ./cmd/deja -run TestBar") {
		t.Error("expected go test remedy to be rejected for --- FAIL: error line")
	}
	if !orientTautologicalEdit("internal/search/search.go:10: err", "internal/search/search.go") {
		t.Error("expected tautological edit of the same file in compiler error to be skipped")
	}
}

func TestOrientRepoTreeKeepsSubdirBasenamesAndDropsDeletedFiles(t *testing.T) {
	repo := t.TempDir()
	cmdDir := filepath.Join(repo, "cmd", "deja")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree := orientRepoTree(repo)
	if !tree.exists("cmd/deja/main.go") {
		t.Error("expected relative path cmd/deja/main.go to exist in repoFileTree")
	}
	if !tree.exists("main.go") {
		t.Error("expected bare basename main.go in subdirectory to exist in repoFileTree")
	}
	if tree.exists("deleted_probe.py") {
		t.Error("unexpected match for deleted_probe.py")
	}
}

func TestHookToolMentionsFileAndRepoMemoryScoping(t *testing.T) {
	if mentionsFile("/Users/x/goprojects/deja-vu/cmd/deja/help_commands.go", "deja") {
		t.Error("mentionsFile must not match parent dir 'deja' when filename has specific stem 'help_commands'")
	}
	if !mentionsFile("/Users/x/goprojects/deja-vu/cmd/deja/help_commands.go", "help_commands returned an error") {
		t.Error("mentionsFile should match specific stem 'help_commands'")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := sources.AddRepoEntries(root, []sources.RepoEntry{
		{
			ID:    "e1",
			Date:  "2026-11-01",
			Kind:  "gotcha",
			Text:  "cursor-state-vscdb-too-large.html needs synced table headers",
			Files: []string{"docs/guide/cursor-state-vscdb-too-large.html"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	targetGo := filepath.Join(root, "cmd", "deja", "rules_candidates.go")
	if err := os.MkdirAll(filepath.Dir(targetGo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetGo, []byte("package main\ntype state struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := repoMemoryDecisionForFile(root, targetGo, []string{"state"}, policy.Load()); got != "" {
		t.Errorf("repoMemoryDecisionForFile cross-matched file-scoped entry onto unrelated Go file: %q", got)
	}
}

func TestRulesCandidatesRejectsNumberedListsAndMidSentenceNet(t *testing.T) {
	if isCorrection("1. да\n2. нет\n3. оставим на потом") {
		t.Error("numbered list answer must not be classified as a user correction")
	}
	if isCorrection("у нас нет аккаунта на этой платформе") {
		t.Error("mid-sentence 'нет' statement must not be classified as a user correction")
	}
	if !isCorrection("нет, стало хуже значительно с багами и аномалиями") {
		t.Error("leading 'нет, стало хуже...' correction should be detected")
	}
}

func TestFixResolvesNearestRecordedErrorForPartialQuery(t *testing.T) {
	dir := seedDay1MagicStore(t)
	var out bytes.Buffer
	if err := runFix(dir, []string{"database is closed"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "make clean && make CGO_ENABLED=0") {
		t.Errorf("runFix for partial error query did not resolve and print the fix:\n%s", got)
	}
}

func TestNearErrorProbesAndStandingCorrectionFilter(t *testing.T) {
	probes := nearErrorProbesFromOutput("ok  pkg 0.1s\n--- FAIL: TestGuideSnippetsFitWhatSearchShows (0.04s)\nFAIL\n")
	if len(probes) != 1 || probes[0] != "--- FAIL: TestGuideSnippetsFitWhatSearchShows" {
		t.Errorf("nearErrorProbesFromOutput = %v, want [--- FAIL: TestGuideSnippetsFitWhatSearchShows]", probes)
	}
	if !isStandingCorrection("не, сам то .deja/ пусть будет локально, потестим насколько с ним лучше, а вот пушить не надо чтоб не палить его") {
		t.Error("expected .deja/ git push rule to be recognized as a standing correction")
	}
	if isStandingCorrection("ок, без просьбы процитировать (это пока не надо нам)") {
		t.Error("situational 'пока не надо' remark must not be promoted to standing repo memory")
	}
}

func TestFilterAndSortOrientCmdsPrefersRecentVerifyCommandsOverStaleOnes(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-60 * 24 * time.Hour)
	cmds := []howEntry{
		{
			Command:  "go test -race ./...",
			Sessions: map[string]bool{"s1": true, "s2": true, "s3": true, "s4": true, "s5": true},
			Last:     stale,
		},
		{
			Command:  "./scripts/verify-fast.sh",
			Sessions: map[string]bool{"s6": true, "s7": true},
			Last:     now,
		},
	}
	got := filterAndSortOrientCmds(cmds)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Command != "./scripts/verify-fast.sh" {
		t.Errorf("got[0].Command = %q, want ./scripts/verify-fast.sh ahead of stale command", got[0].Command)
	}
}
