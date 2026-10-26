package prompt

import (
	"slices"
	"testing"
)

// `opencode run "<question>"` hands the hook the question in quotes, with the
// quotes inside it escaped. Read as one quoted clause, an instruction in front
// of the question spent the six terms and the recall block was empty.
func TestUnwrapTakesOffTheQuotesAHostPutAroundAPrompt(t *testing.T) {
	for in, want := range map[string]string{
		`"how did we install vllm?"`:  `how did we install vllm?`,
		` "fix \"foo\" bar now" `:     `fix "foo" bar now`,
		"\"multi\nline question\"":    "multi\nline question",
		`how did we install vllm?`:    `how did we install vllm?`,
		`"foo" and "bar"`:             `"foo" and "bar"`,
		`"two words" third`:           `"two words" third`,
		`"ends escaped \"`:            `"ends escaped \"`,
		`""`:                          `""`,
		`"`:                           `"`,
		`"path C:\\dir\\ is it here"`: `path C:\\dir\\ is it here`,
	} {
		if got := Unwrap(in); got != want {
			t.Errorf("Unwrap(%q) = %q, want %q", in, got, want)
		}
	}
	ask := "without calling any MCP tool and without reading any project files or running commands: how did we install vllm?"
	if got, want := Terms(Unwrap(`"`+ask+`"`)), Terms(ask); !slices.Equal(got, want) {
		t.Errorf("quoted prompt terms %q, want the unquoted %q", got, want)
	}
}
