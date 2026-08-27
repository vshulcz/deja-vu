package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A word glued to a Latin prefix has no part of its own in the index — nothing
// splits "k8sнастройки" — so the substring rung is the only road to it, and
// that rung looked for the query token as typed. The store's own case reached
// it and no other did, which is the guess deja exists to spare the reader
// (#2145).
func TestAGluedWordIsReachableInAnotherCase(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for sid, text := range map[string]string{
		"s1": "смотрел k8sнастройки кластера",
		"s2": "чинил v2обработчик вебхука",
		"s3": "ничего общего: графики и дашборды",
		// The four-rune floor's case: "тест" sits at the end of "манифест",
		// and the two words have nothing to do with each other.
		"s4": "гонял манифест кластера через ci",
		"s5": "чинил автотест воркера ночью",
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
	// The premise: the store's own spelling reaches it.
	for _, tc := range []struct{ query, want string }{
		{"настройки кластера", "s1"},
		{"обработчик вебхука", "s2"},
	} {
		if out, _ := captureRun(t, "search", tc.query); !strings.Contains(out, tc.want) {
			t.Fatalf("%q does not reach %s, so this measures nothing:\n%s", tc.query, tc.want, out)
		}
	}
	// The case nobody types a month later.
	for _, tc := range []struct{ query, want string }{
		{"настройка кластера", "s1"},
		{"настройками кластера", "s1"},
		{"обработчику вебхука", "s2"},
	} {
		out, _ := captureRun(t, "search", tc.query)
		if !strings.Contains(out, tc.want) {
			t.Errorf("%q does not reach %s, which says the word in another case:\n%s", tc.query, tc.want, out)
		}
	}
	// And the rung has not become a wildcard. A word the store never says
	// finds nothing in any case, and neither does one whose short form sits
	// at the end of an unrelated token — "тест" inside "манифест", which is
	// what the four-rune floor is there to refuse.
	for _, q := range []string{"миграции кластера", "миграция кластера", "тесты кластера", "тест кластера"} {
		out, _ := captureRun(t, "search", q)
		for _, sid := range []string{"s1", "s2", "s3", "s4"} {
			if strings.Contains(out, sid) {
				t.Errorf("%q matched %s, which never says it:\n%s", q, sid, out)
			}
		}
	}
	// What it does admit, and on purpose: a word really inside a longer one.
	// "автотест" is an autotest, and a reader asking about tests wants it —
	// the same trade the substring rung makes when "code" reaches "opencode".
	if out, _ := captureRun(t, "search", "тесты воркера"); !strings.Contains(out, "s5") {
		t.Errorf("\"тесты воркера\" does not reach the autotest session:\n%s", out)
	}
	// The queries that do reach must not drag the unrelated sessions with them.
	for _, q := range []string{"настройка кластера", "обработчику вебхука"} {
		out, _ := captureRun(t, "search", q)
		for _, sid := range []string{"s3", "s4"} {
			if strings.Contains(out, sid) {
				t.Errorf("%q reached %s, which shares nothing with it:\n%s", q, sid, out)
			}
		}
	}
}
