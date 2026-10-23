package prompt

import (
	"slices"
	"strings"
	"testing"
)

func has(terms []string, want string) bool {
	return slices.Contains(terms, want)
}

// Six terms is the whole budget, and a paste above the question spends it on
// file names: the question never reached the query and auto-recall answered
// nothing (#3183).
func TestTheQuestionUnderAPasteIsWhatIsSearched(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		want   string
	}{
		{
			"repo listing above the question",
			"cmd/deja main.go install.go index.go search.go docs/registry Makefile go.mod\nwhy does the zebraquux fetcher time out?",
			"zebraquux",
		},
		{
			"file mentions above the question",
			"@zebraquux/fetch.go @zebraquux/client.go @zebraquux/retry.go @zebraquux/dial.go @zebraquux/config.go\nwhat did we decide about the quokkabloom retry budget?",
			"quokkabloom",
		},
		{
			"no question mark: the ask is the last line",
			"panic: index out of range [7]\n\tgoroutine 41 [running]:\n\tretry.dialLoop(0xc000123456)\nfix the quokkabloom dial timeout",
			"quokkabloom",
		},
		{
			"a paste under the question still works",
			"why does the zebraquux fetcher time out?\ncmd/deja main.go install.go index.go search.go docs/registry Makefile",
			"zebraquux",
		},
		{
			"russian question under a paste",
			"cmd/deja main.go install.go index.go search.go docs/registry Makefile go.mod\nпочему хендлер квоккаблум падает по таймауту?",
			"квоккаблум",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Terms(c.prompt)
			if !has(got, c.want) {
				t.Errorf("terms %q carry nothing from the ask (%q)", got, c.want)
			}
		})
	}
}

// A one-line prompt has no ask to lift out: it reads exactly as before.
func TestASingleLinePromptIsNotSplit(t *testing.T) {
	for _, p := range []string{
		"why does the zebraquux fetcher time out?",
		"почему квоккаблум падает?",
		"",
	} {
		ask, rest := splitAsk(p)
		if ask != "" || rest != p {
			t.Errorf("splitAsk(%q) = (%q, %q), want the whole prompt as the rest", p, ask, rest)
		}
	}
}

// A question mark inside a pasted line is not the ask. The last line that ends
// in one is, and a line of punctuation is nobody's question.
func TestWhichLineCountsAsTheAsk(t *testing.T) {
	ask, rest := splitAsk("is this thing on?\nhere is the paste\nwhy does the zebraquux fetcher time out?\ntrailing note")
	if ask != "why does the zebraquux fetcher time out?" {
		t.Errorf("ask = %q, want the last question", ask)
	}
	if len(strings.Split(rest, "\n")) != 3 {
		t.Errorf("the rest lost or gained a line: %q", rest)
	}
	if ask, _ := splitAsk("here is the paste\n?"); ask == "?" {
		t.Error("a lone question mark was taken for the ask")
	}
	if ask, _ := splitAsk("fix the quokkabloom dial timeout\n\n"); ask != "fix the quokkabloom dial timeout" {
		t.Errorf("trailing blank lines hid the ask: %q", ask)
	}
}

// On one line an instruction in front of the question spends the six terms
// before the question is reached, and the hook said nothing (#4751). The
// clause holding the question leads, whatever boundary sets it off.
func TestTheQuestionBehindAnInstructionOnOneLineIsWhatIsSearched(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		want   string
	}{
		{
			"colon",
			"Without calling any MCP tool and without reading any files: what batch flush threshold did we settle on for queue.go?",
			"flush",
		},
		{
			"full stop",
			"Do not open the editor, terminal or browser tabs, answer from memory only. why does the zebraquux fetcher time out?",
			"zebraquux",
		},
		{
			"dash",
			"Before touching the staging rollout dashboard panels and alerts — did we add jitter to the quokkabloom backoff?",
			"quokkabloom",
		},
		{
			"no question mark",
			"Without calling any MCP tool and without reading any files: what jitter constant did the quokkabloom backoff use",
			"quokkabloom",
		},
		{
			"imperative",
			"Do not open the editor, terminal or browser tabs, answer from memory only. start the quokkabloom retry now",
			"quokkabloom",
		},
		{
			"russian",
			"Не вызывай инструменты, не открывай файлы, отвечай только по памяти: напомни, что мы решали про квоккаблум",
			"квоккаблум",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Terms(c.prompt)
			if !has(got, c.want) {
				t.Errorf("terms %q carry nothing from the question (%q)", got, c.want)
			}
		})
	}
}

// The control: the lead-in alone fills the budget, so the cases above are not
// passing on spare room.
func TestTheLeadInAloneFillsTheBudget(t *testing.T) {
	for _, p := range []string{
		"Without calling any MCP tool and without reading any files:",
		"Do not open the editor, terminal or browser tabs, answer from memory only.",
	} {
		if got := Terms(p); len(got) < 6 {
			t.Errorf("Terms(%q) = %q, want the full six", p, got)
		}
	}
}

// Words joined by a dot or colon with no space are one token, not a boundary,
// and a line of one clause is not split. Among several, the last question is
// the ask, and with no question the last clause is, as with lines.
func TestWhichClauseCountsAsTheAsk(t *testing.T) {
	for _, p := range []string{
		"why does main.go fail at 10:30 on http://127.0.0.1:8080?",
		"?! ...",
	} {
		if ask, rest := splitAsk(p); ask != "" || rest != p {
			t.Errorf("splitAsk(%q) = (%q, %q), want no split", p, ask, rest)
		}
	}
	ask, rest := splitAsk("is this thing on? no tools: why does the zebraquux fetcher time out? answer briefly")
	if ask != "why does the zebraquux fetcher time out?" {
		t.Errorf("ask = %q, want the last question", ask)
	}
	if rest != "is this thing on? no tools: answer briefly" {
		t.Errorf("rest = %q, want every other clause kept", rest)
	}
	if first, _ := splitAsk("Without reading any files: start the quokkabloom retry now"); first != "start the quokkabloom retry now" {
		t.Errorf("ask = %q, want the clause after the instruction", first)
	}
	ask, rest = splitAsk("cmd/deja main.go\nno tools: why does the zebraquux fetcher time out?\ntrailing note")
	if ask != "why does the zebraquux fetcher time out?" || rest != "cmd/deja main.go\nno tools:\ntrailing note" {
		t.Errorf("splitAsk on a pasted prompt = (%q, %q)", ask, rest)
	}
}

// A command quoted in a question is one thing being asked about: a colon or a
// full stop inside backticks or double quotes is not a clause boundary.
func TestAQuotedCommandInAQuestionIsNotCut(t *testing.T) {
	p := "why does `git commit -m \"fix: x. y\"` fail on the zebraquux hook?"
	if ask, rest := splitAsk(p); ask != "" || rest != p {
		t.Errorf("splitAsk(%q) = (%q, %q), want no split", p, ask, rest)
	}
	got, want := Terms(p), []string{"git", "commit", "fail", "zebraquux", "hook"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("Terms = %q, want %q", got, want)
	}
}
