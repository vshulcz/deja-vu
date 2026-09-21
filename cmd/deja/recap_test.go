package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The rule the issue is most insistent about: a line is something somebody
// already wrote, printed under the session it came from. A draft with receipts
// can be checked; a paragraph in a model's voice is the thing people are
// embarrassed to paste (#544).
func TestRecapCarriesTheSessionBehindEveryLine(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-work-app", "week1.jsonl"), "week1", []string{
		`{"type":"user","sessionId":"week1","cwd":"/work/app","timestamp":"` + ts +
			`","message":{"role":"user","content":"the payout retries fire twice"}}`,
		`{"type":"assistant","sessionId":"week1","cwd":"/work/app","timestamp":"` + ts +
			`","message":{"role":"assistant","content":"root cause: the scheduler kept running after the failover, so every payout was submitted twice"}}`,
		`{"type":"assistant","sessionId":"week1","cwd":"/work/app","timestamp":"` + ts +
			`","message":{"role":"assistant","content":"decided: POST /v1/payouts is exempt from the retry budget because a duplicate payout is worse than a dropped one"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runRecap(dir, nil, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "the scheduler kept running after the failover") {
		t.Fatalf("the recap does not quote what the session settled:\n%s", got)
	}
	if !strings.Contains(got, "work/app") {
		t.Errorf("the recap does not group by project:\n%s", got)
	}
	// The receipt: harness, date and the session id, so a line can be chased
	// back to where it was said.
	if !strings.Contains(got, "claude · ") || !strings.Contains(got, "week1") {
		t.Errorf("the recap does not name the session behind the line:\n%s", got)
	}
}

func TestRecapJSONAndItsFlags(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-work-net", "week2.jsonl"), "week2", []string{
		`{"type":"user","sessionId":"week2","cwd":"/work/net","timestamp":"` + ts +
			`","message":{"role":"user","content":"the handshake hangs"}}`,
		`{"type":"assistant","sessionId":"week2","cwd":"/work/net","timestamp":"` + ts +
			`","message":{"role":"assistant","content":"root cause: the default route moved to 10.14.3.7 and the source address stayed on the old subnet"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runRecap(dir, []string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var got recapJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if got.Kind != "deja.recap" || got.Schema == 0 {
		t.Errorf("no kind or schema version: %+v", got)
	}
	if len(got.Sessions) != 1 || len(got.Sessions[0].Lines) == 0 {
		t.Fatalf("the JSON holds no lines: %+v", got.Sessions)
	}
	// The masking is not a screen decoration: a consumer reading the JSON is
	// pasting it somewhere too.
	if strings.Contains(out.String(), "10.14.3.7") {
		t.Errorf("an address reached the JSON:\n%s", out.String())
	}
	if got.Masked["ip"] != 1 {
		t.Errorf("the JSON does not report what was masked: %v", got.Masked)
	}
	for _, args := range [][]string{{"--weekly"}, {"--since"}, {"--since", "soon"}, {"--limit"}, {"--limit", "-2"}} {
		if err := runRecap(dir, args, &out); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}

// An empty window says so rather than printing a heading with nothing under it.
func TestRecapSaysWhenTheWindowIsEmpty(t *testing.T) {
	withStatsStores(t)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runRecap(dir, []string{"--since", "1d"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing in the last 1d") {
		t.Fatalf("an empty window does not say so:\n%s", out.String())
	}
}
