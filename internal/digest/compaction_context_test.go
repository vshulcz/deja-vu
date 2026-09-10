package digest

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestExtractCompactionContextUsesOnlyRecordedEvidence(t *testing.T) {
	at := time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC)
	s := model.Session{
		ID: "session-123456789", Harness: "codex",
		Messages: []model.Message{
			{Role: "user", Text: "Implement parser recovery and keep validation honest.", Time: at},
			{Role: "assistant", Text: "Decision: keep malformed input fail-closed and report its source.", Time: at.Add(time.Minute)},
			{Role: sources.RoleCommand, Text: "$ go test ./... → exit 0", Time: at.Add(2 * time.Minute)},
			{Role: sources.RoleCommand, Text: "$ go vet ./... → exit 2", Time: at.Add(3 * time.Minute)},
			{Role: sources.RoleCommand, Text: "$ npm test", Time: at.Add(4 * time.Minute)},
			{Role: sources.RoleToolOutput, Text: "Conflict: tool output is not an agent claim.", Time: at.Add(5 * time.Minute)},
			{Role: "assistant", Text: "Gap: the integration fixture is still unverified.\nConflict: docs say two retries but code says three.", Time: at.Add(6 * time.Minute)},
			{Role: "user", Text: "continue", Time: at.Add(7 * time.Minute)},
		},
	}

	c := ExtractCompactionContext(s, ExtractOptions{})
	if !strings.Contains(c.Objective.Text, "Implement parser recovery") {
		t.Fatalf("objective = %#v", c.Objective)
	}
	if len(c.Conclusions) != 1 || !strings.Contains(c.Conclusions[0].Text, "Decision:") {
		t.Fatalf("conclusions = %#v", c.Conclusions)
	}
	if len(c.Tests) != 3 {
		t.Fatalf("tests = %#v", c.Tests)
	}
	if got := []string{c.Tests[0].Outcome, c.Tests[1].Outcome, c.Tests[2].Outcome}; strings.Join(got, ",") != "passed,failed,recorded" {
		t.Fatalf("outcomes = %v", got)
	}
	if len(c.Gaps) != 1 || !strings.HasPrefix(c.Gaps[0].Text, "Gap:") {
		t.Fatalf("gaps = %#v", c.Gaps)
	}
	if len(c.Conflicts) != 1 || !strings.HasPrefix(c.Conflicts[0].Text, "Conflict:") {
		t.Fatalf("conflicts = %#v", c.Conflicts)
	}
	if c.Conflicts[0].Provenance.Role != "assistant" || c.Conflicts[0].Provenance.SessionID != s.ID {
		t.Fatalf("conflict provenance = %#v", c.Conflicts[0].Provenance)
	}
	for _, item := range c.Conflicts {
		if strings.Contains(item.Text, "tool output") {
			t.Fatalf("tool output became a conflict: %#v", c.Conflicts)
		}
	}
}

func TestExtractCompactionContextFallsBackToHarnessCompactionIntent(t *testing.T) {
	summary := "Summary:\n1. Primary Request and Intent:\n  Repair the decoder and add a fixture.\n2. Key Technical Concepts:\n  - irrelevant\n"
	s := model.Session{ID: "s", Harness: "claude", Messages: []model.Message{
		{Role: "user", Text: summary},
		{Role: "user", Text: "go on"},
	}}
	c := ExtractCompactionContext(s, ExtractOptions{})
	if !strings.Contains(c.Objective.Text, "Repair the decoder") {
		t.Fatalf("summary intent was not retained: %#v", c.Objective)
	}
	if strings.Contains(c.Objective.Text, "Key Technical Concepts") {
		t.Fatalf("summary was not bounded to intent: %#v", c.Objective)
	}
}

func TestExtractCompactionContextFallsBackToEmphasizedHarnessIntent(t *testing.T) {
	summary := "Summary:\n1. **Primary Request and Intent:**\n  Repair the decoder and keep the fixture.\n2. Key Technical Concepts:\n  - irrelevant\n"
	s := model.Session{ID: "s", Harness: "claude", Messages: []model.Message{
		{Role: "user", Text: summary},
		{Role: "user", Text: "continue"},
	}}
	c := ExtractCompactionContext(s, ExtractOptions{})
	if !strings.Contains(c.Objective.Text, "Repair the decoder") {
		t.Fatalf("emphasized summary intent was lost: %#v", c.Objective)
	}
	if strings.Contains(c.Objective.Text, "Key Technical Concepts") {
		t.Fatalf("emphasized summary was not bounded to intent: %#v", c.Objective)
	}
}

func TestCompactionIntentStaysAlignedWithSummaryRecognition(t *testing.T) {
	for _, tc := range []struct {
		name, summary, want string
	}{
		{
			name:    "inline plain heading",
			summary: "Summary: 1. Primary Request and Intent: Repair the inline decoder.\n2. Key Technical Concepts:\n  ignored\n",
			want:    "Repair the inline decoder.",
		},
		{
			name:    "multiline heading",
			summary: "Summary:\n1. Primary Request and Intent:\n  Repair the multiline decoder.\n2. Key Technical Concepts:\n  ignored\n",
			want:    "Repair the multiline decoder.",
		},
		{
			name:    "underscored heading",
			summary: "Summary:\n1. __Primary Request and Intent:__\n  Repair the underscored decoder.\n2. Key Technical Concepts:\n  ignored\n",
			want:    "Repair the underscored decoder.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !IsCompactionSummary(tc.summary) {
				t.Fatalf("summary was not recognised: %q", tc.summary)
			}
			got := compactionIntent(tc.summary)
			if !strings.Contains(got, tc.want) || strings.Contains(got, "Key Technical Concepts") || strings.Contains(got, "__") || strings.Contains(got, "**") {
				t.Fatalf("intent = %q, want %q only", got, tc.want)
			}
		})
	}
}

func TestCompactionContextRedactsAtExtractionAndStorageBoundary(t *testing.T) {
	secret := "abcdefghijklmnoPQRSTUVWXYZ123456"
	s := model.Session{ID: "s", Harness: "codex", Messages: []model.Message{
		{Role: "user", Text: "Fix the leak."},
		{Role: "assistant", Text: "Gap: token: " + secret + " has not been rotated."},
	}}
	c := ExtractCompactionContext(s, ExtractOptions{})
	if strings.Contains(c.Gaps[0].Text, secret) {
		t.Fatalf("extractor stored secret: %#v", c.Gaps)
	}
	c.Gaps[0].Text = "Gap: token: " + secret
	if got := RedactCompactionContext(c).Gaps[0].Text; strings.Contains(got, secret) {
		t.Fatalf("storage redaction retained secret: %q", got)
	}
	t.Setenv("DEJA_NO_REDACT", "1")
	if got := RedactCompactionContext(c).Gaps[0].Text; !strings.Contains(got, secret) {
		t.Fatalf("documented redaction opt-out was ignored: %q", got)
	}
}

func TestRenderCompactionContextBoundsAndNamesFreshnessFailure(t *testing.T) {
	c := model.CompactionContext{
		Objective: model.ContextFact{Text: "Repair the parser."},
		Freshness: model.RepositoryFreshness{Error: "git status timed out"},
		Gaps:      []model.ContextOpenItem{{Text: "Gap: run integration coverage."}},
		Truncated: true,
	}
	got := RenderCompactionContext(c, 512)
	if len(got) > 512 {
		t.Fatalf("rendered %d bytes, want <= 512: %q", len(got), got)
	}
	if !strings.Contains(got, "Repository freshness unavailable") {
		t.Fatalf("freshness failure was hidden: %q", got)
	}
	if !strings.Contains(got, "Omitted transcript records") {
		t.Fatalf("truncation was hidden: %q", got)
	}
}

func TestExtractCompactionContextBoundsTranscriptWindow(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Text: "old objective"},
		{Role: "assistant", Text: "Decision: old conclusion."},
		{Role: "assistant", Text: "Decision: recent conclusion."},
	}
	c := ExtractCompactionContext(model.Session{ID: "s", Harness: "codex", Messages: messages}, ExtractOptions{MaxRecords: 1})
	if !c.Truncated {
		t.Fatal("record-window cutoff was not marked")
	}
	if len(c.Conclusions) != 1 || !strings.Contains(c.Conclusions[0].Text, "recent") {
		t.Fatalf("window did not prefer newest record: %#v", c.Conclusions)
	}
}

func TestExtractCompactionContextSkipsLatestOversizedToolOutput(t *testing.T) {
	s := model.Session{ID: "s", Harness: "codex", Messages: []model.Message{
		{Role: "user", Text: "Repair the parser."},
		{Role: "assistant", Text: "Decision: preserve malformed input evidence."},
		{Role: sources.RoleToolOutput, Text: strings.Repeat("x", defaultContextText+1)},
	}}
	c := ExtractCompactionContext(s, ExtractOptions{})
	if !c.Truncated {
		t.Fatal("skipped oversized tool output was not marked")
	}
	if !strings.Contains(c.Objective.Text, "Repair the parser") {
		t.Fatalf("oversized output erased objective: %#v", c.Objective)
	}
	if len(c.Conclusions) != 1 || !strings.Contains(c.Conclusions[0].Text, "preserve malformed") {
		t.Fatalf("oversized output erased conclusion: %#v", c.Conclusions)
	}
}

func TestExtractCompactionContextFindsObjectiveBehindToolStream(t *testing.T) {
	messages := []model.Message{{Role: "user", Text: "Repair the parser and add the fixture."}}
	for range 80 {
		messages = append(messages, model.Message{Role: sources.RoleToolOutput, Text: "ordinary tool output"})
	}
	messages = append(messages, model.Message{Role: "user", Text: "continue"})
	c := ExtractCompactionContext(model.Session{ID: "s", Harness: "codex", Messages: messages}, ExtractOptions{})
	if !strings.Contains(c.Objective.Text, "Repair the parser") {
		t.Fatalf("tool stream erased objective: %#v", c.Objective)
	}
}

func TestExtractCompactionContextRecognizesMarkdownLabels(t *testing.T) {
	s := model.Session{ID: "s", Harness: "codex", Messages: []model.Message{
		{Role: "assistant", Text: "**Gap:** validation has not run.\n- **Conflict:** docs and code disagree."},
	}}
	c := ExtractCompactionContext(s, ExtractOptions{})
	if len(c.Gaps) != 1 || len(c.Conflicts) != 1 {
		t.Fatalf("markdown labels lost: gaps=%#v conflicts=%#v", c.Gaps, c.Conflicts)
	}
}

func TestCompactionContextBoundsAndRedactsOversizedProvenance(t *testing.T) {
	secret := "abcdefghijklmnopqrstuvwxyz123456"
	longID := strings.Repeat("token: "+secret+" | ", 2000)
	s := model.Session{ID: longID, Harness: strings.Repeat("h", 500), Messages: []model.Message{
		{Role: "user", Text: "Repair the parser."},
		{Role: "assistant", Text: "Decision: preserve the evidence."},
		{Role: "assistant", Text: "Gap: integration has not run."},
		{Role: "assistant", Text: "Conflict: docs and code disagree."},
	}}
	c := ExtractCompactionContext(s, ExtractOptions{})
	if got := serializedContextBytes(c); got > maxContextBytes {
		t.Fatalf("context is %d bytes, want <= %d", got, maxContextBytes)
	}
	for _, ref := range c.Sources {
		if len(ref.SessionID) > 256 || strings.Contains(ref.SessionID, secret) {
			t.Fatalf("unbounded or unredacted source: %#v", ref)
		}
	}
}

func TestRenderCompactionContextTreatsPartialFingerprintAsUnverified(t *testing.T) {
	c := model.CompactionContext{Freshness: model.RepositoryFreshness{
		Head: "abc", WorktreeState: "partial:deadbeef",
	}}
	got := RenderCompactionContext(c, 1024)
	if !strings.Contains(got, "Repository fingerprint is partial") {
		t.Fatalf("partial fingerprint was not called out: %q", got)
	}
	if strings.Contains(got, "Repository freshness: head=abc") {
		t.Fatalf("partial fingerprint was rendered as complete: %q", got)
	}
}

func TestRedactCompactionContextBoundsDirectStorageInput(t *testing.T) {
	long := strings.Repeat("untrusted transcript text ", 4000)
	ref := model.ContextRef{SessionID: strings.Repeat("session-", 1000), Harness: "codex", Role: "assistant"}
	c := model.CompactionContext{
		Objective:   model.ContextFact{Text: long, Provenance: ref},
		Gaps:        []model.ContextOpenItem{{Text: long, Provenance: ref}, {Text: long, Provenance: ref}},
		Conflicts:   []model.ContextOpenItem{{Text: long, Provenance: ref}, {Text: long, Provenance: ref}},
		Tests:       []model.ContextTest{{Command: long, Provenance: ref}},
		Conclusions: []model.ContextFact{{Text: long, Provenance: ref}},
	}
	got := RedactCompactionContext(c)
	if got.Objective.Text == long || len(got.Objective.Text) > contextObjectiveBytes {
		t.Fatalf("objective was not bounded: %d bytes", len(got.Objective.Text))
	}
	if size := serializedContextBytes(got); size > maxContextBytes {
		t.Fatalf("direct storage input remained %d bytes, want <= %d", size, maxContextBytes)
	}
	if len(got.Sources) == 0 || len(got.Sources[0].SessionID) > 256 {
		t.Fatalf("provenance was not bounded: %#v", got.Sources)
	}
}

func TestRedactCompactionContextDropsLowPriorityRecordsToFitManifest(t *testing.T) {
	long := strings.Repeat("x", 2000)
	c := model.CompactionContext{Objective: model.ContextFact{Text: "Keep the objective.", Provenance: model.ContextRef{SessionID: "objective", Harness: "codex", Role: "user"}}}
	for i := 0; i < 16; i++ {
		ref := model.ContextRef{SessionID: strings.Repeat(string(rune('a'+i)), 1000), Harness: strings.Repeat("h", 100), Role: "assistant"}
		c.Conclusions = append(c.Conclusions, model.ContextFact{Text: long, Provenance: ref})
		c.Tests = append(c.Tests, model.ContextTest{Command: long, Outcome: "recorded", Provenance: ref})
		c.Gaps = append(c.Gaps, model.ContextOpenItem{Text: long, Provenance: ref})
		c.Conflicts = append(c.Conflicts, model.ContextOpenItem{Text: long, Provenance: ref})
	}
	got := RedactCompactionContext(c)
	if !got.Truncated || serializedContextBytes(got) > maxContextBytes {
		t.Fatalf("oversized manifest state was accepted: truncated=%t bytes=%d", got.Truncated, serializedContextBytes(got))
	}
	if len(got.Conclusions) != 0 || len(got.Tests) != 0 {
		t.Fatalf("lower-priority records survived before explicit open items: conclusions=%d tests=%d", len(got.Conclusions), len(got.Tests))
	}
	if got.Objective.Text != "Keep the objective." || len(got.Gaps) == 0 || len(got.Conflicts) == 0 {
		t.Fatalf("required resume state was not retained: %#v", got)
	}
}

func TestRenderCompactionContextRendersEveryEvidenceClassWithProvenance(t *testing.T) {
	ref := model.ContextRef{SessionID: "session-abcdef", Harness: "codex", Role: "assistant", At: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)}
	c := model.CompactionContext{
		Objective:   model.ContextFact{Text: "Repair the parser.", Provenance: ref},
		Conclusions: []model.ContextFact{{Text: "Decision: reject malformed records.", Provenance: ref}},
		Tests:       []model.ContextTest{{Command: "$ go test ./... → exit 0", Outcome: "passed", Provenance: ref}},
		Gaps:        []model.ContextOpenItem{{Text: "Gap: run the integration fixture.", Provenance: ref}},
		Conflicts:   []model.ContextOpenItem{{Text: "Conflict: docs disagree with code.", Provenance: ref}},
		Freshness: model.RepositoryFreshness{
			Head: "abc", Branch: "main", WorktreeState: "clean-digest", CheckedAt: ref.At,
		},
	}
	got := RenderCompactionContext(c, 4096)
	for _, want := range []string{
		"Repository freshness: head=abc, branch=main, worktree=clean-digest",
		"Objective", "Explicit gaps", "Explicit conflicts", "Recorded verification commands", "Assistant-reported conclusions",
		"[passed]", "[codex:session-abcdef/assistant @2026-09-10T12:00:00Z]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("render missed %q:\n%s", want, got)
		}
	}
}

func TestRenderCompactionContextNamesMissingFreshness(t *testing.T) {
	got := RenderCompactionContext(model.CompactionContext{}, 1024)
	if !strings.Contains(got, "Repository freshness unavailable: hook did not record it.") {
		t.Fatalf("missing freshness was hidden: %q", got)
	}
}

func TestExtractCompactionContextDedupesAndCapsRecordedEvidence(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Text: "Keep recovery bounded."},
		{Role: sources.RoleCommand, Text: "$ echo not-a-test"},
		{Role: sources.RoleCommand, Text: "$ go test ./... → exit 0"},
		{Role: sources.RoleCommand, Text: "$ go test ./... → exit 0"},
	}
	for i := 0; i < defaultContextItems+2; i++ {
		messages = append(messages, model.Message{Role: "assistant", Text: "Decision: keep item " + string(rune('a'+i)) + "."})
		messages = append(messages, model.Message{Role: "assistant", Text: "Gap: validate item " + string(rune('a'+i)) + "."})
	}
	c := ExtractCompactionContext(model.Session{ID: "s", Harness: "codex", Messages: messages}, ExtractOptions{})
	if len(c.Tests) != 1 || len(c.Conclusions) != defaultContextItems || len(c.Gaps) != defaultContextItems || !c.Truncated {
		t.Fatalf("dedupe/cap failed: %#v", c)
	}
	if strings.Contains(c.Tests[0].Command, "echo") {
		t.Fatalf("ordinary command became test evidence: %#v", c.Tests)
	}
}

func TestContextWindowStopsAtHardScanBudget(t *testing.T) {
	messages := make([]model.Message, 5)
	for i := range messages {
		messages[i] = model.Message{Role: sources.RoleToolOutput, Text: strings.Repeat("x", 1<<20)}
	}
	window, truncated := contextWindow(messages, ExtractOptions{})
	if !truncated || len(window) != 0 {
		t.Fatalf("unbounded tool stream was retained: truncated=%t window=%d", truncated, len(window))
	}
}

func TestRenderCompactionContextKeepsItsByteContractAtTinyBudgets(t *testing.T) {
	c := model.CompactionContext{Objective: model.ContextFact{Text: "Repair the parser."}, Truncated: true}
	for _, budget := range []int{1, 16, 128} {
		got := RenderCompactionContext(c, budget)
		if len(got) > budget {
			t.Fatalf("budget %d produced %d bytes: %q", budget, len(got), got)
		}
	}
}
