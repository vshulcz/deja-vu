package index

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSelectorLadderResolverAndCounterAlwaysAgree enforces the domain invariant
// across all six rungs of the selector ladder in retrieval_identity.go (#853,
// #921, #1049, #1620, #2401, #4667): for every selector shape, FindByPrefixIn
// resolves a session if and only if PrefixMatchesIn returns > 0.
func TestSelectorLadderResolverAndCounterAlwaysAgree(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claudeDir := filepath.Join(tmp, "claude", "-tmp-w")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const fullUUID = "a1b2c3d4-0000-4000-8000-00000000e1f2"
	body := `{"type":"user","sessionId":"` + fullUUID + `","cwd":"/w","timestamp":"2026-09-01T09:00:00Z","message":{"role":"user","content":"fix the selector ladder drift"}}` + "\n"
	if err := os.WriteFile(filepath.Join(claudeDir, fullUUID+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	selectors := []struct {
		sel  string
		want bool
	}{
		{"", false},
		{"   ", false},
		{"nonexistent-selector", false},
		{fullUUID, true},                   // Rung 1: exact ID
		{"a1b2c3d4", true},                 // Rung 1: prefix ID
		{"a1b2c3d4…0000e1f2", true},        // Rung 4: elided ID
		{"claude:a1b2c3d4", true},          // Rung 5: composite harness:id (#921)
		{"claude:a1b2c3d4…0000e1f2", true}, // Rung 5: composite + elided
		{"codex:a1b2c3d4", false},          // Rung 5: wrong harness
		{"A1B2C3D4-0000", true},            // Rung 6: case-folded UUID (#1620)
	}

	for _, tc := range selectors {
		_, ok, err := FindByPrefix(dir, tc.sel)
		if err != nil {
			t.Fatalf("FindByPrefix(%q): %v", tc.sel, err)
		}
		n := PrefixMatches(dir, tc.sel)
		if ok != tc.want {
			t.Errorf("FindByPrefix(%q) ok = %v, want %v (PrefixMatches = %d)", tc.sel, ok, tc.want, n)
		}
		if (n > 0) != ok {
			t.Errorf("selector %q: FindByPrefix ok=%v disagrees with PrefixMatches=%d", tc.sel, ok, n)
		}
	}
}

// TestSessionTitleRankMonotonicity verifies the domain hierarchy in
// session_title.go: placeholder/plumbing (0) < tool output (1) < agent opening
// (2) < thin user greeting (3) < substantial user question (4).
func TestSessionTitleRankMonotonicity(t *testing.T) {
	r0Empty := titleRankOfRow("", false)
	r0Plumbing := titleRankOfRow(harnessOutputTitle("local stdout"), false)
	r1Tool := titleRankOfRow(toolOutputTitle("PASS ok"), false)
	r2Agent := titleRankOfRow("I inspected the repository and found two issues", true)
	r3Thin := titleRankOfRow("привет", false)
	r4Question := titleRankOfRow("почему падает тест на windows", false)

	if r0Empty != 0 || r0Plumbing != 0 {
		t.Fatalf("placeholder ranks = (%d, %d), want (0, 0)", r0Empty, r0Plumbing)
	}
	if r0Plumbing >= r1Tool || r1Tool >= r2Agent || r2Agent >= r3Thin || r3Thin >= r4Question {
		t.Fatalf("title rank hierarchy violated: plumbing=%d tool=%d agent=%d thin=%d question=%d",
			r0Plumbing, r1Tool, r2Agent, r3Thin, r4Question)
	}
}

// TestDDDStageIReadSideExclusionAndIgnoreInvariants enforces Stage I (Bugs #61-#70):
//  1. AST Invariant: Read-side manifest scanners in internal/index must use the
//     unified metaIgnoredOrExcluded / metaIgnoredOrExcludedLoad / excludedHarnesses
//     gate rather than raw pol.Ignored(...) alone or missing ignore/exclusion checks.
//  2. Behavioral Invariant: DEJA_EXCLUDE_HARNESSES hides excluded sessions from
//     EachAssistantTurnSince, KeptSince, AnyInScope, PromotedNoteMetas, and
//     relevantMetasCounts tie-breaking respects composite (Harness, ID).
func TestDDDStageIReadSideExclusionAndIgnoreInvariants(t *testing.T) {
	fset := token.NewFileSet()
	mustCallGate := map[string][]string{
		"assistant_turns.go":    {"EachAssistantTurnSince"},
		"year.go":               {"ScanYearWork"},
		"store_io.go":           {"SpanInventoryWhere"},
		"fixpair_store.go":      {"fixIgnoreGate"},
		"retrieval_postings.go": {"servableSids", "ignoredSids"},
		"friction.go":           {"PromotedNoteMetas"},
		"user_rules.go":         {"MarkUserRules"},
		"kept_since.go":         {"KeptSince"},
		"scoped.go":             {"AnyInScope"},
		"testruns.go":           {"ScanTestHistory"},
		"secrets.go":            {"ScanSecrets"},
	}

	for filename, wantFuncs := range mustCallGate {
		f, err := parser.ParseFile(fset, filename, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", filename, err)
		}
		foundFuncs := make(map[string]bool)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			for _, want := range wantFuncs {
				if fn.Name.Name != want {
					continue
				}
				foundFuncs[want] = true
				callsGate := false
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					if id, ok := call.Fun.(*ast.Ident); ok {
						switch id.Name {
						case "metaIgnoredOrExcluded", "metaIgnoredOrExcludedLoad", "excludedHarnesses":
							callsGate = true
						}
					}
					return true
				})
				if !callsGate {
					t.Errorf("%s: func %s must call metaIgnoredOrExcluded / metaIgnoredOrExcludedLoad / excludedHarnesses (Stage I Read-Side Gate invariant)",
						filename, want)
				}
			}
		}
		for _, want := range wantFuncs {
			if !foundFuncs[want] {
				t.Errorf("%s: expected function %s not found", filename, want)
			}
		}
	}

	// Behavioral check: two harnesses sharing session ID "shared-1".
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claudeDir := filepath.Join(tmp, "claude", "-work-payments")
	codexDir := filepath.Join(tmp, "codex", "sessions", "2026", "10", "12")
	for _, d := range []string{claudeDir, codexDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	now := time.Now().UTC().Truncate(time.Second)
	ts := now.Add(-10 * time.Minute).Format("2006-01-02T15:04:05Z")
	if err := os.WriteFile(filepath.Join(claudeDir, "shared-1.jsonl"), []byte(
		`{"type":"user","sessionId":"shared-1","cwd":"/work/payments","timestamp":"`+ts+`","message":{"role":"user","content":"fix retry backoff in payments"}}`+"\n"+
			`{"type":"assistant","sessionId":"shared-1","cwd":"/work/payments","timestamp":"`+ts+`","message":{"role":"assistant","content":[{"type":"text","text":"Updated retry backoff in client.go."}]}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "rollout-2026-10-12T10-00-00-shared-1.jsonl"), []byte(
		`{"timestamp":"`+ts+`","type":"session_meta","payload":{"session_id":"shared-1","id":"shared-1","cwd":"/work/payments"}}`+"\n"+
			`{"timestamp":"`+ts+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"fix retry backoff in payments"}]}}`+"\n"+
			`{"timestamp":"`+ts+`","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Changed codex retry logic."}]}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	t.Setenv("DEJA_EXCLUDE_HARNESSES", "codex")
	var turns []string
	if err := EachAssistantTurnSince(dir, now.Add(-1*time.Hour), nil, func(meta SessionMeta, text string, _ time.Time) {
		turns = append(turns, meta.Harness+":"+text)
	}); err != nil {
		t.Fatalf("EachAssistantTurnSince: %v", err)
	}
	if len(turns) != 1 || !strings.HasPrefix(turns[0], "claude:") {
		t.Fatalf("Bug #61 regression: EachAssistantTurnSince with codex excluded = %v, want only claude", turns)
	}

	kept, err := KeptSince(dir, now.Add(-1*time.Hour), now.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("KeptSince: %v", err)
	}
	for _, k := range kept {
		if k.Harness == "codex" {
			t.Fatalf("Bug #70 regression: KeptSince leaked excluded codex session: %+v", k)
		}
	}

	// Stage N (Bug #86): Unscoped FindByPrefix, FindByID, and PrefixHarnesses must
	// skip excluded harnesses rather than letting an excluded harness shadow an
	// active one.
	if hs := PrefixHarnesses(dir, "shared-1"); len(hs) != 1 || hs[0] != "claude" {
		t.Fatalf("Bug #86 regression: PrefixHarnesses with codex excluded = %v, want [claude]", hs)
	}
	if s, ok, err := FindByPrefix(dir, "shared-1"); err != nil || !ok || s.Harness != "claude" {
		t.Fatalf("Bug #86 regression: FindByPrefix with codex excluded = (%s, %v, %v), want claude", s.Harness, ok, err)
	}
	if s, ok, err := FindByID(dir, "shared-1"); err != nil || !ok || s.Harness != "claude" {
		t.Fatalf("Bug #86 regression: FindByID with codex excluded = (%s, %v, %v), want claude", s.Harness, ok, err)
	}
}
