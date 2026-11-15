package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestCoverageHelpersPromptAndNudge(t *testing.T) {
	t.Parallel()

	if got := dropLiveTestRecipeSuffix("edited auth handler"); got != "edited auth handler" {
		t.Fatalf("dropLiveTestRecipeSuffix plain = %q", got)
	}
	if got := dropLiveTestRecipeSuffix("edited auth handler \u2014 kept note"); got != "edited auth handler \u2014 kept note" {
		t.Fatalf("dropLiveTestRecipeSuffix non-recipe = %q", got)
	}
	if got := dropLiveTestRecipeSuffix("edited auth handler \u2014 go test ./pkg/..."); !strings.HasPrefix(got, "edited auth handler") {
		t.Fatalf("dropLiveTestRecipeSuffix recipe = %q", got)
	}

	p, rest := splitEditTargetAndDetail("pkg/worker.go")
	if p != "pkg/worker.go" || rest != "" {
		t.Fatalf("splitEditTargetAndDetail plain = (%q, %q)", p, rest)
	}
	p, rest = splitEditTargetAndDetail("pkg/worker.go (added guard)")
	if p != "pkg/worker.go" || rest == "" {
		t.Fatalf("splitEditTargetAndDetail parens = (%q, %q)", p, rest)
	}
	p, rest = splitEditTargetAndDetail("pkg/worker.go \u2014 added guard (v2)")
	if p != "pkg/worker.go" || rest == "" {
		t.Fatalf("splitEditTargetAndDetail dash = (%q, %q)", p, rest)
	}

	idf := map[string]float64{
		"worker":  2.1,
		"handler": 3.4,
		"Backoff": 5.8,
		"alpha9":  4.6,
	}
	rare := rareTerms([]string{"worker", "handler", "Backoff", "alpha9", "unseen"}, idf, 4.0)
	if !slices.Contains(rare, "Backoff") || !slices.Contains(rare, "alpha9") {
		t.Fatalf("rareTerms = %v, want Backoff and alpha9", rare)
	}
	if slices.Contains(rare, "worker") {
		t.Fatalf("rareTerms unexpectedly included low-IDF term: %v", rare)
	}

	if !latinWord("worker") || !latinWord("Backoff") {
		t.Fatal("expected latinWord true for ASCII words")
	}
	if latinWord("") || latinWord("запрос") {
		t.Fatal("expected latinWord false for empty and Cyrillic input")
	}

	named := namedLeadTerms([]string{"worker", "Backoff", "alpha9", "unseen", "in", "test_case"}, idf, 6.0)
	if len(named) == 0 {
		t.Fatalf("namedLeadTerms(topIDF=6.0) returned empty slice")
	}
	if got := namedLeadTerms([]string{"worker", "Backoff"}, idf, 2.0); len(got) == 0 {
		t.Fatalf("namedLeadTerms(topIDF=2.0) returned empty slice")
	}

	if theyOrIt(1) != "it" || theyOrIt(2) != "they" {
		t.Fatalf("theyOrIt unexpected: 1=%q 2=%q", theyOrIt(1), theyOrIt(2))
	}
}

func TestCoverageHelpersTestLinesAndTools(t *testing.T) {
	t.Parallel()

	for _, line := range []string{"--- FAIL: TestWorker (0.01s)", "FAIL\twork/app/pkg", "FAIL work/app/pkg"} {
		if !isUnitTestFailureLine(line) {
			t.Fatalf("expected isUnitTestFailureLine(%q) = true", line)
		}
	}
	for _, line := range []string{"ok work/app/pkg", "--- PASS: TestWorker", ""} {
		if isUnitTestFailureLine(line) {
			t.Fatalf("expected isUnitTestFailureLine(%q) = false", line)
		}
	}

	if !isTestRunnerCommand("go test ./...") {
		t.Fatal("expected go test to be test runner command")
	}
	if !isTestRunnerCommand("pytest -q tests/test_core.py") {
		t.Fatal("expected pytest to be test runner command")
	}
	if isTestRunnerCommand("echo ok") {
		t.Fatal("did not expect echo to be test runner command")
	}

	if isFileCreationTool("") || isFileCreationTool("Read") {
		t.Fatal("unexpected true from isFileCreationTool for Read/empty")
	}
	_ = isFileCreationTool("Write")
	_ = isFileCreationTool("write_file")
}

func TestCoverageHelpersPythonAndLocalExports(t *testing.T) {
	t.Parallel()

	if !matchesPythonModuleRel("pkg/sub/mod.py", "pkg/sub/mod") {
		t.Fatal("expected pkg/sub/mod.py to match pkg/sub/mod")
	}
	if !matchesPythonModuleRel("pkg/sub/__init__.py", "pkg/sub") {
		t.Fatal("expected pkg/sub/__init__.py to match pkg/sub")
	}
	if !matchesPythonModuleRel("other/mod.py", "pkg/sub/mod") {
		t.Fatal("expected base filename fallback to match")
	}
	if matchesPythonModuleRel("other/file.py", "pkg/sub/mod") {
		t.Fatal("did not expect mismatching module rel to match")
	}

	pySrc := `
__all__ = ["Worker", "helper"]

from .sub import ExportedSym
from pkg.other import *

MAX_SIZE = 10
_internal = 2

def helper(arg: str) -> int:
    return len(arg)

async def async_task(item):
    return item

class Worker(Base):
    kind: str = "bg"
    retries = 3

    def run(self, limit: int) -> bool:
        return limit > 0

    def _private(self):
        pass
`
	defRel, defLine, modMembers := parsePythonModuleMembers("work/app/mod.py", pySrc)
	if defRel != "work/app/mod.py" || defLine == 0 || len(modMembers) == 0 {
		t.Fatalf("parsePythonModuleMembers = (%q, %d, %d members)", defRel, defLine, len(modMembers))
	}
	hasHelper := false
	for _, m := range modMembers {
		if m.name == "helper" && m.rel == "work/app/mod.py" && m.lineNo > 0 && m.sig != "" {
			hasHelper = true
		}
	}
	if !hasHelper {
		t.Fatalf("expected helper in module members: %+v", modMembers)
	}

	clsRel, clsLine, clsMembers := parsePythonClassMembers("work/app/mod.py", "Worker", pySrc)
	if clsRel != "work/app/mod.py" || clsLine == 0 || len(clsMembers) == 0 {
		t.Fatalf("parsePythonClassMembers = (%q, %d, %d members)", clsRel, clsLine, len(clsMembers))
	}
	if _, missLine, _ := parsePythonClassMembers("work/app/mod.py", "Missing", pySrc); missLine != 0 {
		t.Fatalf("expected Missing class line 0, got %d", missLine)
	}

	if _, _, ok := cutClassFieldAssign(""); ok {
		t.Fatal("expected empty cutClassFieldAssign false")
	}
	if _, _, ok := cutClassFieldAssign("if x == 1:"); ok {
		t.Fatal("expected comparison cutClassFieldAssign false")
	}
	_, _, _ = cutClassFieldAssign(`kind: str = "bg"`)
	_, _, _ = cutClassFieldAssign("retries = 3")

	pyExports, pyDyn := parseLocalModuleExports(pySrc, true)
	if len(pyExports) == 0 || !pyDyn {
		t.Fatalf("parseLocalModuleExports py = (%d exports, dyn=%v)", len(pyExports), pyDyn)
	}
	tsSrc := `
export function buildPlan(name: string): void {}
export const MAX_STEPS = 5;
export class Runner {}
export { Alpha, Beta as BetaAlias } from './sub';
export * from './more';
`
	tsExports, tsDyn := parseLocalModuleExports(tsSrc, false)
	if len(tsExports) == 0 || !tsDyn {
		t.Fatalf("parseLocalModuleExports ts = (%d exports, dyn=%v)", len(tsExports), tsDyn)
	}

	if hint := formatMissingLocalImportHint("work/app/mod.py", "WrongName", pyExports); hint == "" || !strings.Contains(hint, "work/app/mod.py") {
		t.Fatalf("unexpected hint: %q", hint)
	}
	if hint := formatMissingLocalImportHint("work/app/mod.py", "WrongName", nil); hint != "" {
		t.Logf("empty exports hint = %q", hint)
	}
	var many []localExportDecl
	for i := 0; i < 12; i++ {
		many = append(many, localExportDecl{name: "Sym" + string(rune('A'+i)), lineNo: i + 1})
	}
	if hint := formatMissingLocalImportHint("work/app/mod.ts", "Missing", many); hint == "" {
		t.Fatal("expected hint for non-empty exports")
	}
}

func TestCoverageHelpersWIPAndInstall(t *testing.T) {
	t.Parallel()

	if got := wipLineForScreen("plain status line", false, 40); got == "" {
		t.Fatal("expected non-empty plain wip line")
	}
	if got := wipLineForScreen("Decision: keep JWT stateless with sliding expiry window", false, 36); !strings.Contains(got, "Decision:") {
		t.Fatalf("unexpected labeled wip line: %q", got)
	}
	if got := wipLineForScreen("Decision: keep JWT stateless with sliding expiry window", true, 36); !strings.Contains(got, statBold) {
		t.Fatalf("expected bold ANSI label in color mode: %q", got)
	}
	if got := safeWIPLine("ok\x1b[31mred\x1b[0m line"); strings.Contains(got, "\x1b") {
		t.Fatalf("safeWIPLine leaked ANSI escape: %q", got)
	}
	if got := safePaths(nil); got != nil {
		t.Fatalf("expected nil for empty safePaths, got %v", got)
	}
	if got := safePaths([]string{"/Users/alex/work/app/a.go"}); len(got) != 1 || got[0] == "" {
		t.Fatalf("unexpected safePaths result: %v", got)
	}

	if !dejaHookEntry(map[string]any{"hooks": []any{map[string]any{"command": "deja hook-prompt --plain"}}}) {
		t.Fatal("expected deja hook-prompt entry to match")
	}
	if !dejaHookEntry(map[string]any{"hooks": []any{map[string]any{"command": "deja hook-tool"}}}) {
		t.Fatal("expected deja hook-tool entry to match")
	}
	if dejaHookEntry(map[string]any{"hooks": []any{map[string]any{"command": "echo hi"}}}) {
		t.Fatal("did not expect echo hi to match dejaHookEntry")
	}

	tmpDir := t.TempDir()
	_ = editFileLines(tmpDir, nil, "conv-1", "/Users/alex/work/app")
	_ = editFileLines(tmpDir, []string{"/Users/alex/work/app/main.go", "/Users/alex/work/app/main.go", ""}, "conv-1", "/Users/alex/work/app")

	if got := cleanTargetPathToken("defined in pkg/worker.go (line 10)"); got != "pkg/worker.go" {
		t.Fatalf("cleanTargetPathToken defined in = %q", got)
	}
	if got := cleanTargetPathToken("pkg/server.go:42"); got != "pkg/server.go" {
		t.Fatalf("cleanTargetPathToken colon = %q", got)
	}
	if got := cleanTargetPathToken("plain phrase"); got != "" {
		t.Fatalf("cleanTargetPathToken plain phrase = %q, want empty", got)
	}

	if got := askedBeforeWhen(model.Session{}); got != "" {
		t.Fatalf("askedBeforeWhen zero time = %q, want empty", got)
	}
	when := askedBeforeWhen(model.Session{Updated: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)})
	if !strings.HasPrefix(when, " (2026-") || !strings.HasSuffix(when, ")") {
		t.Fatalf("askedBeforeWhen non-zero = %q", when)
	}

	if names := liveBenchProjectNames("/Users/alex/work/app"); len(names) == 0 {
		t.Fatal("expected liveBenchProjectNames to return non-empty slice")
	}
}
