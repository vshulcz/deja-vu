package index

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Adding an import moves every error in the file down a line. Keyed by that
// line, the same undefined symbol is a new wall each time — below the second
// sighting a fix pair needs and the three sessions `deja friction` needs, so
// the repair for something this machine hits weekly is never offered.
func TestACompilerErrorIsOneWallWhereverTheLineMoves(t *testing.T) {
	at4, ok := FrictionLine("./main.go:4:2: undefined: glimwraxHelper")
	if !ok {
		t.Fatal("a compile error is not read as friction at all")
	}
	at9, ok := FrictionLine("./main.go:9:2: undefined: glimwraxHelper")
	if !ok {
		t.Fatal("the same error one line down is not read as friction")
	}
	if frictionHash(at4) != frictionHash(at9) {
		t.Errorf("the same error at two lines is two walls:\n  %q\n  %q", at4, at9)
	}

	// The reader still sees where it was: the position is masked for the
	// signature, not taken out of the line.
	if !strings.Contains(at4, "4:2") {
		t.Errorf("the line the reader is shown lost its position: %q", at4)
	}

	// A different symbol is a different wall, and so is a different file.
	other, _ := FrictionLine("./main.go:4:2: undefined: otherHelper")
	if frictionHash(at4) == frictionHash(other) {
		t.Error("two different symbols were folded into one wall")
	}
	elsewhere, _ := FrictionLine("./cmd/run.go:4:2: undefined: glimwraxHelper")
	if frictionHash(at4) == frictionHash(elsewhere) {
		t.Error("the same symbol in two files was folded into one wall")
	}
}

// And the pair survives it: the command that settled the error at one line is
// offered when the same error comes back further down the file.
func TestAFixPairSurvivesTheLineMoving(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "command", Text: "go build ./...", Time: now},
		{Role: "tool-output", Text: "./main.go:4:2: undefined: glimwraxHelper", Time: now},
		{Role: "command", Text: "glimwraxctl --sync && go build ./...", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "ok", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 {
		t.Fatalf("want one pair, got %d", len(pairs))
	}
	line, ok := FrictionLine("./main.go:9:2: undefined: glimwraxHelper")
	if !ok {
		t.Fatal("the moved error is not friction")
	}
	if pairs[0].Sig != frictionHash(line) {
		t.Error("the repair is filed under a signature the same error one line down does not reach")
	}
}
