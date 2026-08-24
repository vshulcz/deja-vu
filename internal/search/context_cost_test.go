package search

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Rendering a digest ran two regexes over every line of every turn, so a
// 30000-turn session spent 22.6 s producing 8 KB (#1742). Counting how many
// lines reach the engine says that directly, and says it the same on any
// machine — a wall-clock bound does not.
func TestOrdinaryProseNeverReachesTheRegexpEngine(t *testing.T) {
	build := func(n int) model.Session {
		s := model.Session{Harness: "claude", ID: "big", Project: "proj", Updated: time.Now()}
		for i := range n {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			s.Messages = append(s.Messages, model.Message{
				Role: role,
				Text: fmt.Sprintf("shard %d\n", i) + strings.Repeat("payload words here\n", 40),
			})
		}
		return s
	}

	before := toolDumpEngineCalls.Load()
	var out bytes.Buffer
	PrintContext(&out, build(2000), "grumbleflux")
	if got := toolDumpEngineCalls.Load() - before; got != 0 {
		t.Errorf("%d lines of ordinary prose were handed to the regexp engine", got)
	}
	if out.Len() == 0 || out.Len() > 9000 {
		t.Fatalf("digest is %d bytes", out.Len())
	}

	// A line that could match still reaches it — the prefilter decides nothing
	// on its own.
	s := model.Session{Harness: "claude", ID: "dump", Project: "proj", Updated: time.Now(),
		Messages: []model.Message{{Role: "user", Text: "goroutine 1 [running]:\nnpm ERR! code E404\nplain line"}}}
	before = toolDumpEngineCalls.Load()
	var dump bytes.Buffer
	PrintContext(&dump, s, "goroutine")
	if got := toolDumpEngineCalls.Load() - before; got != 2 {
		t.Errorf("lines carrying a tool-dump literal reached the engine %d times, want 2", got)
	}
}
