package prompt

import (
	"strings"
	"testing"
)

func has(terms []string, want string) bool {
	for _, t := range terms {
		if t == want {
			return true
		}
	}
	return false
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
