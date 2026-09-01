package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/query"
)

// A store where one harness holds almost everything is the ordinary case: on
// this machine claude holds 133k messages and zed 3.7k. The question #1556
// asks is whether an answer that exists only in the small store reaches the
// injected block, or whether the head of the ranking is simply taken by the
// harness with the most sessions.
//
// The fixture plants the answer in the small store and asks the question the
// planted session answers. It measures two things a report cannot separate on
// a private store: whether search finds the session at all, and whether the
// prompt hook cites it.
func TestASmallStoresAnswerReachesTheInjectedBlock(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	codexRoot := os.Getenv("DEJA_CODEX_ROOT")

	// The big store: many sessions about the ordinary work of the project,
	// each touching the words the questions below use. This is what makes the
	// fixture worth anything — a small store wins trivially when nothing else
	// in the corpus shares its vocabulary, and that is not the case #1556
	// measured.
	seedBigStore(t, claudeRoot)

	// The small store: five sessions, each settling something no claude
	// session mentions.
	// The question is not the planted words: a reader asks in their own
	// phrasing, which is what leaves room for the big store to answer first.
	planted := []struct{ id, topic, asked, answer, question string }{
		{"cx1", "battery cycle data",
			"the laptop health panel shows the wrong number of charges",
			"the macbook m2 battery cycle count is read from ioreg, not from the smc",
			"how do we read the cycle count off a macbook battery"},
		{"cx2", "referral credits",
			"a user says their invite bonus never showed up",
			"a referral credit is only granted after the invitee's first payment clears",
			"when does a referral actually credit the account"},
		{"cx3", "sandbox dns",
			"builds cannot reach the package mirror from inside the jail",
			"the sandbox resolver ignores /etc/resolv.conf and uses the vpn nameserver",
			"why does dns behave differently inside the sandbox"},
		{"cx4", "font fallback",
			"exported documents show tofu boxes for some characters",
			"the pdf exporter falls back to noto when a glyph is missing from the embedded font",
			"what happens to a missing glyph when we export a pdf"},
		{"cx5", "clock skew",
			"batches from the eu region are being refused at import",
			"the ledger rejects a batch whose clock skew exceeds ninety seconds",
			"how much clock skew will the ledger tolerate on a batch"},
	}
	// And older than everything in the big store: the small store must win on
	// what it says, not on when it was written.
	for i, p := range planted {
		at := time.Now().Add(-time.Duration(30*24+i) * time.Hour).UTC()
		writeCodexRollout(t, codexRoot, p.id, at, p.asked, p.answer)
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	found, cited := 0, 0
	for _, p := range planted {
		// The session is in the index: what follows is about ranking, not
		// about ingestion.
		if _, ok, err := index.FindByPrefix(index.DefaultDir(), p.id); err != nil || !ok {
			t.Fatalf("%s was never indexed (ok=%v err=%v)", p.id, ok, err)
		}
		res, err := index.SearchDetailed(index.DefaultDir(), query.Options{Query: p.question, All: true})
		if err != nil {
			t.Fatal(err)
		}
		rank := 0
		for i, s := range res.Sessions {
			if s.ID == p.id {
				rank = i + 1
				break
			}
		}
		if rank > 0 {
			found++
		}
		block := promptHookContext(t, p.question)
		in := strings.Contains(block, p.answer) || strings.Contains(block, p.id)
		if in {
			cited++
		}
		t.Logf("%-18s search rank %-3s cited %v", p.topic, rankLabel(rank), in)
	}
	t.Logf("small-store answers: returned by search %d/%d, cited by the hook %d/%d",
		found, len(planted), cited, len(planted))

	// The floor, not the finding: the block must not be single-harness by
	// construction. The numbers above are the finding, and they are what
	// #1556 asked for; they move with any ranking change, which is the point
	// of keeping them in a test rather than in a report.
	if cited == 0 {
		t.Errorf("no small-store answer reached the injected block, though search returned %d of %d",
			found, len(planted))
	}
}

func rankLabel(rank int) string {
	if rank == 0 {
		return "-"
	}
	return fmt.Sprint(rank)
}

// seedBigStore writes the dominant harness: 200 sessions of ordinary project
// work, each touching the vocabulary of the questions the tests ask, and each
// repeated five ways apart from a shard number — which is what a real store
// looks like and what makes near-duplicates worth collapsing.
func seedBigStore(t *testing.T, claudeRoot string) {
	t.Helper()
	noise := []string{
		"the battery of tests for the cycle detector kept flaking on ci",
		"a referral from the docs page credits the wrong campaign in analytics",
		"the resolver in the sandbox build reads the wrong config for dns",
		"the font in the pdf export is fine, the missing glyph is in the ui",
		"the clock in the ledger dashboard drifts, the skew banner is cosmetic",
	}
	for i := 0; i < 200; i++ {
		at := time.Now().Add(-time.Duration(i+2) * time.Hour).UTC().Format(time.RFC3339)
		id := fmt.Sprintf("big%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-app", id+".jsonl"), "", []string{
			line("user", id, at, fmt.Sprintf("%s (shard %d)", noise[i%len(noise)], i)),
			line("assistant", id, at, fmt.Sprintf("we raised the shard %d prefetch and it settled", i)),
		})
	}
}

func line(role, id, at, text string) string {
	b, err := json.Marshal(map[string]any{
		"type": role, "sessionId": id, "timestamp": at, "cwd": "/tmp/app",
		"message": map[string]any{"role": role, "content": text},
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func writeCodexRollout(t *testing.T, root, id string, at time.Time, question, answer string) {
	t.Helper()
	stamp := at.Format(time.RFC3339)
	path := filepath.Join(root, "sessions", "2026", "08", "30",
		"rollout-"+at.Format("2006-01-02T15-04-05")+"-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"type":"session_meta","timestamp":"` + stamp + `","payload":{"session_id":"` + id + `","cwd":"/tmp/app"}}`,
		`{"type":"message","timestamp":"` + stamp + `","payload":{"role":"user","content":` + mustJSON(t, question) + `}}`,
		`{"type":"message","timestamp":"` + stamp + `","payload":{"role":"assistant","content":` + mustJSON(t, answer) + `}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func promptHookContext(t *testing.T, prompt string) string {
	t.Helper()
	var out bytes.Buffer
	payload, err := json.Marshal(map[string]string{"prompt": prompt})
	if err != nil {
		t.Fatal(err)
	}
	if err := runHookPrompt(index.DefaultDir(), bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if out.Len() == 0 {
		return ""
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("bad hook json %q: %v", out.String(), err)
	}
	return resp.HookSpecificOutput.AdditionalContext
}

// Two sessions that say the same thing about the question, differing only in a
// number, are one answer. Spending both slots on them costs the reader the
// other answer entirely — which is what the clock-skew row of the fixture
// above was: two copies of one dashboard complaint, while the session that
// answers the question sat first in search and never appeared (#1556).
func TestTwoSessionsThatDifferByANumberAreOneAnswer(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	codexRoot := os.Getenv("DEJA_CODEX_ROOT")
	seedBigStore(t, claudeRoot)
	writeCodexRollout(t, codexRoot, "answer", time.Now().Add(-30*24*time.Hour).UTC(),
		"batches from the eu region are being refused at import",
		"the ledger rejects a batch whose clock skew exceeds ninety seconds")
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	block := promptHookContext(t, "how much clock skew will the ledger tolerate on a batch")
	if n := strings.Count(block, "the skew banner is cosmetic"); n > 1 {
		t.Errorf("the block spent %d slots on one answer:\n%s", n, block)
	}
	// And the slot the duplicate gave up goes to the session that answers the
	// question, which sits below the ranking window the forty copies filled.
	if !strings.Contains(block, "ninety seconds") {
		t.Errorf("the answer never reached the block:\n%s", block)
	}
}
