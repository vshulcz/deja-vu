package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var retentionNow = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func daysAgo(n int) time.Time { return retentionNow.Add(-time.Duration(n) * 24 * time.Hour) }

// claudeStand lays down one transcript per age under an isolated Claude root
// and writes settings.json beside it when settings is not "".
func claudeStand(t *testing.T, settings string, ages ...int) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "projects")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CLAUDE_MANAGED_SETTINGS", filepath.Join(dir, "no-managed.json"))
	proj := filepath.Join(root, "-tmp-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, age := range ages {
		p := filepath.Join(proj, fmt.Sprintf("s%d.jsonl", i))
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, daysAgo(age), daysAgo(age)); err != nil {
			t.Fatal(err)
		}
	}
	// A subagent's transcript goes with its session and is not counted.
	sub := filepath.Join(proj, "s0", "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "a.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(filepath.Join(sub, "a.jsonl"), daysAgo(29), daysAgo(29))
	if settings != "" {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClaudeRetentionDueReadsCleanupPeriodDays(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings string
		ages     []int
		days, n  int
		ok       bool
	}{
		{"default 30 days, missing config", "", []int{25, 10, 31}, 30, 2, true},
		{"settings without the key", `{"model":"x"}`, []int{25, 10}, 30, 1, true},
		{"custom 14 days", `{"cleanupPeriodDays": 14}`, []int{8, 5, 40}, 14, 2, true},
		{"a huge period disables it in effect", `{"cleanupPeriodDays": 99999}`, []int{400}, 99999, 0, true},
		{"a value Claude Code rejects", `{"cleanupPeriodDays": 0}`, []int{25}, 0, 0, false},
		{"unreadable settings", `{not json`, []int{25}, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claudeStand(t, tc.settings, tc.ages...)
			d, ok := claudeRetentionDue(retentionNow)
			if ok != tc.ok || d.days != tc.days || d.n != tc.n {
				t.Fatalf("got days=%d n=%d ok=%v, want days=%d n=%d ok=%v", d.days, d.n, ok, tc.days, tc.n, tc.ok)
			}
		})
	}
}

func TestClaudeManagedSettingsWin(t *testing.T) {
	claudeStand(t, `{"cleanupPeriodDays": 99999}`, 25)
	managed := filepath.Join(t.TempDir(), "managed.json")
	if err := os.WriteFile(managed, []byte(`{"cleanupPeriodDays": 30}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_MANAGED_SETTINGS", managed)
	if d, ok := claudeRetentionDue(retentionNow); !ok || d.days != 30 || d.n != 1 {
		t.Fatalf("got days=%d n=%d ok=%v, want the managed 30 days and 1 due", d.days, d.n, ok)
	}
}

func geminiStand(t *testing.T, settings string, ages ...int) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GEMINI_CLI_HOME", home)
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(home, ".gemini"))
	chats := filepath.Join(home, ".gemini", "tmp", "abc", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, age := range ages {
		at := daysAgo(age).Format(time.RFC3339)
		doc := fmt.Sprintf(`{"sessionId":"s%d","startTime":%q,"lastUpdated":%q,"messages":[`+
			`{"id":"m1","timestamp":%q,"type":"user","content":"hello"},`+
			`{"id":"m2","timestamp":%q,"type":"gemini","content":"hi"}]}`, i, at, at, at, at)
		p := filepath.Join(chats, fmt.Sprintf("session-%d.json", i))
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		// The file's mtime says today; Gemini ages by lastUpdated.
		_ = os.Chtimes(p, retentionNow, retentionNow)
	}
	if settings != "" {
		if err := os.WriteFile(filepath.Join(home, ".gemini", "settings.json"), []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGeminiRetentionDueReadsSessionRetention(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings string
		ages     []int
		days, n  int
		ok       bool
	}{
		{"default 30 days, missing config", "", []int{25, 10}, 30, 1, true},
		{"maxAge 2w", `{"general":{"sessionRetention":{"maxAge":"2w"}}}`, []int{10, 3}, 14, 1, true},
		{"disabled", `{"general":{"sessionRetention":{"enabled":false}}}`, []int{25}, 0, 0, false},
		{"a maxAge format deja does not know", `{"general":{"sessionRetention":{"maxAge":"1 month"}}}`, []int{25}, 0, 0, false},
		{"comments in settings", "{\n// mine\n\"general\":{\"sessionRetention\":{\"maxAge\":\"10d\"}}}", []int{5}, 10, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			geminiStand(t, tc.settings, tc.ages...)
			d, ok := geminiRetentionDue(retentionNow)
			if ok != tc.ok || (ok && (d.days != tc.days || d.n != tc.n)) {
				t.Fatalf("got days=%d n=%d ok=%v, want days=%d n=%d ok=%v", d.days, d.n, ok, tc.days, tc.n, tc.ok)
			}
		})
	}
}

func hermesStand(t *testing.T, config string) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	home := t.TempDir()
	t.Setenv("DEJA_HERMES_HOME", home)
	db := filepath.Join(home, "state.db")
	t.Setenv("DEJA_HERMES_DB", db)
	ts := func(days int) int64 { return daysAgo(days).Unix() }
	// e85: ended, last message 85 days ago: due under the default 90.
	// e60: ended, quiet for 60 days: not due. open: never ended: never pruned.
	// e85late: ended 85 days ago but a message 10 days ago keeps it alive.
	schema := fmt.Sprintf(`
CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT NOT NULL, started_at REAL NOT NULL, ended_at REAL);
CREATE TABLE messages (id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content TEXT, timestamp REAL);
INSERT INTO sessions VALUES ('e85','cli',%[1]d,%[1]d),('e60','cli',%[2]d,%[2]d),('open','cli',%[1]d,NULL),('e85late','cli',%[1]d,%[1]d);
INSERT INTO messages (session_id,role,content,timestamp) VALUES ('e85','user','x',%[1]d),('e85late','user','y',%[3]d);`,
		ts(85), ts(60), ts(10))
	if out, err := exec.Command("sqlite3", db, schema).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	if config != "" {
		if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHermesRetentionDueReadsAutoPrune(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  string
		days, n int
		ok      bool
	}{
		{"default 90 days, missing config", "", 90, 1, true},
		{"retention_days 62", "sessions:\n  retention_days: 62\n", 62, 2, true},
		{"auto_prune off", "sessions:\n  auto_prune: false\n", 0, 0, false},
		{"other settings only", "model: x\nsessions:\n  min_interval_hours: 24\n", 90, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermesStand(t, tc.config)
			d, ok := hermesRetentionDue(retentionNow)
			if ok != tc.ok || d.days != tc.days || d.n != tc.n {
				t.Fatalf("got days=%d n=%d ok=%v, want days=%d n=%d ok=%v", d.days, d.n, ok, tc.days, tc.n, tc.ok)
			}
		})
	}
}

// Nothing due, nothing said: a line that reads "0 sessions" every run is
// wallpaper.
func TestRetentionDueSaysNothingWhenNothingIsDue(t *testing.T) {
	claudeStand(t, "", 10, 3)
	geminiStand(t, "", 2)
	t.Setenv("DEJA_HERMES_DB", filepath.Join(t.TempDir(), "none.db"))
	var b bytes.Buffer
	printRetentionDue(&b, retentionNow)
	doctorRetentionDue(&b, retentionNow)
	if b.Len() != 0 {
		t.Fatalf("expected no output, got:\n%s", b.String())
	}
}

func TestRetentionDueLine(t *testing.T) {
	claudeStand(t, "", 25, 10)
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(t.TempDir(), "none"))
	t.Setenv("GEMINI_CLI_HOME", t.TempDir())
	t.Setenv("DEJA_HERMES_DB", filepath.Join(t.TempDir(), "none.db"))
	var b bytes.Buffer
	printRetentionDue(&b, retentionNow)
	want := "deja: claude: 1 session reaches Claude Code's 30-day cleanup in the next 7 days; deja keeps it searchable\n"
	if b.String() != want {
		t.Fatalf("got %q\nwant %q", b.String(), want)
	}
	b.Reset()
	doctorRetentionDue(&b, retentionNow)
	if !strings.HasPrefix(b.String(), "  expiring claude: 1 session") {
		t.Fatalf("doctor row: %q", b.String())
	}
}
