package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
)

func TestSafeScanRootAndSkippedScanDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if got := safeScanRoot(home); got != "" {
			t.Fatalf("safeScanRoot(home) = %q, want empty", got)
		}
		if got := safeScanRoot(filepath.Join(home, "Library", "Caches")); got != "" {
			t.Fatalf("safeScanRoot(home/Library/Caches) = %q, want empty", got)
		}
	}
	tmp := t.TempDir()
	if got := safeScanRoot(tmp); got != tmp {
		t.Fatalf("safeScanRoot(t.TempDir()) = %q, want %q", got, tmp)
	}
	for _, skip := range []string{"Library", "node_modules", ".git", "venv", "target"} {
		if !isSkippedScanDir(skip) {
			t.Fatalf("isSkippedScanDir(%q) = false, want true", skip)
		}
	}
	if isSkippedScanDir("cmd") {
		t.Fatal("isSkippedScanDir(\"cmd\") = true, want false")
	}
}

func TestConversationalRussianPromptTermsFiltered(t *testing.T) {
	terms := extractHistoricalQueryTerms("давай дальше изучать вопрос низкоуровнево и улучшать инструмент", nil)
	for _, bad := range []string{"давай", "дальше", "вопрос"} {
		for _, got := range terms {
			if got == bad {
				t.Fatalf("extractHistoricalQueryTerms kept conversational stop word %q in %v", bad, terms)
			}
		}
	}
}

func TestMalformedStandingRuleFiltered(t *testing.T) {
	for _, bad := range []string{
		"Do not suggest ,: ",
		"Never use ,",
		"Always ;",
	} {
		if !isMalformedStandingRuleText(bad) {
			t.Fatalf("isMalformedStandingRuleText(%q) = false, want true", bad)
		}
	}
	if isMalformedStandingRuleText("Never push to origin without explicit user approval.") {
		t.Fatal("isMalformedStandingRuleText rejected valid rule")
	}
}

func TestFileHookPrecisionHelpers(t *testing.T) {
	if !hasGenericBasename("cmd/app/main.go") {
		t.Fatal("hasGenericBasename(main.go) = false, want true")
	}
	if hasGenericBasename("cmd/deja/hook_tool_file.go") {
		t.Fatal("hasGenericBasename(hook_tool_file.go) = true, want false")
	}
	if mentionsFile("cmd/app/main.go", "edited main.go to fix bug") {
		t.Fatal("mentionsFile matched generic basename main.go without directory")
	}
	if !mentionsFile("cmd/app/main.go", "edited cmd/app/main.go to fix bug") {
		t.Fatal("mentionsFile failed to match qualified cmd/app/main.go")
	}
	clipped := trimClippedDecision("check that the handler validates tokens in this specific request", 42)
	if strings.HasSuffix(clipped, " in…") || strings.HasSuffix(clipped, " in this…") {
		t.Fatalf("trimClippedDecision left dangling connector: %q", clipped)
	}
}

func TestEditTargetMismatchLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sample.go")
	src := "package main\n\nfunc greet(name string) string {\n\treturn \"hello \" + name\n}\n"
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	oldStr := "func greet(name string) string {\n\treturn \"hi \" + name\n}"
	got := editTargetMismatchLine(dir, "sample.go", oldStr, "func greet() {}")
	if !strings.Contains(got, "edit target not verbatim on disk; closest L3-L5:") {
		t.Fatalf("editTargetMismatchLine = %q, want closest L3-L5 hint", got)
	}
	if strings.Contains(got, "\n") || len(got) > 240 {
		t.Fatalf("editTargetMismatchLine must be single-line <= 240B, got %q", got)
	}
}

func TestFailingTestSourceLinePythonTraceback(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pyFile := filepath.Join(srcDir, "worker.py")
	pySrc := "def run():\n    x = 1\n    raise ValueError('bad input')\n"
	if err := os.WriteFile(pyFile, []byte(pySrc), 0o600); err != nil {
		t.Fatal(err)
	}
	tb := "Traceback (most recent call last):\n" +
		"  File \"/usr/lib/python3.11/site-packages/runner.py\", line 10, in exec\n" +
		"    fn()\n" +
		"  File \"" + pyFile + "\", line 3, in run\n" +
		"    raise ValueError('bad input')\n" +
		"ValueError: bad input\n"
	got := failingTestSourceLine(dir, tb, policy.Policy{})
	if !strings.HasPrefix(got, "failing test line — pkg/worker.py:3:") || !strings.Contains(got, "raise ValueError") {
		t.Fatalf("failingTestSourceLine(python traceback) = %q, want prefix failing test line — pkg/worker.py:3:", got)
	}
	winTB := "Traceback (most recent call last):\n" +
		"  File \"" + pyFile + "\", line 3, in run\n" +
		"    raise ValueError('bad input')\n" +
		"  File \"C:\\repo\\.venv\\Lib\\site-packages\\runner.py\", line 10, in exec\n" +
		"    fn()\n" +
		"  File \"C:\\Python311\\Lib\\python3.11\\unittest.py\", line 20, in run\n" +
		"    fn()\n" +
		"ValueError: bad input\n"
	targets := extractFailingTestTargets(winTB)
	if len(targets) != 1 || targets[0].filePart != pyFile {
		t.Fatalf("extractFailingTestTargets(windows venv) = %#v, want single target %q", targets, pyFile)
	}
	gotWin := failingTestSourceLine(dir, winTB, policy.Policy{})
	if !strings.HasPrefix(gotWin, "failing test line — pkg/worker.py:3:") || !strings.Contains(gotWin, "raise ValueError") {
		t.Fatalf("failingTestSourceLine(windows venv) = %q, want prefix failing test line — pkg/worker.py:3:", gotWin)
	}
}

func TestFileMetaMatchesRepoSubpath(t *testing.T) {
	formsMeta := index.SessionMeta{Touched: []string{"webapp/forms/models.py"}}
	authMeta := index.SessionMeta{Touched: []string{"webapp/auth/models.py"}}
	bareMeta := index.SessionMeta{Touched: []string{"models.py"}}

	if !fileMetaMatchesRepoSubpath(formsMeta, "/Users/alex/repo", "/Users/alex/repo/webapp/forms/models.py") {
		t.Fatal("expected exact subpath match to be kept")
	}
	if fileMetaMatchesRepoSubpath(authMeta, "/Users/alex/repo", "/Users/alex/repo/webapp/forms/models.py") {
		t.Fatal("expected sibling module auth/models.py to be filtered out for webapp/forms/models.py")
	}
	if !fileMetaMatchesRepoSubpath(bareMeta, "/Users/alex/repo", "/Users/alex/repo/webapp/forms/models.py") {
		t.Fatal("expected bare basename session without directory info to remain compatible")
	}
}

func TestSubpathScopingPreventsSiblingModuleDecisionAndCountLeak(t *testing.T) {
	tmp := hermeticEnv(t)
	indexDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", indexDir)

	repoDir := filepath.Join(tmp, "work")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	formsFile := filepath.Join(repoDir, "webapp", "forms", "models.py")
	authFile := filepath.Join(repoDir, "webapp", "auth", "models.py")
	billingFile := filepath.Join(repoDir, "webapp", "billing", "models.py")
	for _, f := range []string{formsFile, authFile, billingFile} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(formsFile, []byte("class ModelFormField:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authFile, []byte("class UserAuthAccount:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(billingFile, []byte("class BillingInvoice:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSess := func(id, file, dec string, day int) {
		ts := time.Date(2026, 9, day, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
		uJSON, err := json.Marshal(file)
		if err != nil {
			t.Fatal(err)
		}
		dJSON, err := json.Marshal(dec)
		if err != nil {
			t.Fatal(err)
		}
		cwdJSON, err := json.Marshal(repoDir)
		if err != nil {
			t.Fatal(err)
		}
		lines := []string{
			`{"type":"user","sessionId":"` + id + `","cwd":` + string(cwdJSON) + `,"timestamp":"` + ts + `","message":{"role":"user","content":"update module"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":` + string(cwdJSON) + `,"timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"u1","name":"Edit","input":{"file_path":` + string(uJSON) + `,"old_string":"a","new_string":"b"}},{"type":"text","text":` + string(dJSON) + `}]}}`,
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSess("s-forms-1", formsFile, "Decision: ModelFormField validation in models.py must preserve initial values.", 1)
	writeSess("s-forms-2", formsFile, "Updated ModelFormField error propagation in models.py.", 2)
	writeSess("s-auth-1", authFile, "Decision: UserAuthAccount in models.py hashes tokens on save.", 3)
	writeSess("s-auth-2", authFile, "Verified UserAuthAccount migration in models.py.", 4)
	writeSess("s-billing-1", billingFile, "Decision: BillingInvoice in models.py rounds totals per line item.", 5)
	if err := index.Ensure(indexDir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	self := func(index.SessionMeta, string) bool { return false }
	got := fileHookLineOutsideWithEdit(indexDir, repoDir, formsFile, "", self)
	if !strings.Contains(got, "2 sessions") || !strings.Contains(got, "ModelFormField") {
		t.Fatalf("expected 2-session subpath-scoped decision with ModelFormField, got %q", got)
	}
	for _, leaked := range []string{"UserAuthAccount", "BillingInvoice", "5 sessions"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("unexpected sibling-module data %q leaked into forms/models.py: %q", leaked, got)
		}
	}
	if billingGot := fileHookLineOutsideWithEdit(indexDir, repoDir, billingFile, "", self); billingGot != "" {
		t.Fatalf("expected single-session billing/models.py to stay below 2-session bar, got %q", billingGot)
	}
}
