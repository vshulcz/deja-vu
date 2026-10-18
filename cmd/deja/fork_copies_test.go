package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// claudeForkStore writes a source and a fork that holds the source's four
// records, same uuids and times, plus two turns of its own, into a sandbox
// Claude store. marked adds the forkedFrom a /branch writes.
func claudeForkStore(t *testing.T, marked bool) string {
	return claudeForkStoreWent(t, marked, false)
}

// claudeForkStoreWent is claudeForkStore with the source going on after the
// fork when went is set, with a turn the fork never saw.
func claudeForkStoreWent(t *testing.T, marked, went bool) string {
	t.Helper()
	home := hermeticEnv(t)
	dir := filepath.Join(t.TempDir(), "idx")
	t.Setenv("DEJA_INDEX_DIR", dir)
	const (
		src  = "aaaaaaaa-0000-4000-8000-00000000000a"
		fork = "ffffffff-0000-4000-8000-00000000000f"
	)
	cwd := filepath.Join(home, "work", "app")
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-app")
	if err := os.MkdirAll(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-26 * time.Hour).UTC()
	rec := func(session, uuid, role, text string, sec int, forkedFrom string) string {
		var content any = text
		if role == "assistant" {
			content = []map[string]string{{"type": "text", "text": text}}
		}
		m := map[string]any{
			"type": role, "sessionId": session, "uuid": uuid, "cwd": cwd,
			"timestamp": at.Add(time.Duration(sec) * time.Second).Format(time.RFC3339Nano),
			"message":   map[string]any{"role": role, "content": content},
		}
		if forkedFrom != "" {
			m["forkedFrom"] = map[string]string{"sessionId": forkedFrom}
		}
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	copied := func(session, forkedFrom string) string {
		return rec(session, "u1", "user", "the marmotquill exporter drops rows, add a zebracount", 0, forkedFrom) +
			rec(session, "u2", "assistant", "added zebracount, paging fixes the exporter", 5, forkedFrom) +
			rec(session, "u3", "user", "and the rows past 4096?", 10, forkedFrom) +
			rec(session, "u4", "assistant", "paged, all rows come out now", 15, forkedFrom)
	}
	from := ""
	if marked {
		from = src
	}
	srcBody := copied(src, "")
	if went {
		srcBody += rec(src, "s1", "user", "back on the source: the ospreyquill cache drops writes", 900, "") +
			rec(src, "s2", "assistant", "ospreyquill flushes now, zebracount untouched", 905, "")
	}
	writeFile(t, filepath.Join(proj, src+".jsonl"), srcBody)
	writeFile(t, filepath.Join(proj, fork+".jsonl"), copied(fork, from)+
		rec(fork, "f1", "user", "now look at the importer", 600, "")+
		rec(fork, "f2", "assistant", "the importer reads the whole file", 605, ""))
	return dir
}

// A fork opens with a copy of its source's turns, and search listed both as
// hits with the same quotes while last listed two rows under one title, marked
// or not (#5320).
func TestSearchAndLastDoNotRepeatAForksCopiedTurns(t *testing.T) {
	for _, marked := range []bool{true, false} {
		dir := claudeForkStore(t, marked)
		out := captureStdout(t, func() {
			if err := cmdSearch(dir, []string{"zebracount"}, ""); err != nil {
				t.Fatal(err)
			}
		})
		if strings.Contains(out, "ffffffff") || !strings.Contains(out, "aaaaaaaa") {
			t.Errorf("marked=%v: search answered with the fork's copy of its source's turns:\n%s", marked, out)
		}
		// Control: what the fork added is its own and still found.
		out = captureStdout(t, func() {
			if err := cmdSearch(dir, []string{"importer"}, ""); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(out, "ffffffff") {
			t.Errorf("marked=%v: the fork's own turn is not found:\n%s", marked, out)
		}
		out = captureStdout(t, func() {
			if err := cmdLast(dir, nil, ""); err != nil {
				t.Fatal(err)
			}
		})
		var forkRow string
		for _, ln := range strings.Split(out, "\n") {
			if strings.Contains(ln, "ffffffff") {
				forkRow = ln
			}
		}
		if !strings.Contains(forkRow, "fork of aaaaaaaa") {
			t.Errorf("marked=%v: last does not mark the fork:\n%s", marked, out)
		}
	}
}

// A copy dropped after the cap still took a slot and counted: --limit 1
// said "showing 1 of 2" with the second a fork repeating the first (#5335).
// And MCP recall listed the fork beside its source (#5320).
func TestACappedSearchAndMCPRecallDropAForkBeforeCounting(t *testing.T) {
	dir := claudeForkStore(t, false)
	out := captureStdout(t, func() {
		if err := cmdSearch(dir, []string{"zebracount", "--limit", "1", "--json"}, ""); err != nil {
			t.Fatal(err)
		}
	})
	var got struct {
		Total  int  `json:"total"`
		Capped bool `json:"capped"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if got.Total != 1 || got.Capped {
		t.Errorf("total %d capped %v, want 1 and not capped:\n%s", got.Total, got.Capped, out)
	}
	rec, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"zebracount"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec, "ffffffff") || !strings.Contains(rec, "aaaaaaaa") {
		t.Errorf("MCP recall listed the fork's copy beside its source:\n%s", rec)
	}
}
