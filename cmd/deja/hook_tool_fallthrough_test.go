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

// seedFallthroughStore writes a store where one command has more than one thing
// the walk can say about it: a program this machine does not have, and a
// failure it hit here.
func seedFallthroughStore(t *testing.T) {
	t.Helper()
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
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"),
			[]byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ran := func(sid, when string) string {
		return `{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + when +
			`","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"quibtimer 30 make migrate-orders"}}]}}`
	}
	// Sessions that ran into the same absent program, which is what
	// missingProgramLine asks for before it will say anything.
	for k := 0; k < 3; k++ {
		sid := fmt.Sprintf("m%d", k)
		write(sid, []string{
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(800-10*k) + `","cwd":"/work/app","message":{"role":"user","content":"wrap the orders migration in a time limit"}}`,
			ran(sid, at(799-10*k)),
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(798-10*k) + `","cwd":"/work/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"command not found: quibtimer"}]}}`,
		})
	}
	write("dec", []string{
		`{"type":"user","sessionId":"dec","timestamp":"` + at(401) + `","cwd":"/work/app","message":{"role":"user","content":"should we keep running the orders migration against production directly?"}}`,
		ran("dec", at(400)),
		`{"type":"assistant","sessionId":"dec","timestamp":"` + at(399) + `","cwd":"/work/app","message":{"role":"assistant","content":"no: run the orders migration against the replica first"}}`,
	})
	for k := 0; k < 5; k++ {
		sid := fmt.Sprintf("f%d", k)
		write(sid, []string{
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(200-20*k) + `","cwd":"/work/app","message":{"role":"user","content":"run the orders migration again"}}`,
			ran(sid, at(199-20*k)),
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at(198-20*k) + `","cwd":"/work/app","message":{"role":"assistant","content":"ran the migration"}}`,
		})
	}
	// Sessions about other work, so the command's words carry weight: in a
	// store where every session says the same thing no term is informative.
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
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
}

// The missing-program sentence is the more certain fact and goes first. Once it
// had been said, the next action got nothing at all: the caller hashed the one
// line it was handed, found it already injected, and returned without asking
// the next producer.
//
// Measured on a real store, that cost 34 decisions behind 51 sentences about
// one absent program, and left the hook saying three lines in ninety-one
// actions (#3603).
func TestTheToolHookFallsThroughToTheLineBehindARepeatedFact(t *testing.T) {
	seedFallthroughStore(t)
	dir := index.DefaultDir()
	cmd := "quibtimer 30 make migrate-orders"

	// Nothing said yet: the most certain fact wins, as it should.
	first := commandHookLine(dir, "/work/app", cmd)
	if !strings.Contains(first, "quibtimer is not on this machine") {
		t.Fatalf("the missing program is not the first thing said:\n  %s", first)
	}

	// Said once. The same action again must reach what is behind it rather than
	// going quiet for the rest of the session.
	said := map[string]bool{dedupeFact(first): true}
	saidAlready := func(line string) bool { return said[dedupeFact(line)] }
	second := commandHookLineSkipping(dir, "/work/app", cmd, saidAlready)
	if second == "" {
		t.Fatal("the hook went silent once the missing-program sentence had been said")
	}
	if dedupeFact(second) == dedupeFact(first) {
		t.Errorf("the same fact came back a second time:\n  %s", second)
	}

	// And with both of the cheap facts said, the walk reaches the decision
	// behind them. The first fix stopped here — the two producers past this
	// point ranked and then loaded whole sessions, which is the cost #3001
	// filed — and since #3605 the decision is a table lookup, so the only
	// thing the bound still bought was silence.
	said[dedupeFact(second)] = true
	third := commandHookLineSkipping(dir, "/work/app", cmd, saidAlready)
	if third == "" {
		t.Fatal("the hook went silent with a decision still unsaid behind two repeated facts")
	}
	if !strings.Contains(third, "make migrate-orders") && !strings.Contains(third, "last time") {
		t.Errorf("the third line is neither the command's history nor its decision:\n  %s", third)
	}
	for _, earlier := range []string{first, second} {
		if dedupeFact(third) == dedupeFact(earlier) {
			t.Errorf("a fact already given came back:\n  %s", third)
		}
	}

	// And when everything it can say has been said, silence is the answer.
	all := func(string) bool { return true }
	if line := commandHookLineSkipping(dir, "/work/app", cmd, all); line != "" {
		t.Errorf("a walk with nothing new to say still said something:\n  %s", line)
	}
}
