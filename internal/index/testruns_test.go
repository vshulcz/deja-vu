package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Every shape here was taken from a real output record on a 2,700-session
// store, including the three that used to be read the wrong way round.
func TestARunnersVerdictIsReadFromItsOwnLine(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want TestVerdict
	}{
		{"go pass", "ok  \tgithub.com/me/app/internal/fetch\t1.2s", TestPassed},
		{"go cached", "ok  \tgithub.com/me/app\t(cached)", TestPassed},
		{"go fail", "--- FAIL: TestRetry (0.01s)\nFAIL\tgithub.com/me/app\t0.9s\nFAIL", TestFailed},
		{"build failed", "FAIL\tgithub.com/me/app [build failed]\nFAIL", TestFailed},
		{"vet", "# github.com/me/app\nvet: internal/x/x.go:7:2: \"fmt\" imported and not used", TestFailed},
		{"compile error under an echo", "ok\n# github.com/me/app\ninternal/x/x.go:12:3: undefined: q", TestFailed},
		{"no test files", "?   \tgithub.com/me/app/cmd\t[no test files]", TestPassed},
		{"pytest pass", "===== 41 passed in 2.10s =====", TestPassed},
		{"pytest fail", "===== 2 failed, 39 passed in 2.40s =====", TestFailed},
		{"jest fail", "Tests:       3 failed, 18 passed, 21 total", TestFailed},
		{"cargo fail", "error[E0308]: mismatched types", TestFailed},
		{"cargo pass", "test result: ok. 12 passed; 0 failed", TestPassed},
		{"wrapper", "Running: go test -json ./...\nGo test: 756 passed in 5 packages", TestPassed},
		// The agent filtered its own run and printed a sentinel instead.
		{"filtered", "DONE", TestNoVerdict},
		{"silent build", "", TestNoVerdict},
		// A git push says `ok main` and a shell audit says `ok  friends.sh`.
		// Neither is a test run, and both were read as one.
		{"git push", "To https://github.com/me/app.git\n   c10e92f..3830e85  main -> main\nok main", TestNoVerdict},
		{"script audit", "ok        friends.sh\nNO FALLBACK status-all.sh", TestNoVerdict},
		{"git apply", "ok\nerror: No valid patches in input (allow with \"--allow-empty\")", TestNoVerdict},
	}
	for _, c := range cases {
		if got := TestRunVerdict(c.out); got != c.want {
			t.Errorf("%s: read %q, want %q", c.name, got, c.want)
		}
	}
}

// The store keeps one command record for a command line a session ran twice,
// and both outputs. Counting only the first output made every repeated
// `go test ./...` a single run — 6,000 of 18,645 on a real store (#539).
func TestASessionRepeatingOneCommandIsCountedTwice(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	at := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	msg := func(role, text string, min int) model.Message {
		return model.Message{Role: role, Text: text, Time: at.Add(time.Duration(min) * time.Minute)}
	}
	writeStore(t, dir, []model.Session{{
		ID: "s1", Harness: "claude", Project: "work/app", Path: "/tmp/s1.jsonl", Updated: at,
		Messages: []model.Message{
			msg(roleCommand, "$ go test ./internal/fetch -count=1", 0),
			msg(roleToolOutput, "--- FAIL: TestRetryBudget (0.01s)\nFAIL\tgithub.com/me/app\t0.9s\nFAIL", 1),
			msg(roleToolOutput, "ok  \tgithub.com/me/app/internal/fetch\t1.1s", 2),
			// Filtered: the verdict never reached the transcript.
			msg(roleCommand, `$ go test ./... 2>&1 | grep -E "^FAIL"; echo DONE`, 3),
			msg(roleToolOutput, "DONE", 4),
		},
	}})
	h, err := ScanTestHistory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h.Runs != 3 || h.Failed != 1 || h.Passed != 1 || h.NoVerdict != 1 {
		t.Fatalf("want 3 runs one of each band, got %+v", h)
	}
	if h.Tools["go test"] != 2 {
		t.Errorf("the runners behind the series are wrong: %v", h.Tools)
	}
	if h.Days != 1 || len(h.Weeks) != 1 || h.Weeks[0].Runs != 3 {
		t.Errorf("the series does not hold one week of three runs: %+v", h.Weeks)
	}
	if len(h.Repeats) != 0 {
		t.Errorf("a test that failed on one day was called a repeat: %+v", h.Repeats)
	}
}

// Mining the named failures from every output, not only from the ones that
// answer a command deja matched: `make check` and a shell script both print
// `--- FAIL:` lines, and the test named in them is the same test.
func TestANamedFailureIsMinedFromAnyOutput(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	day := func(d int) time.Time { return time.Date(2026, 3, d, 10, 0, 0, 0, time.UTC) }
	out := func(name string, d int) model.Message {
		return model.Message{Role: roleToolOutput, Time: day(d),
			Text: "--- FAIL: " + name + " (0.02s)\nFAIL\tgithub.com/me/app\t0.4s\nFAIL"}
	}
	writeStore(t, dir, []model.Session{{
		ID: "s2", Harness: "claude", Project: "work/app", Path: "/tmp/s2.jsonl", Updated: day(6),
		Messages: []model.Message{
			out("TestKeepsComingBack", 4),
			out("TestKeepsComingBack", 6),
			out("TestFixedThatAfternoon", 4),
			out("TestFixedThatAfternoon", 4),
		},
	}})
	h, err := ScanTestHistory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h.Runs != 0 {
		t.Fatalf("outputs with no command before them were counted as runs: %+v", h)
	}
	if len(h.Repeats) != 1 {
		t.Fatalf("want one repeat-failing test, got %+v", h.Repeats)
	}
	r := h.Repeats[0]
	if r.Name != "TestKeepsComingBack" || r.Days != 2 || r.Failures != 2 || r.Project != "work/app" {
		t.Errorf("wrong row: %+v", r)
	}
	if !r.LastFail.Equal(day(6)) {
		t.Errorf("the newest failure is %s, want %s", r.LastFail, day(6))
	}
}
