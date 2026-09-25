package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

// claudeLines is a Claude Code transcript fragment: "u" a user turn, "a"
// assistant text, "s" the summary Claude files after a compaction, "t" a Bash
// command.
func claudeLines(t *testing.T, workspace string, minute int, pairs ...string) string {
	t.Helper()
	var b strings.Builder
	for i := 0; i < len(pairs); i += 2 {
		kind, text := pairs[i], pairs[i+1]
		rec := map[string]any{
			"sessionId": "compaction-fixture", "cwd": workspace,
			"timestamp": fmt.Sprintf("2026-09-10T11:%02d:%02dZ", minute, i/2),
		}
		switch kind {
		case "u", "s":
			rec["type"] = "user"
			rec["message"] = map[string]any{"role": "user", "content": text}
			if kind == "s" {
				rec["isCompactSummary"] = true
			}
		case "a":
			rec["type"] = "assistant"
			rec["message"] = map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}}
		case "t":
			rec["type"] = "assistant"
			rec["message"] = map[string]any{"role": "assistant", "content": []any{map[string]any{
				"type": "tool_use", "id": fmt.Sprintf("cmd-%d-%d", minute, i), "name": "Bash", "input": map[string]any{"command": text},
			}}}
		}
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func appendTranscript(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureCompactionCarriesTheListForward(t *testing.T) {
	dir, workspace, transcript := prepareCompaction(t)
	input := precompactHookInput{SessionID: "compaction-fixture", CWD: workspace, TranscriptPath: transcript}
	carry := func() []model.ContextCarry {
		t.Helper()
		state, found, err := index.Compaction(dir, "compaction-fixture", workspace)
		if err != nil || !found {
			t.Fatalf("no state: found=%v err=%v", found, err)
		}
		return state.Data.Carry
	}

	appendTranscript(t, transcript, claudeLines(t, workspace, 1, "u", "проверь очередь", "a", "PR #7307 ждёт мейнтейнера."))
	if _, ok := captureCompaction(dir, input); !ok {
		t.Fatal("first capture stored nothing")
	}
	if got := carry(); len(got) != 1 || got[0].Key != "#7307" || got[0].Age != 0 {
		t.Fatalf("first compaction: %+v", got)
	}

	// The next segment starts after Claude's summary and never names #7307:
	// the line is carried, one compaction older, next to what is new.
	appendTranscript(t, transcript, claudeLines(t, workspace, 2,
		"s", "This session is being continued from a previous conversation that ran out of context.",
		"u", "дальше", "a", "Гипотеза H12 отвергнута замером."))
	if _, ok := captureCompaction(dir, input); !ok {
		t.Fatal("second capture stored nothing")
	}
	got := carry()
	if len(got) != 2 || got[0].Kind != "open" || got[0].Age != 1 || got[1].Kind != "verdict" || got[1].Age != 0 {
		t.Fatalf("second compaction did not merge the carried list: %+v", got)
	}
	out := digest.RenderCarry(got)
	if !strings.HasPrefix(out, "Keep until closed") || !strings.Contains(out, "- [open] PR #7307") || !strings.Contains(out, "- [verdict] Гипотеза H12") {
		t.Fatalf("the carried list renders as %q", out)
	}

	// A merge command in the next segment closes it.
	appendTranscript(t, transcript, claudeLines(t, workspace, 3,
		"s", "This session is being continued from a previous conversation that ran out of context.",
		"u", "мержи", "t", "gh pr merge 7307 --squash"))
	if _, ok := captureCompaction(dir, input); !ok {
		t.Fatal("third capture stored nothing")
	}
	if got := carry(); len(got) != 1 || got[0].Kind != "verdict" || got[0].Age != 1 {
		t.Fatalf("merged PR stayed on the list: %+v", got)
	}
}
