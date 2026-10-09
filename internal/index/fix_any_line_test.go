package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A failing run prints several error lines, and friction counts each of them.
// The pair was keyed on the first one only, so `deja fix` with the line that
// actually explains the failure — the refused connection under the test name —
// said no session ran anything after it, while friction listed it and the
// post-tool hook, handed the whole output, answered.
func TestAFixIsFoundByAnyErrorLineOfTheOutput(t *testing.T) {
	at := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	out := "--- FAIL: TestCheckout (0.02s)\n" +
		"    pool_test.go:41: dial tcp 127.0.0.1:5432: connect: connection refused\n" +
		"FAIL\nexit status 1"
	ms := []model.Message{
		{Role: roleCommand, Text: "go test ./internal/pool/...", Time: at},
		{Role: roleToolOutput, Text: out, Time: at.Add(time.Second)},
		{Role: roleCommand, Text: "docker compose up -d postgres", Time: at.Add(2 * time.Second)},
		{Role: roleToolOutput, Text: "Container postgres  Started", Time: at.Add(3 * time.Second)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "checkout")
	if len(pairs) == 0 {
		t.Fatal("the run mined no pair at all")
	}
	dir := t.TempDir()
	writeFixesForTest(t, dir, pairs)
	for _, asked := range []string{
		"dial tcp 127.0.0.1:5432: connect: connection refused",
		"--- FAIL: TestCheckout (0.02s)",
	} {
		got := FixesFor(dir, asked, 3, nil)
		if len(got) == 0 || got[0].Command != "docker compose up -d postgres" {
			t.Errorf("FixesFor(%q) = %+v", asked, got)
		}
	}
}
