package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The line before a command has the same shape as the line before an edit, and
// #2496 taught only the second one to carry a promoted decision. Here the
// decision is about that exact command and the line said only how often the
// machine had run it (#2516).
func TestTheCommandLineCarriesThePromotedDecision(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := func(m int) string { return now.Add(-time.Duration(m) * time.Minute).Format(time.RFC3339) }
	write := func(sid string, lines []string) {
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bash := func(sid, when string) string {
		return `{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + when + `","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"make migrate-orders"}}]}}`
	}
	write("dec", []string{
		`{"type":"user","sessionId":"dec","timestamp":"` + at(401) + `","cwd":"/work/app","message":{"role":"user","content":"should we keep running the orders migration against production directly?"}}`,
		bash("dec", at(400)),
		`{"type":"assistant","sessionId":"dec","timestamp":"` + at(399) + `","cwd":"/work/app","message":{"role":"assistant","content":"no: run the orders migration against the replica first"}}`,
	})
	for k := 0; k < 5; k++ {
		sid := fmt.Sprintf("f%d", k)
		write(sid, []string{
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(200-20*k) + `","cwd":"/work/app","message":{"role":"user","content":"run the orders migration again"}}`,
			bash(sid, at(199-20*k)),
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at(198-20*k) + `","cwd":"/work/app","message":{"role":"assistant","content":"ran the migration"}}`,
		})
	}
	// Sessions about other work, so the command's words carry weight: in a
	// store where every session says the same thing no term is informative and
	// the ranking matches nothing — the lesson of 430 and 437.
	topics := []string{"the sidebar layout", "the invoice pdf renderer", "the webpack config",
		"the login throttle", "the avatar uploader", "the search autocomplete", "the cron scheduler",
		"the email templates", "the feature flags", "the toast component", "the graphql schema",
		"the docker base image", "the css variables", "the storybook snapshots", "the i18n strings"}
	for i, top := range topics {
		sid := fmt.Sprintf("t%d", i)
		write(sid, []string{
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(900-10*i) + `","cwd":"/work/app","message":{"role":"user","content":"work on ` + top + ` today"}}`,
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at(899-10*i) + `","cwd":"/work/app","message":{"role":"assistant","content":"changed ` + top + ` and the tests pass"}}`,
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "dec", "--state", "accepted",
		"--note", "run the orders migration against the replica first; production needs the pool drained"); err != nil {
		t.Fatal(err)
	}

	line := commandHookLine(dir, "/work/app", "make migrate-orders")
	if line == "" {
		t.Fatal("the fixture produced no line at all")
	}
	if !strings.Contains(line, "replica first") {
		t.Errorf("the decision about this command is not in the line:\n  %s", line)
	}
}
