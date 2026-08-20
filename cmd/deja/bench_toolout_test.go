package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// A corpus chain that says "a tool printed this" has to reach the index as tool
// output. Written as a transcript line of type "tool-output" it reached nothing
// at all: the Claude reader knows "user" and "assistant", so the message was
// dropped and a benchmark arm built on it passed because the corpus was empty.
func TestBenchCorpusCarriesToolOutput(t *testing.T) {
	when := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	session := model.Session{ID: "toolout", Project: "toolproject", Harness: "claude",
		Started: when, Updated: when.Add(time.Minute), Messages: []model.Message{
			{Role: "user", Text: "прогони пайплайн", Time: when},
			{Role: "tool-output", Text: "[toil.leader] Job 'WDLStartJob' v41 is completely failed",
				Time: when.Add(30 * time.Second)},
		}}

	root := t.TempDir()
	if err := writeBenchCorpus(root, []model.Session{session}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "toolproject", "toolout.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var sawToolResult bool
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(entry.Message.Content), `"tool_result"`) {
			sawToolResult = true
			if entry.Type != "user" {
				t.Errorf("tool output written under type %q, which the reader skips", entry.Type)
			}
		}
	}
	if !sawToolResult {
		t.Fatal("no tool_result block written, so the corpus cannot hold tool output")
	}

	dir := filepath.Join(t.TempDir(), "index.db")
	restore := isolateBenchEnv(t.TempDir(), root, dir)
	defer restore()
	if err := index.EnsureForSearch(dir, query.Options{All: true}, true, io.Discard); err != nil {
		t.Fatal(err)
	}
	ss, err := index.Search(dir, query.Options{Query: "v41", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range ss {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "v41") && m.Role == "tool-output" {
				found = true
			}
		}
	}
	if !found {
		t.Error("the indexed session holds no tool-output message carrying v41")
	}
}
