package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// helpLineFor is the usage line that starts with this command name. The check
// has to be on that line: `--since` appears in the search-flags section at the
// bottom of help, so a search anywhere in the text passes for every command.
func helpLineFor(help, command string) string {
	re := regexp.MustCompile(`(?m)^\s+deja ` + regexp.QuoteMeta(command) + `\b.*(?:\n\s{6,}\[.*)*`)
	return re.FindString(help)
}

// A flag the command takes and help never names is a flag nobody finds.
// `deja stats` accepted --project, --harness, --since and --role — the same
// four the search form documents — and its usage line listed none of them, so
// the only way to learn they existed was to read the parser (#753 is the same
// omission one level up, for commands).
func TestHelpNamesTheFiltersACommandAccepts(t *testing.T) {
	tmp := hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "work", "one.jsonl"), "flagcov", []string{
		`{"type":"user","sessionId":"flagcov","timestamp":"2026-08-20T10:00:00Z",` +
			`"message":{"role":"user","content":"the migration ran twice"}}`,
	})
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", false, nil); err != nil {
		t.Fatal(err)
	}
	help := captureHelp(t)

	// Read-only query surfaces only. A command that writes, installs or syncs
	// has no business being run by a coverage check.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"stats", nil},
		{"friction", nil},
		{"fix", []string{"pq: connection refused"}},
		{"how", []string{"go test"}},
		{"files", []string{"migration"}},
		{"last", []string{"3"}},
		{"blame", []string{"main.go"}},
		{"log", nil},
	} {
		line := helpLineFor(help, c.name)
		if line == "" {
			t.Errorf("deja help has no usage line for %q", c.name)
			continue
		}
		for _, f := range []struct{ flag, value string }{
			{"--harness", "claude"},
			{"--project", "work"},
			{"--since", "30d"},
			{"--role", "user"},
			{"--limit", "5"},
		} {
			args := append(append([]string{c.name}, c.args...), f.flag, f.value)
			_, err := captureRun(t, args...)
			// Every refusal deja writes for a flag it does not take. A command
			// that took the flag either succeeded or failed for another reason.
			if err != nil {
				msg := err.Error()
				if strings.Contains(msg, "unknown flag") || strings.Contains(msg, "takes no") ||
					strings.Contains(msg, "and nothing else") {
					continue
				}
			}
			if !strings.Contains(line, f.flag) {
				t.Errorf("deja %s takes %s and its usage line never names it:\n%s", c.name, f.flag, line)
			}
		}
	}
}
