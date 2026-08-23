package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"

	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// Every line the brief prints has to fit the width it laid out for. The
// exception is the `recent` block, where a prefix that already fills the line
// keeps a 12-column title rather than an empty one — that floor is deliberate
// and TestBriefRecentLinesHonourColumns pins it.
//
// Measured before the fix on a 1548-session store: 5 lines over at COLUMNS=60,
// 9 at 50, 12 at 40. `today`, `before` and `try` carried no budget at all, and
// the fixed-prefix lines were cut to a constant 44 columns — 56 with the label,
// wider than the pane (#1588).
// briefNarrowStore seeds the lines the width contract is about: one question
// asked in three sessions (which is what puts `asked` and `before` on the
// screen), a project path of the length people actually have, a span that
// carries a year, and recalls served today so the `today` line grows its
// sizes. The first fixture for this test had none of those and passed while
// `before` was 64 columns wide (#1588).
func briefNarrowStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-work-platform-services-kafka-consumer-rebalance")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	const question = "why does the kafka consumer rebalance keep flapping in staging"
	for i, age := range []time.Duration{400 * 24 * time.Hour, 380 * 24 * time.Hour, 12 * 24 * time.Hour} {
		sid := string(rune('a'+i)) + "1b2c3d4"
		at := time.Now().Add(-age).UTC().Format(time.RFC3339)
		body := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/work/platform/services/kafka-consumer-rebalance","timestamp":%q,"message":{"role":"user","content":%q}}`,
			sid, at, question) + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	usage.RecordDigest(dir, usage.KindRecall, strings.Repeat("x", 4096), 3, 262144)
	usage.RecordDigest(dir, usage.KindDejaVu, "dv digest", 2, 4096)
	return dir
}

// The three lines this fix budgets have to fit the width the brief laid out
// for: `today`, `try`, and `before` in the shape where shortening the project
// keeps every fact.
//
// Not every line on this screen fits. `withheld` is 103 columns of fixed
// wording, `ahead` passes 60 once the count reaches three digits, `before`
// carrying the re-use suffix is 110, and the `recent` floor keeps a 12-column
// title rather than an empty one on purpose. Those need either a wording
// change, which is not a layout decision, or one choke point every line goes
// through; #1588 carries the reproductions.
func TestBriefTodayTryAndBeforeFitTheirPane(t *testing.T) {
	dir := briefNarrowStore(t)

	// 60 is the width the code lays out for — briefTitleBudget names the
	// 60-column split pane #604 fixed — and 80 is the default. Below 60 the
	// screen still overflows on the header, the suggestion and the `recent`
	// floor; those are separate items, not this one.
	for _, width := range []int{60, 80} {
		t.Setenv("COLUMNS", strconv.Itoa(width))
		var buf bytes.Buffer
		if err := runBrief(dir, &buf); err != nil {
			t.Fatal(err)
		}
		var seen int
		for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
			text := visibleText(line)
			if !strings.HasPrefix(text, "today ") && !strings.HasPrefix(text, "try ") &&
				!strings.HasPrefix(text, "before ") {
				continue
			}
			seen++
			if w := termwidth.Columns(text); w > width {
				t.Errorf("COLUMNS=%d: %d columns: %q", width, w, text)
			}
		}
		if seen < 2 {
			t.Fatalf("COLUMNS=%d: the fixture printed %d of the three budgeted lines:\n%s", width, seen, buf.String())
		}
	}
}
