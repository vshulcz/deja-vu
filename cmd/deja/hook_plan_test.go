package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestExtractPlanSteps(t *testing.T) {
	tests := []struct {
		name string
		plan string
		want []string
	}{
		{
			name: "numbered",
			plan: "## Plan\n1. Update gateway_timeout handling\n2) Test reconnect_loop recovery",
			want: []string{"Update gateway_timeout handling", "Test reconnect_loop recovery"},
		},
		{
			name: "bulleted",
			plan: "- Inspect exporter_batch\n* Repair utc_midnight rollover\n+ Add regression coverage",
			want: []string{"Inspect exporter_batch", "Repair utc_midnight rollover", "Add regression coverage"},
		},
		{
			name: "mixed",
			plan: "Plan\n1. Inspect auth_cache\n- [ ] Update token_rotation\n2) Test Windows paths",
			want: []string{"Inspect auth_cache", "Update token_rotation", "Test Windows paths"},
		},
		{
			name: "fallback",
			plan: "## Inspect gateway_timeout\nRepair reconnect_loop",
			want: []string{"Inspect gateway_timeout", "Repair reconnect_loop"},
		},
		{
			name: "empty",
			plan: "\r\n  \n",
			want: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := extractPlanSteps(test.plan); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("steps = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestPlanSearchStepsCapsTermsAndSteps(t *testing.T) {
	plan := strings.Join([]string{
		`1. Inspect C:\work\repo\auth.go gateway_timeout reconnect_loop token_rotation extra_identifier`,
		"2. Update exporter_batch utc_midnight rollover_guard",
		"3. Test websocket_reconnect heartbeat_timeout retry_budget",
		"4. Document deployment_pipeline release_checklist",
	}, "\n")
	got := planSearchSteps(plan)
	if len(got) != planStepLimit {
		t.Fatalf("steps = %d, want %d: %v", len(got), planStepLimit, got)
	}
	for i, terms := range got {
		if len(terms) < 2 || len(terms) > planTermLimit {
			t.Fatalf("step %d terms = %v", i, terms)
		}
	}
	// A Windows path is ordinary text to the tokenizer: backslash splits it,
	// and only the basename survives as a term. Path-aware matching is out of
	// scope here; this pins the actual behavior so nobody reads more into the
	// fixture above.
	joined := strings.Join(got[0], " ")
	if !strings.Contains(joined, "auth.go") || strings.Contains(joined, `\`) {
		t.Fatalf("step 0 terms = %v, want the path reduced to its basename token", got[0])
	}
}

func TestPlanWeakInputStopsBeforeTheIndex(t *testing.T) {
	oldReady := planIndexReady
	oldLookup := planFrictionLookup
	t.Cleanup(func() {
		planIndexReady = oldReady
		planFrictionLookup = oldLookup
	})

	readyCalls := 0
	lookupCalls := 0
	planIndexReady = func(string) bool {
		readyCalls++
		return true
	}
	planFrictionLookup = func(string, [][]string, func(index.SessionMeta) bool, int) []index.PlanCooccurrence {
		lookupCalls++
		return samplePlanMatches("local-project")
	}

	// "retry" alone is a real tech term but neither long enough nor
	// symbol-shaped to count as an identifier, so it still needs a second
	// term it doesn't have.
	for _, plan := range []string{"", "- ok", "1. retry"} {
		if got := planFindings(filepath.Join(t.TempDir(), "index.db"), plan, "session"); len(got) != 0 {
			t.Fatalf("plan %q produced %v", plan, got)
		}
	}
	if readyCalls != 0 || lookupCalls != 0 {
		t.Fatalf("weak plans touched the index: ready=%d lookup=%d", readyCalls, lookupCalls)
	}
}

// TestPlanSearchStepsIdentifierSingleTermAdmitted covers the same rule
// hook-prompt already applies to auto-recall (hasIdentifierTerm,
// cmd/deja/hook_prompt.go:319): a step naming something identifier-shaped is
// specific enough on one word, so it should not need a second term to be
// searched.
func TestPlanSearchStepsIdentifierSingleTermAdmitted(t *testing.T) {
	got := planSearchSteps("1. auth.go")
	if len(got) != 1 {
		t.Fatalf("steps = %v, want one admitted step", got)
	}
	if len(got[0]) != 1 || got[0][0] != "auth.go" {
		t.Fatalf("step terms = %v, want [\"auth.go\"]", got[0])
	}
}

// TestPlanSearchStepsNonIdentifierSingleTermDropped is the negative half of
// the case above: an ordinary single word carries no more signal here than
// it does for auto-recall, so the step is dropped rather than searched.
func TestPlanSearchStepsNonIdentifierSingleTermDropped(t *testing.T) {
	if got := planSearchSteps("1. Retry"); len(got) != 0 {
		t.Fatalf("steps = %v, want none admitted", got)
	}
}

func TestPlanPolicyDenyProducesNoOutput(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"activations":{"auto":{"local":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyPath)
	withPlanLookup(t, samplePlanMatches(`C:\work\private-repo`))

	got := planFindings(
		filepath.Join(t.TempDir(), "index.db"),
		"1. Run timeout around deploy_probe",
		"agent-session",
	)
	if len(got) != 0 {
		t.Fatalf("denied sessions produced findings: %v", got)
	}
}

func TestPlanFindingClaimIsNeutral(t *testing.T) {
	match := samplePlanMatches("local-project")[0]
	match.Wall.Text = "build conflicts with the cache and contradicts the manifest"
	match.Command = "echo already tried and was abandoned"

	got := strings.ToLower(formatPlanFinding(match))
	for _, forbidden := range []string{
		"conflicts with",
		"contradicts",
		"already tried",
		"was abandoned",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("finding contains forbidden claim %q: %q", forbidden, got)
		}
	}
	if !strings.HasPrefix(got, "wall hit in 3 sessions since ") {
		t.Fatalf("finding is not a neutral factual report: %q", got)
	}
	if strings.Contains(got, " should ") || strings.Contains(got, " must ") {
		t.Fatalf("finding commands the reader: %q", got)
	}
}

// TestPlanFindingClaimIsNeutralWallOnly is the wall-only counterpart to
// TestPlanFindingClaimIsNeutral: the forbidden-vocabulary and non-imperative
// checks apply to the wall clause on its own, since v1.5 lets a finding
// stand without a command clause at all.
func TestPlanFindingClaimIsNeutralWallOnly(t *testing.T) {
	match := samplePlanMatches("local-project")[0]
	match.Wall.Text = "build conflicts with the cache and contradicts the manifest"
	match.Command = ""
	match.CommandSessions = nil

	got := strings.ToLower(formatPlanFinding(match))
	for _, forbidden := range []string{
		"conflicts with",
		"contradicts",
		"already tried",
		"was abandoned",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("finding contains forbidden claim %q: %q", forbidden, got)
		}
	}
	if !strings.HasPrefix(got, "wall hit in 3 sessions since ") {
		t.Fatalf("finding is not a neutral factual report: %q", got)
	}
	if strings.Contains(got, "also ran") {
		t.Fatalf("wall-only finding unexpectedly carries a command clause: %q", got)
	}
	if strings.Contains(got, " should ") || strings.Contains(got, " must ") {
		t.Fatalf("finding commands the reader: %q", got)
	}
}

func TestPlanHookBudgetIncludesRecallFrame(t *testing.T) {
	huge := strings.Repeat("oversized_gateway_failure_ﾎｩ ", 400)
	findings := []planFinding{
		{line: "wall hit in 3 sessions: " + huge},
		{line: "wall hit in 4 sessions: " + huge},
	}
	budgeted := budgetPlanFindings(findings, planHookBudget-recallFrameOverhead)
	if len(budgeted) == 0 {
		t.Fatal("oversized finding was dropped rather than truncated")
	}
	var lines []string
	for _, finding := range budgeted {
		lines = append(lines, finding.line)
	}
	payload := strings.Join(lines, "\n")
	framed := frameRecall(payload)
	if len(payload) > planHookBudget-recallFrameOverhead {
		t.Fatalf("payload = %d bytes, budget = %d", len(payload), planHookBudget-recallFrameOverhead)
	}
	if len(framed) > planHookBudget {
		t.Fatalf("framed payload = %d bytes, budget = %d", len(framed), planHookBudget)
	}
	if !strings.Contains(payload, "…") {
		t.Fatalf("oversized finding was not visibly truncated: %q", payload)
	}
}

func TestPlanHookSeenDedupeSuppressesSecondInvocation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	withPlanLookup(t, samplePlanMatches("local-project"))

	first := planFindings(dir, "1. Run timeout around deploy_probe", "agent-1")
	if len(first) != 1 {
		t.Fatalf("first findings = %v", first)
	}
	second := planFindings(dir, "1. Run timeout around deploy_probe", "agent-1")
	if len(second) != 0 {
		t.Fatalf("second invocation repeated findings: %v", second)
	}
	seen := alreadyInjected(dir, "agent-1")
	for _, id := range []string{"wall-a", "wall-b", "wall-c"} {
		if !seen[id] {
			t.Errorf("%s was not recorded in .hookseen: %v", id, seen)
		}
	}
}

func TestPlanHookEmitsEachWallOnce(t *testing.T) {
	match := samplePlanMatches("local-project")[0]
	withPlanLookup(t, []index.PlanCooccurrence{match, match})

	got := planFindings(
		filepath.Join(t.TempDir(), "index.db"),
		"1. Run timeout around deploy_probe",
		"",
	)
	if len(got) != 1 {
		t.Fatalf("duplicate wall emitted %d times: %v", len(got), got)
	}
}

func TestHookPlanJSONContract(t *testing.T) {
	t.Run("hit with unknown fields and Windows paths", func(t *testing.T) {
		withPlanLookup(t, samplePlanMatches(`C:\work\repo`))
		t.Setenv("CLAUDE_PROJECT_DIR", "")

		input := `{
			"hook_event_name":"PreToolUse",
			"tool_name":"ExitPlanMode",
			"tool_input":{
				"plan":"1. Run timeout around deploy_probe",
				"planFilePath":"C:\\work\\repo\\PLAN.md",
				"future_field":true
			},
			"session_id":"",
			"cwd":"C:\\work\\repo",
			"unknown_top_level":{"value":1}
		}`
		var out bytes.Buffer
		if err := runHookPlan(filepath.Join(t.TempDir(), "index.db"), strings.NewReader(input), &out); err != nil {
			t.Fatal(err)
		}
		if out.Len() == 0 {
			t.Fatal("matching hook produced no output")
		}
		if strings.Contains(out.String(), "permissionDecision") {
			t.Fatalf("security-sensitive field appeared in output: %s", out.String())
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
			t.Fatalf("bad JSON %q: %v", out.String(), err)
		}
		if len(raw) != 1 {
			t.Fatalf("top-level shape = %v", raw)
		}
		var envelope struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		}
		if err := json.Unmarshal(raw["hookSpecificOutput"], &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.HookEventName != "PreToolUse" {
			t.Fatalf("event = %q", envelope.HookEventName)
		}
		if !strings.HasPrefix(envelope.AdditionalContext, recallFrameHeader) ||
			!strings.HasSuffix(envelope.AdditionalContext, recallFrameFooter) {
			t.Fatalf("context is not framed: %q", envelope.AdditionalContext)
		}
		if len(envelope.AdditionalContext) > planHookBudget {
			t.Fatalf("context = %d bytes, budget = %d", len(envelope.AdditionalContext), planHookBudget)
		}
	})

	t.Run("miss", func(t *testing.T) {
		withPlanLookup(t, nil)
		var out bytes.Buffer
		err := runHookPlan(
			filepath.Join(t.TempDir(), "index.db"),
			strings.NewReader(`{"tool_name":"ExitPlanMode","tool_input":{"plan":"1. Run timeout around deploy_probe"}}`),
			&out,
		)
		if err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 {
			t.Fatalf("miss produced %q", out.String())
		}
	})

	t.Run("other tool", func(t *testing.T) {
		withPlanLookup(t, samplePlanMatches("local-project"))
		var out bytes.Buffer
		err := runHookPlan(
			filepath.Join(t.TempDir(), "index.db"),
			strings.NewReader(`{"tool_name":"Bash","tool_input":{"plan":"1. Run timeout around deploy_probe"}}`),
			&out,
		)
		if err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 {
			t.Fatalf("other tool produced %q", out.String())
		}
	})

	t.Run("missing tool input", func(t *testing.T) {
		withPlanLookup(t, samplePlanMatches("local-project"))
		var out bytes.Buffer
		if err := runHookPlan(
			filepath.Join(t.TempDir(), "index.db"),
			strings.NewReader(`{"tool_name":"ExitPlanMode","future_field":"accepted"}`),
			&out,
		); err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 {
			t.Fatalf("missing tool_input produced %q", out.String())
		}
	})

	t.Run("missing tool name", func(t *testing.T) {
		withPlanLookup(t, samplePlanMatches("local-project"))
		var out bytes.Buffer
		if err := runHookPlan(
			filepath.Join(t.TempDir(), "index.db"),
			strings.NewReader(`{"tool_input":{"plan":"1. Run timeout around deploy_probe"}}`),
			&out,
		); err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 {
			t.Fatalf("input without tool_name produced %q", out.String())
		}
	})
}

func TestPlanFrictionIndexJoin(t *testing.T) {
	withStatsStores(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "missing-policy.json"))

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("plan-wall-%d", i)
		toolID := fmt.Sprintf("tool-plan-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"run the deploy probe"}}`,
				id, old,
			),
			fmt.Sprintf(
				`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":"go run ./deploy_probe --timeout 5 --config C:\\work\\repo\\PLAN.md"}}]}}`,
				id, old, toolID,
			),
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":true,"content":"/bin/sh: timeout: command not found"}]}}`,
				id, old, toolID,
			),
		})
	}

	// The recurring wall appears in three sessions. Additional unrelated
	// sessions make its terms clear the same IDF floor used by auto recall.
	for i := 0; i < 27; i++ {
		id := fmt.Sprintf("plan-noise-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "noise", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"unrelated color palette discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	steps := planSearchSteps("1. Run timeout around deploy_probe")
	matches := index.PlanFrictionMatches(
		index.DefaultDir(),
		steps,
		func(index.SessionMeta) bool { return true },
		4,
	)
	if len(matches) == 0 {
		t.Fatal("manifest wall and command records did not join")
	}
	got := matches[0]
	if len(got.Wall.Sessions) < index.FrictionMinSessions {
		t.Fatalf("wall sessions = %d", len(got.Wall.Sessions))
	}
	if !strings.Contains(strings.ToLower(got.Wall.Text), "timeout") {
		t.Fatalf("wall = %q", got.Wall.Text)
	}
	if !strings.Contains(got.Command, "deploy_probe") {
		t.Fatalf("command = %q", got.Command)
	}
}

// TestPlanFrictionIndexJoinSingleIdentifierTerm covers the join end to end
// for a plan step that names nothing but a single identifier. The command
// record here shares only that one term with the step (no second word like
// "timeout" to fall back on), so the join succeeds only because
// planCommandForStep's threshold drops to one for an identifier step.
func TestPlanFrictionIndexJoinSingleIdentifierTerm(t *testing.T) {
	withStatsStores(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "missing-policy.json"))

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("plan-single-wall-%d", i)
		toolID := fmt.Sprintf("tool-plan-single-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-single", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"run the deploy probe"}}`,
				id, old,
			),
			fmt.Sprintf(
				`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":"go run ./deploy_probe --config C:\\work\\repo\\PLAN.md"}}]}}`,
				id, old, toolID,
			),
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":true,"content":"/bin/sh: deploy_probe: command not found"}]}}`,
				id, old, toolID,
			),
		})
	}

	// Same noise volume as TestPlanFrictionIndexJoin: deploy_probe appears in
	// exactly the three wall sessions, and this many unrelated sessions is
	// what clears the IDF floor at that document count.
	for i := 0; i < 27; i++ {
		id := fmt.Sprintf("plan-single-noise-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "noise-single", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"unrelated color palette discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	steps := planSearchSteps("1. deploy_probe")
	if len(steps) != 1 || len(steps[0]) != 1 {
		t.Fatalf("steps = %v, want a single single-term step", steps)
	}
	matches := index.PlanFrictionMatches(
		index.DefaultDir(),
		steps,
		func(index.SessionMeta) bool { return true },
		4,
	)
	if len(matches) == 0 {
		t.Fatal("single-identifier-term step did not join to a command")
	}
	got := matches[0]
	if len(got.Wall.Sessions) < index.FrictionMinSessions {
		t.Fatalf("wall sessions = %d", len(got.Wall.Sessions))
	}
	if !strings.Contains(got.Command, "deploy_probe") {
		t.Fatalf("command = %q", got.Command)
	}
}

// TestPlanFrictionIndexJoinWallOnly is the v1.5 case: a census across real
// walls found a matching command logged alongside the wall in only 1 of 19
// carriers, so requiring one would reject nearly every recurrence. Here the
// wall text itself names the step's term but no carrier's command does —
// the join must still succeed, with Command left empty.
func TestPlanFrictionIndexJoinWallOnly(t *testing.T) {
	withStatsStores(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "missing-policy.json"))

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("plan-wallonly-%d", i)
		toolID := fmt.Sprintf("tool-plan-wallonly-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-wallonly", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"run the gateway again"}}`,
				id, old,
			),
			// A command is logged, but it shares no term with the plan step —
			// this is the realistic majority shape, not the 1-in-19 case
			// where a command happens to name the wall too.
			fmt.Sprintf(
				`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":"echo done"}}]}}`,
				id, old, toolID,
			),
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":true,"content":"/bin/sh: gateway_timeout: command not found"}]}}`,
				id, old, toolID,
			),
		})
	}

	// Same noise volume as the other join tests: clears the IDF floor for
	// gateway_timeout at this document count.
	for i := 0; i < 27; i++ {
		id := fmt.Sprintf("plan-wallonly-noise-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "noise-wallonly", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"unrelated color palette discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	steps := planSearchSteps("1. gateway_timeout")
	if len(steps) != 1 || len(steps[0]) != 1 {
		t.Fatalf("steps = %v, want a single single-term step", steps)
	}
	matches := index.PlanFrictionMatches(
		index.DefaultDir(),
		steps,
		func(index.SessionMeta) bool { return true },
		4,
	)
	if len(matches) == 0 {
		t.Fatal("wall-only recurrence did not produce a finding")
	}
	got := matches[0]
	if len(got.Wall.Sessions) < index.FrictionMinSessions {
		t.Fatalf("wall sessions = %d", len(got.Wall.Sessions))
	}
	if !strings.Contains(strings.ToLower(got.Wall.Text), "gateway_timeout") {
		t.Fatalf("wall = %q", got.Wall.Text)
	}
	if got.Command != "" || len(got.CommandSessions) != 0 {
		t.Fatalf("expected no command strengthening, got command=%q sessions=%v", got.Command, got.CommandSessions)
	}

	line := formatPlanFinding(got)
	if line == "" {
		t.Fatal("wall-only match produced no finding line")
	}
	if strings.Contains(line, "also ran") {
		t.Fatalf("wall-only line unexpectedly carries a command clause: %q", line)
	}
	if !strings.Contains(line, "gateway_timeout") {
		t.Fatalf("finding does not name the wall: %q", line)
	}
}

// TestPlanFrictionIndexJoinIdentifierMidFloorFires is the v1.7 case: a
// two-tier floor, not a blanket exemption. A re-measurement against 2,886
// real sessions found identifier-shaped false-positive anchors (cannot,
// directory, branch, runner) topping out at idf 0.70 — the v1.6 full
// exemption let those through — while true anchors (hermes 1.09,
// hermes-agent 1.21) started at 1.09. Here "pipeline_probe" appears in 7 of
// 30 sessions: idf = ln(31/8) ≈ 1.35, above planIdentifierIDFFloor (1.0) but
// below the full dejaVuIDFFloor (2.0) a non-identifier term would need — the
// join fires only because the term is identifier-shaped and clears the
// lower bar.
func TestPlanFrictionIndexJoinIdentifierMidFloorFires(t *testing.T) {
	withStatsStores(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "missing-policy.json"))

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("plan-anchor-wall-%d", i)
		toolID := fmt.Sprintf("tool-plan-anchor-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-anchor", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"run pipeline_probe again"}}`,
				id, old,
			),
			fmt.Sprintf(
				`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":"go run ./pipeline_probe --config C:\\work\\repo\\PLAN.md"}}]}}`,
				id, old, toolID,
			),
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":true,"content":"/bin/sh: pipeline_probe: command not found"}]}}`,
				id, old, toolID,
			),
		})
	}

	// 4 more sessions repeat the same word so it clears 7 of 30 total —
	// idf ≈ 1.35, inside the [1.0, 2.0) band only an identifier gets.
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("plan-anchor-common-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-anchor-common", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"pipeline_probe discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	// The remaining 23 sessions carry neither term, bringing the corpus to
	// 30 and the document count to the 31 the idf figure above assumes.
	for i := 0; i < 23; i++ {
		id := fmt.Sprintf("plan-anchor-noise-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-anchor-noise", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"unrelated color palette discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	steps := planSearchSteps("1. pipeline_probe")
	if len(steps) != 1 || len(steps[0]) != 1 {
		t.Fatalf("steps = %v, want a single single-term step", steps)
	}
	matches := index.PlanFrictionMatches(
		index.DefaultDir(),
		steps,
		func(index.SessionMeta) bool { return true },
		4,
	)
	if len(matches) == 0 {
		t.Fatal("identifier term inside the [1.0, 2.0) band did not join")
	}
	got := matches[0]
	if len(got.Wall.Sessions) < index.FrictionMinSessions {
		t.Fatalf("wall sessions = %d", len(got.Wall.Sessions))
	}
	if !strings.Contains(strings.ToLower(got.Wall.Text), "pipeline_probe") {
		t.Fatalf("wall = %q", got.Wall.Text)
	}
}

// TestPlanFrictionIndexJoinIdentifierBelowFloorSilent is the negative
// control for the case above: the same identifier-shaped word, spread
// common enough to drop its idf below planIdentifierIDFFloor (1.0), does not
// join even though it is identifier-shaped — v1.7 narrows the exemption to a
// lower bar, it does not remove the bar.
func TestPlanFrictionIndexJoinIdentifierBelowFloorSilent(t *testing.T) {
	withStatsStores(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "missing-policy.json"))

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("plan-anchor-lo-wall-%d", i)
		toolID := fmt.Sprintf("tool-plan-anchor-lo-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-anchor-lo", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"run pipeline_probe again"}}`,
				id, old,
			),
			fmt.Sprintf(
				`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":"go run ./pipeline_probe --config C:\\work\\repo\\PLAN.md"}}]}}`,
				id, old, toolID,
			),
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":true,"content":"/bin/sh: pipeline_probe: command not found"}]}}`,
				id, old, toolID,
			),
		})
	}

	// 17 more sessions repeat the same word so it clears 20 of 30 total —
	// idf = ln(31/21) ≈ 0.39, below planIdentifierIDFFloor (1.0).
	for i := 0; i < 17; i++ {
		id := fmt.Sprintf("plan-anchor-lo-common-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-anchor-lo-common", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"pipeline_probe discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("plan-anchor-lo-noise-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-anchor-lo-noise", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"unrelated color palette discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	steps := planSearchSteps("1. pipeline_probe")
	if len(steps) != 1 || len(steps[0]) != 1 {
		t.Fatalf("steps = %v, want a single single-term step", steps)
	}
	matches := index.PlanFrictionMatches(
		index.DefaultDir(),
		steps,
		func(index.SessionMeta) bool { return true },
		4,
	)
	if len(matches) != 0 {
		t.Fatalf("identifier term below planIdentifierIDFFloor should still be silent, got %v", matches)
	}
}

// TestPlanFrictionIndexJoinNonIdentifierStillNeedsTheFloor is the negative
// control for the case above: two ordinary, non-identifier words made just
// as common in the corpus do not get the same exemption, so the step yields
// no anchor terms and the join stays silent — v1.6 narrows the floor for
// identifier-shaped words specifically, it does not lift it in general.
func TestPlanFrictionIndexJoinNonIdentifierStillNeedsTheFloor(t *testing.T) {
	withStatsStores(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "missing-policy.json"))

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("plan-common-wall-%d", i)
		toolID := fmt.Sprintf("tool-plan-common-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-common", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"cache retry again"}}`,
				id, old,
			),
			fmt.Sprintf(
				`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":"echo cache retry done"}}]}}`,
				id, old, toolID,
			),
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":true,"content":"/bin/sh: cache retry limit exceeded"}]}}`,
				id, old, toolID,
			),
		})
	}

	// Same 20-of-30 spread as the identifier case above, but with two plain
	// English words instead of an identifier.
	for i := 0; i < 17; i++ {
		id := fmt.Sprintf("plan-common-noise-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-common-noise", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"cache retry discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("plan-common-other-%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "plan-common-other", id+".jsonl"), id, []string{
			fmt.Sprintf(
				`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"unrelated color palette discussion number %d"}}`,
				id, old, i,
			),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	steps := planSearchSteps("1. cache retry")
	if len(steps) != 1 || len(steps[0]) != 2 {
		t.Fatalf("steps = %v, want a single two-term step", steps)
	}
	matches := index.PlanFrictionMatches(
		index.DefaultDir(),
		steps,
		func(index.SessionMeta) bool { return true },
		4,
	)
	if len(matches) != 0 {
		t.Fatalf("non-identifier common terms should still fail the floor, got %v", matches)
	}
}

func samplePlanMatches(project string) []index.PlanCooccurrence {
	base := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	sessions := []index.SessionMeta{
		{ID: "wall-a", Harness: "claude", Project: project, Updated: base},
		{ID: "wall-b", Harness: "codex", Project: project, Updated: base.Add(24 * time.Hour)},
		{ID: "wall-c", Harness: "claude", Project: project, Updated: base.Add(48 * time.Hour)},
	}
	return []index.PlanCooccurrence{{
		Wall: index.Friction{
			Text:     "/bin/sh: timeout: command not found",
			Sessions: sessions,
			Last:     sessions[2].Updated,
		},
		Command:         `timeout 5 deploy_probe --config C:\work\repo\PLAN.md`,
		CommandSessions: sessions[:2],
	}}
}

func withPlanLookup(t *testing.T, result []index.PlanCooccurrence) {
	t.Helper()
	oldReady := planIndexReady
	oldLookup := planFrictionLookup
	planIndexReady = func(string) bool { return true }
	planFrictionLookup = func(
		_ string,
		_ [][]string,
		keep func(index.SessionMeta) bool,
		limit int,
	) []index.PlanCooccurrence {
		var out []index.PlanCooccurrence
		for _, match := range result {
			var wall []index.SessionMeta
			members := map[string]bool{}
			for _, meta := range match.Wall.Sessions {
				if keep(meta) {
					wall = append(wall, meta)
					members[planMetaKey(meta)] = true
				}
			}
			if len(wall) < index.FrictionMinSessions {
				continue
			}
			var commands []index.SessionMeta
			for _, meta := range match.CommandSessions {
				if keep(meta) && members[planMetaKey(meta)] {
					commands = append(commands, meta)
				}
			}
			if len(commands) == 0 {
				continue
			}
			match.Wall.Sessions = wall
			match.CommandSessions = commands
			out = append(out, match)
			if limit > 0 && len(out) == limit {
				break
			}
		}
		return out
	}
	t.Cleanup(func() {
		planIndexReady = oldReady
		planFrictionLookup = oldLookup
	})
}
