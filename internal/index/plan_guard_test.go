package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func buildPlanGuardCorpus(t *testing.T, name string, sessions []model.Session) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, name)
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPlanTextHasTermRequiresIdentifierBoundaries(t *testing.T) {
	if planTextHasTerm("--- FAIL: TestACommandFailureUpdateEndsWhereAFullBuildDoes", "update") {
		t.Fatal("matched 'update' inside camelCase identifier")
	}
	if planTextHasTerm("--- FAIL: TestFixPairsAreRedactedLikeTheRecordLog", "pairs") {
		t.Fatal("matched 'pairs' inside camelCase identifier")
	}
	if !planTextHasTerm("$ make migrate-orders", "orders") {
		t.Fatal("missed hyphen-bounded 'orders' in 'make migrate-orders'")
	}
	if !planTextHasTerm("go run ./deploy_probe --timeout 5", "deploy_probe") {
		t.Fatal("missed path-bounded 'deploy_probe'")
	}
	if !planTextHasTerm("/bin/sh: timeout: command not found", "timeout") {
		t.Fatal("missed punctuation-bounded 'timeout'")
	}
}

func TestPlanFrictionMatchesRejectsSubstringAndPiggybackFalsePositives(t *testing.T) {
	base := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	sessions := make([]model.Session, 0, 48)
	for i := range 48 {
		s := model.Session{
			ID:      "g" + string(rune('A'+i/26)) + string(rune('a'+i%26)),
			Harness: "claude",
			Project: "app",
			Updated: base.Add(time.Duration(i) * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "general refactoring pass"},
			},
		}
		if i < 4 {
			s.Messages = append(s.Messages,
				model.Message{Role: "user", Text: "review update and pairs serialization"},
				model.Message{Role: "tool-output", Text: "--- FAIL: TestACommandFailureUpdateEndsWhereAFullBuildDoes"},
				model.Message{Role: "tool-output", Text: "--- FAIL: TestFixPairsAreRedactedLikeTheRecordLog"},
			)
		}
		if i >= 4 && i < 8 {
			// Piggyback sessions mention "pairs" without the step's identifier
			// (internal/index/orient.go) and hit a wall mentioning standalone "pairs".
			s.Messages = append(s.Messages,
				model.Message{Role: "user", Text: "check pairs list output"},
				model.Message{Role: "tool-output", Text: "error: pairs validation failed"},
			)
		}
		sessions = append(sessions, s)
	}
	dir := buildPlanGuardCorpus(t, "plan-guard-fp", sessions)

	if out := PlanFrictionMatches(dir, planSteps("update", "co-change", "pairs"), nil, 0); len(out) != 0 {
		t.Fatalf("substring camelCase false positives matched: %+v", out)
	}
	if out := PlanFrictionMatches(dir, planSteps("internal/index/orient.go", "pairs"), nil, 0); len(out) != 0 {
		t.Fatalf("non-identifier word piggybacked on identifier step need=1: %+v", out)
	}
}

func TestPlanFrictionMatchesSkipsFailedAndInspectionCommands(t *testing.T) {
	base := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)
	makeCorpus := func(name string, cmds []string) string {
		sessions := make([]model.Session, 0, 48)
		for i := range 48 {
			s := model.Session{
				ID:      "c" + string(rune('A'+i/26)) + string(rune('a'+i%26)),
				Harness: "claude",
				Project: "app",
				Updated: base.Add(time.Duration(i) * time.Hour),
				Messages: []model.Message{
					{Role: "user", Text: "general UI polish work"},
				},
			}
			if i < 4 {
				s.Messages = append(s.Messages,
					model.Message{Role: "user", Text: "run pytest_asyncio migration suite"},
					model.Message{Role: "tool-output", Text: planWall},
				)
				for _, cmd := range cmds {
					s.Messages = append(s.Messages, model.Message{Role: "command", Text: cmd})
				}
			}
			sessions = append(sessions, s)
		}
		return buildPlanGuardCorpus(t, name, sessions)
	}

	filtered := makeCorpus("plan-guard-cmds-filtered", []string{
		"$ pytest pytest_asyncio → exit 1",
		"$ git grep -n pytest_asyncio",
		"$ rg pytest_asyncio",
	})
	out := PlanFrictionMatches(filtered, planSteps("pytest_asyncio"), nil, 0)
	if len(out) != 1 {
		t.Fatalf("filtered findings = %d, want 1: %+v", len(out), out)
	}
	if out[0].Command != "" {
		t.Fatalf("failed/inspection/viewer command leaked into plan finding: %q", out[0].Command)
	}

	realCmds := makeCorpus("plan-guard-cmds-real", []string{
		"$ pytest pytest_asyncio",
	})
	outReal := PlanFrictionMatches(realCmds, planSteps("pytest_asyncio"), nil, 0)
	if len(outReal) != 1 {
		t.Fatalf("real findings = %d, want 1: %+v", len(outReal), outReal)
	}
	if outReal[0].Command != "$ pytest pytest_asyncio" {
		t.Fatalf("real command = %q, want %q", outReal[0].Command, "$ pytest pytest_asyncio")
	}
}
