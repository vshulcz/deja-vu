package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A token that mixes scripts — `api_настройки`, `webhook-обработчик` — takes
// the ASCII path through the form rules, because isCyrToken stops at the first
// Latin rune. Nothing pins what the reader gets: the reach comes from the
// parts the indexer splits out and from the close tier, not from the Russian
// endings, and both are a rung away from the token itself.
func TestAMixedScriptTokenIsStillReachable(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for sid, text := range map[string]string{
		"s1": "поправил api_настройки воркера",
		"s2": "чинил webhook-обработчик в проде",
		"s3": "смотрел k8sнастройки кластера",
	} {
		rec := claudeRecord(t, map[string]any{
			"type": "user", "sessionId": sid, "cwd": "/tmp/app",
			"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": text},
		})
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ query, want string }{
		// The store's own spelling.
		{"api_настройки", "s1"},
		{"webhook-обработчик", "s2"},
		{"k8sнастройки", "s3"},
		// And another case of the Russian half, which is how someone would
		// actually type it a month later.
		{"api_настройка", "s1"},
		{"webhook-обработчику", "s2"},
		{"k8sнастройка", "s3"},
		// The Russian half on its own reaches the token it is part of.
		{"настройками воркера", "s1"},
	} {
		out, _ := captureRun(t, "search", tc.query)
		if !strings.Contains(out, tc.want) {
			t.Errorf("%q does not reach %s:\n%s", tc.query, tc.want, out)
		}
	}
	// And it is not reaching everything: a Russian word the store does not
	// hold finds nothing, mixed token or not.
	if out, _ := captureRun(t, "search", "миграции кластера"); strings.Contains(out, "s1") {
		t.Errorf("a word no session says matched anyway:\n%s", out)
	}
}
