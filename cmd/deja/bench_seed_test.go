package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// benchCorpusHash runs one bench subcommand and returns the corpus it measured.
func benchCorpusHash(t *testing.T, sub string, args ...string) string {
	t.Helper()
	out, err := captureRun(t, append([]string{"bench", sub}, args...)...)
	if err != nil {
		t.Fatalf("bench %s %v: %v", sub, args, err)
	}
	var report struct {
		CorpusHash string `json:"corpus_hash"`
		Seed       int64  `json:"seed"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("bench %s output is not JSON: %v\n%s", sub, err, out)
	}
	return report.CorpusHash
}

// `deja help` documents `bench recall|context|prompt|block [--json] [--seed n]`,
// and two of the first three did not honor it: recall exited 1 on --seed,
// prompt took the flag and measured seed 1 anyway. A benchmark whose seed does
// nothing cannot be re-run on another corpus, which is the point of publishing
// one. Every subcommand with a seeded corpus belongs in this loop.
func TestBenchSeedChangesTheCorpusOnEverySubcommand(t *testing.T) {
	hermeticEnv(t)
	for _, sub := range []string{"recall", "context", "prompt", "block"} {
		one := benchCorpusHash(t, sub, "--seed", "1", "--json")
		other := benchCorpusHash(t, sub, "--seed", "7", "--json")
		if one == "" || other == "" {
			t.Fatalf("bench %s reported no corpus hash", sub)
		}
		if one == other {
			t.Errorf("bench %s measured the same corpus %s for seed 1 and seed 7 — --seed is ignored", sub, one)
		}
		// Same seed, same corpus: the flag has to be the only thing deciding.
		if again := benchCorpusHash(t, sub, "--seed", "7", "--json"); again != other {
			t.Errorf("bench %s is not reproducible at seed 7: %s then %s", sub, other, again)
		}
	}
}

// The usage line names the flags, so it has to name --seed too.
func TestBenchUsageNamesTheSeedFlag(t *testing.T) {
	hermeticEnv(t)
	_, err := captureRun(t, "bench", "recall", "--nope")
	if err == nil {
		t.Fatal("an unknown bench flag was accepted")
	}
	if !strings.Contains(err.Error(), "--seed") {
		t.Errorf("usage does not mention --seed: %v", err)
	}
}
